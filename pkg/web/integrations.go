package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/misp"
	"keywordhunter-mvp/pkg/storage"
)

// ─────────────────────────── MISP entegrasyonu ───────────────────────────
//
// Ayarlar yalnız admin oturumuyla okunur/yazılır (API token ile çağrılamaz,
// bkz. tokenForbiddenRoutes). API anahtarı bir sırdır: APP_SECRET'tan ayrı bir
// amaç etiketiyle türetilen AES-256-GCM anahtarıyla şifreli saklanır, hiçbir
// yanıtta, sayfada, logda ya da denetim kaydında yer almaz (yalnız "var/yok").
// Kayıtlı anahtar, adres başka bir sunucuya (origin) çevrilirken yeniden
// girilmeden kullanılamaz: ele geçirilmiş bir admin oturumu anahtarı kendi
// sunucusuna "test" ettirerek sızdıramaz.
//
// Gönderim (push) analyst+ yazma işlemidir; seçilen bulgular TEK bir MISP
// olayına dönüşür. Çıkış Tor DEĞİL, doğrudan clearnet/intranet'tir.

const (
	// mispKeyContext şifreli anahtarın AAD bağlamı (başka bir sırrın şifreli
	// metniyle yer değiştirilemez).
	mispKeyContext = "misp:api_key"
	// mispMaxPush tek gönderimdeki en fazla bulgu (Bulgular sayfasının en
	// büyük sayfa boyutu 250: "tümünü seç" tek gönderime sığar).
	mispMaxPush = 250
	// mispCallTimeout bir test/gönderim çağrısının üst sınırı (istemci kendi
	// 15 sn zaman aşımını ayrıca uygular).
	mispCallTimeout = 20 * time.Second
	// mispAutoEvery otomatik gönderim kontrol aralığı.
	mispAutoEvery = 10 * time.Minute
	// mispAutoBatch bir otomatik turda gönderilen en fazla bulgu.
	mispAutoBatch = 100
	// mispConcurrent aynı anda yürüyen en fazla MISP çağrısı.
	mispConcurrent = 2
)

var errMISPNotConfigured = misp.Errorf(nil, "MISP entegrasyonu yapılandırılmamış (Ayarlar → Entegrasyonlar)",
	"The MISP integration is not configured (Settings → Integrations)")

// mispFail MISP uçlarının hata yanıtı: iki dilli hatalar (misp.Error) arayüz
// diline göre yazılır, ileti sınırlı uzunluktadır.
func mispFail(c *gin.Context, status int, err error) {
	c.JSON(status, gin.H{"success": false, "error": truncateErr(misp.Message(err, requestLang(c)))})
}

// mispFailText iki dilli sabit bir ileti için mispFail.
func mispFailText(c *gin.Context, status int, tr, en string) {
	mispFail(c, status, misp.Errorf(nil, tr, en))
}

// mispConfigured adres ve şifreli anahtar kayıtlı mı.
func mispConfigured(cfg storage.MISPConfig) bool {
	return cfg.BaseURL != "" && cfg.APIKeyEnc != ""
}

// mispSecretStorage anahtarın şifreli saklanıp saklanamayacağı (APP_SECRET).
func (s *Server) mispSecretStorage() (bool, *misp.Error) {
	if s.secretSealer == nil {
		return false, misp.Errorf(nil, "APP_SECRET yapılandırılmamış; MISP API anahtarı şifreli saklanamaz",
			"APP_SECRET is not configured; the MISP API key cannot be stored encrypted")
	}
	if s.appSecretEphemeral {
		return false, misp.Errorf(nil, "APP_SECRET .env dosyasına kalıcı yazılamadı; yeniden başlatmada anahtar çözülemezdi",
			"APP_SECRET could not be persisted to .env; the key could not be decrypted after a restart")
	}
	return true, nil
}

