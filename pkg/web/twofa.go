package web

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/config"
	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/storage"
	"keywordhunter-mvp/pkg/totp"
)

// ─────────────────────── İki adımlı doğrulama (TOTP) ───────────────────────
//
// Akış:
//   - Kayıt (Hesabım): POST /api/me/2fa/setup bekleyen (şifreli) bir sır yazar
//     ve QR + base32 sırrı döndürür; POST /api/me/2fa/enable doğru kodla
//     bekleyeni etkinleştirir ve 10 kurtarma kodunu BİR KEZ döndürür.
//   - Giriş: parola doğruysa ve 2FA etkinse oturum AÇILMAZ; sunucu tarafında
//     kısa ömürlü bir "bekleyen giriş" oluşturulur ve yalnız /login altına
//     kapsamlı, HttpOnly + SameSite=Strict bir çerezle ona başvurulur.
//     POST /login/2fa kodu (ya da kurtarma kodunu) doğrular ve oturumu normal
//     girişle AYNI yoldan (startSession) açar.
//   - Zorunluluk (REQUIRE_2FA): kayıtsız kullanıcı yalnız Hesabım'daki kayıt
//     akışına erişir (bkz. twoFactorGate).
//
// Uçlar oturum gerektirir: /api/me/2fa/* API token ile çağrılamaz
// (tokenForbiddenRoutes); admin sıfırlaması /api/users altındadır.

const (
	twoFAIssuer = "KeywordHunter"
	// twoFASetupTTL bekleyen kurulum sırrının ömrü (QR gösterildikten sonra
	// kodun girilmesi için süre).
	twoFASetupTTL = 15 * time.Minute

	pendingLoginCookie      = "kh_2fa"
	pendingLoginTTL         = 5 * time.Minute
	pendingLoginMaxAttempts = 5
	// pendingLoginMax bellekte tutulan bekleyen giriş üst sınırı. Kayıt yalnız
	// doğru parolayla oluşur; yine de sınırsız büyüme olmasın.
	pendingLoginMax = 10000
	// maxSecondFactorInput kod alanının kabul edilen en uzun hâli (bayt).
	maxSecondFactorInput = 64
)

// errTwoFARequired REQUIRE_2FA kapsamındaki kayıtsız kullanıcıya dönen sabit
// hata metni (istemciler bununla eşleşebilir).
const errTwoFARequired = "2FA required"

// ─────────────────────────── Bekleyen girişler ───────────────────────────

type pendingLogin struct {
	userID   int64
	username string
	role     string
	created  time.Time
	attempts int
}

// pendingLogins parolası doğrulanmış, ikinci adımı bekleyen girişler.
// Kimlikler 256 bit rastgeledir; kayıt başarı, süre dolumu, 5 hatalı deneme
// ya da iptal ile silinir. Kullanıcı başına tek bekleyen giriş tutulur.
type pendingLogins struct {
	mu     sync.Mutex
	m      map[string]*pendingLogin
	byUser map[int64]string
	now    func() time.Time
}

func newPendingLogins() *pendingLogins {
	return &pendingLogins{m: make(map[string]*pendingLogin), byUser: make(map[int64]string), now: time.Now}
}

func (p *pendingLogins) expired(pl *pendingLogin, now time.Time) bool {
	return now.Sub(pl.created) >= pendingLoginTTL
}

// deleteLocked kaydı ve kullanıcı dizinini birlikte siler (kilit tutulurken).
func (p *pendingLogins) deleteLocked(id string) {
	if pl, ok := p.m[id]; ok {
		if p.byUser[pl.userID] == id {
			delete(p.byUser, pl.userID)
		}
		delete(p.m, id)
	}
}

// create yeni bekleyen giriş oluşturur ve kimliğini döndürür. Aynı kullanıcının
// önceki bekleyen girişi düşer (tek etkin ikinci adım).
func (p *pendingLogins) create(userID int64, username, role string) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	id := hex.EncodeToString(buf)
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	if old, ok := p.byUser[userID]; ok {
		p.deleteLocked(old)
	}
	if len(p.m) >= pendingLoginMax {
		// Yalnız sınırdayken O(n): önce süresi dolanlar, yetmezse en eskisi.
		for k, v := range p.m {
			if p.expired(v, now) {
				p.deleteLocked(k)
			}
		}
		if len(p.m) >= pendingLoginMax {
			oldestK, oldestT := "", now
			for k, v := range p.m {
				if !v.created.After(oldestT) {
					oldestK, oldestT = k, v.created
				}
			}
			p.deleteLocked(oldestK)
		}
	}
	p.m[id] = &pendingLogin{userID: userID, username: username, role: role, created: now}
	p.byUser[userID] = id
	return id, nil
}

