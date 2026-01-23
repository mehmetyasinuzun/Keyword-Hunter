package shared

import (
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// ==================== ALIAS CONSTANTS ====================

// Kolay erişim için alias'lar
var (
	HTTPTimeout   = DefaultHTTPTimeout
	SearchTimeout = SearchHTTPTimeout
	CTIKeywords   = HighValueKeywords
)

// NewHTTPClient yeni HTTP client oluşturur (Tor proxy ile)
// Standart *http.Client döndürür
func NewHTTPClient(torProxy string) (*http.Client, error) {
	proxyURL, err := url.Parse(TorProxyScheme + torProxy)
	if err != nil {
		return nil, fmt.Errorf("proxy URL parse hatası: %w", err)
	}

	dialer, err := proxy.FromURL(proxyURL, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("proxy dialer hatası: %w", err)
	}

	transport := &http.Transport{
		Dial:                dialer.Dial,
		TLSHandshakeTimeout: TLSHandshakeTimeout,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   DefaultHTTPTimeout,
	}

	return client, nil
}

// RandomUserAgent rastgele User-Agent döndürür
func RandomUserAgent() string {
	return UserAgents[rand.Intn(len(UserAgents))]
}

// DoWithRetry HTTP isteği yapar ve gerekirse exponential backoff ile yeniden dener
// Robin tarzı profesyonel retry mekanizması
func DoWithRetry(client *http.Client, req *http.Request) (*http.Response, error) {
	var lastErr error
	var resp *http.Response

	for attempt := 0; attempt < MaxRetryAttempts; attempt++ {
		// İlk denemeden sonra backoff uygula
		if attempt > 0 {
			backoffDuration := CalculateBackoff(attempt)
			time.Sleep(backoffDuration)
		}

		// İsteği yap
		resp, lastErr = client.Do(req)

		// Başarılı - hata yok
		if lastErr == nil {
			// Status code kontrolü
			if !RetryableStatusCodes[resp.StatusCode] {
				return resp, nil
			}

			// Retryable status code - body'yi kapat ve yeniden dene
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d (retryable)", resp.StatusCode)
			continue
		}

		// Bağlantı hatası - yeniden dene
		if IsRetryableError(lastErr) {
			continue
		}

		// Yeniden denenemez hata - dur
		break
	}

	return nil, ClassifyError(lastErr)
}

// CalculateBackoff exponential backoff hesaplar
func CalculateBackoff(attempt int) time.Duration {
	// Exponential backoff: initialBackoff * 2^attempt + jitter
	backoff := float64(InitialBackoffDuration) * math.Pow(BackoffMultiplier, float64(attempt))

	// Max backoff'u aşma
	if backoff > float64(MaxBackoffDuration) {
		backoff = float64(MaxBackoffDuration)
	}

	// Jitter ekle (%0-25 arası rastgele)
	jitter := backoff * (0.25 * rand.Float64())

	return time.Duration(backoff + jitter)
}

// IsRetryableError yeniden denenebilir hata kontrolü
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()

	// Timeout hataları
	if strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "deadline exceeded") {
		return true
	}

	// Bağlantı hataları
	if strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "network is unreachable") {
		return true
	}

	// SOCKS proxy hataları
	if strings.Contains(errStr, "socks") {
		return true
	}

	// EOF (bağlantı kesildi)
	if err == io.EOF || strings.Contains(errStr, "EOF") {
		return true
	}

	return false
}

// ClassifyError hatayı kullanıcı dostu mesaja çevirir
func ClassifyError(err error) error {
	if err == nil {
		return nil
	}

	errStr := err.Error()

	// Tor SOCKS hata kodları - site erişilemez
	if strings.Contains(errStr, "unknown error unknown code: 240") ||
		strings.Contains(errStr, "general SOCKS server failure") {
		return fmt.Errorf("site erişilemez: .onion adresi geçersiz veya site çevrimdışı")
	}

	// Bağlantı reddedildi - Tor kapalı
	if strings.Contains(errStr, "connection refused") {
		return fmt.Errorf("Tor proxy'ye bağlanılamadı: Tor Browser açık olduğundan emin olun")
	}

	// Genel socks connect hatası
	if strings.Contains(errStr, "socks connect") {
		return fmt.Errorf("site yanıt vermiyor veya erişilemez durumda")
	}

	// Timeout
	if strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "deadline exceeded") {
		return fmt.Errorf("zaman aşımı: site yanıt vermiyor")
	}

	return err
}

// IsLowValueURL URL'nin düşük değerli olup olmadığını kontrol eder
func IsLowValueURL(urlStr string) (bool, string) {
	lowerURL := strings.ToLower(urlStr)

	// Binary dosya kontrolü
	for _, ext := range BinaryExtensions {
		if strings.HasSuffix(lowerURL, ext) || strings.Contains(lowerURL, ext+"?") {
			return true, "binary/media file"
		}
	}

	// Düşük değerli pattern kontrolü
	for _, pattern := range LowValueURLPatterns {
		if strings.Contains(lowerURL, pattern) {
			return true, fmt.Sprintf("low-value pattern: %s", pattern)
		}
	}

	return false, ""
}

// ExtractDomain URL'den domain çıkarır
// Örnek: "http://example.onion/path" -> "example.onion"
func ExtractDomain(urlStr string) string {
	urlStr = strings.TrimSpace(urlStr)
	urlStr = strings.TrimPrefix(urlStr, "http://")
	urlStr = strings.TrimPrefix(urlStr, "https://")
	if idx := strings.Index(urlStr, "/"); idx != -1 {
		urlStr = urlStr[:idx]
	}
	if idx := strings.Index(urlStr, "?"); idx != -1 {
		urlStr = urlStr[:idx]
	}
	return strings.ToLower(strings.TrimSpace(urlStr))
}
