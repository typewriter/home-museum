package web

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/typewriter/home-museum/finder/internal/store"
)

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	p := store.SearchParams{
		Q:          q(r, "q"),
		Raw:        qBool(r, "raw"),
		Like:       qBool(r, "like"),
		Sources:    qList(r, "source"),
		Style:      q(r, "style"),
		Category:   q(r, "category"),
		Medium:     q(r, "medium"),
		Origin:     q(r, "origin"),
		Artist:     q(r, "artist"),
		PersonKey:  q(r, "person_key"),
		YearFrom:   qIntPtr(r, "year_from"),
		YearTo:     qIntPtr(r, "year_to"),
		YearKind:   q(r, "year_kind"),
		Precision:  qList(r, "precision"),
		HasJa:      q(r, "has_ja"),
		ArtistCond: q(r, "artist_cond"),
		Sort:       q(r, "sort"),
		Page:       qInt(r, "page", 1),
		Per:        qInt(r, "per", 50),
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.queryWait)
	defer cancel()

	res, err := s.st.SearchWorks(ctx, p)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, err)
		return
	}

	// 名寄せ済み人物で絞っているときは、誰なのかを見出しに出す。
	var person *store.ArtistRow
	if p.PersonKey != "" {
		if d, err := s.st.ArtistDetail(ctx, p.PersonKey); err == nil && d.Found {
			person = &d.Artist
		}
	}

	s.render(w, r, "search.html", "作品検索", "search", map[string]any{
		"P":      p,
		"Res":    res,
		"Facets": s.facets(ctx),
		"Query":  r.URL.Query(),
		"Person": person,
		// 一覧のサムネは既定で出さない。1 ページ 50 件を一度に要求すると
		// 館ごと 10 秒では埋まるのに数分かかる (spec_image_cache.md §8)。
		"Thumbs": s.cache != nil && qBool(r, "thumbs"),
	})
}

func (s *Server) handleWork(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, errors.New("作品 ID が不正です"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.queryWait)
	defer cancel()

	d, err := s.st.WorkDetail(ctx, id)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err)
		return
	}
	if d == nil {
		s.fail(w, r, http.StatusNotFound, errors.New("その ID の作品は hm.db にありません"))
		return
	}
	title := d.Title
	if d.TitleJa != "" {
		title = d.TitleJa
	}
	s.render(w, r, "work.html", title, "search", d)
}

func (s *Server) handleArtists(w http.ResponseWriter, r *http.Request) {
	p := store.ArtistParams{
		Q:       q(r, "q"),
		KeyKind: q(r, "kind"),
		MinN:    qInt(r, "min", 0),
		Sort:    q(r, "sort"),
		Page:    qInt(r, "page", 1),
		Per:     qInt(r, "per", 50),
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.queryWait)
	defer cancel()

	res, err := s.st.SearchArtists(ctx, p)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err)
		return
	}
	s.render(w, r, "artists.html", "作者", "artists", map[string]any{
		"P": p, "Res": res, "Query": r.URL.Query(),
	})
}

func (s *Server) handleArtist(w http.ResponseWriter, r *http.Request) {
	key := q(r, "key")
	if key == "" {
		http.Redirect(w, r, "/artists/", http.StatusSeeOther)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.queryWait)
	defer cancel()

	d, err := s.st.ArtistDetail(ctx, key)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err)
		return
	}
	s.render(w, r, "artist.html", d.Artist.DisplayName, "artists", d)
}

func (s *Server) handleUnmatched(w http.ResponseWriter, r *http.Request) {
	page := qInt(r, "page", 1)
	per := qInt(r, "per", 50)
	ctx, cancel := context.WithTimeout(r.Context(), s.queryWait)
	defer cancel()

	t, err := s.st.UnmatchedArtists(ctx, q(r, "q"), page, per)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err)
		return
	}
	s.render(w, r, "unmatched.html", "名寄せ漏れの作者表記", "artists", map[string]any{
		"T": t, "Q": q(r, "q"), "Page": page, "Per": per,
		"HasNext": len(t.Rows) == per, "Query": r.URL.Query(),
	})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()

	st, at, err := s.stats(ctx, qBool(r, "refresh"))
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err)
		return
	}
	data := map[string]any{"S": st, "ComputedAt": at}
	// 画像キャッシュの状況は cache.db だけ見るので軽い。毎回数える。
	if s.cache != nil {
		if cs, err := s.cache.Stats(ctx); err != nil {
			log.Printf("画像キャッシュの集計に失敗: %v", err)
		} else {
			data["Cache"] = cs
			data["CacheDesc"] = s.cache.Describe()
		}
	}
	s.render(w, r, "stats.html", "カバレッジ", "stats", data)
}

func (s *Server) handleSQL(w http.ResponseWriter, r *http.Request) {
	if !s.allowSQL {
		s.fail(w, r, http.StatusNotFound, errors.New("SQL コンソールは無効です (-sql=false)"))
		return
	}
	query := r.URL.Query().Get("sql")
	data := map[string]any{"SQL": query, "Limit": 500}

	if strings.TrimSpace(query) != "" {
		ctx, cancel := context.WithTimeout(r.Context(), s.queryWait)
		defer cancel()
		t, err := s.st.Query(ctx, 500, query)
		if err != nil {
			data["Error"] = err.Error()
		} else {
			data["T"] = t
		}
	}
	s.render(w, r, "sql.html", "SQL", "sql", data)
}

// stats はカバレッジ計算をキャッシュする。images と image_artists の全走査を
// 含むので、リロードのたびに数えると待たされる。
func (s *Server) stats(ctx context.Context, refresh bool) (*store.Stats, time.Time, error) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	if !refresh && s.statsVal != nil && time.Since(s.statsAt) < 10*time.Minute {
		return s.statsVal, s.statsAt, nil
	}
	st, err := s.st.Stats(ctx)
	if err != nil {
		return nil, time.Time{}, err
	}
	s.statsVal, s.statsAt = st, time.Now()
	return st, s.statsAt, nil
}

// urlWith は現在のクエリ文字列を土台に、指定したキーだけ差し替えた URL を返す。
// ページ送りや並べ替えのリンクに使う。値が空ならそのキーを落とす。
func urlWith(base url.Values, pairs ...string) string {
	v := url.Values{}
	for k, vs := range base {
		v[k] = append([]string(nil), vs...)
	}
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] == "" {
			v.Del(pairs[i])
		} else {
			v.Set(pairs[i], pairs[i+1])
		}
	}
	if len(v) == 0 {
		return "?"
	}
	return "?" + v.Encode()
}