// get bekleyen girişin kopyasını döndürür; süresi dolmuşsa siler.
func (p *pendingLogins) get(id string) (pendingLogin, bool) {
	if id == "" || len(id) > 128 {
		return pendingLogin{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	pl, ok := p.m[id]
	if !ok {
		return pendingLogin{}, false
	}
	if p.expired(pl, p.now()) {
		p.deleteLocked(id)
		return pendingLogin{}, false
	}
	return *pl, true
}

// reserve doğrulamadan ÖNCE bir denemeyi atomik olarak ayırır (deneme sayacı
// hemen artar). Böylece aynı bekleyen girişe eşzamanlı istekler en fazla
// pendingLoginMaxAttempts doğrulama yaptırabilir. found=false: kayıt yok ya da
// süresi dolmuş; found=true, ok=false: hak tükendi (uçuştaki denemeler dahil).
func (p *pendingLogins) reserve(id string) (pl pendingLogin, found, ok bool) {
	if id == "" || len(id) > 128 {
		return pendingLogin{}, false, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	e, exists := p.m[id]
	if !exists {
		return pendingLogin{}, false, false
	}
	if p.expired(e, p.now()) {
		p.deleteLocked(id)
		return pendingLogin{}, false, false
	}
	if e.attempts >= pendingLoginMaxAttempts {
		return *e, true, false
	}
	e.attempts++
	return *e, true, true
}

// failReserved ayrılmış ve başarısız olan denemeyi sonuçlandırır: hak
// tükendiyse kaydı siler. Kalan hak ve geçersiz kılınıp kılınmadığını döndürür.
func (p *pendingLogins) failReserved(id string) (remaining int, invalidated bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pl, ok := p.m[id]
	if !ok {
		return 0, true
	}
	if pl.attempts >= pendingLoginMaxAttempts {
		p.deleteLocked(id)
		return 0, true
	}
	return pendingLoginMaxAttempts - pl.attempts, false
}

// take başarılı doğrulamadan sonra kaydı atomik olarak tüketir; kayıt bu arada
// silinmiş/geçersiz kılınmışsa (iptal, 5. hata, admin sıfırlaması) false döner
// ve oturum AÇILMAZ.
func (p *pendingLogins) take(id string, userID int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	pl, ok := p.m[id]
	if !ok || pl.userID != userID {
		return false
	}
	expired := p.expired(pl, p.now())
	p.deleteLocked(id)
	return !expired
}

func (p *pendingLogins) remove(id string) {
	p.mu.Lock()
	p.deleteLocked(id)
	p.mu.Unlock()
}

// removeUser kullanıcının bekleyen girişini düşürür (2FA kapatma/sıfırlama,
// parola sıfırlama, pasifleştirme, silme).
func (p *pendingLogins) removeUser(userID int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id, ok := p.byUser[userID]; ok {
		p.deleteLocked(id)
	}
}

// cleanup süresi dolan kayıtları siler (periyodik).
func (p *pendingLogins) cleanup() {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	for k, v := range p.m {
		if p.expired(v, now) {
			p.deleteLocked(k)
		}
	}
}

func (s *Server) setPendingCookie(c *gin.Context, id string) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: pendingLoginCookie, Value: id, Path: "/login",
		MaxAge: int(pendingLoginTTL / time.Second), HttpOnly: true,
		Secure: s.cookieSecure, SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) clearPendingCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: pendingLoginCookie, Value: "", Path: "/login", MaxAge: -1,
		HttpOnly: true, Secure: s.cookieSecure, SameSite: http.SameSiteStrictMode,
	})
}

// pendingFromCookie isteğin bekleyen girişini çözer.
func (s *Server) pendingFromCookie(c *gin.Context) (string, pendingLogin, bool) {
	id, err := c.Cookie(pendingLoginCookie)
	if err != nil || s.pendingLogins == nil {
		return "", pendingLogin{}, false
	}
	pl, ok := s.pendingLogins.get(id)
	return id, pl, ok
}

// ─────────────────────────── Yardımcılar ───────────────────────────

// sealContext şifreli sırrı kullanıcıya bağlayan AAD.
func sealContext(userID int64) string { return "user:" + strconv.FormatInt(userID, 10) }

// twoFactorAvailable 2FA kaydı bu sunucuda yapılabilir mi (APP_SECRET kalıcı
// ve geçerli). Kapalıysa kullanıcıya gösterilecek nedeni döndürür.
func (s *Server) twoFactorAvailable() (bool, string) {
	if s.totpSealer == nil {
		return false, "2FA bu sunucuda kullanılamıyor: APP_SECRET yapılandırılmamış (2FA is unavailable: APP_SECRET is not configured)"
	}
	if s.appSecretEphemeral {
		return false, "2FA bu sunucuda geçici olarak kullanılamıyor: APP_SECRET .env dosyasına kalıcı yazılamadı (2FA is unavailable: APP_SECRET could not be persisted)"
	}
	return true, ""
}

// twoFactorPolicyActive REQUIRE_2FA admins ya da all mı.
func (s *Server) twoFactorPolicyActive() bool {
	return s.require2FA == config.Require2FAAdmins || s.require2FA == config.Require2FAAll
}

// twoFactorRequiredFor REQUIRE_2FA politikasının bu role 2FA şart koşup koşmadığı.
func (s *Server) twoFactorRequiredFor(role string) bool {
	switch s.require2FA {
	case config.Require2FAAll:
		return true
	case config.Require2FAAdmins:
		return role == storage.RoleAdmin
	default:
		return false
	}
}

