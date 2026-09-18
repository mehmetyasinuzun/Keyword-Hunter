package shared

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// IsOnionURL bir adresin geçerli http(s) .onion URL'si olup olmadığını doğrular.
// Dark web'e yönelik tüm çıkış noktaları (derinleştirme, analiz, izleme listesi)
// yalnızca .onion hedeflere izin verir (SSRF koruması).
func IsOnionURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if !strings.HasSuffix(host, ".onion") {
		return false
	}
	label := strings.TrimSuffix(host, ".onion")
	if i := strings.LastIndex(label, "."); i >= 0 {
		label = label[i+1:]
	}
	// v3 onion adresleri 56 karakter base32; v2 (16) artık ağ tarafından desteklenmiyor
	// ama eski kayıtlarla uyum için kabul edilir.
	if len(label) != 56 && len(label) != 16 {
		return false
	}
	for _, c := range label {
		if !((c >= 'a' && c <= 'z') || (c >= '2' && c <= '7')) {
			return false
		}
	}
	return true
}

// IsPrivateHost bir host adının/IP'sinin dahili, loopback, link-local veya
// özel bir hedefe işaret edip etmediğini söyler. Alan adları için yalnızca
// bilinen dahili son ekler kontrol edilir; DNS çözümü ayrıca DialControl ile
// korunur (bkz. SafeDialControl).
func IsPrivateHost(host string) bool {
	lower := strings.ToLower(strings.TrimSuffix(host, "."))
	if lower == "" || lower == "localhost" || strings.HasSuffix(lower, ".localhost") ||
		strings.HasSuffix(lower, ".local") || strings.HasSuffix(lower, ".internal") ||
		strings.HasSuffix(lower, ".home.arpa") || strings.HasSuffix(lower, ".lan") {
		return true
	}
	if ip := net.ParseIP(lower); ip != nil {
		return IsPrivateIP(ip)
	}
	return false
}

// IsPrivateIP loopback, özel, link-local, unspecified, multicast ve CGNAT
// aralıklarını dahili sayar.
func IsPrivateIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	// 100.64.0.0/10 (CGNAT) ve 192.0.0.0/24, 198.18.0.0/15 (benchmark), 240.0.0.0/4
	if v4 := ip.To4(); v4 != nil {
		switch {
		case v4[0] == 100 && v4[1]&0xC0 == 64:
			return true
		case v4[0] == 192 && v4[1] == 0 && v4[2] == 0:
			return true
		case v4[0] == 198 && (v4[1] == 18 || v4[1] == 19):
			return true
		case v4[0] >= 240:
			return true
		}
	}
	return false
}

// IsPublicWebURL yalnızca http(s) şemalı, genel (dahili olmayan) bir hedefe
// işaret eden URL'leri kabul eder. allowOnion=true ise .onion hedefler de geçer.
func IsPublicWebURL(raw string, allowOnion bool) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	if u.User != nil {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	if strings.HasSuffix(strings.ToLower(host), ".onion") {
		return allowOnion && IsOnionURL(raw)
	}
	return !IsPrivateHost(host)
}

// SplitHostPort "host:port" doğrular; port 1-65535 aralığında olmalıdır.
func SplitHostPort(v string) (string, string, error) {
	host, port, err := net.SplitHostPort(strings.TrimSpace(v))
	if err != nil {
		return "", "", err
	}
	if host == "" {
		return "", "", fmt.Errorf("host boş olamaz")
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return "", "", fmt.Errorf("geçersiz port: %s", port)
	}
	return host, port, nil
}
