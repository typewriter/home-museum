package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Exhibition は順序の付いた作品の列。コレクションも作者展もこの形で返し、
// 画面は入口 (図録) と展示室 (1 作品ずつ) の 2 つだけで両方を見せる。
type Exhibition struct {
	Kind        string `json:"kind"` // collection | artist
	Key         string `json:"key"`  // slug または person_key
	Title       string `json:"title"`
	Subtitle    string `json:"subtitle,omitempty"`
	Description string `json:"description,omitempty"`
	CoverID     *int64 `json:"cover_id,omitempty"`
	Total       int    `json:"total"`

	from  string // "FROM ... WHERE ..."。w が works
	args  []any
	order string
}

func (s *Store) count(ctx context.Context, ex *Exhibition) error {
	return s.r.QueryRowContext(ctx, `SELECT count(*) `+ex.from, ex.args...).Scan(&ex.Total)
}

// Page は図録の 1 ページ。page は 1 始まり。
func (s *Store) Page(ctx context.Context, ex *Exhibition, page, per int) ([]WorkCard, error) {
	if page < 1 {
		page = 1
	}
	args := append(append([]any{}, ex.args...), per, (page-1)*per)
	return scanCards(s.r.QueryContext(ctx,
		`SELECT `+cardCols+` `+ex.from+` ORDER BY `+ex.order+` LIMIT ? OFFSET ?`, args...))
}

// Room は展示室の 1 作品と、その前後の作品の id (先読みと矢印のため)。
type Room struct {
	N      int    `json:"n"` // 1 始まり
	Work   Work   `json:"work"`
	PrevID *int64 `json:"prev_id,omitempty"`
	NextID *int64 `json:"next_id,omitempty"`
}

