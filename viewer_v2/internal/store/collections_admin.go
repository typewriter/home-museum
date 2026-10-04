package store

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ErrInvalid は入力の誤り。管理 API は 400 で返す。
type ErrInvalid struct{ msg string }

func (e ErrInvalid) Error() string { return e.msg }

func invalid(format string, a ...any) error { return ErrInvalid{fmt.Sprintf(format, a...)} }

// slug は URL (/c/{slug}) にそのまま出るので、エスケープの要らない文字に限る。
var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// SaveCollection は c.ID が 0 なら作り、それ以外なら更新する。
func (s *Store) SaveCollection(ctx context.Context, c *Collection) error {
	c.Title = strings.TrimSpace(c.Title)
	c.Slug = strings.TrimSpace(strings.ToLower(c.Slug))
	if c.Title == "" {
		return invalid("題名は必須です")
	}
	if !slugRe.MatchString(c.Slug) {
		return invalid("slug %q は使えません。半角英小文字・数字・ハイフンで、先頭は英数字にしてください", c.Slug)
	}
	if c.Sort == "" {
		c.Sort = "manual"
	}
	if !ValidSort(c.Sort) {
		return invalid("並び順 %q は使えません", c.Sort)
	}

	ts := now()
	if c.ID == 0 {
		r, err := s.w.ExecContext(ctx, `
			INSERT INTO collections
			  (slug, title, title_en, description, cover_url, sort, published, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			c.Slug, c.Title, nullIfEmpty(c.TitleEN), nullIfEmpty(c.Description),
			nullIfEmpty(c.CoverURL), c.Sort, c.Published, ts, ts)
		if err != nil {
			return slugErr(err, c.Slug)
		}
		c.ID, _ = r.LastInsertId()
		return nil
	}
	r, err := s.w.ExecContext(ctx, `
		UPDATE collections
		   SET slug = ?, title = ?, title_en = ?, description = ?, cover_url = ?,
		       sort = ?, published = ?, updated_at = ?
		 WHERE id = ?`,
		c.Slug, c.Title, nullIfEmpty(c.TitleEN), nullIfEmpty(c.Description),
		nullIfEmpty(c.CoverURL), c.Sort, c.Published, ts, c.ID)
	if err != nil {
		return slugErr(err, c.Slug)
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func slugErr(err error, slug string) error {
	if strings.Contains(err.Error(), "UNIQUE") {
		return invalid("slug %q は既に使われています", slug)
	}
	return err
}

// DeleteCollection はメンバーごと消す (collection_works は ON DELETE CASCADE)。
func (s *Store) DeleteCollection(ctx context.Context, id int64) error {
	_, err := s.w.ExecContext(ctx, `DELETE FROM collections WHERE id = ?`, id)
	return err
}

func (s *Store) touch(ctx context.Context, tx *sql.Tx, id int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE collections SET updated_at = ? WHERE id = ?`, now(), id)
	return err
}

// AddWorks は末尾に足す。既に入っているものは位置を動かさずに飛ばす。
// 戻り値は実際に増えた source_url。
func (s *Store) AddWorks(ctx context.Context, id int64, urls []string, note string) ([]string, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var pos int64
	if err := tx.QueryRowContext(ctx,
		`SELECT coalesce(max(position) + 1, 0) FROM collection_works WHERE collection_id = ?`, id).
		Scan(&pos); err != nil {
		return nil, err
	}
	ts := now()
	var added []string
	for _, u := range urls {
		if u = strings.TrimSpace(u); u == "" {
			continue
		}
		r, err := tx.ExecContext(ctx, `
			INSERT INTO collection_works (collection_id, source_url, position, note, added_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (collection_id, source_url) DO NOTHING`,
			id, u, pos, nullIfEmpty(note), ts)
		if err != nil {
			if strings.Contains(err.Error(), "FOREIGN KEY") {
				return nil, sql.ErrNoRows
			}
			return nil, err
		}
		if n, _ := r.RowsAffected(); n > 0 {
			added = append(added, u)
			pos++
		}
	}
	if len(added) > 0 {
		if err := s.touch(ctx, tx, id); err != nil {
			return nil, err
		}
	}
	return added, tx.Commit()
}

func (s *Store) RemoveWork(ctx context.Context, id int64, url string) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM collection_works WHERE collection_id = ? AND source_url = ?`, id, url); err != nil {
		return err
	}
	if err := s.touch(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SetNote(ctx context.Context, id int64, url, note string) error {
	_, err := s.w.ExecContext(ctx,
		`UPDATE collection_works SET note = ? WHERE collection_id = ? AND source_url = ?`,
		nullIfEmpty(note), id, url)
	return err
}

// SetOrder は手動順を urls の並びにする。渡された集合が今のメンバーと一致しなければ
// 拒否する。別のタブで足し引きした後の古い並びで上書きすると、そのぶんが消えたり
// 位置を失ったりするため。
func (s *Store) SetOrder(ctx context.Context, id int64, urls []string) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `SELECT source_url FROM collection_works WHERE collection_id = ?`, id)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			rows.Close()
			return err
		}
		have[u] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, u := range urls {
		if !have[u] || seen[u] {
			return invalid("メンバーが変わっています。読み込み直してから並べ替えてください")
		}
		seen[u] = true
	}
	if len(seen) != len(have) {
		return invalid("メンバーが変わっています。読み込み直してから並べ替えてください")
	}

	for i, u := range urls {
		if _, err := tx.ExecContext(ctx,
			`UPDATE collection_works SET position = ? WHERE collection_id = ? AND source_url = ?`,
			i, id, u); err != nil {
			return err
		}
	}
	if err := s.touch(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit()
}

// Member はコレクションの 1 行。Work が nil なら works に引けない (未解決)。
type Member struct {
	SourceURL string    `json:"source_url"`
	Position  int       `json:"position"`
	Note      string    `json:"note,omitempty"`
	AddedAt   string    `json:"added_at"`
	Work      *WorkCard `json:"work,omitempty"`
}

// Members は手動順 (position) で全件を返す。並べ替えの画面は全体を一度に扱うため。
func (s *Store) Members(ctx context.Context, id int64) ([]Member, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT cw.source_url, cw.position, coalesce(cw.note, ''), cw.added_at,
		       w.id, coalesce(w.title, ''), coalesce(w.title_ja, ''), coalesce(w.artist, ''), coalesce(w.date_text, '')
		  FROM collection_works cw
		  LEFT JOIN works w ON w.source_url = cw.source_url
		 WHERE cw.collection_id = ?
		 ORDER BY cw.position, cw.added_at`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		var wid sql.NullInt64
		var c WorkCard
		if err := rows.Scan(&m.SourceURL, &m.Position, &m.Note, &m.AddedAt,
			&wid, &c.Title, &c.TitleJA, &c.Artist, &c.DateText); err != nil {
			return nil, err
		}
		if wid.Valid {
			c.ID = wid.Int64
			m.Work = &c
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AdminWork は管理画面の作品一覧の 1 枚。どのコレクションに入っているかを付ける。
type AdminWork struct {
	WorkCard
	SourceURL   string  `json:"source_url"`
	Collections []int64 `json:"collections"`
}

// WithCollections は作品ごとに所属コレクションの id を付ける。
func (s *Store) WithCollections(ctx context.Context, cards []WorkCard) ([]AdminWork, error) {
	out := make([]AdminWork, len(cards))
	for i, c := range cards {
		out[i] = AdminWork{WorkCard: c, Collections: []int64{}}
		rows, err := s.r.QueryContext(ctx, `
			SELECT w.source_url, cw.collection_id FROM works w
			  LEFT JOIN collection_works cw ON cw.source_url = w.source_url
			 WHERE w.id = ?`, c.ID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var cid sql.NullInt64
			if err := rows.Scan(&out[i].SourceURL, &cid); err != nil {
				rows.Close()
				return nil, err
			}
			if cid.Valid {
				out[i].Collections = append(out[i].Collections, cid.Int64)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}
