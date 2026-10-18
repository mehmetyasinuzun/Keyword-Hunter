package web

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	"keywordhunter-mvp/pkg/config"
	"keywordhunter-mvp/pkg/metrics"
	"keywordhunter-mvp/pkg/storage"
)

// newMetricsServer METRICS_ENABLED=true ile (New'in kullandığı
// useBaseMiddleware ile: HTTP metrik ara katmanı en dışta) test sunucusu kurar.
func newMetricsServer(t *testing.T) *Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := newWebTestDB(t)
	creds, err := newCredentialStore("admin", "correct-horse", "")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		db: db, creds: creds, loginGuard: newLoginGuard(), sessionTTL: time.Hour,
		router: gin.New(), rateLimiter: NewIPRateLimiter(1000, 1000),
		cleanupStop: make(chan struct{}), watchlistStop: make(chan struct{}),
		tagEngine: mockTagEngine{}, batchRunner: mockBatchRunner{}, startedAt: time.Now(),
		metricsEnabled: true,
	}
	useBaseMiddleware(s.router, true, false)
	s.router.Use(s.rateLimiter.Middleware())
	s.setupRoutes()
	return s
}

// metricsToken admin (ya da verilen rol) hesabı açıp token üretir.
func metricsToken(t *testing.T, s *Server, user, role, scope string) string {
	t.Helper()
	ck := mkTokenUser(t, s, user, user+"-pass-123", role)
	return createToken(t, s, ck, "prometheus", scope, 0).Token
}

func scrape(s *Server, auth string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	return w
}

// parseExposition Prometheus metin biçimini ayrıştırır (geçersizse test düşer).
func parseExposition(t *testing.T, w *httptest.ResponseRecorder) map[string]*dto.MetricFamily {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("kazıma: %d %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("Content-Type: %q", ct)
	}
	p := expfmt.NewTextParser(model.UTF8Validation)
	fams, err := p.TextToMetricFamilies(strings.NewReader(w.Body.String()))
	if err != nil {
		t.Fatalf("exposition ayrıştırılamadı: %v", err)
	}
	return fams
}

func scrapeOK(t *testing.T, s *Server, tok string) map[string]*dto.MetricFamily {
	t.Helper()
	return parseExposition(t, scrape(s, "Bearer "+tok))
}

func metricValue(m *dto.Metric) float64 {
	switch {
	case m.Gauge != nil:
		return m.GetGauge().GetValue()
	case m.Counter != nil:
		return m.GetCounter().GetValue()
	case m.Untyped != nil:
		return m.GetUntyped().GetValue()
	case m.Histogram != nil:
		return float64(m.GetHistogram().GetSampleCount())
	}
	return 0
}

// sample ad + etiket alt kümesine uyan serinin değeri.
func sample(fams map[string]*dto.MetricFamily, name string, labels ...string) (float64, bool) {
	f, ok := fams[name]
	if !ok {
		return 0, false
	}
next:
	for _, m := range f.GetMetric() {
		got := map[string]string{}
		for _, l := range m.GetLabel() {
			got[l.GetName()] = l.GetValue()
		}
		for i := 0; i+1 < len(labels); i += 2 {
			if got[labels[i]] != labels[i+1] {
				continue next
			}
		}
		return metricValue(m), true
	}
	return 0, false
}

func mustSample(t *testing.T, fams map[string]*dto.MetricFamily, name string, labels ...string) float64 {
	t.Helper()
	v, ok := sample(fams, name, labels...)
	if !ok {
		t.Fatalf("%s%v serisi yok", name, labels)
	}
	return v
}

// ─────────────────────────── Açma / kimlik ───────────────────────────

func TestMetrics_DisabledIs404(t *testing.T) {
	s := newTestServer(t) // metricsEnabled=false
	ck := mkTokenUser(t, s, "ops", "ops-pass-123", storage.RoleAdmin)
	tok := createToken(t, s, ck, "prometheus", storage.TokenScopeMetrics, 0).Token
	if w := scrape(s, "Bearer "+tok); w.Code != http.StatusNotFound {
		t.Fatalf("kapalıyken 404 beklenir: %d", w.Code)
	}
	if w := scrape(s, ""); w.Code != http.StatusNotFound {
		t.Fatalf("kapalıyken anonim 404 beklenir: %d", w.Code)
	}
}