// mispReady bulgular sayfasındaki "MISP'e gönder" düğmesi için.
func (s *Server) mispReady() bool {
	if ok, _ := s.mispSecretStorage(); !ok {
		return false
	}
	cfg, err := s.db.GetMISPConfig()
	return err == nil && mispConfigured(cfg)
}

// mispView ayarların istemciye dönen hali — anahtar YOK, yalnız varlığı.
func (s *Server) mispView(cfg storage.MISPConfig, lang string) gin.H {
	ok, reason := s.mispSecretStorage()
	v := gin.H{
		"baseUrl":            cfg.BaseURL,
		"hasApiKey":          cfg.APIKeyEnc != "",
		"verifyTls":          cfg.VerifyTLS,
		"distribution":       cfg.Distribution,
		"tags":               cfg.Tags,
		"allowPrivate":       cfg.AllowPrivate,
		"autoPush":           cfg.AutoPush,
		"autoMinCriticality": cfg.AutoMinCriticality,
		"configured":         mispConfigured(cfg),
		"secretStorage":      ok,
		"timeoutSeconds":     int(misp.DefaultTimeout / time.Second),
		"maxResponseKb":      misp.MaxResponseBytes / 1024,
	}
	if !ok {
		v["secretStorageReason"] = reason.Text(lang)
	}
	if cfg.UpdatedAt != nil {
		v["updatedAt"] = *cfg.UpdatedAt
		v["updatedBy"] = cfg.UpdatedBy
	}
	return v
}

// mispHost denetim hedefi için yalnız ana makine (yol/sorgu yok).
func mispHost(base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return "misp"
	}
	return "misp:" + u.Host
}

// sameOrigin iki taban adresin aynı şema + ana makineyi gösterip göstermediği.
func sameOrigin(a, b string) bool {
	ua, errA := url.Parse(a)
	ub, errB := url.Parse(b)
	return errA == nil && errB == nil && ua.Host != "" &&
		strings.EqualFold(ua.Scheme, ub.Scheme) && strings.EqualFold(ua.Host, ub.Host)
}

// mispAcquire eşzamanlı MISP çağrılarını sınırlar.
func (s *Server) mispAcquire() (func(), bool) {
	select {
	case s.mispSem <- struct{}{}:
		return func() { <-s.mispSem }, true
	default:
		return nil, false
	}
}

// mispClient kayıtlı (ya da taslak) ayarlardan istemci kurar; anahtar burada
// ve yalnız bellekte çözülür.
func (s *Server) mispClient(cfg storage.MISPConfig, plainKey string) (*misp.Client, error) {
	key := plainKey
	if key == "" {
		if cfg.APIKeyEnc == "" {
			return nil, errMISPNotConfigured
		}
		if ok, reason := s.mispSecretStorage(); !ok {
			return nil, reason
		}
		k, err := s.secretSealer.Open(cfg.APIKeyEnc, mispKeyContext)
		if err != nil {
			return nil, misp.Errorf(nil, "kayıtlı MISP API anahtarı çözülemedi (APP_SECRET değişmiş olabilir); anahtarı yeniden girin",
				"the stored MISP API key could not be decrypted (APP_SECRET may have changed); re-enter the key")
		}
		key = k
	}
	return misp.New(misp.Config{
		BaseURL: cfg.BaseURL, APIKey: key, VerifyTLS: cfg.VerifyTLS, AllowPrivate: cfg.AllowPrivate,
		Timeout: misp.DefaultTimeout, UserAgent: "KeywordHunter/" + Version,
	})
}

// handleMISPConfigGet GET /api/integrations/misp/config (admin).
func (s *Server) handleMISPConfigGet(c *gin.Context) {
	cfg, err := s.db.GetMISPConfig()
	if err != nil {
		respondInternalError(c, "GetMISPConfig", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"misp": s.mispView(cfg, requestLang(c))})
}

