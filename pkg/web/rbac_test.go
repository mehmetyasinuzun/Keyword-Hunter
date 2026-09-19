package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"keywordhunter-mvp/pkg/storage"
)

// jsonPost oturum çerezleri + CSRF başlığıyla JSON POST yapar ve durum kodunu döndürür.
// do() yalnız form gövdesi desteklediği için JSON isteği burada kurulur.
func jsonPost(s *Server, path, body string, cookies []*http.Cookie) int {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrfOf(cookies))
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	return w.Code
}

// TestRBAC_ViewerCanChangeOwnPasswordButNotWrite: viewer öz-hizmet parola
// değişikliğini yapabilmeli (aksi halde ele geçirilmiş parolasını asla
// döndüremez) ama gerçek yazma uçlarında 403 almalı — istisna dar olmalı.
func TestRBAC_ViewerCanChangeOwnPasswordButNotWrite(t *testing.T) {
	s := newTestServer(t)

	hash, err := bcrypt.GenerateFromPassword([]byte("viewer-pass-1"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.CreateUser("qaviewer", string(hash), storage.RoleViewer); err != nil {
		t.Fatal(err)
	}

	cookies := loginAs(t, s, "qaviewer", "viewer-pass-1")

	// 1) Kendi parolasını değiştirebilmeli (öz-hizmet istisnası)
	if code := jsonPost(s, "/api/me/password", `{"current":"viewer-pass-1","new":"viewer-pass-2"}`, cookies); code != http.StatusOK {
		t.Fatalf("viewer kendi parolasını değiştiremedi: %d (200 bekleniyordu)", code)
	}

	// 2) Ama gerçek bir yazma ucu hâlâ yasak olmalı — istisna yalnız parola içindir
	if code := jsonPost(s, "/api/results/1/case", `{"status":"new","note":""}`, cookies); code != http.StatusForbidden {
		t.Fatalf("viewer yazma ucunda %d aldı (403 bekleniyordu)", code)
	}

	// 3) Yeni parolayla giriş çalışmalı, eski parola reddedilmeli
	loginAs(t, s, "qaviewer", "viewer-pass-2")
	w := do(s, http.MethodPost, "/login", url.Values{"username": {"qaviewer"}, "password": {"viewer-pass-1"}}, nil, nil)
	if w.Code == http.StatusFound && strings.HasSuffix(w.Header().Get("Location"), "/dashboard") {
		t.Fatal("eski parola ile giriş hâlâ başarılı — parola gerçekten değişmemiş")
	}
}
