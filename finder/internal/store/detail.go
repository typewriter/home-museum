package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Cell は 1 セル。NULL と空文字を区別して表示したいので分けて持つ
// (hm.db は両方が混ざっており、どちらなのかは点検で知りたい情報)。
type Cell struct {
	Null bool
	Text string
}

// Table は列名つきの汎用の結果表。作品詳細の生ダンプと SQL コンソールで共用する。
type Table struct {
	Cols    []string
	Rows    [][]Cell
	Elapsed time.Duration
	// Truncated は limit で打ち切られたことを示す。
	Truncated bool
}

func (t *Table) Empty() bool { return t == nil || len(t.Rows) == 0 }

// Query は任意の SELECT を実行して Table に読む。接続は mode=ro なので、
// 書き込み文はドライバの手前で SQLite に弾かれる。
func (s *Store) Query(ctx context.Context, limit int, query string, args ...any) (*Table, error) {
	start := time.Now()
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	t := &Table{Cols: cols}
	buf := make([]any, len(cols))
	ptr := make([]any, len(cols))
	for i := range buf {
		ptr[i] = &buf[i]
	}
	for rows.Next() {
		if limit > 0 && len(t.Rows) >= limit {
			t.Truncated = true
			break
		}
		if err := rows.Scan(ptr...); err != nil {
			return nil, err
		}
		row := make([]Cell, len(cols))
		for i, v := range buf {
			row[i] = toCell(v)
		}
		t.Rows = append(t.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	t.Elapsed = time.Since(start)
	return t, nil
}

func toCell(v any) Cell {
	switch x := v.(type) {
	case nil:
		return Cell{Null: true}
	case []byte:
		return Cell{Text: string(x)}
	case string:
		return Cell{Text: x}
	case int64:
		return Cell{Text: strconv.FormatInt(x, 10)}
	case float64:
		return Cell{Text: strconv.FormatFloat(x, 'g', -1, 64)}
	case bool:
		return Cell{Text: strconv.FormatBool(x)}
	case time.Time:
		return Cell{Text: x.Format(time.RFC3339)}
	default:
		return Cell{Text: fmt.Sprint(x)}
	}
}

// WorkDetail は 1 作品について hm.db にある行を全部集めたもの。
// 「どのテーブルに何が入っていて、どこが空か」を見るのが目的なので、
// 列を選ばずそのまま出す。
type WorkDetail struct {
	ID        int64
	Title     string
	TitleJa   string
	Source    string
	SourceURL string
	ImageURL  string

	Image        *Table
	Artists      *Table
	Dates        *Table
	Translations *Table
	ArtistNames  *Table
	Meta         *Table // index.db 側の解決済みの値
	// People はこの作品の作者エントリが寄せられた人物。生の表 (Artists) には
	// リンクを張れないので、辿れるように別に持つ。
	People []ArtistRow
}

func (s *Store) WorkDetail(ctx context.Context, id int64) (*WorkDetail, error) {
	d := &WorkDetail{ID: id}
	var titleJa sql.NullString
	err := s.DB.QueryRowContext(ctx, `
		SELECT i.source, coalesce(i.title, ''), i.source_url, coalesce(i.image_url, ''), tj.text
		  FROM images i
		  LEFT JOIN image_translations tj
		         ON tj.image_id = i.id AND tj.field = 'title' AND tj.lang = 'ja'
		 WHERE i.id = ?`, id).
		Scan(&d.Source, &d.Title, &d.SourceURL, &d.ImageURL, &titleJa)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.TitleJa = nullStr(titleJa)

	if d.Image, err = s.Query(ctx, 0, `SELECT * FROM images WHERE id = ?`, id); err != nil {
		return nil, err
	}
	if d.Artists, err = s.Query(ctx, 0,
		`SELECT * FROM image_artists WHERE image_id = ? ORDER BY position`, id); err != nil {
		return nil, err
	}
	if d.Dates, err = s.Query(ctx, 0, `SELECT * FROM image_dates WHERE image_id = ?`, id); err != nil {
		return nil, err
	}
	if d.Translations, err = s.Query(ctx, 0,
		`SELECT * FROM image_translations WHERE image_id = ? ORDER BY field, lang`, id); err != nil {
		return nil, err
	}
	if d.ArtistNames, err = s.Query(ctx, 0, `
		SELECT n.* FROM image_artist_names n
		  JOIN image_artists ia ON ia.id = n.image_artist_id
		 WHERE ia.image_id = ? ORDER BY ia.position, n.lang`, id); err != nil {
		return nil, err
	}
	if s.HasIndex {
		if d.Meta, err = s.Query(ctx, 0,
			`SELECT * FROM `+AttachAlias+`.image_meta WHERE image_id = ?`, id); err != nil {
			return nil, err
		}
	}

	prows, err := s.DB.QueryContext(ctx, `
		SELECT DISTINCT ia.person_key, coalesce(a.display_name, ia.name_raw), a.image_count
		  FROM image_artists ia
		  LEFT JOIN artists a ON a.person_key = ia.person_key
		 WHERE ia.image_id = ? AND ia.person_key IS NOT NULL
		 ORDER BY ia.position`, id)
	if err != nil {
		return nil, err
	}
	defer prows.Close()
	for prows.Next() {
		var a ArtistRow
		var n sql.NullInt64
		if err := prows.Scan(&a.PersonKey, &a.DisplayName, &n); err != nil {
			return nil, err
		}
		a.ImageCount = n.Int64
		d.People = append(d.People, a)
	}
	if err := prows.Err(); err != nil {
		return nil, err
	}
	return d, nil
}

// ImageSource は画像キャッシュが 1 枚を取りに行くのに要る最小限の情報。
type ImageSource struct {
	ID        int64
	Source    string
	SourceURL string
	ImageURL  string
}

// ImageSource は id から取得元を引く。見つからなければ (nil, nil)。
func (s *Store) ImageSource(ctx context.Context, id int64) (*ImageSource, error) {
	var r ImageSource
	var img sql.NullString
	err := s.DB.QueryRowContext(ctx,
		`SELECT id, source, source_url, image_url FROM images WHERE id = ?`, id).
		Scan(&r.ID, &r.Source, &r.SourceURL, &img)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.ImageURL = nullStr(img)
	return &r, nil
}

// ArtistRow は作者一覧の 1 行。
type ArtistRow struct {
	PersonKey   string
	DisplayName string
	BirthYear   sql.NullInt64
	DeathYear   sql.NullInt64
	ImageCount  int64
	EntryCount  int64
}

// KeyKind は person_key の出どころ。ulan / wd は外部典拠、cluster は
// finder から見て「機械的に寄せた」もの。信頼度が違うので一覧で区別する。
func (a ArtistRow) KeyKind() string {
	if i := strings.IndexByte(a.PersonKey, ':'); i > 0 {
		return a.PersonKey[:i]
	}
	return "?"
}

func (a ArtistRow) LifeSpan() string {
	if !a.BirthYear.Valid && !a.DeathYear.Valid {
		return ""
	}
	b, d := "", ""
	if a.BirthYear.Valid {
		b = formatYear(a.BirthYear.Int64)
	}
	if a.DeathYear.Valid {
		d = formatYear(a.DeathYear.Int64)
	}
	return b + "–" + d
}

// ArtistParams は作者一覧の絞り込み。
type ArtistParams struct {
	Q       string
	KeyKind string // ulan | wd | cluster | ""
	MinN    int
	Sort    string // count | name
	Page    int
	Per     int
}

type ArtistResult struct {
	Rows    []ArtistRow
	Total   int
	Capped  bool
	Page    int
	Per     int
	SQL     string
	Elapsed time.Duration
}

func (r ArtistResult) HasPrev() bool { return r.Page > 1 }
func (r ArtistResult) HasNext() bool { return len(r.Rows) == r.Per }

func (s *Store) SearchArtists(ctx context.Context, p ArtistParams) (*ArtistResult, error) {
	if p.Page < 1 {
		p.Page = 1
	}
	if p.Per < 1 || p.Per > 200 {
		p.Per = 50
	}
	var where []string
	var args []any
	if q := strings.TrimSpace(p.Q); q != "" {
		where = append(where, "a.display_name LIKE ?")
		args = append(args, like(q))
	}
	if p.KeyKind != "" {
		where = append(where, "a.person_key LIKE ?")
		args = append(args, p.KeyKind+":%")
	}
	if p.MinN > 0 {
		where = append(where, "a.image_count >= ?")
		args = append(args, p.MinN)
	}
	cond := ""
	if len(where) > 0 {
		cond = "WHERE " + strings.Join(where, " AND ")
	}
	order := "ORDER BY a.image_count DESC, a.display_name"
	if p.Sort == "name" {
		order = "ORDER BY a.display_name COLLATE NOCASE"
	}

	query := fmt.Sprintf(`SELECT a.person_key, a.display_name, a.birth_year, a.death_year, a.image_count,
       (SELECT count(*) FROM image_artists ia WHERE ia.person_key = a.person_key)
  FROM artists a
%s
%s
LIMIT %d OFFSET %d`, cond, order, p.Per, (p.Page-1)*p.Per)

	start := time.Now()
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	res := &ArtistResult{Page: p.Page, Per: p.Per, SQL: query}
	for rows.Next() {
		var a ArtistRow
		if err := rows.Scan(&a.PersonKey, &a.DisplayName, &a.BirthYear, &a.DeathYear,
			&a.ImageCount, &a.EntryCount); err != nil {
			return nil, err
		}
		res.Rows = append(res.Rows, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	res.Elapsed = time.Since(start)

	countSQL := fmt.Sprintf("SELECT count(*) FROM (SELECT 1 FROM artists a %s LIMIT %d)", cond, CountCap+1)
	if err := s.DB.QueryRowContext(ctx, countSQL, args...).Scan(&res.Total); err != nil {
		return nil, err
	}
	if res.Total > CountCap {
		res.Total = CountCap
		res.Capped = true
	}
	return res, nil
}

// ArtistDetail は名寄せ結果 1 件の中身。どの生表記が寄せられているかを
// 見せるのが主目的なので、name_raw × 判定手法で数える。
type ArtistDetail struct {
	Artist  ArtistRow
	Found   bool
	Entries *Table // 寄せられた生表記の内訳
	Sources *Table // ソース別の作品数
}

func (s *Store) ArtistDetail(ctx context.Context, key string) (*ArtistDetail, error) {
	d := &ArtistDetail{}
	err := s.DB.QueryRowContext(ctx,
		`SELECT person_key, display_name, birth_year, death_year, image_count FROM artists WHERE person_key = ?`,
		key).Scan(&d.Artist.PersonKey, &d.Artist.DisplayName, &d.Artist.BirthYear,
		&d.Artist.DeathYear, &d.Artist.ImageCount)
	switch {
	case err == sql.ErrNoRows:
		// 名寄せ結果に無い person_key でも、エントリ側は見られるようにする。
		d.Artist.PersonKey = key
		d.Artist.DisplayName = key
	case err != nil:
		return nil, err
	default:
		d.Found = true
	}

	if d.Entries, err = s.Query(ctx, 500, `
		SELECT ia.name_raw, coalesce(ia.role_raw, '') AS role_raw,
		       coalesce(ia.role_bucket, '') AS role_bucket,
		       coalesce(ia.qualifier_raw, '') AS qualifier_raw,
		       coalesce(ia.match_method, '') AS match_method,
		       coalesce(ia.match_confidence, '') AS match_confidence,
		       count(*) AS n
		  FROM image_artists ia
		 WHERE ia.person_key = ?
		 GROUP BY 1, 2, 3, 4, 5, 6
		 ORDER BY n DESC`, key); err != nil {
		return nil, err
	}
	if d.Sources, err = s.Query(ctx, 0, `
		SELECT i.source, count(DISTINCT i.id) AS n
		  FROM image_artists ia JOIN images i ON i.id = ia.image_id
		 WHERE ia.person_key = ?
		 GROUP BY 1 ORDER BY n DESC`, key); err != nil {
		return nil, err
	}
	d.Artist.EntryCount = 0
	for _, r := range d.Entries.Rows {
		if n, err := strconv.ParseInt(r[len(r)-1].Text, 10, 64); err == nil {
			d.Artist.EntryCount += n
		}
	}
	return d, nil
}

// UnmatchedArtists は person_key が付いていない作者エントリを生表記でまとめる。
// 名寄せの取りこぼしを件数の多い順に見るための画面。
func (s *Store) UnmatchedArtists(ctx context.Context, q string, page, per int) (*Table, error) {
	if page < 1 {
		page = 1
	}
	if per < 1 || per > 200 {
		per = 50
	}
	where := "WHERE ia.person_key IS NULL"
	var args []any
	if q = strings.TrimSpace(q); q != "" {
		where += " AND ia.name_raw LIKE ?"
		args = append(args, like(q))
	}
	return s.Query(ctx, 0, fmt.Sprintf(`
		SELECT ia.name_raw,
		       coalesce(ia.role_bucket, '') AS role_bucket,
		       count(*) AS entries,
		       count(DISTINCT i.source) AS sources,
		       group_concat(DISTINCT i.source) AS source_list
		  FROM image_artists ia JOIN images i ON i.id = ia.image_id
		%s
		 GROUP BY 1, 2
		 ORDER BY entries DESC
		 LIMIT %d OFFSET %d`, where, per, (page-1)*per), args...)
}

// FacetValue は絞り込みの選択肢 1 件。
type FacetValue struct {
	Value string
	N     int64
}

// Facets は index.db に焼いた選択肢を読む。索引が無いときは source だけ
// hm.db から直接数える (462 件ある style を毎回 DISTINCT すると遅いため)。
func (s *Store) Facets(ctx context.Context) (map[string][]FacetValue, error) {
	out := map[string][]FacetValue{}
	if !s.HasIndex {
		rows, err := s.DB.QueryContext(ctx, `SELECT source, count(*) FROM images GROUP BY source ORDER BY 2 DESC`)
		if err != nil {
			return out, err
		}
		defer rows.Close()
		for rows.Next() {
			var f FacetValue
			if err := rows.Scan(&f.Value, &f.N); err != nil {
				return out, err
			}
			out["source"] = append(out["source"], f)
		}
		return out, rows.Err()
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT kind, value, n FROM `+AttachAlias+`.facet ORDER BY kind, n DESC`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var f FacetValue
		if err := rows.Scan(&kind, &f.Value, &f.N); err != nil {
			return out, err
		}
		out[kind] = append(out[kind], f)
	}
	return out, rows.Err()
}