// mispConfigRequest kaydetme/test gövdesi; verilmeyen alan mevcut değeri korur.
type mispConfigRequest struct {
	BaseURL            *string `json:"baseUrl"`
	APIKey             string  `json:"apiKey"`
	ClearAPIKey        bool    `json:"clearApiKey"`
	VerifyTLS          *bool   `json:"verifyTls"`
	Distribution       *int    `json:"distribution"`
	Tags               *string `json:"tags"`
	AllowPrivate       *bool   `json:"allowPrivate"`
	AutoPush           *bool   `json:"autoPush"`
	AutoMinCriticality *int    `json:"autoMinCriticality"`
}

// applyMISPRequest isteği mevcut yapılandırmaya uygular ve doğrular. Dönen
// newKey, istekte yeni bir düz metin anahtar verildiyse odur.
func applyMISPRequest(cur storage.MISPConfig, req mispConfigRequest) (cfg storage.MISPConfig, newKey string, err error) {
	cfg = cur
	if req.VerifyTLS != nil {
		cfg.VerifyTLS = *req.VerifyTLS
	}
	if req.AllowPrivate != nil {
		cfg.AllowPrivate = *req.AllowPrivate
	}
	if req.Distribution != nil {
		if *req.Distribution < 0 || *req.Distribution > 3 {
			return cfg, "", misp.Errorf(nil, "dağıtım (distribution) 0–3 arasında olmalı", "distribution must be between 0 and 3")
		}
		cfg.Distribution = *req.Distribution
	}
	if req.Tags != nil {
		tags, err := misp.ParseTags(*req.Tags)
		if err != nil {
			return cfg, "", &misp.Error{TR: "etiketler geçersiz: " + misp.Message(err, "tr"), EN: "invalid tags: " + misp.Message(err, "en"), Kind: []error{err}}
		}
		cfg.Tags = strings.Join(tags, ",")
	}
	if req.AutoMinCriticality != nil {
		if *req.AutoMinCriticality < 1 || *req.AutoMinCriticality > 5 {
			return cfg, "", misp.Errorf(nil, "otomatik gönderim eşiği 1–5 arasında olmalı", "the auto-push threshold must be between 1 and 5")
		}
		cfg.AutoMinCriticality = *req.AutoMinCriticality
	}
	if req.AutoPush != nil {
		cfg.AutoPush = *req.AutoPush
	}
	if req.BaseURL != nil {
		raw := strings.TrimSpace(*req.BaseURL)
		if raw == "" {
			// Adres silinirse entegrasyon kapanır; sahipsiz anahtar tutulmaz.
			cfg.BaseURL, cfg.APIKeyEnc, cfg.AutoPush = "", "", false
		} else {
			base, err := misp.NormalizeBaseURL(raw, cfg.AllowPrivate)
			if err != nil {
				return cfg, "", err
			}
			cfg.BaseURL = base
		}
	} else if cfg.BaseURL != "" {
		// allowPrivate değiştiyse kayıtlı adres yeni kurala göre yeniden doğrulanır.
		if _, err := misp.NormalizeBaseURL(cfg.BaseURL, cfg.AllowPrivate); err != nil {
			return cfg, "", err
		}
	}
	newKey = strings.TrimSpace(req.APIKey)
	if newKey != "" && !misp.ValidAPIKey(newKey) {
		return cfg, "", misp.Errorf(nil, "API anahtarı geçersiz (boşluksuz, en fazla 256 karakter)", "invalid API key (no spaces, at most 256 characters)")
	}
	if req.ClearAPIKey && newKey == "" {
		cfg.APIKeyEnc, cfg.AutoPush = "", false
	}
	// Kayıtlı anahtar başka bir sunucuya taşınamaz: adres değiştiyse anahtar
	// yeniden girilmeli (ele geçirilmiş oturum anahtarı sızdıramasın).
	if newKey == "" && cfg.APIKeyEnc != "" && cur.BaseURL != "" && !sameOrigin(cur.BaseURL, cfg.BaseURL) {
		return cfg, "", misp.Errorf(nil, "MISP adresi değişti; güvenlik için API anahtarını yeniden girin", "the MISP URL changed; re-enter the API key for security")
	}
	if cfg.AutoPush && (cfg.BaseURL == "" || (cfg.APIKeyEnc == "" && newKey == "")) {
		return cfg, "", misp.Errorf(nil, "otomatik gönderim için MISP adresi ve API anahtarı gerekli", "auto-push requires a MISP URL and API key")
	}
	return cfg, newKey, nil
}

