package storage

import (
	"reflect"
	"strings"
	"testing"
)

// FuzzNormalizeTags etiket dizgisinin result_tags'e yazılan biçimi (etiketler
// sayfa içeriğinden türetilir): her etiket boş değil, virgül içermez, kenarında
// boşluk/sekme yok, küçük harfli ve tekil; normalizasyon sabit noktadır.
func FuzzNormalizeTags(f *testing.F) {
	for _, s := range []string{"", "a,b", " A ,b,,a,\tB\t, c ", ",,,", "İSTANBUL,istanbul", "x,\xff,ğ"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		if len(in) > 1<<16 {
			return
		}
		tags := normalizeTags(in)
		seen := map[string]bool{}
		for _, tag := range tags {
			if tag == "" || strings.Contains(tag, ",") || tag != trimSpace(tag) || seen[tag] {
				t.Fatalf("geçersiz etiket %q (%q)", tag, in)
			}
			seen[tag] = true
		}
		if again := normalizeTags(strings.Join(tags, ",")); !reflect.DeepEqual(again, tags) && len(tags) > 0 {
			t.Fatalf("sabit nokta değil: %q → %q → %q", in, tags, again)
		}
	})
}
