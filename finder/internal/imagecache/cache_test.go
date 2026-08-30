package imagecache

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestCache は一時ディレクトリだけを使う Cache を作る。
// 変換に libvips が要るので、無い環境ではスキップする。
func newTestCache(t *testing.T) *Cache {
	t.Helper()
	if _, err := exec.LookPath("vipsthumbnail"); err != nil {
		t.Skip("libvips-tools が無いのでスキップします")
	}
	dir := t.TempDir()
	blob, err := OpenBlob("local:" + filepath.Join(dir, "blob"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(context.Background(), Options{
		DBPath: filepath.Join(dir, "cache.db"),
		Store:  blob,
		Widths: []int{400},
		Logger: quietLogger(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// sampleJPEG は libvips に作らせた本物の JPEG。
func sampleJPEG(t *testing.T) []byte {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sample.jpg")
	cmd := exec.Command("vips", "black", p+"[Q=90]", "900", "700", "--bands", "3")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("テスト用の JPEG を作れません: %v: %s", err, out)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestFetchStates(t *testing.T) {
	c := newTestCache(t)
	jpg := sampleJPEG(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			w.Write(jpg)
		case "/missing.jpg":
			http.Error(w, "no", http.StatusNotFound)
		case "/page.html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, "<html>エラーページ</html>")
		case "/boom.jpg":
			http.Error(w, "oops", http.StatusInternalServerError)
		case "/challenge.jpg":
			w.Header().Set("Cf-Mitigated", "challenge")
			http.Error(w, "just a moment", http.StatusForbidden)
		case "/forbidden.jpg":
			http.Error(w, "no ua", http.StatusForbidden)
		}
	}))
	defer srv.Close()

	ctx := context.Background()
	cases := []struct {
		name      string
		path      string
		wantState string
	}{
		{"取得できれば ready", "/ok.jpg", StateReady},
		{"404 は gone (二度と取りに行かない)", "/missing.jpg", StateGone},
		{"画像でなければ gone", "/page.html", StateGone},
		{"5xx は failed (再試行する)", "/boom.jpg", StateFailed},
		{"cf-mitigated 付き 403 は gone (チャレンジは解けない)", "/challenge.jpg", StateGone},
		{"cf-mitigated なし 403 は failed (UA/Referer で直るかもしれない)", "/forbidden.jpg", StateFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref := Ref{ID: 1, Source: "test" + tc.path, SourceURL: srv.URL + tc.path,
				ImageURL: srv.URL + tc.path}
			if _, err := c.Request(ctx, ref); err != nil {
				t.Fatal(err)
			}
			c.step(ctx, ref.Source)

			e, ok, err := c.lookup(ctx, Hash(ref.SourceURL))
			if err != nil || !ok {
				t.Fatalf("行がありません: ok=%v err=%v", ok, err)
			}
			if e.State != tc.wantState {
				t.Fatalf("state = %q, want %q (last_error=%q)", e.State, tc.wantState, e.LastError)
			}
			if tc.wantState == StateReady {
				if !e.HasVariant(400) {
					t.Errorf("400px の variant が記録されていません: %v", e.Variants)
				}
				if _, err := c.store.Stat(ctx, Key(e.Source, e.Hash, 400)); err != nil {
					t.Errorf("保管先にオブジェクトがありません: %v", err)
				}
			}
		})
	}
}

// gone を再要求してもキューに戻らないこと。ここが崩れると失敗する画像を
// 延々と館に取りに行き続けることになる。
func TestGoneIsNotRequeued(t *testing.T) {
	c := newTestCache(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusNotFound)
	}))
	defer srv.Close()

	ctx := context.Background()
	ref := Ref{ID: 9, Source: "s", SourceURL: srv.URL + "/x.jpg", ImageURL: srv.URL + "/x.jpg"}
	if _, err := c.Request(ctx, ref); err != nil {
		t.Fatal(err)
	}
	c.step(ctx, "s")

	st, err := c.Request(ctx, ref) // 2 回目の要求
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StateGone {
		t.Fatalf("再要求で state = %q, want %q", st.State, StateGone)
	}
	// キューに積まれていないこと。
	srcs, err := c.pendingSources(ctx, timeNow())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range srcs {
		if s == "s" {
			t.Fatal("gone の行がキューに戻っています")
		}
	}
}

// 中断で fetching のまま残った行が起動時に queued へ戻ること。
func TestRecoverStuck(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()
	ref := Ref{ID: 3, Source: "s", SourceURL: "https://example.org/a", ImageURL: "https://example.org/a.jpg"}
	if _, err := c.Request(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.claim(ctx, "s", timeNow()); err != nil {
		t.Fatal(err)
	}
	n, err := c.recoverStuck(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("戻した件数 = %d, want 1", n)
	}
	e, _, _ := c.lookup(ctx, Hash(ref.SourceURL))
	if e.State != StateQueued {
		t.Fatalf("state = %q, want %q", e.State, StateQueued)
	}
}

// テスト用の小さなヘルパ。
func timeNow() time.Time { return time.Now() }

func quietLogger(t *testing.T) *log.Logger {
	return log.New(testWriter{t}, "", 0)
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
