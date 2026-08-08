package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// CountCap は件数表示の上限。1,361,997 件を毎回数え切ると遅いので、ここで
// 打ち切って「10,000+ 件」と出す。
const CountCap = 10000

// SearchParams は作品検索の絞り込み条件。ゼロ値が「絞らない」。
type SearchParams struct {
	Q   string
	Raw bool // Q を FTS5 の式としてそのまま渡す
	// Like は FTS を使わず LIKE で引く。索引に載っていない断片や、
	// 索引を作り直す前の確認に使う。
	Like bool

	Sources   []string
	Style     string // facet の完全一致
	Category  string // 部分一致
	Medium    string // 部分一致
	Origin    string // 部分一致
	Artist    string // 作者名 (FTS の artist 列 / LIKE モードでは images.artist)
	PersonKey string // 名寄せ済み人物の完全一致

	YearFrom  *int
	YearTo    *int
	YearKind  string   // normalized | raw | none | ""
	Precision []string // image_dates.date_precision

	HasJa      string // "1" 訳あり / "0" 訳なし / "" 問わず
	ArtistCond string // unmatched (名寄せ漏れ) | none (作者なし) | ""

	Sort string
	Page int
	Per  int
}

// WorkRow は検索結果 1 行。生の列は hm.db から、派生値は index.db から来る。
type WorkRow struct {
	ID        int64
	Source    string
	SourceID  string
	Title     string
	TitleJa   string
	Artist    string
	Date      string
	Style     string
	Category  string
	Medium    string
	SourceURL string
	ImageURL  string

	YearStart sql.NullInt64
	YearEnd   sql.NullInt64
	YearKind  string
	Precision string
	HasJa     bool
	// 索引が無いときは -1 (不明)。
	ArtistCount  int
	CreatorCount int
	PersonCount  int
}

// YearLabel は制作年の表示用文字列。BCE は負数で入っているので「前」を付ける。
func (w WorkRow) YearLabel() string {
	if !w.YearStart.Valid && !w.YearEnd.Valid {
		return ""
	}
	s, e := w.YearStart, w.YearEnd
	if !s.Valid {
		return "〜" + formatYear(e.Int64)
	}
	if !e.Valid {
		return formatYear(s.Int64) + "〜"
	}
	if s.Int64 == e.Int64 {
		return formatYear(s.Int64)
	}
	return formatYear(s.Int64) + "–" + formatYear(e.Int64)
}

func formatYear(y int64) string {
	if y < 0 {
		return fmt.Sprintf("前%d", -y)
	}
	return fmt.Sprint(y)
}

// SearchResult は結果と、それを出した SQL。データ点検が目的なので実行した
// SQL と所要時間をそのまま画面に出す。
type SearchResult struct {
	Rows    []WorkRow
	Total   int
	Capped  bool // Total が CountCap で打ち切られている
	Page    int
	Per     int
	SQL     string
	Args    []any
	Elapsed time.Duration
	Notes   []string
}

func (r SearchResult) HasPrev() bool { return r.Page > 1 }
func (r SearchResult) HasNext() bool { return len(r.Rows) == r.Per }

type builder struct {
	sel   []string
	from  []string
	where []string
	args  []any
	order string
	notes []string
}

func (b *builder) addWhere(cond string, args ...any) {
	b.where = append(b.where, cond)
	b.args = append(b.args, args...)
}

func like(s string) string { return "%" + s + "%" }

func hasCJK(s string) bool {
	for _, r := range s {
		if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana) {
			return true
		}
	}
	return false
}

// ftsQuote は 1 語を FTS5 の文字列リテラルにする。ユーザー入力に " や AND、
// * が混ざっても構文エラーにしないため、必ず引用符で囲む。
func ftsQuote(tok string) string {
	return `"` + strings.ReplaceAll(tok, `"`, `""`) + `"`
}

type parsedQuery struct {
	Latin  string   // search テーブルに渡す式
	Ja     string   // search_ja テーブルに渡す式
	JaLike []string // trigram に載らない 2 文字以下の日本語語
	Notes  []string
}

// parseQuery は入力を欧文語と日本語語に振り分ける。「Monet 睡蓮」のように
// 混ざっていても、それぞれの索引に投げて AND を取る。
func parseQuery(q string) parsedQuery {
	var p parsedQuery
	var latin, ja []string
	for _, tok := range strings.Fields(q) {
		if hasCJK(tok) {
			if len([]rune(tok)) >= 3 {
				ja = append(ja, ftsQuote(tok))
			} else {
				p.JaLike = append(p.JaLike, tok)
			}
			continue
		}
		// 欧文は前方一致まで許す。"monet" は Monet/Monets の両方に当たる。
		latin = append(latin, ftsQuote(tok)+"*")
	}
	p.Latin = strings.Join(latin, " AND ")
	p.Ja = strings.Join(ja, " AND ")
	if len(p.JaLike) > 0 {
		p.Notes = append(p.Notes,
			fmt.Sprintf("2 文字以下の日本語 (%s) は trigram 索引に載らないため、"+
				"翻訳テキストへの LIKE で照合しています", strings.Join(p.JaLike, ", ")))
	}
	return p
}

