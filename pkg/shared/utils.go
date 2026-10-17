package shared

import (
	"strings"
	"unicode/utf8"
)

// ExtractDomain URL'den domain çıkarır
func ExtractDomain(urlStr string) string {
	// http://xxx.onion/path -> xxx.onion
	urlStr = strings.TrimPrefix(urlStr, "http://")
	urlStr = strings.TrimPrefix(urlStr, "https://")

	// Host; yol, sorgu ya da parça (#) ile biter: "http://x.onion#top" → x.onion
	if idx := strings.IndexAny(urlStr, "/?#"); idx != -1 {
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

// TruncateBytes s'yi en fazla maxBytes bayta, bir UTF-8 karakterini ortadan
// bölmeden kısaltır (bayt bütçeli alanlar: depolama/çıkarım üst sınırları).
func TruncateBytes(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	// Geçerli UTF-8'de karakter başı en fazla UTFMax-1 bayt geridedir; daha
	// uzun devam baytı dizisi geçersiz girdidir, bayt sınırında kesilir.
	cut := maxBytes
	for i := 0; i < utf8.UTFMax-1 && cut > 0 && !utf8.RuneStart(s[cut]); i++ {
		cut--
	}
	if !utf8.RuneStart(s[cut]) {
		cut = maxBytes
	}
	return s[:cut]
}
