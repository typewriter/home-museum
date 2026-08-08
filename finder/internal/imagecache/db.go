package imagecache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// 状態遷移は spec_image_cache.md §2。
const (
	StateQueued   = "queued"
	StateFetching = "fetching"
	StateReady    = "ready"
	StateFailed   = "failed" // 一時的。指数バックオフで再試行
	StateGone     = "gone"   // 恒久的。二度と取りに行かない
)

// maxAttempts を超えたら failed → gone に落とす。
const maxAttempts = 8

const cacheDDL = `
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);

-- 主キーが sha256(source_url) なのは、hm.db を作り直しても images.id に
-- 引きずられないようにするため (spec_image_cache.md §1)。
CREATE TABLE IF NOT EXISTS image_cache (
  url_hash     TEXT PRIMARY KEY,
  image_id     INTEGER NOT NULL,   -- 参考値。再構築で変わりうるのでキーにしない
  source       TEXT NOT NULL,
  source_url   TEXT NOT NULL,
  origin_url   TEXT NOT NULL,
  state        TEXT NOT NULL,
  variants     TEXT NOT NULL DEFAULT '',  -- 保存できた幅 (カンマ区切り)
  bytes        INTEGER NOT NULL DEFAULT 0,
  pixel_w      INTEGER NOT NULL DEFAULT 0,
  pixel_h      INTEGER NOT NULL DEFAULT 0,
  attempts     INTEGER NOT NULL DEFAULT 0,
  last_error   TEXT NOT NULL DEFAULT '',
  requested_at TEXT NOT NULL,
  queued_at    TEXT,
  retry_after  TEXT,
  fetched_at   TEXT,
  hits         INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS image_cache_queue ON image_cache (source, state, queued_at);
CREATE INDEX IF NOT EXISTS image_cache_image ON image_cache (image_id);
CREATE INDEX IF NOT EXISTS image_cache_state ON image_cache (state);
`

// Entry は image_cache の 1 行。
type Entry struct {
	Hash      string
	ImageID   int64
	Source    string
	SourceURL string
	OriginURL string
	State     string
	Variants  []int
	Bytes     int64
	PixelW    int
	PixelH    int
	Attempts  int
	LastError string
	QueuedAt  sql.NullString
	FetchedAt sql.NullString
	Hits      int64
}

func (e Entry) HasVariant(w int) bool {
	for _, v := range e.Variants {
		if v == w {
			return true
		}
	}
	return false
}

func parseVariants(s string) []int {
	var out []int
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			var n int
			if _, err := fmt.Sscan(p, &n); err == nil {
				out = append(out, n)
			}
		}
	}
	return out
}

func formatVariants(vs []Variant) string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = fmt.Sprint(v.Width)
	}
	return strings.Join(parts, ",")
}

func openCacheDB(path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// _txlock=immediate は必須。既定の BEGIN DEFERRED だと、読み取りで始まった
	// トランザクションが UPDATE で書き込みへ昇格する瞬間に他の書き手と競合し、
	// SQLITE_BUSY_SNAPSHOT (517) が busy_timeout を無視して即座に返る
	// (リトライしても成功し得ないため)。館ごとのワーカーが同時に claim すると
	// 実際にこれを踏む。
	dsn := "file:" + url.PathEscape(abs) +
		"?_pragma=journal_mode(wal)&_pragma=busy_timeout(10000)" +
		"&_pragma=synchronous(normal)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// 接続を 1 本に固定して、館ごとのワーカーと web ハンドラの書き込みを
	// プロセス内で直列化する。1 件あたりの処理はミリ秒なので実害がない。
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(cacheDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("cache.db のスキーマ作成に失敗: %w", err)
	}
	return db, nil
}

func nowStr() string { return time.Now().UTC().Format(time.RFC3339Nano) }

const entryCols = `url_hash, image_id, source, source_url, origin_url, state, variants,
	bytes, pixel_w, pixel_h, attempts, last_error, queued_at, fetched_at, hits`

func scanEntry(sc interface{ Scan(...any) error }) (Entry, error) {
	var e Entry
	var variants string
	err := sc.Scan(&e.Hash, &e.ImageID, &e.Source, &e.SourceURL, &e.OriginURL,
		&e.State, &variants, &e.Bytes, &e.PixelW, &e.PixelH, &e.Attempts,
		&e.LastError, &e.QueuedAt, &e.FetchedAt, &e.Hits)
	e.Variants = parseVariants(variants)
	return e, err
}