// SearchWorks は作品を検索する。索引が無い場合は LIKE にフォールバックする
// ので、`finder index` を実行する前でも一通り使える。
func (s *Store) SearchWorks(ctx context.Context, p SearchParams) (*SearchResult, error) {
	if p.Page < 1 {
		p.Page = 1
	}
	if p.Per < 1 || p.Per > 200 {
		p.Per = 50
	}
	useFTS := s.HasIndex && !p.Like

	b := &builder{}
	b.from = append(b.from, "FROM images i")

	if s.HasIndex {
		b.from = append(b.from, "JOIN "+AttachAlias+".image_meta m ON m.image_id = i.id")
		b.sel = append(b.sel,
			"m.year_start", "m.year_end", "m.year_kind", "m.date_precision",
			"m.has_title_ja", "m.artist_count", "m.creator_count", "m.person_count")
	} else {
		b.notes = append(b.notes,
			"index.db がないため LIKE 検索です。`finder index` を実行すると全文検索と正規化済みの制作年が使えます")
		b.sel = append(b.sel,
			"i.date_raw_start", "i.date_raw_end",
			"CASE WHEN i.date_raw_start IS NOT NULL OR i.date_raw_end IS NOT NULL THEN 'raw' ELSE '' END",
			"''", "CASE WHEN tj.image_id IS NOT NULL THEN 1 ELSE 0 END", "-1", "-1", "-1")
	}
	b.from = append(b.from,
		"LEFT JOIN image_translations tj ON tj.image_id = i.id AND tj.field = 'title' AND tj.lang = 'ja'")

	// 欧文 FTS への条件は 1 本の式にまとめる。同じ FTS 仮想テーブルを別名で
	// 2 回 JOIN しても意図どおりには絞れないため、キーワードと作者名を AND で繋ぐ。
	var latinExpr []string
	var jaExpr []string
	rank := ""

	if q := strings.TrimSpace(p.Q); q != "" {
		switch {
		case !useFTS:
			// LIKE モード: 原題・訳題・作者・説明文を横断する。
			b.addWhere(
				"(i.title LIKE ? OR tj.text LIKE ? OR i.artist LIKE ? OR i.description LIKE ?)",
				like(q), like(q), like(q), like(q))
		case p.Raw:
			latinExpr = append(latinExpr, "("+q+")")
		default:
			pq := parseQuery(q)
			b.notes = append(b.notes, pq.Notes...)
			if pq.Latin != "" {
				latinExpr = append(latinExpr, "("+pq.Latin+")")
			}
			if pq.Ja != "" {
				jaExpr = append(jaExpr, "("+pq.Ja+")")
			}
			for _, tok := range pq.JaLike {
				b.addWhere("tj.text LIKE ?", like(tok))
			}
		}
	}

	if a := strings.TrimSpace(p.Artist); a != "" {
		if useFTS {
			var terms []string
			for _, tok := range strings.Fields(a) {
				if hasCJK(tok) {
					// 作者の日本語名は search_ja 側にある。
					if len([]rune(tok)) >= 3 {
						jaExpr = append(jaExpr, ftsQuote(tok))
					} else {
						b.addWhere("EXISTS (SELECT 1 FROM image_artist_names an "+
							"JOIN image_artists ia ON ia.id = an.image_artist_id "+
							"WHERE ia.image_id = i.id AND an.name LIKE ?)", like(tok))
					}
					continue
				}
				terms = append(terms, "artist:"+ftsQuote(tok)+"*")
			}
			if len(terms) > 0 {
				latinExpr = append(latinExpr, "("+strings.Join(terms, " AND ")+")")
			}
		} else {
			b.addWhere("(i.artist LIKE ? OR EXISTS (SELECT 1 FROM image_artists ia "+
				"WHERE ia.image_id = i.id AND ia.name_raw LIKE ?))", like(a), like(a))
		}
	}

	if len(latinExpr) > 0 {
		b.from = append(b.from, "JOIN "+AttachAlias+".search ON search.rowid = i.id")
		b.addWhere("search MATCH ?", strings.Join(latinExpr, " AND "))
		rank = "search.rank"
	}
	if len(jaExpr) > 0 {
		b.from = append(b.from, "JOIN "+AttachAlias+".search_ja ON search_ja.rowid = i.id")
		b.addWhere("search_ja MATCH ?", strings.Join(jaExpr, " AND "))
		if rank == "" {
			rank = "search_ja.rank"
		}
	}

	if len(p.Sources) > 0 {
		b.addWhere("i.source IN ("+placeholders(len(p.Sources))+")", toAny(p.Sources)...)
	}
	if p.Style != "" {
		b.addWhere("i.style = ?", p.Style)
	}
	if p.Category != "" {
		b.addWhere("i.category LIKE ?", like(p.Category))
	}
	if p.Medium != "" {
		b.addWhere("i.medium LIKE ?", like(p.Medium))
	}
	if p.Origin != "" {
		b.addWhere("i.origin LIKE ?", like(p.Origin))
	}
	if p.PersonKey != "" {
		// EXISTS だと images 側から 136 万行を舐めることになる。IN にすると
		// image_artists_person 索引から入って一気に絞れる。
		b.addWhere("i.id IN (SELECT ia.image_id FROM image_artists ia WHERE ia.person_key = ?)",
			p.PersonKey)
	}

	yearStart, yearEnd := "i.date_raw_start", "i.date_raw_end"
	if s.HasIndex {
		yearStart, yearEnd = "m.year_start", "m.year_end"
	}
	// 期間の指定は「重なり」で判定する。1450–1550 の作品は 1500 年代の検索に出る。
	if p.YearFrom != nil {
		b.addWhere(fmt.Sprintf("coalesce(%s, %s) >= ?", yearEnd, yearStart), *p.YearFrom)
	}
	if p.YearTo != nil {
		b.addWhere(fmt.Sprintf("coalesce(%s, %s) <= ?", yearStart, yearEnd), *p.YearTo)
	}
	if s.HasIndex {
		switch p.YearKind {
		case "normalized", "raw":
			b.addWhere("m.year_kind = ?", p.YearKind)
		case "none":
			b.addWhere("m.year_kind = ''")
		}
		if len(p.Precision) > 0 {
			b.addWhere("m.date_precision IN ("+placeholders(len(p.Precision))+")", toAny(p.Precision)...)
		}
		switch p.HasJa {
		case "1":
			b.addWhere("m.has_title_ja = 1")
		case "0":
			b.addWhere("m.has_title_ja = 0")
		}
		switch p.ArtistCond {
		case "unmatched":
			b.addWhere("m.artist_count > 0 AND m.person_count = 0")
		case "none":
			b.addWhere("m.artist_count = 0")
		}
	} else {
		switch p.HasJa {
		case "1":
			b.addWhere("tj.image_id IS NOT NULL")
		case "0":
			b.addWhere("tj.image_id IS NULL")
		}
	}

	switch p.Sort {
	case "year_asc":
		b.order = "ORDER BY " + yearStart + " IS NULL, " + yearStart + " ASC, i.id"
	case "year_desc":
		b.order = "ORDER BY " + yearStart + " IS NULL, " + yearStart + " DESC, i.id"
	case "title":
		b.order = "ORDER BY i.title COLLATE NOCASE, i.id"
	case "id_desc":
		b.order = "ORDER BY i.id DESC"
	case "id":
		b.order = "ORDER BY i.id"
	default: // relevance
		if rank != "" {
			b.order = "ORDER BY " + rank
		} else {
			b.order = "ORDER BY i.id"
		}
	}

	cols := append([]string{
		"i.id", "i.source", "coalesce(i.source_id, '')", "coalesce(i.title, '')",
		"coalesce(tj.text, '')", "coalesce(i.artist, '')", "coalesce(i.date, '')",
		"coalesce(i.style, '')", "coalesce(i.category, '')", "coalesce(i.medium, '')",
		"i.source_url", "coalesce(i.image_url, '')",
	}, b.sel...)

	where := ""
	if len(b.where) > 0 {
		where = "WHERE " + strings.Join(b.where, "\n  AND ")
	}
	body := strings.Join(b.from, "\n") + "\n" + where

	query := "SELECT " + strings.Join(cols, ", ") + "\n" + body + "\n" + b.order +
		fmt.Sprintf("\nLIMIT %d OFFSET %d", p.Per, (p.Page-1)*p.Per)

	start := time.Now()
	rows, err := s.DB.QueryContext(ctx, query, b.args...)
	if err != nil {
		return nil, fmt.Errorf("検索に失敗しました: %w\n\n%s", err, query)
	}
	defer rows.Close()

	res := &SearchResult{Page: p.Page, Per: p.Per, SQL: query, Args: b.args, Notes: b.notes}
	for rows.Next() {
		var w WorkRow
		var hasJa int
		if err := rows.Scan(&w.ID, &w.Source, &w.SourceID, &w.Title, &w.TitleJa, &w.Artist,
			&w.Date, &w.Style, &w.Category, &w.Medium, &w.SourceURL, &w.ImageURL,
			&w.YearStart, &w.YearEnd, &w.YearKind, &w.Precision,
			&hasJa, &w.ArtistCount, &w.CreatorCount, &w.PersonCount); err != nil {
			return nil, err
		}
		w.HasJa = hasJa == 1
		res.Rows = append(res.Rows, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	res.Elapsed = time.Since(start)

	countSQL := fmt.Sprintf("SELECT count(*) FROM (SELECT 1\n%s\nLIMIT %d)", body, CountCap+1)
	if err := s.DB.QueryRowContext(ctx, countSQL, b.args...).Scan(&res.Total); err != nil {
		return nil, err
	}
	if res.Total > CountCap {
		res.Total = CountCap
		res.Capped = true
	}
	return res, nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func toAny[T any](in []T) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}
