package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/typewriter/home-museum/finder/internal/store"
)

// BulkCap は「検索結果をまとめて追加」の上限。件数表示が CountCap で
// 打ち切られている以上、それより多く入れると何を入れたのか分からなくなる。
const BulkCap = store.CountCap

func (s *Server) handleCollections(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.queryWait)
	defer cancel()

	cols, err := s.st.Collections(ctx)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err)
		return
	}
	s.render(w, r, "collections.html", "コレクション", "collections", map[string]any{
		"Cols":  cols,
		"Sorts": store.CollectionSorts(),
		"Flash": q(r, "flash"),
	})
}

func (s *Server) handleCollection(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.queryWait)
	defer cancel()

	c, err := s.st.CollectionBySlug(ctx, r.PathValue("slug"))
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err)
		return
	}
	if c == nil {
		s.fail(w, r, http.StatusNotFound, errors.New("そのコレクションはありません"))
		return
	}

	res, err := s.st.CollectionMembers(ctx, c, qInt(r, "page", 1), qInt(r, "per", 24))
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err)
		return
	}
	s.render(w, r, "collection.html", c.Title, "collections", map[string]any{
		"C":     c,
		"Res":   res,
		"Sorts": store.CollectionSorts(),
		"Query": r.URL.Query(),
		"Flash": q(r, "flash"),
		// 選別には画像が要るので、検索一覧と違ってサムネは既定で出す
		// (spec_collections.md §5)。
		"Thumbs": s.cache != nil && !qBool(r, "nothumbs"),
	})
}

// handleCollectionSave は新規作成と編集の両方。id が空なら作成。
func (s *Server) handleCollectionSave(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.queryWait)
	defer cancel()

	c := &store.Collection{
		ID:          formInt64(r, "id"),
		Slug:        r.FormValue("slug"),
		Title:       r.FormValue("title"),
		TitleEn:     strings.TrimSpace(r.FormValue("title_en")),
		Description: strings.TrimSpace(r.FormValue("description")),
		CoverURL:    strings.TrimSpace(r.FormValue("cover_url")),
		Sort:        r.FormValue("sort"),
	}
	if err := s.st.SaveCollection(ctx, c); err != nil {
		s.fail(w, r, http.StatusBadRequest, err)
		return
	}
	redirect(w, r, s.u("/collections/"+c.Slug), "保存しました")
}

func (s *Server) handleCollectionDelete(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.queryWait)
	defer cancel()

	id := formInt64(r, "id")
	c, err := s.st.CollectionByID(ctx, id)
	if err != nil || c == nil {
		s.fail(w, r, http.StatusNotFound, errors.New("そのコレクションはありません"))
		return
	}
	// 取り違えて消さないよう、題名の再入力を求める。復旧手段が
	// エクスポート済みの CSV しかないため (spec_collections.md §1)。
	if r.FormValue("confirm") != c.Title {
		s.fail(w, r, http.StatusBadRequest,
			fmt.Errorf("削除するには題名 %q をそのまま入力してください", c.Title))
		return
	}
	if err := s.st.DeleteCollection(ctx, id); err != nil {
		s.fail(w, r, http.StatusInternalServerError, err)
		return
	}
	redirect(w, r, s.u("/collections/"), "「"+c.Title+"」を削除しました")
}

// handleCollectionAdd は 1 枚または検索結果ぶんを追加する。
//
//	url=...            指定した source_url を追加 (複数可)
//	from_search=1      現在の検索条件の結果をまとめて追加
func (s *Server) handleCollectionAdd(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.queryWait)
	defer cancel()

	c, err := s.collectionFromForm(ctx, r)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, err)
		return
	}

	urls := r.Form["url"]
	msg := ""
	if r.FormValue("from_search") != "" {
		p := searchParams(r)
		found, err := s.st.SearchSourceURLs(ctx, p, BulkCap)
		if err != nil {
			s.fail(w, r, http.StatusBadRequest, err)
			return
		}
		urls = append(urls, found...)
		if len(found) == BulkCap {
			msg = fmt.Sprintf(" (上限 %d 件で打ち切り)", BulkCap)
		}
	}

	n, err := s.st.AddToCollection(ctx, c.ID, urls, r.FormValue("note"))
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err)
		return
	}
	skipped := len(urls) - n
	flash := fmt.Sprintf("「%s」に %d 件を追加しました%s", c.Title, n, msg)
	if skipped > 0 {
		flash += fmt.Sprintf(" — %d 件は既に入っていました", skipped)
	}
	redirect(w, r, backTo(r, s.u("/collections/"+c.Slug)), flash)
}

