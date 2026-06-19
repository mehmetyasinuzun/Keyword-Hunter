package shared

import "strings"

// ExtractDomain URL'den domain çıkarır
func ExtractDomain(urlStr string) string {
	// http://xxx.onion/path -> xxx.onion
	urlStr = strings.TrimPrefix(urlStr, "http://")
	urlStr = strings.TrimPrefix(urlStr, "https://")

	if idx := strings.Index(urlStr, "/"); idx != -1 {
		urlStr = urlStr[:idx]
	}
	if idx := strings.Index(urlStr, "?"); idx != -1 {
		urlStr = urlStr[:idx]
	}

	return strings.ToLower(urlStr)
}

// Truncate string kısaltma (rune-güvenli)
func Truncate(s string, length int) string {
	if len([]rune(s)) <= length {
		return s
	}
	return TruncateRunes(s, length) + "..."
}

// TruncateRunes UTF-8 rune sınırını koruyarak ilk length rune'u döndürür
func TruncateRunes(s string, length int) string {
	if length < 0 {
		length = 0
	}
	runes := []rune(s)
	if len(runes) <= length {
		return s
	}
	return string(runes[:length])
}
