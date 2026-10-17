package artifact

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

var knownTypes = map[ArtifactType]bool{
	TypeEmail: true, TypeBitcoin: true, TypeMonero: true, TypeIP: true, TypeOnion: true,
	TypeCreditCard: true, TypePhone: true, TypeSSH: true, TypeAPIKey: true, TypeUsername: true, TypeHash: true,
}

// FuzzExtract dark web sayfasından gelen düşmanca metinle IOC çıkarımı:
// panik yok; çıktı sınırlı (≤500), her değer metnin bir alt dizgisi, tür bilinen
// kümede, güven [0,1], (tür, değer) tekil, bağlam sınırlı ve girdi geçerli UTF-8
// ise geçerli UTF-8; sayfanın kendi onion'u IOC sayılmaz; çıkarım deterministik.
func FuzzExtract(f *testing.F) {
	for _, s := range []struct{ text, src string }{
		{"Contact: seller.ops@protonmail.com, BTC 1BoatSLRHtKNngkdXEeobR76b53LETtpyT", "http://x.onion/"},
		{strings.Repeat("ş", 30) + " ali.veli@protonmail.com " + strings.Repeat("ğ", 30), ""},
		{"server 185.220.101.5 and 10.0.0.1, card 4111111111111111, +90 532 123 45 67", "https://a.b/c"},
		{"api_key=" + strings.Repeat("A", 40) + " username: root -----BEGIN RSA PRIVATE KEY-----", ""},
		{"5f4dcc3b5aa765d61d8327deb882cf99 " + strings.Repeat("0", 64) + " bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq", ""},
		{strings.Repeat("a", 56) + ".onion mirror " + strings.Repeat("b", 56) + ".onion", "http://" + strings.Repeat("a", 56) + ".onion/x"},
		{"4" + strings.Repeat("A", 94) + " monero xmr", ""},
		{"\xff\xfe@\xc3.com \xe2\x82", ""},
	} {
		f.Add(s.text, s.src)
	}
	ex := NewExtractor()
	f.Fuzz(func(t *testing.T, text, src string) {
		if len(text) > 1<<16 {
			return
		}
		arts := ex.Extract(text, src)
		if len(arts) > 500 {
			t.Fatalf("çıktı sınırı aşıldı: %d", len(arts))
		}
		host := strings.ToLower(src)
		host = strings.TrimPrefix(strings.TrimPrefix(host, "http://"), "https://")
		if i := strings.IndexAny(host, "/?#"); i >= 0 {
			host = host[:i]
		}
		seen := map[string]bool{}
		for _, a := range arts {
			if !knownTypes[a.Type] {
				t.Fatalf("bilinmeyen tür %q", a.Type)
			}
			if a.Value == "" || !strings.Contains(text, a.Value) {
				t.Fatalf("değer metnin alt dizgisi olmalı: %q", a.Value)
			}
			if a.Confidence < 0 || a.Confidence > 1 {
				t.Fatalf("güven aralık dışı: %v", a.Confidence)
			}
			if a.SourceURL != src {
				t.Fatalf("kaynak URL korunmalı")
			}
			key := string(a.Type) + "\x00" + a.Value
			if seen[key] {
				t.Fatalf("yinelenen artifact %q", key)
			}
			seen[key] = true
			if len(a.Context) > len(a.Value)+2*(50+utf8.UTFMax) {
				t.Fatalf("bağlam sınırsız: %d bayt (değer %d)", len(a.Context), len(a.Value))
			}
			if utf8.ValidString(text) && !utf8.ValidString(a.Context) {
				t.Fatalf("geçerli UTF-8 girdide bağlam geçersiz UTF-8: %q", a.Context)
			}
			if a.Type == TypeOnion && host != "" && strings.HasSuffix(host, strings.ToLower(a.Value)) {
				t.Fatalf("sayfanın kendi onion'u IOC sayıldı: %q", a.Value)
			}
		}
		if again := ex.Extract(text, src); !reflect.DeepEqual(arts, again) {
			t.Fatal("çıkarım deterministik olmalı")
		}
		for _, a := range FilterByConfidence(arts, 0.5) {
			if a.Confidence < 0.5 {
				t.Fatal("güven filtresi")
			}
		}
		sum := 0
		for _, n := range GetSummary(arts) {
			sum += n
		}
		if sum != len(arts) {
			t.Fatal("özet toplamı")
		}
	})
}
