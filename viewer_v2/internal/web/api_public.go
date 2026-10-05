package web

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/typewriter/home-museum/viewer_v2/internal/store"
)

// catalogPer は図録 1 ページの枚数。サムネは閲覧者の優先度で 1 枚ずつ取りに行くので、
// 1 ページで積む量をこのくらいに抑える (spec_image_cache.md §8)。
const catalogPer = 60

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func serverError(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
	writeError(w, http.StatusInternalServerError, "サーバーでエラーが起きました")
}

func (s *Server) routePublicAPI() {
	s.mux.HandleFunc("GET /api/collections", s.apiCollections)
	s.mux.HandleFunc("GET /api/collections/{slug}", s.apiExhibition(s.publicCollection))
	s.mux.HandleFunc("GET /api/collections/{slug}/works/{n}", s.apiRoom(s.publicCollection))
	s.mux.HandleFunc("GET /api/artists", s.apiArtists)
	s.mux.HandleFunc("GET /api/artists/{key}", s.apiExhibition(s.publicArtist))
	s.mux.HandleFunc("GET /api/artists/{key}/works/{n}", s.apiRoom(s.publicArtist))
	s.mux.HandleFunc("GET /api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "見つかりません")
	})
}

// exhibitionFinder は URL から展覧会を引く。無いときと非公開のときは (nil, nil)。
type exhibitionFinder func(r *http.Request) (*store.Exhibition, error)

func (s *Server) publicCollection(r *http.Request) (*store.Exhibition, error) {
	c, err := s.st.CollectionBySlug(r.Context(), r.PathValue("slug"))
	if err != nil || c == nil || !c.Published {
		return nil, err
	}
	return s.st.CollectionExhibition(r.Context(), c)
}

func (s *Server) publicArtist(r *http.Request) (*store.Exhibition, error) {
	return s.st.ArtistExhibition(r.Context(), r.PathValue("key"), true)
}

func (s *Server) apiCollections(w http.ResponseWriter, r *http.Request) {
	cs, err := s.st.Collections(r.Context(), true)
	if err != nil {
		serverError(w, r, err)
		return
	}
	type card struct {
		Slug        string `json:"slug"`
		Title       string `json:"title"`
		TitleEN     string `json:"title_en,omitempty"`
		Description string `json:"description,omitempty"`
		Count       int    `json:"count"`
		CoverID     *int64 `json:"cover_id,omitempty"`
	}
	out := []card{}
	for _, c := range cs {
		ex, err := s.st.CollectionExhibition(r.Context(), &c)
		if err != nil {
			serverError(w, r, err)
			return
		}
		out = append(out, card{c.Slug, c.Title, c.TitleEN, c.Description, ex.Total, ex.CoverID})
	}
	writeJSON(w, http.StatusOK, map[string]any{"collections": out})
}

func (s *Server) apiExhibition(find exhibitionFinder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ex, err := find(r)
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
		works, err := s.st.Page(r.Context(), ex, page, catalogPer)
		if err != nil {
			serverError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"exhibition": ex, "page": page, "per": catalogPer, "works": works,
		})
	}
}

func (s *Server) apiRoom(find exhibitionFinder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ex, err := find(r)
		if err != nil {
			serverError(w, r, err)
			return
		}
		n, _ := strconv.Atoi(r.PathValue("n"))
		var room *store.Room
		if ex != nil {
			if room, err = s.st.Room(r.Context(), ex, n); err != nil {
				serverError(w, r, err)
				return
			}
		}
		if room == nil {
			writeError(w, http.StatusNotFound, "見つかりません")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"exhibition": ex, "room": room})
	}
}

func (s *Server) apiArtists(w http.ResponseWriter, r *http.Request) {
	as, err := s.st.SearchArtists(r.Context(), r.URL.Query().Get("q"), s.st.ArtistMinWorks, 50)
	if err != nil {
		serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"artists": as})
}
