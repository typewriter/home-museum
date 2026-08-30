// コレクション。設計は spec_collections.md。
//
// finder が唯一の書き手になる初めてのデータで、人の操作でしか増えない。
// hm.db / index.db / cache.db のどれとも寿命が違うので 4 つ目のファイル
// collections.db に置き、`co.` として ATTACH する (§1)。
//
// メンバーのキーは images.id ではなく source_url (§3)。hm.db を作り直すと
// id は変わるが、コレクションには派生層のような「作り直せばよい」逃げ道がない。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// CollectionsAlias は collections.db を ATTACH するときのスキーマ名。
const CollectionsAlias = "co"

// collectionsDDL は接続後に一度だけ流す。ATTACH 済みの接続で実行するので
// スキーマ名で修飾する。
const collectionsDDL = `
CREATE TABLE IF NOT EXISTS co.collections (
  id          INTEGER PRIMARY KEY,
  slug        TEXT NOT NULL UNIQUE,
  title       TEXT NOT NULL,
  title_en    TEXT,
  description TEXT,
  cover_url   TEXT,
  sort        TEXT NOT NULL,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS co.collection_images (
  collection_id INTEGER NOT NULL REFERENCES collections (id) ON DELETE CASCADE,
  source_url    TEXT NOT NULL,
  position      INTEGER,
  note          TEXT,
  added_at      TEXT NOT NULL,
  PRIMARY KEY (collection_id, source_url)
);

CREATE INDEX IF NOT EXISTS co.collection_images_url
  ON collection_images (source_url);
`

// 並び順の選択肢。random は viewer 側の話なのでここには持たない
// (spec_collections.md §7 の「公開範囲」と同じ理由で先送り)。
var collectionSorts = []struct{ Value, Label string }{
	{"manual", "手動 (並べ替えられる)"},
	{"added_desc", "追加した順 (新しい順)"},
	{"year_asc", "制作年 古い順"},
	{"year_desc", "制作年 新しい順"},
	{"title", "題名"},
}

// CollectionSorts は UI の <select> 用。
func CollectionSorts() []struct{ Value, Label string } { return collectionSorts }

func validSort(s string) bool {
	for _, v := range collectionSorts {
		if v.Value == s {
			return true
		}
	}
	return false
}

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Collection は 1 コレクション。Count / Unresolved は一覧でだけ埋まる。
type Collection struct {
	ID          int64
	Slug        string
	Title       string
	TitleEn     string
	Description string
	CoverURL    string
	Sort        string
	CreatedAt   string
	UpdatedAt   string

	Count int64
	// Unresolved は hm.db に見つからない source_url の数。§3 のとおり
	// 未収録の作品も指せるようにしてあるので、0 とは限らない。
	Unresolved int64
}

// SortLabel は表示用。
func (c Collection) SortLabel() string {
	for _, v := range collectionSorts {
		if v.Value == c.Sort {
			return v.Label
		}
	}
	return c.Sort
}

// MemberRow はコレクションの 1 行。Work は hm.db に見つからなければ nil。
type MemberRow struct {
	SourceURL string
	Position  sql.NullInt64
	Note      string
	AddedAt   string
	Work      *WorkRow
}

type MemberResult struct {
	Rows       []MemberRow
	Total      int64
	Unresolved int64
	Page, Per  int
}

func (r MemberResult) HasPrev() bool { return r.Page > 1 }
func (r MemberResult) HasNext() bool { return int64(r.Page*r.Per) < r.Total }

var errNoCollections = errors.New("コレクションが無効です (-collections を空にして起動しています)")

func (s *Store) requireCollections() error {
	if !s.HasCollections {
		return errNoCollections
	}
	return nil
}

func now() string { return time.Now().Format(time.RFC3339) }

// ---- 読み取り ----

// Collections は一覧。件数と未解決件数を一緒に数える。コレクションは多くて
// 数十本、メンバーも 1 本あたり数千件なので、毎回数えてよい。
func (s *Store) Collections(ctx context.Context) ([]Collection, error) {
	if err := s.requireCollections(); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT c.id, c.slug, c.title, coalesce(c.title_en, ''), coalesce(c.description, ''),
		       coalesce(c.cover_url, ''), c.sort, c.created_at, c.updated_at,
		       count(ci.source_url),
		       sum(CASE WHEN ci.source_url IS NOT NULL AND i.id IS NULL THEN 1 ELSE 0 END)
		  FROM co.collections c
		  LEFT JOIN co.collection_images ci ON ci.collection_id = c.id
		  LEFT JOIN images i ON i.source_url = ci.source_url
		 GROUP BY c.id
		 ORDER BY c.title COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Collection
	for rows.Next() {
		var c Collection
		var unresolved sql.NullInt64
		if err := rows.Scan(&c.ID, &c.Slug, &c.Title, &c.TitleEn, &c.Description,
			&c.CoverURL, &c.Sort, &c.CreatedAt, &c.UpdatedAt, &c.Count, &unresolved); err != nil {
			return nil, err
		}
		c.Unresolved = unresolved.Int64
		out = append(out, c)
	}
	return out, rows.Err()
}

