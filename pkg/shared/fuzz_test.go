package shared

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzExtractDomain URL'den host çıkarımı (bağlantılar düşmanca sayfalardan
// gelir; iç/dış bağlantı ve aynı-domain kuralları buna dayanır): sonuç yol, sorgu
// ya da parça içermez, küçük harflidir ve sabit noktadır.
func FuzzExtractDomain(f *testing.F) {
	for _, s := range []string{
		"", "http://ABC.onion/x?y#z", "https://abc.onion?q", "http://abc.onion#top", "abc.onion",
		"http://user@abc.onion:8080/p", "http://http://x", "//x/y", "http://İ.onion/", "\xff/\xfe",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, u string) {
		d := ExtractDomain(u)
		if strings.ContainsAny(d, "/?#") {
			t.Fatalf("alan adı yol/sorgu/parça içeriyor: %q → %q", u, d)
		}
		if strings.ToLower(d) != d {
			t.Fatalf("küçük harf değil: %q", d)
		}
		if again := ExtractDomain(d); again != d {
			t.Fatalf("sabit nokta değil: %q → %q → %q", u, d, again)
		}
		if IsOnionURL(u) && !strings.Contains(d, ".onion") {
			t.Fatalf("onion URL'nin alan adı .onion içermeli: %q → %q", u, d)
		}
	})
}

// FuzzTruncate rune/bayt kısaltıcıları: sınır aşılmaz, sonuç girdinin önekidir,
// geçerli UTF-8 girdi geçerli UTF-8 kalır ve bayt kısaltıcı gereksiz kısaltmaz.
func FuzzTruncate(f *testing.F) {
	f.Add("şğüİ€😀abc", 5)
	f.Add("", 0)
	f.Add("\xff\xfe\xfd", 2)
	f.Add(strings.Repeat("ğ", 100), 199)
	f.Fuzz(func(t *testing.T, s string, n int) {
		if n < -10 || n > 1<<12 {
			return
		}
		b := TruncateBytes(s, n)
		if len(b) > max(n, 0) || !strings.HasPrefix(s, b) {
			t.Fatalf("TruncateBytes(%q,%d) = %q", s, n, b)
		}
		if utf8.ValidString(s) && !utf8.ValidString(b) {
			t.Fatalf("karakter bölündü: %q", b)
		}
		if n > 0 && len(s) > n && len(b) < n-(utf8.UTFMax-1) {
			t.Fatalf("gereksiz kısaltma: %d/%d", len(b), n)
		}
		// TruncateRunes geçersiz baytları U+FFFD'ye çevirir (rune dönüşümü);
		// önek/geçerlilik özellikleri geçerli UTF-8 girdi için aranır.
		if r := TruncateRunes(s, n); utf8.ValidString(s) {
			if !strings.HasPrefix(s, r) || !utf8.ValidString(r) || (r != s && utf8.RuneCountInString(r) != max(n, 0)) {
				t.Fatalf("TruncateRunes(%q,%d) = %q", s, n, r)
			}
		}
		if tr := Truncate(s, n); n >= 0 && utf8.RuneCountInString(tr) > n+3 {
			t.Fatalf("Truncate sınırı: %q", tr)
		}
	})
}

// FuzzDetectChallenge engel sayfası sezgisi: panik yok, (found, kind) tutarlı ve
// büyük/küçük harften bağımsız.
func FuzzDetectChallenge(f *testing.F) {
	f.Add("Just a moment...", "<div class=cf-browser-verification>")
	f.Add("Giriş", "Lütfen insan olduğunuzu doğrulayın")
	f.Add("", "<div class=\"g-recaptcha\"></div>")
	f.Add("Shop", "welcome")
	f.Fuzz(func(t *testing.T, title, body string) {
		found, kind := DetectChallenge(title, body)
		if found != (kind != "") {
			t.Fatalf("found=%v kind=%q tutarsız", found, kind)
		}
		f2, k2 := DetectChallenge(strings.ToUpper(title), strings.ToUpper(body))
		if utf8.ValidString(title+body) && !strings.ContainsAny(title+body, "ıİiI") && (f2 != found || k2 != kind) {
			t.Fatalf("büyük/küçük harf duyarlı: %v/%q vs %v/%q", found, kind, f2, k2)
		}
	})
}
