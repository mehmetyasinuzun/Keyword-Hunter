package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"keywordhunter-mvp/pkg/backup"
	"keywordhunter-mvp/pkg/config"
	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/scraper"
	"keywordhunter-mvp/pkg/search"
	"keywordhunter-mvp/pkg/storage"
	"keywordhunter-mvp/pkg/web"
)

func main() {
	envFilePath := resolveEnvFilePath()

	// Alt komutlar: keywordhunter backup [-out f.db] | verify-db f.db
	// (sunucuyu başlatmadan çalışıp çıkar; bkz. pkg/backup/cli.go).
	if len(os.Args) > 1 && backup.Commands[os.Args[1]] {
		if err := backup.RunCommand(os.Args[1], os.Args[2:], web.Version, envFilePath, os.Stdout, os.Stderr); err != nil {
			fmt.Fprintf(os.Stderr, "keywordhunter %s: %v\n", os.Args[1], err)
			os.Exit(1)
		}
		return
	}
	appConfig, err := config.Load(envFilePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Konfigürasyon hatası (%s): %v\n", envFilePath, err)
		fmt.Fprintln(os.Stderr, "Ipucu: local icin .env.example dosyasini .env olarak kopyalayin; Docker icin ENV_FILE=/data/.env kullanilir")
		os.Exit(1)
	}

	// Logger seviyesi LOG_LEVEL ile kontrol edilir (debug/info/warn/error)
	if err := logger.Init(appConfig.LogDir, resolveLogLevel(appConfig.LogLevel), true); err != nil {
		panic("Logger başlatılamadı: " + err.Error())
	}
	defer func() { _ = logger.Close() }()

	if appConfig.AdminPass == "admin123" || appConfig.AdminPass == "admin" || appConfig.AdminPass == "password" {
		logger.Warn("ZAYIF PAROLA: ADMIN_PASS varsayilan/zayif bir deger. /settings ekranindan hemen degistirin")
	}
	if appConfig.AdminPass != "" && appConfig.AdminPassHash == "" {
		logger.Info("ADMIN_PASS duz metin olarak yuklendi; /settings ekranindan parola degistirildiginde bcrypt hash olarak saklanacak")
	}

	// APP_SECRET: TOTP sırlarını şifreleyen anahtarın kaynağı. Yoksa üretilip
	// etkin .env'e yazılır (değer ASLA loglanmaz). Yazılamazsa sır bu
	// çalıştırmayla sınırlıdır; 2FA kaydı kapatılır (yeniden başlatmada
	// kaydedilen sırlar çözülemezdi).
	appSecretEphemeral := false
	if created, err := config.EnsureAppSecret(config.NewEnvStore(appConfig.EnvFilePath), &appConfig); err != nil {
		appSecretEphemeral = true
		logger.Error("APP_SECRET kalıcı yazılamadı: %v — 2FA kaydı bu çalıştırmada kapalı; APP_SECRET'ı (64 hex, ör. openssl rand -hex 32) ortamda ya da .env'de tanımlayın", err)
	} else if created {
		logger.Info("APP_SECRET bulunamadı; yeni bir sır üretildi ve %s dosyasına yazıldı. Yedekleyin: kaybolursa yöneticilerin kullanıcıların 2FA kaydını sıfırlaması gerekir", appConfig.EnvFilePath)
	}
	if appConfig.Require2FA != config.Require2FANone {
		// Kayıt kapalıyken zorunluluk, kapsamdaki herkesi Hesabım'a hapsederdi
		// (çıkış yolu yok, token'lar da 403): açıkça başlatmayı reddet.
		if err := config.CheckRequire2FAStartup(appConfig.Require2FA, !appSecretEphemeral); err != nil {
			logger.Error("%v", err)
			os.Exit(1)
		}
		logger.Info("REQUIRE_2FA=%s: iki adımlı doğrulama zorunlu; users tablosunda olmayan .env (ADMIN_USER) hesabıyla giriş reddedilir", appConfig.Require2FA)
	}

	// Graceful shutdown için signal handler
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Banner
	printBanner()

	// Sistem başlangıç logu
	logger.SystemStartup(appConfig.TorProxy, appConfig.DBPath, appConfig.WebAddr)
	logger.Info("CONFIG: Platform: %s/%s, Go %s, sürüm v%s", runtime.GOOS, runtime.GOARCH, runtime.Version(), web.Version)

	// Veritabanını aç
	db, err := storage.New(appConfig.DBPath)
	if err != nil {
		logger.Error("Database could not be opened: %v", err)
		os.Exit(1)
	}
	defer func() {
		logger.Info("Database connection is being closed...")
		if err := db.Close(); err != nil {
			logger.Error("Database close error: %v", err)
		}
	}()
	logger.Info("Database connection ready")

	// Searcher oluştur
	searcher, err := search.New(appConfig.TorProxy)
	if err != nil {
		logger.Error("Tor connection failed: %v", err)
		logger.Warn("Tor Browser (9150) veya tor servisi (9050) %s adresinde çalışıyor olmalı; TOR_PROXY ile değiştirin", appConfig.TorProxy)
		os.Exit(1)
	}
	logger.Info("Tor connection ready")

	// Scraper oluştur
	scraperClient, err := scraper.New(appConfig.TorProxy)
	if err != nil {
		logger.Error("Scraper could not be initialized: %v", err)
		os.Exit(1)
	}
	logger.Info("Scraper ready")

	// Web server başlat
	logger.SystemReady(appConfig.WebAddr, appConfig.AdminUser)
	logger.Info("Durdurmak için Ctrl+C")
	envStore := config.NewEnvStore(appConfig.EnvFilePath)

	server, err := web.New(web.Config{
		DB:             db,
		Searcher:       searcher,
		Scraper:        scraperClient,
		Username:       appConfig.AdminUser,
		Password:       appConfig.AdminPass,
		PasswordHash:   appConfig.AdminPassHash,
		CookieSecure:   appConfig.SecureCookies,
		SessionTTL:     appConfig.SessionTTL,
		RateLimitRPS:   appConfig.RateLimitRPS,
		RateLimitBurst: appConfig.RateLimitBurst,
		EnvStore:       envStore,
		TorProxy:       appConfig.TorProxy,
		TorControlAddr: appConfig.TorControlAddr,
		TorControlPass: appConfig.TorControlPass,

		AuditRetentionDays: appConfig.AuditRetentionDays,
		AppSecret:          appConfig.AppSecret,
		AppSecretEphemeral: appSecretEphemeral,
		Require2FA:         appConfig.Require2FA,

		BackupDir:           appConfig.BackupDir,
		BackupIntervalHours: appConfig.BackupIntervalHours,
		BackupKeep:          appConfig.BackupKeep,

		ResultRetentionDays:     appConfig.ResultRetentionDays,
		ScreenshotRetentionDays: appConfig.ScreenshotRetentionDays,
		MetricsEnabled:          appConfig.MetricsEnabled,
	})
	if err != nil {
		logger.Error("Web server could not be initialized: %v", err)
		os.Exit(1)
	}

	// Sunucuyu goroutine'de başlat
	go func() {
		if err := server.Run(appConfig.WebAddr); err != nil {
			logger.Error("Web Server Error: %v", err)
			sigChan <- syscall.SIGTERM
		}
	}()

	// Shutdown sinyali bekle
	<-sigChan

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Warn("Web server graceful shutdown başarısız: %v", err)
	}

	logger.SystemShutdown()
}

func printBanner() {
	banner := `
-------------------------------------------------------------
            KeywordHunter MVP - Dark Web CTI                 
             Robin-based Go Implementation                   
-------------------------------------------------------------
`
	println(banner)
}

func resolveEnvFilePath() string {
	path := strings.TrimSpace(os.Getenv("ENV_FILE"))
	if path != "" {
		return path
	}
	return ".env"
}

func resolveLogLevel(raw string) logger.LogLevel {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "DEBUG":
		return logger.DEBUG
	case "WARN":
		return logger.WARN
	case "ERROR":
		return logger.ERROR
	default:
		return logger.INFO
	}
}
