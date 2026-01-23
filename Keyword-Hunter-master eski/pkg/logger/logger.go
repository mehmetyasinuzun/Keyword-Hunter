package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LogLevel log seviyesi
type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
)

var levelNames = map[LogLevel]string{
	DEBUG: "DEBUG",
	INFO:  "INFO",
	WARN:  "WARN",
	ERROR: "ERROR",
}

var levelColors = map[LogLevel]string{
	DEBUG: "\033[36m", // Cyan
	INFO:  "\033[32m", // Green
	WARN:  "\033[33m", // Yellow
	ERROR: "\033[31m", // Red
}

const colorReset = "\033[0m"

// Logger yapısı
type Logger struct {
	mu          sync.Mutex
	logDir      string
	currentDate string
	file        *os.File
	minLevel    LogLevel
	console     bool
}

var (
	instance *Logger
	once     sync.Once
)

// Init logger'ı başlatır
func Init(logDir string, minLevel LogLevel, consoleOutput bool) error {
	var initErr error
	once.Do(func() {
		instance = &Logger{
			logDir:   logDir,
			minLevel: minLevel,
			console:  consoleOutput,
		}
		initErr = instance.ensureLogDir()
		if initErr == nil {
			initErr = instance.rotateIfNeeded()
		}
	})
	return initErr
}

// GetInstance singleton instance döndürür
func GetInstance() *Logger {
	if instance == nil {
		// Default initialization
		Init("logs", INFO, true)
	}
	return instance
}

// ensureLogDir log dizinini oluşturur
func (l *Logger) ensureLogDir() error {
	return os.MkdirAll(l.logDir, 0755)
}

// rotateIfNeeded günlük log dosyası rotasyonu
func (l *Logger) rotateIfNeeded() error {
	today := time.Now().Format("2006-01-02")

	if l.currentDate == today && l.file != nil {
		return nil
	}

	// Eski dosyayı kapat
	if l.file != nil {
		l.file.Close()
	}

	// Yeni dosya aç
	logPath := filepath.Join(l.logDir, fmt.Sprintf("%s.log", today))
	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	l.file = file
	l.currentDate = today
	return nil
}

// log ana log fonksiyonu
func (l *Logger) log(level LogLevel, format string, args ...interface{}) {
	if level < l.minLevel {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// Gün değişti mi kontrol et
	l.rotateIfNeeded()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	levelStr := levelNames[level]
	message := fmt.Sprintf(format, args...)

	// Dosyaya yaz (renksiz)
	logLine := fmt.Sprintf("[%s] [%s] %s\n", timestamp, levelStr, message)
	if l.file != nil {
		l.file.WriteString(logLine)
	}

	// Console'a yaz (renkli)
	if l.console {
		coloredLine := fmt.Sprintf("%s[%s]%s [%s%s%s] %s\n",
			"\033[90m", timestamp, colorReset, // Gray timestamp
			levelColors[level], levelStr, colorReset,
			message)
		fmt.Print(coloredLine)
	}
}

// Debug debug seviyesi log
func (l *Logger) Debug(format string, args ...interface{}) {
	l.log(DEBUG, format, args...)
}

// Info info seviyesi log
func (l *Logger) Info(format string, args ...interface{}) {
	l.log(INFO, format, args...)
}

// Warn warning seviyesi log
func (l *Logger) Warn(format string, args ...interface{}) {
	l.log(WARN, format, args...)
}

// Error error seviyesi log
func (l *Logger) Error(format string, args ...interface{}) {
	l.log(ERROR, format, args...)
}

// Close logger'ı kapatır
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file != nil {
		err := l.file.Close()
		l.file = nil
		return err
	}
	return nil
}

// Writer io.Writer interface implementasyonu (Gin için)
func (l *Logger) Writer() io.Writer {
	return &logWriter{logger: l, level: INFO}
}

type logWriter struct {
	logger *Logger
	level  LogLevel
}

func (w *logWriter) Write(p []byte) (n int, err error) {
	w.logger.log(w.level, "%s", strings.TrimSpace(string(p)))
	return len(p), nil
}

// ============================================================================
// KISA ERİŞİM FONKSİYONLARI (Package-level)
// ============================================================================

// Debug package-level debug log
func Debug(format string, args ...interface{}) {
	GetInstance().Debug(format, args...)
}

// Info package-level info log
func Info(format string, args ...interface{}) {
	GetInstance().Info(format, args...)
}

