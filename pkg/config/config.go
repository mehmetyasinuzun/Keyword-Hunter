package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultTorProxy       = "127.0.0.1:9150"
	defaultDBPath         = "keywordhunter.db"
	defaultWebAddr        = ":8080"
	defaultLogDir         = "logs"
	defaultLogLevel       = "info"
	defaultSessionTTL     = 24
	defaultRateLimitRPS   = 25
	defaultRateLimitBurst = 80
	// defaultAuditRetentionDays denetim kaydı saklama süresi (0 = süresiz).
	defaultAuditRetentionDays = 365
	// Zamanlanmış yedekleme: BACKUP_DIR boşsa kapalı.
	defaultBackupIntervalHours = 24
	defaultBackupKeep          = 7
)

// AppConfig uygulama genel ayarlari.
type AppConfig struct {
	EnvFilePath     string
	TorProxy        string
	TorControlAddr  string
	TorControlPass  string
	DBPath          string
	WebAddr         string
	LogDir          string
	LogLevel        string
	AdminUser       string
	AdminPass       string // düz metin parola (eski kurulumlar); boş olabilir
	AdminPassHash   string // bcrypt hash (tercih edilen); boş olabilir
	SecureCookies   bool
	SessionTTL      time.Duration
	SessionTTLHours int
	RateLimitRPS    float64
	RateLimitBurst  int
	// AuditRetentionDays denetim kaydının saklanacağı gün; 0 = süresiz sakla.
	AuditRetentionDays int
	// BackupDir zamanlanmış yedeklerin yazıldığı dizin; boş = zamanlanmış yedek kapalı.
	BackupDir string
	// BackupIntervalHours zamanlanmış yedek aralığı (saat).
	BackupIntervalHours int
	// BackupKeep tutulacak en yeni yedek sayısı (eskiler silinir).
	BackupKeep int
	// ResultRetentionDays bulguların saklanacağı gün (0 = süresiz). İnceleniyor /
	// Doğrulandı vakaları süresiz tutulur.
	ResultRetentionDays int
	// ScreenshotRetentionDays ekran görüntülerinin saklanacağı gün (0 = süresiz).
	ScreenshotRetentionDays int
	// AppSecret uygulama ana sırrı (64 hex = 32 bayt). TOTP sırlarını
	// şifreleyen anahtar bundan türetilir. Boşsa EnsureAppSecret üretir.
	AppSecret string
	// Require2FA iki adımlı doğrulama zorunluluğu: none | admins | all.
	Require2FA string
	// MetricsEnabled GET /metrics (Prometheus) ucunu açar; varsayılan kapalı.
	// Açıkken bile anonim değildir: admin hesabına ait API token gerekir.
	MetricsEnabled bool
}

// REQUIRE_2FA değerleri.
const (
	Require2FANone   = "none"
	Require2FAAdmins = "admins"
	Require2FAAll    = "all"
)

// appSecretLen APP_SECRET'ın hex karakter uzunluğu (32 bayt).
const appSecretLen = 64

