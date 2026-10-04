package imagecache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
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

// Priority は取得の順番。館ごとの間隔は変えずに、待ち行列の並びだけを変える。
// コレクションに入れた作品は公開されるので最優先、管理画面で選んでいる最中の
// サムネがその次、閲覧者の要求は最後。
type Priority int

const (
	PriorityVisitor    Priority = 0
	PriorityAdmin      Priority = 1
	PriorityCollection Priority = 2
)

// Entry は image_cache の 1 行。
type Entry struct {
	Hash      string
	ImageID   int64
	Source    string
	SourceURL string
	OriginURL string
	State     string
	Priority  Priority
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

func nowStr() string { return time.Now().UTC().Format(time.RFC3339Nano) }

const entryCols = `url_hash, image_id, source, source_url, origin_url, state, priority, variants,
	bytes, pixel_w, pixel_h, attempts, last_error, queued_at, fetched_at, hits`

func scanEntry(sc interface{ Scan(...any) error }) (Entry, error) {
	var e Entry
	var variants string
	err := sc.Scan(&e.Hash, &e.ImageID, &e.Source, &e.SourceURL, &e.OriginURL,
		&e.State, &e.Priority, &variants, &e.Bytes, &e.PixelW, &e.PixelH, &e.Attempts,
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
// 優先度は上げるだけで下げない。閲覧者が後から同じ作品を見ても、コレクションに
// 入れたときの順番を失わないため。
func (c *Cache) enqueue(ctx context.Context, r Ref, origin string, p Priority) error {
	now := nowStr()
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO image_cache
		  (url_hash, image_id, source, source_url, origin_url, state, priority, requested_at, queued_at)
		VALUES (?, ?, ?, ?, ?, 'queued', ?, ?, ?)
		ON CONFLICT (url_hash) DO UPDATE SET
		  image_id   = excluded.image_id,
		  origin_url = excluded.origin_url,
		  priority   = max(image_cache.priority, excluded.priority),
		  hits       = image_cache.hits + 1`,
		Hash(r.SourceURL), r.ID, r.Source, r.SourceURL, origin, p, now, now)
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
		 ORDER BY CASE state WHEN 'queued' THEN 0 ELSE 1 END, priority DESC, queued_at
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
	return c.markFailedAt(ctx, e, cause, time.Time{})
}

// maxBackoff は指数バックオフの上限。サーバー指定の Retry-After にも同じ上限を
// 適用する (下記 markFailedAt)。上限を揃えないと、異常に大きい Retry-After を
// 返す相手 (誤設定・悪意のいずれでも) に対して failed のまま無期限に固着しうる。
const maxBackoff = 24 * time.Hour

// markFailedAt は markFailed と同じだが、サーバーが Retry-After で明示してきた
// 次回再試行時刻 (serverRetryAfter) を考慮する。指数バックオフより遅ければ
// そちらを優先し (礼儀として要求を尊重する)、早ければ無視する
// (極端に短い指定で叩き過ぎないための下限として既存のバックオフを残す)。
// ただし maxBackoff は超えさせない。serverRetryAfter がゼロ値なら従来どおり
// 指数バックオフだけを使う。
func (c *Cache) markFailedAt(ctx context.Context, e Entry, cause error, serverRetryAfter time.Time) error {
	attempts := e.Attempts + 1
	state := StateFailed
	if attempts >= maxAttempts {
		state = StateGone
	}
	backoff := time.Duration(1<<uint(min(attempts, 8))) * 5 * time.Minute
	if backoff > maxBackoff {
		backoff = maxBackoff
	}
	retryAt := time.Now().Add(backoff)
	if serverCap := time.Now().Add(maxBackoff); serverRetryAfter.After(serverCap) {
		serverRetryAfter = serverCap
	}
	if serverRetryAfter.After(retryAt) {
		retryAt = serverRetryAfter
	}
	_, err := c.db.ExecContext(ctx, `
		UPDATE image_cache
		   SET state = ?, attempts = ?, last_error = ?, retry_after = ?
		 WHERE url_hash = ?`,
		state, attempts, truncErr(cause), retryAt.UTC().Format(time.RFC3339Nano), e.Hash)
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
		 WHERE source = ? AND (state = 'fetching' OR (state = 'queued' AND
		       (priority > ? OR (priority = ? AND queued_at < ?))))`,
		e.Source, e.Priority, e.Priority, e.QueuedAt.String).Scan(&n)
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

// ClearFailures は失敗の記録 (failed / gone) を消す。消した作品は、次に表示された
// ときに普通の要求として取り直される。queued に戻して一斉に積み直すことはしない。
// 取得不可が数千件あると、そのぶんが閲覧者の要求より前に並んでしまうため。
// source が空なら全館が対象。ready と取得待ちの行には触れない。
func (c *Cache) ClearFailures(ctx context.Context, source string, states []string) (int64, error) {
	var n int64
	for _, st := range states {
		if st != StateFailed && st != StateGone {
			return 0, fmt.Errorf("消せるのは %s と %s だけです: %q", StateFailed, StateGone, st)
		}
		res, err := c.db.ExecContext(ctx,
			`DELETE FROM image_cache WHERE state = ? AND (? = '' OR source = ?)`, st, source, source)
		if err != nil {
			return n, err
		}
		k, _ := res.RowsAffected()
		n += k
	}
	return n, nil
}
