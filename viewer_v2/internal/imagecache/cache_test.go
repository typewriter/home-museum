package imagecache

import (
	"context"
	"database/sql"
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

	"github.com/typewriter/home-museum/viewer_v2/internal/db"
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
		DB:     openTestDB(t, filepath.Join(dir, "viewer.db")),
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
		case "/limited.jpg":
			w.Header().Set("Retry-After", "120")
			http.Error(w, "slow down", http.StatusTooManyRequests)
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
		{"cf-mitigated 付き 403 も failed (過負荷が引けば通るかもしれない)", "/challenge.jpg", StateFailed},
		{"cf-mitigated なし 403 は failed (UA/Referer で直るかもしれない)", "/forbidden.jpg", StateFailed},
		{"429 は failed (Retry-After に従って再試行する)", "/limited.jpg", StateFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref := Ref{ID: 1, Source: "test" + tc.path, SourceURL: srv.URL + tc.path,
				ImageURL: srv.URL + tc.path}
			if _, err := c.Request(ctx, ref, PriorityVisitor); err != nil {
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

// 429 の Retry-After が長ければ、既定の指数バックオフより優先されること。
// (短ければ既定のバックオフのままでよい。「サーバーの指定より長く待つ」のは
// 礼儀に反しないため、大きい方を採る設計になっている)
func TestRetryAfterOverridesBackoffWhenLonger(t *testing.T) {
	c := newTestCache(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "10000") // 初回の既定バックオフ(10分=600秒)よりずっと長い
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	ctx := context.Background()
	ref := Ref{ID: 1, Source: "wd", SourceURL: srv.URL + "/x.jpg", ImageURL: srv.URL + "/x.jpg"}
	if _, err := c.Request(ctx, ref, PriorityVisitor); err != nil {
		t.Fatal(err)
	}
	c.step(ctx, ref.Source)

	var state, retryAfter string
	err := c.db.QueryRowContext(ctx,
		`SELECT state, retry_after FROM image_cache WHERE url_hash = ?`, Hash(ref.SourceURL),
	).Scan(&state, &retryAfter)
	if err != nil {
		t.Fatal(err)
	}
	if state != StateFailed {
		t.Fatalf("state = %q, want %q", state, StateFailed)
	}
	retryAt, err := time.Parse(time.RFC3339Nano, retryAfter)
	if err != nil {
		t.Fatalf("retry_after をパースできません: %q: %v", retryAfter, err)
	}
	wait := time.Until(retryAt)
	if wait < 9*time.Minute+30*time.Second || wait > 3*time.Hour {
		t.Fatalf("retry_after までの待ち時間 = %s, Retry-After: 10000 に近い値 (約2.8時間) を期待", wait)
	}
}

// 429 の Retry-After が極端に大きくても (誤設定や悪意のいずれでも)、指数
// バックオフと同じ maxBackoff (24h) で頭打ちにすること。ここが無いと、
// おかしな値を返す相手に対して failed のまま無期限に固着しうる。
func TestRetryAfterCappedAtMaxBackoff(t *testing.T) {
	c := newTestCache(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1000000") // 約11.6日、24hよりずっと長い
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	ctx := context.Background()
	ref := Ref{ID: 1, Source: "wd2", SourceURL: srv.URL + "/y.jpg", ImageURL: srv.URL + "/y.jpg"}
	if _, err := c.Request(ctx, ref, PriorityVisitor); err != nil {
		t.Fatal(err)
	}
	c.step(ctx, ref.Source)

	var retryAfter string
	err := c.db.QueryRowContext(ctx,
		`SELECT retry_after FROM image_cache WHERE url_hash = ?`, Hash(ref.SourceURL),
	).Scan(&retryAfter)
	if err != nil {
		t.Fatal(err)
	}
	retryAt, err := time.Parse(time.RFC3339Nano, retryAfter)
	if err != nil {
		t.Fatalf("retry_after をパースできません: %q: %v", retryAfter, err)
	}
	wait := time.Until(retryAt)
	if wait > maxBackoff+time.Minute {
		t.Fatalf("retry_after までの待ち時間 = %s, maxBackoff (%s) を超えないことを期待", wait, maxBackoff)
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
	if _, err := c.Request(ctx, ref, PriorityVisitor); err != nil {
		t.Fatal(err)
	}
	c.step(ctx, "s")

	st, err := c.Request(ctx, ref, PriorityVisitor) // 2 回目の要求
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

// AIC は Cloudflare の動的チャレンジ再発を避けるため、既定間隔より長い下限
// (30s) を使うこと。他の館は既定間隔のままであること。
func TestAICUsesLongerInterval(t *testing.T) {
	c := newTestCache(t)
	if got := c.intervalFor("aic"); got != 30*time.Second {
		t.Fatalf("intervalFor(aic) = %s, want 30s", got)
	}
	if got := c.intervalFor("met"); got != c.iv {
		t.Fatalf("intervalFor(met) = %s, want %s (既定のまま)", got, c.iv)
	}

	now := time.Now()
	if !c.reserve("aic", now) {
		t.Fatal("初回の reserve が失敗しました")
	}
	c.release("aic")
	if c.reserve("aic", now.Add(10*time.Second)) {
		t.Fatal("10 秒後の reserve(aic) が通ってしまいました (30 秒空くはず)")
	}
	if !c.reserve("aic", now.Add(31*time.Second)) {
		t.Fatal("31 秒後の reserve(aic) が失敗しました")
	}
}

// 中断で fetching のまま残った行が起動時に queued へ戻ること。
func TestRecoverStuck(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()
	ref := Ref{ID: 3, Source: "s", SourceURL: "https://example.org/a", ImageURL: "https://example.org/a.jpg"}
	if _, err := c.Request(ctx, ref, PriorityVisitor); err != nil {
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

// Retry-After は秒数と HTTP-date のどちらの形式でも来る (RFC 9110 §10.2.3)。
func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		value string
		want  time.Time
	}{
		{"秒数", "120", now.Add(120 * time.Second)},
		{"空 = 指定なし", "", time.Time{}},
		{"負数は無視", "-1", time.Time{}},
		{"読めない値は無視", "not-a-date", time.Time{}},
		{"HTTP-date", "Mon, 01 Jan 2024 00:02:00 GMT", now.Add(2 * time.Minute)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseRetryAfter(tc.value, now); !got.Equal(tc.want) {
				t.Errorf("parseRetryAfter(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
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

func openTestDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	d, err := db.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d.W
}

// 優先度の高いものから取り出し、後から低い優先度で要求されても下がらないこと。
func TestClaimByPriority(t *testing.T) {
	c := newTestCache(t)
	ctx := context.Background()
	ref := func(name string) Ref {
		u := "https://example.org/" + name
		return Ref{ID: 1, Source: "s", SourceURL: u, ImageURL: u + ".jpg"}
	}
	for _, r := range []struct {
		name string
		p    Priority
	}{
		{"visitor", PriorityVisitor},
		{"admin", PriorityAdmin},
		{"collection", PriorityCollection},
		{"collection", PriorityVisitor}, // 閲覧者が同じ作品を見ても collection のまま
	} {
		if _, err := c.Request(ctx, ref(r.name), r.p); err != nil {
			t.Fatal(err)
		}
	}

	st, _, err := c.Lookup(ctx, ref("visitor").SourceURL)
	if err != nil {
		t.Fatal(err)
	}
	if st.Ahead != 2 {
		t.Errorf("visitor の前にいる枚数 = %d, want 2", st.Ahead)
	}

	for _, want := range []string{"collection", "admin", "visitor"} {
		e, ok, err := c.claim(ctx, "s", timeNow())
		if err != nil || !ok {
			t.Fatalf("claim: ok=%v err=%v", ok, err)
		}
		if e.SourceURL != ref(want).SourceURL {
			t.Fatalf("取り出したのは %s, want %s", e.SourceURL, want)
		}
	}
}