func TestMetrics_AuthRequired(t *testing.T) {
	s := newMetricsServer(t)
	admin := metricsToken(t, s, "ops", storage.RoleAdmin, storage.TokenScopeMetrics)
	adminRead := createToken(t, s, loginAs(t, s, "ops", "ops-pass-123"), "siem", storage.TokenScopeRead, 0).Token
	adminWrite := metricsToken(t, s, "ops2", storage.RoleAdmin, storage.TokenScopeWrite)
	analyst := metricsToken(t, s, "ana", storage.RoleAnalyst, storage.TokenScopeWrite)
	viewer := metricsToken(t, s, "vie", storage.RoleViewer, storage.TokenScopeRead)

	// Anonim, biçimsiz, bilinmeyen token → 401 + WWW-Authenticate: Bearer.
	assert401(t, scrape(s, ""), "anonim")
	assert401(t, scrape(s, "Basic b3BzOng="), "Basic şeması")
	assert401(t, scrape(s, "Bearer not-a-token"), "biçimsiz token")
	assert401(t, scrape(s, "Bearer "+storage.APITokenPrefix+strings.Repeat("a", 43)), "bilinmeyen token")
	if w := scrape(s, ""); apiCode(w) != "metrics_token_required" {
		t.Fatalf("hata kodu: %q", apiCode(w))
	}

	// Genel amaçlı read/write token'ları (sahibi admin olsa bile) → 403:
	// tüm bulgulara erişen API kimliği kazıma yapılandırmasına konmasın.
	for name, tok := range map[string]string{"admin-read": adminRead, "admin-write": adminWrite, "analyst": analyst, "viewer": viewer} {
		w := scrape(s, "Bearer "+tok)
		if w.Code != http.StatusForbidden || apiCode(w) != "metrics_scope_required" {
			t.Fatalf("%s: 403 metrics_scope_required beklenir: %d %s", name, w.Code, w.Body.String())
		}
	}
	// Ret denetlenir (token başına dakikada bir).
	ev, _, err := s.db.ListAuditEvents(storage.AuditFilter{ActionPrefixes: []string{"access.denied"}, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range ev {
		if strings.Contains(e.Details, "token_scope") {
			found = true
		}
	}
	if !found {
		t.Fatal("yanlış kapsamlı kazıma denemesi denetim kaydına yazılmalı")
	}

	// Admin hesabının metrics token'ı → 200.
	if w := scrape(s, "Bearer "+admin); w.Code != http.StatusOK {
		t.Fatalf("admin metrics token: %d %s", w.Code, w.Body.String())
	}
	// Oturum çerezi bu uçta kimlik sayılmaz (CSRF/ortam kimliği yok).
	adminCk := loginAs(t, s, "ops", "ops-pass-123")
	assert401(t, scrape(s, "", adminCk...), "yalnız oturum çerezi")
	if w := scrape(s, "Bearer "+admin, adminCk...); w.Code != http.StatusOK {
		t.Fatalf("çerez + Bearer: Bearer geçerli olmalı: %d", w.Code)
	}

	// Rol her istekte tazelenir: admin'likten düşürülen hesabın token'ı 403.
	u, err := s.db.GetUserByUsername("ops")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.UpdateUserRole(u.ID, storage.RoleAnalyst); err != nil {
		t.Fatal(err)
	}
	if w := scrape(s, "Bearer "+admin); w.Code != http.StatusForbidden || apiCode(w) != "metrics_admin_only" {
		t.Fatalf("rolü düşürülen hesap: %d %s", w.Code, w.Body.String())
	}
	ev, _, err = s.db.ListAuditEvents(storage.AuditFilter{ActionPrefixes: []string{"access.denied"}, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, e := range ev {
		if strings.Contains(e.Details, "metrics_role") {
			found = true
		}
	}
	if !found {
		t.Fatal("admin dışı sahibin kazıma denemesi denetim kaydına yazılmalı")
	}
}

// "metrics" kapsamlı token yalnız /metrics'i kazır: /api/ ve /taxii2/'de
// reddedilir (kazıma sunucusundan sızan token bulgu okuyamaz) ve yalnız
// admin rolü üretebilir.
func TestMetrics_MetricsScopeTokenIsConfined(t *testing.T) {
	s := newMetricsServer(t)
	tok := metricsToken(t, s, "ops", storage.RoleAdmin, storage.TokenScopeMetrics)
	for _, path := range []string{"/api/whoami", "/api/export/results?format=json", "/api/stats"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden || apiCode(w) != "token_metrics_only" {
			t.Fatalf("%s: 403 token_metrics_only beklenir: %d %s", path, w.Code, w.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/api/update-criticality", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("metrics token ile yazma: %d", w.Code)
	}
	for _, path := range []string{"/taxii2/", "/taxii2/" + taxiiAPIRoot + "/collections/"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Accept", "application/taxii+json;version=2.1")
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s: metrics token 403 almalı: %d %s", path, w.Code, w.Body.String())
		}
	}
	if w := scrape(s, "Bearer "+tok); w.Code != http.StatusOK {
		t.Fatalf("/metrics: %d", w.Code)
	}

	// Admin dışı rol metrics kapsamlı token üretemez.
	for _, role := range []string{storage.RoleAnalyst, storage.RoleViewer} {
		ck := mkTokenUser(t, s, "u-"+role, "u-"+role+"-pass-123", role)
		w := doJSON(s, http.MethodPost, "/api/tokens", `{"name":"p","scope":"metrics","expiresInDays":0}`, ck, csrfOf(ck))
		if w.Code != http.StatusBadRequest || apiCode(w) != "token_metrics_admin_only" {
			t.Fatalf("%s metrics token üretebilmemeli: %d %s", role, w.Code, w.Body.String())
		}
		// Hesabım → Yeni token: Metrik seçeneği yalnız admin'e gösterilir.
		if body := do(s, http.MethodGet, "/account", nil, ck, nil).Body.String(); strings.Contains(body, `value="metrics"`) {
			t.Fatalf("%s için metrics seçeneği gösterilmemeli", role)
		}
	}
	adminCk := loginAs(t, s, "ops", "ops-pass-123")
	if body := do(s, http.MethodGet, "/account", nil, adminCk, nil).Body.String(); !strings.Contains(body, `name="tok-scope" value="metrics"`) {
		t.Fatal("admin için metrics kapsam seçeneği yok")
	}
}

func TestMetrics_TwoFactorPolicyBlocksOwnerWithout2FA(t *testing.T) {
	s := newMetricsServer(t)
	tok := metricsToken(t, s, "ops", storage.RoleAdmin, storage.TokenScopeMetrics)
	s.require2FA = config.Require2FAAdmins
	if w := scrape(s, "Bearer "+tok); w.Code != http.StatusForbidden || apiCode(w) != "2fa_required" {
		t.Fatalf("REQUIRE_2FA kapsamında 2FA'sız admin token'ı reddedilmeli: %d %s", w.Code, w.Body.String())
	}
}

// ─────────────────────────── İçerik ───────────────────────────

// fakeSOCKS5 kimliksiz SOCKS5 selamlamasına 05 00 ile yanıt veren dinleyici.
func fakeSOCKS5(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				buf := make([]byte, 3)
				if _, err := c.Read(buf); err == nil && buf[0] == 0x05 {
					_, _ = c.Write([]byte{0x05, 0x00})
				}
			}(conn)
		}
	}()
	return ln.Addr().String()
}

func TestMetrics_ExpositionReflectsState(t *testing.T) {
	s := newMetricsServer(t)
	s.torProxy = fakeSOCKS5(t)
	s.backupDir = t.TempDir()
	tok := metricsToken(t, s, "ops", storage.RoleAdmin, storage.TokenScopeMetrics)

	// Tohum: 1×sev2, 3×sev4 bulgu; 1 down + 1 up izleme; iki motor; başarısız
	// yedek (önceki başarılı); doğrulanmış denetim zinciri.
	var res []storage.SearchResult
	for i, crit := range []int{2, 4, 4, 4} {
		res = append(res, storage.SearchResult{Title: "t", URL: "http://seed" + string(rune('a'+i)) + ".onion/secret-path", Source: "Torch", Query: "acme leak", Criticality: crit})
	}
	if _, err := s.db.SaveResults(res); err != nil {
		t.Fatal(err)
	}
	down, _ := s.db.AddWatchlistItem("d", "http://down.onion/", "Genel", "")
	up, _ := s.db.AddWatchlistItem("u", "http://up.onion/", "Genel", "")
	_ = s.db.RecordWatchlistCheck(down, "down", 0, 0, "", "", false)
	_ = s.db.RecordWatchlistCheck(up, "up", 200, 10, "", "", false)
	for _, n := range []string{"Ahmia", "Torch", "Haystak"} {
		if err := s.db.UpsertEngineStat(n, "http://"+n, true); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.db.UpdateEngineCheck("Ahmia", "up", 1200, 1)
	_ = s.db.UpdateEngineCheck("Ahmia", "up", 800, 1)
	_ = s.db.UpdateEngineCheck("Ahmia", "down", 9000, 0)
	_ = s.db.UpdateEngineCheck("Torch", "down", 2, 0) // reddedilen bağlantı ≈ 0 sn
	_ = s.db.UpdateEngineCheck("Haystak", "up", 750, 1)
	okAt := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	_ = s.db.RecordMaintenanceRun(storage.MaintenanceRun{Kind: storage.MaintenanceBackup, StartedAt: okAt, FinishedAt: okAt, OK: true})
	failAt := time.Now().Truncate(time.Second)
	_ = s.db.RecordMaintenanceRun(storage.MaintenanceRun{Kind: storage.MaintenanceBackup, StartedAt: failAt, FinishedAt: failAt, OK: false, Message: "x"})

	// Doğrulama yapılmadan zincir göstergeleri yoktur (yanıltıcı 0 yerine).
	fams := scrapeOK(t, s, tok)
	if _, ok := sample(fams, "keywordhunter_audit_chain_ok"); ok {
		t.Fatal("doğrulama öncesi audit_chain_ok yayımlanmamalı")
	}
	if _, err := s.auditChain(true); err != nil {
		t.Fatal(err)
	}
	s.metricsCollector.at = time.Time{} // önbelleği düşür
	fams = scrapeOK(t, s, tok)

	checks := []struct {
		name   string
		labels []string
		want   float64
	}{
		{"keywordhunter_build_info", []string{"version", Version}, 1},
		{"keywordhunter_findings", []string{"severity", "1"}, 0},
		{"keywordhunter_findings", []string{"severity", "2"}, 1},
		{"keywordhunter_findings", []string{"severity", "4"}, 3},
		{"keywordhunter_findings", []string{"severity", "5"}, 0},
		{"keywordhunter_active_sessions", nil, 1}, // metricsToken'daki giriş
		{"keywordhunter_watchlist_sites", nil, 2},
		{"keywordhunter_watchlist_sites_down", nil, 1},
		{"keywordhunter_engine_up", []string{"engine", "Ahmia"}, 0},
		{"keywordhunter_engine_up", []string{"engine", "Torch"}, 0},
		{"keywordhunter_engine_active", []string{"engine", "Ahmia"}, 1},
		{"keywordhunter_engine_up", []string{"engine", "Haystak"}, 1},
		{"keywordhunter_engine_response_seconds", []string{"engine", "Haystak"}, 0.75},
		{"keywordhunter_engine_success_ratio", []string{"engine", "Ahmia"}, 2.0 / 3.0},
		{"keywordhunter_engine_success_ratio", []string{"engine", "Torch"}, 0},
		{"keywordhunter_backup_enabled", nil, 1},
		{"keywordhunter_backup_last_status", nil, 0},
		{"keywordhunter_backup_last_success_timestamp_seconds", nil, float64(okAt.Unix())},
		{"keywordhunter_backup_last_run_timestamp_seconds", nil, float64(failAt.Unix())},
		{"keywordhunter_tor_reachable", nil, 1},
		{"keywordhunter_audit_chain_ok", nil, 1},
		{"keywordhunter_audit_chain_verify_error", nil, 0},
	}
	for _, c := range checks {
		if got := mustSample(t, fams, c.name, c.labels...); got != c.want {
			t.Errorf("%s%v = %v, beklenen %v", c.name, c.labels, got, c.want)
		}
	}
	// Düşük motorun yanıt süresi (hatanın süresi) yayımlanmaz: reddedilen
	// bağlantı onu "en hızlı" motor gibi gösterirdi.
	for _, e := range []string{"Ahmia", "Torch"} {
		if v, ok := sample(fams, "keywordhunter_engine_response_seconds", "engine", e); ok {
			t.Errorf("düşük motor %s için yanıt süresi yayımlanmamalı: %v", e, v)
		}
	}
	if v := mustSample(t, fams, "keywordhunter_audit_chain_last_verify_timestamp_seconds"); v < float64(time.Now().Add(-time.Minute).Unix()) {
		t.Errorf("son doğrulama zamanı: %v", v)
	}
	for _, name := range []string{"go_goroutines", "process_cpu_seconds_total", "keywordhunter_search_runs_total",
		"keywordhunter_http_requests_total", "keywordhunter_new_findings_total", "keywordhunter_login_attempts_total",
		"keywordhunter_retention_purged_total", "keywordhunter_notification_sends_total", "keywordhunter_scheduled_scan_runs_total",
		"keywordhunter_watchlist_checks_total", "keywordhunter_tor_circuit_renewals_total", "keywordhunter_metrics_snapshot_timestamp_seconds"} {
		if _, ok := fams[name]; !ok {
			t.Errorf("%s eksik", name)
		}
	}

	// Hassas değerler hiçbir etikette görünmez: sorgu, URL, kullanıcı adı, IP.
	for _, f := range fams {
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				v := l.GetValue()
				for _, bad := range []string{"acme", "onion", "secret-path", "ops", "192.0.2.", "127.0.0.1"} {
					if strings.Contains(v, bad) {
						t.Errorf("%s{%s=%q}: hassas değer etikette", f.GetName(), l.GetName(), v)
					}
				}
			}
		}
	}

	// Tor ucu kapanınca (önbellek yenilendikten sonra) 0.
	s.torProxy = "127.0.0.1:1"
	s.metricsCollector.at = time.Time{}
	if v := mustSample(t, scrapeOK(t, s, tok), "keywordhunter_tor_reachable"); v != 0 {
		t.Fatalf("erişilemeyen Tor: %v", v)
	}
}

// Veritabanı göstergeleri en fazla 30 sn'de bir okunur: sık kazıma SQLite'ı yormaz.
func TestMetrics_DBGaugesAreCached(t *testing.T) {
	s := newMetricsServer(t)
	tok := metricsToken(t, s, "ops", storage.RoleAdmin, storage.TokenScopeMetrics)
	if _, err := s.db.SaveResults([]storage.SearchResult{{Title: "a", URL: "http://c1.onion/", Source: "Torch", Query: "q", Criticality: 5}}); err != nil {
		t.Fatal(err)
	}
	if v := mustSample(t, scrapeOK(t, s, tok), "keywordhunter_findings", "severity", "5"); v != 1 {
		t.Fatalf("ilk kazıma: %v", v)
	}
	if _, err := s.db.SaveResults([]storage.SearchResult{{Title: "b", URL: "http://c2.onion/", Source: "Torch", Query: "q", Criticality: 5}}); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if v := mustSample(t, scrapeOK(t, s, tok), "keywordhunter_findings", "severity", "5"); v != 1 {
			t.Fatalf("önbellek süresi içinde değer değişmemeli: %v", v)
		}
	}
	if s.metricsCollector.refreshes != 1 {
		t.Fatalf("6 kazımada %d veritabanı okuması (1 beklenir)", s.metricsCollector.refreshes)
	}
	// Süre dolunca yeniden okunur.
	s.metricsCollector.at = time.Now().Add(-metricsCacheTTL - time.Second)
	if v := mustSample(t, scrapeOK(t, s, tok), "keywordhunter_findings", "severity", "5"); v != 2 {
		t.Fatalf("süre dolduktan sonra: %v", v)
	}
	if s.metricsCollector.refreshes != 2 {
		t.Fatalf("yenileme sayısı: %d", s.metricsCollector.refreshes)
	}
}

