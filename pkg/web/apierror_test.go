package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/i18n"
	"keywordhunter-mvp/pkg/scraper"
	"keywordhunter-mvp/pkg/shared"
	"keywordhunter-mvp/pkg/storage"
	"keywordhunter-mvp/pkg/tagging"
)

// apiErrCodeRefs kaynak kodda hata koduna dönüşen dize sabitlerini yakalar.
// Yeni bir çağrı biçimi eklenirse buraya da eklenmeli; aksi halde aşağıdaki
// "kullanılmayan anahtar" denetimi katalogdaki anahtarı ölü sayar.
var apiErrCodeRefs = []*regexp.Regexp{
	regexp.MustCompile(`\b(?:apiError|apiFail|apiAbort)\(c, [^,]+, "([^"]+)"`),
	regexp.MustCompile(`\b(?:apiErrBody|errCode)\((?:c, )?"([^"]+)"`),
	regexp.MustCompile(`\bfail\("[^"]+", "([^"]+)"`),        // verifyAPIToken: fail(reason, code, …)
	regexp.MustCompile(`return false, "([a-z0-9_]+)"`),      // twoFactorAvailable
	regexp.MustCompile(`\bcode :?= "([a-z0-9_]+)"`),         // renderError
	regexp.MustCompile(`\bapiMsg\([^,]+, "([a-z0-9_]+)"`),   // doğrudan ileti
	regexp.MustCompile(`\bfetchErrBody\(c, "([a-z0-9_]+)"`), // çekme hatası sarmalayıcısı
}

var apiMsgRefs = regexp.MustCompile(`\bapiText\(c, "([^"]+)"\)`)

var stableCode = regexp.MustCompile(`^[a-z0-9]+(?:_[a-z0-9]+)*$`)

// scanAPIRefs pkg/web üretim kaynaklarındaki hata kodlarını ve başarı ileti
// anahtarlarını toplar (kod → ilk geçtiği dosya).
func scanAPIRefs(t *testing.T) (codes, msgs map[string]string) {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	codes, msgs = map[string]string{}, map[string]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		for _, re := range apiErrCodeRefs {
			for _, m := range re.FindAllStringSubmatch(src, -1) {
				if _, ok := codes[m[1]]; !ok {
					codes[m[1]] = f
				}
			}
		}
		for _, m := range apiMsgRefs.FindAllStringSubmatch(src, -1) {
			msgs[m[1]] = f
		}
	}
	return codes, msgs
}

// Koddaki her hata kodu ve başarı iletisi TR+EN kataloğunda bulunmalı; kodlar
// kararlı snake_case olmalı; katalogda koddan referans almayan (ölü) anahtar
// kalmamalı.
func TestAPIErrorCodes_ReferencedKeysExistInBothCatalogs(t *testing.T) {
	codes, msgs := scanAPIRefs(t)
	if len(codes) < 100 {
		t.Fatalf("tarayıcı beklenenden az kod buldu (%d); regex'ler bozulmuş olabilir", len(codes))
	}
	for code, file := range codes {
		if !stableCode.MatchString(code) {
			t.Errorf("%s: kod snake_case değil: %q", file, code)
		}
		for _, lang := range i18n.Supported {
			if !i18n.Has(lang, "apierr."+code) {
				t.Errorf("%s: apierr.%s %s kataloğunda yok", file, code, lang)
			}
		}
	}
	for key, file := range msgs {
		for _, lang := range i18n.Supported {
			if !i18n.Has(lang, "apimsg."+key) {
				t.Errorf("%s: apimsg.%s %s kataloğunda yok", file, key, lang)
			}
		}
	}
	for _, lang := range i18n.Supported {
		for _, k := range i18n.KeysWithPrefix(lang, "apierr.") {
			if _, ok := codes[strings.TrimPrefix(k, "apierr.")]; !ok {
				t.Errorf("%s: %s kodda kullanılmıyor (ölü anahtar)", lang, k)
			}
		}
		for _, k := range i18n.KeysWithPrefix(lang, "apimsg.") {
			if _, ok := msgs[strings.TrimPrefix(k, "apimsg.")]; !ok {
				t.Errorf("%s: %s kodda kullanılmıyor (ölü anahtar)", lang, k)
			}
		}
	}
}

