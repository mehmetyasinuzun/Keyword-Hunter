package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"keywordhunter-mvp/pkg/storage"
	"keywordhunter-mvp/pkg/totp"
)

// testMISPKey sahte MISP'in kabul ettiği API anahtarı; hiçbir yanıtta,
// sayfada ya da denetim kaydında görünmemeli.
const testMISPKey = "MISPkey0123456789abcdefABCDEF0123456789XY"

// fakeMISP gelen istekleri kaydeden basit bir MISP taklidi.
type fakeMISP struct {
	*httptest.Server
	mu       sync.Mutex
	reqs     []fakeMISPReq
	addCode  int // /events/add yanıt kodu (0 = 200)
	eventSeq int
}

type fakeMISPReq struct {
	Method, Path, Auth, Accept, ContentType string
	Body                                    []byte
}

func newFakeMISP(t *testing.T) *fakeMISP {
	t.Helper()
	f := &fakeMISP{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.reqs = append(f.reqs, fakeMISPReq{r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Accept"), r.Header.Get("Content-Type"), body})
		code := f.addCode
		f.eventSeq++
		seq := f.eventSeq
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != testMISPKey {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"name":"Authentication failed.","message":"Authentication failed."}`))
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/servers/getVersion":
			_, _ = w.Write([]byte(`{"version":"2.4.199","perm_sync":false}`))
		case r.Method == http.MethodPost && r.URL.Path == "/events/add":
			if code != 0 {
				w.WriteHeader(code)
				_, _ = w.Write([]byte(`{"name":"Could not add Event","message":"Could not add Event","errors":{"Event":"x"}}`))
				return
			}
			_, _ = fmt.Fprintf(w, `{"Event":{"id":"%d","uuid":"5f3c2a1b-0000-4000-8000-00000000000%d","info":"x"}}`, 76+seq, seq%10)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeMISP) requests(path string) []fakeMISPReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeMISPReq
	for _, r := range f.reqs {
		if r.Path == path {
			out = append(out, r)
		}
	}
	return out
}

// newMISPServer test sunucusu + entegrasyon mühürleyicisi + admin/analyst/viewer oturumları.
func newMISPServer(t *testing.T) (s *Server, admin, analyst, viewer []*http.Cookie) {
	t.Helper()
	s = newTestServer(t)
	key, err := totp.GenerateAppSecret()
	if err != nil {
		t.Fatal(err)
	}
	if s.secretSealer, err = totp.NewSealerFor(key, totp.PurposeIntegrationSecret); err != nil {
		t.Fatal(err)
	}
	admin = loginAs(t, s, "admin", "correct-horse")
	analyst = mkTokenUser(t, s, "ana", "ana-pass-123", storage.RoleAnalyst)
	viewer = mkTokenUser(t, s, "vic", "vic-pass-123", storage.RoleViewer)
	return s, admin, analyst, viewer
}

func saveMISP(t *testing.T, s *Server, ck []*http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	return doJSON(s, http.MethodPost, "/api/integrations/misp/config", body, ck, csrfOf(ck))
}

func configureMISP(t *testing.T, s *Server, admin []*http.Cookie, f *fakeMISP) {
	t.Helper()
	w := saveMISP(t, s, admin, fmt.Sprintf(`{"baseUrl":%q,"apiKey":%q,"allowPrivate":true,"verifyTls":true,"distribution":1,"tags":"tlp:amber, source:keywordhunter"}`, f.URL, testMISPKey))
	if w.Code != http.StatusOK {
		t.Fatalf("MISP ayarı kaydedilemedi: %d %s", w.Code, w.Body.String())
	}
}

func TestMISP_SecretNeverReturned(t *testing.T) {
	s, admin, analyst, _ := newMISPServer(t)
	f := newFakeMISP(t)
	w := saveMISP(t, s, admin, fmt.Sprintf(`{"baseUrl":%q,"apiKey":%q,"allowPrivate":true}`, f.URL+"/", testMISPKey))
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), testMISPKey) {
		t.Fatalf("kaydetme: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		MISP map[string]any `json:"misp"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.MISP["hasApiKey"] != true || resp.MISP["configured"] != true || resp.MISP["baseUrl"] != f.URL || resp.MISP["verifyTls"] != true {
		t.Fatalf("görünüm: %v", resp.MISP)
	}
	if _, ok := resp.MISP["apiKey"]; ok {
		t.Fatal("yanıtta apiKey alanı olmamalı")
	}

	// GET ayarları, Ayarlar sayfası ve Bulgular sayfası anahtarı içermez.
	for _, path := range []string{"/api/integrations/misp/config", "/settings"} {
		w := doJSON(s, http.MethodGet, path, "", admin, "")
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), testMISPKey) {
			t.Fatalf("%s anahtarı sızdırdı ya da açılamadı: %d", path, w.Code)
		}
	}
	if w := doJSON(s, http.MethodGet, "/results", "", analyst, ""); w.Code != http.StatusOK || strings.Contains(w.Body.String(), testMISPKey) {
		t.Fatalf("/results: %d", w.Code)
	}

	// Depoda yalnız şifreli metin; TOTP anahtarıyla (başka amaç) çözülemez.
	cfg, err := s.db.GetMISPConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKeyEnc == "" || strings.Contains(cfg.APIKeyEnc, testMISPKey) || !strings.HasPrefix(cfg.APIKeyEnc, "v1:") {
		t.Fatalf("anahtar şifreli saklanmalı: %q", cfg.APIKeyEnc)
	}
	if plain, err := s.secretSealer.Open(cfg.APIKeyEnc, mispKeyContext); err != nil || plain != testMISPKey {
		t.Fatalf("anahtar çözülemedi: %v", err)
	}
	if _, err := s.secretSealer.Open(cfg.APIKeyEnc, "user:1"); err == nil {
		t.Fatal("farklı bağlamla çözülmemeli")
	}

	// Test + gönderim sonrası bile denetim kaydında anahtar yok.
	if w := doJSON(s, http.MethodPost, "/api/integrations/misp/test", `{}`, admin, csrfOf(admin)); w.Code != http.StatusOK {
		t.Fatalf("test: %d %s", w.Code, w.Body.String())
	}
	assertNoSecretsInAudit(t, s, testMISPKey)
	if e := lastAudit(t, s, "misp.config.update"); !e.Success || !strings.Contains(e.Details, `"keyChanged":true`) {
		t.Fatalf("ayar denetimi: %+v", e)
	}

	// Anahtarsız (yalnız eşik) kaydetme kayıtlı anahtarı korur; silme kaldırır.
	if w := saveMISP(t, s, admin, `{"distribution":2}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"hasApiKey":true`) {
		t.Fatalf("anahtar korunmalı: %s", w.Body.String())
	}
	if w := saveMISP(t, s, admin, `{"clearApiKey":true}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"hasApiKey":false`) {
		t.Fatalf("anahtar silinmeli: %s", w.Body.String())
	}
}

func TestMISP_KeyRequiresEncryptionKey(t *testing.T) {
	s, admin, _, _ := newMISPServer(t)
	s.secretSealer = nil
	w := saveMISP(t, s, admin, fmt.Sprintf(`{"baseUrl":"https://misp.example.org","apiKey":%q}`, testMISPKey))
	if w.Code != http.StatusConflict || apiCode(w) != "misp_secret_unavailable" || !strings.Contains(w.Body.String(), "APP_SECRET") {
		t.Fatalf("APP_SECRET yokken anahtar düz metin saklanmamalı: %d %s", w.Code, w.Body.String())
	}
}

func TestMISP_SSRFGuard(t *testing.T) {
	s, admin, _, _ := newMISPServer(t)
	f := newFakeMISP(t)
	for _, target := range []string{f.URL, "https://127.0.0.1", "https://10.1.2.3", "https://169.254.169.254", "https://misp.internal", "http://misp.example.org", "https://abcdefghijklmnop.onion"} {
		w := saveMISP(t, s, admin, fmt.Sprintf(`{"baseUrl":%q,"apiKey":%q,"allowPrivate":false}`, target, testMISPKey))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: özel ağ izni yokken reddedilmeli: %d %s", target, w.Code, w.Body.String())
		}
		// Test ucu da aynı kurala uyar (kaydedilmemiş taslakla).
		w = doJSON(s, http.MethodPost, "/api/integrations/misp/test", fmt.Sprintf(`{"baseUrl":%q,"apiKey":%q,"allowPrivate":false}`, target, testMISPKey), admin, csrfOf(admin))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: test ucu SSRF korumasını atlamamalı: %d %s", target, w.Code, w.Body.String())
		}
	}
	if n := len(f.requests("/servers/getVersion")); n != 0 {
		t.Fatalf("reddedilen hedefe istek gitmemeli: %d", n)
	}
	// Özel ağ izni açıkken bile düz http genel bir IP'ye kaydedilemez/test
	// edilemez: anahtar Authorization başlığında şifresiz giderdi.
	for _, target := range []string{"http://93.184.215.14", "http://8.8.8.8:8080/misp"} {
		w := saveMISP(t, s, admin, fmt.Sprintf(`{"baseUrl":%q,"apiKey":%q,"allowPrivate":true}`, target, testMISPKey))
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "https") {
			t.Fatalf("%s: düz http genel IP reddedilmeli: %d %s", target, w.Code, w.Body.String())
		}
		w = doJSON(s, http.MethodPost, "/api/integrations/misp/test", fmt.Sprintf(`{"baseUrl":%q,"apiKey":%q,"allowPrivate":true}`, target, testMISPKey), admin, csrfOf(admin))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: test ucu düz http genel IP'yi reddetmeli: %d %s", target, w.Code, w.Body.String())
		}
	}
	// Kurum içi MISP: açık izinle kabul.
	configureMISP(t, s, admin, f)
	// İzin sonradan kapatılırsa kayıtlı özel adres yeniden doğrulanır.
	if w := saveMISP(t, s, admin, `{"allowPrivate":false}`); w.Code != http.StatusBadRequest {
		t.Fatalf("izin kapatılınca özel adres reddedilmeli: %d", w.Code)
	}
}

