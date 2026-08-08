// Package web は finder の HTTP 層。テンプレートと静的ファイルは go:embed で
// バイナリに埋め込むので、実行時に必要なのは hm.db と index.db だけ。
package web

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/typewriter/home-museum/finder/internal/imagecache"
	"github.com/typewriter/home-museum/finder/internal/store"
)

//go:embed templates/*.html static/*
var assets embed.FS

// pages はページごとに layout と組にして持つ。html/template は "content" を
// 差し替える仕組みを持たないので、テンプレートセットをページ単位で作る。
var pages = []string{
	"search.html", "work.html", "artists.html", "artist.html",
	"unmatched.html", "stats.html", "sql.html", "error.html",
}

// Server は 1 プロセスぶんの状態。
type Server struct {
	st        *store.Store
	cache     *imagecache.Cache // nil なら画像表示を出さない
	tpl       map[string]*template.Template
	allowSQL  bool
	queryWait time.Duration

	// 索引の鮮度チェックは images の全走査を含むので、短時間キャッシュする。
	statusMu   sync.Mutex
	statusVal  store.IndexStatus
	statusAt   time.Time
	statusTTL  time.Duration
	facetsMu   sync.Mutex
	facetsVal  map[string][]store.FacetValue
	facetsOnce bool

	statsMu  sync.Mutex
	statsVal *store.Stats
	statsAt  time.Time
}

type Options struct {
	// AllowSQL は読み取り専用 SQL コンソールを有効にする。
	AllowSQL bool
	// QueryTimeout は 1 リクエストあたりの上限。
	QueryTimeout time.Duration
	// Cache が nil のときは画像まわりの UI を一切出さない。
	Cache *imagecache.Cache
}

func New(st *store.Store, opt Options) (*Server, error) {
	if opt.QueryTimeout <= 0 {
		opt.QueryTimeout = 30 * time.Second
	}
	s := &Server{
		st:        st,
		cache:     opt.Cache,
		tpl:       map[string]*template.Template{},
		allowSQL:  opt.AllowSQL,
		queryWait: opt.QueryTimeout,
		statusTTL: 60 * time.Second,
	}
	for _, p := range pages {
		t, err := template.New("layout.html").Funcs(funcs).
			ParseFS(assets, "templates/layout.html", "templates/"+p)
		if err != nil {
			return nil, fmt.Errorf("テンプレート %s: %w", p, err)
		}
		s.tpl[p] = t
	}
	return s, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	static, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/",
		http.FileServer(http.FS(static))))

	mux.HandleFunc("GET /{$}", s.handleSearch)
	mux.HandleFunc("GET /works/{id}", s.handleWork)
	mux.HandleFunc("GET /artists/{$}", s.handleArtists)
	mux.HandleFunc("GET /artists/unmatched", s.handleUnmatched)
	mux.HandleFunc("GET /artists/detail", s.handleArtist)
	mux.HandleFunc("GET /stats", s.handleStats)
	mux.HandleFunc("GET /sql", s.handleSQL)
	mux.HandleFunc("GET /img/{id}/{w}", s.handleImage)
	mux.HandleFunc("GET /img/{id}/{w}/status", s.handleImageStatus)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})
	return logRequests(mux)
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if !strings.HasPrefix(r.URL.Path, "/static/") {
			log.Printf("%s %s %s", r.Method, r.URL.RequestURI(), time.Since(start).Round(time.Millisecond))
		}
	})
}

// pageData は layout が使う共通部分。各ページ固有の値は Data に入れる。
type pageData struct {
	Title    string
	Nav      string
	Index    store.IndexStatus
	AllowSQL bool
	Images   bool // 画像キャッシュが有効か
	Data     any
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, page, title, nav string, data any) {
	t, ok := s.tpl[page]
	if !ok {
		http.Error(w, "unknown page "+page, http.StatusInternalServerError)
		return
	}
	pd := pageData{
		Title:    title,
		Nav:      nav,
		Index:    s.indexStatus(r.Context()),
		AllowSQL: s.allowSQL,
		Images:   s.cache != nil,
		Data:     data,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.Execute(w, pd); err != nil {
		log.Printf("テンプレートの描画に失敗: %v", err)
	}
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, code int, err error) {
	w.WriteHeader(code)
	s.render(w, r, "error.html", "エラー", "", map[string]any{
		"Code":    code,
		"Message": err.Error(),
	})
}

func (s *Server) indexStatus(ctx context.Context) store.IndexStatus {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	if time.Since(s.statusAt) < s.statusTTL && !s.statusAt.IsZero() {
		return s.statusVal
	}
	st, err := s.st.IndexStatus(ctx)
	if err != nil {
		log.Printf("索引の状態を取得できませんでした: %v", err)
		return s.statusVal
	}
	s.statusVal, s.statusAt = st, time.Now()
	return st
}

func (s *Server) facets(ctx context.Context) map[string][]store.FacetValue {
	s.facetsMu.Lock()
	defer s.facetsMu.Unlock()
	if s.facetsOnce {
		return s.facetsVal
	}
	f, err := s.st.Facets(ctx)
	if err != nil {
		log.Printf("facet を読めませんでした: %v", err)
	}
	s.facetsVal, s.facetsOnce = f, true
	return f
}

// InvalidateCaches は索引を作り直したあとに呼ぶ想定 (現状は起動時のみ)。
func (s *Server) InvalidateCaches() {
	s.statusMu.Lock()
	s.statusAt = time.Time{}
	s.statusMu.Unlock()
	s.facetsMu.Lock()
	s.facetsOnce = false
	s.facetsMu.Unlock()
}

// ---- クエリパラメータの取り出し ----

func q(r *http.Request, key string) string {
	return strings.TrimSpace(r.URL.Query().Get(key))
}

func qBool(r *http.Request, key string) bool {
	v := q(r, key)
	return v == "1" || v == "on" || v == "true"
}

func qInt(r *http.Request, key string, def int) int {
	if v, err := strconv.Atoi(q(r, key)); err == nil {
		return v
	}
	return def
}

// qIntPtr は未入力と 0 を区別する (制作年 0 は「西暦 0 年」で有効な値)。
func qIntPtr(r *http.Request, key string) *int {
	raw := q(r, key)
	if raw == "" {
		return nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return nil
	}
	return &v
}

func qList(r *http.Request, key string) []string {
	var out []string
	for _, v := range r.URL.Query()[key] {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
