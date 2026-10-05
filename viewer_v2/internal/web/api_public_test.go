package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
)

func getJSON(t *testing.T, f *fixture, path string, wantCode int, v any) {
	t.Helper()
	rec := f.get(t, path)
	if rec.Code != wantCode {
		t.Fatalf("%s: code = %d, want %d: %s", path, rec.Code, wantCode, rec.Body)
	}
	if v != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
}

type pageResp struct {
	Exhibition struct {
		Title   string `json:"title"`
		Total   int    `json:"total"`
		CoverID *int64 `json:"cover_id"`
	} `json:"exhibition"`
	Works []struct {
		ID int64 `json:"id"`
	} `json:"works"`
}

func ids(p pageResp) []int64 {
	var out []int64
	for _, w := range p.Works {
		out = append(out, w.ID)
	}
	return out
}

func equal(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPublicCollections(t *testing.T) {
	f := newDBFixture(t)

	var list struct {
		Collections []struct {
			Slug  string `json:"slug"`
			Count int    `json:"count"`
		} `json:"collections"`
	}
	getJSON(t, f, "/api/collections", 200, &list)
	if len(list.Collections) != 1 || list.Collections[0].Slug != "pub" || list.Collections[0].Count != 2 {
		t.Errorf("公開中の 1 本だけ、引けるメンバー 2 件: %+v", list.Collections)
	}

	getJSON(t, f, "/api/collections/draft", 404, nil)
	getJSON(t, f, "/api/collections/draft/works/1", 404, nil)

	var p pageResp
	getJSON(t, f, "/api/collections/pub", 200, &p)
	if !equal(ids(p), []int64{2, 1}) || p.Exhibition.Total != 2 {
		t.Errorf("手動順で引けるものだけ: %v total=%d", ids(p), p.Exhibition.Total)
	}
	if p.Exhibition.CoverID == nil || *p.Exhibition.CoverID != 2 {
		t.Errorf("cover_url が無ければ先頭の作品: %v", p.Exhibition.CoverID)
	}

	var room struct {
		Room struct {
			N      int    `json:"n"`
			PrevID *int64 `json:"prev_id"`
			NextID *int64 `json:"next_id"`
			Work   struct {
				ID      int64  `json:"id"`
				TitleJA string `json:"title_ja"`
			} `json:"work"`
		} `json:"room"`
	}
	getJSON(t, f, "/api/collections/pub/works/2", 200, &room)
	if room.Room.Work.ID != 1 || room.Room.Work.TitleJA != "睡蓮" ||
		room.Room.PrevID == nil || *room.Room.PrevID != 2 || room.Room.NextID != nil {
		t.Errorf("room = %+v", room.Room)
	}
	getJSON(t, f, "/api/collections/pub/works/3", 404, nil)
	getJSON(t, f, "/api/collections/pub/works/0", 404, nil)
}

func TestCollectionSortByYear(t *testing.T) {
	f := newDBFixture(t)
	mustExec(t, f.db, `UPDATE collections SET sort = 'year_desc' WHERE slug = 'pub'`)
	var p pageResp
	getJSON(t, f, "/api/collections/pub", 200, &p)
	if !equal(ids(p), []int64{1, 2}) {
		t.Errorf("年の新しい順、年不明は最後: %v", ids(p))
	}
}

func TestPublicArtists(t *testing.T) {
	f := newDBFixture(t)

	var p pageResp
	getJSON(t, f, "/api/artists/ulan:1", 200, &p)
	if !equal(ids(p), []int64{3, 1, 2}) {
		t.Errorf("年の古い順、年不明は最後: %v", ids(p))
	}

	// 閾値 (2) 未満は公開しない。non_creator の関与は数えない。
	getJSON(t, f, "/api/artists/ulan:2", 404, nil)
	getJSON(t, f, "/api/artists/nobody", 404, nil)

	// person_key は '/' を含みうる。エスケープすれば 1 区切りとして届く。
	key := url.PathEscape("cluster:r|https://id/2101")
	getJSON(t, f, "/api/artists/"+key, 200, &p)
	if !equal(ids(p), []int64{4, 5}) {
		t.Errorf("'/' を含むキー: %v", ids(p))
	}
	getJSON(t, f, "/api/artists/"+key+"/works/2", 200, nil)

	var s struct {
		Artists []struct {
			PersonKey string `json:"person_key"`
		} `json:"artists"`
	}
	getJSON(t, f, "/api/artists?q="+url.QueryEscape("モネ"), 200, &s)
	if len(s.Artists) != 1 || s.Artists[0].PersonKey != "ulan:1" {
		t.Errorf("日本語名で引ける: %+v", s.Artists)
	}
	getJSON(t, f, "/api/artists?q=publisher", 200, &s)
	if len(s.Artists) != 0 {
		t.Errorf("閾値未満は検索にも出ない: %+v", s.Artists)
	}
}

func TestSPAInjectsMeta(t *testing.T) {
	f := newDBFixture(t)
	f.srv = New(Options{DB: f.db, ArtistMinWorks: 2, Dist: fstest.MapFS{
		"index.html":  {Data: []byte("<html><head><script src=/assets/a.js></script></head><body></body></html>")},
		"assets/a.js": {Data: []byte("x")},
		"admin.html":  {Data: []byte("<html><head><title>管理</title></head></html>")},
	}})

	rec := f.get(t, "/c/pub")
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `<meta property="og:title" content="睡蓮と積みわら | おうちの美術館">`) ||
		!strings.Contains(body, `<meta property="og:image" content="http://example.com/img/2/1600">`) {
		t.Errorf("code=%d body=%s", rec.Code, body)
	}

	rec = f.get(t, "/c/pub/2")
	if !strings.Contains(rec.Body.String(), "<title>睡蓮 — 睡蓮と積みわら | おうちの美術館</title>") {
		t.Errorf("展示室は作品名: %s", rec.Body)
	}

	for _, p := range []string{"/c/draft", "/a/ulan:2", "/c/pub/9", "/nope"} {
		if rec := f.get(t, p); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "<title>おうちの美術館</title>") {
			t.Errorf("%s: code = %d, want 404 で SPA を返す", p, rec.Code)
		}
	}

	if rec := f.get(t, "/admin/c/3"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "<title>管理</title>") {
		t.Errorf("/admin 以下は admin.html: %d %s", rec.Code, rec.Body)
	}

	rec = f.get(t, "/assets/a.js")
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("assets: code=%d cache=%q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	if rec := f.get(t, "/api/nope"); rec.Code != 404 || !strings.Contains(rec.Header().Get("Content-Type"), "json") {
		t.Errorf("/api/ の 404 は JSON: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
}