func TestMISP_OriginChangeRequiresKeyReentry(t *testing.T) {
	s, admin, _, _ := newMISPServer(t)
	f := newFakeMISP(t)
	configureMISP(t, s, admin, f)
	other := newFakeMISP(t) // saldırganın sunucusu
	// Kaydetme ve test: anahtar olmadan başka bir sunucuya taşınamaz.
	if w := saveMISP(t, s, admin, fmt.Sprintf(`{"baseUrl":%q}`, other.URL)); w.Code != http.StatusBadRequest || apiCode(w) != "misp_config_invalid" || !strings.Contains(w.Body.String(), "yeniden girin") {
		t.Fatalf("adres değişince anahtar istenmeli: %d %s", w.Code, w.Body.String())
	}
	if w := doJSON(s, http.MethodPost, "/api/integrations/misp/test", fmt.Sprintf(`{"baseUrl":%q}`, other.URL), admin, csrfOf(admin)); w.Code != http.StatusBadRequest {
		t.Fatalf("test ucu kayıtlı anahtarı başka sunucuya göndermemeli: %d", w.Code)
	}
	if n := len(other.requests("/servers/getVersion")); n != 0 {
		t.Fatalf("kayıtlı anahtar başka sunucuya gitti: %d istek", n)
	}
	// Aynı sunucuda yol değişikliği anahtarı korur.
	if w := saveMISP(t, s, admin, fmt.Sprintf(`{"baseUrl":%q}`, f.URL+"/misp")); w.Code != http.StatusOK {
		t.Fatalf("aynı origin: %d %s", w.Code, w.Body.String())
	}
	// Yeni anahtarla taşınabilir.
	if w := saveMISP(t, s, admin, fmt.Sprintf(`{"baseUrl":%q,"apiKey":%q}`, other.URL, testMISPKey)); w.Code != http.StatusOK {
		t.Fatalf("yeni anahtarla taşınmalı: %d %s", w.Code, w.Body.String())
	}
}

