package config

import (
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
	defaultRateLimitRPS   = 12
	defaultRateLimitBurst = 30
)

// AppConfig uygulama genel ayarlari.
type AppConfig struct {
	EnvFilePath     string
	TorProxy        string
	DBPath          string
	WebAddr         string
	LogDir          string
	LogLevel        string
	AdminUser       string
	AdminPass       string
	SecureCookies   bool
	SessionTTL      time.Duration
	SessionTTLHours int
	RateLimitRPS    float64
	RateLimitBurst  int
}

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
	if adminUser == "" || adminPass == "" {
		return AppConfig{}, fmt.Errorf("ADMIN_USER ve ADMIN_PASS zorunludur. Lutfen .env dosyasini doldurun")
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

	logLevel, err := parseLogLevel(get("LOG_LEVEL", defaultLogLevel))
	if err != nil {
		return AppConfig{}, err
	}

	secureCookies := parseBool(get("WEB_SECURE_COOKIES", "false"))

	cfg := AppConfig{
		EnvFilePath:     store.Path(),
		TorProxy:        get("TOR_PROXY", defaultTorProxy),
		DBPath:          get("DB_PATH", defaultDBPath),
		WebAddr:         get("WEB_ADDR", defaultWebAddr),
		LogDir:          get("LOG_DIR", defaultLogDir),
		LogLevel:        logLevel,
		AdminUser:       adminUser,
		AdminPass:       adminPass,
		SecureCookies:   secureCookies,
		SessionTTL:      time.Duration(sessionHours) * time.Hour,
		SessionTTLHours: sessionHours,
		RateLimitRPS:    rateLimitRPS,
		RateLimitBurst:  rateLimitBurst,
	}

	return cfg, nil
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
