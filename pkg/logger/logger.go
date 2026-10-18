package logger

import (
	"fmt"
	"io"
	"net"
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
		// Default initialization: dizin açılamazsa instance yine kurulur ve
		// konsola yazmaya devam eder (dosya yazımı atlanır).
		_ = Init("logs", INFO, true)
	}
	return instance
}

// ensureLogDir log dizinini oluşturur
func (l *Logger) ensureLogDir() error {
	// Loglar sorgu ve hedef adresleri içerebilir: yalnız sahip (+grup okuma).
	return os.MkdirAll(l.logDir, 0o750)
}

// rotateIfNeeded günlük log dosyası rotasyonu
func (l *Logger) rotateIfNeeded() error {
	today := time.Now().Format("2006-01-02")

	if l.currentDate == today && l.file != nil {
		return nil
	}

	// Eski dosyayı kapat
	if l.file != nil {
		_ = l.file.Close()
		// Yeni dosya açılamazsa kapalı dosyaya yazmaya çalışılmasın.
		l.file = nil
	}

	// Yeni dosya aç
	logPath := filepath.Join(l.logDir, fmt.Sprintf("%s.log", today))
	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // G304: yol operatörün LOG_DIR'i + tarih; kullanıcı girdisi değil
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

	// Gün değişti mi kontrol et. Hata loglanamaz (logger'ın kendisi); dosya
	// açılamazsa l.file nil kalır ve yalnız konsola yazılır.
	_ = l.rotateIfNeeded()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	levelStr := levelNames[level]
	message := fmt.Sprintf(format, args...)

	// Dosyaya yaz (renksiz)
	logLine := fmt.Sprintf("[%s] [%s] %s\n", timestamp, levelStr, message)
	if l.file != nil {
		_, _ = l.file.WriteString(logLine) // logger kendi yazma hatasını loglayamaz
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
	Info("SEARCH STARTED: Query='%s' at %d engines", query, engineCount)
}

// SearchEngineResult arama motoru sonucu
func SearchEngineResult(engineName string, resultCount int, err error) {
	if err != nil {
		Warn("SEARCH ENGINE ERROR: %s - %v", engineName, err)
	} else if resultCount > 0 {
		Info("SEARCH ENGINE SUCCESS: %s - %d results found", engineName, resultCount)
	} else {
		Debug("SEARCH ENGINE NO RESULTS: %s", engineName)
	}
}

// SearchCompleted arama tamamlandığında
func SearchCompleted(query string, totalResults int, duration time.Duration) {
	Info("SEARCH COMPLETED: Query='%s' - Total results: %d, Duration: %v", query, totalResults, duration.Round(time.Millisecond))
}

// ScrapeStarted scrape başladığında
func ScrapeStarted(urlCount int) {
	Info("SCRAPE STARTED: %d URLs to process", urlCount)
}

// ScrapeResult scrape sonucu
func ScrapeResult(url string, success bool, err string) {
	if success {
		Debug("SCRAPE SUCCESS: %s", truncateURL(url))
	} else {
		Warn("SCRAPE FAILED: %s - %s", truncateURL(url), err)
	}
}

// ScrapeCompleted scrape tamamlandığında
func ScrapeCompleted(total, success, failed int, duration time.Duration) {
	Info("SCRAPE COMPLETED: %d/%d success, %d failed, Duration: %v", success, total, failed, duration.Round(time.Millisecond))
}

// DatabaseOperation veritabanı işlemi
func DatabaseOperation(operation string, details string) {
	Debug("DATABASE OPS: %s - %s", operation, details)
}

// DatabaseError veritabanı hatası
func DatabaseError(operation string, err error) {
	Error("DATABASE ERROR: %s - %v", operation, err)
}

// WebRequest web isteği
func WebRequest(method, path string, statusCode int, duration time.Duration) {
	Info("WEB REQUEST: %s %s - STATUS %d (%v)", method, path, statusCode, duration.Round(time.Millisecond))
}

// UserLogin kullanıcı girişi
func UserLogin(username string, success bool, ip string) {
	if success {
		Info("USER LOGIN SUCCESS: User='%s' (IP: %s)", username, ip)
	} else {
		Warn("USER LOGIN FAILED: User='%s' (IP: %s)", username, ip)
	}
}

// ExpandNode derinleştirme işlemi
func ExpandNode(url string, linkCount int, err error) {
	if err != nil {
		Warn("GRAPH EXPAND ERROR: %s - %v", truncateURL(url), err)
	} else {
		Info("GRAPH EXPAND SUCCESS: %s - %d links found", truncateURL(url), linkCount)
	}
}

// SystemStartup sistem başlangıcı
func SystemStartup(torProxy, dbPath, webAddr string) {
	Info("-----------------------------------------------------------")
	Info("SYSTEM STARTUP: KeywordHunter MVP Refactor")
	Info("-----------------------------------------------------------")
	Info("CONFIG: Tor Proxy: %s", torProxy)
	Info("CONFIG: Database Path: %s", dbPath)
	Info("CONFIG: Web Address: %s", BrowseURL(webAddr))
}

// BrowseURL dinleme adresini tarayıcıda açılabilir bir URL'ye çevirir:
// ":8080" → http://localhost:8080, "127.0.0.1:8080" → http://127.0.0.1:8080,
// "0.0.0.0:8080" / "[::]:8080" → http://localhost:8080.
func BrowseURL(webAddr string) string {
	host, port, err := net.SplitHostPort(webAddr)
	if err != nil {
		return "http://" + webAddr
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// SystemReady sistem hazır
func SystemReady(webAddr, username string) {
	Info("-----------------------------------------------------------")
	Info("SYSTEM READY: Listening for requests")
	Info("URL: %s", BrowseURL(webAddr))
	Info("AUTH: Login user: %s", username)
	Info("-----------------------------------------------------------")
}

// SystemShutdown sistem kapanışı
func SystemShutdown() {
	Info("SYSTEM SHUTDOWN: Cleaning up resources...")
}

// truncateURL uzun URL'leri kısaltır
func truncateURL(url string) string {
	if len(url) > 60 {
		return url[:57] + "..."
	}
	return url
}
