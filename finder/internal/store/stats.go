package store

import (
	"context"
	"time"
)

// SourceStat はソース 1 つぶんのカバレッジ。派生層 (image_dates,
// image_translations, role_bucket, person_key) がどこまで埋まったかを見る。
type SourceStat struct {
	Source     string
	Images     int64
	DateRaw    int64 // 館が構造化して持っていた年
	DateNorm   int64 // image_dates
	TitleJa    int64 // image_translations(field='title', lang='ja')
	Entries    int64 // image_artists の行数
	RoleBucket int64
	PersonKey  int64
	ArtistJa   int64 // image_artist_names(lang='ja')
}

// Pct は Images に対する割合。作者系の 3 つは Entries が母数。
func pct(n, d int64) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) * 100 / float64(d)
}

func (s SourceStat) PctDateRaw() float64  { return pct(s.DateRaw, s.Images) }
func (s SourceStat) PctDateNorm() float64 { return pct(s.DateNorm, s.Images) }
func (s SourceStat) PctTitleJa() float64  { return pct(s.TitleJa, s.Images) }
func (s SourceStat) PctRole() float64     { return pct(s.RoleBucket, s.Entries) }
func (s SourceStat) PctPerson() float64   { return pct(s.PersonKey, s.Entries) }
func (s SourceStat) PctArtistJa() float64 { return pct(s.ArtistJa, s.Entries) }

// Stats はカバレッジ画面ぶんのデータ一式。
type Stats struct {
	Sources    []SourceStat
	Totals     SourceStat
	Precision  *Table
	RoleBucket *Table
	Centuries  *Table
	KeyKinds   *Table
	Index      IndexStatus
	Elapsed    time.Duration
}

// Stats はカバレッジを数える。images 全走査が数回入るので数秒かかる。
func (s *Store) Stats(ctx context.Context) (*Stats, error) {
	start := time.Now()
	out := &Stats{}

	byName := map[string]*SourceStat{}
	get := func(name string) *SourceStat {
		if v, ok := byName[name]; ok {
			return v
		}
		v := &SourceStat{Source: name}
		byName[name] = v
		return v
	}

	rows, err := s.DB.QueryContext(ctx, `
		SELECT i.source,
		       count(*),
		       sum(CASE WHEN i.date_raw_start IS NOT NULL OR i.date_raw_end IS NOT NULL THEN 1 ELSE 0 END),
		       sum(CASE WHEN d.image_id IS NOT NULL THEN 1 ELSE 0 END),
		       sum(CASE WHEN tj.image_id IS NOT NULL THEN 1 ELSE 0 END)
		  FROM images i
		  LEFT JOIN image_dates d ON d.image_id = i.id
		  LEFT JOIN image_translations tj
		         ON tj.image_id = i.id AND tj.field = 'title' AND tj.lang = 'ja'
		 GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name string
		var images, raw, norm, ja int64
		if err := rows.Scan(&name, &images, &raw, &norm, &ja); err != nil {
			rows.Close()
			return nil, err
		}
		st := get(name)
		st.Images, st.DateRaw, st.DateNorm, st.TitleJa = images, raw, norm, ja
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = s.DB.QueryContext(ctx, `
		SELECT i.source,
		       count(*),
		       sum(CASE WHEN ia.role_bucket IS NOT NULL THEN 1 ELSE 0 END),
		       sum(CASE WHEN ia.person_key IS NOT NULL THEN 1 ELSE 0 END),
		       sum(CASE WHEN an.image_artist_id IS NOT NULL THEN 1 ELSE 0 END)
		  FROM image_artists ia
		  JOIN images i ON i.id = ia.image_id
		  LEFT JOIN image_artist_names an
		         ON an.image_artist_id = ia.id AND an.lang = 'ja'
		 GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name string
		var entries, role, person, ja int64
		if err := rows.Scan(&name, &entries, &role, &person, &ja); err != nil {
			rows.Close()
			return nil, err
		}
		st := get(name)
		st.Entries, st.RoleBucket, st.PersonKey, st.ArtistJa = entries, role, person, ja
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, st := range byName {
		out.Sources = append(out.Sources, *st)
		out.Totals.Images += st.Images
		out.Totals.DateRaw += st.DateRaw
		out.Totals.DateNorm += st.DateNorm
		out.Totals.TitleJa += st.TitleJa
		out.Totals.Entries += st.Entries
		out.Totals.RoleBucket += st.RoleBucket
		out.Totals.PersonKey += st.PersonKey
		out.Totals.ArtistJa += st.ArtistJa
	}
	out.Totals.Source = "合計"
	// 件数の多い順に並べる。
	for i := 1; i < len(out.Sources); i++ {
		for j := i; j > 0 && out.Sources[j].Images > out.Sources[j-1].Images; j-- {
			out.Sources[j], out.Sources[j-1] = out.Sources[j-1], out.Sources[j]
		}
	}

	if out.Precision, err = s.Query(ctx, 0, `
		SELECT date_precision, count(*) AS n FROM image_dates GROUP BY 1 ORDER BY n DESC`); err != nil {
		return nil, err
	}
	if out.RoleBucket, err = s.Query(ctx, 0, `
		SELECT coalesce(role_bucket, '(未分類)') AS role_bucket, count(*) AS n
		  FROM image_artists GROUP BY 1 ORDER BY n DESC`); err != nil {
		return nil, err
	}
	if out.KeyKinds, err = s.Query(ctx, 0, `
		SELECT CASE WHEN person_key IS NULL THEN '(未名寄せ)'
		            ELSE substr(person_key, 1, instr(person_key, ':') - 1) END AS kind,
		       count(*) AS entries, count(DISTINCT person_key) AS people
		  FROM image_artists GROUP BY 1 ORDER BY entries DESC`); err != nil {
		return nil, err
	}

	// 世紀ごとの分布。index.db があれば正規化済みの年を、無ければ館の生の年を使う。
	centurySQL := `
		SELECT CASE WHEN year_start IS NULL THEN '(年なし)'
		            ELSE CAST(CAST(year_start / 100.0 AS INT) * 100 AS TEXT) END AS century,
		       count(*) AS n
		  FROM ` + AttachAlias + `.image_meta GROUP BY 1 ORDER BY 1`
	if !s.HasIndex {
		centurySQL = `
		SELECT CASE WHEN date_raw_start IS NULL THEN '(年なし)'
		            ELSE CAST(CAST(date_raw_start / 100.0 AS INT) * 100 AS TEXT) END AS century,
		       count(*) AS n
		  FROM images GROUP BY 1 ORDER BY 1`
	}
	if out.Centuries, err = s.Query(ctx, 0, centurySQL); err != nil {
		return nil, err
	}

	if out.Index, err = s.IndexStatus(ctx); err != nil {
		return nil, err
	}
	out.Elapsed = time.Since(start)
	return out, nil
}