func TestMISP_TestEndpoint(t *testing.T) {
	s, admin, analyst, _ := newMISPServer(t)
	f := newFakeMISP(t)
	if w := doJSON(s, http.MethodPost, "/api/integrations/misp/test", `{}`, admin, csrfOf(admin)); w.Code != http.StatusBadRequest {
		t.Fatalf("yapılandırma yokken: %d", w.Code)
	}
	configureMISP(t, s, admin, f)
	w := doJSON(s, http.MethodPost, "/api/integrations/misp/test", `{}`, admin, csrfOf(admin))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"version":"2.4.199"`) {
		t.Fatalf("test: %d %s", w.Code, w.Body.String())
	}
	reqs := f.requests("/servers/getVersion")
	if len(reqs) != 1 || reqs[0].Method != http.MethodGet || reqs[0].Auth != testMISPKey || reqs[0].Accept != "application/json" {
		t.Fatalf("getVersion isteği: %+v", reqs)
	}
	if e := lastAudit(t, s, "misp.test"); !e.Success || !strings.HasPrefix(e.Target, "misp:127.0.0.1") {
		t.Fatalf("test denetimi: %+v", e)
	}
	// Yanlış anahtar (taslak): sunucu hata iletisi döner, anahtar sızmaz.
	w = doJSON(s, http.MethodPost, "/api/integrations/misp/test", `{"apiKey":"wrong-key-123"}`, admin, csrfOf(admin))
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "403") || strings.Contains(w.Body.String(), "wrong-key-123") {
		t.Fatalf("yanlış anahtar: %d %s", w.Code, w.Body.String())
	}
	// Admin dışı roller ayar/test uçlarını çağıramaz.
	for _, path := range []string{"/api/integrations/misp/test", "/api/integrations/misp/config"} {
		if w := doJSON(s, http.MethodPost, path, `{}`, analyst, csrfOf(analyst)); w.Code != http.StatusForbidden {
			t.Fatalf("analyst %s: %d", path, w.Code)
		}
	}
	if w := doJSON(s, http.MethodGet, "/api/integrations/misp/config", "", analyst, ""); w.Code != http.StatusForbidden {
		t.Fatalf("analyst ayar okuma: %d", w.Code)
	}
	assertNoSecretsInAudit(t, s, testMISPKey, "wrong-key-123")
}

type mispEventBody struct {
	Event struct {
		Info          string `json:"info"`
		Distribution  string `json:"distribution"`
		ThreatLevelID string `json:"threat_level_id"`
		Attribute     []struct {
			Type     string `json:"type"`
			Category string `json:"category"`
			Value    string `json:"value"`
			Comment  string `json:"comment"`
			ToIDS    bool   `json:"to_ids"`
		} `json:"Attribute"`
		Tag []struct {
			Name string `json:"name"`
		} `json:"Tag"`
	} `json:"Event"`
}

func TestMISP_PushMapsFindingsToOneEvent(t *testing.T) {
	s, admin, analyst, _ := newMISPServer(t)
	f := newFakeMISP(t)
	ids := seedFeed(t, s, taxiiFixture())
	configureMISP(t, s, admin, f)

	body := fmt.Sprintf(`{"resultIds":[%d,%d,%d]}`, ids[0], ids[1], ids[0])
	w := doJSON(s, http.MethodPost, "/api/integrations/misp/push", body, analyst, csrfOf(analyst))
	if w.Code != http.StatusOK {
		t.Fatalf("gönderim: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		EventID    string         `json:"eventId"`
		EventURL   string         `json:"eventUrl"`
		Findings   int            `json:"findings"`
		Attributes int            `json:"attributes"`
		Skipped    map[string]int `json:"skipped"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.EventID == "" || resp.EventURL != f.URL+"/events/view/"+resp.EventID || resp.Findings != 2 || resp.Skipped["credit_card"] != 1 {
		t.Fatalf("yanıt: %s", w.Body.String())
	}

	adds := f.requests("/events/add")
	if len(adds) != 1 {
		t.Fatalf("tek olay beklenirdi: %d", len(adds))
	}
	req := adds[0]
	if req.Auth != testMISPKey || req.Method != http.MethodPost || !strings.HasPrefix(req.ContentType, "application/json") || req.Accept != "application/json" {
		t.Fatalf("istek başlıkları: %+v", req)
	}
	if strings.Contains(string(req.Body), "4111111111111111") {
		t.Fatal("kart numarası MISP'e gönderilmemeli")
	}
	var ev mispEventBody
	if err := json.Unmarshal(req.Body, &ev); err != nil {
		t.Fatalf("gövde JSON değil: %v", err)
	}
	if ev.Event.Distribution != "1" || ev.Event.ThreatLevelID != "1" || !strings.Contains(ev.Event.Info, "2 dark web finding") {
		t.Fatalf("olay alanları: %+v", ev.Event)
	}
	attrs := map[string]string{}
	for _, a := range ev.Event.Attribute {
		attrs[a.Type+"|"+a.Value] = a.Comment
		if a.ToIDS {
			t.Fatalf("to_ids=false olmalı: %+v", a)
		}
	}
	for _, k := range []string{
		"url|http://aaaa.onion/leak", "url|http://bbbb.onion/post", "email-src|seller@proton.me",
		"btc|bc1qexamplewallet0000000000000000000000", "md5|" + strings.Repeat("a", 32), "ip-dst|185.1.2.3",
	} {
		if _, ok := attrs[k]; !ok {
			t.Errorf("öznitelik eksik: %s (%v)", k, attrs)
		}
	}
	if len(attrs) != 6 {
		t.Fatalf("öznitelik sayısı = %d: %v", len(attrs), attrs)
	}
	if attrs["url|http://aaaa.onion/leak"] != "Fresh combo leak" {
		t.Fatalf("URL yorumu bulgu başlığı olmalı: %q", attrs["url|http://aaaa.onion/leak"])
	}
	tags := []string{}
	for _, tg := range ev.Event.Tag {
		tags = append(tags, tg.Name)
	}
	if strings.Join(tags, ",") != "tlp:amber,source:keywordhunter" {
		t.Fatalf("etiketler: %v", tags)
	}

	// Gönderim kaydı + denetim
	var n int
	if err := s.db.GetDBConn().QueryRow(`SELECT COUNT(*) FROM misp_pushed WHERE event_id = ?`, resp.EventID).Scan(&n); err != nil || n != 2 {
		t.Fatalf("misp_pushed: %d %v", n, err)
	}
	e := lastAudit(t, s, "misp.push")
	if !e.Success || e.Actor != "ana" || !strings.Contains(e.Details, `"eventId":"`+resp.EventID+`"`) || auditCategory(e.Action) != "export" {
		t.Fatalf("gönderim denetimi: %+v", e)
	}
	assertNoSecretsInAudit(t, s, testMISPKey)
}

