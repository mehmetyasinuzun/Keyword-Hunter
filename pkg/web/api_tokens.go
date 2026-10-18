package web

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/storage"
)

// ─────────────────────────── Kişisel API token'ları ───────────────────────────
//
// SOAR/SIEM/betikler /api/ uçlarını "Authorization: Bearer kh_…" ile çağırır.
// Kimlik yolu seçimi ve CSRF kuralı authMiddleware açıklamasındadır. Burada:
// token doğrulama, token ile çağrılamayan uçlar ve token yönetim uçları.

const (
	maxTokenNameRunes      = 64
	maxTokenExpiryDays     = 365
	maxActiveTokensPerUser = 25
	// maxInactiveTokensKept kullanıcı başına saklanan en fazla etkin olmayan
	// (iptal/süresi dolmuş) token satırı; eskileri oluşturmada budanır.
	maxInactiveTokensKept = 50
	// tokenRetention etkin olmayan token satırlarının periyodik temizlikten
	// önce saklandığı süre (geçmiş ayrıca denetim kaydındadır).
	tokenRetention = 90 * 24 * time.Hour
	// tokenTouchInterval son kullanım bilgisini her istekte yazmamak için.
	tokenTouchInterval = time.Minute
)

// tokenForbiddenRoutes API token ile ASLA çağrılamayan hesap/güvenlik yönetimi
// uçları (rota kalıbı kökleri; alt yollar dahil). Sızmış bir token yeni token
// basamamalı, parola/2FA değiştirememeli, kullanıcı yönetememeli, ayar ya da
// entegrasyonları (webhook, site profilleri) değiştirememeli. Yeni bir hesap
// veya güvenlik uç noktası eklendiğinde buraya da eklenmelidir.
var tokenForbiddenRoutes = []string{
	"/api/tokens",
	"/api/me",
	// 2FA yönetimi "/api/me" kökünce de kapsanır; ayrıca yazılıdır ki kök
	// ileride daraltılırsa (ör. /api/me/profile token'a açılırsa) 2FA uçları
	// sessizce açılmasın.
	"/api/me/2fa",
	"/api/users",
	"/api/settings",
	"/api/site-profiles",
	"/api/alert-config",
	// MISP ayarları (şifreli API anahtarı) ve bağlantı testi yalnız etkileşimli
	// admin oturumuyla; gönderim (/api/integrations/misp/push) "write" kapsamlı
	// token ile otomasyona açıktır.
	"/api/integrations/misp/config",
	"/api/integrations/misp/test",
	// Yönetim uçları (veritabanı yedeği: parola özetleri ve şifreli sırlar
	// içerir) yalnız etkileşimli admin oturumuyla.
	"/api/admin",
}

// tokenForbiddenRoute rota kalıbının token ile çağrılamayacağını söyler.
func tokenForbiddenRoute(route string) bool {
	for _, p := range tokenForbiddenRoutes {
		if route == p || strings.HasPrefix(route, p+"/") {
			return true
		}
	}
	return false
}

// isAPIRoute eşleşen rota kalıbı /api/ altında mı. Ham yol yerine kalıp
// kullanılır: "/api/../dashboard" gibi yollar bir API rotasıyla eşleşmez.
func isAPIRoute(c *gin.Context) bool {
	return strings.HasPrefix(c.FullPath(), "/api/")
}

// bearerToken Authorization başlığından Bearer token'ı okur (şema büyük/küçük
// harf duyarsız). Sorgu dizgisi ya da form alanı bilerek desteklenmez: URL'ler
// loglara, geçmişe ve Referer'a sızar.
func bearerToken(c *gin.Context) (string, bool) {
	h := strings.TrimSpace(c.GetHeader("Authorization"))
	if len(h) < 7 || !strings.EqualFold(h[:7], "bearer ") {
		return "", false
	}
	tok := strings.TrimSpace(h[7:])
	return tok, tok != ""
}

// tokenAuthFailure geçersiz token denemesinin nedeni (denetim) ve istemciye
// dönen hata kodu (apierr.<Code>; ileti istek dilinde üretilir).
type tokenAuthFailure struct {
	Reason, Code string
}

