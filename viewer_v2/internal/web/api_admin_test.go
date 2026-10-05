package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/typewriter/home-museum/viewer_v2/internal/imagecache"
)

func (f *fixture) do(t *testing.T, method, path, body string, header ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.SetBasicAuth(testAdminUser, testAdminPassword)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(rec, req)
	return rec
}

func memberURLs(t *testing.T, f *fixture, id string) []string {
	t.Helper()
	var r struct {
		Members []struct {
			SourceURL string `json:"source_url"`
			Note      string `json:"note"`
		} `json:"members"`
	}
	getJSON(t, f, "/api/admin/collections/"+id, 200, &r)
	var out []string
	for _, m := range r.Members {
		out = append(out, m.SourceURL+"|"+m.Note)
	}
	return out
}

func TestAdminCollectionLifecycle(t *testing.T) {
	f := newDBFixture(t)

	rec := f.do(t, "POST", "/api/admin/collections", `{"slug":"ukiyoe","title":"浮世絵","sort":"manual"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var created struct {
		Collection struct {
			ID int64 `json:"id"`
		} `json:"collection"`
	}
	json.Unmarshal(rec.Body.Bytes(), &created)
	id := "3"
	if created.Collection.ID != 3 {
		t.Fatalf("id = %d", created.Collection.ID)
	}

	for _, body := range []string{
		`{"slug":"Bad Slug","title":"x"}`,
		`{"slug":"pub","title":"x"}`, // 重複
		`{"slug":"ok","title":""}`,
		`{"slug":"ok","title":"x","sort":"random"}`,
	} {
		if rec := f.do(t, "POST", "/api/admin/collections", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: code = %d, want 400", body, rec.Code)
		}
	}

	add := `{"source_urls":["https://a/1","https://m/3","https://a/1"]}`
	if rec := f.do(t, "POST", "/api/admin/collections/"+id+"/works", add); rec.Code != 200 ||
		!strings.Contains(rec.Body.String(), `"added":2`) {
		t.Fatalf("add: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(t, "POST", "/api/admin/collections/"+id+"/works", `{"source_urls":["https://a/1"]}`); !strings.Contains(rec.Body.String(), `"added":0`) {
		t.Errorf("二重追加: %s", rec.Body)
	}
	if rec := f.do(t, "POST", "/api/admin/collections/99/works", add); rec.Code != http.StatusNotFound {
		t.Errorf("無いコレクションへの追加: %d", rec.Code)
	}

	if rec := f.do(t, "PUT", "/api/admin/collections/"+id+"/order", `{"source_urls":["https://m/3"]}`); rec.Code != http.StatusBadRequest {
		t.Errorf("メンバーの欠けた並びは拒否: %d", rec.Code)
	}
	if rec := f.do(t, "PUT", "/api/admin/collections/"+id+"/order", `{"source_urls":["https://m/3","https://a/1"]}`); rec.Code != http.StatusNoContent {
		t.Fatalf("order: %d %s", rec.Code, rec.Body)
	}
	f.do(t, "PUT", "/api/admin/collections/"+id+"/note", `{"source_url":"https://a/1","note":"代表作"}`)
	if got := memberURLs(t, f, id); strings.Join(got, ",") != "https://m/3|,https://a/1|代表作" {
		t.Errorf("members = %v", got)
	}

	// 非公開のうちは公開 API から見えない。公開すると見える。
	getJSON(t, f, "/api/collections/ukiyoe", 404, nil)
	if rec := f.do(t, "PUT", "/api/admin/collections/"+id, `{"slug":"ukiyoe","title":"浮世絵","sort":"manual","published":true}`); rec.Code != 200 {
		t.Fatalf("publish: %d %s", rec.Code, rec.Body)
	}
	var p pageResp
	getJSON(t, f, "/api/collections/ukiyoe", 200, &p)
	if !equal(ids(p), []int64{3, 1}) {
		t.Errorf("公開後の並び: %v", ids(p))
	}

	f.do(t, "DELETE", "/api/admin/collections/"+id+"/works?source_url="+url.QueryEscape("https://m/3"), "")
	if got := memberURLs(t, f, id); len(got) != 1 {
		t.Errorf("外した後: %v", got)
	}
	if rec := f.do(t, "DELETE", "/api/admin/collections/"+id, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	if n := countRows(t, f, `SELECT count(*) FROM collection_works WHERE collection_id = 3`); n != 0 {
		t.Errorf("コレクションを消してもメンバーが残っている: %d", n)
	}
}

func countRows(t *testing.T, f *fixture, q string) int {
	t.Helper()
	var n int
	if err := f.db.R.QueryRow(q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// basic 認証はクロスサイトのリクエストにも付くので、書き込みはオリジンで弾く。
func TestAdminRejectsCrossOriginWrites(t *testing.T) {
	f := newDBFixture(t)
	rec := f.do(t, "POST", "/api/admin/collections", `{"slug":"evil","title":"x"}`,
		"Sec-Fetch-Site", "cross-site", "Origin", "https://evil.example")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rec.Code)
	}
	rec = f.do(t, "POST", "/api/admin/collections", `{"slug":"mine","title":"x"}`, "Sec-Fetch-Site", "same-origin")
	if rec.Code != http.StatusCreated {
		t.Fatalf("同一オリジン: code = %d %s", rec.Code, rec.Body)
	}
}

func TestAdminArtists(t *testing.T) {
	f := newDBFixture(t)
	var s struct {
		Artists []struct {
			PersonKey string `json:"person_key"`
		} `json:"artists"`
	}
	getJSON(t, f, "/api/admin/artists?q=publisher", 200, &s)
	if len(s.Artists) != 1 {
		t.Errorf("管理画面では閾値未満の作者も探せる: %+v", s.Artists)
	}

	var w struct {
		Works []struct {
			ID          int64   `json:"id"`
			SourceURL   string  `json:"source_url"`
			Collections []int64 `json:"collections"`
		} `json:"works"`
	}
	getJSON(t, f, "/api/admin/artists/ulan:1", 200, &w)
	got := map[int64][]int64{}
	for _, x := range w.Works {
		got[x.ID] = x.Collections
	}
	if len(got[1]) != 1 || got[1][0] != 1 || len(got[3]) != 1 || got[3][0] != 2 || len(got[2]) != 1 {
		t.Errorf("所属コレクション: %v", got)
	}
}

// コレクションに足した作品の画像は最優先で積む。
func TestAdminAddQueuesImagesAtCollectionPriority(t *testing.T) {
	f := newImageFixture(t)
	f.get(t, "/img/2/400") // 閲覧者が先に見ていた
	if rec := f.do(t, "POST", "/api/admin/collections/2/works", `{"source_urls":["https://a/2","https://r/4"]}`); rec.Code != 200 {
		t.Fatalf("add: %d %s", rec.Code, rec.Body)
	}
	for _, u := range []string{"https://a/2", "https://r/4"} {
		var p int
		if err := f.db.R.QueryRow(`SELECT priority FROM image_cache WHERE source_url = ?`, u).Scan(&p); err != nil {
			t.Fatalf("%s: %v", u, err)
		}
		if p != int(imagecache.PriorityCollection) {
			t.Errorf("%s: priority = %d", u, p)
		}
	}
}

func TestAdminSearchWorksByTitle(t *testing.T) {
	f := newDBFixture(t)
	var r struct {
		Works []struct {
			ID          int64   `json:"id"`
			Collections []int64 `json:"collections"`
		} `json:"works"`
		HasMore bool `json:"has_more"`
	}
	getJSON(t, f, "/api/admin/works?q="+url.QueryEscape("睡蓮"), 200, &r)
	if len(r.Works) != 1 || r.Works[0].ID != 1 || len(r.Works[0].Collections) != 1 {
		t.Errorf("日本語訳で引ける: %+v", r.Works)
	}
	getJSON(t, f, "/api/admin/works?q=print", 200, &r)
	if len(r.Works) != 2 || r.HasMore {
		t.Errorf("原題の大文字小文字を区別しない: %+v", r)
	}
	getJSON(t, f, "/api/admin/works?q=100%25", 200, &r)
	if len(r.Works) != 0 {
		t.Errorf("%% はワイルドカードにしない: %+v", r.Works)
	}
	getJSON(t, f, "/api/admin/works?q=+", 200, &r)
	if len(r.Works) != 0 {
		t.Errorf("空白だけなら何も返さない: %+v", r.Works)
	}
}

func TestAdminClearFailures(t *testing.T) {
	f := newImageFixture(t)
	mustExec(t, f.db, `INSERT INTO image_cache (url_hash, image_id, source, source_url, origin_url, state, requested_at) VALUES
		('h1', 2, 'aic', 'https://a/2', 'x', 'gone', 'x'),
		('h2', 3, 'met', 'https://m/3', 'x', 'failed', 'x')`)
	rec := f.do(t, "POST", "/api/admin/image-failures/clear", `{"source":"aic","states":["gone","failed"]}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"cleared":1`) {
		t.Fatalf("clear: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(t, "POST", "/api/admin/image-failures/clear", `{"states":["ready"]}`); rec.Code != http.StatusBadRequest {
		t.Errorf("ready は消せない: %d", rec.Code)
	}
	if n := countRows(t, f, `SELECT count(*) FROM image_cache WHERE state = 'ready'`); n != 1 {
		t.Errorf("ready の行が残っていない: %d", n)
	}
}