// HTTP ara katmanı rota KALIBINI etiketler; ham yol, kimlik numarası ya da
// sorgu dizgisi etikete girmez; eşleşmeyen yol "unmatched", bilinmeyen yöntem
// "OTHER" olur.
func TestMetrics_HTTPMiddlewareLabels(t *testing.T) {
	s := newMetricsServer(t)
	tok := metricsToken(t, s, "ops", storage.RoleAdmin, storage.TokenScopeMetrics)
	apiTok := createToken(t, s, loginAs(t, s, "ops", "ops-pass-123"), "api", storage.TokenScopeRead, 0).Token
	before := scrapeOK(t, s, tok)

	get := func(method, path string) int {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+apiTok)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
		return w.Code
	}
	if code := get(http.MethodGet, "/api/results/987654/artifacts?q=acme-secret"); code != http.StatusOK {
		t.Fatalf("artifacts: %d", code)
	}
	get(http.MethodGet, "/no/such/page-zz9")
	get(http.MethodGet, "/no/such/page-zz8")
	get("PROPFIND", "/dashboard")
	// Başarısız giriş: login_attempts_total{method="password",result="failure"}.
	do(s, http.MethodPost, "/login", url.Values{"username": {"ops"}, "password": {"wrong"}}, nil, nil)

	after := scrapeOK(t, s, tok)
	delta := func(name string, labels ...string) float64 {
		b, _ := sample(before, name, labels...)
		return mustSample(t, after, name, labels...) - b
	}
	if d := delta("keywordhunter_http_requests_total", "method", "GET", "route", "/api/results/:id/artifacts", "status_class", "2xx"); d != 1 {
		t.Errorf("rota kalıbı sayacı: %v", d)
	}
	if d := delta("keywordhunter_http_request_duration_seconds", "method", "GET", "route", "/api/results/:id/artifacts"); d != 1 {
		t.Errorf("rota kalıbı histogramı: %v", d)
	}
	if d := delta("keywordhunter_http_requests_total", "method", "GET", "route", "unmatched", "status_class", "4xx"); d != 2 {
		t.Errorf("unmatched: %v", d)
	}
	if d := delta("keywordhunter_http_requests_total", "method", "OTHER", "route", "unmatched", "status_class", "4xx"); d != 1 {
		t.Errorf("bilinmeyen yöntem: %v", d)
	}
	if d := delta("keywordhunter_http_requests_total", "method", "GET", "route", "/metrics", "status_class", "2xx"); d < 1 {
		t.Errorf("/metrics kendi isteğini saymalı: %v", d)
	}
	if d := delta("keywordhunter_http_requests_total", "method", "POST", "route", "/login", "status_class", "3xx"); d != 1 {
		t.Errorf("/login: %v", d)
	}
	if d := delta("keywordhunter_login_attempts_total", "method", "password", "result", "failure"); d != 1 {
		t.Errorf("başarısız giriş sayacı: %v", d)
	}

	routes := map[string]bool{}
	for _, m := range after["keywordhunter_http_requests_total"].GetMetric() {
		for _, l := range m.GetLabel() {
			switch l.GetName() {
			case "route":
				routes[l.GetValue()] = true
				if l.GetValue() != "unmatched" && !strings.HasPrefix(l.GetValue(), "/") {
					t.Errorf("route etiketi kalıp değil: %q", l.GetValue())
				}
				for _, bad := range []string{"987654", "zz9", "zz8", "acme", "?"} {
					if strings.Contains(l.GetValue(), bad) {
						t.Errorf("ham yol etikete sızdı: %q", l.GetValue())
					}
				}
			case "method":
				if !map[string]bool{"GET": true, "HEAD": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "OPTIONS": true, "OTHER": true}[l.GetValue()] {
					t.Errorf("yöntem etiketi: %q", l.GetValue())
				}
			case "status_class":
				if !map[string]bool{"1xx": true, "2xx": true, "3xx": true, "4xx": true, "5xx": true, "other": true}[l.GetValue()] {
					t.Errorf("durum sınıfı: %q", l.GetValue())
				}
			}
		}
	}
	if !routes["/api/results/:id/artifacts"] || !routes["unmatched"] {
		t.Fatalf("beklenen rotalar yok: %v", routes)
	}
}