// sessionManagedUser oturum kullanıcısının users kaydı (bootstrap kimlik
// deposu hesabıysa nil).
func (s *Server) sessionManagedUser(c *gin.Context) *storage.User {
	u, err := s.db.GetUserByUsername(c.GetString("username"))
	if err != nil || u == nil {
		return nil
	}
	return u
}

// verifySecondFactor kodu kullanıcının etkin 2FA'sına karşı doğrular. mode
// "totp" ya da "recovery"; "auto" 6 haneli rakamları TOTP, diğerlerini
// kurtarma kodu sayar. TOTP başarısında adım atomik olarak ilerletilir
// (yeniden oynatma koruması); kurtarma kodu tek seferlik tüketilir.
// reason denetim kaydı içindir: bad_code | replay | unavailable | internal.
func (s *Server) verifySecondFactor(u *storage.User, tf *storage.TwoFactor, mode, code string) (usedMode string, ok bool, reason string) {
	code = strings.TrimSpace(code)
	if len(code) > maxSecondFactorInput || code == "" {
		return mode, false, "bad_code"
	}
	if mode == "auto" {
		mode = "recovery"
		if totp.ValidCodeFormat(totp.NormalizeCode(code)) {
			mode = "totp"
		}
	}
	now := time.Now()
	switch mode {
	case "recovery":
		norm := totp.NormalizeRecoveryCode(code)
		if norm == "" {
			return mode, false, "bad_code"
		}
		used, err := s.db.UseRecoveryCode(u.ID, totp.HashRecoveryCode(u.ID, norm), now)
		if err != nil {
			logger.Error("Kurtarma kodu doğrulanamadı (kullanıcı=%s): %v", u.Username, err)
			return mode, false, "internal"
		}
		if !used {
			return mode, false, "bad_code"
		}
		return mode, true, ""
	default:
		mode = "totp"
		if s.totpSealer == nil {
			return mode, false, "unavailable"
		}
		secret, err := s.totpSealer.Open(tf.SecretEnc, sealContext(u.ID))
		if err != nil {
			// APP_SECRET değişmiş/kaybolmuş olabilir: kurtarma kodu ya da admin sıfırlaması gerekir.
			logger.Error("2FA sırrı çözülemedi (kullanıcı=%s): APP_SECRET değişmiş olabilir", u.Username)
			return mode, false, "unavailable"
		}
		key, err := totp.DecodeSecret(secret)
		if err != nil {
			return mode, false, "unavailable"
		}
		// Pencere eşleşmesi son adımdan bağımsız aranır ki yeniden oynatma
		// denetim kaydında yanlış koddan ayırt edilebilsin.
		step, ok := totp.Verify(key, code, now, -1)
		if !ok {
			return mode, false, "bad_code"
		}
		if step <= tf.LastStep {
			return mode, false, "replay"
		}
		advanced, err := s.db.AdvanceTwoFactorStep(u.ID, step)
		if err != nil {
			logger.Error("2FA adımı kaydedilemedi (kullanıcı=%s): %v", u.Username, err)
			return mode, false, "internal"
		}
		if !advanced {
			return mode, false, "replay"
		}
		return mode, true, ""
	}
}

// newRecoveryCodes düz metin kodları ve kullanıcıya bağlı özetlerini üretir.
func newRecoveryCodes(userID int64) ([]string, []string, error) {
	codes, err := totp.GenerateRecoveryCodes(totp.RecoveryCodeCount)
	if err != nil {
		return nil, nil, err
	}
	hashes := make([]string, len(codes))
	for i, c := range codes {
		hashes[i] = totp.HashRecoveryCode(userID, totp.NormalizeRecoveryCode(c))
	}
	return codes, hashes, nil
}

// twoFAView Hesabım sayfası / durum ucu için 2FA özeti (sır içermez).
func (s *Server) twoFAView(c *gin.Context, u *storage.User) gin.H {
	available, reason := s.twoFactorAvailable()
	view := gin.H{
		"managed":           u != nil,
		"available":         available,
		"enabled":           false,
		"recoveryRemaining": 0,
		"recoveryTotal":     totp.RecoveryCodeCount,
		"required":          false,
		"policy":            s.require2FA,
	}
	if !available {
		view["unavailableReason"] = reason
	}
	if u == nil {
		return view
	}
	view["required"] = s.twoFactorRequiredFor(u.Role)
	tf, err := s.db.GetTwoFactor(u.ID)
	if err != nil {
		logger.Warn("2FA durumu okunamadı (kullanıcı=%s): %v", u.Username, err)
		return view
	}
	if tf.Enabled {
		view["enabled"] = true
		if tf.EnabledAt != nil {
			view["enabledAt"] = *tf.EnabledAt
		}
		if n, err := s.db.CountRecoveryCodes(u.ID); err == nil {
			view["recoveryRemaining"] = n
		}
	}
	return view
}

// ─────────────────────────── Zorunluluk ───────────────────────────

// twoFAEnrollRoutes REQUIRE_2FA kapsamındaki kayıtsız oturumun yine de
// çağırabildiği API uçları (yalnız kayıt için gerekenler + kimlik).
var twoFAEnrollRoutes = map[string]bool{
	"/api/me/2fa":        true,
	"/api/me/2fa/setup":  true,
	"/api/me/2fa/enable": true,
	"/api/whoami":        true,
}

