package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

const (
	testAdminUser     = "admin"
	testAdminPassword = "secret"
)

func TestAdminRequiresBasicAuth(t *testing.T) {
	f := newDBFixture(t)
	f.srv = New(Options{DB: f.db, ArtistMinWorks: 2, AdminUser: testAdminUser, AdminPassword: testAdminPassword,
		Dist: fstest.MapFS{
			"index.html": {Data: []byte("<html><head></head></html>")},
			"admin.html": {Data: []byte("<html></html>")},
		}})
	serve := func(path, user, pass string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if user != "" {
			req.SetBasicAuth(user, pass)
		}
		rec := httptest.NewRecorder()
		f.srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	for _, p := range []string{"/admin", "/admin/c/1", "/api/admin/collections", "/api/admin/img/1/400"} {
		if rec := serve(p, "", ""); rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("%s: 資格情報なしで code = %d", p, rec.Code)
		}
		if rec := serve(p, testAdminUser, "wrong"); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: パスワード違いで code = %d", p, rec.Code)
		}
	}
	if rec := serve("/api/admin/collections", testAdminUser, testAdminPassword); rec.Code != 200 {
		t.Errorf("正しい資格情報で code = %d", rec.Code)
	}
	for _, p := range []string{"/", "/c/pub", "/api/collections"} {
		if rec := serve(p, "", ""); rec.Code != 200 {
			t.Errorf("%s: 公開画面は認証なしで見られる: code = %d", p, rec.Code)
		}
	}
}

func TestAdminClosedWithoutPassword(t *testing.T) {
	f := newDBFixture(t)
	f.srv = New(Options{DB: f.db, ArtistMinWorks: 2, AdminUser: testAdminUser})
	req := httptest.NewRequest(http.MethodGet, "/api/admin/collections", nil)
	req.SetBasicAuth(testAdminUser, "")
	rec := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("パスワード未設定なら閉じる: code = %d", rec.Code)
	}
}
