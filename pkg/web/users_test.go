package web

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/storage"
)

func newMultiUserServer(t *testing.T) *Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := newWebTestDB(t)
	creds, _ := newCredentialStore("admin", "adminpass1", "")
	// bootstrap admin into users
	hash, _ := creds.Update("", "")
	if _, err := db.CreateUser("admin", hash, storage.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	s := &Server{
		db: db, creds: creds, loginGuard: newLoginGuard(), sessionTTL: time.Hour,
		router: gin.New(), rateLimiter: NewIPRateLimiter(1000, 1000),
		cleanupStop: make(chan struct{}), watchlistStop: make(chan struct{}),
		tagEngine: mockTagEngine{}, batchRunner: mockBatchRunner{}, startedAt: time.Now(),
	}
	s.router.Use(securityHeaders(false))
	s.setupRoutes()
	return s
}

func loginAs(t *testing.T, s *Server, user, pass string) []*http.Cookie {
	t.Helper()
	w := do(s, http.MethodPost, "/login", url.Values{"username": {user}, "password": {pass}}, nil, nil)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/dashboard" {
		t.Fatalf("login %s başarısız: %d %s", user, w.Code, w.Header().Get("Location"))
	}
	return w.Result().Cookies()
}

func csrfOf(cookies []*http.Cookie) string {
	for _, c := range cookies {
		if c.Name == "csrf_token" {
			return c.Value
		}
	}
	return ""
}

func TestRBAC_RolesEnforced(t *testing.T) {
	s := newMultiUserServer(t)
	adminCk := loginAs(t, s, "admin", "adminpass1")
	adminCsrf := csrfOf(adminCk)

	// Admin creates analyst + viewer
	for _, body := range []string{
		`{"username":"analyst1","password":"analystpass","role":"analyst"}`,
		`{"username":"viewer1","password":"viewerpass1","role":"viewer"}`,
	} {
		w := doJSON(s, http.MethodPost, "/api/users", body, adminCk, adminCsrf)
		if w.Code != http.StatusOK {
			t.Fatalf("kullanıcı oluşturma: %d %s", w.Code, w.Body.String())
		}
	}

	// Analyst: can write (alert-config) but NOT manage users or settings
	anCk := loginAs(t, s, "analyst1", "analystpass")
	anCsrf := csrfOf(anCk)
	if w := doJSON(s, http.MethodPost, "/api/alert-config", `{"webhookUrl":"","minCriticality":3,"enabled":false}`, anCk, anCsrf); w.Code != http.StatusOK {
		t.Fatalf("analist alert-config yazamadı: %d %s", w.Code, w.Body.String())
	}
	if w := doJSON(s, http.MethodPost, "/api/users", `{"username":"x","password":"xxxxxxxx","role":"viewer"}`, anCk, anCsrf); w.Code != http.StatusForbidden {
		t.Fatalf("analist kullanıcı oluşturabildi: %d", w.Code)
	}
	if w := doJSON(s, http.MethodPost, "/api/settings/env", `{}`, anCk, anCsrf); w.Code != http.StatusForbidden {
		t.Fatalf("analist ayar değiştirebildi: %d", w.Code)
	}
	if w := do(s, http.MethodGet, "/api/whoami", nil, anCk, nil); w.Code != http.StatusOK {
		t.Fatalf("whoami: %d", w.Code)
	}

	// Viewer: read OK, any write forbidden
	vCk := loginAs(t, s, "viewer1", "viewerpass1")
	vCsrf := csrfOf(vCk)
	if w := do(s, http.MethodGet, "/api/stats", nil, vCk, nil); w.Code != http.StatusOK {
		t.Fatalf("viewer okuyamadı: %d", w.Code)
	}
	if w := doJSON(s, http.MethodPost, "/api/alert-config", `{"enabled":false}`, vCk, vCsrf); w.Code != http.StatusForbidden {
		t.Fatalf("viewer yazabildi: %d", w.Code)
	}

	// Cannot delete last admin
	admin, _ := s.db.GetUserByUsername("admin")
	if w := doJSON(s, http.MethodPost, "/api/users/"+itoa(admin.ID)+"/delete", `{}`, adminCk, adminCsrf); w.Code != http.StatusBadRequest {
		t.Fatalf("son admin silinebildi: %d %s", w.Code, w.Body.String())
	}

	// Disabled analyst cannot log in
	if err := s.db.SetUserEnabled(mustUserID(t, s, "analyst1"), false); err != nil {
		t.Fatal(err)
	}
	w := do(s, http.MethodPost, "/login", url.Values{"username": {"analyst1"}, "password": {"analystpass"}}, nil, nil)
	if w.Header().Get("Location") == "/dashboard" {
		t.Fatal("pasif kullanıcı giriş yapabildi")
	}
}

func mustUserID(t *testing.T, s *Server, name string) int64 {
	u, err := s.db.GetUserByUsername(name)
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func itoa(i int64) string {
	return strconvFormat(i)
}