// twoFactorGate REQUIRE_2FA politikasını uygular (authMiddleware'den sonra).
// Kayıtsız oturum: HTML sayfaları /account#security-2fa'ya yönlenir (Hesabım
// açık kalır), API çağrıları kayıt uçları dışında 403 alır. API token'ı:
// sahibi 2FA'ya tabi ve kayıtsızsa her istek 403 (token ile kayıt yapılamaz).
// users tablosunda olmayan (.env bootstrap) hesap admin sayılır ama 2FA kaydı
// yapamaz: politika etkinken oturumu sonlandırılır (girişi de handleLogin'de
// reddedilir). Aksi halde .env parolası ikinci faktörsüz gizli bir admin
// girişi olarak kalırdı.
func (s *Server) twoFactorGate() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.twoFactorPolicyActive() {
			c.Next()
			return
		}
		u, err := s.db.GetUserByUsername(c.GetString("username"))
		if errors.Is(err, sql.ErrNoRows) || (err == nil && u == nil) {
			if s.auditNoise.allow("2fa-required-bootstrap:"+strings.ToLower(c.GetString("username")), time.Minute) {
				s.auditDenied(c, "2fa_required", "bootstrap")
			}
			if sid := c.GetString("sessionID"); sid != "" {
				_ = s.db.DeleteSession(sid)
				clearAuthCookies(c, s.cookieSecure)
			}
			if isAPIRoute(c) || !isSafeMethod(c.Request.Method) {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": errTwoFARequired, "code": "2fa_required"})
				return
			}
			c.Redirect(http.StatusFound, "/login?message=2fa_policy")
			c.Abort()
			return
		}
		if err != nil {
			respondInternalError(c, "twoFactorGate", err)
			c.Abort()
			return
		}
		// Rol, token kapsamıyla indirgenmiş bağlam rolü değil hesabın gerçek rolüdür.
		if !s.twoFactorRequiredFor(u.Role) {
			c.Next()
			return
		}
		on, err := s.db.TwoFactorEnabled(u.ID)
		if err != nil {
			respondInternalError(c, "TwoFactorEnabled", err)
			c.Abort()
			return
		}
		if on {
			c.Next()
			return
		}
		c.Set("need2FA", true)
		route := c.FullPath()
		deny := func() {
			if s.auditNoise.allow("2fa-required:"+strings.ToLower(u.Username)+":"+c.GetString("authMethod"), time.Minute) {
				s.auditDenied(c, "2fa_required", "")
			}
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": errTwoFARequired, "code": "2fa_required"})
		}
		if c.GetString("authMethod") == storage.AuthMethodToken {
			deny()
			return
		}
		if twoFAEnrollRoutes[route] {
			c.Next()
			return
		}
		if isAPIRoute(c) || !isSafeMethod(c.Request.Method) {
			deny()
			return
		}
		if route == "/account" {
			c.Next()
			return
		}
		c.Redirect(http.StatusFound, "/account#security-2fa")
		c.Abort()
	}
}

// ─────────────────────────── Giriş: ikinci adım ───────────────────────────

// beginTwoFactorLogin parolası doğrulanan 2FA'lı kullanıcı için bekleyen giriş
// oluşturur ve ikinci adım sayfasına yönlendirir. Oturum AÇILMAZ ve IP'nin
// hata sayacı sıfırlanmaz: parolayı bilen biri girişi yeniden başlatarak kod
// denemelerinden doğan kilidi aşamamalı.
func (s *Server) beginTwoFactorLogin(c *gin.Context, u *storage.User) {
	id, err := s.pendingLogins.create(u.ID, u.Username, u.Role)
	if err != nil {
		logger.Error("Bekleyen giriş oluşturulamadı: %v", err)
		c.Redirect(http.StatusFound, "/login?error=1&message=session")
		return
	}
	s.recordAudit(c, storage.AuditEvent{Action: "auth.2fa.challenge", Target: "user:" + u.Username, Actor: u.Username, Role: u.Role,
		AuthMethod: storage.AuthMethodAnonymous, Success: true}, nil)
	s.setPendingCookie(c, id)
	c.Header("Cache-Control", "no-store")
	c.Redirect(http.StatusFound, "/login/2fa")
}

// handleLogin2FAPage ikinci adım (tek seferlik kod) sayfası.
func (s *Server) handleLogin2FAPage(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	_, pl, ok := s.pendingFromCookie(c)
	if !ok {
		s.clearPendingCookie(c)
		c.Redirect(http.StatusFound, "/login?message=2fa_expired")
		return
	}
	wait, _ := strconv.Atoi(c.Query("wait"))
	if wait < 0 || wait > 3600 {
		wait = 0
	}
	errKind := c.Query("error")
	switch errKind {
	case "", "1", "locked":
	default:
		errKind = "1"
	}
	expiresIn := int((pendingLoginTTL - time.Since(pl.created)) / time.Second)
	if expiresIn < 1 {
		expiresIn = 1
	}
	s.render(c, http.StatusOK, "login_2fa.html", gin.H{
		"username":  pl.username,
		"recovery":  c.Query("mode") == "recovery",
		"error":     errKind,
		"wait":      wait,
		"remaining": pendingLoginMaxAttempts - pl.attempts,
		"expiresIn": expiresIn,
	})
}