// verifyAPIToken düz metin token'ı doğrular ve sahibini (güncel, etkin hesap)
// döndürür. Başarısız denemeler IP + neden başına dakikada bir denetlenir.
// Yanıt yazmaz: /api/ (Bearer, JSON) ve /taxii2/ (Basic|Bearer, TAXII hata
// nesnesi) kendi biçimleriyle yanıtlar.
func (s *Server) verifyAPIToken(c *gin.Context, raw string) (*storage.APIToken, *storage.User, *tokenAuthFailure) {
	fail := func(reason, code string, t *storage.APIToken) (*storage.APIToken, *storage.User, *tokenAuthFailure) {
		s.auditTokenFailure(c, reason, t)
		return nil, nil, &tokenAuthFailure{Reason: reason, Code: code}
	}

	if !storage.LooksLikeAPIToken(raw) {
		return fail("malformed", "token_invalid", nil)
	}
	hash := storage.HashAPIToken(raw)
	t, err := s.db.GetAPITokenByHash(hash)
	if err != nil {
		if !errors.Is(err, storage.ErrTokenNotFound) {
			logger.Warn("API token okunamadı: %v", err)
		}
		return fail("unknown", "token_invalid", nil)
	}
	// Arama özetle yapıldı; sabit-zamanlı karşılaştırma ikinci savunma hattıdır.
	if subtle.ConstantTimeCompare([]byte(t.TokenHash), []byte(hash)) != 1 {
		return fail("unknown", "token_invalid", nil)
	}
	switch t.Status(time.Now()) {
	case storage.TokenStatusRevoked:
		return fail("revoked", "token_revoked", t)
	case storage.TokenStatusExpired:
		return fail("expired", "token_expired", t)
	}
	// Sahip hâlâ var, etkin ve AYNI hesap olmalı (silinip aynı adla yeniden
	// açılan hesap eski token'ı devralmaz). Rol her istekte tazelenir.
	u, err := s.db.GetUserByUsername(t.Username)
	if err != nil || u == nil || u.ID != t.UserID || !u.Enabled {
		return fail("user_unavailable", "token_invalid", t)
	}
	return t, u, nil
}

// auditTokenFailure geçersiz token denemesini IP + neden başına dakikada bir
// kaydeder: rastgele token yağmuru denetim tablosunu şişiremez.
func (s *Server) auditTokenFailure(c *gin.Context, reason string, t *storage.APIToken) {
	if !s.auditNoise.allow("token-fail:"+c.ClientIP()+":"+reason, time.Minute) {
		return
	}
	details := map[string]any{"reason": reason, "method": c.Request.Method}
	target := c.FullPath()
	if t != nil {
		details["prefix"] = t.Prefix
		details["tokenId"] = t.ID
		target = "user:" + t.Username
	}
	s.recordAudit(c, storage.AuditEvent{Action: "auth.token.failure", Target: target, AuthMethod: storage.AuthMethodAnonymous}, details)
}

// setTokenContext doğrulanmış token'ın kimliğini istek bağlamına yazar.
// "read" (ve "metrics") kapsamı rol ne olursa olsun viewer'a indirgenir
// (yazma yok); "metrics" ayrıca /api/ ve /taxii2/'de tümden reddedilir.
func setTokenContext(c *gin.Context, t *storage.APIToken, u *storage.User) {
	role := u.Role
	if t.Scope != storage.TokenScopeWrite {
		role = storage.RoleViewer
	}
	c.Set("username", u.Username)
	c.Set("role", role)
	c.Set("authMethod", storage.AuthMethodToken)
	c.Set("tokenID", t.ID)
	c.Set("tokenPrefix", t.Prefix)
	c.Set("tokenScope", t.Scope)
}

// touchAPIToken son kullanım bilgisini (en fazla dakikada bir) günceller.
func (s *Server) touchAPIToken(c *gin.Context, t *storage.APIToken) {
	now := time.Now()
	if t.LastUsedAt == nil || now.Sub(*t.LastUsedAt) >= tokenTouchInterval {
		if err := s.db.TouchAPIToken(t.ID, c.ClientIP(), now); err != nil {
			logger.Warn("API token touch hatası: %v", err)
		}
	}
}

// authenticateToken Bearer token'ı doğrular, bağlamı kurar ve zinciri sürdürür
// ya da 401/403 ile keser. Yalnız çerezsiz /api/ isteklerinde çağrılır.
func (s *Server) authenticateToken(c *gin.Context, raw string) {
	t, u, failure := s.verifyAPIToken(c, raw)
	if failure != nil {
		c.Header("WWW-Authenticate", `Bearer realm="KeywordHunter", error="invalid_token"`)
		apiAbort(c, http.StatusUnauthorized, failure.Code)
		return
	}
	setTokenContext(c, t, u)

	// "metrics" kapsamlı token yalnız /metrics içindir: kazıma sunucusundan
	// sızan bir kimlik bulgu/IOC okuyamaz.
	if t.Scope == storage.TokenScopeMetrics {
		s.auditDenied(c, "token_scope", "read")
		apiAbort(c, http.StatusForbidden, "token_metrics_only")
		return
	}
	if tokenForbiddenRoute(c.FullPath()) {
		s.auditDenied(c, "token_session_required", "")
		apiAbort(c, http.StatusForbidden, "session_required")
		return
	}
	s.touchAPIToken(c, t)
	c.Next()
}