// Load .env + process environment kaynaklarindan ayarlari yukler.
func Load(envFilePath string) (AppConfig, error) {
	store := NewEnvStore(envFilePath)
	fileValues, err := store.Read()
	if err != nil {
		return AppConfig{}, fmt.Errorf("env dosyasi okunamadi: %w", err)
	}

	get := func(key, fallback string) string {
		if value, ok := os.LookupEnv(key); ok {
			value = strings.TrimSpace(value)
			if value != "" {
				return value
			}
		}
		if value, ok := fileValues[key]; ok {
			value = strings.TrimSpace(value)
			if value != "" {
				return value
			}
		}
		return fallback
	}

	adminUser := get("ADMIN_USER", "")
	adminPass := get("ADMIN_PASS", "")
	adminPassHash := get("ADMIN_PASS_HASH", "")
	if adminUser == "" || (adminPass == "" && adminPassHash == "") {
		return AppConfig{}, fmt.Errorf("ADMIN_USER ve ADMIN_PASS (veya ADMIN_PASS_HASH) zorunludur. Lutfen .env dosyasini doldurun")
	}
	if adminPassHash != "" && !strings.HasPrefix(adminPassHash, "$2") {
		return AppConfig{}, fmt.Errorf("ADMIN_PASS_HASH bcrypt biçiminde olmalidir ($2a$/$2b$ ile baslar)")
	}

	sessionHours, err := parseInt(get("SESSION_TTL_HOURS", strconv.Itoa(defaultSessionTTL)), 1, 720, "SESSION_TTL_HOURS")
	if err != nil {
		return AppConfig{}, err
	}

	rateLimitRPS, err := parseFloat(get("RATE_LIMIT_RPS", strconv.FormatFloat(defaultRateLimitRPS, 'f', -1, 64)), 1, 200, "RATE_LIMIT_RPS")
	if err != nil {
		return AppConfig{}, err
	}

	rateLimitBurst, err := parseInt(get("RATE_LIMIT_BURST", strconv.Itoa(defaultRateLimitBurst)), 1, 500, "RATE_LIMIT_BURST")
	if err != nil {
		return AppConfig{}, err
	}

	auditRetention, err := parseInt(get("AUDIT_RETENTION_DAYS", strconv.Itoa(defaultAuditRetentionDays)), 0, 36500, "AUDIT_RETENTION_DAYS")
	if err != nil {
		return AppConfig{}, err
	}

	backupInterval, err := parseInt(get("BACKUP_INTERVAL_HOURS", strconv.Itoa(defaultBackupIntervalHours)), 1, 8760, "BACKUP_INTERVAL_HOURS")
	if err != nil {
		return AppConfig{}, err
	}
	backupKeep, err := parseInt(get("BACKUP_KEEP", strconv.Itoa(defaultBackupKeep)), 1, 1000, "BACKUP_KEEP")
	if err != nil {
		return AppConfig{}, err
	}

	resultRetention, err := parseInt(get("RESULT_RETENTION_DAYS", "0"), 0, 36500, "RESULT_RETENTION_DAYS")
	if err != nil {
		return AppConfig{}, err
	}
	screenshotRetention, err := parseInt(get("SCREENSHOT_RETENTION_DAYS", "0"), 0, 36500, "SCREENSHOT_RETENTION_DAYS")
	if err != nil {
		return AppConfig{}, err
	}

	appSecret := get("APP_SECRET", "")
	if appSecret != "" && !validAppSecret(appSecret) {
		return AppConfig{}, fmt.Errorf("APP_SECRET 64 onaltilik (hex) karakter olmalidir (32 bayt); uretmek icin: openssl rand -hex 32")
	}
	require2FA, err := parseRequire2FA(get("REQUIRE_2FA", Require2FANone))
	if err != nil {
		return AppConfig{}, err
	}

	logLevel, err := parseLogLevel(get("LOG_LEVEL", defaultLogLevel))
	if err != nil {
		return AppConfig{}, err
	}

	// Secure cookie varsayılanı ortama duyarlı: production'da true, development'ta false.
	defaultSecure := "true"
	if strings.EqualFold(get("APP_ENV", "production"), "development") {
		defaultSecure = "false"
	}
	secureCookies := parseBool(get("WEB_SECURE_COOKIES", defaultSecure))

	cfg := AppConfig{
		EnvFilePath:     store.Path(),
		TorProxy:        get("TOR_PROXY", defaultTorProxy),
		TorControlAddr:  get("TOR_CONTROL", ""),
		TorControlPass:  get("TOR_CONTROL_PASSWORD", ""),
		DBPath:          get("DB_PATH", defaultDBPath),
		WebAddr:         get("WEB_ADDR", defaultWebAddr),
		LogDir:          get("LOG_DIR", defaultLogDir),
		LogLevel:        logLevel,
		AdminUser:       adminUser,
		AdminPass:       adminPass,
		AdminPassHash:   adminPassHash,
		SecureCookies:   secureCookies,
		SessionTTL:      time.Duration(sessionHours) * time.Hour,
		SessionTTLHours: sessionHours,
		RateLimitRPS:    rateLimitRPS,
		RateLimitBurst:  rateLimitBurst,

		AuditRetentionDays:  auditRetention,
		BackupDir:           get("BACKUP_DIR", ""),
		BackupIntervalHours: backupInterval,
		BackupKeep:          backupKeep,

		ResultRetentionDays:     resultRetention,
		ScreenshotRetentionDays: screenshotRetention,
		AppSecret:               strings.ToLower(appSecret),
		Require2FA:              require2FA,
		MetricsEnabled:          parseBool(get("METRICS_ENABLED", "false")),
	}

	return cfg, nil
}

