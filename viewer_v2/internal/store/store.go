// Package store は viewer.db への問い合わせをまとめる。公開 API と管理 API の両方が使う。
package store

import (
	"database/sql"

	"github.com/typewriter/home-museum/viewer_v2/internal/db"
)

type Store struct {
	r, w *sql.DB
	// ArtistMinWorks 未満の作者は公開しない。名寄せの取りこぼしで 1〜2 作品しかない
	// 「作者」が大量にいて、展覧会として見せられる量にならないため。
	ArtistMinWorks int
}

func New(d *db.DB, artistMinWorks int) *Store {
	return &Store{r: d.R, w: d.W, ArtistMinWorks: artistMinWorks}
}

// WorkCard は図録グリッドの 1 枚。
type WorkCard struct {
	ID       int64  `json:"id"`
	Title    string `json:"title,omitempty"`
	TitleJA  string `json:"title_ja,omitempty"`
	Artist   string `json:"artist,omitempty"`
	DateText string `json:"date,omitempty"`
}

// Work は展示室のキャプションに出す全項目。
type Work struct {
	WorkCard
	Source      string `json:"source"`
	SourceURL   string `json:"source_url"`
	YearStart   *int   `json:"year_start,omitempty"`
	Medium      string `json:"medium,omitempty"`
	Dimensions  string `json:"dimensions,omitempty"`
	Credit      string `json:"credit,omitempty"`
	Description string `json:"description,omitempty"`
}

const cardCols = `w.id, coalesce(w.title, ''), coalesce(w.title_ja, ''), coalesce(w.artist, ''), coalesce(w.date_text, '')`

const workCols = cardCols + `, w.source, w.source_url, w.year_start,
	coalesce(w.medium, ''), coalesce(w.dimensions, ''), coalesce(w.credit, ''), coalesce(w.description, '')`

func scanCard(sc interface{ Scan(...any) error }) (WorkCard, error) {
	var c WorkCard
	err := sc.Scan(&c.ID, &c.Title, &c.TitleJA, &c.Artist, &c.DateText)
	return c, err
}

func scanWork(sc interface{ Scan(...any) error }) (Work, error) {
	var w Work
	err := sc.Scan(&w.ID, &w.Title, &w.TitleJA, &w.Artist, &w.DateText,
		&w.Source, &w.SourceURL, &w.YearStart,
		&w.Medium, &w.Dimensions, &w.Credit, &w.Description)
	return w, err
}

func scanCards(rows *sql.Rows, err error) ([]WorkCard, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WorkCard{}
	for rows.Next() {
		c, err := scanCard(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