// Üretim kodunda elle yazılmış {"error": "…"} yanıtı kalmamalı: her hata
// apiError/apiFail/apiAbort (ya da MISP için mispFail) üzerinden geçer.
func TestAPIErrors_NoHardcodedErrorBodies(t *testing.T) {
	literal := regexp.MustCompile(`gin\.H\{[^}]*"error":\s*("|err\.Error|fmt\.|failure\.)`)
	files, _ := filepath.Glob("*.go")
	sort.Strings(files)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for i, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, "s.audit") || strings.Contains(line, "auditSystem") {
				continue // denetim ayrıntısındaki "error" alanı yanıt değil
			}
			if literal.MatchString(line) {
				t.Errorf("%s:%d: sabit hata gövdesi; apiError/apiFail kullanın: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}

func TestParseAcceptLanguage(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"", "", false},
		{"en", "en", true},
		{"en-US,en;q=0.9", "en", true},
		{"tr-TR,tr;q=0.9,en-US;q=0.8,en;q=0.7", "tr", true},
		{"de-DE,de;q=0.9,en;q=0.8", "en", true},
		{"fr, de;q=0.5", "", false},
		{"tr;q=0.4, en;q=0.6", "en", true},
		{"en;q=0, tr;q=0.1", "tr", true},
		{"en;q=0", "", false},
		{"en;q=bogus", "", false},
		{"en;q=1.5", "", false},
		{"*", "", false},
		{"EN_gb", "en", true},
		{"en;q=0.5, tr;q=0.5", "en", true}, // eşitlikte başlık sırası
		{strings.Repeat("x,", 400) + "en", "", false},
	}
	for _, c := range cases {
		got, ok := parseAcceptLanguage(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("parseAcceptLanguage(%.40q) = %q,%v; want %q,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestAPIMsg_FormatsAndKeepsArgsLiteral(t *testing.T) {
	if got := apiMsg("en", "token_limit", 10); got != "At most 10 active tokens are allowed; revoke the ones you no longer use" {
		t.Fatalf("en biçim: %q", got)
	}
	if got := apiMsg("tr", "token_limit", 10); !strings.Contains(got, "En fazla 10 etkin token") {
		t.Fatalf("tr biçim: %q", got)
	}
	// Güvenilmeyen argümandaki fmt fiilleri yorumlanmaz, olduğu gibi kalır.
	if got := apiMsg("en", "audit_date_invalid", "%d%s%n"); got != "Invalid date: %d%s%n" {
		t.Fatalf("argüman biçim dizesi gibi yorumlanmamalı: %q", got)
	}
	// Argümansız ileti Sprintf'ten geçmez.
	if got := apiMsg("en", "invalid_request"); got != "Invalid request" {
		t.Fatalf("argümansız: %q", got)
	}
	if ceilSeconds(0) != 1 || ceilSeconds(1500*time.Millisecond) != 2 || ceilSeconds(3*time.Second) != 3 {
		t.Fatal("ceilSeconds yukarı yuvarlamalı (en az 1)")
	}
}

// decodeAPIErr hata gövdesini çözer.
func decodeAPIErr(t *testing.T, w *httptest.ResponseRecorder) (msg, code string, body map[string]any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("JSON değil (%d): %s", w.Code, w.Body.String())
	}
	msg, _ = body["error"].(string)
	code, _ = body["code"].(string)
	return msg, code, body
}

// Dil seçimi: lang çerezi > Accept-Language > tr. Kimliksiz 401 bunu
// oturumdan bağımsız sınar.
func TestAPIError_LanguageNegotiation(t *testing.T) {
	s := newTestServer(t)
	langCk := func(v string) []*http.Cookie { return []*http.Cookie{{Name: "lang", Value: v}} }
	cases := []struct {
		name    string
		cookies []*http.Cookie
		headers map[string]string
		want    string
	}{
		{"varsayılan", nil, nil, "Oturum süresi doldu; lütfen yeniden giriş yapın"},
		{"çerez en", langCk("en"), nil, "Your session has expired; please sign in again"},
		{"Accept-Language en", nil, map[string]string{"Accept-Language": "en-US,en;q=0.9"}, "Your session has expired; please sign in again"},
		{"desteklenmeyen dil", nil, map[string]string{"Accept-Language": "de-DE,fr;q=0.8"}, "Oturum süresi doldu; lütfen yeniden giriş yapın"},
		{"çerez başlığı ezer", langCk("tr"), map[string]string{"Accept-Language": "en"}, "Oturum süresi doldu; lütfen yeniden giriş yapın"},
		{"geçersiz çerez başlığa düşer", langCk("xx"), map[string]string{"Accept-Language": "en"}, "Your session has expired; please sign in again"},
	}
	for _, c := range cases {
		w := do(s, http.MethodGet, "/api/stats", nil, c.cookies, c.headers)
		msg, code, _ := decodeAPIErr(t, w)
		if w.Code != http.StatusUnauthorized || code != "session_expired" || msg != c.want {
			t.Errorf("%s: %d code=%q msg=%q", c.name, w.Code, code, msg)
		}
	}
}

// Ara katman ve handler hataları İngilizce arayüzde İngilizce, kod ve HTTP
// durumu dilden bağımsız; success:false eski istemciler için korunur.
func TestAPIError_EnglishAcrossLayers(t *testing.T) {
	s := newMultiUserServer(t)
	admin := append(loginAs(t, s, "admin", "adminpass1"), &http.Cookie{Name: "lang", Value: "en"})

	// CSRF (ara katman, 403)
	w := doJSON(s, http.MethodPost, "/api/users", `{}`, admin, "")
	if msg, code, _ := decodeAPIErr(t, w); w.Code != http.StatusForbidden || code != "csrf_failed" || !strings.HasPrefix(msg, "CSRF validation failed") {
		t.Fatalf("csrf: %d %s", w.Code, w.Body.String())
	}

	// Handler doğrulaması (apiFail → success:false korunur)
	w = doJSON(s, http.MethodPost, "/api/users", `{"username":"a b","password":"long-enough-1","role":"viewer"}`, admin, csrfOf(admin))
	msg, code, body := decodeAPIErr(t, w)
	if w.Code != http.StatusBadRequest || code != "username_invalid" || msg != "The username must be 3-64 characters with no spaces" || body["success"] != false {
		t.Fatalf("doğrulama: %d %s", w.Code, w.Body.String())
	}

	// Ayrıştırma yardımcısı (errCode → apiErrorFrom), dinamik argümanla
	w = do(s, http.MethodGet, "/api/audit?from=not-a-date", nil, admin, nil)
	if msg, code, _ := decodeAPIErr(t, w); w.Code != http.StatusBadRequest || code != "audit_date_invalid" || msg != "Invalid date: not-a-date" {
		t.Fatalf("audit tarih: %d %s", w.Code, w.Body.String())
	}

	// /api altında bilinmeyen rota (renderError → JSON)
	w = do(s, http.MethodGet, "/api/does-not-exist", nil, admin, nil)
	if msg, code, _ := decodeAPIErr(t, w); w.Code != http.StatusNotFound || code != "not_found" || msg != "Not found" {
		t.Fatalf("404: %d %s", w.Code, w.Body.String())
	}

	// Rol kapısı (viewer yazamaz)
	viewer := append(mkTokenUser(t, s, "vera", "viewer-pass-1", storage.RoleViewer), &http.Cookie{Name: "lang", Value: "en"})
	w = doJSON(s, http.MethodPost, "/api/watchlist", `{}`, viewer, csrfOf(viewer))
	if msg, code, _ := decodeAPIErr(t, w); w.Code != http.StatusForbidden || code != "viewer_read_only" || msg != "The read-only (viewer) role cannot do this" {
		t.Fatalf("viewer: %d %s", w.Code, w.Body.String())
	}
	w = do(s, http.MethodGet, "/api/users", nil, viewer, nil)
	if msg, code, _ := decodeAPIErr(t, w); w.Code != http.StatusForbidden || code != "role_required" || msg != "You are not allowed to do this (requires the admin role)" {
		t.Fatalf("admin rolü: %d %s", w.Code, w.Body.String())
	}

	// Aynı istek Türkçe arayüzde Türkçe, kod aynı.
	trViewer := []*http.Cookie{}
	for _, ck := range viewer {
		if ck.Name != "lang" {
			trViewer = append(trViewer, ck)
		}
	}
	w = do(s, http.MethodGet, "/api/users", nil, trViewer, nil)
	if msg, code, _ := decodeAPIErr(t, w); w.Code != http.StatusForbidden || code != "role_required" || msg != "Bu işlem için yetkiniz yok (admin rolü gerekir)" {
		t.Fatalf("tr: %d %s", w.Code, w.Body.String())
	}
}

// Token istemcileri çerez göndermez: dil Accept-Language'den gelir.
func TestAPIError_TokenClientUsesAcceptLanguage(t *testing.T) {
	s := newMultiUserServer(t)
	call := func(token, lang string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/results", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		if lang != "" {
			req.Header.Set("Accept-Language", lang)
		}
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
		return w
	}
	w := call("kh_not-a-real-token", "en-GB,en;q=0.9")
	if msg, code, _ := decodeAPIErr(t, w); w.Code != http.StatusUnauthorized || code != "token_invalid" || msg != "Invalid API token" {
		t.Fatalf("en: %d %s", w.Code, w.Body.String())
	}
	w = call("kh_not-a-real-token", "")
	if msg, code, _ := decodeAPIErr(t, w); w.Code != http.StatusUnauthorized || code != "token_invalid" || msg != "Geçersiz API token" {
		t.Fatalf("varsayılan tr: %d %s", w.Code, w.Body.String())
	}

	// Geçerli read token ile yazma: kapsam reddi İngilizce.
	ck := mkTokenUser(t, s, "tokie", "tokie-pass-1", storage.RoleAnalyst)
	tok := createToken(t, s, ck, "ci", storage.TokenScopeRead, 0)
	req := httptest.NewRequest(http.MethodPost, "/api/watchlist", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", "en")
	w = httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	if msg, code, _ := decodeAPIErr(t, w); w.Code != http.StatusForbidden || code != "token_read_only" || msg != "A read-scoped API token cannot perform write operations" {
		t.Fatalf("read kapsamı: %d %s", w.Code, w.Body.String())
	}
}

// Hız sınırı (429) da sözleşmeye uyar.
func TestAPIError_RateLimitTranslated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rl := NewIPRateLimiter(0.001, 1)
	r := gin.New()
	r.Use(rl.Middleware())
	r.GET("/api/x", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	hit := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
		req.Header.Set("Accept-Language", "en")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	if w := hit(); w.Code != http.StatusOK {
		t.Fatalf("ilk istek: %d", w.Code)
	}
	w := hit()
	if msg, code, _ := decodeAPIErr(t, w); w.Code != http.StatusTooManyRequests || code != "too_many_requests" || msg != "Too many requests; please try again shortly" {
		t.Fatalf("429: %d %s", w.Code, w.Body.String())
	}
}

// MISP: iki dilli doğrulama hataları (misp.Error) ve katalog iletileri aynı
// sözleşmeyle (code + istek dili) döner.
func TestAPIError_MISPFollowsLanguageAndCode(t *testing.T) {
	s, admin, analyst, _ := newMISPServer(t)
	en := append(append([]*http.Cookie{}, admin...), &http.Cookie{Name: "lang", Value: "en"})
	w := saveMISP(t, s, en, `{"distribution":9}`)
	if msg, code, body := decodeAPIErr(t, w); w.Code != http.StatusBadRequest || code != "misp_config_invalid" || msg != "distribution must be between 0 and 3" || body["success"] != false {
		t.Fatalf("misp doğrulama en: %d %s", w.Code, w.Body.String())
	}
	if msg, code, _ := decodeAPIErr(t, saveMISP(t, s, admin, `{"distribution":9}`)); code != "misp_config_invalid" || !strings.Contains(msg, "arasında olmalı") {
		t.Fatalf("misp doğrulama tr: %q %q", code, msg)
	}
	enAnalyst := append(append([]*http.Cookie{}, analyst...), &http.Cookie{Name: "lang", Value: "en"})
	w = doJSON(s, http.MethodPost, "/api/integrations/misp/push", `{"resultIds":[]}`, enAnalyst, csrfOf(analyst))
	if msg, code, _ := decodeAPIErr(t, w); w.Code != http.StatusBadRequest || code != "misp_push_range" || msg != "Select 1–250 findings per push" {
		t.Fatalf("misp push aralığı: %d %s", w.Code, w.Body.String())
	}
}

// hasTurkish İngilizce yanıta Türkçe metin sızmış mı (Türkçeye özgü harfler).
func hasTurkish(s string) bool { return strings.ContainsAny(s, "çğışöüÇĞİŞÖÜ") }

// Tor kapalıyken (en sık üretim hatası) çekme hataları İngilizce arayüzde
// tamamen İngilizce dönmeli; kod hatanın sınıfını söyler.
func TestFetchErrBody_ClassifiedErrorsTranslated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		err      error
		wantCode string
	}{
		{shared.ClassifyError(errors.New("dial tcp 127.0.0.1:9150: connect: connection refused")), "tor_unreachable"},
		{fmt.Errorf("%s", shared.ErrSiteUnreachable.Error()), "site_unreachable"}, // scraper: metne dönüşmüş
		{fmt.Errorf("%w: %s", tagging.ErrFetchFailed, shared.ErrSiteNotResponding.Error()), "site_not_responding"},
		{fmt.Errorf("x: %w", shared.ErrFetchTimeout), "site_timeout"},
	}
	for _, tc := range cases {
		for _, lang := range []string{"en", "tr"} {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/api/analyze-result", nil)
			c.Request.AddCookie(&http.Cookie{Name: "lang", Value: lang})
			body := fetchErrBody(c, "analyze_failed", tc.err, tagging.ErrFetchFailed)
			msg, _ := body["error"].(string)
			if body["code"] != tc.wantCode {
				t.Errorf("%v [%s]: code = %v, want %s", tc.err, lang, body["code"], tc.wantCode)
			}
			want := apiMsg(lang, "analyze_failed", apiMsg(lang, tc.wantCode))
			if msg != want {
				t.Errorf("%v [%s]: ileti = %q, want %q", tc.err, lang, msg, want)
			}
			if lang == "en" && hasTurkish(msg) {
				t.Errorf("EN iletisinde Türkçe metin: %q", msg)
			}
		}
	}

	// Tanınmayan hata: sarmalayıcı kodu + kırpılmış ham ayrıntı (önek atılır).
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/auto-tag", nil)
	c.Request.Header.Set("Accept-Language", "en")
	body := fetchErrBody(c, "tag_fetch_failed", fmt.Errorf("%w: HTTP 404", tagging.ErrFetchFailed), tagging.ErrFetchFailed)
	if body["code"] != "tag_fetch_failed" || body["error"] != "Could not fetch the page: HTTP 404" {
		t.Fatalf("tanınmayan hata: %v", body)
	}
}

