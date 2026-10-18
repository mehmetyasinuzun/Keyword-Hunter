// Package misp bulguları ve IOC'lerini bir MISP örneğine olay (event) olarak
// gönderir. Çıkış Tor üzerinden DEĞİL, doğrudan clearnet/intranet'tir; bu
// yüzden istemci sertleştirilmiştir: ortam proxy'si yok, yönlendirme takibi
// yok, sınırlı yanıt boyutu, kısa zaman aşımı ve (allowPrivate kapalıyken)
// DNS çözümü sonrası dahili adrese bağlanma yasağı.
package misp

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"keywordhunter-mvp/pkg/shared"
)

const (
	// DefaultTimeout bir MISP isteğinin toplam süresi.
	DefaultTimeout = 15 * time.Second
	// MaxResponseBytes okunan en büyük yanıt gövdesi.
	MaxResponseBytes = 1 << 20
	// MaxTags olaya eklenebilecek en fazla etiket.
	MaxTags = 20
	// maxTagRunes tek bir etiketin en fazla karakter sayısı.
	maxTagRunes = 128
	// MaxAttributes tek olaydaki en fazla öznitelik (gönderim boyutu sınırı).
	MaxAttributes = 5000
	// maxAPIKeyLen kabul edilen en uzun API anahtarı (MISP anahtarları 40 karakter).
	maxAPIKeyLen = 256
)

var (
	// ErrInvalidURL taban adres geçersiz ya da izin verilmeyen bir hedef.
	ErrInvalidURL = errors.New("MISP adresi geçersiz")
	// ErrInvalidAPIKey API anahtarı biçimi geçersiz.
	ErrInvalidAPIKey = errors.New("MISP API anahtarı geçersiz")
	// ErrPlainHTTP düz http genel bir hedefe (ya da özel ağ izni kapalıyken).
	ErrPlainHTTP = errors.New("düz http yalnız kurum içi (özel) ağdaki bir MISP için ve 'Özel/kurum içi ağa izin ver' açıkken kullanılabilir; genel adresler için https gerekli")
	// errPublicPlainHTTP düz http adı bağlantı anında genel bir IP'ye çözüldü.
	errPublicPlainHTTP = errors.New("düz http hedefi genel bir IP")
)

// privateOnlyDialControl düz http bağlantılarında çözülen IP'nin kurum içi ağda
// olmasını şart koşar: API anahtarı Authorization başlığında şifresiz gider;
// genel bir IP'ye çözülen bir ad anahtarı internete sızdıramaz.
func privateOnlyDialControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip == nil || !shared.IsPrivateIP(ip) {
		return fmt.Errorf("%w: %s", errPublicPlainHTTP, host)
	}
	return nil
}

// errInvalidURL genel "geçersiz adres" hatası (iki dilli).
func errInvalidURL() *Error {
	return Errorf([]error{ErrInvalidURL}, ErrInvalidURL.Error(), "Invalid MISP URL")
}

// errPlainHTTP düz http'nin reddedildiği adres hatası (iki dilli).
func errPlainHTTP() *Error {
	return Errorf([]error{ErrInvalidURL, ErrPlainHTTP}, ErrInvalidURL.Error()+": "+ErrPlainHTTP.Error(),
		"Invalid MISP URL: plain http is only allowed for a MISP on an internal (private) network with 'Allow private/internal networks' on; public addresses require https")
}

// eventIDPattern MISP'in döndürdüğü olay kimliği (sayısal ya da UUID): kimlik
// bir bağlantıya gömüleceği için yalnız bu karakterler kabul edilir.
var eventIDPattern = regexp.MustCompile(`^[0-9A-Fa-f-]{1,64}$`)

// Config bir MISP bağlantısının ayarları.
type Config struct {
	BaseURL      string
	APIKey       string
	VerifyTLS    bool
	AllowPrivate bool
	Timeout      time.Duration
	UserAgent    string
}

