package store

import (
	"context"
	"database/sql"
	"errors"
)

type Collection struct {
	ID          int64  `json:"id"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	TitleEN     string `json:"title_en,omitempty"`
	Description string `json:"description,omitempty"`
	CoverURL    string `json:"cover_url,omitempty"` // source_url。works.id は再 import で変わる
	Sort        string `json:"sort"`
	Published   bool   `json:"published"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	Count       int    `json:"count"`                // works に引けるメンバー数
	Unresolved  int    `json:"unresolved,omitempty"` // 引けないメンバー数
	CoverID     *int64 `json:"cover_id,omitempty"`
}

const collectionSelect = `
SELECT c.id, c.slug, c.title, coalesce(c.title_en, ''), coalesce(c.description, ''),
       coalesce(c.cover_url, ''), c.sort, c.published, c.created_at, c.updated_at,
       (SELECT count(*) FROM collection_works cw JOIN works w ON w.source_url = cw.source_url
         WHERE cw.collection_id = c.id),
       (SELECT count(*) FROM collection_works cw
         WHERE cw.collection_id = c.id
           AND NOT EXISTS (SELECT 1 FROM works w WHERE w.source_url = cw.source_url)),
       (SELECT w.id FROM works w WHERE w.source_url = c.cover_url)
  FROM collections c`

func scanCollection(sc interface{ Scan(...any) error }) (Collection, error) {
	var c Collection
	err := sc.Scan(&c.ID, &c.Slug, &c.Title, &c.TitleEN, &c.Description, &c.CoverURL,
		&c.Sort, &c.Published, &c.CreatedAt, &c.UpdatedAt, &c.Count, &c.Unresolved, &c.CoverID)
	return c, err
}

// Collections は一覧。publishedOnly なら公開中のものだけ。
func (s *Store) Collections(ctx context.Context, publishedOnly bool) ([]Collection, error) {
	q := collectionSelect
	if publishedOnly {
		q += ` WHERE c.published = 1`
	}
	rows, err := s.r.QueryContext(ctx, q+` ORDER BY c.updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Collection{}
	for rows.Next() {
		c, err := scanCollection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) collectionWhere(ctx context.Context, cond string, arg any) (*Collection, error) {
	c, err := scanCollection(s.r.QueryRowContext(ctx, collectionSelect+` WHERE `+cond, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) CollectionBySlug(ctx context.Context, slug string) (*Collection, error) {
	return s.collectionWhere(ctx, `c.slug = ?`, slug)
}

func (s *Store) CollectionByID(ctx context.Context, id int64) (*Collection, error) {
	return s.collectionWhere(ctx, `c.id = ?`, id)
}