// Uçtan uca: Tor proxy'si kapalı (bağlantı reddedilir) ve arayüz İngilizce →
// /api/expand yanıtında Türkçe alt katman metni olmamalı; durum kodu aynı kalır.
func TestExpand_TorDownIsEnglish(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedAddr := ln.Addr().String()
	_ = ln.Close() // artık kimse dinlemiyor: SOCKS bağlantısı reddedilir

	s := newTestServer(t)
	sc, err := scraper.New(closedAddr)
	if err != nil {
		t.Fatal(err)
	}
	s.scraper = sc
	ck := append(loginAs(t, s, "admin", "correct-horse"), &http.Cookie{Name: "lang", Value: "en"})
	onion := "http://" + strings.Repeat("a", 56) + ".onion/"
	w := doJSON(s, http.MethodPost, "/api/expand", `{"url":"`+onion+`"}`, ck, csrfOf(ck))
	msg, code, body := decodeAPIErr(t, w)
	if w.Code != http.StatusInternalServerError || code != "tor_unreachable" || body["success"] != false {
		t.Fatalf("expand: %d %s", w.Code, w.Body.String())
	}
	if hasTurkish(msg) || !strings.HasPrefix(msg, "Could not crawl the page: Could not connect to the Tor proxy") {
		t.Fatalf("EN ileti beklenirdi: %q", msg)
	}
}