// handleLogin2FA ikinci adımı doğrular; başarıda oturumu normal girişle aynı
// yoldan açar.
func (s *Server) handleLogin2FA(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	clientIP := c.ClientIP()
	id, pl, ok := s.pendingFromCookie(c)
	if !ok {
		s.clearPendingCookie(c)
		c.Redirect(http.StatusFound, "/login?message=2fa_expired")
		return
	}
	mode := "totp"
	back := "/login/2fa?"
	if c.PostForm("mode") == "recovery" {
		mode = "recovery"
		back = "/login/2fa?mode=recovery&"
	}
	target := "user:" + pl.username
	audit := func(action string, success bool, details map[string]any) {
		s.recordAudit(c, storage.AuditEvent{Action: action, Target: target, Actor: pl.username, Role: pl.role,
			AuthMethod: storage.AuthMethodAnonymous, Success: success}, details)
	}

	// IP denemesi ve bekleyen girişin deneme hakkı doğrulamadan ÖNCE atomik
	// olarak ayrılır: eşzamanlı istekler 5 deneme sınırını ya da IP kilidini
	// aşamaz (yalnız başta bakıp sonra saymak yarışa açıktı).
	if s.loginGuard != nil {
		if ok, wait := s.loginGuard.Acquire(clientIP); !ok {
			secs := int(wait.Seconds()) + 1
			if s.auditNoise.allow("login-locked:"+clientIP, time.Minute) {
				audit("auth.login.locked", false, map[string]any{"retryAfterSec": secs, "step": "2fa"})
			}
			c.Header("Retry-After", strconv.Itoa(secs))
			c.Redirect(http.StatusFound, back+"error=locked&wait="+strconv.Itoa(secs))
			return
		}
		defer s.loginGuard.Release(clientIP)
	}
	if _, found, ok := s.pendingLogins.reserve(id); !found {
		s.clearPendingCookie(c)
		c.Redirect(http.StatusFound, "/login?message=2fa_expired")
		return
	} else if !ok {
		// Hak, uçuştaki denemelerle tükendi; sonuçları kaydı düşürecek.
		s.clearPendingCookie(c)
		c.Redirect(http.StatusFound, "/login?error=2fa_locked")
		return
	}

	u, err := s.db.GetUserByID(pl.userID)
	if err != nil || u == nil || !u.Enabled || u.Username != pl.username {
		s.pendingLogins.remove(id)
		s.clearPendingCookie(c)
		audit("auth.2fa.failure", false, map[string]any{"reason": "user_unavailable", "method": mode})
		c.Redirect(http.StatusFound, "/login?error=1")
		return
	}
	tf, err := s.db.GetTwoFactor(u.ID)
	if err != nil {
		logger.Error("2FA kaydı okunamadı (kullanıcı=%s): %v", u.Username, err)
		c.Redirect(http.StatusFound, back+"error=1")
		return
	}
	if !tf.Enabled {
		// Bu arada admin 2FA'yı sıfırladı: girişi baştan başlat.
		s.pendingLogins.remove(id)
		s.clearPendingCookie(c)
		c.Redirect(http.StatusFound, "/login?message=2fa_expired")
		return
	}

	usedMode, ok, reason := s.verifySecondFactor(u, tf, mode, c.PostForm("code"))
	if !ok {
		if s.loginGuard != nil {
			s.loginGuard.Fail(clientIP)
		}
		remaining, invalidated := s.pendingLogins.failReserved(id)
		logger.UserLogin(u.Username+" (2FA)", false, clientIP)
		audit("auth.2fa.failure", false, map[string]any{"reason": reason, "method": usedMode, "attemptsLeft": remaining})
		if invalidated {
			s.clearPendingCookie(c)
			c.Redirect(http.StatusFound, "/login?error=2fa_locked")
			return
		}
		c.Redirect(http.StatusFound, back+"error=1")
		return
	}

	s.clearPendingCookie(c)
	if !s.pendingLogins.take(id, u.ID) {
		// Doğrulama sürerken bekleyen giriş geçersiz kılındı (iptal, hak
		// tükenmesi, admin sıfırlaması/parola değişikliği): oturum açılmaz.
		audit("auth.2fa.failure", false, map[string]any{"reason": "pending_invalidated", "method": usedMode})
		c.Redirect(http.StatusFound, "/login?message=2fa_expired")
		return
	}
	details := map[string]any{"method": usedMode}
	if usedMode == "recovery" {
		if n, err := s.db.CountRecoveryCodes(u.ID); err == nil {
			details["recoveryRemaining"] = n
		}
	}
	audit("auth.2fa.success", true, details)
	if !s.startSession(c, u.Username, u.Role, map[string]any{"mfa": usedMode}) {
		return
	}
	// Kurtarma koduyla girişte kullanıcı nedenini ve kalan kodlarını görsün
	// (cihaz kaybıysa yenilemesi / yeniden kurması için).
	if usedMode == "recovery" {
		c.Redirect(http.StatusFound, "/account?notice=recovery_used#security-2fa")
		return
	}
	c.Redirect(http.StatusFound, "/dashboard")
}

