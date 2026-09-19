package search

import (
	"strings"
	"testing"
)

const v3a = "http://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.onion"
const v3b = "http://bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.onion"

func TestParseResults_ExtractsLinksTitlesAndHits(t *testing.T) {
	s := &Searcher{}
	html := `<html><body>
	<a href="` + v3a + `/leak/db?x=1&amp;y=2">Fresh <b>leak</b> database &amp; dump</a>
	<a href="` + v3b + `/">&lt;img src=x onerror=alert(1)&gt;</a>
	<a href="` + v3a + `/leak/db?x=1&amp;y=2">duplicate</a>
	<a href="http://tordexu73joywapk2txdr54jed4imqledpcvcuf75qsas2gwdgksvnyd.onion/search?query=leak">engine self link</a>
	<a href="http://short.onion/x">invalid onion</a>
	plain text mention ` + v3b + `/forum/leak-thread
	</body></html>`

	results := s.deduplicate(s.parseResults(html, "TestEngine", "leak"))

	byURL := map[string]Result{}
	for _, r := range results {
		byURL[r.URL] = r
	}
	first, ok := byURL[v3a+"/leak/db?x=1&y=2"]
	if !ok {
		t.Fatalf("href'teki &amp; çözülmeli ve sonuç bulunmalı; sonuçlar: %+v", results)
	}
	if first.Title != "Fresh leak database & dump" {
		t.Errorf("title = %q", first.Title)
	}
	if first.KeywordHits < 1 {
		t.Errorf("keyword hits = %d, want >=1", first.KeywordHits)
	}
	if first.Source != "TestEngine" {
		t.Errorf("source = %q", first.Source)
	}
	if _, ok := byURL["http://tordexu73joywapk2txdr54jed4imqledpcvcuf75qsas2gwdgksvnyd.onion/search?query=leak"]; ok {
		t.Error("arama motoru alan adı sonuç olarak listelenmemeli")
	}
	if _, ok := byURL["http://short.onion/x"]; ok {
		t.Error("geçersiz onion adresi sonuç olarak listelenmemeli")
	}
	if _, ok := byURL[v3b+"/forum/leak-thread"]; !ok {
		t.Error("düz metindeki onion URL yakalanmalı")
	}
	// Başlık HTML varlıkları çözülür ama şablon/JS tarafında kaçışlanır; burada
	// yalnızca metin olarak saklandığını doğrula.
	if r, ok := byURL[v3b+"/"]; ok && !strings.Contains(r.Title, "img src=x") {
		t.Errorf("başlık metni korunmalı: %q", r.Title)
	}
}

func TestParseResults_EmptyQueryDoesNotInflateHits(t *testing.T) {
	s := &Searcher{}
	html := `<a href="` + v3a + `/x">something</a>`
	res := s.parseResults(html, "E", "")
	if len(res) != 1 || res[0].KeywordHits != 0 {
		t.Fatalf("boş sorguda hit 0 olmalı: %+v", res)
	}
}

func TestActiveEngines_FilterAndDefaults(t *testing.T) {
	s := &Searcher{}
	def := s.ActiveEngines()
	if len(def) != DefaultActiveCount() || len(def) == 0 {
		t.Fatalf("varsayılan aktif motor sayısı = %d, want %d", len(def), DefaultActiveCount())
	}
	s.SetEngineFilter(func(name string) bool { return name == "Tordex" })
	act := s.ActiveEngines()
	if len(act) != 1 || act[0].Name != "Tordex" {
		t.Fatalf("filtre uygulanmadı: %+v", act)
	}
}

func TestCleanTitle(t *testing.T) {
	if got := cleanTitle("..", v3a+"/some-page.html"); got != "[Some page]" {
		t.Errorf("kısa başlık URL'den türetilmeli: %q", got)
	}
	long := strings.Repeat("ş", 200)
	if got := cleanTitle(long, v3a); len([]rune(got)) != 150 {
		t.Errorf("uzun başlık rune-güvenli kısaltılmalı: %d", len([]rune(got)))
	}
}

func TestEnginePageURL(t *testing.T) {
	e := Engine{Name: "X", URL: "http://x.onion/search?q={query}", PageParam: "page"}
	if got := e.PageURL("leak", 1); got != "http://x.onion/search?q=leak" {
		t.Errorf("page1 = %q", got)
	}
	if got := e.PageURL("leak", 3); got != "http://x.onion/search?q=leak&page=3" {
		t.Errorf("page3 = %q", got)
	}
	single := Engine{Name: "Y", URL: "http://y.onion/?q={query}"}
	if got := single.PageURL("leak", 2); got != "" {
		t.Errorf("tek sayfalı motor page2 = %q, boş olmalı", got)
	}
	noq := Engine{Name: "Z", URL: "http://z.onion/s/{query}", PageParam: "p"}
	if got := noq.PageURL("leak", 2); got != "http://z.onion/s/leak?p=2" {
		t.Errorf("? olmayan URL page2 = %q", got)
	}
	paged := 0
	for _, e := range SearchEngines {
		if e.PageParam != "" {
			paged++
		}
	}
	if paged != 6 {
		t.Errorf("sayfalama destekleyen motor sayısı = %d, want 6", paged)
	}
}