// lookup は 1 件読む。無ければ (Entry{}, false, nil)。
func (c *Cache) lookup(ctx context.Context, hash string) (Entry, bool, error) {
	e, err := scanEntry(c.db.QueryRowContext(ctx,
		`SELECT `+entryCols+` FROM image_cache WHERE url_hash = ?`, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, false, nil
	}
	return e, err == nil, err
}

// enqueue は未登録なら queued として積む。既にあれば状態を変えない。
// gone / failed を勝手に queued に戻さないのがネガティブキャッシュの要点。
func (c *Cache) enqueue(ctx context.Context, r Ref, origin string) error {
	now := nowStr()
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO image_cache
		  (url_hash, image_id, source, source_url, origin_url, state, requested_at, queued_at)
		VALUES (?, ?, ?, ?, ?, 'queued', ?, ?)
		ON CONFLICT (url_hash) DO UPDATE SET
		  image_id   = excluded.image_id,
		  origin_url = excluded.origin_url,
		  hits       = image_cache.hits + 1`,
		Hash(r.SourceURL), r.ID, r.Source, r.SourceURL, origin, now, now)
	return err
}

// claim は次に処理する 1 件を取り出して fetching にする。
// 明示的に要求されたもの (queued) を、再試行待ち (failed) より先に処理する。
func (c *Cache) claim(ctx context.Context, source string, now time.Time) (Entry, bool, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return Entry{}, false, err
	}
	defer tx.Rollback()

	ts := now.UTC().Format(time.RFC3339Nano)
	e, err := scanEntry(tx.QueryRowContext(ctx, `
		SELECT `+entryCols+` FROM image_cache
		 WHERE source = ?
		   AND (state = 'queued' OR (state = 'failed' AND retry_after <= ?))
		 ORDER BY CASE state WHEN 'queued' THEN 0 ELSE 1 END, queued_at
		 LIMIT 1`, source, ts))
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE image_cache SET state = 'fetching' WHERE url_hash = ?`, e.Hash); err != nil {
		return Entry{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Entry{}, false, err
	}
	e.State = StateFetching
	return e, true, nil
}

// pendingSources は仕事の残っている館を返す。ワーカーはここから拾う。
func (c *Cache) pendingSources(ctx context.Context, now time.Time) ([]string, error) {
	rows, err := c.db.QueryContext(ctx, `
		SELECT DISTINCT source FROM image_cache
		 WHERE state = 'queued' OR (state = 'failed' AND retry_after <= ?)`,
		now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (c *Cache) markReady(ctx context.Context, hash string, vs []Variant) error {
	var total int64
	var pw, ph int
	for _, v := range vs {
		total += v.Bytes
		if v.Width > pw {
			pw, ph = v.PixelW, v.PixelH
		}
	}
	_, err := c.db.ExecContext(ctx, `
		UPDATE image_cache
		   SET state = 'ready', variants = ?, bytes = ?, pixel_w = ?, pixel_h = ?,
		       last_error = '', fetched_at = ?, retry_after = NULL
		 WHERE url_hash = ?`,
		formatVariants(vs), total, pw, ph, nowStr(), hash)
	return err
}

// markFailed は一時的な失敗。指数バックオフで retry_after を伸ばし、
// 回数を超えたら gone に落とす。
func (c *Cache) markFailed(ctx context.Context, e Entry, cause error) error {
	attempts := e.Attempts + 1
	state := StateFailed
	if attempts >= maxAttempts {
		state = StateGone
	}
	backoff := time.Duration(1<<uint(min(attempts, 8))) * 5 * time.Minute
	if backoff > 24*time.Hour {
		backoff = 24 * time.Hour
	}
	_, err := c.db.ExecContext(ctx, `
		UPDATE image_cache
		   SET state = ?, attempts = ?, last_error = ?, retry_after = ?
		 WHERE url_hash = ?`,
		state, attempts, truncErr(cause),
		time.Now().Add(backoff).UTC().Format(time.RFC3339Nano), e.Hash)
	return err
}

// markGone は恒久的な失敗 (404 / 410 / 画像でない)。再試行しない。
func (c *Cache) markGone(ctx context.Context, hash string, cause error) error {
	_, err := c.db.ExecContext(ctx, `
		UPDATE image_cache
		   SET state = 'gone', attempts = attempts + 1, last_error = ?, retry_after = NULL
		 WHERE url_hash = ?`, truncErr(cause), hash)
	return err
}

// recover は起動時に呼ぶ。落ちたプロセスが fetching のまま残した行を戻す。
func (c *Cache) recoverStuck(ctx context.Context) (int64, error) {
	res, err := c.db.ExecContext(ctx,
		`UPDATE image_cache SET state = 'queued' WHERE state = 'fetching'`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ahead は同じ館のキューで自分より前に何枚あるかを数える。
// 「あと N 枚 / 約 M 分」の表示に使う。
func (c *Cache) ahead(ctx context.Context, e Entry) (int, error) {
	if !e.QueuedAt.Valid {
		return 0, nil
	}
	var n int
	err := c.db.QueryRowContext(ctx, `
		SELECT count(*) FROM image_cache
		 WHERE source = ? AND state IN ('queued', 'fetching') AND queued_at < ?`,
		e.Source, e.QueuedAt.String).Scan(&n)
	return n, err
}

func truncErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > 500 {
		s = s[:500] + "…"
	}
	return s
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
