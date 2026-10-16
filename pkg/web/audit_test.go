package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/config"
	"keywordhunter-mvp/pkg/i18n"
	"keywordhunter-mvp/pkg/storage"
)

// auditEvents tüm denetim olaylarını (en yeni önce) döndürür.
func auditEvents(t *testing.T, s *Server) []storage.AuditEvent {
	t.Helper()
	list, _, err := s.db.ListAuditEvents(storage.AuditFilter{Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	return list
}

// lastAudit verilen eylemin en yeni olayını döndürür (yoksa testi düşürür).
func lastAudit(t *testing.T, s *Server, action string) storage.AuditEvent {
	t.Helper()
	for _, e := range auditEvents(t, s) {
		if e.Action == action {
			return e
		}
	}
	t.Fatalf("%s olayı kaydedilmedi", action)
	return storage.AuditEvent{}
}

func countAudit(t *testing.T, s *Server, action string) int {
	t.Helper()
	n := 0
	for _, e := range auditEvents(t, s) {
		if e.Action == action {
			n++
		}
	}
	return n
}

// assertNoSecretsInAudit hiçbir denetim satırının (tüm alanlarıyla) verilen
// sırlardan birini içermediğini doğrular.
func assertNoSecretsInAudit(t *testing.T, s *Server, secrets ...string) {
	t.Helper()
	for _, e := range auditEvents(t, s) {
		b, _ := json.Marshal(e)
		for _, sec := range secrets {
			if strings.Contains(string(b), sec) {
				t.Errorf("denetim kaydında sır sızdı (%s #%d): %q", e.Action, e.ID, sec)
			}
		}
	}
}

func TestAudit_LoginSuccessFailureLogout(t *testing.T) {
	s := newTestServer(t)
	const wrong = "Wr0ng-Pa55-Value!"

	w := do(s, http.MethodPost, "/login", url.Values{"username": {"admin"}, "password": {wrong}}, nil, nil)
	if !strings.Contains(w.Header().Get("Location"), "error=1") {
		t.Fatalf("yanlış parola reddedilmeli: %s", w.Header().Get("Location"))
	}
	f := lastAudit(t, s, "auth.login.failure")
	if f.Success || f.Actor != "" || f.AuthMethod != storage.AuthMethodAnonymous || f.Target != "user:admin" || f.IP == "" {
		t.Fatalf("başarısız giriş olayı: %+v", f)
	}
	if !strings.Contains(f.Details, "bad_credentials") {
		t.Fatalf("neden kaydedilmeli: %s", f.Details)
	}

	// Kullanıcı adı alanına uzun metin: kısaltılarak kaydedilir
	long := strings.Repeat("x", 100)
	do(s, http.MethodPost, "/login", url.Values{"username": {long}, "password": {"p"}}, nil, nil)
	if f := lastAudit(t, s, "auth.login.failure"); len(f.Target) > len("user:")+64 {
		t.Fatalf("denenen kullanıcı adı kısaltılmalı: %d", len(f.Target))
	}

	session, csrf := login(t, s)
	ok := lastAudit(t, s, "auth.login.success")
	if !ok.Success || ok.Actor != "admin" || ok.Role != storage.RoleAdmin || ok.AuthMethod != storage.AuthMethodSession {
		t.Fatalf("başarılı giriş olayı: %+v", ok)
	}

	w = do(s, http.MethodPost, "/logout", url.Values{"_csrf": {csrf.Value}}, []*http.Cookie{session}, nil)
	if w.Header().Get("Location") != "/login" {
		t.Fatalf("logout: %d %s", w.Code, w.Header().Get("Location"))
	}
	if lo := lastAudit(t, s, "auth.logout"); lo.Actor != "admin" || !lo.Success {
		t.Fatalf("çıkış olayı: %+v", lo)
	}
	// Oturumsuz çıkış olay üretmez (gürültü yok)
	do(s, http.MethodPost, "/logout", nil, nil, nil)
	if n := countAudit(t, s, "auth.logout"); n != 1 {
		t.Fatalf("oturumsuz çıkış kaydedilmemeli: %d", n)
	}

	assertNoSecretsInAudit(t, s, wrong, "correct-horse", session.Value, csrf.Value)
	if ok, broken, err := s.db.VerifyAuditChain(); err != nil || !ok {
		t.Fatalf("zincir geçerli olmalı: broken=%d err=%v", broken, err)
	}
}

func TestAudit_LockedLoginIsThrottled(t *testing.T) {
	s := newTestServer(t)
	for i := 0; i < loginMaxFailures; i++ {
		do(s, http.MethodPost, "/login", url.Values{"username": {"admin"}, "password": {"bad"}}, nil, nil)
	}
	for i := 0; i < 5; i++ {
		do(s, http.MethodPost, "/login", url.Values{"username": {"admin"}, "password": {"correct-horse"}}, nil, nil)
	}
	if n := countAudit(t, s, "auth.login.failure"); n != loginMaxFailures {
		t.Fatalf("%d başarısız giriş bekleniyordu: %d", loginMaxFailures, n)
	}
	if n := countAudit(t, s, "auth.login.locked"); n != 1 {
		t.Fatalf("kilitli denemeler IP başına seyreltilmeli (1 olay): %d", n)
	}
	if e := lastAudit(t, s, "auth.login.locked"); e.Success || !strings.Contains(e.Details, "retryAfterSec") {
		t.Fatalf("kilit olayı: %+v", e)
	}
	assertNoSecretsInAudit(t, s, "correct-horse")
}

func TestAudit_UserManagementEvents(t *testing.T) {
	s := newTestServer(t)
	admin := loginAs(t, s, "admin", "correct-horse")
	csrf := csrfOf(admin)
	const pass1, pass2 = "Fresh-User-Pass-1", "Reset-User-Pass-2"

	if w := doJSON(s, http.MethodPost, "/api/users", `{"username":"ayse","password":"`+pass1+`","role":"analyst"}`, admin, csrf); w.Code != http.StatusOK {
		t.Fatalf("kullanıcı oluşturma: %d %s", w.Code, w.Body.String())
	}
	e := lastAudit(t, s, "user.create")
	if !e.Success || e.Actor != "admin" || e.Target != "user:ayse" || !strings.Contains(e.Details, `"role":"analyst"`) {
		t.Fatalf("user.create: %+v", e)
	}
	// Mükerrer kullanıcı: başarısız olay
	doJSON(s, http.MethodPost, "/api/users", `{"username":"ayse","password":"`+pass1+`","role":"viewer"}`, admin, csrf)
	if e := lastAudit(t, s, "user.create"); e.Success || !strings.Contains(e.Details, "exists") {
		t.Fatalf("mükerrer kullanıcı başarısız kaydedilmeli: %+v", e)
	}

	id := itoa(mustUserID(t, s, "ayse"))
	if w := doJSON(s, http.MethodPost, "/api/users/"+id, `{"password":"`+pass2+`"}`, admin, csrf); w.Code != http.StatusOK {
		t.Fatalf("parola sıfırlama: %d", w.Code)
	}
	if e := lastAudit(t, s, "user.password.reset"); !e.Success || e.Target != "user:ayse" {
		t.Fatalf("user.password.reset: %+v", e)
	}
	if w := doJSON(s, http.MethodPost, "/api/users/"+id, `{"role":"viewer","enabled":false}`, admin, csrf); w.Code != http.StatusOK {
		t.Fatalf("güncelleme: %d", w.Code)
	}
	up := lastAudit(t, s, "user.update")
	for _, want := range []string{`"role":"viewer"`, `"prevRole":"analyst"`, `"enabled":false`} {
		if !strings.Contains(up.Details, want) {
			t.Fatalf("user.update ayrıntısı %s içermeli: %s", want, up.Details)
		}
	}
	if w := doJSON(s, http.MethodPost, "/api/users/"+id+"/delete", `{}`, admin, csrf); w.Code != http.StatusOK {
		t.Fatalf("silme: %d", w.Code)
	}
	if e := lastAudit(t, s, "user.delete"); !e.Success || e.Target != "user:ayse" {
		t.Fatalf("user.delete: %+v", e)
	}
	assertNoSecretsInAudit(t, s, pass1, pass2, "$2a$", "$2b$")
}

func TestAudit_SelfPasswordChange(t *testing.T) {
	s := newTestServer(t)
	ck := mkUser(t, s, "self1", "Self-Old-Pass-1", storage.RoleViewer)
	if code := jsonPost(s, "/api/me/password", `{"current":"wrong-current-x","new":"Self-New-Pass-2"}`, ck); code != http.StatusForbidden {
		t.Fatalf("yanlış mevcut parola: %d", code)
	}
	if e := lastAudit(t, s, "me.password.change"); e.Success || e.Actor != "self1" || e.Role != storage.RoleViewer {
		t.Fatalf("başarısız öz-parola olayı: %+v", e)
	}
	if code := jsonPost(s, "/api/me/password", `{"current":"Self-Old-Pass-1","new":"Self-New-Pass-2"}`, ck); code != http.StatusOK {
		t.Fatalf("öz-parola: %d", code)
	}
	if e := lastAudit(t, s, "me.password.change"); !e.Success {
		t.Fatalf("başarılı öz-parola olayı: %+v", e)
	}
	assertNoSecretsInAudit(t, s, "Self-Old-Pass-1", "Self-New-Pass-2", "wrong-current-x")
}

func TestAudit_ResultsDeleteAndCase(t *testing.T) {
	s := newTestServer(t)
	if _, err := s.db.SaveResults([]storage.SearchResult{
		{Title: "a", URL: "http://a.onion/1", Source: "Torch", Query: "q"},
		{Title: "b", URL: "http://a.onion/2", Source: "Torch", Query: "q"},
	}); err != nil {
		t.Fatal(err)
	}
	rs, _, _ := s.db.GetResultsFiltered(storage.ResultFilter{Limit: 10})
	if len(rs) != 2 {
		t.Fatalf("2 sonuç bekleniyordu: %d", len(rs))
	}
	admin := loginAs(t, s, "admin", "correct-horse")
	if code := jsonPost(s, "/api/results/"+itoa(rs[0].ID)+"/case", `{"status":"investigating","note":"gizli analist notu"}`, admin); code != http.StatusOK {
		t.Fatalf("vaka: %d", code)
	}
	ce := lastAudit(t, s, "result.case.update")
	if ce.Target != "result:"+itoa(rs[0].ID) || !strings.Contains(ce.Details, "investigating") || strings.Contains(ce.Details, "gizli analist") {
		t.Fatalf("result.case.update (not metni kaydedilmemeli): %+v", ce)
	}
	body := `{"ids":[` + itoa(rs[0].ID) + `,` + itoa(rs[1].ID) + `]}`
	if code := jsonPost(s, "/api/results/delete", body, admin); code != http.StatusOK {
		t.Fatalf("silme: %d", code)
	}
	de := lastAudit(t, s, "results.delete")
	if !de.Success || !strings.Contains(de.Details, `"deleted":2`) || !strings.Contains(de.Details, `"requested":2`) {
		t.Fatalf("results.delete: %+v", de)
	}
}

func TestAudit_SettingsUpdateRecordsKeysNotValues(t *testing.T) {
	s := newTestServer(t)
	s.envStore = config.NewEnvStore(filepath.Join(t.TempDir(), ".env"))
	_ = s.envStore.Update(map[string]string{"ADMIN_USER": "admin", "TOR_PROXY": "127.0.0.1:9150", "DB_PATH": "kh.db",
		"WEB_ADDR": ":8080", "LOG_DIR": "logs", "WEB_SECURE_COOKIES": "false", "SESSION_TTL_HOURS": "24",
		"RATE_LIMIT_RPS": "25.00", "RATE_LIMIT_BURST": "80"})
	admin := loginAs(t, s, "admin", "correct-horse")
	const newPass = "Brand-New-Admin-Pass-9"
	body := `{"adminUser":"admin","adminPass":"` + newPass + `","torProxy":"127.0.0.1:9050","dbPath":"kh.db","webAddr":":8080",` +
		`"logDir":"logs","secureCookies":false,"sessionTtlHours":24,"rateLimitRps":30,"rateLimitBurst":80}`
	if w := doJSON(s, http.MethodPost, "/api/settings/env", body, admin, csrfOf(admin)); w.Code != http.StatusOK {
		t.Fatalf("ayar: %d %s", w.Code, w.Body.String())
	}
	e := lastAudit(t, s, "settings.env.update")
	var d struct {
		Changed []string `json:"changed"`
	}
	if err := json.Unmarshal([]byte(e.Details), &d); err != nil {
		t.Fatalf("ayrıntı JSON: %v %s", err, e.Details)
	}
	want := map[string]bool{"TOR_PROXY": true, "RATE_LIMIT_RPS": true, "ADMIN_PASS": true}
	if len(d.Changed) != len(want) {
		t.Fatalf("yalnız değişen anahtarlar beklenirdi: %v", d.Changed)
	}
	for _, k := range d.Changed {
		if !want[k] {
			t.Fatalf("beklenmeyen anahtar: %s (%v)", k, d.Changed)
		}
	}
	stored, _ := s.envStore.Read()
	assertNoSecretsInAudit(t, s, newPass, stored["ADMIN_PASS_HASH"], "127.0.0.1:9050", "30.00")
}

func TestAudit_SecretsRedactedForWebhookAndCookies(t *testing.T) {
	s := newTestServer(t)
	admin := loginAs(t, s, "admin", "correct-horse")
	if code := jsonPost(s, "/api/alert-config", `{"webhookUrl":"`+testHookSecret+`","minCriticality":4,"enabled":true}`, admin); code != http.StatusOK {
		t.Fatalf("alert-config: %d", code)
	}
	e := lastAudit(t, s, "alert.config.update")
	if !strings.Contains(e.Details, "hooks.slack.com") || !strings.Contains(e.Details, `"minCriticality":4`) {
		t.Fatalf("alert.config.update yalnız ana makineyi taşımalı: %s", e.Details)
	}
	// Geçersiz (iç ağ) hedefe test: başarısız ve gizlenmiş
	if code := jsonPost(s, "/api/alert-config/test", `{"webhookUrl":"http://127.0.0.1/hook/PrivatePathToken42"}`, admin); code != http.StatusBadRequest {
		t.Fatalf("iç ağ test hedefi reddedilmeli: %d", code)
	}
	if e := lastAudit(t, s, "alert.test"); e.Success || !strings.HasPrefix(e.Target, "http://127.0.0.1") {
		t.Fatalf("alert.test: %+v", e)
	}
	if code := jsonPost(s, "/api/site-profiles", `{"host":"abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefab.onion","cookies":"sid=TopSecretCookieVal; cf=xyz","userAgent":"UA-SECRET-1","renderJs":true}`, admin); code != http.StatusOK {
		t.Fatalf("site profili: %d", code)
	}
	sp := lastAudit(t, s, "site_profile.save")
	if !strings.HasSuffix(sp.Target, ".onion") || !strings.Contains(sp.Details, `"renderJs":true`) {
		t.Fatalf("site_profile.save: %+v", sp)
	}
	assertNoSecretsInAudit(t, s, "SuperSecretToken123", "T0AAA", "PrivatePathToken42", "TopSecretCookieVal", "UA-SECRET-1")
}

func TestAudit_AccessControl(t *testing.T) {
	s := newTestServer(t)
	admin := loginAs(t, s, "admin", "correct-horse")
	analyst := mkUser(t, s, "an1", "Analyst-Pass-1", storage.RoleAnalyst)
	viewer := mkUser(t, s, "vw1", "Viewer-Pass-1", storage.RoleViewer)

	for name, ck := range map[string][]*http.Cookie{"analyst": analyst, "viewer": viewer} {
		if w := do(s, http.MethodGet, "/api/audit", nil, ck, nil); w.Code != http.StatusForbidden {
			t.Fatalf("%s /api/audit: %d (403 bekleniyordu)", name, w.Code)
		}
		if w := do(s, http.MethodGet, "/api/audit/export?format=csv", nil, ck, nil); w.Code != http.StatusForbidden {
			t.Fatalf("%s export: %d", name, w.Code)
		}
		w := do(s, http.MethodGet, "/audit", nil, ck, nil)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "<html") {
			t.Fatalf("%s /audit: %d (markalı 403 bekleniyordu)", name, w.Code)
		}
		if strings.Contains(w.Body.String(), `href="/audit"`) {
			t.Fatalf("%s menüsünde denetim bağlantısı olmamalı", name)
		}
	}
	// Reddedilen erişimler de kayıtlıdır: kim neyi denedi
	den := lastAudit(t, s, "access.denied")
	if den.Success || den.Actor == "" || !strings.HasPrefix(den.Target, "/") || !strings.Contains(den.Details, `"reason":"role"`) {
		t.Fatalf("access.denied: %+v", den)
	}
	// Viewer yazma denemesi de kaydedilir
	jsonPost(s, "/api/results/delete", `{"ids":[1]}`, viewer)
	if den := lastAudit(t, s, "access.denied"); den.Actor != "vw1" || den.Target != "/api/results/delete" {
		t.Fatalf("viewer yazma reddi: %+v", den)
	}
	// CSRF'siz yazma: reddedilir ve nedeni csrf
	doJSON(s, http.MethodPost, "/api/results/delete", `{"ids":[1]}`, admin, "")
	if den := lastAudit(t, s, "access.denied"); !strings.Contains(den.Details, `"reason":"csrf"`) {
		t.Fatalf("csrf reddi: %+v", den)
	}

	// Admin: sayfa + API
	w := do(s, http.MethodGet, "/audit", nil, admin, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "/static/js/pages/audit.js") || !strings.Contains(w.Body.String(), `id="a-chain"`) {
		t.Fatalf("admin /audit: %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `href="/audit"`) {
		t.Fatal("admin menüsünde denetim bağlantısı olmalı")
	}
	w = do(s, http.MethodGet, "/api/audit?category=auth&result=failure&limit=5", nil, admin, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("admin /api/audit: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Events []struct {
			Action, Category string
			Success          bool
		} `json:"events"`
		Total int                      `json:"total"`
		Chain storage.AuditChainStatus `json:"chain"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total == 0 || len(resp.Events) == 0 || !resp.Chain.OK {
		t.Fatalf("API yanıtı: total=%d chain=%+v", resp.Total, resp.Chain)
	}
	for _, e := range resp.Events {
		if e.Category != "auth" || e.Success {
			t.Fatalf("filtre dışı olay: %+v", e)
		}
	}
	if w := do(s, http.MethodGet, "/api/audit?category=bogus", nil, admin, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("geçersiz kategori 400 olmalı: %d", w.Code)
	}
	if w := do(s, http.MethodGet, "/api/audit?from=not-a-date", nil, admin, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("geçersiz tarih 400 olmalı: %d", w.Code)
	}
}

func TestAudit_ExportCSVIsSafeAndAudited(t *testing.T) {
	s := newTestServer(t)
	admin := loginAs(t, s, "admin", "correct-horse")
	if _, err := s.db.AppendAuditEvent(storage.AuditEvent{Action: "search.run", Actor: "admin", AuthMethod: storage.AuthMethodSession,
		Target: `=HYPERLINK("http://evil","x")`, Success: true}); err != nil {
		t.Fatal(err)
	}
	w := do(s, http.MethodGet, "/api/audit/export?format=csv", nil, admin, nil)
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("csv: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	body := w.Body.String()
	if !strings.Contains(body, "id,ts,actor,role,auth_method,ip,action,category,target,details,success,prev_hash,hash") {
		t.Fatal("CSV başlığı eksik")
	}
	if strings.Contains(body, `,"=HYPERLINK`) || !strings.Contains(body, `'=HYPERLINK`) {
		t.Fatal("formül enjeksiyonu nötrlenmeli")
	}
	if e := lastAudit(t, s, "export.audit"); e.Actor != "admin" || !strings.Contains(e.Details, `"format":"csv"`) {
		t.Fatalf("dışa aktarma denetlenmeli: %+v", e)
	}
	// JSON: birebir değerler, kronolojik
	w = do(s, http.MethodGet, "/api/audit/export?format=json&category=data", nil, admin, nil)
	var rows []struct {
		ID     int64  `json:"id"`
		Target string `json:"target"`
		Hash   string `json:"hash"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil || len(rows) != 1 || rows[0].Target != `=HYPERLINK("http://evil","x")` || rows[0].Hash == "" {
		t.Fatalf("json export: %v %s", err, w.Body.String())
	}
	if w := do(s, http.MethodGet, "/api/audit/export?format=xml", nil, admin, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("bilinmeyen format 400 olmalı: %d", w.Code)
	}
	// Doğrulama ucu: POST + CSRF; sonucu döner ve kendisi de kaydedilir
	if code := jsonPost(s, "/api/audit/verify", `{}`, admin); code != http.StatusOK {
		t.Fatalf("verify: %d", code)
	}
	if e := lastAudit(t, s, "audit.verify"); !e.Success {
		t.Fatalf("audit.verify: %+v", e)
	}
}

// Kayıt yazılamasa bile istek başarısız olmamalı (geçersiz eylem, nil DB, panik yok).
func TestAudit_NeverFailsRequest(t *testing.T) {
	s := newTestServer(t)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	s.audit(c, "GEÇERSİZ EYLEM", "x", nil, true) // reddedilir, loglanır
	s.audit(c, "x.y", "t", map[string]any{"bad": make(chan int)}, true)
	(&Server{}).audit(c, "x.y", "t", nil, true)
	var nilSrv *Server
	nilSrv.audit(c, "x.y", "t", nil, true)
	if n := len(auditEvents(t, s)); n != 1 {
		t.Fatalf("yalnız geçerli olay yazılmalı: %d", n)
	}
}

func TestSanitizeAuditDetails(t *testing.T) {
	in := map[string]any{
		"password": "p@ss", "newPassword": "x", "webhookUrl": "https://h/x/y", "Cookie": "a=b",
		"apiToken": "t", "passwordChanged": true, "count": 3, "name": "ok\r\nINJ",
		"nested": map[string]any{"secret": "s", "keep": "v", "deep": map[string]any{"deeper": map[string]any{"x": 1}}},
		"list":   []string{"A", "B"}, "long": strings.Repeat("ş", 400),
	}
	out := sanitizeAuditDetails(in, 0)
	for _, k := range []string{"password", "newPassword", "webhookUrl", "Cookie", "apiToken"} {
		if out[k] != auditRedacted {
			t.Errorf("%s gizlenmeli: %v", k, out[k])
		}
	}
	if out["passwordChanged"] != true || out["count"] != 3 {
		t.Errorf("bool/sayı korunmalı: %v %v", out["passwordChanged"], out["count"])
	}
	if strings.ContainsAny(out["name"].(string), "\r\n") {
		t.Error("kontrol karakterleri atılmalı")
	}
	nested := out["nested"].(map[string]any)
	if nested["secret"] != auditRedacted || nested["keep"] != "v" {
		t.Errorf("iç içe gizleme: %v", nested)
	}
	if deep := nested["deep"].(map[string]any); deep["deeper"] != "…" {
		t.Errorf("derinlik sınırı: %v", deep)
	}
	if n := len([]rune(out["long"].(string))); n > 257 {
		t.Errorf("uzun metin kısaltılmalı: %d", n)
	}
}

// Kaynaktaki her eylem kodu: geçerli biçimde, bir kategoriye açıkça bağlı ve
// TR + EN etiketli olmalı (yeni bir olay eklenip etiketlenmezse test yakalar).
func TestAudit_ActionCodesAreCategorizedAndTranslated(t *testing.T) {
	re := regexp.MustCompile(`(?:s\.audit\(c, |auditSystem\(|Action: )"([a-z0-9_.]+)"\s*,`)
	files, _ := filepath.Glob("*.go")
	codes := map[string]bool{"export.csv": true, "export.json": true} // "export."+format ile dinamik
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			codes[m[1]] = true
		}
	}
	if len(codes) < 40 {
		t.Fatalf("beklenenden az eylem kodu bulundu: %d", len(codes))
	}
	for code := range codes {
		explicit := false
		for _, c := range auditCategories {
			for _, p := range c.Prefixes {
				if strings.HasPrefix(code, p) {
					explicit = true
				}
			}
		}
		if !explicit {
			t.Errorf("%s hiçbir kategoriye bağlı değil", code)
		}
		key := "audit.act." + code
		if tr, en := i18n.T("tr", key), i18n.T("en", key); tr == key || en == tr {
			t.Errorf("%s için TR/EN etiket eksik", code)
		}
	}
}

// Aşırı uzun kullanıcı adı/parola da başarısız denemedir: kilide sayılır ve
// kilitliyken yeni denetim satırı üretmez (tablo şişirme yok).
func TestAudit_OversizedLoginCountsTowardLockout(t *testing.T) {
	s := newTestServer(t)
	long := strings.Repeat("A", 200)
	for i := 0; i < 50; i++ {
		do(s, http.MethodPost, "/login", url.Values{"username": {long}, "password": {"p"}}, nil, nil)
	}
	if n := countAudit(t, s, "auth.login.failure"); n != loginMaxFailures {
		t.Fatalf("aşırı uzun denemeler kilitten önce en fazla %d satır yazmalı: %d", loginMaxFailures, n)
	}
	if n := countAudit(t, s, "auth.login.locked"); n != 1 {
		t.Fatalf("kilitliyken tek (seyreltilmiş) kilit olayı beklenirdi: %d", n)
	}
	// IP gerçekten kilitli: doğru parola da reddedilir
	w := do(s, http.MethodPost, "/login", url.Values{"username": {"admin"}, "password": {"correct-horse"}}, nil, nil)
	if !strings.Contains(w.Header().Get("Location"), "error=locked") {
		t.Fatalf("aşırı uzun denemeler IP'yi kilitlemeli: %s", w.Header().Get("Location"))
	}
}

func TestAuditURL(t *testing.T) {
	cases := map[string]string{
		"": "",
		"http://analyst:Hunter2Secret@abc.onion/login?session=SESSIONTOKEN123&x=1#frag": "http://abc.onion/login?session=…&x=…",
		"https://example.org/path":            "https://example.org/path",
		"http://ops:W4tchPw@abc.onion/?token": "http://abc.onion/?token=…",
		"not a url user:pw@host?token=abc":    "host",
		"ht!tp://u:p@h/x?y=z":                 "ht!tp://h/x",
	}
	for in, want := range cases {
		if got := auditURL(in); got != want {
			t.Errorf("auditURL(%q) = %q, beklenen %q", in, got, want)
		}
	}
}

// URL değerli hedeflerde kullanıcı bilgisi ve sorgu değerleri kayda girmez.
func TestAudit_URLTargetsRedacted(t *testing.T) {
	s := newTestServer(t)
	admin := loginAs(t, s, "admin", "correct-horse")
	onion := strings.Repeat("a", 56) + ".onion"
	body := `{"name":"site","url":"http://ops:W4tchPw@` + onion + `/?token=ABCSECRET"}`
	if code := jsonPost(s, "/api/watchlist", body, admin); code != http.StatusOK {
		t.Fatalf("watchlist ekleme: %d", code)
	}
	e := lastAudit(t, s, "watchlist.add")
	if e.Target != "http://"+onion+"/?token=…" {
		t.Fatalf("watchlist.add hedefi arındırılmalı: %q", e.Target)
	}
	// Saklanan URL'den üretilen hedefler (aç/kapa, silme) de arındırılır
	wid := itoa(lastAuditID(t, e))
	if code := jsonPost(s, "/api/watchlist/"+wid+"/toggle", `{}`, admin); code != http.StatusOK {
		t.Fatalf("watchlist toggle: %d", code)
	}
	if code := jsonPost(s, "/api/watchlist/"+wid+"/delete", `{}`, admin); code != http.StatusOK {
		t.Fatalf("watchlist delete: %d", code)
	}
	if d := lastAudit(t, s, "watchlist.delete"); d.Target != e.Target {
		t.Fatalf("watchlist.delete hedefi arındırılmalı: %q", d.Target)
	}
	assertNoSecretsInAudit(t, s, "W4tchPw", "ABCSECRET")
}

// lastAuditID olay ayrıntısındaki watchlistId'yi döndürür.
func lastAuditID(t *testing.T, e storage.AuditEvent) int64 {
	t.Helper()
	var d struct {
		ID int64 `json:"watchlistId"`
	}
	if err := json.Unmarshal([]byte(e.Details), &d); err != nil || d.ID == 0 {
		t.Fatalf("watchlistId yok: %s", e.Details)
	}
	return d.ID
}

// Başka siteden tetiklenen dışa aktarma: dosya üretilmez, olay yazılmaz.
func TestAudit_CrossSiteExportRefused(t *testing.T) {
	s := newTestServer(t)
	admin := loginAs(t, s, "admin", "correct-horse")
	xs := map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate"}
	for _, path := range []string{
		"/api/audit/export?format=json&q=admin%20deleted%20evidence",
		"/api/export/results?format=csv&q=attacker",
		"/api/export/stix?q=attacker",
	} {
		w := do(s, http.MethodGet, path, nil, admin, xs)
		if w.Code != http.StatusSeeOther || w.Header().Get("Content-Disposition") != "" {
			t.Fatalf("%s siteler-arası reddedilmeli: %d %q", path, w.Code, w.Header().Get("Content-Disposition"))
		}
	}
	for _, a := range []string{"export.audit", "export.csv", "export.json", "export.stix"} {
		if n := countAudit(t, s, a); n != 0 {
			t.Fatalf("siteler-arası istek %s olayı üretmemeli: %d", a, n)
		}
	}
	// Aynı köken (ya da başlıksız) dışa aktarma çalışır
	w := do(s, http.MethodGet, "/api/audit/export?format=json", nil, admin, map[string]string{"Sec-Fetch-Site": "same-origin"})
	if w.Code != http.StatusOK || countAudit(t, s, "export.audit") != 1 {
		t.Fatalf("aynı köken dışa aktarma: %d", w.Code)
	}
}

// JSON dışa aktarmadaki ts, hash'in kapsadığı birebir metindir (9 hane kesir).
func TestAudit_JSONExportTimestampExact(t *testing.T) {
	s := newTestServer(t)
	admin := loginAs(t, s, "admin", "correct-horse")
	w := do(s, http.MethodGet, "/api/audit/export?format=json", nil, admin, nil)
	var rows []struct {
		ID int64  `json:"id"`
		TS string `json:"ts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil || len(rows) == 0 {
		t.Fatalf("json export: %v %s", err, w.Body.String())
	}
	byID := map[int64]string{}
	for _, e := range auditEvents(t, s) {
		byID[e.ID] = e.TS
	}
	re := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{9}Z$`)
	for _, r := range rows {
		if !re.MatchString(r.TS) || r.TS != byID[r.ID] {
			t.Fatalf("#%d ts birebir olmalı: %q (db %q)", r.ID, r.TS, byID[r.ID])
		}
	}
}

// Son sayfanın ötesi istenirse son sayfaya sabitlenir (boş "henüz olay yok" yerine).
func TestAudit_ListClampsPastLastPage(t *testing.T) {
	s := newTestServer(t)
	admin := loginAs(t, s, "admin", "correct-horse")
	for i := 0; i < 7; i++ {
		if _, err := s.db.AppendAuditEvent(storage.AuditEvent{Action: "search.run", Actor: "admin", AuthMethod: storage.AuthMethodSession, Success: true}); err != nil {
			t.Fatal(err)
		}
	}
	w := do(s, http.MethodGet, "/api/audit?page=999&limit=5", nil, admin, nil)
	var resp struct {
		Events []json.RawMessage `json:"events"`
		Page   int               `json:"page"`
		Pages  int               `json:"pages"`
		Total  int               `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total < 8 || resp.Page != resp.Pages || len(resp.Events) == 0 {
		t.Fatalf("son sayfaya sabitlenmeli: page=%d pages=%d events=%d total=%d", resp.Page, resp.Pages, len(resp.Events), resp.Total)
	}
}

// Rol uygulandıktan sonra pasifleştirme başarısız olursa uygulanan değişiklik
// başarısız bir user.update olayıyla yine kayda geçer.
func TestAudit_PartialUserUpdateFailureRecorded(t *testing.T) {
	s := newTestServer(t)
	admin := loginAs(t, s, "admin", "correct-horse")
	mkUser(t, s, "pu1", "Partial-Pass-1", storage.RoleAnalyst)
	if _, err := s.db.GetDBConn().Exec(`CREATE TRIGGER t_fail_enabled BEFORE UPDATE OF enabled ON users BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	id := itoa(mustUserID(t, s, "pu1"))
	if w := doJSON(s, http.MethodPost, "/api/users/"+id, `{"role":"viewer","enabled":false}`, admin, csrfOf(admin)); w.Code != http.StatusInternalServerError {
		t.Fatalf("pasifleştirme hatası 500 olmalı: %d", w.Code)
	}
	e := lastAudit(t, s, "user.update")
	if e.Success || !strings.Contains(e.Details, `"role":"viewer"`) || !strings.Contains(e.Details, `"error":"internal"`) {
		t.Fatalf("kısmi güncelleme başarısız olay olarak kaydedilmeli: %+v", e)
	}
}

// Zincir dışı satır (başın ötesi) hem zinciri kırar hem de sonraki yazmaları
// düşürür: ikisi de /audit'te görünür, yalnız loglanmaz.
func TestAudit_WriteFailuresAndExtraRowsVisible(t *testing.T) {
	s := newTestServer(t)
	admin := loginAs(t, s, "admin", "correct-horse")
	conn := s.db.GetDBConn()
	var head int64
	if err := conn.QueryRow(`SELECT head_id FROM audit_chain_state WHERE id = 1`).Scan(&head); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`DROP TRIGGER audit_events_append_only`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO audit_events (id, ts, action, details, prev_hash, hash) VALUES (?, '2026-01-01T00:00:00.000000000Z', 'results.delete', '{"deleted":9999}', 'x', 'y')`, head+1); err != nil {
		t.Fatal(err)
	}
	w := do(s, http.MethodGet, "/api/audit", nil, admin, nil)
	var resp struct {
		Chain         storage.AuditChainStatus `json:"chain"`
		WriteFailures struct {
			Count  int64  `json:"count"`
			LastAt string `json:"lastAt"`
		} `json:"writeFailures"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Chain.OK || resp.Chain.Reason != "extra" || resp.Chain.BrokenAt != head+1 {
		t.Fatalf("başın ötesindeki satır zinciri kırmalı: %+v", resp.Chain)
	}
	// Bu istekten önceki yazma (ör. erişim) çakışır → sayaç artar
	jsonPost(s, "/api/audit/verify", `{}`, admin)
	w = do(s, http.MethodGet, "/api/audit", nil, admin, nil)
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.WriteFailures.Count == 0 || resp.WriteFailures.LastAt == "" {
		t.Fatalf("yazma hatası sayacı görünmeli: %+v", resp.WriteFailures)
	}
	page := do(s, http.MethodGet, "/audit", nil, admin, nil).Body.String()
	if !strings.Contains(page, `id="a-wfail"`) || strings.Contains(page, `id="a-wfail" role="alert" hidden`) {
		t.Fatal("yazma hatası uyarısı sayfada görünür olmalı")
	}
	if !strings.Contains(page, i18n.T("tr", "audit.chainBroken")+" #") {
		t.Fatal("kırık zincir kartı 'Kırık #id' göstermeli")
	}
}