// NormalizeBaseURL taban adresi doğrular ve sondaki "/" olmadan döndürür.
// Kurallar: yalnız http(s), kullanıcı bilgisi/sorgu/parça yok, .onion yok
// (çıkış Tor'dan geçmez). allowPrivate kapalıyken hedef genel bir adres
// olmalı; düz http yalnız allowPrivate açıkken ve hedef kurum içi ağdaysa
// kabul edilir — API anahtarı genel internette şifresiz taşınmaz. Ana makine
// bir IP ise özel aralıkta olmalıdır; bir ad ise (ör. misp.corp.example)
// bağlantı anında çözülen IP denetlenir (bkz. New → privateOnlyDialControl).
func NormalizeBaseURL(raw string, allowPrivate bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 {
		return "", errInvalidURL()
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", errInvalidURL()
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	switch u.Scheme {
	case "https":
	case "http":
		if !allowPrivate {
			return "", errPlainHTTP()
		}
		if ip := net.ParseIP(host); ip != nil && !shared.IsPrivateIP(ip) {
			return "", errPlainHTTP()
		}
	default:
		return "", errInvalidURL()
	}
	if strings.HasSuffix(host, ".onion") {
		return "", Errorf([]error{ErrInvalidURL},
			"MISP adresi geçersiz: .onion adresleri desteklenmez (MISP çıkışı Tor kullanmaz)",
			"Invalid MISP URL: .onion addresses are not supported (MISP egress does not use Tor)")
	}
	if !allowPrivate && shared.IsPrivateHost(host) {
		return "", Errorf([]error{ErrInvalidURL},
			"MISP adresi geçersiz: dahili/özel ağ adresi (kurum içi MISP için 'Özel/kurum içi ağa izin ver' seçeneğini açın)",
			"Invalid MISP URL: internal/private network address (turn on 'Allow private/internal networks' for an internal MISP)")
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host+u.EscapedPath(), "/"), nil
}

// ValidAPIKey anahtar biçimini denetler: boş değil, sınırlı uzunluk, yalnız
// görünür ASCII (başlığa enjeksiyon yok).
func ValidAPIKey(k string) bool {
	if k == "" || len(k) > maxAPIKeyLen {
		return false
	}
	for i := 0; i < len(k); i++ {
		if k[i] <= 0x20 || k[i] >= 0x7f {
			return false
		}
	}
	return true
}

// ParseTags virgül/satır ile ayrılmış etiket listesini doğrular, kırpar ve
// tekilleştirir.
func ParseTags(raw string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, t := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if !utf8.ValidString(t) || utf8.RuneCountInString(t) > maxTagRunes {
			return nil, Errorf(nil, "etiket en fazla %d karakter olmalı", "a tag can be at most %d characters", maxTagRunes)
		}
		for _, r := range t {
			if unicode.IsControl(r) {
				return nil, Errorf(nil, "etiket kontrol karakteri içeremez", "a tag cannot contain control characters")
			}
		}
		if seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
		if len(out) > MaxTags {
			return nil, Errorf(nil, "en fazla %d etiket", "at most %d tags", MaxTags)
		}
	}
	return out, nil
}

// Client tek bir MISP örneğine konuşan istemci.
type Client struct {
	base   string
	key    string
	ua     string
	client *http.Client
}

