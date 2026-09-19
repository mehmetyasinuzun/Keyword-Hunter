package scraper

import (
	"strings"
	"testing"
)

// TestLooksJSOnly sezginin dört kuralını sabitler: zengin metin → false; script yok →
// false; script + SPA/noscript izi → true; script var, iz yok → yalnız metin <120 ise true.
func TestLooksJSOnly(t *testing.T) {
	rich := "<html><body><script src=\"a.js\"></script><p>" + strings.Repeat("anlamlı içerik ", 60) + "</p></body></html>"
	medium := "<html><body><script src=\"a.js\"></script><p>" + strings.Repeat("orta uzunlukta metin ", 10) + "</p></body></html>"

	cases := []struct {
		name string
		html string
		want bool
	}{
		{"SPA kabuğu id=root", `<html><body><div id="root"></div><script src="/app.js"></script></body></html>`, true},
		{"noscript + enable javascript", `<html><body><noscript>Please enable JavaScript</noscript><script>x()</script></body></html>`, true},
		{"next.js işareti", `<html><body><div id="__next"></div><script>1</script></body></html>`, true},
		{"react root", `<html><body><div data-reactroot=""></div><script>1</script></body></html>`, true},
		{"script var, iz yok, çok kısa metin", `<html><body><script>1</script><p>hi</p></body></html>`, true},
		{"script var, iz yok, orta metin (>=120)", medium, false},
		{"zengin içerik (>600) script olsa da", rich, false},
		{"hiç script yok, kısa metin", `<html><body><p>hi</p></body></html>`, false},
		{"boş", "", false},
	}
	for _, c := range cases {
		if got := LooksJSOnly(c.html); got != c.want {
			t.Errorf("%s: LooksJSOnly=%v, want %v (text len=%d)", c.name, got, c.want, len(HTMLToText(c.html)))
		}
	}
}