// handleLogin2FACancel bekleyen girişi iptal eder. Çerez SameSite=Strict ve
// /login kapsamlıdır: siteler arası bir bağlantı onu taşıyamaz.
func (s *Server) handleLogin2FACancel(c *gin.Context) {
	if id, err := c.Cookie(pendingLoginCookie); err == nil && s.pendingLogins != nil {
		s.pendingLogins.remove(id)
	}
	s.clearPendingCookie(c)
	c.Redirect(http.StatusFound, "/login")
}

// ─────────────────────────── Öz-hizmet uçları ───────────────────────────

// handleTwoFAStatus kendi 2FA durumunu döndürür.
func (s *Server) handleTwoFAStatus(c *gin.Context) {
	c.JSON(http.StatusOK, s.twoFAView(c, s.sessionManagedUser(c)))
}

// requireManaged2FAUser 2FA uçları için users kaydını ve hizmetin kullanılabilir
// olduğunu doğrular; aksi halde yanıtı yazar ve nil döner.
func (s *Server) requireManaged2FAUser(c *gin.Context) *storage.User {
	u := s.sessionManagedUser(c)
	if u == nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "2FA yalnız kullanıcı hesapları için kullanılabilir (2FA is only available for user accounts)"})
		return nil
	}
	return u
}

// handleTwoFASetup yeni bir bekleyen sır üretir; QR + base32 sırrı döndürür.
func (s *Server) handleTwoFASetup(c *gin.Context) {
	u := s.requireManaged2FAUser(c)
	if u == nil {
		return
	}
	if ok, reason := s.twoFactorAvailable(); !ok {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": reason, "code": "unavailable"})
		return
	}
	secret, err := totp.GenerateSecret()
	if err != nil {
		respondInternalError(c, "totp.GenerateSecret", err)
		return
	}
	enc, err := s.totpSealer.Seal(secret, sealContext(u.ID))
	if err != nil {
		respondInternalError(c, "totp.Seal", err)
		return
	}
	if err := s.db.SetTwoFactorPending(u.ID, enc, time.Now()); err != nil {
		if errors.Is(err, storage.ErrTwoFactorState) {
			c.JSON(http.StatusConflict, gin.H{"success": false, "error": "2FA zaten etkin (2FA is already enabled)"})
			return
		}
		respondInternalError(c, "SetTwoFactorPending", err)
		return
	}
	uri := totp.URI(twoFAIssuer, u.Username, secret)
	qr, err := totp.QRDataURI(uri)
	if err != nil {
		respondInternalError(c, "totp.QRDataURI", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"qrDataURI":  qr,
		"secret":     secret,
		"otpauthURI": uri,
		"expiresIn":  int(twoFASetupTTL / time.Second),
	})
}

// handleTwoFAEnable bekleyen sırrı kodla doğrulayıp 2FA'yı etkinleştirir;
// kurtarma kodları yalnız bu yanıtta döner. Mevcut parola da istenir: aksi
// halde çalınmış bir oturum kendi doğrulayıcısını kaydedip (diğer oturumlar
// da düştüğü için) gerçek sahibi hesabın dışında bırakabilirdi. Yanlış parola
// IP'nin giriş kilidine sayılır; yanlış kod sayılmaz (kod, çağıranın kendi
// elindeki bekleyen sırdan gelir — kaba kuvvetle bir şey kazandırmaz).
func (s *Server) handleTwoFAEnable(c *gin.Context) {
	var req struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Geçersiz istek"})
		return
	}
	u := s.requireManaged2FAUser(c)
	if u == nil {
		return
	}
	if ok, reason := s.twoFactorAvailable(); !ok {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": reason, "code": "unavailable"})
		return
	}
	target := "user:" + u.Username
	ip := c.ClientIP()
	if !s.acquireReauth(c, ip) {
		return
	}
	defer s.loginGuard.Release(ip)
	tf, err := s.db.GetTwoFactor(u.ID)
	if err != nil {
		respondInternalError(c, "GetTwoFactor", err)
		return
	}
	if tf.Enabled {
		c.JSON(http.StatusConflict, gin.H{"success": false, "error": "2FA zaten etkin (2FA is already enabled)"})
		return
	}
	if len(req.Password) > 512 || !verifyBcrypt(u.PasswordHash, req.Password) {
		s.loginGuard.Fail(ip)
		s.audit(c, "2fa.enable", target, map[string]any{"error": "wrong_password"}, false)
		c.JSON(http.StatusForbidden, gin.H{"success": false, "error": "Mevcut parola hatalı (current password is incorrect)", "code": "wrong_password"})
		return
	}
	if tf.PendingEnc == "" || tf.PendingAt == nil || time.Since(*tf.PendingAt) > twoFASetupTTL {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Kurulum süresi doldu ya da başlatılmadı; yeniden başlatın (setup expired or not started)", "code": "setup_expired"})
		return
	}
	secret, err := s.totpSealer.Open(tf.PendingEnc, sealContext(u.ID))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Kurulum geçersiz; yeniden başlatın (setup is invalid; start again)"})
		return
	}
	key, err := totp.DecodeSecret(secret)
	if err != nil {
		respondInternalError(c, "totp.DecodeSecret", err)
		return
	}
	code := req.Code
	if len(code) > maxSecondFactorInput {
		code = ""
	}
	step, ok := totp.Verify(key, code, time.Now(), 0)
	if !ok {
		if s.auditNoise.allow("2fa-enable-fail:"+strings.ToLower(u.Username), 10*time.Second) {
			s.audit(c, "2fa.enable", target, map[string]any{"error": "bad_code"}, false)
		}
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Kod doğrulanamadı; uygulamadaki güncel kodu girin ve cihaz saatini kontrol edin (invalid code)", "code": "bad_code"})
		return
	}
	codes, hashes, err := newRecoveryCodes(u.ID)
	if err != nil {
		respondInternalError(c, "newRecoveryCodes", err)
		return
	}
	now := time.Now()
	if err := s.db.EnableTwoFactor(u.ID, tf.PendingEnc, step, hashes, now); err != nil {
		if errors.Is(err, storage.ErrTwoFactorState) {
			c.JSON(http.StatusConflict, gin.H{"success": false, "error": "Kurulum başka bir pencerede değişti; yeniden başlatın (setup changed; start again)"})
			return
		}
		s.audit(c, "2fa.enable", target, map[string]any{"error": "internal"}, false)
		respondInternalError(c, "EnableTwoFactor", err)
		return
	}
	s.loginGuard.Success(ip)
	// 2FA'yı açmak bir güvenlik sıkılaştırmasıdır: bu oturum dışındaki (belki
	// ele geçirilmiş) oturumlar düşer.
	revoked := s.revokeOtherSessions(c, u.Username)
	logger.Info("2FA ENABLED: %s", u.Username)
	s.audit(c, "2fa.enable", target, map[string]any{"recoveryCodes": len(codes), "sessionsRevoked": revoked}, true)
	c.JSON(http.StatusOK, gin.H{"success": true, "recoveryCodes": codes, "enabledAt": now.UTC()})
}

