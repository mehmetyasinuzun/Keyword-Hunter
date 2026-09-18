package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// newTestServer gerçek DB ve kimlik deposu ile tam yönlendirme tablosunu kurar.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := newWebTestDB(t)
	creds, err := newCredentialStore("admin", "correct-horse", "")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		db:            db,
		creds:         creds,
		loginGuard:    newLoginGuard(),
		sessionTTL:    time.Hour,
		router:        gin.New(),
		rateLimiter:   NewIPRateLimiter(1000, 1000),
		cleanupStop:   make(chan struct{}),
		watchlistStop: make(chan struct{}),
		tagEngine:     mockTagEngine{},
		batchRunner:   mockBatchRunner{},
		startedAt:     time.Now(),
	}
	s.router.Use(securityHeaders(false))
	s.router.Use(s.rateLimiter.Middleware())
	s.setupRoutes()
	return s
}

func do(s *Server, method, path string, form url.Values, cookies []*http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	return w
}

func login(t *testing.T, s *Server) (session, csrf *http.Cookie) {
	t.Helper()
	w := do(s, http.MethodPost, "/login", url.Values{"username": {"admin"}, "password": {"correct-horse"}}, nil, nil)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/dashboard" {
		t.Fatalf("login başarısız: %d %s", w.Code, w.Header().Get("Location"))
	}
	for _, ck := range w.Result().Cookies() {
		switch ck.Name {
		case "session":
			session = ck
		case "csrf_token":
			csrf = ck
		}
	}
	if session == nil || csrf == nil {
		t.Fatal("oturum çerezleri ayarlanmadı")
	}
	if !session.HttpOnly || session.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session çerezi HttpOnly+Lax olmalı: %+v", session)
	}
	return session, csrf
}

func TestAuthFlow_ProtectedRoutesRequireSession(t *testing.T) {
	s := newTestServer(t)

	w := do(s, http.MethodGet, "/dashboard", nil, nil, nil)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/login" {
		t.Fatalf("oturumsuz /dashboard yönlendirmeli: %d", w.Code)
	}
	w = do(s, http.MethodGet, "/api/stats", nil, nil, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("oturumsuz API 401 dönmeli: %d", w.Code)
	}

	w = do(s, http.MethodPost, "/login", url.Values{"username": {"admin"}, "password": {"nope"}}, nil, nil)
	if !strings.Contains(w.Header().Get("Location"), "error=1") {
		t.Fatalf("yanlış parola error=1 ile dönmeli: %s", w.Header().Get("Location"))
	}

	session, csrf := login(t, s)
	w = do(s, http.MethodGet, "/dashboard", nil, []*http.Cookie{session}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("oturumlu /dashboard 200 olmalı: %d", w.Code)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("kimlikli sayfa no-store olmalı: %q", cc)
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("CSP eksik: %q", csp)
	}

	// CSRF: token olmadan 403, token ile geçer
	w = do(s, http.MethodPost, "/api/alert-config", nil, []*http.Cookie{session}, map[string]string{"Content-Type": "application/json"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("CSRF token'sız POST 403 olmalı: %d", w.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/alert-config", strings.NewReader(`{"webhookUrl":"","minCriticality":3,"enabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf.Value)
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("CSRF token ile POST 200 olmalı: %d %s", rec.Code, rec.Body.String())
	}

	// Siteler-arası GET /logout oturumu düşürmemeli
	w = do(s, http.MethodGet, "/logout", nil, []*http.Cookie{session}, map[string]string{"Sec-Fetch-Site": "cross-site"})
	if w.Header().Get("Location") != "/dashboard" {
		t.Fatalf("cross-site logout engellenmeli: %s", w.Header().Get("Location"))
	}
	w = do(s, http.MethodGet, "/dashboard", nil, []*http.Cookie{session}, nil)
	if w.Code != http.StatusOK {
		t.Fatal("cross-site logout sonrası oturum hâlâ geçerli olmalı")
	}
	// Aynı-site logout çalışır
	w = do(s, http.MethodGet, "/logout", nil, []*http.Cookie{session}, map[string]string{"Sec-Fetch-Site": "same-origin"})
	if w.Header().Get("Location") != "/login" {
		t.Fatal("logout /login'e yönlendirmeli")
	}
	w = do(s, http.MethodGet, "/dashboard", nil, []*http.Cookie{session}, nil)
	if w.Code != http.StatusFound {
		t.Fatal("logout sonrası oturum geçersiz olmalı")
	}
}

func TestLogin_LockoutAfterRepeatedFailures(t *testing.T) {
	s := newTestServer(t)
	for i := 0; i < loginMaxFailures; i++ {
		do(s, http.MethodPost, "/login", url.Values{"username": {"admin"}, "password": {"bad"}}, nil, nil)
	}
	w := do(s, http.MethodPost, "/login", url.Values{"username": {"admin"}, "password": {"correct-horse"}}, nil, nil)
	if !strings.Contains(w.Header().Get("Location"), "error=locked") {
		t.Fatalf("kilit sonrası doğru parola bile reddedilmeli: %s", w.Header().Get("Location"))
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After başlığı bekleniyor")
	}
}

func TestHealthz_PublicAndUnauthenticated(t *testing.T) {
	s := newTestServer(t)
	w := do(s, http.MethodGet, "/healthz", nil, nil, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status":"ok"`) {
		t.Fatalf("healthz: %d %s", w.Code, w.Body.String())
	}
}

func TestIndex_RedirectsBySessionState(t *testing.T) {
	s := newTestServer(t)
	if w := do(s, http.MethodGet, "/", nil, nil, nil); w.Header().Get("Location") != "/login" {
		t.Fatal("oturumsuz / → /login")
	}
	session, _ := login(t, s)
	if w := do(s, http.MethodGet, "/", nil, []*http.Cookie{session}, nil); w.Header().Get("Location") != "/dashboard" {
		t.Fatal("oturumlu / → /dashboard")
	}
}