func TestMISP_PushPermissionsAndErrors(t *testing.T) {
	s, admin, analyst, viewer := newMISPServer(t)
	f := newFakeMISP(t)
	ids := seedFeed(t, s, taxiiFixture())
	body := fmt.Sprintf(`{"resultIds":[%d]}`, ids[0])

	if w := doJSON(s, http.MethodPost, "/api/integrations/misp/push", body, analyst, csrfOf(analyst)); w.Code != http.StatusConflict {
		t.Fatalf("yapılandırma yokken 409: %d", w.Code)
	}
	configureMISP(t, s, admin, f)
	if w := doJSON(s, http.MethodPost, "/api/integrations/misp/push", body, viewer, csrfOf(viewer)); w.Code != http.StatusForbidden {
		t.Fatalf("viewer gönderememeli: %d", w.Code)
	}
	if w := doJSON(s, http.MethodPost, "/api/integrations/misp/push", body, analyst, ""); w.Code != http.StatusForbidden {
		t.Fatalf("CSRF'siz oturum isteği reddedilmeli: %d", w.Code)
	}
	tooMany := make([]string, mispMaxPush+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprint(i + 1)
	}
	for _, bad := range []string{`{}`, `{"resultIds":[]}`, `{"resultIds":"1"}`, `{"resultIds":[` + strings.Join(tooMany, ",") + `]}`} {
		w := doJSON(s, http.MethodPost, "/api/integrations/misp/push", bad, analyst, csrfOf(analyst))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%.40s: %d", bad, w.Code)
		}
		// Kullanıcıya iç alan adı (resultIds) gösterilmez.
		if strings.Contains(w.Body.String(), "resultIds") {
			t.Fatalf("hata iletisi alan adını sızdırmamalı: %s", w.Body.String())
		}
	}
	if mispMaxPush < 250 {
		t.Fatal("Bulgular sayfasında 250 satırlık 'tümünü seç' tek gönderime sığmalı")
	}
	if w := doJSON(s, http.MethodPost, "/api/integrations/misp/push", `{"resultIds":[999999]}`, analyst, csrfOf(analyst)); w.Code != http.StatusNotFound {
		t.Fatalf("olmayan bulgu: %d", w.Code)
	}

	// Token: read kapsamı gönderemez, write kapsamı gönderebilir (SOAR);
	// ayar/test uçları token ile hiç çağrılamaz.
	read := createToken(t, s, analyst, "r", storage.TokenScopeRead, 0)
	write := createToken(t, s, analyst, "w", storage.TokenScopeWrite, 0)
	if w := bearer(s, http.MethodPost, "/api/integrations/misp/push", body, read.Token, nil, ""); w.Code != http.StatusForbidden {
		t.Fatalf("read token: %d", w.Code)
	}
	if w := bearer(s, http.MethodPost, "/api/integrations/misp/push", body, write.Token, nil, ""); w.Code != http.StatusOK {
		t.Fatalf("write token: %d %s", w.Code, w.Body.String())
	}
	adminTok := createToken(t, s, mkTokenUser(t, s, "boss", "boss-pass-123", storage.RoleAdmin), "a", storage.TokenScopeWrite, 0)
	for _, path := range []string{"/api/integrations/misp/config", "/api/integrations/misp/test"} {
		if w := bearer(s, http.MethodPost, path, `{}`, adminTok.Token, nil, ""); w.Code != http.StatusForbidden || apiCode(w) != "session_required" {
			t.Fatalf("token ile %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if w := bearer(s, http.MethodGet, "/api/integrations/misp/config", "", adminTok.Token, nil, ""); w.Code != http.StatusForbidden {
		t.Fatalf("token ile ayar okuma: %d", w.Code)
	}

	// MISP hata döndürürse 502 + temiz ileti; denetimde başarısız.
	f.mu.Lock()
	f.addCode = http.StatusForbidden
	f.mu.Unlock()
	w := doJSON(s, http.MethodPost, "/api/integrations/misp/push", body, analyst, csrfOf(analyst))
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "HTTP 403") {
		t.Fatalf("MISP hatası: %d %s", w.Code, w.Body.String())
	}
	if e := lastAudit(t, s, "misp.push"); e.Success {
		t.Fatalf("başarısız gönderim denetimi: %+v", e)
	}
}