// Panikleyen işleyicinin Recovery'nin ürettiği 500'ü de sayılır: New'in
// kullandığı useBaseMiddleware metrik ara katmanını Recovery'den önce kurar.
// METRICS_ENABLED kapalıyken ara katman hiç kurulmaz.
func TestMetrics_HTTPMiddlewareCountsPanics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reg := newMetricsServer(t)
	tok := metricsToken(t, reg, "ops", storage.RoleAdmin, storage.TokenScopeMetrics)
	for _, c := range []struct {
		route   string
		enabled bool
		delta   float64
	}{
		{"/boom/:id", true, 1},
		{"/boom-off/:id", false, 0},
	} {
		r := gin.New()
		useBaseMiddleware(r, c.enabled, false)
		r.GET(c.route, func(*gin.Context) { panic("boom") })
		before := scrapeOK(t, reg, tok)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, strings.Replace(c.route, ":id", "1", 1), nil))
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("%s panik: %d", c.route, w.Code)
		}
		b, _ := sample(before, "keywordhunter_http_requests_total", "route", c.route, "status_class", "5xx")
		a, _ := sample(scrapeOK(t, reg, tok), "keywordhunter_http_requests_total", "route", c.route, "status_class", "5xx")
		if a-b != c.delta {
			t.Fatalf("%s (metrics=%v): panik 500'ü artışı %v, beklenen %v", c.route, c.enabled, a-b, c.delta)
		}
	}
}

