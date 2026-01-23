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

// Truncate string kısaltma
func Truncate(s string, length int) string {
	if len(s) <= length {
		return s
	}
	return s[:length] + "..."
}
