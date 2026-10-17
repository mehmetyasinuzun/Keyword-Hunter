package scraper

import (
	"net/url"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"keywordhunter-mvp/pkg/shared"
)

func addFixtureSeed(f *testing.F, add func(string)) {
	if b, err := os.ReadFile("testdata/forum_thread.html"); err == nil {
		add(string(b))
	}
}

// collapseASCIISpace HTMLToText'in boşluk kuralının bağımsız tanımı: RE2 \s
// ([\t\n\f\r ]) dizileri tek boşluğa iner, kenarlar kırpılır.
func collapseASCIISpace(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\f' || r == '\r'
	}), " ")
}

// FuzzHTMLToText görünür metin çıkarımı: panik yok; çıktı kırpılmış, ardışık
// ya da \t\n\r\f boşluk içermez; geçerli UTF-8 girdi geçerli UTF-8 çıktı verir;
// etiket ve varlık içermeyen düz metinde yalnız boşluk sadeleştirmesi yapılır.
func FuzzHTMLToText(f *testing.F) {
	for _, s := range []string{
		"", "  düz   metin\n\tsatır ", "<p>a</p><p>b</p>", "&lt;script&gt;x&lt;/script&gt;",
		"<script>alert('<a>')</script>görünür<style>p{}</style>", "<head><title>t</title></head><body>x</body>",
		"&#0;&#xD800;&#x110000;&nbsp;&amp;amp;", "<<<>>>", "<!-- yorum -->metin", "\xff<\xfe>",
	} {
		f.Add(s)
	}
	addFixtureSeed(f, func(s string) { f.Add(s) })
	f.Fuzz(func(t *testing.T, in string) {
		if len(in) > 1<<16 {
			return
		}
		out := HTMLToText(in)
		if out != strings.TrimSpace(out) {
			t.Fatalf("kırpılmamış çıktı: %q", out)
		}
		if strings.Contains(out, "  ") || strings.ContainsAny(out, "\t\n\r\f") {
			t.Fatalf("boşluk sadeleşmemiş: %q", out)
		}
		if utf8.ValidString(in) && !utf8.ValidString(out) {
			t.Fatalf("geçersiz UTF-8 üretildi: %q", out)
		}
		if !strings.ContainsAny(in, "<&") && out != collapseASCIISpace(strings.TrimSpace(in)) && out != strings.TrimSpace(collapseASCIISpace(in)) {
			t.Fatalf("düz metin değişmemeli: in=%q out=%q", in, out)
		}
		_ = LooksJSOnly(in)
		_ = extractHTMLTitle(in)
	})
}

// FuzzNormalizeURL bağlantı normalizasyonu: sonuç ya boştur ya da http(s)
// şemalı ayrıştırılabilir bir URL'dir (javascript:/data:/file: asla); sonuç
// sabit noktadır (yeniden normalize etmek değiştirmez).
func FuzzNormalizeURL(f *testing.F) {
	base := "http://" + strings.Repeat("s", 56) + ".onion/a/b/page?x=1"
	for _, s := range []string{
		"", "#x", "/root", "../up", "sib", "?q=1", "//other.onion/x", "JavaScript:alert(1)",
		" jaVascript:x", "data:text/html,x", "file:///etc/passwd", "ftp://h/f", "http://ok.onion/",
		"HTTPS://Up.Example/", "http:/a/../b", "http:0", "%zz", "http://[::1", "mailto:a@b",
	} {
		f.Add(s, base)
	}
	f.Add("x", "::bozuk")
	f.Add("/x", "http:opaque")
	f.Fuzz(func(t *testing.T, href, base string) {
		got := normalizeURL(href, base)
		if got == "" {
			return
		}
		if !strings.HasPrefix(got, "http://") && !strings.HasPrefix(got, "https://") {
			t.Fatalf("http(s) dışı sonuç: href=%q base=%q → %q", href, base, got)
		}
		if !strings.HasPrefix(href, "http") {
			// Çözümlenen (göreli) bağlantı ayrıştırılabilir ve host'ludur.
			if u, err := url.Parse(got); err != nil || u.Host == "" {
				t.Fatalf("host'suz/bozuk çözümleme: href=%q base=%q → %q (%v)", href, base, got, err)
			}
		}
		if again := normalizeURL(got, base); again != got {
			t.Fatalf("sabit nokta değil: %q → %q", got, again)
		}
	})
}

