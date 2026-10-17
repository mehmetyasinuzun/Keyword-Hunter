// Package i18n basit iki dilli (tr/en) mesaj kataloğu sağlar. Anahtar bulunamazsa
// önce Türkçe'ye, o da yoksa anahtarın kendisine düşülür — bu sayede eksik çeviri
// arayüzü bozmaz.
package i18n

import (
	"sort"
	"strings"
)

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

// serverOnlyPrefixes yalnız sunucuda kullanılan anahtar aileleri: API hata ve
// başarı iletileri JSON yanıtında zaten çevrilmiş gelir; her sayfaya gömülen
// istemci kataloğunu boşuna şişirmesinler.
var serverOnlyPrefixes = []string{"apierr.", "apimsg."}

func serverOnly(key string) bool {
	for _, p := range serverOnlyPrefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// Catalog bir dilin istemci tarafı (JS) anahtarlarını döndürür; yalnız
// sunucuda kullanılan aileler (serverOnlyPrefixes) dahil edilmez.
func Catalog(lang string) map[string]string {
	lang = Normalize(lang)
	out := make(map[string]string, len(catalog[DefaultLang]))
	for k, v := range catalog[DefaultLang] {
		if !serverOnly(k) {
			out[k] = v
		}
	}
	if m, ok := catalog[lang]; ok {
		for k, v := range m {
			if !serverOnly(k) {
				out[k] = v
			}
		}
	}
	return out
}

// register bir dilin anahtarlarını kataloğa ekler. Sayfa/alan bazlı katalog
// dosyaları (catalog_*.go) bunu init() içinde çağırır; böylece çeviriler tek dev
// bir haritada değil, ilgili ekranın yanında tutulur.
func register(lang string, msgs map[string]string) {
	m, ok := catalog[lang]
	if !ok {
		m = make(map[string]string, len(msgs))
		catalog[lang] = m
	}
	for k, v := range msgs {
		m[k] = v
	}
}

// Has anahtarın verilen dilde (geri düşme OLMADAN) tanımlı olup olmadığını
// döndürür; çeviri eksiklerini yakalayan testler içindir.
func Has(lang, key string) bool {
	_, ok := catalog[lang][key]
	return ok
}

// KeysWithPrefix bir dilde verilen önekle başlayan tüm anahtarları sıralı
// döndürür (ör. "apierr." — kullanılmayan anahtar denetimi için).
func KeysWithPrefix(lang, prefix string) []string {
	var out []string
	for k := range catalog[lang] {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
