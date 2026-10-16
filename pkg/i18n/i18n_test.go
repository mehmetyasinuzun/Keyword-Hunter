package i18n

import "testing"

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
	if len(c) != len(catalog["tr"]) {
		t.Fatalf("katalog TR ile aynı anahtar sayısında olmalı: en=%d tr=%d", len(c), len(catalog["tr"]))
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
