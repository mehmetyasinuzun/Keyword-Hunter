// Package i18n basit iki dilli (tr/en) mesaj kataloğu sağlar. Anahtar bulunamazsa
// önce Türkçe'ye, o da yoksa anahtarın kendisine düşülür — bu sayede eksik çeviri
// arayüzü bozmaz.
package i18n

import "strings"

// DefaultLang varsayılan dil.
const DefaultLang = "tr"

// Supported desteklenen diller.
var Supported = []string{"tr", "en"}

// IsSupported dil kodunun desteklenip desteklenmediğini döndürür.
func IsSupported(lang string) bool {
	for _, l := range Supported {
		if l == lang {
			return true
		}
	}
	return false
}

// Normalize dil kodunu doğrular; geçersizse varsayılana düşer.
func Normalize(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if i := strings.IndexAny(lang, "-_"); i > 0 {
		lang = lang[:i]
	}
	if IsSupported(lang) {
		return lang
	}
	return DefaultLang
}

// T anahtarı verilen dilde çevirir.
func T(lang, key string) string {
	lang = Normalize(lang)
	if m, ok := catalog[lang]; ok {
		if v, ok := m[key]; ok {
			return v
		}
	}
	if v, ok := catalog[DefaultLang][key]; ok {
		return v
	}
	return key
}

// Catalog bir dilin tüm anahtarlarını döndürür (istemci tarafı JS için).
func Catalog(lang string) map[string]string {
	lang = Normalize(lang)
	out := make(map[string]string, len(catalog[DefaultLang]))
	for k, v := range catalog[DefaultLang] {
		out[k] = v
	}
	if m, ok := catalog[lang]; ok {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}