// tokenView API yanıtındaki token (hesaplanan durum ile; özet ve düz metin yok).
// Mine sahipliği hesap kimliğiyle (user_id) bildirir: istemci kullanıcı
// adlarını kendi harf katlama kuralıyla karşılaştırmak zorunda kalmaz.
type tokenView struct {
	storage.APIToken
	Status string `json:"status"`
	Mine   bool   `json:"mine"`
}

func viewToken(t storage.APIToken, now time.Time, myID int64) tokenView {
	return tokenView{APIToken: t, Status: t.Status(now), Mine: myID > 0 && t.UserID == myID}
}

// sessionUserID oturum kullanıcısının hesap kimliği (users tablosunda yoksa 0:
// .env bootstrap hesabı). Sahiplik kararları ada göre değil buna göre verilir:
// SQLite NOCASE yalnız ASCII katlar, Go'nun EqualFold'u Unicode katlar; adla
// karşılaştırma "Şule" ile "şule" gibi iki ayrı hesabı aynı sayardı.
func (s *Server) sessionUserID(c *gin.Context) int64 {
	u, err := s.db.GetUserByUsername(c.GetString("username"))
	if err != nil || u == nil {
		return 0
	}
	return u.ID
}

// handleTokensList kendi token'larını döndürür; admin ?all=1 ile herkesinkini.
func (s *Server) handleTokensList(c *gin.Context) {
	me := c.GetString("username")
	role := c.GetString("role")
	all := c.Query("all") == "1"
	if all && role != storage.RoleAdmin {
		s.auditDenied(c, "role", storage.RoleAdmin)
		apiError(c, http.StatusForbidden, "tokens_all_admin_only")
		return
	}
	owner := me
	if all {
		owner = ""
	}
	list, err := s.db.ListAPITokens(owner)
	if err != nil {
		respondInternalError(c, "ListAPITokens", err)
		return
	}
	now := time.Now()
	myID := s.sessionUserID(c)
	out := make([]tokenView, 0, len(list))
	for _, t := range list {
		out = append(out, viewToken(t, now, myID))
	}
	c.JSON(http.StatusOK, gin.H{
		"tokens":   out,
		"me":       me,
		"all":      all,
		"canWrite": roleRank(role) >= roleRank(storage.RoleAnalyst),
		"max":      maxActiveTokensPerUser,
	})
}