func TestProbeSOCKS5(t *testing.T) {
	if !probeSOCKS5(fakeSOCKS5(t), time.Second) {
		t.Fatal("SOCKS5 sunucusu erişilebilir görünmeli")
	}
	// SOCKS konuşmayan dinleyici (ör. yanlış porttaki HTTP sunucusu) erişilemez sayılır.
	hs := httptest.NewServer(http.NotFoundHandler())
	defer hs.Close()
	if probeSOCKS5(strings.TrimPrefix(hs.URL, "http://"), time.Second) {
		t.Fatal("HTTP sunucusu SOCKS5 sayılmamalı")
	}
	if probeSOCKS5("127.0.0.1:1", 200*time.Millisecond) {
		t.Fatal("kapalı port erişilemez olmalı")
	}
}

// Ayarlar sayfası metrik ucunun durumunu (env değişkeniyle) gösterir.
func TestMetrics_SettingsSection(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		var s *Server
		if enabled {
			s = newMetricsServer(t)
		} else {
			s = newTestServer(t)
		}
		sess, csrf := login(t, s)
		w := do(s, http.MethodGet, "/settings", nil, []*http.Cookie{sess, csrf}, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("settings: %d", w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, `id="sec-metrics"`) || !strings.Contains(body, "METRICS_ENABLED") {
			t.Fatal("metrik bölümü yok")
		}
		// Bölüm menüsünden ulaşılabilir (diğer bölümler gibi).
		if !strings.Contains(body, `<a href="#sec-metrics">`) {
			t.Fatal("bölüm menüsünde metrik bağlantısı yok")
		}
		if hasOff := strings.Contains(body, `id="met-off"`); hasOff == enabled {
			t.Fatalf("enabled=%v: kapalı ipucu görünürlüğü yanlış", enabled)
		}
	}
}

