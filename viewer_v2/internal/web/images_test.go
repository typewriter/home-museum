package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/typewriter/home-museum/viewer_v2/internal/db"
	"github.com/typewriter/home-museum/viewer_v2/internal/imagecache"
)

// presignBlob は R2 の代わり。署名の中身は imagecache のテストで見ているので、
// ここでは署名時刻と有効期限が URL に乗ることだけを見る。
type presignBlob struct{ imagecache.Blob }

func (presignBlob) PresignGet(key string, t time.Time, exp time.Duration) string {
	return "https://r2.example/" + key + "?t=" + t.UTC().Format(time.RFC3339) + "&exp=" + exp.String()
}

type fixture struct {
	db    *db.DB
	cache *imagecache.Cache
	now   time.Time
	srv   *Server
}

// newImageFixture は画像キャッシュ付き。変換に libvips が要るので、無ければスキップする。
func newImageFixture(t *testing.T) *fixture {
	t.Helper()
	if _, err := exec.LookPath("vipsthumbnail"); err != nil {
		t.Skip("libvips-tools が無いのでスキップします")
	}
	f := newDBFixture(t)
	local, err := imagecache.OpenBlob("local:" + filepath.Join(t.TempDir(), "blob"))
	if err != nil {
		t.Fatal(err)
	}
	f.cache, err = imagecache.New(context.Background(), imagecache.Options{DB: f.db.W, Store: presignBlob{local}})
	if err != nil {
		t.Fatal(err)
	}
	f.srv = New(Options{DB: f.db, Cache: f.cache, Now: func() time.Time { return f.now }})
	mustExec(t, f.db, `INSERT INTO image_cache (url_hash, image_id, source, source_url, origin_url, state, variants, requested_at)
		VALUES (?, 1, 'aic', 'https://a/1', 'x', 'ready', '400,1600', 'x')`, imagecache.Hash("https://a/1"))
	return f
}

// newDBFixture は works と作者とコレクションを入れた viewer.db。
func newDBFixture(t *testing.T) *fixture {
	t.Helper()
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "viewer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	mustExec(t, d, `INSERT INTO works (id, source, source_url, image_url, title, title_ja, artist, year_start) VALUES
		(1, 'aic', 'https://a/1', 'https://img/1.jpg', 'Water Lilies', '睡蓮', 'Claude Monet', 1906),
		(2, 'aic', 'https://a/2', 'https://img/2.jpg', 'Undated',      NULL,   'Claude Monet', NULL),
		(3, 'met', 'https://m/3', 'https://img/3.jpg', 'Haystacks',    NULL,   'Claude Monet', 1890),
		(4, 'rijksmuseum', 'https://r/4', 'https://img/4.jpg', 'Print A', NULL, 'Someone', 1700),
		(5, 'rijksmuseum', 'https://r/5', 'https://img/5.jpg', 'Print B', NULL, 'Someone', 1701)`)
	mustExec(t, d, `INSERT INTO work_artists (work_id, position, person_key, role_bucket) VALUES
		(1, 0, 'ulan:1', 'creator'), (2, 0, 'ulan:1', NULL), (3, 0, 'ulan:1', NULL),
		(3, 1, 'ulan:2', 'non_creator'), (1, 1, 'ulan:2', 'creator'),
		(4, 0, 'cluster:r|https://id/2101', NULL), (5, 0, 'cluster:r|https://id/2101', NULL)`)
	mustExec(t, d, `INSERT INTO artists (person_key, display_name, name_ja, birth_year, death_year, work_count) VALUES
		('ulan:1', 'Claude Monet', 'クロード・モネ', 1840, 1926, 3),
		('ulan:2', 'Publisher', NULL, NULL, NULL, 1),
		('cluster:r|https://id/2101', 'Someone', NULL, NULL, NULL, 2)`)
	mustExec(t, d, `INSERT INTO collections (id, slug, title, title_en, sort, published, created_at, updated_at) VALUES
		(1, 'pub', '睡蓮と積みわら', 'Lilies', 'manual', 1, 'x', '2026-10-02'),
		(2, 'draft', '下書き', NULL, 'manual', 0, 'x', '2026-10-01')`)
	mustExec(t, d, `INSERT INTO collection_works (collection_id, source_url, position, added_at) VALUES
		(1, 'https://a/2', 0, 'x'), (1, 'https://a/1', 1, 'x'), (1, 'https://gone/9', 2, 'x'),
		(2, 'https://m/3', 0, 'x')`)
	f := &fixture{db: d, now: time.Date(2026, 10, 4, 15, 0, 0, 0, time.UTC)}
	f.srv = New(Options{DB: d, ArtistMinWorks: 2, Now: func() time.Time { return f.now }})
	return f
}

func mustExec(t *testing.T, d *db.DB, q string, args ...any) {
	t.Helper()
	if _, err := d.W.Exec(q, args...); err != nil {
		t.Fatalf("%v\n%s", err, q)
	}
}

func (f *fixture) get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestImageRedirectsToPresignedURLAlignedToWindow(t *testing.T) {
	f := newImageFixture(t)
	rec := f.get(t, "/img/1/400")
	if rec.Code != http.StatusFound {
		t.Fatalf("code = %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	want := "https://r2.example/" + imagecache.Key("aic", imagecache.Hash("https://a/1"), 400) +
		"?t=2026-10-04T00:00:00Z&exp=48h0m0s"
	if loc != want {
		t.Fatalf("Location = %s\nwant       %s", loc, want)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=32400" {
		t.Errorf("Cache-Control = %q, want 窓の終わり (24:00) までの 9 時間", cc)
	}

	// 同じ窓のあいだは同じ URL、窓をまたぐと変わる。
	f.now = f.now.Add(8 * time.Hour)
	if got := f.get(t, "/img/1/400").Header().Get("Location"); got != loc {
		t.Errorf("同じ窓で URL が変わった: %s", got)
	}
	f.now = f.now.Add(2 * time.Hour)
	if got := f.get(t, "/img/1/400").Header().Get("Location"); got == loc {
		t.Errorf("窓をまたいでも URL が同じ")
	}
}

func TestImageNotCachedIsQueuedAsVisitor(t *testing.T) {
	f := newImageFixture(t)
	rec := f.get(t, "/img/2/1600")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("code = %d, want 202", rec.Code)
	}
	var state string
	var p int
	if err := f.db.R.QueryRow(`SELECT state, priority FROM image_cache WHERE source_url = 'https://a/2'`).
		Scan(&state, &p); err != nil {
		t.Fatal(err)
	}
	if state != imagecache.StateQueued || p != int(imagecache.PriorityVisitor) {
		t.Errorf("state=%s priority=%d", state, p)
	}
}

func TestImageNotFound(t *testing.T) {
	f := newImageFixture(t)
	for _, path := range []string{"/img/999/400", "/img/abc/400", "/img/1/123"} {
		if rec := f.get(t, path); rec.Code != http.StatusNotFound {
			t.Errorf("%s: code = %d, want 404", path, rec.Code)
		}
	}
}
