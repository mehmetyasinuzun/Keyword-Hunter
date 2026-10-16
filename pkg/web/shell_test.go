package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"keywordhunter-mvp/pkg/storage"
)

// Kabuk sunucu tarafında role göre süzülür: viewer admin menü bağlantılarını
// HTML'de hiç almamalı; admin almalı. Tema ve dil çerezleri ilk baytta basılmalı.
func TestShell_ServerSideRoleThemeLang(t *testing.T) {
	s := newTestServer(t)
	hash, _ := bcrypt.GenerateFromPassword([]byte("viewer-pass-1"), bcrypt.MinCost)
	if _, err := s.db.CreateUser("shellviewer", string(hash), storage.RoleViewer); err != nil {
		t.Fatal(err)
	}
	viewer := loginAs(t, s, "shellviewer", "viewer-pass-1")
	viewer = append(viewer, &http.Cookie{Name: "theme", Value: "light"}, &http.Cookie{Name: "lang", Value: "en"})

	w := do(s, http.MethodGet, "/dashboard", nil, viewer, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("dashboard: %d", w.Code)
	}
	body := w.Body.String()
	for _, admin := range []string{`href="/users"`, `href="/settings"`} {
		if strings.Contains(body, admin) {
			t.Errorf("viewer HTML'inde admin bağlantısı var: %s", admin)
		}
	}
	if !strings.Contains(body, `data-theme="light"`) || !strings.Contains(body, `lang="en"`) {
		t.Error("tema/dil sunucu tarafında basılmadı")
	}
	if !strings.Contains(body, `class="role-viewer"`) {
		t.Error("viewer rol kancası eksik")
	}

	// Geçersiz tema değeri enjekte edilemez → auto
	bad := append(viewer[:len(viewer)-2:len(viewer)-2], &http.Cookie{Name: "theme", Value: `x" onload="alert(1)`})
	w = do(s, http.MethodGet, "/dashboard", nil, bad, nil)
	if !strings.Contains(w.Body.String(), `data-theme="auto"`) {
		t.Error("geçersiz tema çerezi auto'ya düşmeli")
	}
}

func TestErrorPages(t *testing.T) {
	s := newTestServer(t)
	w := do(s, http.MethodGet, "/boyle-bir-sayfa-yok", nil, nil, nil)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "KeywordHunter") {
		t.Fatalf("404 sayfası: %d", w.Code)
	}
	w = do(s, http.MethodGet, "/api/boyle-bir-uc-yok", nil, nil, nil)
	if w.Code == http.StatusOK || strings.Contains(w.Body.String(), "<html") {
		t.Fatalf("API 404 HTML dönmemeli: %d", w.Code)
	}

	hash, _ := bcrypt.GenerateFromPassword([]byte("analyst-pass-1"), bcrypt.MinCost)
	if _, err := s.db.CreateUser("erranalyst", string(hash), storage.RoleAnalyst); err != nil {
		t.Fatal(err)
	}
	ck := loginAs(t, s, "erranalyst", "analyst-pass-1")
	w = do(s, http.MethodGet, "/users", nil, ck, nil)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "<html") {
		t.Fatalf("analist /users: %d (markalı 403 bekleniyordu)", w.Code)
	}
}

// POST /search viewer için yasak, CSRF'siz istek reddedilir.
func TestSearchPost_RequiresAnalystAndCSRF(t *testing.T) {
	s := newTestServer(t)
	hash, _ := bcrypt.GenerateFromPassword([]byte("viewer-pass-1"), bcrypt.MinCost)
	if _, err := s.db.CreateUser("searchviewer", string(hash), storage.RoleViewer); err != nil {
		t.Fatal(err)
	}
	viewer := loginAs(t, s, "searchviewer", "viewer-pass-1")
	form := url.Values{"query": {"test"}, "_csrf": {csrfOf(viewer)}}
	if w := do(s, http.MethodPost, "/search", form, viewer, nil); w.Code != http.StatusForbidden {
		t.Fatalf("viewer arama: %d (403 bekleniyordu)", w.Code)
	}
	admin := loginAs(t, s, "admin", "correct-horse")
	if w := do(s, http.MethodPost, "/search", url.Values{"query": {"test"}}, admin, nil); w.Code != http.StatusForbidden {
		t.Fatalf("CSRF'siz arama: %d (403 bekleniyordu)", w.Code)
	}
}