// handleMISPConfigSave POST /api/integrations/misp/config (admin).
func (s *Server) handleMISPConfigSave(c *gin.Context) {
	var req mispConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		mispFailText(c, http.StatusBadRequest, "Geçersiz istek", "Invalid request")
		return
	}
	cur, err := s.db.GetMISPConfig()
	if err != nil {
		respondInternalError(c, "GetMISPConfig", err)
		return
	}
	cfg, newKey, err := applyMISPRequest(cur, req)
	if err != nil {
		s.audit(c, "misp.config.update", "misp", map[string]any{"error": "validation"}, false)
		mispFail(c, http.StatusBadRequest, err)
		return
	}
	if newKey != "" {
		if ok, reason := s.mispSecretStorage(); !ok {
			mispFail(c, http.StatusConflict, reason)
			return
		}
		if cfg.BaseURL == "" {
			mispFailText(c, http.StatusBadRequest, "API anahtarından önce MISP adresini girin", "Enter the MISP URL before the API key")
			return
		}
		sealed, err := s.secretSealer.Seal(newKey, mispKeyContext)
		if err != nil {
			respondInternalError(c, "MISP key seal", err)
			return
		}
		cfg.APIKeyEnc = sealed
	}
	now := time.Now()
	switch {
	case !cfg.AutoPush:
		cfg.AutoSince = nil
	case !cur.AutoPush || cur.AutoSince == nil:
		// Yalnız bundan SONRA eklenen bulgular otomatik gönderilir (geçmiş değil).
		cfg.AutoSince = &now
	}
	if err := s.db.SaveMISPConfig(cfg, c.GetString("username"), now); err != nil {
		s.audit(c, "misp.config.update", "misp", map[string]any{"error": "internal"}, false)
		respondInternalError(c, "SaveMISPConfig", err)
		return
	}
	logger.Info("MISP AYARLARI GÜNCELLENDİ: %s — %s", mispHost(cfg.BaseURL), c.GetString("username"))
	s.audit(c, "misp.config.update", mispHost(cfg.BaseURL), map[string]any{
		"urlChanged": cur.BaseURL != cfg.BaseURL, "keyChanged": newKey != "" || cur.APIKeyEnc != cfg.APIKeyEnc,
		"configured": mispConfigured(cfg), "verifyTls": cfg.VerifyTLS, "allowPrivate": cfg.AllowPrivate,
		"distribution": cfg.Distribution, "tags": cfg.Tags,
		"autoPush": cfg.AutoPush, "autoMinCriticality": cfg.AutoMinCriticality,
	}, true)
	c.JSON(http.StatusOK, gin.H{"success": true, "misp": s.mispView(cfg, requestLang(c))})
}