// FuzzExtractLinks düşmanca HTML'den bağlantı çıkarımı: panik yok; her bağlantı
// http(s), tekil, ikili dosya değil; başlık boş değil ve ≤100 rune; tür ile alan
// adı tutarlı (iç = sayfa alan adı).
func FuzzExtractLinks(f *testing.F) {
	page := "http://" + strings.Repeat("s", 56) + ".onion/forum/thread/9"
	for _, s := range []string{
		`<a href="/x">x</a>`, `<A HREF='JavaScript:alert(1)'>j</A>`, `<a href="&#106;avascript:x">e</a>`,
		`<a href="?a=1&amp;b=2">q</a><a href="#t">t</a>`, `<script>"<a href='http://evil'>"</script><a href="/real">r</a>`,
		`<a href="/` + strings.Repeat("ğ", 200) + `">` + strings.Repeat("ş", 200) + `</a>`,
		`<a href="/f.zip?dl=1">z</a><a href="/i.PNG">p</a>`, `<a href="http://` + strings.Repeat("o", 56) + `.onion">o</a>`,
		`<a href="%zz">bad</a>`, `<a href=" ">empty</a>`,
	} {
		f.Add(s, page)
	}
	addFixtureSeed(f, func(s string) { f.Add(s, page) })
	s := &Scraper{}
	f.Fuzz(func(t *testing.T, htmlIn, pageURL string) {
		if len(htmlIn) > 1<<16 {
			return
		}
		links, err := s.ExtractLinks(pageURL, htmlIn)
		if err != nil {
			t.Fatal(err)
		}
		pageDomain := shared.ExtractDomain(pageURL)
		seen := map[string]bool{}
		for _, l := range links {
			if !strings.HasPrefix(l.URL, "http://") && !strings.HasPrefix(l.URL, "https://") {
				t.Fatalf("http(s) dışı bağlantı: %q", l.URL)
			}
			if seen[l.URL] {
				t.Fatalf("yinelenen bağlantı %q", l.URL)
			}
			seen[l.URL] = true
			if isBinaryExtension(l.URL) {
				t.Fatalf("ikili dosya bağlantısı: %q", l.URL)
			}
			if l.Title == "" || utf8.RuneCountInString(l.Title) > 100 {
				t.Fatalf("başlık boş/sınırsız: %q", l.Title)
			}
			if l.Domain != shared.ExtractDomain(l.URL) {
				t.Fatalf("alan adı tutarsız: %q vs %q", l.Domain, l.URL)
			}
			if (l.LinkType == "internal") != (l.Domain == pageDomain) || (l.LinkType != "internal" && l.LinkType != "external") {
				t.Fatalf("tür tutarsız: %+v (sayfa %q)", l, pageDomain)
			}
		}
	})
}

// FuzzExtractTopKeywordsFromHTML etiket çıkarımı: panik yok; etiket sayısı
// sınırlı, tekil, küçük harf, durdurma kelimesi ya da arama sorgusu değil, kök
// tekrarı yok; hit sayısı negatif değil.
func FuzzExtractTopKeywordsFromHTML(f *testing.F) {
	for _, s := range []string{
		"<p>" + strings.Repeat("leak database password dump ", 20) + "</p>",
		"<p>administrator moderator history story operator separate generated monitor editor</p>" + strings.Repeat(" x", 30),
		"<p>İSTANBUL ŞİFRE SIZINTISI veritabanı veritabanı veritabanı " + strings.Repeat("ğüşıöç ", 20) + "</p>",
		"<p>tor rat 0day zero-day credit card api key " + strings.Repeat("y ", 40) + "</p>",
	} {
		f.Add(s, "leak", int8(5))
	}
	addFixtureSeed(f, func(s string) { f.Add(s, "leak", int8(0)) })
	s := &Scraper{}
	f.Fuzz(func(t *testing.T, htmlIn, query string, maxTags int8) {
		if len(htmlIn) > 1<<16 {
			return
		}
		res := s.ExtractTopKeywordsFromHTML(htmlIn, query, int(maxTags))
		limit := int(maxTags)
		if limit <= 0 {
			limit = 5
		}
		if res.KeywordHits < 0 || len(res.Tags) > limit {
			t.Fatalf("sınır: hits=%d tags=%v limit=%d", res.KeywordHits, res.Tags, limit)
		}
		if !res.Success {
			if len(res.Tags) != 0 {
				t.Fatal("başarısız sonuçta etiket olmamalı")
			}
			return
		}
		q := strings.ToLower(query)
		for i, tag := range res.Tags {
			if tag == "" || tag == q || stopWords[tag] || turkishStopWords[tag] {
				t.Fatalf("geçersiz etiket %q (sorgu %q)", tag, q)
			}
			for _, prev := range res.Tags[:i] {
				if strings.HasPrefix(tag, prev) || strings.HasPrefix(prev, tag) {
					t.Fatalf("kök tekrarı %q/%q", prev, tag)
				}
			}
		}
	})
}
