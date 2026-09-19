package shared

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// ErrPrivateDestination bağlantı dahili bir adrese çözümlendiğinde döner.
var ErrPrivateDestination = errors.New("hedef dahili/özel bir adrese çözümlendi")

// SafeDialControl DNS çözümü sonrası gerçek hedef IP'yi kontrol eder; dahili
// adreslere bağlantıyı reddeder (DNS rebinding / yönlendirme ile SSRF koruması).
func SafeDialControl(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || IsPrivateIP(ip) {
		return fmt.Errorf("%w: %s", ErrPrivateDestination, host)
	}
	return nil
}

// NewSafeClearnetClient webhook gibi clearnet çağrıları için sertleştirilmiş
// istemci: yönlendirme takibi yok, dahili hedef yok, kısa zaman aşımı,
// ortam proxy'leri kullanılmaz.
func NewSafeClearnetClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   SafeDialControl,
	}
	transport := &http.Transport{
		Proxy:               nil,
		DialContext:         dialer.DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		MaxIdleConns:        4,
		IdleConnTimeout:     30 * time.Second,
		ForceAttemptHTTP2:   true,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// contextKey paket-içi bağlam anahtarı (ileride kullanım için ayrılmıştır).
type contextKey struct{}

var _ = context.Background
var _ contextKey

// NewPlainClearnetClient dahili hedeflere izin veren ama yine yönlendirme takip
// etmeyen, ortam proxy'si kullanmayan istemci (WEBHOOK_ALLOW_PRIVATE için).
func NewPlainClearnetClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Client{
		Transport: &http.Transport{
			Proxy:               nil,
			DialContext:         dialer.DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
			MaxIdleConns:        4,
			IdleConnTimeout:     30 * time.Second,
		},
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