// CollectionList は件数を数えない軽い一覧。どのページにも出す「追加先」の
// 選択肢に使うので、images との JOIN を含む Collections とは分けてある。
func (s *Store) CollectionList(ctx context.Context) ([]Collection, error) {
	if !s.HasCollections {
		return nil, nil
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, slug, title FROM co.collections ORDER BY title COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Collection
	for rows.Next() {
		var c Collection
		if err := rows.Scan(&c.ID, &c.Slug, &c.Title); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) collectionWhere(ctx context.Context, cond string, arg any) (*Collection, error) {
	if err := s.requireCollections(); err != nil {
		return nil, err
	}
	var c Collection
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, slug, title, coalesce(title_en, ''), coalesce(description, ''),
		       coalesce(cover_url, ''), sort, created_at, updated_at
		  FROM co.collections WHERE `+cond, arg).
		Scan(&c.ID, &c.Slug, &c.Title, &c.TitleEn, &c.Description,
			&c.CoverURL, &c.Sort, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) CollectionBySlug(ctx context.Context, slug string) (*Collection, error) {
	return s.collectionWhere(ctx, "slug = ?", slug)
}

func (s *Store) CollectionByID(ctx context.Context, id int64) (*Collection, error) {
	return s.collectionWhere(ctx, "id = ?", id)
}

// CollectionMembers はメンバーを 1 ページぶん返す。hm.db に無い source_url も
// 落とさずに返す (§3) ので images は LEFT JOIN。
func (s *Store) CollectionMembers(ctx context.Context, c *Collection, page, per int) (*MemberResult, error) {
	if err := s.requireCollections(); err != nil {
		return nil, err
	}
	if page < 1 {
		page = 1
	}
	if per < 1 || per > 200 {
		per = 24
	}

	res := &MemberResult{Page: page, Per: per}
	if err := s.DB.QueryRowContext(ctx, `
		SELECT count(*), sum(CASE WHEN i.id IS NULL THEN 1 ELSE 0 END)
		  FROM co.collection_images ci
		  LEFT JOIN images i ON i.source_url = ci.source_url
		 WHERE ci.collection_id = ?`, c.ID).Scan(&res.Total, &unresolvedTo{&res.Unresolved}); err != nil {
		return nil, err
	}

	metaSel, metaJoin := "NULL, NULL, '', ''", ""
	if s.HasIndex {
		metaSel = "m.year_start, m.year_end, coalesce(m.year_kind, ''), coalesce(m.date_precision, '')"
		metaJoin = "LEFT JOIN " + AttachAlias + ".image_meta m ON m.image_id = i.id"
	}

	yearCol := "i.date_raw_start"
	if s.HasIndex {
		yearCol = "m.year_start"
	}
	var order string
	switch c.Sort {
	case "year_asc":
		order = fmt.Sprintf("ORDER BY %s IS NULL, %s ASC, ci.added_at", yearCol, yearCol)
	case "year_desc":
		order = fmt.Sprintf("ORDER BY %s IS NULL, %s DESC, ci.added_at", yearCol, yearCol)
	case "title":
		order = "ORDER BY coalesce(nullif(tj.text, ''), i.title) COLLATE NOCASE, ci.added_at"
	case "added_desc":
		order = "ORDER BY ci.added_at DESC, ci.rowid DESC"
	default: // manual
		order = "ORDER BY ci.position IS NULL, ci.position, ci.added_at"
	}

	query := fmt.Sprintf(`
		SELECT ci.source_url, ci.position, coalesce(ci.note, ''), ci.added_at,
		       i.id, coalesce(i.source, ''), coalesce(i.title, ''), coalesce(tj.text, ''),
		       coalesce(i.artist, ''), coalesce(i.date, ''), coalesce(i.image_url, ''),
		       %s
		  FROM co.collection_images ci
		  LEFT JOIN images i ON i.source_url = ci.source_url
		  LEFT JOIN image_translations tj
		         ON tj.image_id = i.id AND tj.field = 'title' AND tj.lang = 'ja'
		  %s
		 WHERE ci.collection_id = ?
		 %s
		 LIMIT %d OFFSET %d`, metaSel, metaJoin, order, per, (page-1)*per)

	rows, err := s.DB.QueryContext(ctx, query, c.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var m MemberRow
		var w WorkRow
		var id sql.NullInt64
		if err := rows.Scan(&m.SourceURL, &m.Position, &m.Note, &m.AddedAt,
			&id, &w.Source, &w.Title, &w.TitleJa, &w.Artist, &w.Date, &w.ImageURL,
			&w.YearStart, &w.YearEnd, &w.YearKind, &w.Precision); err != nil {
			return nil, err
		}
		if id.Valid {
			w.ID = id.Int64
			w.SourceURL = m.SourceURL
			m.Work = &w
		}
		res.Rows = append(res.Rows, m)
	}
	return res, rows.Err()
}

// unresolvedTo は sum() の NULL (行が 0 件のとき) を 0 として受けるための Scanner。
type unresolvedTo struct{ dst *int64 }

func (u unresolvedTo) Scan(v any) error {
	switch x := v.(type) {
	case nil:
		*u.dst = 0
	case int64:
		*u.dst = x
	case float64:
		*u.dst = int64(x)
	default:
		return fmt.Errorf("unresolved: 予期しない型 %T", v)
	}
	return nil
}

// CollectionsForURL は「この作品が入っているコレクション」。作品詳細で使う。
func (s *Store) CollectionsForURL(ctx context.Context, sourceURL string) ([]Collection, error) {
	if !s.HasCollections {
		return nil, nil
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT c.id, c.slug, c.title
		  FROM co.collection_images ci
		  JOIN co.collections c ON c.id = ci.collection_id
		 WHERE ci.source_url = ?
		 ORDER BY c.title COLLATE NOCASE`, sourceURL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Collection
	for rows.Next() {
		var c Collection
		if err := rows.Scan(&c.ID, &c.Slug, &c.Title); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---- 書き込み ----

// SaveCollection は id が 0 なら作成、それ以外なら更新する。slug が空なら
// 採番する (題名が日本語なので slug は自動生成できない)。
func (s *Store) SaveCollection(ctx context.Context, c *Collection) error {
	if err := s.requireCollections(); err != nil {
		return err
	}
	c.Title = strings.TrimSpace(c.Title)
	if c.Title == "" {
		return errors.New("題名は必須です")
	}
	c.Slug = strings.TrimSpace(strings.ToLower(c.Slug))
	if c.Slug == "" {
		var n int64
		if err := s.DB.QueryRowContext(ctx,
			`SELECT coalesce(max(id), 0) + 1 FROM co.collections`).Scan(&n); err != nil {
			return err
		}
		c.Slug = fmt.Sprintf("c%d", n)
	}
	if !slugRe.MatchString(c.Slug) {
		return fmt.Errorf("slug %q は使えません。半角英小文字・数字・ハイフンで、先頭は英数字", c.Slug)
	}
	if !validSort(c.Sort) {
		c.Sort = "manual"
	}

	ts := now()
	if c.ID == 0 {
		r, err := s.DB.ExecContext(ctx, `
			INSERT INTO co.collections
				(slug, title, title_en, description, cover_url, sort, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			c.Slug, c.Title, c.TitleEn, c.Description, c.CoverURL, c.Sort, ts, ts)
		if err != nil {
			return collErr(err, c.Slug)
		}
		c.ID, _ = r.LastInsertId()
		c.CreatedAt, c.UpdatedAt = ts, ts
		return nil
	}
	_, err := s.DB.ExecContext(ctx, `
		UPDATE co.collections
		   SET slug = ?, title = ?, title_en = ?, description = ?, cover_url = ?,
		       sort = ?, updated_at = ?
		 WHERE id = ?`,
		c.Slug, c.Title, c.TitleEn, c.Description, c.CoverURL, c.Sort, ts, c.ID)
	if err != nil {
		return collErr(err, c.Slug)
	}
	c.UpdatedAt = ts
	return nil
}

func collErr(err error, slug string) error {
	if strings.Contains(err.Error(), "UNIQUE") {
		return fmt.Errorf("slug %q は既に使われています", slug)
	}
	return err
}

func (s *Store) DeleteCollection(ctx context.Context, id int64) error {
	if err := s.requireCollections(); err != nil {
		return err
	}
	// ATTACH した DB にも外部キーは効くが、PRAGMA foreign_keys は既定で off。
	// 依存を増やさず自分で消す。
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM co.collection_images WHERE collection_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM co.collections WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// AddToCollection は末尾に追加する。既に入っているものは position を動かさずに
// 飛ばす (二重追加で並びが崩れないように)。戻り値は実際に増えた件数。
func (s *Store) AddToCollection(ctx context.Context, id int64, urls []string, note string) (int, error) {
	if err := s.requireCollections(); err != nil {
		return 0, err
	}
	if len(urls) == 0 {
		return 0, nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// 末尾に足す。空なら 0 から (renumber も 0 起点なので揃える)。
	var next sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		`SELECT max(position) FROM co.collection_images WHERE collection_id = ?`, id).
		Scan(&next); err != nil {
		return 0, err
	}
	var pos int64
	if next.Valid {
		pos = next.Int64 + 1
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO co.collection_images (collection_id, source_url, position, note, added_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (collection_id, source_url) DO NOTHING`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	ts := now()
	added := 0
	for _, u := range urls {
		if u = strings.TrimSpace(u); u == "" {
			continue
		}
		r, err := stmt.ExecContext(ctx, id, u, pos, nullIfEmpty(note), ts)
		if err != nil {
			return 0, err
		}
		if n, _ := r.RowsAffected(); n > 0 {
			added++
			pos++
		}
	}
	if added > 0 {
		if _, err := tx.ExecContext(ctx,
			`UPDATE co.collections SET updated_at = ? WHERE id = ?`, ts, id); err != nil {
			return 0, err
		}
	}
	return added, tx.Commit()
}

func (s *Store) RemoveFromCollection(ctx context.Context, id int64, urls []string) (int, error) {
	if err := s.requireCollections(); err != nil {
		return 0, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	removed := 0
	for _, u := range urls {
		r, err := tx.ExecContext(ctx,
			`DELETE FROM co.collection_images WHERE collection_id = ? AND source_url = ?`, id, u)
		if err != nil {
			return 0, err
		}
		n, _ := r.RowsAffected()
		removed += int(n)
	}
	if removed > 0 {
		if _, err := tx.ExecContext(ctx,
			`UPDATE co.collections SET updated_at = ? WHERE id = ?`, now(), id); err != nil {
			return 0, err
		}
	}
	return removed, tx.Commit()
}

// SetNote はメンバーの覚書を書き換える。「なぜ入れたか」は後から効く。
func (s *Store) SetMemberNote(ctx context.Context, id int64, url, note string) error {
	if err := s.requireCollections(); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx,
		`UPDATE co.collection_images SET note = ? WHERE collection_id = ? AND source_url = ?`,
		nullIfEmpty(note), id, url)
	return err
}

// MoveMember は手動順で 1 つ隣と入れ替える (delta は -1 か +1)。
//
// 入れ替えの前に必ず 0..n-1 へ採番し直す。追加・削除で position には穴が空くし、
// エクスポートから読み戻した値が連番とは限らないため。5,000 件で 6 ms 程度
// なので、毎回やってよい (spec_collections.md §4)。
func (s *Store) MoveMember(ctx context.Context, id int64, url string, delta int) error {
	if err := s.requireCollections(); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := renumber(ctx, tx, id); err != nil {
		return err
	}

	var pos, total int64
	if err := tx.QueryRowContext(ctx,
		`SELECT position FROM co.collection_images WHERE collection_id = ? AND source_url = ?`,
		id, url).Scan(&pos); err != nil {
		if err == sql.ErrNoRows {
			return errors.New("そのメンバーはコレクションにありません")
		}
		return err
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM co.collection_images WHERE collection_id = ?`, id).Scan(&total); err != nil {
		return err
	}

	target := pos + int64(delta)
	if target < 0 || target >= total {
		return nil // 端なので何もしない
	}
	// 一時的に範囲外へ退避してから入れ替える (position に UNIQUE は無いが、
	// 途中経過でも重複させないほうが読み戻しやすい)。
	if _, err := tx.ExecContext(ctx,
		`UPDATE co.collection_images SET position = -1 WHERE collection_id = ? AND position = ?`,
		id, target); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE co.collection_images SET position = ? WHERE collection_id = ? AND source_url = ?`,
		target, id, url); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE co.collection_images SET position = ? WHERE collection_id = ? AND position = -1`,
		pos, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE co.collections SET updated_at = ? WHERE id = ?`, now(), id); err != nil {
		return err
	}
	return tx.Commit()
}

func renumber(ctx context.Context, tx *sql.Tx, id int64) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE co.collection_images SET position = r.n FROM (
		  SELECT source_url, row_number() OVER (ORDER BY position IS NULL, position, added_at) - 1 AS n
		    FROM co.collection_images WHERE collection_id = ?
		) r
		 WHERE collection_id = ? AND co.collection_images.source_url = r.source_url`, id, id)
	return err
}

func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