// fullyPopulatedFamilies tüm koşullu serilerin (motor, yedek, Tor, denetim
// zinciri) göründüğü bir kazımanın metrik aileleri.
func fullyPopulatedFamilies(t *testing.T) map[string]*dto.MetricFamily {
	t.Helper()
	s := newMetricsServer(t)
	s.torProxy = fakeSOCKS5(t)
	tok := metricsToken(t, s, "ops", storage.RoleAdmin, storage.TokenScopeMetrics)
	if err := s.db.UpsertEngineStat("Ahmia", "http://ahmia", true); err != nil {
		t.Fatal(err)
	}
	_ = s.db.UpdateEngineCheck("Ahmia", "up", 100, 1)
	now := time.Now()
	_ = s.db.RecordMaintenanceRun(storage.MaintenanceRun{Kind: storage.MaintenanceBackup, StartedAt: now, FinishedAt: now, OK: true})
	if _, err := s.auditChain(true); err != nil {
		t.Fatal(err)
	}
	return scrapeOK(t, s, tok)
}

var metricNameRe = regexp.MustCompile(`\b(?:keywordhunter|go|process)_[a-z0-9_]*[a-z0-9]\b`)

// knownMetric ad (histogram için _bucket/_sum/_count son ekleriyle) yayımlanıyor mu.
func knownMetric(fams map[string]*dto.MetricFamily, name string) bool {
	if _, ok := fams[name]; ok {
		return true
	}
	for _, suf := range []string{"_bucket", "_sum", "_count"} {
		if f, ok := fams[strings.TrimSuffix(name, suf)]; ok && strings.HasSuffix(name, suf) && f.GetType() == dto.MetricType_HISTOGRAM {
			return true
		}
	}
	return false
}