// Toplu etiketleme işinin kayıtlı (Türkçe) hata iletisi istek dilinde ve
// kararlı kodla dönmeli; bilinmeyen ileti ham haliyle sızmamalı.
func TestBatchAutoTagStatus_ErrorTranslated(t *testing.T) {
	db := newWebTestDB(t)
	seed := func(id, status, msg string) {
		t.Helper()
		if err := db.CreateTaggingJob(storage.TaggingJob{ID: id, Query: "q", TotalCount: 1, Status: tagging.StatusPending, ResultIDsJSON: "[1]"}); err != nil {
			t.Fatal(err)
		}
		if err := db.MarkTaggingJobFinished(id, status, msg); err != nil {
			t.Fatal(err)
		}
	}
	seed("job-cancel", tagging.StatusCancelled, tagging.JobMsgCancelled)
	seed("job-legacy", tagging.StatusFailed, "eski sürümden kalma <b>ileti</b>")
	seed("job-ok", tagging.StatusCompleted, "")

	r := setupAPIRouter(&Server{db: db, tagEngine: mockTagEngine{}, batchRunner: mockBatchRunner{}})
	get := func(id string, hdr map[string]string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/batch-auto-tag/"+id, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", id, w.Code, w.Body.String())
		}
		var m map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	en := map[string]string{"Cookie": "lang=en"}
	if m := get("job-cancel", en); m["error"] != "The job was cancelled by the user" || m["errorCode"] != "job_cancelled_by_user" {
		t.Fatalf("EN iptal: %v", m)
	}
	if m := get("job-cancel", map[string]string{"Accept-Language": "en-US"}); m["error"] != "The job was cancelled by the user" {
		t.Fatalf("Accept-Language EN iptal: %v", m)
	}
	if m := get("job-cancel", nil); m["error"] != "İş kullanıcı tarafından iptal edildi" || m["errorCode"] != "job_cancelled_by_user" {
		t.Fatalf("TR iptal: %v", m)
	}
	if m := get("job-legacy", en); m["error"] != "The tagging job failed" || m["errorCode"] != "tagging_job_failed" {
		t.Fatalf("bilinmeyen ileti: %v", m)
	}
	if m := get("job-ok", en); m["error"] != "" || m["errorCode"] != "" {
		t.Fatalf("hatasız iş: %v", m)
	}

	// Runner'ın yazdığı her ileti kendi koduna eşlenmeli (genel koda düşmemeli).
	for _, msg := range []string{tagging.JobMsgQueueFull, tagging.JobMsgCancelled, tagging.JobMsgPayloadInvalid,
		tagging.JobMsgPayloadEmpty, tagging.JobMsgAllFailed, storage.TaggingJobRestartedMsg} {
		if code := taggingJobErrCode(msg); code == "tagging_job_failed" {
			t.Errorf("%q genel koda düştü", msg)
		}
	}
}