// revokeOtherSessions kullanıcının mevcut oturum dışındaki oturumlarını siler.
func (s *Server) revokeOtherSessions(c *gin.Context, username string) int64 {
	sid := c.GetString("sessionID")
	if sid == "" {
		return 0
	}
	res, err := s.db.GetDBConn().Exec(`DELETE FROM sessions WHERE username = ? COLLATE NOCASE AND id <> ?`, username, sid)
	if err != nil {
		logger.Error("SESSION REVOKE FAILED (2FA, kullanıcı=%s): %v", username, err)
		return 0
	}
	n, _ := res.RowsAffected()
	return n
}

// acquireReauth parola yeniden doğrulaması için IP denemesini atomik olarak
// ayırır (bkz. loginGuard.Acquire). Kilitliyse 429 yazar ve false döner;
// true dönerse çağıran s.loginGuard.Release(ip) çağırmalıdır.
func (s *Server) acquireReauth(c *gin.Context, ip string) bool {
	ok, wait := s.loginGuard.Acquire(ip)
	if ok {
		return true
	}
	secs := int(wait.Seconds()) + 1
	c.Header("Retry-After", strconv.Itoa(secs))
	c.JSON(http.StatusTooManyRequests, gin.H{"success": false, "error": "Çok fazla hatalı deneme; " + strconv.Itoa(secs) + " sn sonra tekrar deneyin (too many failed attempts)", "retryAfter": secs, "code": "rate_limited"})
	return false
}

// reauthTwoFA parola + ikinci faktörle yeniden kimlik doğrulama (kapatma ve
// kod yenileme). Başarısızlık IP'nin giriş kilidine sayılır: çalınmış bir
// oturumla parola/kod kaba kuvveti yapılamaz. Başarısızsa yanıtı yazar.
func (s *Server) reauthTwoFA(c *gin.Context, action string) (*storage.User, *storage.TwoFactor, bool) {
	var req struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Geçersiz istek"})
		return nil, nil, false
	}
	u := s.requireManaged2FAUser(c)
	if u == nil {
		return nil, nil, false
	}
	target := "user:" + u.Username
	ip := c.ClientIP()
	if !s.acquireReauth(c, ip) {
		return nil, nil, false
	}
	defer s.loginGuard.Release(ip)
	tf, err := s.db.GetTwoFactor(u.ID)
	if err != nil {
		respondInternalError(c, "GetTwoFactor", err)
		return nil, nil, false
	}
	if !tf.Enabled {
		c.JSON(http.StatusConflict, gin.H{"success": false, "error": "2FA etkin değil (2FA is not enabled)"})
		return nil, nil, false
	}
	// Parola her durumda (yanlış olsa da) bcrypt ile denetlenir; ikinci faktör
	// yalnız parola doğruysa denenir ki bir kurtarma kodu boşuna yanmasın.
	reason := ""
	usedMode := "auto"
	if len(req.Password) > 512 || !verifyBcrypt(u.PasswordHash, req.Password) {
		reason = "wrong_password"
	} else {
		var ok bool
		usedMode, ok, reason = s.verifySecondFactor(u, tf, "auto", req.Code)
		if ok {
			reason = ""
		}
	}
	if reason != "" {
		if reason != "internal" {
			s.loginGuard.Fail(ip)
		}
		s.audit(c, action, target, map[string]any{"error": reason}, false)
		if reason == "internal" {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Sunucu hatası"})
			return nil, nil, false
		}
		// Hangisinin yanlış olduğu söylenmez.
		c.JSON(http.StatusForbidden, gin.H{"success": false, "error": "Parola ya da doğrulama kodu hatalı (wrong password or code)", "code": "wrong_credentials"})
		return nil, nil, false
	}
	s.loginGuard.Success(ip)
	c.Set("twoFAMethod", usedMode)
	return u, tf, true
}