func TestMISP_FindingsButtonOnlyWhenConfigured(t *testing.T) {
	s, admin, analyst, _ := newMISPServer(t)
	seedFeed(t, s, taxiiFixture())
	if w := doJSON(s, http.MethodGet, "/results", "", analyst, ""); strings.Contains(w.Body.String(), `data-action="misp-push"`) {
		t.Fatal("MISP yapılandırılmamışken düğme basılmamalı")
	}
	configureMISP(t, s, admin, newFakeMISP(t))
	w := doJSON(s, http.MethodGet, "/results", "", analyst, "")
	if !strings.Contains(w.Body.String(), `data-action="misp-push"`) || !strings.Contains(w.Body.String(), fmt.Sprintf(`data-max="%d"`, mispMaxPush)) {
		t.Fatal("yapılandırıldıktan sonra düğme (gönderim sınırıyla) görünmeli")
	}
	// Ayarlar sayfası TAXII adreslerini ve koleksiyon kimliklerini basar.
	w = doJSON(s, http.MethodGet, "/settings", "", admin, "")
	col, _ := findTAXIICollection("findings-high")
	if !strings.Contains(w.Body.String(), "/taxii2/kh/collections/"+col.ID+"/objects/") || !strings.Contains(w.Body.String(), `id="sec-misp"`) {
		t.Fatal("Ayarlar → Entegrasyonlar bölümü eksik")
	}
}

