package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"keywordhunter-mvp/pkg/config"
	"keywordhunter-mvp/pkg/i18n"
	"keywordhunter-mvp/pkg/storage"
	"keywordhunter-mvp/pkg/totp"
)

// newTwoFAServer çok kullanıcılı test sunucusunu geçerli bir APP_SECRET
// mühürleyicisi ve verilen REQUIRE_2FA politikasıyla kurar.
func newTwoFAServer(t *testing.T, policy string) *Server {
	t.Helper()
	s := newMultiUserServer(t)
	key, err := totp.GenerateAppSecret()
	if err != nil {
		t.Fatal(err)
	}
	if s.totpSealer, err = totp.NewSealer(key); err != nil {
		t.Fatal(err)
	}
	s.require2FA = policy
	return s
}

type enrolled struct {
	secret string
	key    []byte
	codes  []string
}

func decodeJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("JSON çözülemedi (%d): %s", w.Code, w.Body.String())
	}
	return m
}

// enroll2FA oturumla kurulum + etkinleştirme yapar.
func enroll2FA(t *testing.T, s *Server, ck []*http.Cookie, pass string) enrolled {
	t.Helper()
	w := doJSON(s, http.MethodPost, "/api/me/2fa/setup", `{}`, ck, csrfOf(ck))
	if w.Code != http.StatusOK {
		t.Fatalf("kurulum: %d %s", w.Code, w.Body.String())
	}
	setup := decodeJSON(t, w)
	secret, _ := setup["secret"].(string)
	key, err := totp.DecodeSecret(secret)
	if err != nil {
		t.Fatalf("sır çözülemedi: %v", err)
	}
	w = doJSON(s, http.MethodPost, "/api/me/2fa/enable", `{"password":"`+pass+`","code":"`+totp.CodeAt(key, time.Now())+`"}`, ck, csrfOf(ck))
	if w.Code != http.StatusOK {
		t.Fatalf("etkinleştirme: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		RecoveryCodes []string `json:"recoveryCodes"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return enrolled{secret: secret, key: key, codes: resp.RecoveryCodes}
}

// nextCode etkinleştirmede/son girişte kullanılan adımdan SONRAKİ adımın kodu
// (yeniden oynatma koruması aynı adımı reddeder; +1 adım pencere içindedir).
func nextCode(e enrolled, steps int) string {
	return totp.CodeAt(e.key, time.Now().Add(time.Duration(steps)*totp.Period))
}

func cookieNamed(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, ck := range w.Result().Cookies() {
		if ck.Name == name {
			return ck
		}
	}
	return nil
}

// passwordStep parolayı gönderir ve bekleyen giriş çerezini döndürür (oturum yok).
func passwordStep(t *testing.T, s *Server, user, pass string) *http.Cookie {
	t.Helper()
	w := do(s, http.MethodPost, "/login", url.Values{"username": {user}, "password": {pass}}, nil, nil)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/login/2fa" {
		t.Fatalf("parola adımı /login/2fa'ya yönlenmeliydi: %d %s", w.Code, w.Header().Get("Location"))
	}
	if ck := cookieNamed(w, "session"); ck != nil && ck.Value != "" {
		t.Fatal("ikinci adımdan önce oturum çerezi verildi")
	}
	pc := cookieNamed(w, pendingLoginCookie)
	if pc == nil || pc.Value == "" {
		t.Fatal("bekleyen giriş çerezi yok")
	}
	return pc
}

func secondStep(s *Server, pending *http.Cookie, mode, code string) *httptest.ResponseRecorder {
	form := url.Values{"code": {code}}
	if mode != "" {
		form.Set("mode", mode)
	}
	return do(s, http.MethodPost, "/login/2fa", form, []*http.Cookie{pending}, nil)
}

func TestTwoFA_SetupAndEnableRequireValidCode(t *testing.T) {
	s := newTwoFAServer(t, config.Require2FANone)
	ck := loginAs(t, s, "admin", "adminpass1")

	w := doJSON(s, http.MethodPost, "/api/me/2fa/setup", `{}`, ck, csrfOf(ck))
	if w.Code != http.StatusOK {
		t.Fatalf("kurulum: %d %s", w.Code, w.Body.String())
	}
	setup := decodeJSON(t, w)
	secret, _ := setup["secret"].(string)
	if len(secret) != 32 {
		t.Fatalf("sır: %q", secret)
	}
	if qr, _ := setup["qrDataURI"].(string); !strings.HasPrefix(qr, "data:image/png;base64,") {
		t.Fatalf("QR data URI bekleniyordu: %.40s", qr)
	}
	uri, _ := setup["otpauthURI"].(string)
	if !strings.HasPrefix(uri, "otpauth://totp/KeywordHunter:admin?") || !strings.Contains(uri, "issuer=KeywordHunter") || !strings.Contains(uri, "secret="+secret) {
		t.Fatalf("otpauth URI: %s", uri)
	}

	// Bekleyen sır yalnız şifreli saklanır
	u, _ := s.db.GetUserByUsername("admin")
	tf, _ := s.db.GetTwoFactor(u.ID)
	if tf.Enabled || !strings.HasPrefix(tf.PendingEnc, "v1:") || strings.Contains(tf.PendingEnc, secret) {
		t.Fatalf("bekleyen kayıt: %+v", tf)
	}

	// Yanlış / biçimsiz kodlar etkinleştirmez
	key, _ := totp.DecodeSecret(secret)
	wrong := totp.CodeAt(key, time.Now().Add(-10*time.Minute))
	for _, c := range []string{wrong, "", "abcdef", strings.Repeat("1", 500)} {
		w = doJSON(s, http.MethodPost, "/api/me/2fa/enable", `{"password":"adminpass1","code":"`+c+`"}`, ck, csrfOf(ck))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("geçersiz kod %q kabul edildi: %d", c, w.Code)
		}
	}
	if on, _ := s.db.TwoFactorEnabled(u.ID); on {
		t.Fatal("geçersiz kodla 2FA etkinleşti")
	}

	// Doğru kod ama parola yok/yanlış: etkinleşmez (çalınmış oturum kendi
	// doğrulayıcısını kaydedip sahibini dışarıda bırakamaz)
	good := totp.CodeAt(key, time.Now())
	for _, pw := range []string{"", "wrong-pass"} {
		w = doJSON(s, http.MethodPost, "/api/me/2fa/enable", `{"password":"`+pw+`","code":"`+good+`"}`, ck, csrfOf(ck))
		if w.Code != http.StatusForbidden || decodeJSON(t, w)["code"] != "wrong_password" {
			t.Fatalf("parolasız etkinleştirme: %d %s", w.Code, w.Body.String())
		}
	}
	if on, _ := s.db.TwoFactorEnabled(u.ID); on {
		t.Fatal("parolasız 2FA etkinleşti")
	}
	w = doJSON(s, http.MethodPost, "/api/me/2fa/enable", `{"password":"adminpass1","code":"`+good+`"}`, ck, csrfOf(ck))
	if w.Code != http.StatusOK {
		t.Fatalf("etkinleştirme: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		RecoveryCodes []string `json:"recoveryCodes"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.RecoveryCodes) != 10 {
		t.Fatalf("10 kurtarma kodu bekleniyordu: %v", resp.RecoveryCodes)
	}
	re := regexp.MustCompile(`^[0-9a-z]{4}-[0-9a-z]{4}$`)
	for _, c := range resp.RecoveryCodes {
		if !re.MatchString(c) {
			t.Fatalf("kod biçimi: %q", c)
		}
	}

	// Etkinleştirmeden sonra sır hiçbir yanıtta yok; DB'de yalnız şifreli
	st := do(s, http.MethodGet, "/api/me/2fa", nil, ck, nil)
	page := do(s, http.MethodGet, "/account", nil, ck, nil)
	for name, body := range map[string]string{"status": st.Body.String(), "account": page.Body.String()} {
		if strings.Contains(body, secret) {
			t.Fatalf("%s yanıtı TOTP sırrını içeriyor", name)
		}
	}
	status := decodeJSON(t, st)
	if status["enabled"] != true || status["recoveryRemaining"] != float64(10) {
		t.Fatalf("durum: %v", status)
	}
	assertNoPlainSecretInDB(t, s, secret, resp.RecoveryCodes)

	// Etkinken yeni kurulum ve ikinci etkinleştirme reddedilir
	if w := doJSON(s, http.MethodPost, "/api/me/2fa/setup", `{}`, ck, csrfOf(ck)); w.Code != http.StatusConflict {
		t.Fatalf("etkin 2FA'da kurulum: %d", w.Code)
	}
	if n := countAudit(t, s, "2fa.enable"); n < 1 {
		t.Fatal("2fa.enable denetim olayı yok")
	}
}

// assertNoPlainSecretInDB 2FA tablolarının düz metin sır ya da kurtarma kodu
// içermediğini doğrular (base32 sır hiçbir sütunda geçmez).
func assertNoPlainSecretInDB(t *testing.T, s *Server, secret string, codes []string) {
	t.Helper()
	for _, q := range []string{
		`SELECT secret_enc || '|' || pending_enc FROM user_2fa`,
		`SELECT code_hash FROM user_recovery_codes`,
		`SELECT COALESCE(details, '') FROM audit_events`,
	} {
		rows, err := s.db.GetDBConn().Query(q)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				t.Fatal(err)
			}
			lv := strings.ToLower(v)
			if strings.Contains(strings.ToUpper(v), secret) {
				t.Fatalf("DB düz metin TOTP sırrı içeriyor (%s)", q)
			}
			for _, c := range codes {
				if strings.Contains(lv, c) || strings.Contains(lv, strings.ReplaceAll(c, "-", "")) {
					t.Fatalf("DB düz metin kurtarma kodu içeriyor (%s)", q)
				}
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
}

func TestTwoFA_LoginRequiresSecondStep(t *testing.T) {
	s := newTwoFAServer(t, config.Require2FANone)
	ck := loginAs(t, s, "admin", "adminpass1")
	e := enroll2FA(t, s, ck, "adminpass1")

	// Yanlış parola: 2FA'sız hesapla aynı yanıt (2FA varlığı sızmaz), bekleyen çerez yok
	w := do(s, http.MethodPost, "/login", url.Values{"username": {"admin"}, "password": {"wrong-pass"}}, nil, nil)
	if w.Header().Get("Location") != "/login?error=1" || cookieNamed(w, pendingLoginCookie) != nil {
		t.Fatalf("yanlış parola: %s", w.Header().Get("Location"))
	}

	pc := passwordStep(t, s, "admin", "adminpass1")
	if !pc.HttpOnly || pc.SameSite != http.SameSiteStrictMode || pc.Path != "/login" || pc.MaxAge <= 0 || pc.MaxAge > 300 {
		t.Fatalf("bekleyen giriş çerezi özellikleri: %+v", pc)
	}
	// Bekleyen çerez korumalı sayfalara erişim vermez
	if w := do(s, http.MethodGet, "/dashboard", nil, []*http.Cookie{pc}, nil); w.Header().Get("Location") != "/login" {
		t.Fatalf("bekleyen giriş oturum sayılmamalı: %d %s", w.Code, w.Header().Get("Location"))
	}
	// İkinci adım sayfası
	page := do(s, http.MethodGet, "/login/2fa", nil, []*http.Cookie{pc}, nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `autocomplete="one-time-code"`) || !strings.Contains(page.Body.String(), `inputmode="numeric"`) {
		t.Fatalf("2FA sayfası: %d", page.Code)
	}
	if !strings.Contains(page.Header().Get("Cache-Control"), "no-store") {
		t.Fatal("2FA sayfası önbelleğe alınmamalı")
	}

	w = secondStep(s, pc, "", nextCode(e, 1))
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/dashboard" {
		t.Fatalf("ikinci adım: %d %s", w.Code, w.Header().Get("Location"))
	}
	sess, csrf := cookieNamed(w, "session"), cookieNamed(w, "csrf_token")
	if sess == nil || sess.Value == "" || !sess.HttpOnly || csrf == nil || csrf.Value == "" {
		t.Fatal("başarılı ikinci adım normal girişle aynı çerezleri vermeli")
	}
	if cleared := cookieNamed(w, pendingLoginCookie); cleared == nil || cleared.MaxAge >= 0 {
		t.Fatal("bekleyen giriş çerezi silinmeli")
	}
	if w := do(s, http.MethodGet, "/dashboard", nil, []*http.Cookie{sess, csrf}, nil); w.Code != http.StatusOK {
		t.Fatalf("oturumla panel: %d", w.Code)
	}
	// Bekleyen giriş tek kullanımlık
	if w := secondStep(s, pc, "", nextCode(e, 1)); w.Header().Get("Location") != "/login?message=2fa_expired" {
		t.Fatalf("kullanılmış bekleyen giriş: %s", w.Header().Get("Location"))
	}
	if countAudit(t, s, "auth.2fa.success") != 1 || countAudit(t, s, "auth.2fa.challenge") != 1 {
		t.Fatal("2FA başarı/istek denetim olayları eksik")
	}
	var details string
	_ = s.db.GetDBConn().QueryRow(`SELECT details FROM audit_events WHERE action = 'auth.login.success' ORDER BY id DESC LIMIT 1`).Scan(&details)
	if !strings.Contains(details, `"mfa":"totp"`) {
		t.Fatalf("giriş olayı 2FA yöntemini taşımalı: %s", details)
	}
}

func TestTwoFA_ReplayRejected(t *testing.T) {
	s := newTwoFAServer(t, config.Require2FANone)
	e := enroll2FA(t, s, loginAs(t, s, "admin", "adminpass1"), "adminpass1")

	code := nextCode(e, 1)
	if w := secondStep(s, passwordStep(t, s, "admin", "adminpass1"), "", code); w.Header().Get("Location") != "/dashboard" {
		t.Fatalf("ilk kullanım: %s", w.Header().Get("Location"))
	}
	// Aynı kod (aynı adım) ikinci girişte reddedilir
	pc := passwordStep(t, s, "admin", "adminpass1")
	if w := secondStep(s, pc, "", code); w.Header().Get("Location") != "/login/2fa?error=1" {
		t.Fatalf("tekrar oynatılan kod kabul edildi: %s", w.Header().Get("Location"))
	}
	// Etkinleştirmede kullanılan (daha eski) adım da reddedilir
	if w := secondStep(s, pc, "", totp.CodeAt(e.key, time.Now())); w.Header().Get("Location") != "/login/2fa?error=1" {
		t.Fatalf("eski adımın kodu kabul edildi: %s", w.Header().Get("Location"))
	}
	var details string
	_ = s.db.GetDBConn().QueryRow(`SELECT details FROM audit_events WHERE action = 'auth.2fa.failure' ORDER BY id LIMIT 1`).Scan(&details)
	if !strings.Contains(details, `"reason":"replay"`) {
		t.Fatalf("yeniden oynatma nedeni kaydedilmeli: %s", details)
	}
}

func TestTwoFA_WrongCodesInvalidatePendingAndLockIP(t *testing.T) {
	s := newTwoFAServer(t, config.Require2FANone)
	e := enroll2FA(t, s, loginAs(t, s, "admin", "adminpass1"), "adminpass1")
	pc := passwordStep(t, s, "admin", "adminpass1")

	for i := 1; i <= pendingLoginMaxAttempts; i++ {
		w := secondStep(s, pc, "", "000000")
		want := "/login/2fa?error=1"
		if i == pendingLoginMaxAttempts {
			want = "/login?error=2fa_locked"
		}
		if w.Header().Get("Location") != want {
			t.Fatalf("deneme %d: %s (beklenen %s)", i, w.Header().Get("Location"), want)
		}
		if i < pendingLoginMaxAttempts {
			page := do(s, http.MethodGet, "/login/2fa?error=1", nil, []*http.Cookie{pc}, nil)
			if !strings.Contains(page.Body.String(), "<strong>"+itoa(int64(pendingLoginMaxAttempts-i))+"</strong>") {
				t.Fatalf("kalan deneme sayısı gösterilmeli (deneme %d)", i)
			}
		}
	}
	// Bekleyen giriş geçersiz: doğru kod da işe yaramaz
	if w := secondStep(s, pc, "", nextCode(e, 1)); w.Header().Get("Location") != "/login?message=2fa_expired" {
		t.Fatalf("5 hatadan sonra bekleyen giriş geçersiz olmalı: %s", w.Header().Get("Location"))
	}
	if n := countAudit(t, s, "auth.2fa.failure"); n != pendingLoginMaxAttempts {
		t.Fatalf("2FA hata olayı: %d", n)
	}
	// Kod hataları IP kilidine sayılır: parolayla yeniden başlamak kilidi aşamaz
	w := do(s, http.MethodPost, "/login", url.Values{"username": {"admin"}, "password": {"adminpass1"}}, nil, nil)
	if !strings.Contains(w.Header().Get("Location"), "error=locked") {
		t.Fatalf("IP kilitlenmeliydi: %s", w.Header().Get("Location"))
	}
}

func TestTwoFA_LoginGuardAppliesToSecondStep(t *testing.T) {
	s := newTwoFAServer(t, config.Require2FANone)
	e := enroll2FA(t, s, loginAs(t, s, "admin", "adminpass1"), "adminpass1")
	pc := passwordStep(t, s, "admin", "adminpass1")
	// IP başka yollarla (ör. diğer hesaplara parola denemeleri) kilitlenmiş
	for i := 0; i < loginMaxFailures; i++ {
		s.loginGuard.Fail("192.0.2.1")
	}
	w := secondStep(s, pc, "", nextCode(e, 1))
	if !strings.HasPrefix(w.Header().Get("Location"), "/login/2fa?error=locked&wait=") || w.Header().Get("Retry-After") == "" {
		t.Fatalf("kilitli IP ikinci adımı geçememeli: %s", w.Header().Get("Location"))
	}
	if cookieNamed(w, "session") != nil {
		t.Fatal("kilitliyken oturum açıldı")
	}
}

func TestTwoFA_RecoveryCodeWorksOnce(t *testing.T) {
	s := newTwoFAServer(t, config.Require2FANone)
	e := enroll2FA(t, s, loginAs(t, s, "admin", "adminpass1"), "adminpass1")
	code := strings.ToUpper(e.codes[3]) // büyük harf/biçim normalize edilir

	w := secondStep(s, passwordStep(t, s, "admin", "adminpass1"), "recovery", code)
	if w.Header().Get("Location") != "/account?notice=recovery_used#security-2fa" || cookieNamed(w, "session") == nil {
		t.Fatalf("kurtarma koduyla giriş: %s", w.Header().Get("Location"))
	}
	// Hesabım nedenini ve kalan kodu söyler
	sessCk := w.Result().Cookies()
	page := do(s, http.MethodGet, "/account?notice=recovery_used", nil, sessCk, nil)
	if !strings.Contains(page.Body.String(), "tfa-used") || !strings.Contains(page.Body.String(), "<b>9/10</b>") {
		t.Fatalf("kurtarma kodu bilgi notu gösterilmeli: %d", page.Code)
	}
	if page := do(s, http.MethodGet, "/account", nil, sessCk, nil); strings.Contains(page.Body.String(), "tfa-used") {
		t.Fatal("bilgi notu yalnız kurtarma girişinden sonra görünmeli")
	}
	w = secondStep(s, passwordStep(t, s, "admin", "adminpass1"), "recovery", code)
	if w.Header().Get("Location") != "/login/2fa?mode=recovery&error=1" {
		t.Fatalf("kurtarma kodu ikinci kez kabul edildi: %s", w.Header().Get("Location"))
	}
	u, _ := s.db.GetUserByUsername("admin")
	if n, _ := s.db.CountRecoveryCodes(u.ID); n != 9 {
		t.Fatalf("kalan kod: %d", n)
	}
	// Kurtarma modunda TOTP kodu kurtarma kodu sayılmaz
	if w := secondStep(s, passwordStep(t, s, "admin", "adminpass1"), "recovery", nextCode(e, 1)); w.Header().Get("Location") != "/login/2fa?mode=recovery&error=1" {
		t.Fatalf("TOTP kurtarma kodu yerine geçmemeli: %s", w.Header().Get("Location"))
	}
}

func TestTwoFA_PendingExpiryAndCancel(t *testing.T) {
	s := newTwoFAServer(t, config.Require2FANone)
	e := enroll2FA(t, s, loginAs(t, s, "admin", "adminpass1"), "adminpass1")
	pc := passwordStep(t, s, "admin", "adminpass1")

	base := time.Now()
	s.pendingLogins.now = func() time.Time { return base.Add(pendingLoginTTL + time.Second) }
	if w := do(s, http.MethodGet, "/login/2fa", nil, []*http.Cookie{pc}, nil); w.Header().Get("Location") != "/login?message=2fa_expired" {
		t.Fatalf("süresi dolan sayfa: %s", w.Header().Get("Location"))
	}
	if w := secondStep(s, pc, "", nextCode(e, 1)); w.Header().Get("Location") != "/login?message=2fa_expired" || cookieNamed(w, "session") != nil {
		t.Fatalf("süresi dolan bekleyen giriş kabul edildi: %s", w.Header().Get("Location"))
	}
	s.pendingLogins.now = time.Now

	// İptal bekleyen girişi siler
	pc = passwordStep(t, s, "admin", "adminpass1")
	if w := do(s, http.MethodGet, "/login/2fa/cancel", nil, []*http.Cookie{pc}, nil); w.Header().Get("Location") != "/login" {
		t.Fatalf("iptal: %s", w.Header().Get("Location"))
	}
	if w := secondStep(s, pc, "", nextCode(e, 1)); w.Header().Get("Location") != "/login?message=2fa_expired" {
		t.Fatalf("iptal edilen bekleyen giriş kabul edildi: %s", w.Header().Get("Location"))
	}
	// Periyodik temizlik süresi dolanları atar
	_ = passwordStep(t, s, "admin", "adminpass1")
	s.pendingLogins.now = func() time.Time { return time.Now().Add(time.Hour) }
	s.pendingLogins.cleanup()
	if n, u := len(s.pendingLogins.m), len(s.pendingLogins.byUser); n != 0 || u != 0 {
		t.Fatalf("temizlik sonrası kalan: %d / %d", n, u)
	}
	// Sahte/bilinmeyen çerez
	if w := do(s, http.MethodGet, "/login/2fa", nil, []*http.Cookie{{Name: pendingLoginCookie, Value: "deadbeef"}}, nil); w.Header().Get("Location") != "/login?message=2fa_expired" {
		t.Fatalf("bilinmeyen bekleyen giriş: %s", w.Header().Get("Location"))
	}
}

func TestTwoFA_DisableAndRegenerate(t *testing.T) {
	s := newTwoFAServer(t, config.Require2FANone)
	ck := loginAs(t, s, "admin", "adminpass1")
	e := enroll2FA(t, s, ck, "adminpass1")
	post := func(path, body string) *httptest.ResponseRecorder {
		return doJSON(s, http.MethodPost, path, body, ck, csrfOf(ck))
	}

	// Yenileme: yanlış parola / yanlış kod 403 (hangisi olduğu söylenmez)
	if w := post("/api/me/2fa/recovery-codes", `{"password":"nope","code":"`+nextCode(e, 1)+`"}`); w.Code != http.StatusForbidden {
		t.Fatalf("yanlış parola: %d", w.Code)
	}
	if w := post("/api/me/2fa/recovery-codes", `{"password":"adminpass1","code":"000000"}`); w.Code != http.StatusForbidden {
		t.Fatalf("yanlış kod: %d", w.Code)
	}
	w := post("/api/me/2fa/recovery-codes", `{"password":"adminpass1","code":"`+nextCode(e, 1)+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("yenileme: %d %s", w.Code, w.Body.String())
	}
	var regen struct {
		RecoveryCodes []string `json:"recoveryCodes"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &regen)
	if len(regen.RecoveryCodes) != 10 {
		t.Fatal("10 yeni kod bekleniyordu")
	}
	// Eski kodlar geçersiz, yenisi çalışır (kapatmada ikinci faktör olarak)
	if w := post("/api/me/2fa/disable", `{"password":"adminpass1","code":"`+e.codes[0]+`"}`); w.Code != http.StatusForbidden {
		t.Fatalf("eski kurtarma kodu yenilemeden sonra çalışmamalı: %d", w.Code)
	}
	if w := post("/api/me/2fa/disable", `{"password":"adminpass1","code":"`+regen.RecoveryCodes[0]+`"}`); w.Code != http.StatusOK {
		t.Fatalf("kapatma: %d %s", w.Code, w.Body.String())
	}
	if countAudit(t, s, "2fa.disable") < 1 || countAudit(t, s, "2fa.recovery.regenerate") < 1 {
		t.Fatal("kapatma/yenileme denetim olayları eksik")
	}
	// Artık parola yeterli; kurtarma kodları da silindi
	if w := do(s, http.MethodPost, "/login", url.Values{"username": {"admin"}, "password": {"adminpass1"}}, nil, nil); w.Header().Get("Location") != "/dashboard" {
		t.Fatalf("2FA kapalıyken giriş: %s", w.Header().Get("Location"))
	}
	u, _ := s.db.GetUserByUsername("admin")
	if n, _ := s.db.CountRecoveryCodes(u.ID); n != 0 {
		t.Fatalf("kapatmadan sonra kod kaldı: %d", n)
	}
	if w := post("/api/me/2fa/disable", `{"password":"adminpass1","code":"123456"}`); w.Code != http.StatusConflict {
		t.Fatalf("kapalı 2FA'yı kapatma: %d", w.Code)
	}
}

func TestTwoFA_AdminReset(t *testing.T) {
	s := newTwoFAServer(t, config.Require2FANone)
	admin := loginAs(t, s, "admin", "adminpass1")
	analyst := mkTokenUser(t, s, "analyst2fa", "analystpass1", storage.RoleAnalyst)
	enroll2FA(t, s, analyst, "analystpass1")
	enroll2FA(t, s, admin, "adminpass1")
	an, _ := s.db.GetUserByUsername("analyst2fa")
	ad, _ := s.db.GetUserByUsername("admin")

	// Liste 2FA durumunu taşır (sır yok)
	w := do(s, http.MethodGet, "/api/users", nil, admin, nil)
	if !strings.Contains(w.Body.String(), `"twoFactor":{"enabled":true`) || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("kullanıcı listesi 2FA durumu: %s", w.Body.String())
	}
	// Admin olmayan sıfırlayamaz
	if w := doJSON(s, http.MethodPost, "/api/users/"+itoa(ad.ID)+"/2fa/reset", `{}`, analyst, csrfOf(analyst)); w.Code != http.StatusForbidden {
		t.Fatalf("analist sıfırlama: %d", w.Code)
	}
	// Admin kendi 2FA'sını bu uçla sıfırlayamaz (parola + kod gerekir)
	if w := doJSON(s, http.MethodPost, "/api/users/"+itoa(ad.ID)+"/2fa/reset", `{}`, admin, csrfOf(admin)); w.Code != http.StatusBadRequest {
		t.Fatalf("kendi 2FA'sını sıfırlama: %d", w.Code)
	}
	w = doJSON(s, http.MethodPost, "/api/users/"+itoa(an.ID)+"/2fa/reset", `{}`, admin, csrfOf(admin))
	if w.Code != http.StatusOK || decodeJSON(t, w)["changed"] != true {
		t.Fatalf("sıfırlama: %d %s", w.Code, w.Body.String())
	}
	if on, _ := s.db.TwoFactorEnabled(an.ID); on {
		t.Fatal("sıfırlamadan sonra 2FA etkin kaldı")
	}
	// Hedefin oturumları düştü; artık yalnız parolayla girer
	if w := do(s, http.MethodGet, "/api/whoami", nil, analyst, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("sıfırlanan kullanıcının oturumu düşmeliydi: %d", w.Code)
	}
	loginAs(t, s, "analyst2fa", "analystpass1")
	if countAudit(t, s, "2fa.reset_by_admin") != 2 { // self reddi (başarısız) + başarılı sıfırlama
		t.Fatalf("2fa.reset_by_admin olayları: %d", countAudit(t, s, "2fa.reset_by_admin"))
	}
	// İkinci sıfırlama değişiklik yapmaz
	if w := doJSON(s, http.MethodPost, "/api/users/"+itoa(an.ID)+"/2fa/reset", `{}`, admin, csrfOf(admin)); decodeJSON(t, w)["changed"] != false {
		t.Fatal("ikinci sıfırlama changed=false dönmeli")
	}
}

// REQUIRE_2FA=all: kayıtsız kullanıcı yalnız Hesabım + kayıt uçlarına erişir;
// token'ları reddedilir. Kayıttan sonra her şey normal.
func TestTwoFA_RequireAllEnforcement(t *testing.T) {
	s := newTwoFAServer(t, config.Require2FAAll)
	ck := loginAs(t, s, "admin", "adminpass1")
	u, _ := s.db.GetUserByUsername("admin")
	tok, _, err := s.db.CreateAPIToken(u.ID, u.Username, "siem", storage.TokenScopeRead, nil)
	if err != nil {
		t.Fatal(err)
	}
	bearer := map[string]string{"Authorization": "Bearer " + tok}

	for _, p := range []string{"/dashboard", "/results", "/users", "/settings"} {
		w := do(s, http.MethodGet, p, nil, ck, nil)
		if w.Code != http.StatusFound || w.Header().Get("Location") != "/account#security-2fa" {
			t.Fatalf("%s kayıtsızken Hesabım'a yönlenmeli: %d %s", p, w.Code, w.Header().Get("Location"))
		}
	}
	page := do(s, http.MethodGet, "/account", nil, ck, nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "acc-need2fa") || !strings.Contains(page.Body.String(), `data-need2fa="1"`) {
		t.Fatalf("Hesabım uyarı ile açılmalı: %d", page.Code)
	}
	if w := do(s, http.MethodGet, "/static/js/app.js", nil, ck, nil); w.Code != http.StatusOK {
		t.Fatalf("statik dosyalar etkilenmemeli: %d", w.Code)
	}
	for _, c := range []struct{ method, path string }{{"GET", "/api/stats"}, {"GET", "/api/tokens"}, {"POST", "/api/alert-config"}} {
		var w *httptest.ResponseRecorder
		if c.method == "GET" {
			w = do(s, http.MethodGet, c.path, nil, ck, nil)
		} else {
			w = doJSON(s, http.MethodPost, c.path, `{}`, ck, csrfOf(ck))
		}
		if w.Code != http.StatusForbidden || decodeJSON(t, w)["error"] != "2FA required" {
			t.Fatalf("%s %s: %d %s", c.method, c.path, w.Code, w.Body.String())
		}
	}
	if w := do(s, http.MethodGet, "/api/me/2fa", nil, ck, nil); w.Code != http.StatusOK || decodeJSON(t, w)["required"] != true {
		t.Fatalf("durum ucu açık olmalı: %d", w.Code)
	}
	// Token: kayıt muafiyeti yok, 403
	if w := do(s, http.MethodGet, "/api/stats", nil, nil, bearer); w.Code != http.StatusForbidden || decodeJSON(t, w)["error"] != "2FA required" {
		t.Fatalf("kayıtsız kullanıcının token'ı reddedilmeli: %d %s", w.Code, w.Body.String())
	}
	if w := do(s, http.MethodGet, "/api/whoami", nil, nil, bearer); w.Code != http.StatusForbidden {
		t.Fatalf("token whoami: %d", w.Code)
	}
	if countAudit(t, s, "access.denied") == 0 {
		t.Fatal("2FA zorunluluk reddi denetime yazılmalı")
	}

	enroll2FA(t, s, ck, "adminpass1")
	if w := do(s, http.MethodGet, "/dashboard", nil, ck, nil); w.Code != http.StatusOK {
		t.Fatalf("kayıttan sonra panel: %d", w.Code)
	}
	if w := do(s, http.MethodGet, "/api/stats", nil, nil, bearer); w.Code != http.StatusOK {
		t.Fatalf("kayıttan sonra token: %d", w.Code)
	}
	if page := do(s, http.MethodGet, "/account", nil, ck, nil); strings.Contains(page.Body.String(), "acc-need2fa") {
		t.Fatal("kayıttan sonra uyarı kalmamalı")
	}
}

func TestTwoFA_RequireAdminsOnlyAffectsAdmins(t *testing.T) {
	s := newTwoFAServer(t, config.Require2FAAdmins)
	admin := loginAs(t, s, "admin", "adminpass1")
	viewer := mkTokenUser(t, s, "viewer2fa", "viewerpass1", storage.RoleViewer)
	if w := do(s, http.MethodGet, "/dashboard", nil, viewer, nil); w.Code != http.StatusOK {
		t.Fatalf("admins politikası viewer'ı etkilememeli: %d", w.Code)
	}
	if w := do(s, http.MethodGet, "/dashboard", nil, admin, nil); w.Header().Get("Location") != "/account#security-2fa" {
		t.Fatalf("admin yönlenmeliydi: %s", w.Header().Get("Location"))
	}
	// Viewer öz-hizmetle kendi 2FA'sını kurabilir (yazma kapısından muaf)
	enroll2FA(t, s, viewer, "viewerpass1")
}

// /api/me/2fa/* ve admin sıfırlaması API token ile çağrılamaz.
func TestTwoFA_TokenDenied(t *testing.T) {
	s := newTwoFAServer(t, config.Require2FANone)
	admin := loginAs(t, s, "admin", "adminpass1")
	tok := createToken(t, s, admin, "automation", storage.TokenScopeWrite, 0)
	h := map[string]string{"Authorization": "Bearer " + tok.Token, "Content-Type": "application/json"}
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/me/2fa"},
		{"POST", "/api/me/2fa/setup"},
		{"POST", "/api/me/2fa/enable"},
		{"POST", "/api/me/2fa/disable"},
		{"POST", "/api/me/2fa/recovery-codes"},
		{"POST", "/api/users/1/2fa/reset"},
	} {
		if !tokenForbiddenRoute(strings.Replace(c.path, "/users/1/", "/users/:id/", 1)) {
			t.Errorf("%s token yasak listesinde değil", c.path)
		}
		w := do(s, c.method, c.path, nil, nil, h)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "interactive session") {
			t.Fatalf("%s %s token ile: %d %s", c.method, c.path, w.Code, w.Body.String())
		}
	}
	u, _ := s.db.GetUserByUsername("admin")
	if tf, _ := s.db.GetTwoFactor(u.ID); tf.PendingEnc != "" {
		t.Fatal("token ile kurulum başlatılmamalı")
	}
}

func TestTwoFA_UnavailableWithoutAppSecret(t *testing.T) {
	s := newMultiUserServer(t) // mühürleyici yok
	ck := loginAs(t, s, "admin", "adminpass1")
	if w := doJSON(s, http.MethodPost, "/api/me/2fa/setup", `{}`, ck, csrfOf(ck)); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("APP_SECRET yokken kurulum: %d", w.Code)
	}
	s2 := newTwoFAServer(t, config.Require2FANone)
	s2.appSecretEphemeral = true
	ck2 := loginAs(t, s2, "admin", "adminpass1")
	if w := doJSON(s2, http.MethodPost, "/api/me/2fa/setup", `{}`, ck2, csrfOf(ck2)); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("geçici APP_SECRET ile kurulum: %d", w.Code)
	}
}

// Giriş sayfası sorgu dizgisindeki serbest metni yansıtmaz; bilinen kodları çevirir.
func TestLoginPage_OnlyKnownMessages(t *testing.T) {
	s := newTestServer(t)
	w := do(s, http.MethodGet, "/login?message=Hesabiniz+askiya+alindi", nil, nil, nil)
	if strings.Contains(w.Body.String(), "askiya") {
		t.Fatal("serbest ileti metni sayfaya yansıtıldı")
	}
	w = do(s, http.MethodGet, "/login?message=2fa_expired", nil, []*http.Cookie{{Name: "lang", Value: "en"}}, nil)
	if !strings.Contains(w.Body.String(), i18n.T("en", "tfa.login.expired")) {
		t.Fatal("2fa_expired iletisi gösterilmeli")
	}
}

// Yeni 2FA denetim kodları kategorili ve TR/EN etiketli olmalı (dinamik
// çağrıldıkları için kaynak taraması testi bunları yakalamayabilir).
func TestTwoFA_AuditCodesLabeled(t *testing.T) {
	for _, code := range []string{"auth.2fa.challenge", "auth.2fa.success", "auth.2fa.failure", "2fa.enable", "2fa.disable", "2fa.recovery.regenerate", "2fa.reset_by_admin"} {
		key := "audit.act." + code
		if tr, en := i18n.T("tr", key), i18n.T("en", key); tr == key || en == tr {
			t.Errorf("%s için TR/EN etiket eksik", code)
		}
		if cat := auditCategory(code); cat != "auth" && cat != "user" {
			t.Errorf("%s kategorisi: %s", code, cat)
		}
	}
}

func TestPendingLogins_CapAndSingleActive(t *testing.T) {
	p := newPendingLogins()
	a, _ := p.create(1, "u1", "admin")
	b, _ := p.create(1, "u1", "admin")
	if _, ok := p.get(a); ok {
		t.Fatal("aynı kullanıcının önceki bekleyen girişi düşmeli")
	}
	if _, ok := p.get(b); !ok {
		t.Fatal("son bekleyen giriş geçerli olmalı")
	}
	for i := int64(2); i < pendingLoginMax+50; i++ {
		if _, err := p.create(i, "u", "viewer"); err != nil {
			t.Fatal(err)
		}
	}
	if len(p.m) > pendingLoginMax || len(p.byUser) != len(p.m) {
		t.Fatalf("üst sınır aşıldı ya da dizin tutarsız: %d / %d", len(p.m), len(p.byUser))
	}
	p.removeUser(5000)
	p.cleanup()
	if _, ok := p.byUser[5000]; ok {
		t.Fatal("removeUser dizini temizlemeli")
	}
	if _, ok := p.get(strings.Repeat("a", 500)); ok {
		t.Fatal("aşırı uzun kimlik")
	}
}

// Eşzamanlı hatalı kodlar bekleyen girişin 5 deneme sınırını ve IP kilidini
// aşamamalı; sınır dolduktan sonra gelen doğru kod oturum açmamalı.
func TestTwoFA_ConcurrentWrongCodesCapped(t *testing.T) {
	s := newTwoFAServer(t, config.Require2FANone)
	e := enroll2FA(t, s, loginAs(t, s, "admin", "adminpass1"), "adminpass1")
	pc := passwordStep(t, s, "admin", "adminpass1")

	const n = 40
	var wg sync.WaitGroup
	start := make(chan struct{})
	sessions := int32(0)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if w := secondStep(s, pc, "", "000000"); cookieNamed(w, "session") != nil {
				atomic.AddInt32(&sessions, 1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if sessions != 0 {
		t.Fatal("hatalı kodla oturum açıldı")
	}
	if got := countAudit(t, s, "auth.2fa.failure"); got > pendingLoginMaxAttempts {
		t.Fatalf("bir bekleyen giriş için %d kod doğrulandı (sınır %d)", got, pendingLoginMaxAttempts)
	}
	// Sınır dolduktan sonra doğru kod işe yaramaz
	w := secondStep(s, pc, "", nextCode(e, 1))
	if cookieNamed(w, "session") != nil || strings.HasPrefix(w.Header().Get("Location"), "/dashboard") {
		t.Fatalf("sınırdan sonra doğru kod oturum açtı: %s", w.Header().Get("Location"))
	}
}

// Eşzamanlı yeniden doğrulama (kapatma) denemeleri IP kilidini aşamamalı.
func TestTwoFA_ConcurrentReauthCapped(t *testing.T) {
	s := newTwoFAServer(t, config.Require2FANone)
	ck := loginAs(t, s, "admin", "adminpass1")
	e := enroll2FA(t, s, ck, "adminpass1")

	const n = 30
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			doJSON(s, http.MethodPost, "/api/me/2fa/disable", `{"password":"adminpass1","code":"000000"}`, ck, csrfOf(ck))
		}()
	}
	close(start)
	wg.Wait()
	failed := 0
	for _, ev := range auditEvents(t, s) {
		if ev.Action == "2fa.disable" && !ev.Success {
			failed++
		}
	}
	if failed > loginMaxFailures {
		t.Fatalf("%d parola+kod denemesi doğrulandı (sınır %d)", failed, loginMaxFailures)
	}
	// IP artık kilitli: doğru kimlik bilgileri de 429 alır
	w := doJSON(s, http.MethodPost, "/api/me/2fa/disable", `{"password":"adminpass1","code":"`+nextCode(e, 1)+`"}`, ck, csrfOf(ck))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("kilitli IP: %d %s", w.Code, w.Body.String())
	}
}

func TestPendingLogins_ReserveAndTake(t *testing.T) {
	p := newPendingLogins()
	id, _ := p.create(7, "u7", "viewer")
	for i := 0; i < pendingLoginMaxAttempts; i++ {
		if _, found, ok := p.reserve(id); !found || !ok {
			t.Fatalf("deneme %d ayrılamadı", i+1)
		}
	}
	if _, found, ok := p.reserve(id); !found || ok {
		t.Fatal("hak dolunca ayırma reddedilmeli")
	}
	// Uçuştaki son denemenin başarısı hâlâ tüketebilir
	if !p.take(id, 7) {
		t.Fatal("geçerli kayıt tüketilebilmeli")
	}
	if p.take(id, 7) {
		t.Fatal("kayıt iki kez tüketilemez")
	}
	// Doğrulama sürerken geçersiz kılınan kayıt tüketilemez
	id2, _ := p.create(8, "u8", "viewer")
	if _, _, ok := p.reserve(id2); !ok {
		t.Fatal("ayırma")
	}
	p.removeUser(8)
	if p.take(id2, 8) {
		t.Fatal("silinmiş bekleyen girişle oturum açılmamalı")
	}
	// Son hatalı deneme kaydı düşürür
	id3, _ := p.create(9, "u9", "viewer")
	for i := 0; i < pendingLoginMaxAttempts; i++ {
		p.reserve(id3)
	}
	if _, invalidated := p.failReserved(id3); !invalidated {
		t.Fatal("hak dolunca kayıt düşmeli")
	}
	if _, found, _ := p.reserve(id3); found {
		t.Fatal("düşen kayıt bulunmamalı")
	}
}

func TestLoginGuard_AcquireLimitsInflight(t *testing.T) {
	g := newLoginGuard()
	ip := "198.51.100.7"
	for i := 0; i < loginMaxFailures; i++ {
		if ok, _ := g.Acquire(ip); !ok {
			t.Fatalf("deneme %d ayrılamadı", i+1)
		}
	}
	if ok, _ := g.Acquire(ip); ok {
		t.Fatal("uçuşta 5 deneme varken altıncısı reddedilmeli")
	}
	for i := 0; i < loginMaxFailures; i++ {
		g.Fail(ip)
		g.Release(ip)
	}
	if ok, _ := g.Acquire(ip); ok {
		t.Fatal("5 hatadan sonra IP kilitli olmalı")
	}
	// Kilit süresi dolunca yalnız tek uçuştaki denemeye izin verilir
	base := time.Now()
	g.now = func() time.Time { return base.Add(loginBaseLock + time.Second) }
	if ok, _ := g.Acquire(ip); !ok {
		t.Fatal("kilit sonrası bir deneme ayrılabilmeli")
	}
	if ok, _ := g.Acquire(ip); ok {
		t.Fatal("kilit sonrası eşzamanlı ikinci deneme reddedilmeli")
	}
	g.Success(ip)
	g.Release(ip)
	if len(g.attempts) != 0 {
		t.Fatal("başarı + bırakma sonrası kayıt kalmamalı")
	}
	// Uçuşta değilken ayrılmış-bırakılmış IP iz bırakmaz
	g.Acquire("203.0.113.1")
	g.Release("203.0.113.1")
	if _, ok := g.attempts["203.0.113.1"]; ok {
		t.Fatal("temiz IP kaydı silinmeli")
	}
}

// REQUIRE_2FA etkinken users tablosunda olmayan .env (bootstrap) hesabı
// ikinci faktörsüz admin girişi olamaz: giriş reddedilir, eski oturumu düşer.
func TestTwoFA_BootstrapAccountBlockedByPolicy(t *testing.T) {
	for _, policy := range []string{config.Require2FAAll, config.Require2FAAdmins} {
		s := newTwoFAServer(t, policy)
		// ADMIN_USER /settings'ten değiştirilmiş: .env hesabı artık users tablosu dışında
		if _, err := s.creds.Update("root", ""); err != nil {
			t.Fatal(err)
		}
		// Var olan (politika öncesi) oturum
		if err := s.db.CreateSession("bootstrap-sess", "root", "bootstrap-csrf", time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		old := []*http.Cookie{{Name: "session", Value: "bootstrap-sess"}, {Name: "csrf_token", Value: "bootstrap-csrf"}}

		w := do(s, http.MethodPost, "/login", url.Values{"username": {"root"}, "password": {"adminpass1"}}, nil, nil)
		if w.Header().Get("Location") != "/login?error=1" || cookieNamed(w, "session") != nil {
			t.Fatalf("%s: bootstrap girişi reddedilmeli: %s", policy, w.Header().Get("Location"))
		}
		if w := do(s, http.MethodGet, "/api/users", nil, old, nil); w.Code != http.StatusForbidden || decodeJSON(t, w)["error"] != "2FA required" {
			t.Fatalf("%s: bootstrap oturumu API'de reddedilmeli: %d %s", policy, w.Code, w.Body.String())
		}
		if sess, _ := s.db.GetSession("bootstrap-sess"); sess != nil {
			t.Fatalf("%s: bootstrap oturumu silinmeli", policy)
		}
		if err := s.db.CreateSession("bootstrap-sess2", "root", "c2", time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if w := do(s, http.MethodGet, "/dashboard", nil, []*http.Cookie{{Name: "session", Value: "bootstrap-sess2"}}, nil); w.Header().Get("Location") != "/login?message=2fa_policy" {
			t.Fatalf("%s: bootstrap oturumu girişe yönlenmeli: %s", policy, w.Header().Get("Location"))
		}
		page := do(s, http.MethodGet, "/login?message=2fa_policy", nil, []*http.Cookie{{Name: "lang", Value: "en"}}, nil)
		if !strings.Contains(page.Body.String(), i18n.T("en", "tfa.login.bootstrapBlocked")) {
			t.Fatalf("%s: politika iletisi gösterilmeli", policy)
		}
	}
	// Politika yokken eski davranış korunur
	s := newTwoFAServer(t, config.Require2FANone)
	if _, err := s.creds.Update("root", ""); err != nil {
		t.Fatal(err)
	}
	loginAs(t, s, "root", "adminpass1")
}

// Kendi parolasını değiştiren kullanıcının eski parolayla başlatılmış ikinci
// adımı düşer.
func TestTwoFA_SelfPasswordChangeDropsPending(t *testing.T) {
	s := newTwoFAServer(t, config.Require2FANone)
	ck := loginAs(t, s, "admin", "adminpass1")
	e := enroll2FA(t, s, ck, "adminpass1")
	pc := passwordStep(t, s, "admin", "adminpass1")
	if w := doJSON(s, http.MethodPost, "/api/me/password", `{"current":"adminpass1","new":"adminpass2-new"}`, ck, csrfOf(ck)); w.Code != http.StatusOK {
		t.Fatalf("parola değişikliği: %d %s", w.Code, w.Body.String())
	}
	w := secondStep(s, pc, "recovery", e.codes[0])
	if cookieNamed(w, "session") != nil || w.Header().Get("Location") != "/login?message=2fa_expired" {
		t.Fatalf("eski parolayla başlatılan ikinci adım düşmeli: %s", w.Header().Get("Location"))
	}
}