func (s *Server) handleCollectionRemove(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.queryWait)
	defer cancel()

	c, err := s.collectionFromForm(ctx, r)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, err)
		return
	}
	n, err := s.st.RemoveFromCollection(ctx, c.ID, r.Form["url"])
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, err)
		return
	}
	redirect(w, r, backTo(r, s.u("/collections/"+c.Slug)),
		fmt.Sprintf("「%s」から %d 件を外しました", c.Title, n))
}

func (s *Server) handleCollectionMove(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.queryWait)
	defer cancel()

	c, err := s.collectionFromForm(ctx, r)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, err)
		return
	}
	delta := -1
	if r.FormValue("dir") == "down" {
		delta = 1
	}
	if err := s.st.MoveMember(ctx, c.ID, r.FormValue("url"), delta); err != nil {
		s.fail(w, r, http.StatusInternalServerError, err)
		return
	}
	redirect(w, r, backTo(r, s.u("/collections/"+c.Slug)), "")
}

func (s *Server) handleCollectionNote(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.queryWait)
	defer cancel()

	c, err := s.collectionFromForm(ctx, r)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, err)
		return
	}
	if err := s.st.SetMemberNote(ctx, c.ID, r.FormValue("url"), r.FormValue("note")); err != nil {
		s.fail(w, r, http.StatusInternalServerError, err)
		return
	}
	redirect(w, r, backTo(r, s.u("/collections/"+c.Slug)), "覚書を保存しました")
}

func (s *Server) collectionFromForm(ctx context.Context, r *http.Request) (*store.Collection, error) {
	id := formInt64(r, "collection_id")
	if id == 0 {
		return nil, errors.New("コレクションが指定されていません")
	}
	c, err := s.st.CollectionByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, errors.New("そのコレクションはありません")
	}
	return c, nil
}

// ---- ヘルパ ----

func formInt64(r *http.Request, key string) int64 {
	v, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue(key)), 10, 64)
	return v
}

// backTo は操作後の戻り先。作品詳細や検索結果から追加したときは、そこへ戻す。
// 外部サイトへ飛ばされないよう、自サイト内の絶対パスだけ許す。
func backTo(r *http.Request, def string) string {
	b := r.FormValue("back")
	if strings.HasPrefix(b, "/") && !strings.HasPrefix(b, "//") {
		return b
	}
	return def
}

func redirect(w http.ResponseWriter, r *http.Request, to, flash string) {
	if flash != "" {
		sep := "?"
		if strings.Contains(to, "?") {
			sep = "&"
		}
		to += sep + "flash=" + url.QueryEscape(flash)
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// requirePost は状態を変える操作を POST に限り、他サイトのページからの
// 送信を弾く。finder には認証もセッションも無いが、公開する場合に
// 「開いただけでコレクションが書き換わる」ことは防いでおく。
func (s *Server) requirePost(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.st.HasCollections {
			s.fail(w, r, http.StatusNotFound,
				errors.New("コレクションは無効です (-collections を空にして起動しています)"))
			return
		}
		if err := r.ParseForm(); err != nil {
			s.fail(w, r, http.StatusBadRequest, err)
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			if u, err := url.Parse(o); err != nil || u.Host != r.Host {
				s.fail(w, r, http.StatusForbidden, errors.New("別のオリジンからの送信は受け付けません"))
				return
			}
		}
		next(w, r)
	}
}

// searchParams はクエリ文字列から検索条件を組み立てる。検索画面と
// 「まとめて追加」で同じ関数を使わないと、見えている集合と入る集合がずれる。
func searchParams(r *http.Request) store.SearchParams {
	return store.SearchParams{
		Q:          q(r, "q"),
		Raw:        qBool(r, "raw"),
		Like:       qBool(r, "like"),
		Sources:    qList(r, "source"),
		Style:      q(r, "style"),
		Category:   q(r, "category"),
		Medium:     q(r, "medium"),
		Origin:     q(r, "origin"),
		Artist:     q(r, "artist"),
		PersonKey:  q(r, "person_key"),
		YearFrom:   qIntPtr(r, "year_from"),
		YearTo:     qIntPtr(r, "year_to"),
		YearKind:   q(r, "year_kind"),
		Precision:  qList(r, "precision"),
		HasJa:      q(r, "has_ja"),
		ArtistCond: q(r, "artist_cond"),
		Sort:       q(r, "sort"),
		Page:       qInt(r, "page", 1),
		Per:        qInt(r, "per", 50),
	}
}
