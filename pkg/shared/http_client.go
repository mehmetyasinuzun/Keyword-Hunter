package shared

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
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

	// SOCKS5 dialer ContextDialer'dır: DialContext ile istek bağlamı iptal
	// edildiğinde (zaman aşımı, kullanıcı vazgeçti) Tor bağlantı denemesi de
	// anında iptal olur; eski Dial ile bağlantı kurulumu arka planda sızıyordu.
	dialContext := func(ctx context.Context, network, addr string) (net.Conn, error) {
		if cd, ok := dialer.(proxy.ContextDialer); ok {
			return cd.DialContext(ctx, network, addr)
		}
		return dialer.Dial(network, addr)
	}

	transport := &http.Transport{
		Proxy:               nil, // ortam proxy'leri (HTTP_PROXY) asla kullanılmaz; her şey Tor'dan geçer
		DialContext:         dialContext,
		TLSHandshakeTimeout: TLSHandshakeTimeout,
		MaxIdleConns:        32,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
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
func DoWithRetry(client *http.Client, req *http.Request) (*http.Response, error) {
	return DoWithRetryN(client, req, MaxRetryAttempts)
}

// DoWithRetryN en fazla attempts deneme yapar (içerik çekimleri için daha az deneme
// tercih edilir: ölü bir .onion'a 3×60 sn harcamak kullanıcıyı dakikalarca bekletir).
func DoWithRetryN(client *http.Client, req *http.Request, attempts int) (*http.Response, error) {
	var lastErr error
	var resp *http.Response

	ctx := req.Context()
	if attempts <= 0 {
		attempts = 1
	}

	// Gövdeli istekler için her denemede gövdeyi yeniden oluşturabilmek gerekir.
	if req.Body != nil && req.GetBody == nil {
		return nil, fmt.Errorf("gövdeli istekte GetBody zorunludur (retry için)")
	}

	for attempt := 0; attempt < attempts; attempt++ {
		// Bağlam bittiyse (kullanıcı vazgeçti / zaman aşımı) denemeyi bırak
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// İlk denemeden sonra backoff uygula (iptal-edilebilir)
		if attempt > 0 {
			backoffDuration := CalculateBackoff(attempt)
			select {
			case <-time.After(backoffDuration):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		// Her denemede gövdeyi yeniden bağla
		if req.Body != nil && req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			req.Body = body
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

	// Tip-tabanlı kontroller (string fallback'ten önce)
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	if errors.Is(err, io.EOF) {
		return true
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
		return fmt.Errorf("tor proxy'ye bağlanılamadı: Tor Browser veya Tor servisinin çalıştığından emin olun")
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
