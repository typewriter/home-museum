package web

import (
	"bytes"
	"context"
	"html"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/typewriter/home-museum/viewer_v2/internal/store"
)

const (
	siteName        = "おうちの美術館"
	siteDescription = "気軽に楽しむ、名画と名品。"
)

// spa は Vite がビルドした dist/ を配る。
type spa struct {
	dist   fs.FS
	index  []byte // dist/index.html。無ければ未ビルド
	admin  []byte // dist/admin.html
	assets http.Handler
}

func loadSPA(dist fs.FS) *spa {
	sp := &spa{dist: dist, assets: http.FileServerFS(dist)}
	if dist == nil {
		return sp
	}
	sp.index, _ = fs.ReadFile(dist, "index.html")
	sp.admin, _ = fs.ReadFile(dist, "admin.html")
	return sp
}

type pageMeta struct {
	title, description string
	imageID            *int64
}

func (s *Server) routeSPA() {
	s.mux.HandleFunc("GET /c/{slug}", s.spaExhibition(s.publicCollection))
	s.mux.HandleFunc("GET /c/{slug}/{n}", s.spaExhibition(s.publicCollection))
	s.mux.HandleFunc("GET /a/{key}", s.spaExhibition(s.publicArtist))
	s.mux.HandleFunc("GET /a/{key}/{n}", s.spaExhibition(s.publicArtist))
	s.mux.HandleFunc("GET /", s.spaRoot)
}

// spaRoot は / と dist/ 直下のファイル (assets/、favicon など) を返す。
// それ以外は 404 として index.html を返し、画面側に「見つかりません」を出させる。
func (s *Server) spaRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		s.writeIndex(w, r, http.StatusOK, pageMeta{})
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if s.spa.dist != nil && !strings.HasSuffix(name, ".html") {
		if st, err := fs.Stat(s.spa.dist, name); err == nil && !st.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				// Vite はファイル名に内容のハッシュを入れる。
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "public, max-age=86400")
			}
			s.spa.assets.ServeHTTP(w, r)
			return
		}
	}
	s.writeIndex(w, r, http.StatusNotFound, pageMeta{})
}

// spaExhibition は展覧会のページ。中身は SPA が API から取るが、<title> と OGP だけは
// ここで差し込む。共有リンクのプレビューを作るクローラは JS を実行しないため。
func (s *Server) spaExhibition(find exhibitionFinder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ex, err := find(r)
		if err != nil {
			serverError(w, r, err)
			return
		}
		if ex == nil {
			s.writeIndex(w, r, http.StatusNotFound, pageMeta{})
			return
		}
		meta := pageMeta{title: ex.Title, description: ex.Description, imageID: ex.CoverID}
		if n := r.PathValue("n"); n != "" {
			room, err := s.roomFor(r.Context(), ex, n)
			if err != nil {
				serverError(w, r, err)
				return
			}
			if room == nil {
				s.writeIndex(w, r, http.StatusNotFound, pageMeta{})
				return
			}
			t := room.Work.TitleJA
			if t == "" {
				t = room.Work.Title
			}
			meta = pageMeta{title: t + " — " + ex.Title, description: room.Work.Artist, imageID: &room.Work.ID}
		}
		s.writeIndex(w, r, http.StatusOK, meta)
	}
}

func (s *Server) roomFor(ctx context.Context, ex *store.Exhibition, n string) (*store.Room, error) {
	i, err := strconv.Atoi(n)
	if err != nil {
		return nil, nil
	}
	return s.st.Room(ctx, ex, i)
}

func (s *Server) writeIndex(w http.ResponseWriter, r *http.Request, code int, m pageMeta) {
	s.writeHTML(w, r, s.spa.index, code, m)
}

func (s *Server) writeHTML(w http.ResponseWriter, r *http.Request, page []byte, code int, m pageMeta) {
	if page == nil {
		http.Error(w, "画面がビルドされていません。web/ で npm run build を実行してください", http.StatusServiceUnavailable)
		return
	}
	title := siteName
	if m.title != "" {
		title = m.title + " | " + siteName
	}
	desc := m.description
	if desc == "" {
		desc = siteDescription
	}
	base := s.baseURL(r)
	image := base + "/og.jpg"
	if m.imageID != nil {
		image = base + "/img/" + strconv.FormatInt(*m.imageID, 10) + "/1600"
	}
	var head bytes.Buffer
	esc := html.EscapeString
	head.WriteString("<title>" + esc(title) + "</title>\n")
	head.WriteString(`<meta name="description" content="` + esc(desc) + `">` + "\n")
	head.WriteString(`<meta property="og:site_name" content="` + siteName + `">` + "\n")
	head.WriteString(`<meta property="og:type" content="website">` + "\n")
	head.WriteString(`<meta property="og:title" content="` + esc(title) + `">` + "\n")
	head.WriteString(`<meta property="og:description" content="` + esc(desc) + `">` + "\n")
	head.WriteString(`<meta property="og:url" content="` + esc(base+r.URL.RequestURI()) + `">` + "\n")
	head.WriteString(`<meta property="og:image" content="` + esc(image) + `">` + "\n")
	head.WriteString(`<meta name="twitter:card" content="summary_large_image">` + "\n")

	out := bytes.Replace(page, []byte("</head>"), append(head.Bytes(), []byte("</head>")...), 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(code)
	w.Write(out)
}

// baseURL は OGP に要る絶対 URL の起点。-base-url が無ければリクエストから組み立てる。
func (s *Server) baseURL(r *http.Request) string {
	if s.base != "" {
		return s.base
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
