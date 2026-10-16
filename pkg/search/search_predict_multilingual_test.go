package search

import "testing"

// Arama sonuçları paylaşılan sınıflandırıcının çok dilli sinyallerinden yararlanır.
func TestResultPredictCTI_MultilingualTitles(t *testing.T) {
	cases := []struct {
		title, url, wantCat string
		minCrit             int
	}{
		{"Пробив по базам ФСБ и МВД, слив базы", "http://abc.onion/", "Veri Sızıntısı", 5},
		{"Kreditkartenbetrug & Geldwäsche Forum", "http://abc.onion/", "Finansal Dolandırıcılık", 5},
		{"Vendo acceso inicial — red corporativa", "http://abc.onion/", "Initial Access", 4},
		{"Maconha e cocaína com entrega", "http://abc.onion/loja", "Illegal Market", 4},
		{"Главная", "http://abc.onion/обнал/кардинг", "Finansal Dolandırıcılık", 5},
	}
	for _, tc := range cases {
		r := &Result{Title: tc.title, URL: tc.url}
		r.PredictCTI()
		if r.Category != tc.wantCat || r.Criticality < tc.minCrit {
			t.Errorf("%q: got %q/%d, want %q/>=%d", tc.title, r.Category, r.Criticality, tc.wantCat, tc.minCrit)
		}
	}
}
