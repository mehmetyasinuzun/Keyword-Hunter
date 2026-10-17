package i18n

import (
	"regexp"
	"strings"
	"testing"
)

func TestT_FallbackChain(t *testing.T) {
	if T("en", "nav.dashboard") != "Dashboard" {
		t.Fatal("en çeviri")
	}
	if T("tr", "nav.dashboard") != "Panel" {
		t.Fatal("tr çeviri")
	}
	// EN'de olmayan anahtar TR'ye düşer
	if got := T("en", "role.admin"); got != "Admin" {
		t.Fatalf("fallback: %s", got)
	}
	// Hiç olmayan anahtar kendini döner
	if T("en", "yok.boyle") != "yok.boyle" {
		t.Fatal("bilinmeyen anahtar")
	}
	// Geçersiz dil → tr
	if T("de", "nav.search") != "Ara"[:0]+"Arama" {
		t.Fatalf("geçersiz dil tr'ye düşmeli: %s", T("de", "nav.search"))
	}
}

func TestNormalizeAndCatalog(t *testing.T) {
	for in, want := range map[string]string{"EN": "en", "tr-TR": "tr", "en_US": "en", "xx": "tr", "": "tr"} {
		if Normalize(in) != want {
			t.Errorf("Normalize(%q)=%q want %q", in, Normalize(in), want)
		}
	}
	c := Catalog("en")
	if c["nav.dashboard"] != "Dashboard" || c["role.admin"] != "Admin" {
		t.Fatal("katalog eksik")
	}
	client := 0
	for k := range catalog["tr"] {
		if !serverOnly(k) {
			client++
		}
	}
	if len(c) != client {
		t.Fatalf("katalog TR'nin istemci anahtarlarıyla aynı sayıda olmalı: en=%d tr=%d", len(c), client)
	}
}

// Denetim kaydı kataloğu TR ve EN'de birebir aynı anahtar kümesine sahip olmalı
// (EN'de eksik anahtar sessizce Türkçe'ye düşerdi).
func TestAuditCatalog_KeyParity(t *testing.T) {
	isAudit := func(k string) bool { return k == "nav.audit" || len(k) > 6 && k[:6] == "audit." }
	n := 0
	for k := range catalog["tr"] {
		if !isAudit(k) {
			continue
		}
		n++
		if _, ok := catalog["en"][k]; !ok {
			t.Errorf("EN eksik: %s", k)
		}
	}
	for k := range catalog["en"] {
		if isAudit(k) {
			if _, ok := catalog["tr"][k]; !ok {
				t.Errorf("TR eksik: %s", k)
			}
		}
	}
	if n < 50 {
		t.Fatalf("denetim kataloğu beklenenden küçük: %d", n)
	}
}

// API hata/başarı iletileri TR ve EN'de birebir aynı anahtarlara ve aynı fmt
// fiillerine (aynı sırada) sahip olmalı: EN'de eksik anahtar Türkçe'ye düşer,
// fiil uyuşmazlığı ise "%!d(MISSING)" gibi bozuk metin üretir.
func TestAPIErrorCatalog_ParityAndVerbs(t *testing.T) {
	verbs := regexp.MustCompile(`%[-+# 0]*[0-9]*(?:\.[0-9]+)?[a-zA-Z%]`)
	isAPI := func(k string) bool { return strings.HasPrefix(k, "apierr.") || strings.HasPrefix(k, "apimsg.") }
	n := 0
	for k, tr := range catalog["tr"] {
		if !isAPI(k) {
			continue
		}
		n++
		en, ok := catalog["en"][k]
		if !ok {
			t.Errorf("EN eksik: %s", k)
			continue
		}
		if a, b := strings.Join(verbs.FindAllString(tr, -1), ","), strings.Join(verbs.FindAllString(en, -1), ","); a != b {
			t.Errorf("%s: fmt fiilleri uyuşmuyor tr=%q en=%q", k, a, b)
		}
		if strings.TrimSpace(tr) == "" || strings.TrimSpace(en) == "" {
			t.Errorf("%s: boş ileti", k)
		}
	}
	for k := range catalog["en"] {
		if isAPI(k) {
			if _, ok := catalog["tr"][k]; !ok {
				t.Errorf("TR eksik: %s", k)
			}
		}
	}
	if n < 100 {
		t.Fatalf("API hata kataloğu beklenenden küçük: %d", n)
	}
}

// Yalnız sunucuda kullanılan aileler sayfalara gömülen istemci kataloğuna
// girmez; T() ile yine çözülür.
func TestCatalog_ExcludesServerOnlyKeys(t *testing.T) {
	for _, lang := range Supported {
		for k := range Catalog(lang) {
			if serverOnly(k) {
				t.Fatalf("%s istemci kataloğunda olmamalı: %s", lang, k)
			}
		}
	}
	if T("en", "apierr.invalid_request") != "Invalid request" || T("tr", "apierr.invalid_request") != "Geçersiz istek" {
		t.Fatal("apierr anahtarları T() ile çözülmeli")
	}
}