// New yapılandırmayı doğrular ve sertleştirilmiş bir istemci kurar.
func New(cfg Config) (*Client, error) {
	base, err := NormalizeBaseURL(cfg.BaseURL, cfg.AllowPrivate)
	if err != nil {
		return nil, err
	}
	if !ValidAPIKey(cfg.APIKey) {
		return nil, Errorf([]error{ErrInvalidAPIKey}, ErrInvalidAPIKey.Error(), "Invalid MISP API key")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	switch {
	case !cfg.AllowPrivate:
		// DNS çözümü sonrası gerçek IP denetlenir (DNS rebinding'e karşı).
		dialer.Control = shared.SafeDialControl
	case strings.HasPrefix(base, "http://"):
		// Düz http: yalnız kurum içi IP'ler (anahtar şifresiz taşınır).
		dialer.Control = privateOnlyDialControl
	}
	transport := &http.Transport{
		Proxy:                 nil, // Tor dahil hiçbir ortam proxy'si kullanılmaz
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
		MaxIdleConns:          2,
		IdleConnTimeout:       30 * time.Second,
		ForceAttemptHTTP2:     true,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			// Yalnız yöneticinin bilinçli seçimiyle (kurum içi öz-imzalı sertifika).
			InsecureSkipVerify: !cfg.VerifyTLS, //nolint:gosec // ayar ekranında uyarılı, varsayılan kapalı
		},
	}
	ua := cfg.UserAgent
	if ua == "" {
		ua = "KeywordHunter"
	}
	return &Client{
		base: base, key: cfg.APIKey, ua: ua,
		client: &http.Client{
			Transport: transport,
			Timeout:   timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// Base doğrulanmış taban adres.
func (c *Client) Base() string { return c.base }

// do bir JSON isteği gönderir ve yanıtı (en fazla MaxResponseBytes) out'a çözer.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.ua)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		// *url.Error tam adresi taşır; yalnız alttaki ağ hatası döner.
		var ue *url.Error
		if errors.As(err, &ue) && ue.Err != nil {
			err = ue.Err
		}
		if errors.Is(err, shared.ErrPrivateDestination) {
			return Errorf([]error{err},
				"MISP adresi dahili/özel bir IP'ye çözümlendi; kurum içi MISP için 'Özel/kurum içi ağa izin ver' seçeneğini açın",
				"The MISP address resolved to an internal/private IP; turn on 'Allow private/internal networks' for an internal MISP")
		}
		if errors.Is(err, errPublicPlainHTTP) {
			return Errorf([]error{err},
				"MISP adresi genel bir IP'ye çözümlendi; API anahtarı şifresiz gönderilmez — https kullanın",
				"The MISP address resolved to a public IP; the API key is never sent unencrypted — use https")
		}
		return Errorf([]error{err}, "MISP'e bağlanılamadı: %v", "Could not connect to MISP: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return Errorf([]error{err}, "MISP yanıtı okunamadı: %v", "Could not read the MISP response: %v", err)
	}
	if len(data) > MaxResponseBytes {
		return Errorf(nil, "MISP yanıtı çok büyük", "The MISP response is too large")
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return Errorf(nil,
			"MISP yönlendirme döndürdü (HTTP %d); taban adresi doğrudan MISP'i göstermeli (yönlendirmeler izlenmez)",
			"MISP returned a redirect (HTTP %d); the base URL must point directly at MISP (redirects are not followed)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Errorf(nil, "MISP HTTP %d döndürdü%s", "MISP returned HTTP %d%s", resp.StatusCode, errorMessage(data))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return Errorf(nil, "MISP yanıtı JSON değil (adres MISP API'sini göstermiyor olabilir)",
			"The MISP response is not JSON (the URL may not point at the MISP API)")
	}
	return nil
}

// errorMessage MISP hata gövdesinden kısa, temizlenmiş bir açıklama çıkarır.
func errorMessage(data []byte) string {
	var e struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &e) != nil {
		return ""
	}
	msg := e.Message
	if msg == "" {
		msg = e.Name
	}
	msg = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.TrimSpace(msg))
	if msg == "" {
		return ""
	}
	return ": " + shared.TruncateRunes(msg, 160)
}

// Version bağlantıyı ve anahtarı doğrular (GET /servers/getVersion).
func (c *Client) Version(ctx context.Context) (string, error) {
	var v struct {
		Version string `json:"version"`
	}
	if err := c.do(ctx, http.MethodGet, "/servers/getVersion", nil, &v); err != nil {
		return "", err
	}
	if v.Version == "" {
		return "", Errorf(nil, "MISP sürüm bilgisi döndürmedi (adres MISP API'sini göstermiyor olabilir)",
			"MISP returned no version (the URL may not point at the MISP API)")
	}
	return shared.TruncateRunes(v.Version, 32), nil
}

// EventRef oluşturulan olayın kimliği ve bağlantısı.
type EventRef struct {
	ID   string
	UUID string
	URL  string
}

// AddEvent olayı oluşturur (POST /events/add).
func (c *Client) AddEvent(ctx context.Context, ev Event) (EventRef, error) {
	var resp struct {
		Event struct {
			ID   json.RawMessage `json:"id"`
			UUID string          `json:"uuid"`
		} `json:"Event"`
	}
	if err := c.do(ctx, http.MethodPost, "/events/add", map[string]any{"Event": ev}, &resp); err != nil {
		return EventRef{}, err
	}
	id := rawID(resp.Event.ID)
	if !eventIDPattern.MatchString(id) {
		return EventRef{}, Errorf(nil, "MISP olay kimliği döndürmedi", "MISP returned no event id")
	}
	uid := resp.Event.UUID
	if !eventIDPattern.MatchString(uid) {
		uid = ""
	}
	return EventRef{ID: id, UUID: uid, URL: c.base + "/events/view/" + id}, nil
}

// rawID MISP kimliğini (metin ya da sayı) metne çevirir.
func rawID(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n int64
	if json.Unmarshal(raw, &n) == nil {
		return strconv.FormatInt(n, 10)
	}
	return ""
}