// İçe aktarılabilir Grafana panosu geçerli JSON'dur ve sorguları yalnız
// gerçekten yayımlanan metrik adlarını kullanır.
func TestMetrics_GrafanaDashboardUsesRealMetrics(t *testing.T) {
	fams := fullyPopulatedFamilies(t)
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "grafana", "keywordhunter.json"))
	if err != nil {
		t.Fatal(err)
	}
	var dash struct {
		UID           string `json:"uid"`
		Title         string `json:"title"`
		SchemaVersion int    `json:"schemaVersion"`
		Templating    struct {
			List []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"list"`
		} `json:"templating"`
		Panels []struct {
			Type       string `json:"type"`
			Title      string `json:"title"`
			Datasource *struct {
				Type string `json:"type"`
				UID  string `json:"uid"`
			} `json:"datasource"`
			Targets []struct {
				Expr  string `json:"expr"`
				RefID string `json:"refId"`
			} `json:"targets"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(raw, &dash); err != nil {
		t.Fatalf("pano JSON'u geçersiz: %v", err)
	}
	if dash.UID == "" || dash.Title == "" || dash.SchemaVersion < 30 || len(dash.Panels) < 10 {
		t.Fatalf("pano üst bilgisi eksik: uid=%q title=%q schema=%d panel=%d", dash.UID, dash.Title, dash.SchemaVersion, len(dash.Panels))
	}
	vars := map[string]string{}
	for _, v := range dash.Templating.List {
		vars[v.Name] = v.Type
	}
	if vars["datasource"] != "datasource" || vars["instance"] != "query" {
		t.Fatalf("pano değişkenleri: %v", vars)
	}
	used := map[string]bool{}
	queries := 0
	for _, p := range dash.Panels {
		if p.Type == "row" {
			continue
		}
		if p.Datasource == nil || p.Datasource.Type != "prometheus" || p.Datasource.UID != "${datasource}" || len(p.Targets) == 0 {
			t.Errorf("panel %q: veri kaynağı/sorgu eksik", p.Title)
		}
		for _, tg := range p.Targets {
			queries++
			if strings.Count(tg.Expr, "(") != strings.Count(tg.Expr, ")") || strings.Count(tg.Expr, "{") != strings.Count(tg.Expr, "}") {
				t.Errorf("panel %q: dengesiz parantez: %s", p.Title, tg.Expr)
			}
			names := metricNameRe.FindAllString(tg.Expr, -1)
			if len(names) == 0 {
				t.Errorf("panel %q: sorguda metrik yok: %s", p.Title, tg.Expr)
			}
			for _, n := range names {
				used[n] = true
				if !knownMetric(fams, n) {
					t.Errorf("panel %q: bilinmeyen metrik %q", p.Title, n)
				}
			}
		}
	}
	if queries < 30 {
		t.Fatalf("yalnız %d sorgu", queries)
	}
	// Panonun kapsamı: her keywordhunter_ ailesi en az bir panelde.
	for name, f := range fams {
		if !strings.HasPrefix(name, "keywordhunter_") {
			continue
		}
		n := name
		if f.GetType() == dto.MetricType_HISTOGRAM {
			n += "_bucket"
		}
		if !used[n] && !used[name] {
			t.Errorf("%s panoda kullanılmıyor", name)
		}
	}
}

// docs/METRICS.md her yayımlanan keywordhunter_ metriğini listeler ve
// belgede/uyarı kurallarında yayımlanmayan (eskimiş) ad yoktur.
func TestMetrics_DocsListEveryMetric(t *testing.T) {
	fams := fullyPopulatedFamilies(t)
	for _, doc := range []string{
		filepath.Join("..", "..", "docs", "METRICS.md"),
		filepath.Join("..", "..", "docs", "prometheus", "keywordhunter-alerts.yml"),
	} {
		raw, err := os.ReadFile(doc)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		for _, n := range metricNameRe.FindAllString(text, -1) {
			if strings.HasPrefix(n, "keywordhunter_") && !knownMetric(fams, n) {
				t.Errorf("%s: yayımlanmayan metrik adı %q", filepath.Base(doc), n)
			}
		}
		if filepath.Base(doc) != "METRICS.md" {
			continue
		}
		for name := range fams {
			if strings.HasPrefix(name, "keywordhunter_") && !strings.Contains(text, "`"+name+"`") {
				t.Errorf("METRICS.md %s metriğini listelemiyor", name)
			}
		}
	}
}

// globalFamilies süreç geneli olay sayaçlarının anlık görüntüsü.
func globalFamilies(t *testing.T) map[string]*dto.MetricFamily {
	t.Helper()
	reg, err := metrics.NewRegistry("test")
	if err != nil {
		t.Fatal(err)
	}
	list, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]*dto.MetricFamily, len(list))
	for _, f := range list {
		out[f.GetName()] = f
	}
	return out
}

// İki adımlı girişte yöntem ve sonuç doğru sayılır: parola adımı "challenge",
// ikinci adım totp / recovery_code olarak başarı ya da hata.
func TestMetrics_LoginAttemptsTwoFactor(t *testing.T) {
	s := newTwoFAServer(t, config.Require2FANone)
	e := enroll2FA(t, s, loginAs(t, s, "admin", "adminpass1"), "adminpass1")
	before := globalFamilies(t)

	if w := secondStep(s, passwordStep(t, s, "admin", "adminpass1"), "", nextCode(e, 1)); w.Header().Get("Location") != "/dashboard" {
		t.Fatalf("TOTP girişi: %s", w.Header().Get("Location"))
	}
	if w := secondStep(s, passwordStep(t, s, "admin", "adminpass1"), "recovery", "not-a-code"); w.Header().Get("Location") != "/login/2fa?mode=recovery&error=1" {
		t.Fatalf("hatalı kurtarma kodu: %s", w.Header().Get("Location"))
	}
	if w := secondStep(s, passwordStep(t, s, "admin", "adminpass1"), "recovery", e.codes[0]); cookieNamed(w, "session") == nil {
		t.Fatalf("kurtarma koduyla giriş: %s", w.Header().Get("Location"))
	}

	after := globalFamilies(t)
	for _, c := range []struct {
		method, result string
		delta          float64
	}{
		{"password", "challenge", 3},
		{"password", "success", 0}, // 2FA'lı hesapta parola adımı oturum açmaz
		{"totp", "success", 1},
		{"recovery_code", "failure", 1},
		{"recovery_code", "success", 1},
	} {
		b, _ := sample(before, "keywordhunter_login_attempts_total", "method", c.method, "result", c.result)
		a := mustSample(t, after, "keywordhunter_login_attempts_total", "method", c.method, "result", c.result)
		if a-b != c.delta {
			t.Errorf("login_attempts_total{%s,%s} artışı %v, beklenen %v", c.method, c.result, a-b, c.delta)
		}
	}
}