// validTokenName ad 1–64 karakter, kontrol karakteri içermez.
func validTokenName(name string) bool {
	n := utf8.RuneCountInString(name)
	if n < 1 || n > maxTokenNameRunes || !utf8.ValidString(name) {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// handleTokenCreate yeni token üretir; düz metin yalnız bu yanıtta bir kez döner.
func (s *Server) handleTokenCreate(c *gin.Context) {
	var req struct {
		Name          string `json:"name"`
		Scope         string `json:"scope"`
		ExpiresInDays int    `json:"expiresInDays"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiFail(c, http.StatusBadRequest, "invalid_request")
		return
	}
	name := strings.TrimSpace(req.Name)
	if !validTokenName(name) {
		apiFail(c, http.StatusBadRequest, "token_name_length", maxTokenNameRunes)
		return
	}
	if !storage.ValidTokenScope(req.Scope) {
		apiFail(c, http.StatusBadRequest, "token_scope_invalid")
		return
	}
	if req.ExpiresInDays < 0 || req.ExpiresInDays > maxTokenExpiryDays {
		apiFail(c, http.StatusBadRequest, "token_expiry_range", maxTokenExpiryDays)
		return
	}
	me := c.GetString("username")
	u, err := s.db.GetUserByUsername(me)
	if err != nil || u == nil {
		// Bootstrap kimlik deposu (users tablosu dışı) hesabına token bağlanamaz.
		apiFail(c, http.StatusBadRequest, "token_managed_only")
		return
	}
	if req.Scope == storage.TokenScopeWrite && roleRank(u.Role) < roleRank(storage.RoleAnalyst) {
		apiFail(c, http.StatusBadRequest, "token_viewer_read_only")
		return
	}
	// Kazıma token'ı yalnız admin'e: /metrics zaten admin sahibi ister;
	// başka rol bu kapsamla işe yaramaz bir token biriktirmesin.
	if req.Scope == storage.TokenScopeMetrics && u.Role != storage.RoleAdmin {
		apiFail(c, http.StatusBadRequest, "token_metrics_admin_only")
		return
	}
	now := time.Now()
	var expiresAt *time.Time
	if req.ExpiresInDays > 0 {
		e := now.Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)
		expiresAt = &e
	}
	// Sınır denetimi ve ekleme tek SQL ifadesidir (eşzamanlı isteklerle aşılamaz).
	plain, t, err := s.db.CreateAPITokenLimited(u.ID, u.Username, name, req.Scope, expiresAt, maxActiveTokensPerUser)
	if errors.Is(err, storage.ErrTokenLimit) {
		s.audit(c, "token.create", "user:"+u.Username, map[string]any{"error": "limit", "max": maxActiveTokensPerUser}, false)
		apiFail(c, http.StatusConflict, "token_limit", maxActiveTokensPerUser)
		return
	}
	if err != nil {
		s.audit(c, "token.create", "user:"+u.Username, map[string]any{"error": "internal", "scope": req.Scope}, false)
		respondInternalError(c, "CreateAPIToken", err)
		return
	}
	// Oluştur-iptal et döngüsü tabloyu sınırsız büyütmesin.
	if _, err := s.db.PruneUserInactiveAPITokens(u.ID, maxInactiveTokensKept, now); err != nil {
		logger.Warn("API token budama hatası: %v", err)
	}
	logger.Info("API TOKEN CREATED: %s… (kapsam=%s) — %s", t.Prefix, t.Scope, u.Username)
	s.audit(c, "token.create", "token:"+strconv.FormatInt(t.ID, 10), map[string]any{
		"owner": t.Username, "prefix": t.Prefix, "name": t.Name, "scope": t.Scope, "expiresInDays": req.ExpiresInDays,
	}, true)
	c.JSON(http.StatusCreated, gin.H{"success": true, "token": plain, "meta": viewToken(*t, now, u.ID)})
}

// handleTokenRevoke token'ı iptal eder (sahibi ya da admin).
func (s *Server) handleTokenRevoke(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		apiFail(c, http.StatusBadRequest, "invalid_id")
		return
	}
	target := "token:" + strconv.FormatInt(id, 10)
	t, err := s.db.GetAPIToken(id)
	if err != nil {
		if errors.Is(err, storage.ErrTokenNotFound) {
			apiFail(c, http.StatusNotFound, "token_not_found")
			return
		}
		respondInternalError(c, "GetAPIToken", err)
		return
	}
	me := c.GetString("username")
	// Sahiplik hesap kimliğiyle belirlenir (bkz. sessionUserID), adla değil.
	myID := s.sessionUserID(c)
	owner := myID > 0 && t.UserID == myID
	if !owner && c.GetString("role") != storage.RoleAdmin {
		// Başkasının token'ının varlığı sızdırılmaz: bulunamadı ile aynı yanıt.
		s.audit(c, "token.revoke", target, map[string]any{"error": "not_owner"}, false)
		apiFail(c, http.StatusNotFound, "token_not_found")
		return
	}
	changed, err := s.db.RevokeAPIToken(id)
	if err != nil {
		s.audit(c, "token.revoke", target, map[string]any{"error": "internal"}, false)
		respondInternalError(c, "RevokeAPIToken", err)
		return
	}
	if !changed {
		c.JSON(http.StatusOK, gin.H{"success": true, "alreadyRevoked": true})
		return
	}
	logger.Info("API TOKEN REVOKED: %s… (sahip=%s) — %s tarafından", t.Prefix, t.Username, me)
	s.audit(c, "token.revoke", target, map[string]any{
		"owner": t.Username, "prefix": t.Prefix, "name": t.Name, "scope": t.Scope, "byAdmin": !owner,
	}, true)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handleAccountPage "Hesabım": profil, parola değişikliği, API token'ları, 2FA.
func (s *Server) handleAccountPage(c *gin.Context) {
	me := c.GetString("username")
	profile := gin.H{"username": me, "managed": false}
	u, err := s.db.GetUserByUsername(me)
	if err != nil {
		u = nil
	}
	if u != nil {
		profile["username"] = u.Username
		profile["managed"] = true
		profile["createdAt"] = u.CreatedAt
		if u.LastLoginAt != nil {
			profile["lastLoginAt"] = *u.LastLoginAt
		}
	}
	s.render(c, http.StatusOK, "account.html", gin.H{
		"ActivePage": "account",
		"profile":    profile,
		"tokenMax":   maxActiveTokensPerUser,
		"twofa":      s.twoFAView(u, requestLang(c)),
		"need2fa":    c.GetBool("need2FA"),
		// Kurtarma koduyla girişten sonra 2FA kartında bilgi notu (yalnız sabit kod).
		"recoveryUsed": c.Query("notice") == "recovery_used" && u != nil,
	})
}
