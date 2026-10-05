package imagecache

import (
	"context"
	"database/sql"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

// finderCacheDDL は finder の cache.db の形 (feature/finder の db.go)。priority 列が無い。
const finderCacheDDL = `
CREATE TABLE image_cache (
  url_hash TEXT PRIMARY KEY, image_id INTEGER NOT NULL, source TEXT NOT NULL,
  source_url TEXT NOT NULL, origin_url TEXT NOT NULL, state TEXT NOT NULL,
  variants TEXT NOT NULL DEFAULT '', bytes INTEGER NOT NULL DEFAULT 0,
  pixel_w INTEGER NOT NULL DEFAULT 0, pixel_h INTEGER NOT NULL DEFAULT 0,
  attempts INTEGER NOT NULL DEFAULT 0, last_error TEXT NOT NULL DEFAULT '',
  requested_at TEXT NOT NULL, queued_at TEXT, retry_after TEXT, fetched_at TEXT,
  hits INTEGER NOT NULL DEFAULT 0)`

func TestMigrate(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := OpenBlob("local:" + filepath.Join(dir, "blob"))
	if err != nil {
		t.Fatal(err)
	}

	oldPath := filepath.Join(dir, "cache.db")
	old, err := sql.Open("sqlite", oldPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(finderCacheDDL); err != nil {
		t.Fatal(err)
	}
	rows := map[string]string{ // source_url → state
		"https://a/ready":    StateReady,
		"https://a/lost":     StateReady, // オブジェクトが無い
		"https://a/gone":     StateGone,
		"https://a/fetching": StateFetching,
	}
	for u, st := range rows {
		v := ""
		if st == StateReady {
			v = "400,1600"
		}
		if _, err := old.Exec(`INSERT INTO image_cache
			(url_hash, image_id, source, source_url, origin_url, state, variants, requested_at)
			VALUES (?, 1, 'aic', ?, ?, ?, ?, 'x')`, Hash(u), u, u, st, v); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()
	for _, w := range []int{400, 1600} {
		if err := store.Put(ctx, Key("aic", Hash("https://a/ready"), w),
			strings.NewReader("webp"), 4, "image/webp"); err != nil {
			t.Fatal(err)
		}
	}

	w := openTestDB(t, filepath.Join(dir, "viewer.db"))
	for i := 0; i < 2; i++ { // やり直しても重複しないこと
		if err := Migrate(ctx, w, store, oldPath, io.Discard); err != nil {
			t.Fatal(err)
		}
	}

	got := map[string]string{}
	rs, err := w.Query(`SELECT source_url, state FROM image_cache`)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	for rs.Next() {
		var u, st string
		rs.Scan(&u, &st)
		got[u] = st
	}
	want := map[string]string{
		"https://a/ready":    StateReady,
		"https://a/gone":     StateGone,
		"https://a/fetching": StateQueued,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for u, st := range want {
		if got[u] != st {
			t.Errorf("%s = %q, want %q", u, got[u], st)
		}
	}
}
