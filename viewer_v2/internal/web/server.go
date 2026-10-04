// Package web は viewer_v2 の HTTP 層。
package web

import (
	"net/http"
	"time"

	"github.com/typewriter/home-museum/viewer_v2/internal/db"
	"github.com/typewriter/home-museum/viewer_v2/internal/imagecache"
)

type Options struct {
	DB    *db.DB
	Cache *imagecache.Cache // nil なら画像は常にプレースホルダ
	Now   func() time.Time  // テスト用。nil なら time.Now
}

type Server struct {
	db    *db.DB
	cache *imagecache.Cache
	now   func() time.Time
	mux   *http.ServeMux
}

func New(o Options) *Server {
	s := &Server{db: o.DB, cache: o.Cache, now: o.Now, mux: http.NewServeMux()}
	if s.now == nil {
		s.now = time.Now
	}
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})
	s.mux.HandleFunc("GET /img/{id}/{w}", s.handleImage(imagecache.PriorityVisitor))
	s.mux.HandleFunc("GET /img/{id}/{w}/status", s.handleImageStatus(imagecache.PriorityVisitor))
	return s
}

func (s *Server) Handler() http.Handler { return s.mux }