// Room は n 番目 (1 始まり) を返す。範囲外なら nil。
func (s *Store) Room(ctx context.Context, ex *Exhibition, n int) (*Room, error) {
	if n < 1 || n > ex.Total {
		return nil, nil
	}
	off, lim := n-2, 3
	if off < 0 {
		off, lim = 0, 2
	}
	args := append(append([]any{}, ex.args...), lim, off)
	rows, err := s.r.QueryContext(ctx,
		`SELECT w.id `+ex.from+` ORDER BY `+ex.order+` LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	at := n - 1 - off
	if at >= len(ids) {
		return nil, nil
	}
	room := &Room{N: n}
	if at > 0 {
		room.PrevID = &ids[at-1]
	}
	if at+1 < len(ids) {
		room.NextID = &ids[at+1]
	}
	w, err := s.Work(ctx, ids[at])
	if err != nil {
		return nil, err
	}
	room.Work = *w
	return room, nil
}

// Work は 1 作品。無ければ nil。
func (s *Store) Work(ctx context.Context, id int64) (*Work, error) {
	w, err := scanWork(s.r.QueryRowContext(ctx, `SELECT `+workCols+` FROM works w WHERE w.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &w, nil
}

// collectionOrders は collections.sort ごとの並び。同順位は手動順で決める。
var collectionOrders = map[string]string{
	"manual":     `cw.position`,
	"added_desc": `cw.added_at DESC, cw.position`,
	"year_asc":   `w.year_start IS NULL, w.year_start, cw.position`,
	"year_desc":  `w.year_start IS NULL, w.year_start DESC, cw.position`,
	"title":      `coalesce(w.title_ja, w.title) COLLATE NOCASE, cw.position`,
}

// ValidSort はコレクションの並び順として受け付ける値か。
func ValidSort(s string) bool { _, ok := collectionOrders[s]; return ok }

func (s *Store) CollectionExhibition(ctx context.Context, c *Collection) (*Exhibition, error) {
	order, ok := collectionOrders[c.Sort]
	if !ok {
		order = collectionOrders["manual"]
	}
	ex := &Exhibition{
		Kind: "collection", Key: c.Slug, Title: c.Title, Subtitle: c.TitleEN,
		Description: c.Description,
		from: `FROM collection_works cw JOIN works w ON w.source_url = cw.source_url
		      WHERE cw.collection_id = ?`,
		args:  []any{c.ID},
		order: order,
	}
	if err := s.count(ctx, ex); err != nil {
		return nil, err
	}
	ex.CoverID = c.CoverID
	if ex.CoverID == nil && ex.Total > 0 {
		first, err := s.Page(ctx, ex, 1, 1)
		if err != nil {
			return nil, err
		}
		ex.CoverID = &first[0].ID
	}
	return ex, nil
}

// creatorOnly は作者展に入れる関与。出版社・刷り師として名前が載っているだけの
// 作品 (non_creator) を、その人の作品として並べない。
const creatorOnly = `(role_bucket IS NULL OR role_bucket <> 'non_creator')`

// ArtistExhibition は作者展。public なら ArtistMinWorks 未満の作者は無いものとして nil を返す。
func (s *Store) ArtistExhibition(ctx context.Context, key string, public bool) (*Exhibition, error) {
	a, err := s.Artist(ctx, key)
	if err != nil || a == nil {
		return nil, err
	}
	if public && a.WorkCount < s.ArtistMinWorks {
		return nil, nil
	}
	ex := &Exhibition{
		Kind: "artist", Key: a.PersonKey, Title: a.DisplayName, Subtitle: a.NameJA,
		Description: a.LifeSpan(),
		from: `FROM works w WHERE w.id IN (
		         SELECT work_id FROM work_artists WHERE person_key = ? AND ` + creatorOnly + `)`,
		args:  []any{a.PersonKey},
		order: `w.year_start IS NULL, w.year_start, w.id`,
		Total: a.WorkCount,
	}
	if ex.Total > 0 {
		first, err := s.Page(ctx, ex, 1, 1)
		if err != nil {
			return nil, err
		}
		if len(first) > 0 {
			ex.CoverID = &first[0].ID
		}
	}
	return ex, nil
}

// Artist は名寄せした人物。
type Artist struct {
	PersonKey   string `json:"person_key"`
	DisplayName string `json:"display_name"`
	NameJA      string `json:"name_ja,omitempty"`
	BirthYear   *int   `json:"birth_year,omitempty"`
	DeathYear   *int   `json:"death_year,omitempty"`
	WorkCount   int    `json:"work_count"`
}

func (a Artist) LifeSpan() string {
	switch {
	case a.BirthYear != nil && a.DeathYear != nil:
		return fmt.Sprintf("%d–%d", *a.BirthYear, *a.DeathYear)
	case a.BirthYear != nil:
		return fmt.Sprintf("%d–", *a.BirthYear)
	case a.DeathYear != nil:
		return fmt.Sprintf("–%d", *a.DeathYear)
	}
	return ""
}

const artistCols = `person_key, display_name, coalesce(name_ja, ''), birth_year, death_year, work_count`

func scanArtist(sc interface{ Scan(...any) error }) (Artist, error) {
	var a Artist
	err := sc.Scan(&a.PersonKey, &a.DisplayName, &a.NameJA, &a.BirthYear, &a.DeathYear, &a.WorkCount)
	return a, err
}

func (s *Store) Artist(ctx context.Context, key string) (*Artist, error) {
	a, err := scanArtist(s.r.QueryRowContext(ctx,
		`SELECT `+artistCols+` FROM artists WHERE person_key = ?`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// SearchArtists は表記と日本語名への部分一致。q が空なら作品数の多い順。
// 6 万人程度なので FTS は張らずに LIKE で済ませる。
func (s *Store) SearchArtists(ctx context.Context, q string, minWorks, limit int) ([]Artist, error) {
	like := "%" + escapeLike(q) + "%"
	rows, err := s.r.QueryContext(ctx, `SELECT `+artistCols+` FROM artists
		WHERE work_count >= ?
		  AND (? = '' OR display_name LIKE ? ESCAPE '\' OR name_ja LIKE ? ESCAPE '\')
		ORDER BY work_count DESC, display_name
		LIMIT ?`, minWorks, q, like, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Artist{}
	for rows.Next() {
		a, err := scanArtist(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func escapeLike(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '%' || r == '_' || r == '\\' {
			out = append(out, '\\')
		}
		out = append(out, r)
	}
	return string(out)
}
