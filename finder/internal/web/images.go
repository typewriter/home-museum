package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/typewriter/home-museum/finder/internal/imagecache"
)

// placeholderSVG は未取得のときに 202 とともに返す絵。<img> にそのまま入るので、
// ページ側は特別扱いせずに済む。
const placeholderSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 400 300" width="400" height="300">
<rect width="400" height="300" fill="#22262f"/>
<text x="200" y="150" fill="#949aa6" font-family="sans-serif" font-size="15"
 text-anchor="middle">%s</text></svg>`

func writePlaceholder(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	fmt.Fprintf(w, placeholderSVG, msg)
}

func (s *Server) imageRef(ctx context.Context, r *http.Request) (imagecache.Ref, bool, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return imagecache.Ref{}, false, err
	}
	src, err := s.st.ImageSource(ctx, id)
	if err != nil || src == nil {
		return imagecache.Ref{}, false, err
	}
	return imagecache.Ref{
		ID: src.ID, Source: src.Source, SourceURL: src.SourceURL, ImageURL: src.ImageURL,
	}, true, nil
}

func (s *Server) imageWidth(r *http.Request) (int, bool) {
	w, err := strconv.Atoi(r.PathValue("w"))
	if err != nil {
		return 0, false
	}
	for _, allowed := range imagecache.DefaultWidths {
		if w == allowed {
			return w, true
		}
	}
	return 0, false
}

// handleImage は画像を返す。未取得ならキューに積んだうえで 202 と
// プレースホルダを返し、決してブラウザを待たせない (spec_image_cache.md §8)。
func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	if s.cache == nil {
		writePlaceholder(w, http.StatusNotFound, "画像キャッシュは無効です")
		return
	}
	width, ok := s.imageWidth(r)
	if !ok {
		writePlaceholder(w, http.StatusNotFound, "対応していないサイズです")
		return
	}
	ctx := r.Context()
	ref, found, err := s.imageRef(ctx, r)
	if err != nil {
		writePlaceholder(w, http.StatusInternalServerError, "エラー")
		return
	}
	if !found {
		writePlaceholder(w, http.StatusNotFound, "作品がありません")
		return
	}

	body, size, err := s.cache.Open(ctx, ref, width)
	if err == nil {
		defer body.Close()
		w.Header().Set("Content-Type", "image/webp")
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		// キーは source_url のハッシュなので、内容が変わればキーも変わる。
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		io.Copy(w, body)
		return
	}
	if !errors.Is(err, imagecache.ErrNotFound) {
		writePlaceholder(w, http.StatusBadGateway, "保管先を読めません")
		return
	}

	st, err := s.cache.Request(ctx, ref)
	if err != nil {
		writePlaceholder(w, http.StatusInternalServerError, "キューに積めません")
		return
	}
	switch st.State {
	case imagecache.StateGone:
		writePlaceholder(w, http.StatusNotFound, "取得できない画像です")
	case imagecache.StateFailed:
		writePlaceholder(w, http.StatusAccepted, "取得に失敗 (再試行待ち)")
	default:
		w.Header().Set("Retry-After", "10")
		writePlaceholder(w, http.StatusAccepted,
			fmt.Sprintf("取得待ち — あと %d 枚", st.Ahead))
	}
}

// handleImageStatus はページ側の JS が数秒おきに見るための状態。
func (s *Server) handleImageStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if s.cache == nil {
		json.NewEncoder(w).Encode(map[string]any{"state": "disabled"})
		return
	}
	ctx := r.Context()
	ref, found, err := s.imageRef(ctx, r)
	if err != nil || !found {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{"state": "missing"})
		return
	}
	st, err := s.cache.Request(ctx, ref)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{"state": "error", "error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(st)
}
