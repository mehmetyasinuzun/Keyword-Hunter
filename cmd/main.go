package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"keywordhunter-mvp/pkg/config"
	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/scraper"
	"keywordhunter-mvp/pkg/search"
	"keywordhunter-mvp/pkg/storage"
	"keywordhunter-mvp/pkg/web"
)

func main() {
	envFilePath := resolveEnvFilePath()
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
	defer logger.Close()

	if appConfig.AdminPass == "admin123" {
		logger.Warn("ADMIN_PASS olarak admin123 kullaniliyor. Yalnizca kapali/development ortaminda onerilir")
	}

	// Graceful shutdown için signal handler
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Banner
	printBanner()

	// Sistem başlangıç logu
	logger.SystemStartup(appConfig.TorProxy, appConfig.DBPath, appConfig.WebAddr)

	// Veritabanını aç
	db, err := storage.New(appConfig.DBPath)
	if err != nil {
		logger.Error("Database could not be opened: %v", err)
		os.Exit(1)
	}
	defer func() {
		logger.Info("Database connection is being closed...")
		db.Close()
	}()
	logger.Info("Database connection ready")

	// Searcher oluştur
	searcher, err := search.New(appConfig.TorProxy)
	if err != nil {
		logger.Error("Tor connection failed: %v", err)
		logger.Warn("Ensure Tor Browser or Tor Service is running on %s", appConfig.TorProxy)
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

	server := web.New(web.Config{
		DB:             db,
		Searcher:       searcher,
		Scraper:        scraperClient,
		Username:       appConfig.AdminUser,
		Password:       appConfig.AdminPass,
		CookieSecure:   appConfig.SecureCookies,
		SessionTTL:     appConfig.SessionTTL,
		RateLimitRPS:   appConfig.RateLimitRPS,
		RateLimitBurst: appConfig.RateLimitBurst,
		EnvStore:       envStore,
	})

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
