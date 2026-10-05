package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/typewriter/home-museum/viewer_v2/internal/imagecache"
	"github.com/typewriter/home-museum/viewer_v2/internal/store"
)

// 管理 API の認証は手前の Caddy の basic_auth に任せる (/admin* と /api/admin/*)。
// basic 認証の資格情報はクロスサイトのリクエストにもブラウザが付けてしまうので、
// 書き込みは CrossOriginProtection (Sec-Fetch-Site / Origin) で同一オリジンに限る。
func (s *Server) routeAdminAPI() {
	cop := http.NewCrossOriginProtection()
	h := func(pattern string, f http.HandlerFunc) { s.mux.Handle(pattern, cop.Handler(f)) }

	h("GET /api/admin/collections", s.adminCollections)
	h("POST /api/admin/collections", s.adminSaveCollection)
	h("GET /api/admin/collections/{id}", s.adminCollection)
	h("PUT /api/admin/collections/{id}", s.adminSaveCollection)
	h("DELETE /api/admin/collections/{id}", s.adminDeleteCollection)
	h("POST /api/admin/collections/{id}/works", s.adminAddWorks)
	h("DELETE /api/admin/collections/{id}/works", s.adminRemoveWork)
	h("PUT /api/admin/collections/{id}/note", s.adminSetNote)
	h("PUT /api/admin/collections/{id}/order", s.adminSetOrder)
	h("GET /api/admin/artists", s.adminArtists)
	h("GET /api/admin/artists/{key}", s.adminArtistWorks)
	h("GET /api/admin/works", s.adminSearchWorks)
	h("GET /api/admin/stats", s.adminStats)
	h("POST /api/admin/image-failures/clear", s.adminClearFailures)
	// 管理画面で選んでいる作品のサムネは、閲覧者より先に取りに行く。
	h("GET /api/admin/img/{id}/{w}", s.handleImage(imagecache.PriorityAdmin))
	h("GET /api/admin/img/{id}/{w}/status", s.handleImageStatus(imagecache.PriorityAdmin))
}

func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	var inv store.ErrInvalid
	switch {
	case errors.As(err, &inv):
		writeError(w, http.StatusBadRequest, inv.Error())
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "見つかりません")
	default:
		serverError(w, r, err)
	}
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "JSON を読めません: "+err.Error())
		return false
	}
	return true
}

func (s *Server) adminCollections(w http.ResponseWriter, r *http.Request) {
	cs, err := s.st.Collections(r.Context(), false)
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"collections": cs})
}

func (s *Server) adminCollection(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "見つかりません")
		return
	}
	c, err := s.st.CollectionByID(r.Context(), id)
	if err != nil {
		serverError(w, r, err)
		return
	}
	if c == nil {
		writeError(w, http.StatusNotFound, "見つかりません")
		return
	}
	ms, err := s.st.Members(r.Context(), id)
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"collection": c, "members": ms})
}

func (s *Server) adminSaveCollection(w http.ResponseWriter, r *http.Request) {
	var c store.Collection
	if !readJSON(w, r, &c) {
		return
	}
	c.ID = 0
	if r.Method == http.MethodPut {
		id, ok := pathID(r)
		if !ok {
			writeError(w, http.StatusNotFound, "見つかりません")
			return
		}
		c.ID = id
	}
	if err := s.st.SaveCollection(r.Context(), &c); err != nil {
		writeStoreError(w, r, err)
		return
	}
	saved, err := s.st.CollectionByID(r.Context(), c.ID)
	if err != nil {
		serverError(w, r, err)
		return
	}
	code := http.StatusOK
	if r.Method == http.MethodPost {
		code = http.StatusCreated
	}
	writeJSON(w, code, map[string]any{"collection": saved})
}

func (s *Server) adminDeleteCollection(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	if err := s.st.DeleteCollection(r.Context(), id); err != nil {
		serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminAddWorks(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	var body struct {
		SourceURLs []string `json:"source_urls"`
		Note       string   `json:"note"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	added, err := s.st.AddWorks(r.Context(), id, body.SourceURLs, body.Note)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	// コレクションに入れた作品は公開されるので、画像を最優先で取りに行く。
	if s.cache != nil {
		for _, u := range added {
			ref, ok, err := s.imageRefByURL(r.Context(), u)
			if err != nil {
				serverError(w, r, err)
				return
			}
			if ok {
				if _, err := s.cache.Request(r.Context(), ref, imagecache.PriorityCollection); err != nil {
					serverError(w, r, err)
					return
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"added": len(added)})
}

func (s *Server) adminRemoveWork(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	if err := s.st.RemoveWork(r.Context(), id, r.URL.Query().Get("source_url")); err != nil {
		serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminSetNote(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	var body struct {
		SourceURL string `json:"source_url"`
		Note      string `json:"note"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.st.SetNote(r.Context(), id, body.SourceURL, body.Note); err != nil {
		serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminSetOrder(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	var body struct {
		SourceURLs []string `json:"source_urls"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.st.SetOrder(r.Context(), id, body.SourceURLs); err != nil {
		writeStoreError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// adminArtists は閾値を掛けずに全作者から探す。コレクションには作品数の少ない作者も入れたい。
func (s *Server) adminArtists(w http.ResponseWriter, r *http.Request) {
	as, err := s.st.SearchArtists(r.Context(), r.URL.Query().Get("q"), 1, 100)
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"artists": as})
}

func (s *Server) adminArtistWorks(w http.ResponseWriter, r *http.Request) {
	ex, err := s.st.ArtistExhibition(r.Context(), r.PathValue("key"), false)
	if err != nil {
		serverError(w, r, err)
		return
	}
	if ex == nil {
		writeError(w, http.StatusNotFound, "見つかりません")
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	cards, err := s.st.Page(r.Context(), ex, page, catalogPer)
	if err != nil {
		serverError(w, r, err)
		return
	}
	works, err := s.st.WithCollections(r.Context(), cards)
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"exhibition": ex, "page": page, "per": catalogPer, "works": works,
	})
}

func (s *Server) adminSearchWorks(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	cards, more, err := s.st.SearchWorks(r.Context(), r.URL.Query().Get("q"), page, catalogPer)
	if err != nil {
		serverError(w, r, err)
		return
	}
	works, err := s.st.WithCollections(r.Context(), cards)
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"page": page, "per": catalogPer, "has_more": more, "works": works,
	})
}

func (s *Server) adminClearFailures(w http.ResponseWriter, r *http.Request) {
	if s.cache == nil {
		writeError(w, http.StatusBadRequest, "画像キャッシュは無効です")
		return
	}
	var body struct {
		Source string   `json:"source"` // 空なら全館
		States []string `json:"states"` // failed / gone
	}
	if !readJSON(w, r, &body) {
		return
	}
	n, err := s.cache.ClearFailures(r.Context(), body.Source, body.States)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cleared": n})
}

func (s *Server) adminStats(w http.ResponseWriter, r *http.Request) {
	if s.cache == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	st, err := s.cache.Stats(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "stats": st})
}