// handleMISPTest POST /api/integrations/misp/test (admin) — GET
// /servers/getVersion ile bağlantıyı ve anahtarı doğrular. Gövde formdaki
// kaydedilmemiş değerleri taşıyabilir (aynı doğrulama ve anahtar kuralı).
func (s *Server) handleMISPTest(c *gin.Context) {
	var req mispConfigRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			mispFailText(c, http.StatusBadRequest, "Geçersiz istek", "Invalid request")
			return
		}
	}
	req.AutoPush, req.ClearAPIKey = nil, false // test otomatik gönderim kuralına takılmasın
	cur, err := s.db.GetMISPConfig()
	if err != nil {
		respondInternalError(c, "GetMISPConfig", err)
		return
	}
	cfg, newKey, err := applyMISPRequest(cur, req)
	if err != nil {
		s.audit(c, "misp.test", mispHost(cur.BaseURL), map[string]any{"error": "validation"}, false)
		mispFail(c, http.StatusBadRequest, err)
		return
	}
	if cfg.BaseURL == "" || (cfg.APIKeyEnc == "" && newKey == "") {
		mispFailText(c, http.StatusBadRequest, "Test için MISP adresi ve API anahtarı gerekli", "A MISP URL and API key are required to test")
		return
	}
	target := mispHost(cfg.BaseURL)
	client, err := s.mispClient(cfg, newKey)
	if err != nil {
		s.audit(c, "misp.test", target, map[string]any{"error": "config"}, false)
		mispFail(c, http.StatusBadRequest, err)
		return
	}
	release, ok := s.mispAcquire()
	if !ok {
		mispFailText(c, http.StatusTooManyRequests, "Başka bir MISP isteği sürüyor; birazdan tekrar deneyin", "Another MISP request is in progress; try again shortly")
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(c.Request.Context(), mispCallTimeout)
	defer cancel()
	version, err := client.Version(ctx)
	if err != nil {
		logger.Warn("MISP testi başarısız (%s): %v", target, err)
		s.audit(c, "misp.test", target, map[string]any{"error": "request_failed", "verifyTls": cfg.VerifyTLS}, false)
		mispFail(c, http.StatusBadGateway, err)
		return
	}
	s.audit(c, "misp.test", target, map[string]any{"version": version, "verifyTls": cfg.VerifyTLS}, true)
	c.JSON(http.StatusOK, gin.H{"success": true, "version": version, "baseUrl": client.Base()})
}

// mispPushResult bir gönderimin özeti.
type mispPushResult struct {
	Ref   misp.EventRef
	Stats misp.BuildStats
	IDs   []int64
}

// pushToMISP bulguları tek bir MISP olayı olarak gönderir ve kaydeder.
func (s *Server) pushToMISP(ctx context.Context, cfg storage.MISPConfig, results []storage.SearchResult) (mispPushResult, error) {
	var out mispPushResult
	client, err := s.mispClient(cfg, "")
	if err != nil {
		return out, err
	}
	tags, err := misp.ParseTags(cfg.Tags)
	if err != nil {
		tags = nil
	}
	findings := make([]misp.Finding, 0, len(results))
	for _, r := range results {
		arts, err := s.db.GetArtifacts(r.ID)
		if err != nil {
			return out, err
		}
		findings = append(findings, misp.Finding{Result: r, Artifacts: arts})
		out.IDs = append(out.IDs, r.ID)
	}
	ev, stats := misp.BuildEvent(findings, misp.EventOptions{Distribution: cfg.Distribution, Tags: tags})
	out.Stats = stats
	ref, err := client.AddEvent(ctx, ev)
	if err != nil {
		return out, err
	}
	out.Ref = ref
	if err := s.db.RecordMISPPush(out.IDs, ref.ID, time.Now()); err != nil {
		logger.Warn("MISP gönderim kaydı yazılamadı: %v", err)
	}
	return out, nil
}