// handleTwoFADisable parola + kodla 2FA'yı kapatır.
func (s *Server) handleTwoFADisable(c *gin.Context) {
	u, _, ok := s.reauthTwoFA(c, "2fa.disable")
	if !ok {
		return
	}
	target := "user:" + u.Username
	if _, err := s.db.DisableTwoFactor(u.ID); err != nil {
		s.audit(c, "2fa.disable", target, map[string]any{"error": "internal"}, false)
		respondInternalError(c, "DisableTwoFactor", err)
		return
	}
	s.pendingLogins.removeUser(u.ID)
	logger.Info("2FA DISABLED: %s", u.Username)
	s.audit(c, "2fa.disable", target, map[string]any{"method": c.GetString("twoFAMethod")}, true)
	c.JSON(http.StatusOK, gin.H{"success": true, "required": s.twoFactorRequiredFor(u.Role)})
}

// handleTwoFARecoveryCodes parola + kodla kurtarma kodlarını yeniler; eskilerin
// tümü geçersiz olur. Yeni kodlar yalnız bu yanıtta döner.
func (s *Server) handleTwoFARecoveryCodes(c *gin.Context) {
	u, _, ok := s.reauthTwoFA(c, "2fa.recovery.regenerate")
	if !ok {
		return
	}
	target := "user:" + u.Username
	codes, hashes, err := newRecoveryCodes(u.ID)
	if err != nil {
		respondInternalError(c, "newRecoveryCodes", err)
		return
	}
	if err := s.db.ReplaceRecoveryCodes(u.ID, hashes, time.Now()); err != nil {
		s.audit(c, "2fa.recovery.regenerate", target, map[string]any{"error": "internal"}, false)
		if errors.Is(err, storage.ErrTwoFactorState) {
			c.JSON(http.StatusConflict, gin.H{"success": false, "error": "2FA etkin değil (2FA is not enabled)"})
			return
		}
		respondInternalError(c, "ReplaceRecoveryCodes", err)
		return
	}
	s.audit(c, "2fa.recovery.regenerate", target, map[string]any{"recoveryCodes": len(codes), "method": c.GetString("twoFAMethod")}, true)
	c.JSON(http.StatusOK, gin.H{"success": true, "recoveryCodes": codes})
}

// ─────────────────────────── Admin sıfırlaması ───────────────────────────

// handleUserTwoFAReset bir kullanıcının 2FA kaydını siler (cihaz kaybı).
// Kendi hesabına uygulanamaz: kendi 2FA'sını kapatmak parola + kod ister
// (Hesabım); aksi halde ele geçirilmiş bir admin oturumu bu uçla kendi
// ikinci faktörünü yeniden doğrulamadan kaldırabilirdi. Hedefin oturumları
// ve bekleyen girişleri düşer.
func (s *Server) handleUserTwoFAReset(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Geçersiz ID"})
		return
	}
	u, err := s.db.GetUserByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "Kullanıcı bulunamadı"})
		return
	}
	target := "user:" + u.Username
	if me := s.sessionManagedUser(c); me != nil && me.ID == u.ID {
		s.audit(c, "2fa.reset_by_admin", target, map[string]any{"error": "self"}, false)
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Kendi 2FA kaydınızı Hesabım sayfasından parola ve kodla kapatın (use My Account to disable your own 2FA)"})
		return
	}
	changed, err := s.db.DisableTwoFactor(u.ID)
	if err != nil {
		s.audit(c, "2fa.reset_by_admin", target, map[string]any{"error": "internal"}, false)
		respondInternalError(c, "DisableTwoFactor", err)
		return
	}
	s.pendingLogins.removeUser(u.ID)
	if !changed {
		c.JSON(http.StatusOK, gin.H{"success": true, "changed": false})
		return
	}
	revoked := int64(0)
	if res, err := s.db.GetDBConn().Exec(`DELETE FROM sessions WHERE username = ? COLLATE NOCASE`, u.Username); err != nil {
		logger.Error("SESSION REVOKE FAILED (2FA sıfırlama, kullanıcı=%s): %v", u.Username, err)
	} else {
		revoked, _ = res.RowsAffected()
	}
	logger.Info("2FA RESET: %s — %s tarafından", u.Username, c.GetString("username"))
	s.audit(c, "2fa.reset_by_admin", target, map[string]any{"userId": u.ID, "sessionsRevoked": revoked}, true)
	c.JSON(http.StatusOK, gin.H{"success": true, "changed": true, "sessionsRevoked": revoked})
}
