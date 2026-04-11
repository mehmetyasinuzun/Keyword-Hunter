package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/scraper"
	"keywordhunter-mvp/pkg/search"
	"keywordhunter-mvp/pkg/storage"
	"keywordhunter-mvp/pkg/web"
)

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

var (
	TorProxy = getEnv("TOR_PROXY", "127.0.0.1:9150")
	DBPath   = getEnv("DB_PATH", "keywordhunter.db")
	WebAddr  = getEnv("WEB_ADDR", ":8080")
	Username = getEnv("ADMIN_USER", "admin")
	Password = getEnv("ADMIN_PASS", "admin123")
	LogDir   = getEnv("LOG_DIR", "logs")
)

func main() {
	// Logger'ı başlat - Kullanıcı isteği üzerine DEBUG seviyesine çekildi
	if err := logger.Init(LogDir, logger.DEBUG, true); err != nil {
		panic("Logger başlatılamadı: " + err.Error())
	}
	defer logger.Close()

	// Graceful shutdown için signal handler
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Banner
	printBanner()

	// Sistem başlangıç logu
	logger.SystemStartup(TorProxy, DBPath, WebAddr)

	// Veritabanını aç
	db, err := storage.New(DBPath)
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
	searcher, err := search.New(TorProxy)
	if err != nil {
		logger.Error("Tor connection failed: %v", err)
		logger.Warn("Ensure Tor Browser or Tor Service is running on %s", TorProxy)
		os.Exit(1)
	}
	logger.Info("Tor connection ready")

	// Scraper oluştur
	scraperClient, err := scraper.New(TorProxy)
	if err != nil {
		logger.Error("Scraper could not be initialized: %v", err)
		os.Exit(1)
	}
	logger.Info("Scraper ready")

	// Web server başlat
	logger.SystemReady(WebAddr, Username)
	logger.Info("Durdurmak için Ctrl+C")

	server := web.New(web.Config{
		DB:       db,
		Searcher: searcher,
		Scraper:  scraperClient,
		TorProxy: TorProxy,
		Username: Username,
		Password: Password,
	})

	// Sunucuyu goroutine'de başlat
	go func() {
		if err := server.Run(WebAddr); err != nil {
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