// handleMISPPush POST /api/integrations/misp/push {resultIds} (analyst+).
func (s *Server) handleMISPPush(c *gin.Context) {
	var req struct {
		ResultIDs []int64 `json:"resultIds"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.ResultIDs) == 0 || len(req.ResultIDs) > mispMaxPush {
		mispFail(c, http.StatusBadRequest, misp.Errorf(nil, "Tek gönderimde 1–%d bulgu seçilmeli", "Select 1–%d findings per push", mispMaxPush))
		return
	}
	seen := make(map[int64]bool, len(req.ResultIDs))
	ids := make([]int64, 0, len(req.ResultIDs))
	for _, id := range req.ResultIDs {
		if id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	cfg, err := s.db.GetMISPConfig()
	if err != nil {
		respondInternalError(c, "GetMISPConfig", err)
		return
	}
	if !mispConfigured(cfg) {
		mispFail(c, http.StatusConflict, errMISPNotConfigured)
		return
	}
	target := mispHost(cfg.BaseURL)
	results, err := s.db.GetResultsByIDs(ids)
	if err != nil {
		respondInternalError(c, "GetResultsByIDs", err)
		return
	}
	if len(results) == 0 {
		mispFailText(c, http.StatusNotFound, "Seçilen bulgular bulunamadı", "The selected findings were not found")
		return
	}
	release, ok := s.mispAcquire()
	if !ok {
		mispFailText(c, http.StatusTooManyRequests, "Başka bir MISP isteği sürüyor; birazdan tekrar deneyin", "Another MISP request is in progress; try again shortly")
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(c.Request.Context(), mispCallTimeout)
	defer cancel()
	res, err := s.pushToMISP(ctx, cfg, results)
	if err != nil {
		logger.Warn("MISP gönderimi başarısız (%s): %v", target, err)
		s.audit(c, "misp.push", target, map[string]any{"requested": len(ids), "error": "request_failed"}, false)
		mispFail(c, http.StatusBadGateway, err)
		return
	}
	logger.Info("MISP GÖNDERİMİ: %d bulgu → olay #%s (%s)", len(res.IDs), res.Ref.ID, target)
	s.audit(c, "misp.push", target, map[string]any{
		"requested": len(ids), "findings": len(res.IDs), "attributes": res.Stats.Attributes,
		"eventId": res.Ref.ID, "ids": res.IDs, "skipped": skippedDetails(res.Stats.Skipped),
	}, true)
	c.JSON(http.StatusOK, gin.H{
		"success": true, "eventId": res.Ref.ID, "eventUuid": res.Ref.UUID, "eventUrl": res.Ref.URL,
		"findings": len(res.IDs), "attributes": res.Stats.Attributes, "skipped": res.Stats.Skipped,
	})
}

// skippedDetails atlanan IOC sayılarını denetim ayrıntısına çevirir.
func skippedDetails(m map[string]int) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// startMISPAutoPush yeni ve eşik üstü bulguları periyodik olarak MISP'e
// gönderen arka plan işini başlatır (ayar varsayılan kapalı).
func (s *Server) startMISPAutoPush() {
	go func() {
		ticker := time.NewTicker(mispAutoEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.runMISPAutoPush()
			case <-s.cleanupStop:
				return
			}
		}
	}()
}

// runMISPAutoPush tek bir otomatik gönderim turu.
func (s *Server) runMISPAutoPush() {
	cfg, err := s.db.GetMISPConfig()
	if err != nil || !cfg.AutoPush || !mispConfigured(cfg) || cfg.AutoSince == nil {
		return
	}
	if ok, _ := s.mispSecretStorage(); !ok {
		return
	}
	results, err := s.db.MISPAutoPushCandidates(cfg.AutoMinCriticality, *cfg.AutoSince, mispAutoBatch)
	if err != nil {
		logger.Warn("MISP otomatik gönderim adayları alınamadı: %v", err)
		return
	}
	if len(results) == 0 {
		return
	}
	release, ok := s.mispAcquire()
	if !ok {
		return // elle gönderim sürüyor; bir sonraki turda
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), mispCallTimeout)
	defer cancel()
	target := mispHost(cfg.BaseURL)
	res, err := s.pushToMISP(ctx, cfg, results)
	if err != nil {
		logger.Warn("MISP otomatik gönderimi başarısız (%s): %v", target, err)
		s.auditSystem("misp.push", target, map[string]any{"auto": true, "requested": len(results), "error": "request_failed"}, false)
		return
	}
	logger.Info("MISP OTOMATİK GÖNDERİM: %d bulgu → olay #%s (%s)", len(res.IDs), res.Ref.ID, target)
	s.auditSystem("misp.push", target, map[string]any{
		"auto": true, "findings": len(res.IDs), "attributes": res.Stats.Attributes, "eventId": res.Ref.ID,
		"minCriticality": cfg.AutoMinCriticality, "ids": res.IDs,
	}, true)
}
