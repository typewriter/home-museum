// Package web は viewer_v2 の HTTP 層。
package web

import (
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/typewriter/home-museum/viewer_v2/internal/db"
	"github.com/typewriter/home-museum/viewer_v2/internal/imagecache"
	"github.com/typewriter/home-museum/viewer_v2/internal/store"
)

type Options struct {
	DB             *db.DB
	Cache          *imagecache.Cache // nil なら画像は常にプレースホルダ
	Dist           fs.FS             // Vite のビルド結果。nil なら画面を出さない
	ArtistMinWorks int               // 公開する作者の作品数の下限
	BaseURL        string            // OGP の絶対 URL。空ならリクエストから組み立てる
	Now            func() time.Time  // テスト用。nil なら time.Now
}

type Server struct {
	db    *db.DB
	st    *store.Store
	cache *imagecache.Cache
	spa   *spa
	base  string
	now   func() time.Time
	mux   *http.ServeMux
}

func New(o Options) *Server {
	s := &Server{
		db: o.DB, st: store.New(o.DB, o.ArtistMinWorks), cache: o.Cache,
		spa: loadSPA(o.Dist), base: strings.TrimSuffix(o.BaseURL, "/"),
		now: o.Now, mux: http.NewServeMux(),
	}
	if s.now == nil {
		s.now = time.Now
	}
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})
	s.mux.HandleFunc("GET /img/{id}/{w}", s.handleImage(imagecache.PriorityVisitor))
	s.mux.HandleFunc("GET /img/{id}/{w}/status", s.handleImageStatus(imagecache.PriorityVisitor))
	s.routePublicAPI()
	s.routeAdminAPI()
	s.routeSPA()
	return s
}

func (s *Server) Handler() http.Handler { return s.mux }
