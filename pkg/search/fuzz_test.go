package search

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"keywordhunter-mvp/pkg/shared"
)

// FuzzParseResults arama motoru sonuç sayfası ayrıştırması (motor sayfası saldırgan
// kontrolünde olabilir): panik yok; tekilleştirilmiş her sonuç geçerli v3/v2 onion,
// motor alan adı değil, düşük değerli değil; URL ve başlık sınırlı, başlık boş
// değil; hit negatif değil; kaynak korunur; ayrıştırma deterministiktir.
func FuzzParseResults(f *testing.F) {
	files, _ := filepath.Glob("testdata/engines/*.html")
	for _, fn := range files {
		if b, err := os.ReadFile(fn); err == nil {
			f.Add(string(b), "leak")
		}
	}
	for _, s := range []string{
		`<a href="` + onion("d") + `/x">t</a>`,
		`<a href="/r?u=` + onion("e") + `&amp;x=1">&lt;b&gt;</a>`,
		onion("f") + "/" + strings.Repeat("a-b_", 600),
		onion("f") + "/" + strings.Repeat("x", 3000),
		`<a href="` + onion("g") + `/">` + strings.Repeat("ş", 300) + `</a>`,
		"http://\xff.onion/ " + onion("h") + "/\xfe",
	} {
		f.Add(s, "")
	}
	s := &Searcher{}
	f.Fuzz(func(t *testing.T, html, query string) {
		if len(html) > 1<<16 {
			return
		}
		raw := s.parseResults(html, "F", query)
		if again := s.parseResults(html, "F", query); !reflect.DeepEqual(raw, again) {
			t.Fatal("ayrıştırma deterministik olmalı")
		}
		res := s.deduplicate(raw)
		seen := map[string]bool{}
		for _, r := range res {
			if seen[r.URL] {
				t.Fatalf("yinelenen URL %q", r.URL)
			}
			seen[r.URL] = true
			if !shared.IsOnionURL(r.URL) || isSearchEngineDomain(r.URL) {
				t.Fatalf("geçersiz sonuç URL'si %q", r.URL)
			}
			if low, _ := shared.IsLowValueURL(r.URL); low {
				t.Fatalf("düşük değerli URL sızdı %q", r.URL)
			}
			if len(r.URL) > maxResultURLLen {
				t.Fatalf("URL sınırsız: %d bayt", len(r.URL))
			}
			if r.Title == "" || utf8.RuneCountInString(r.Title) > 150 {
				t.Fatalf("başlık boş/sınırsız (%d rune): %q", utf8.RuneCountInString(r.Title), r.Title)
			}
			if r.KeywordHits < 0 || r.Source != "F" || r.Criticality < 1 || r.Criticality > 5 {
				t.Fatalf("alanlar: %+v", r)
			}
		}
	})
}