func TestMISP_AutoPushNewFindingsOnce(t *testing.T) {
	s, admin, _, _ := newMISPServer(t)
	f := newFakeMISP(t)
	configureMISP(t, s, admin, f)
	// Eski bulgular (otomatik gönderim açılmadan önce) gönderilmez.
	seedFeed(t, s, taxiiFixture())
	if w := saveMISP(t, s, admin, `{"autoPush":true,"autoMinCriticality":4}`); w.Code != http.StatusOK {
		t.Fatalf("otomatik gönderim: %d %s", w.Code, w.Body.String())
	}
	future := time.Now().Add(time.Minute)
	seedFeed(t, s, []seedFinding{
		{"New critical leak", "http://dddd.onion/x", "Ahmia", 5, future, []storage.ResultArtifact{{Type: "ip", Value: "203.0.113.9", Confidence: .7}}},
		{"New low noise", "http://eeee.onion/y", "Ahmia", 2, future, nil},
	})
	s.runMISPAutoPush()
	adds := f.requests("/events/add")
	if len(adds) != 1 {
		t.Fatalf("bir otomatik olay beklenirdi: %d", len(adds))
	}
	b := string(adds[0].Body)
	if !strings.Contains(b, "dddd.onion") || strings.Contains(b, "eeee.onion") || strings.Contains(b, "aaaa.onion") {
		t.Fatalf("yalnız yeni ve eşik üstü bulgu gönderilmeli: %s", b)
	}
	s.runMISPAutoPush()
	if n := len(f.requests("/events/add")); n != 1 {
		t.Fatalf("aynı bulgu ikinci kez gönderilmemeli: %d", n)
	}
	e := lastAudit(t, s, "misp.push")
	if !e.Success || e.Actor != "system" || !strings.Contains(e.Details, `"auto":true`) {
		t.Fatalf("otomatik gönderim denetimi: %+v", e)
	}
	// Otomatik gönderim anahtarsız açılamaz.
	if w := saveMISP(t, s, admin, `{"clearApiKey":true}`); !strings.Contains(w.Body.String(), `"autoPush":false`) {
		t.Fatalf("anahtar silinince otomatik gönderim kapanmalı: %s", w.Body.String())
	}
	if w := saveMISP(t, s, admin, `{"autoPush":true}`); w.Code != http.StatusBadRequest {
		t.Fatalf("anahtarsız otomatik gönderim reddedilmeli: %d", w.Code)
	}
}

