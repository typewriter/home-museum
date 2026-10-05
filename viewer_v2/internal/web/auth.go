package web

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
)

// requireAdmin は管理画面と管理 API に basic 認証を掛ける。
// 管理者は 1 人なので、資格情報は環境変数で渡す 1 組だけにした。
// パスワードが空なら開けずに閉じる。設定し忘れたまま公開しても、管理画面が
// 誰でも開ける状態にならないように。
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.adminPassword == "" {
			http.Error(w, "管理画面は無効です。VIEWER_ADMIN_PASSWORD を設定してください", http.StatusServiceUnavailable)
			return
		}
		user, pass, ok := r.BasicAuth()
		if !ok || !secureEqual(user, s.adminUser) || !secureEqual(pass, s.adminPassword) {
			w.Header().Set("WWW-Authenticate", `Basic realm="admin", charset="UTF-8"`)
			http.Error(w, "認証が必要です", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// secureEqual は長さの違いからも中身が漏れないよう、ハッシュを揃えてから比べる。
func secureEqual(a, b string) bool {
	ha, hb := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}
