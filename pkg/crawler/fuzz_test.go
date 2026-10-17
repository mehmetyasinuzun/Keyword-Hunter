package crawler

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzVisitKey örümceğin ziyaret anahtarı ve başlık üretimi (bağlantılar düşmanca
// sayfalardan gelir): normalizeURL sabit noktadır (aynı sayfanın varyantları tek
// anahtara iner), parça (#) ve sondaki / içermez, küçük harflidir; titleFromURL
// "[Crawl] " önekli ve sınırlıdır.
func FuzzVisitKey(f *testing.F) {
	for _, s := range []string{
		"", "http://abc.onion/page#frag", "  HTTP://ABC.onion/X/  ", "http://abc.onion/a?b=1#c",
		"https://abc.onion/dir/leak-db_2026?x=1", "#only", "////", "http://" + strings.Repeat("ğ", 200),
		"http://abc.onion/İSTANBUL/", "\xff\xfe/#/",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, u string) {
		k := normalizeURL(u)
		if normalizeURL(k) != k {
			t.Fatalf("anahtar sabit nokta değil: %q → %q → %q", u, k, normalizeURL(k))
		}
		if strings.Contains(k, "#") || strings.HasSuffix(k, "/") || k != strings.TrimSpace(k) {
			t.Fatalf("anahtar normalize değil: %q", k)
		}
		if strings.ToLower(k) != k {
			t.Fatalf("anahtar küçük harf değil: %q", k)
		}
		title := titleFromURL(u)
		if !strings.HasPrefix(title, "[Crawl] ") || utf8.RuneCountInString(title) > len("[Crawl] ")+93 {
			t.Fatalf("başlık: %q", title)
		}
	})
}
