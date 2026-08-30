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
	"net/url"
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
	"collections.html", "collection.html",
}

// Server は 1 プロセスぶんの状態。
type Server struct {
	st        *store.Store
	cache     *imagecache.Cache // nil なら画像表示を出さない
	tpl       map[string]*template.Template
	basePath  string // 空 or "/" から始まり "/" で終わらない接頭辞
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
	// BasePath はリバースプロキシがパスベースで振り分けるときの接頭辞
	// (例: "/art-finder")。空ならルート直下で待ち受ける。
	BasePath string
}

func New(st *store.Store, opt Options) (*Server, error) {
	if opt.QueryTimeout <= 0 {
		opt.QueryTimeout = 30 * time.Second
	}
	s := &Server{
		st:        st,
		cache:     opt.Cache,
		tpl:       map[string]*template.Template{},
		basePath:  normalizeBasePath(opt.BasePath),
		allowSQL:  opt.AllowSQL,
		queryWait: opt.QueryTimeout,
		statusTTL: 60 * time.Second,
	}
	// テンプレートは href / src / action をすべて絶対パスで書いているので、
	// basePath を前置する "u" をここで束縛する (funcs はパッケージ変数で
	// basePath を知らない)。
	tplFuncs := template.FuncMap{"u": s.u}
	for _, p := range pages {
		t, err := template.New("layout.html").Funcs(funcs).Funcs(tplFuncs).
			ParseFS(assets, "templates/layout.html", "templates/"+p)
		if err != nil {
			return nil, fmt.Errorf("テンプレート %s: %w", p, err)
		}
		s.tpl[p] = t
	}
	return s, nil
}

// normalizeBasePath は "art-finder" や "/art-finder/" のような入力を
// "/art-finder" に揃える。空文字はそのまま (ルート直下)。
func normalizeBasePath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimSuffix(p, "/")
	if p == "" {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

// u はページ内の絶対パス (「/」から始まる) に basePath を前置する。
// リダイレクト先やテンプレートの href / src / action はすべてこれを通す。
func (s *Server) u(p string) string {
	return s.basePath + p
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

	if s.st.HasCollections {
		mux.HandleFunc("GET /collections/{$}", s.handleCollections)
		mux.HandleFunc("GET /collections/{slug}", s.handleCollection)
		// 状態を変える操作はすべて POST (requirePost がオリジンも見る)。
		mux.HandleFunc("POST /collections/save", s.requirePost(s.handleCollectionSave))
		mux.HandleFunc("POST /collections/delete", s.requirePost(s.handleCollectionDelete))
		mux.HandleFunc("POST /collections/add", s.requirePost(s.handleCollectionAdd))
		mux.HandleFunc("POST /collections/remove", s.requirePost(s.handleCollectionRemove))
		mux.HandleFunc("POST /collections/move", s.requirePost(s.handleCollectionMove))
		mux.HandleFunc("POST /collections/note", s.requirePost(s.handleCollectionNote))
	}

	mux.HandleFunc("GET /img/{id}/{w}", s.handleImage)
	mux.HandleFunc("GET /img/{id}/{w}/status", s.handleImageStatus)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})
	var h http.Handler = mux
	if s.basePath != "" {
		h = s.stripBasePath(h)
	}
	return logRequests(h)
}

// stripBasePath は "{basePath}/works/1" のようなリクエストを "/works/1" に
// 付け替えたうえで mux に渡す。ルーティング自体は basePath を知らずに済む。
// 逆方向 (レスポンスに埋め込む絶対パス) は u() が担う。
//
// /healthz はコンテナへの直接ヘルスチェック (リバースプロキシ越しではない) を
// 想定して basePath の対象から外す。
func (s *Server) stripBasePath(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == s.basePath {
			http.Redirect(w, r, s.basePath+"/", http.StatusMovedPermanently)
			return
		}
		rest, ok := strings.CutPrefix(r.URL.Path, s.basePath+"/")
		if !ok {
			http.NotFound(w, r)
			return
		}
		r.URL.Path = "/" + rest
		next.ServeHTTP(w, r)
	})
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
	// Collections はどのページでも「追加先」を選べるように毎回渡す。
	// 空でもコレクション機能が無効とは限らない (1 本も作っていないだけ)。
	HasCollections bool
	Collections    []store.Collection
	Data           any
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, page, title, nav string, data any) {
	t, ok := s.tpl[page]
	if !ok {
		http.Error(w, "unknown page "+page, http.StatusInternalServerError)
		return
	}
	pd := pageData{
		Title:          title,
		Nav:            nav,
		Index:          s.indexStatus(r.Context()),
		AllowSQL:       s.allowSQL,
		Images:         s.cache != nil,
		HasCollections: s.st.HasCollections,
		Data:           data,
	}
	if s.st.HasCollections {
		cs, err := s.st.CollectionList(r.Context())
		if err != nil {
			log.Printf("コレクション一覧を読めませんでした: %v", err)
		}
		pd.Collections = cs
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
//
// r.URL.Query() ではなく r.Form を見る。コレクションへの「まとめて追加」は
// 同じ条件を POST の本文で受けるので、GET と POST で同じ関数を使えないと
// 画面に出ている集合と実際に入る集合がずれる。ParseForm は冪等で、GET なら
// URL のクエリだけを読む。

func form(r *http.Request) url.Values {
	if r.Form == nil {
		r.ParseForm()
	}
	return r.Form
}

func q(r *http.Request, key string) string {
	return strings.TrimSpace(form(r).Get(key))
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
	for _, v := range form(r)[key] {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