// Warn package-level warning log
func Warn(format string, args ...interface{}) {
	GetInstance().Warn(format, args...)
}

// Error package-level error log
func Error(format string, args ...interface{}) {
	GetInstance().Error(format, args...)
}

// Close package-level close
func Close() error {
	if instance != nil {
		return instance.Close()
	}
	return nil
}

// ============================================================================
// ÖZEL LOG FONKSİYONLARI (İŞLEM BAZLI)
// ============================================================================

// SearchStarted arama başladığında
func SearchStarted(query string, engineCount int) {
	Info("🔍 ARAMA BAŞLADI: '%s' sorgusu %d arama motorunda aranıyor", query, engineCount)
}

// SearchEngineResult arama motoru sonucu
func SearchEngineResult(engineName string, resultCount int, err error) {
	if err != nil {
		Warn("❌ %s: Hata - %v", engineName, err)
	} else if resultCount > 0 {
		Info("✅ %s: %d sonuç bulundu", engineName, resultCount)
	} else {
		Debug("⚪ %s: Sonuç yok", engineName)
	}
}

// SearchCompleted arama tamamlandığında
func SearchCompleted(query string, totalResults int, duration time.Duration) {
	Info("✅ ARAMA TAMAMLANDI: '%s' - %d sonuç, süre: %v", query, totalResults, duration.Round(time.Millisecond))
}

// ScrapeStarted scrape başladığında
func ScrapeStarted(urlCount int) {
	Info("🕷️ SCRAPE BAŞLADI: %d URL taranacak", urlCount)
}

// ScrapeResult scrape sonucu
func ScrapeResult(url string, success bool, err string) {
	if success {
		Debug("✅ Scraped: %s", truncateURL(url))
	} else {
		Warn("❌ Scrape hatası: %s - %s", truncateURL(url), err)
	}
}

// ScrapeCompleted scrape tamamlandığında
func ScrapeCompleted(total, success, failed int, duration time.Duration) {
	Info("✅ SCRAPE TAMAMLANDI: %d/%d başarılı, %d başarısız, süre: %v", success, total, failed, duration.Round(time.Millisecond))
}

// DatabaseOperation veritabanı işlemi
func DatabaseOperation(operation string, details string) {
	Debug("💾 DB: %s - %s", operation, details)
}

// DatabaseError veritabanı hatası
func DatabaseError(operation string, err error) {
	Error("💾 DB HATA: %s - %v", operation, err)
}

// WebRequest web isteği
func WebRequest(method, path string, statusCode int, duration time.Duration) {
	Info("🌐 %s %s - %d (%v)", method, path, statusCode, duration.Round(time.Millisecond))
}

// UserLogin kullanıcı girişi
func UserLogin(username string, success bool, ip string) {
	if success {
		Info("👤 Giriş başarılı: %s (IP: %s)", username, ip)
	} else {
		Warn("🚫 Giriş başarısız: %s (IP: %s)", username, ip)
	}
}

// ExpandNode derinleştirme işlemi
func ExpandNode(url string, linkCount int, err error) {
	if err != nil {
		Warn("🔍 Derinleştirme hatası: %s - %v", truncateURL(url), err)
	} else {
		Info("🔍 Derinleştirildi: %s - %d link bulundu", truncateURL(url), linkCount)
	}
}

// SystemStartup sistem başlangıcı
func SystemStartup(torProxy, dbPath, webAddr string) {
	Info("═══════════════════════════════════════════════════════════")
	Info("🚀 KeywordHunter MVP Başlatılıyor")
	Info("═══════════════════════════════════════════════════════════")
	Info("🔌 Tor Proxy: %s", torProxy)
	Info("💾 Veritabanı: %s", dbPath)
	Info("🌐 Web Adresi: http://localhost%s", webAddr)
}

// SystemReady sistem hazır
func SystemReady(webAddr, username, password string) {
	Info("═══════════════════════════════════════════════════════════")
	Info("✅ Sistem hazır!")
	Info("🌐 http://localhost%s", webAddr)
	Info("👤 Kullanıcı: %s / %s", username, password)
	Info("═══════════════════════════════════════════════════════════")
}

// SystemShutdown sistem kapanışı
func SystemShutdown() {
	Info("🛑 KeywordHunter kapatılıyor...")
}

// truncateURL uzun URL'leri kısaltır
func truncateURL(url string) string {
	if len(url) > 60 {
		return url[:57] + "..."
	}
	return url
}