// Kod denetlenmeden reddedilen ikinci adım gönderimleri de sayılır: bekleyen
// giriş yok/süresi dolmuş (failure), deneme hakkı uçuştaki isteklerle
// tükenmiş (locked), 2FA bu arada sıfırlanmış (failure).
func TestMetrics_LoginAttemptsTwoFactorRejections(t *testing.T) {
	s := newTwoFAServer(t, config.Require2FANone)
	e := enroll2FA(t, s, loginAs(t, s, "admin", "adminpass1"), "adminpass1")
	before := globalFamilies(t)

	// 1) Bekleyen giriş çerezi bilinmiyor.
	stale := &http.Cookie{Name: pendingLoginCookie, Value: "no-such-pending-login"}
	if w := secondStep(s, stale, "", nextCode(e, 1)); w.Header().Get("Location") != "/login?message=2fa_expired" {
		t.Fatalf("bilinmeyen bekleyen giriş: %s", w.Header().Get("Location"))
	}
	// 2) Deneme hakkı tükenmiş (eşzamanlı isteklerle ayrılmış).
	pc := passwordStep(t, s, "admin", "adminpass1")
	s.pendingLogins.mu.Lock()
	s.pendingLogins.m[pc.Value].attempts = pendingLoginMaxAttempts
	s.pendingLogins.mu.Unlock()
	if w := secondStep(s, pc, "recovery", e.codes[0]); w.Header().Get("Location") != "/login?error=2fa_locked" {
		t.Fatalf("tükenmiş hak: %s", w.Header().Get("Location"))
	}
	// 3) Parola adımından sonra admin 2FA'yı sıfırladı.
	pc = passwordStep(t, s, "admin", "adminpass1")
	u, err := s.db.GetUserByUsername("admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.DisableTwoFactor(u.ID); err != nil {
		t.Fatal(err)
	}
	if w := secondStep(s, pc, "", nextCode(e, 1)); w.Header().Get("Location") != "/login?message=2fa_expired" {
		t.Fatalf("sıfırlanmış 2FA: %s", w.Header().Get("Location"))
	}

	after := globalFamilies(t)
	for _, c := range []struct {
		method, result string
		delta          float64
	}{
		{"totp", "failure", 2},
		{"recovery_code", "locked", 1},
		{"recovery_code", "failure", 0},
		{"totp", "success", 0},
	} {
		b, _ := sample(before, "keywordhunter_login_attempts_total", "method", c.method, "result", c.result)
		a := mustSample(t, after, "keywordhunter_login_attempts_total", "method", c.method, "result", c.result)
		if a-b != c.delta {
			t.Errorf("login_attempts_total{%s,%s} artışı %v, beklenen %v", c.method, c.result, a-b, c.delta)
		}
	}
}

// Zincir doğrulaması HATA verdiğinde (satırı taranamaz hâle getiren kurcalama)
// audit_chain_ok önceki 1'i korumaz: 0 olur ve verify_error 1; son başarılı tam
// doğrulama zamanı değişmez. İlk doğrulama hata verirse de seri yok değil 0'dır.
func TestMetrics_AuditChainVerifyErrorIsNotOK(t *testing.T) {
	tamper := func(t *testing.T, s *Server) {
		t.Helper()
		conn := s.db.GetDBConn()
		if _, err := conn.Exec(`DROP TRIGGER IF EXISTS audit_events_no_update`); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(`UPDATE audit_events SET success = 'tampered' WHERE id = (SELECT MIN(id) FROM audit_events)`); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("after-ok", func(t *testing.T) {
		s := newMetricsServer(t)
		tok := metricsToken(t, s, "ops", storage.RoleAdmin, storage.TokenScopeMetrics)
		s.auditSystem("test.event", "x", nil, true)
		if st, err := s.auditChain(true); err != nil || !st.OK {
			t.Fatalf("ilk doğrulama: %+v %v", st, err)
		}
		fams := scrapeOK(t, s, tok)
		verifiedAt := mustSample(t, fams, "keywordhunter_audit_chain_last_verify_timestamp_seconds")
		if v := mustSample(t, fams, "keywordhunter_audit_chain_ok"); v != 1 {
			t.Fatalf("sağlam zincir: %v", v)
		}

		tamper(t, s)
		if _, err := s.auditChain(true); err == nil {
			t.Fatal("taranamayan satır doğrulama hatası vermeli (test ön koşulu)")
		}
		fams = scrapeOK(t, s, tok)
		if v := mustSample(t, fams, "keywordhunter_audit_chain_ok"); v != 0 {
			t.Fatalf("doğrulama hatasından sonra audit_chain_ok = %v, beklenen 0", v)
		}
		if v := mustSample(t, fams, "keywordhunter_audit_chain_verify_error"); v != 1 {
			t.Fatalf("audit_chain_verify_error = %v, beklenen 1", v)
		}
		if v := mustSample(t, fams, "keywordhunter_audit_chain_last_verify_timestamp_seconds"); v != verifiedAt {
			t.Fatalf("son başarılı doğrulama zamanı değişmemeli: %v → %v", verifiedAt, v)
		}
	})

	t.Run("first-verify-fails", func(t *testing.T) {
		s := newMetricsServer(t)
		tok := metricsToken(t, s, "ops", storage.RoleAdmin, storage.TokenScopeMetrics)
		s.auditSystem("test.event", "x", nil, true)
		tamper(t, s)
		s.runAuditMaintenance() // açılış yolu: hata yalnız günlüğe yazılır
		fams := scrapeOK(t, s, tok)
		if v := mustSample(t, fams, "keywordhunter_audit_chain_ok"); v != 0 {
			t.Fatalf("audit_chain_ok = %v, beklenen 0", v)
		}
		if v := mustSample(t, fams, "keywordhunter_audit_chain_verify_error"); v != 1 {
			t.Fatalf("audit_chain_verify_error = %v, beklenen 1", v)
		}
		if _, ok := sample(fams, "keywordhunter_audit_chain_last_verify_timestamp_seconds"); ok {
			t.Fatal("hiç başarılı tam doğrulama yokken zaman damgası yayımlanmamalı")
		}
	})
}
