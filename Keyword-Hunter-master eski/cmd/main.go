package main

import (
	"os"
	"os/signal"
	"syscall"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/scraper"
	"keywordhunter-mvp/pkg/search"
	"keywordhunter-mvp/pkg/storage"
	"keywordhunter-mvp/pkg/web"
)

const (
	// Tor Browser proxy adresi
	TorProxy = "127.0.0.1:9150"
	// Veritabanı dosyası
	DBPath = "keywordhunter.db"
	// Web server adresi
	WebAddr = ":8080"
	// Login bilgileri
	Username = "admin"
	Password = "admin123"
	// Log dizini
	LogDir = "logs"
)

func main() {
	// Logger'ı başlat
	if err := logger.Init(LogDir, logger.INFO, true); err != nil {
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
		logger.Error("❌ Veritabanı açılamadı: %v", err)
		os.Exit(1)
	}
	defer func() {
		logger.Info("💾 Veritabanı kapatılıyor...")
		db.Close()
	}()
	logger.Info("✅ Veritabanı hazır")

	// Searcher oluştur
	searcher, err := search.New(TorProxy)
	if err != nil {
		logger.Error("❌ Tor bağlantısı kurulamadı: %v", err)
		logger.Warn("💡 Tor Browser'ın çalıştığından emin olun!")
		os.Exit(1)
	}
	logger.Info("✅ Tor bağlantısı hazır")

	// Scraper oluştur
	scraperClient, err := scraper.New(TorProxy)
	if err != nil {
		logger.Error("❌ Scraper oluşturulamadı: %v", err)
		os.Exit(1)
	}
	logger.Info("✅ Scraper hazır")

	// Web server başlat
	logger.SystemReady(WebAddr, Username, Password)
	logger.Info("Durdurmak için Ctrl+C")

	server := web.New(web.Config{
		DB:       db,
		Searcher: searcher,
		Scraper:  scraperClient,
		Username: Username,
		Password: Password,
	})

	// Sunucuyu goroutine'de başlat
	go func() {
		if err := server.Run(WebAddr); err != nil {
			logger.Error("❌ Sunucu hatası: %v", err)
			sigChan <- syscall.SIGTERM
		}
	}()

	// Shutdown sinyali bekle
	<-sigChan
	logger.SystemShutdown()
}

func printBanner() {
	banner := `
╔═══════════════════════════════════════════════════════════╗
║           🕵️  KeywordHunter MVP - Dark Web CTI             ║
║                   Robin-based Go Implementation            ║
╚═══════════════════════════════════════════════════════════╝
`
	println(banner)
}