// ResolveDBPath DB_PATH'i Load ile aynı öncelikle çözer (ortam → .env →
// varsayılan) ama kimlik alanlarını (ADMIN_*) istemez: komut satırı araçları
// (keywordhunter backup) için. .env yoksa hata değildir.
func ResolveDBPath(envFilePath string) (string, error) {
	if v := strings.TrimSpace(os.Getenv("DB_PATH")); v != "" {
		return v, nil
	}
	values, err := NewEnvStore(envFilePath).Read()
	if err != nil {
		return "", fmt.Errorf("env dosyasi okunamadi: %w", err)
	}
	if v := strings.TrimSpace(values["DB_PATH"]); v != "" {
		return v, nil
	}
	return defaultDBPath, nil
}

// EnsureAppSecret APP_SECRET yoksa crypto/rand ile üretir ve etkin .env
// dosyasına kalıcı yazar (cfg.AppSecret güncellenir). created true ise yeni
// sır üretilmiştir; çağıran bunu loglar (değeri ASLA loglanmaz). Yazma
// başarısız olursa üretilen sır yine cfg'ye konur ve hata döner: çağıran sırrın
// bu çalıştırmayla sınırlı (geçici) olduğunu bilir ve 2FA kaydını kapatır,
// yoksa yeniden başlatmada kaydedilen 2FA sırları çözülemez hâle gelirdi.
func EnsureAppSecret(store *EnvStore, cfg *AppConfig) (created bool, err error) {
	if cfg.AppSecret != "" {
		return false, nil
	}
	buf := make([]byte, appSecretLen/2)
	if _, err := rand.Read(buf); err != nil {
		return false, fmt.Errorf("APP_SECRET uretilemedi: %w", err)
	}
	cfg.AppSecret = hex.EncodeToString(buf)
	if store == nil {
		return true, fmt.Errorf("APP_SECRET kalici yazilamadi: env deposu yok")
	}
	// Yalnız APP_SECRET satırı eklenir/değiştirilir: kullanıcının .env'indeki
	// yorumlar ve diğer satırlar korunur (Update dosyayı yeniden üretirdi).
	if err := store.SetPreserving("APP_SECRET", cfg.AppSecret); err != nil {
		return true, fmt.Errorf("APP_SECRET kalici yazilamadi (%s): %w", store.Path(), err)
	}
	return true, nil
}

// CheckRequire2FAStartup REQUIRE_2FA ile APP_SECRET durumunun uyumunu denetler.
// Zorunluluk etkinken sır kalıcı değilse 2FA kaydı kapalı olur ve kapsamdaki
// herkes çıkışı olmayan bir kayıt sayfasına hapsolur: başlatma reddedilmeli.
func CheckRequire2FAStartup(policy string, appSecretPersisted bool) error {
	if policy == "" || policy == Require2FANone || appSecretPersisted {
		return nil
	}
	return fmt.Errorf("REQUIRE_2FA=%s kalici bir APP_SECRET gerektirir: APP_SECRET'i (64 hex) ortamda ya da yazilabilir .env'de tanimlayin veya REQUIRE_2FA=none yapin", policy)
}

func validAppSecret(s string) bool {
	if len(s) != appSecretLen {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func parseRequire2FA(raw string) (string, error) {
	switch v := strings.ToLower(strings.TrimSpace(raw)); v {
	case Require2FANone, Require2FAAdmins, Require2FAAll:
		return v, nil
	default:
		return "", fmt.Errorf("REQUIRE_2FA gecersiz deger: %s (none/admins/all)", raw)
	}
}

func parseBool(raw string) bool {
	raw = strings.TrimSpace(strings.ToLower(raw))
	switch raw {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func parseInt(raw string, minVal int, maxVal int, key string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%s gecersiz deger: %s", key, raw)
	}
	if value < minVal || value > maxVal {
		return 0, fmt.Errorf("%s %d ile %d arasinda olmalidir", key, minVal, maxVal)
	}
	return value, nil
}

func parseFloat(raw string, minVal float64, maxVal float64, key string) (float64, error) {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return 0, fmt.Errorf("%s gecersiz deger: %s", key, raw)
	}
	if value < minVal || value > maxVal {
		return 0, fmt.Errorf("%s %.1f ile %.1f arasinda olmalidir", key, minVal, maxVal)
	}
	return value, nil
}

func parseLogLevel(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "debug", "info", "warn", "error":
		return value, nil
	default:
		return "", fmt.Errorf("LOG_LEVEL gecersiz deger: %s (debug/info/warn/error)", raw)
	}
}