// MISP hata iletileri arayüz diline göre döner (lang çerezi): İngilizce
// arayüzde Türkçe ileti ya da iç alan adı (allowPrivate, resultIds) görünmez.
func TestMISP_ErrorsFollowUILanguage(t *testing.T) {
	s, admin, analyst, _ := newMISPServer(t)
	f := newFakeMISP(t)
	en := append([]*http.Cookie{{Name: "lang", Value: "en"}}, admin...)
	errOf := func(w *httptest.ResponseRecorder) string {
		t.Helper()
		var resp struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.Error == "" {
			t.Fatalf("hata iletisi bekleniyordu: %d %s", w.Code, w.Body.String())
		}
		return resp.Error
	}
	turkish := func(msg string) bool { return strings.ContainsAny(msg, "çğıöşüÇĞİÖŞÜ") }

	body := `{"baseUrl":"http://misp.example.org","apiKey":"k","allowPrivate":false}`
	if msg := errOf(saveMISP(t, s, admin, body)); !turkish(msg) || !strings.Contains(msg, "https") {
		t.Fatalf("varsayılan dil Türkçe olmalı: %q", msg)
	}
	msg := errOf(saveMISP(t, s, en, body))
	if turkish(msg) || !strings.Contains(msg, "Invalid MISP URL") || !strings.Contains(msg, "https") || strings.Contains(msg, "allowPrivate") {
		t.Fatalf("İngilizce arayüzde İngilizce ileti bekleniyordu: %q", msg)
	}
	// Uzak MISP hatası (yanlış anahtar → HTTP 403) da çevrilir; MISP'in kendi
	// iletisi olduğu gibi kalır.
	w := doJSON(s, http.MethodPost, "/api/integrations/misp/test", fmt.Sprintf(`{"baseUrl":%q,"apiKey":"wrong-key","allowPrivate":true}`, f.URL), en, csrfOf(admin))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("yanlış anahtar 502 olmalı: %d %s", w.Code, w.Body.String())
	}
	if msg := errOf(w); msg != "MISP returned HTTP 403: Authentication failed." {
		t.Fatalf("İngilizce uzak hata: %q", msg)
	}
	// Gönderim: yapılandırılmamış ve aşırı büyük seçim.
	enAnalyst := append([]*http.Cookie{{Name: "lang", Value: "en"}}, analyst...)
	w = doJSON(s, http.MethodPost, "/api/integrations/misp/push", `{"resultIds":[1]}`, enAnalyst, csrfOf(analyst))
	if msg := errOf(w); w.Code != http.StatusConflict || turkish(msg) || !strings.Contains(msg, "not configured") {
		t.Fatalf("yapılandırılmamış gönderim: %d %q", w.Code, msg)
	}
	tooMany := make([]string, mispMaxPush+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprint(i + 1)
	}
	w = doJSON(s, http.MethodPost, "/api/integrations/misp/push", `{"resultIds":[`+strings.Join(tooMany, ",")+`]}`, enAnalyst, csrfOf(analyst))
	if msg := errOf(w); w.Code != http.StatusBadRequest || msg != fmt.Sprintf("Select 1–%d findings per push", mispMaxPush) {
		t.Fatalf("aşırı büyük seçim: %d %q", w.Code, msg)
	}
}
