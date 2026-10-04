package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/typewriter/home-museum/viewer_v2/internal/imagecache"
)

// presignWindow は署名時刻を揃える窓。同じ窓のあいだは同じ URL を返すので、
// R2 が付ける immutable がブラウザのキャッシュとして効く (spec_image_cache.md §9)。
// 有効期限を窓の 2 倍にしておくのは、窓の終わり際に受け取った URL を
// 読み込む前に失効させないため。
const presignWindow = 24 * time.Hour

// placeholderSVG は未取得のときに 202 とともに返す絵。<img> にそのまま入るので、
// ページ側は特別扱いせずに済む。
const placeholderSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 400 300" width="400" height="300">
<rect width="400" height="300" fill="#1b1b1d"/>
<text x="200" y="150" fill="#8a8a90" font-family="sans-serif" font-size="15"
 text-anchor="middle">%s</text></svg>`

func writePlaceholder(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	fmt.Fprintf(w, placeholderSVG, msg)
}

func (s *Server) imageRef(ctx context.Context, idStr string) (imagecache.Ref, bool, error) {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return imagecache.Ref{}, false, nil
	}
	r := imagecache.Ref{ID: id}
	err = s.db.R.QueryRowContext(ctx,
		`SELECT source, source_url, image_url FROM works WHERE id = ?`, id,
	).Scan(&r.Source, &r.SourceURL, &r.ImageURL)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	return r, err == nil, err
}

// imageRefByURL はコレクションに足した作品を積むときに使う。
func (s *Server) imageRefByURL(ctx context.Context, sourceURL string) (imagecache.Ref, bool, error) {
	r := imagecache.Ref{SourceURL: sourceURL}
	err := s.db.R.QueryRowContext(ctx,
		`SELECT id, source, image_url FROM works WHERE source_url = ?`, sourceURL,
	).Scan(&r.ID, &r.Source, &r.ImageURL)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	return r, err == nil, err
}

func (s *Server) imageWidth(r *http.Request) (int, bool) {
	w, err := strconv.Atoi(r.PathValue("w"))
	if err != nil || !slices.Contains(s.cache.Widths(), w) {
		return 0, false
	}
	return w, true
}

// handleImage は保管済みなら署名付き URL へ 302 し、未取得なら積んだうえで
// 202 とプレースホルダを即座に返す。ブラウザを待たせない (spec_image_cache.md §8)。
func (s *Server) handleImage(p imagecache.Priority) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cache == nil {
			writePlaceholder(w, http.StatusNotFound, "画像は無効です")
			return
		}
		width, ok := s.imageWidth(r)
		if !ok {
			writePlaceholder(w, http.StatusNotFound, "対応していないサイズです")
			return
		}
		ctx := r.Context()
		ref, found, err := s.imageRef(ctx, r.PathValue("id"))
		if err != nil {
			writePlaceholder(w, http.StatusInternalServerError, "エラー")
			return
		}
		if !found {
			writePlaceholder(w, http.StatusNotFound, "作品がありません")
			return
		}

		key, ready, err := s.cache.ReadyKey(ctx, ref.SourceURL, width)
		if err != nil {
			writePlaceholder(w, http.StatusInternalServerError, "エラー")
			return
		}
		if ready {
			s.serveReady(w, r, ref, key, width)
			return
		}

		st, err := s.cache.Request(ctx, ref, p)
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
			writePlaceholder(w, http.StatusAccepted, fmt.Sprintf("取得待ち — あと %d 枚", st.Ahead))
		}
	}
}

func (s *Server) serveReady(w http.ResponseWriter, r *http.Request, ref imagecache.Ref, key string, width int) {
	if ps, ok := s.cache.Presigner(); ok {
		now := s.now()
		signed := now.Truncate(presignWindow)
		maxAge := int(signed.Add(presignWindow).Sub(now).Seconds())
		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", maxAge))
		http.Redirect(w, r, ps.PresignGet(key, signed, 2*presignWindow), http.StatusFound)
		return
	}
	// ローカルの保管先 (開発用) は署名付き URL を作れないので、自分で中継する。
	body, size, err := s.cache.Open(r.Context(), ref, width)
	if err != nil {
		writePlaceholder(w, http.StatusBadGateway, "保管先を読めません")
		return
	}
	defer body.Close()
	w.Header().Set("Content-Type", "image/webp")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Cache-Control", "public, max-age=86400")
	io.Copy(w, body)
}

// handleImageStatus はページ側が 202 を受けたあと数秒おきに見る。
func (s *Server) handleImageStatus(p imagecache.Priority) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if s.cache == nil {
			json.NewEncoder(w).Encode(map[string]any{"state": "disabled"})
			return
		}
		ctx := r.Context()
		ref, found, err := s.imageRef(ctx, r.PathValue("id"))
		if err != nil || !found {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]any{"state": "missing"})
			return
		}
		st, err := s.cache.Request(ctx, ref, p)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"state": "error"})
			return
		}
		json.NewEncoder(w).Encode(st)
	}
}
