package misp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"keywordhunter-mvp/pkg/storage"
)

func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct {
		raw          string
		allowPrivate bool
		want         string // "" = reddedilmeli
	}{
		{"https://misp.example.org", false, "https://misp.example.org"},
		{"https://misp.example.org/", false, "https://misp.example.org"},
		{"https://misp.example.org/misp/", false, "https://misp.example.org/misp"},
		{"  https://MISP.example.org:8443  ", false, "https://MISP.example.org:8443"},
		{"http://misp.example.org", false, ""},       // genel internette düz http yok
		{"http://10.0.0.5", true, "http://10.0.0.5"}, // kurum içi: http kabul
		{"http://93.184.215.14", true, ""},           // genel IP: özel ağ izni olsa da düz http yok
		{"http://[2606:4700::1]", true, ""},
		{"http://misp.corp.example", true, "http://misp.corp.example"}, // ad: IP bağlantı anında denetlenir
		{"https://10.0.0.5", false, ""},                                // SSRF: özel IP
		{"https://127.0.0.1", false, ""},
		{"https://localhost", false, ""},
		{"https://misp.internal", false, ""},
		{"https://[::1]", false, ""},
		{"https://169.254.169.254", false, ""}, // bulut metadata
		{"https://misp.internal", true, "https://misp.internal"},
		{"https://abcdefghijklmnop.onion", true, ""}, // Tor kullanılmaz
		{"https://user:pw@misp.example.org", false, ""},
		{"https://misp.example.org/?x=1", false, ""},
		{"https://misp.example.org/#f", false, ""},
		{"ftp://misp.example.org", true, ""},
		{"javascript:alert(1)", true, ""},
		{"misp.example.org", true, ""},
		{"", true, ""},
	}
	for _, tc := range cases {
		got, err := NormalizeBaseURL(tc.raw, tc.allowPrivate)
		if tc.want == "" {
			if err == nil {
				t.Errorf("%q (allowPrivate=%v) reddedilmeliydi: %q", tc.raw, tc.allowPrivate, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%q: got %q, %v; want %q", tc.raw, got, err, tc.want)
		}
	}
}

func TestValidAPIKeyAndTags(t *testing.T) {
	if !ValidAPIKey(strings.Repeat("a", 40)) || ValidAPIKey("") || ValidAPIKey("abc def") || ValidAPIKey("abc\r\nX-Evil: 1") || ValidAPIKey(strings.Repeat("a", 300)) {
		t.Fatal("API anahtarı doğrulaması")
	}
	tags, err := ParseTags(" tlp:amber, source:keywordhunter ,TLP:AMBER,\n misp-galaxy:threat-actor=\"X\" ")
	if err != nil || strings.Join(tags, "|") != `tlp:amber|source:keywordhunter|misp-galaxy:threat-actor="X"` {
		t.Fatalf("etiketler: %v %v", tags, err)
	}
	if _, err := ParseTags("ok,\x00bad"); err == nil {
		t.Fatal("kontrol karakteri reddedilmeli")
	}
	if _, err := ParseTags(strings.Repeat("x", 200)); err == nil {
		t.Fatal("aşırı uzun etiket reddedilmeli")
	}
	many := make([]string, 25)
	for i := range many {
		many[i] = "tag" + string(rune('a'+i))
	}
	if _, err := ParseTags(strings.Join(many, ",")); err == nil {
		t.Fatal("en fazla 20 etiket")
	}
}

func sampleFindings() []Finding {
	return []Finding{
		{Result: storage.SearchResult{ID: 1, Title: "Combo\nleak", URL: "http://aaaa.onion/leak", Query: "acme", Criticality: 3}, Artifacts: []storage.ResultArtifact{
			{Type: "email", Value: "seller@proton.me"},
			{Type: "bitcoin", Value: "bc1qexamplewallet"},
			{Type: "monero", Value: "4AdUndXHHZ6cfufTMvppY6JwXNouMBzSkbLYfpAV5Usx"},
			{Type: "ip", Value: "185.1.2.3"},
			{Type: "hash", Value: strings.Repeat("a", 32)},
			{Type: "hash", Value: strings.Repeat("b", 40)},
			{Type: "hash", Value: strings.Repeat("c", 64)},
			{Type: "hash", Value: strings.Repeat("d", 50)},
			{Type: "onion", Value: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.onion"},
			{Type: "onion", Value: "cccc.onion/path"},
			{Type: "credit_card", Value: "4111111111111111"},
			{Type: "api_key", Value: "sk_live_SECRET"},
		}},
		{Result: storage.SearchResult{ID: 2, Title: "Mirror", URL: "http://aaaa.onion/leak", Query: "acme", Criticality: 5}, Artifacts: []storage.ResultArtifact{
			{Type: "email", Value: "seller@proton.me"}, // yinelenen öznitelik
		}},
	}
}

func TestBuildEvent_Mapping(t *testing.T) {
	now := time.Date(2026, 5, 4, 3, 2, 1, 0, time.UTC)
	ev, st := BuildEvent(sampleFindings(), EventOptions{Distribution: 1, Tags: []string{"tlp:amber", "source:keywordhunter"}, Now: now})
	if ev.Distribution != "1" || ev.ThreatLevelID != "1" || ev.Analysis != "0" || ev.Published || ev.Date != "2026-05-04" {
		t.Fatalf("olay alanları: %+v", ev)
	}
	if !strings.Contains(ev.Info, "2 dark web finding") || !strings.Contains(ev.Info, "acme") {
		t.Fatalf("info: %q", ev.Info)
	}
	got := map[string]Attribute{}
	for _, a := range ev.Attribute {
		if _, dup := got[a.Type+"|"+a.Value]; dup {
			t.Fatalf("yinelenen öznitelik: %v", a)
		}
		got[a.Type+"|"+a.Value] = a
		if a.ToIDS || a.Distribution != "5" {
			t.Fatalf("to_ids=false ve dağıtım olaydan alınmalı: %+v", a)
		}
	}
	want := map[string]string{
		"url|http://aaaa.onion/leak":                                            "Network activity",
		"email-src|seller@proton.me":                                            "Payload delivery",
		"btc|bc1qexamplewallet":                                                 "Financial fraud",
		"xmr|4AdUndXHHZ6cfufTMvppY6JwXNouMBzSkbLYfpAV5Usx":                      "Financial fraud",
		"ip-dst|185.1.2.3":                                                      "Network activity",
		"md5|" + strings.Repeat("a", 32):                                        "Payload delivery",
		"sha1|" + strings.Repeat("b", 40):                                       "Payload delivery",
		"sha256|" + strings.Repeat("c", 64):                                     "Payload delivery",
		"domain|bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.onion": "Network activity",
		"url|cccc.onion/path":                                                   "Network activity",
	}
	for k, cat := range want {
		a, ok := got[k]
		if !ok || a.Category != cat {
			t.Errorf("eksik/yanlış öznitelik %s: %+v", k, a)
		}
	}
	if len(got) != len(want) || st.Attributes != len(want) || st.Findings != 2 {
		t.Fatalf("öznitelik sayısı %d (beklenen %d): %v", len(got), len(want), got)
	}
	if u := got["url|http://aaaa.onion/leak"]; u.Comment != "Combo leak" {
		t.Fatalf("URL yorumu = başlık (kontrol karakterleri temizlenmiş): %q", u.Comment)
	}
	if st.Skipped["credit_card"] != 1 || st.Skipped["api_key"] != 1 || st.Skipped["hash_length"] != 1 {
		t.Fatalf("atlananlar: %v", st.Skipped)
	}
	b, _ := json.Marshal(ev)
	if strings.Contains(string(b), "4111111111111111") || strings.Contains(string(b), "sk_live_SECRET") {
		t.Fatal("kart numarası / sır MISP'e gönderilmemeli")
	}
	if len(ev.Tag) != 2 || ev.Tag[0].Name != "tlp:amber" {
		t.Fatalf("etiketler: %v", ev.Tag)
	}
}

func TestThreatLevel(t *testing.T) {
	for crit, want := range map[int]int{5: 1, 4: 1, 3: 2, 2: 3, 1: 3, 0: 3} {
		if ThreatLevel(crit) != want {
			t.Errorf("ThreatLevel(%d) = %d, beklenen %d", crit, ThreatLevel(crit), want)
		}
	}
}

func TestClient_GuardsAndLimits(t *testing.T) {
	if _, err := New(Config{BaseURL: "https://127.0.0.1", APIKey: "k"}); err == nil {
		t.Fatal("allowPrivate kapalıyken loopback reddedilmeli")
	}
	if _, err := New(Config{BaseURL: "https://misp.example.org", APIKey: "bad key"}); err == nil {
		t.Fatal("geçersiz anahtar reddedilmeli")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect/servers/getVersion":
			http.Redirect(w, r, "https://evil.example/steal", http.StatusFound)
		case "/huge/servers/getVersion":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":"` + strings.Repeat("x", MaxResponseBytes) + `"}`))
		case "/html/servers/getVersion":
			_, _ = w.Write([]byte("<html>login</html>"))
		case "/denied/servers/getVersion":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"name":"Authentication failed.","message":"Authentication failed.\nPlease check key"}`))
		case "/slow/servers/getVersion":
			time.Sleep(300 * time.Millisecond)
			_, _ = w.Write([]byte(`{"version":"2.4.190"}`))
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	mk := func(path string, timeout time.Duration) *Client {
		c, err := New(Config{BaseURL: srv.URL + path, APIKey: "k", AllowPrivate: true, VerifyTLS: true, Timeout: timeout})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	if _, err := mk("/redirect", 0).Version(ctx); err == nil || !strings.Contains(err.Error(), "yönlendirme") {
		t.Fatalf("yönlendirme izlenmemeli: %v", err)
	}
	if _, err := mk("/huge", 0).Version(ctx); err == nil || !strings.Contains(err.Error(), "büyük") {
		t.Fatalf("büyük yanıt reddedilmeli: %v", err)
	}
	if _, err := mk("/html", 0).Version(ctx); err == nil {
		t.Fatal("JSON olmayan yanıt hata olmalı")
	}
	if _, err := mk("/denied", 0).Version(ctx); err == nil || !strings.Contains(err.Error(), "HTTP 403") || strings.Contains(err.Error(), "\n") {
		t.Fatalf("hata iletisi temizlenmeli: %v", err)
	}
	if _, err := mk("/slow", 50*time.Millisecond).Version(ctx); err == nil {
		t.Fatal("zaman aşımı uygulanmalı")
	}
}

func TestClient_PrivateDestinationBlockedAtDial(t *testing.T) {
	// Genel görünen bir ad dahili bir IP'ye çözülürse (DNS rebinding) bağlantı
	// kurulmadan reddedilir: "localhost." son eki IsPrivateHost'tan kaçmak için
	// kullanılamaz, bu yüzden doğrudan dial denetimi sınanır.
	c, err := New(Config{BaseURL: "https://misp.example.org", APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	tr := c.client.Transport.(*http.Transport)
	conn, err := tr.DialContext(context.Background(), "tcp", "127.0.0.1:1")
	if err == nil {
		conn.Close()
		t.Fatal("dahili hedefe bağlantı reddedilmeliydi")
	}
	if !strings.Contains(err.Error(), "dahili") {
		t.Fatalf("beklenen SSRF reddi, gelen: %v", err)
	}
}

// Düz http'de anahtar şifresiz gider: özel ağ izni açıkken bile bir ad genel
// bir IP'ye çözülürse bağlantı kurulmaz; özel IP'lere izin verilir.
func TestClient_PlainHTTPOnlyToPrivateIPsAtDial(t *testing.T) {
	c, err := New(Config{BaseURL: "http://misp.corp.example", APIKey: "k", AllowPrivate: true})
	if err != nil {
		t.Fatal(err)
	}
	tr := c.client.Transport.(*http.Transport)
	conn, err := tr.DialContext(context.Background(), "tcp", "93.184.215.14:80")
	if err == nil {
		conn.Close()
		t.Fatal("düz http genel IP'ye bağlanmamalı")
	}
	if !errors.Is(err, errPublicPlainHTTP) {
		t.Fatalf("beklenen genel-IP reddi, gelen: %v", err)
	}
	// Özel IP'ye dial denetimi izin verir (127.0.0.1:1 kapalı port → bağlantı
	// hatası, ama denetim hatası değil).
	if _, err := tr.DialContext(context.Background(), "tcp", "127.0.0.1:1"); errors.Is(err, errPublicPlainHTTP) {
		t.Fatalf("özel IP reddedilmemeli: %v", err)
	}
}

// Doğrulama hataları iki dillidir ve sentinel'leri errors.Is ile korur.
func TestErrors_BilingualAndWrapped(t *testing.T) {
	_, err := NormalizeBaseURL("http://93.184.215.14", true)
	if !errors.Is(err, ErrInvalidURL) || !errors.Is(err, ErrPlainHTTP) {
		t.Fatalf("sentinel'ler korunmalı: %v", err)
	}
	if tr, en := Message(err, "tr"), Message(err, "en"); tr != err.Error() || !strings.HasPrefix(en, "Invalid MISP URL") || tr == en {
		t.Fatalf("iki dilli ileti: tr=%q en=%q", tr, en)
	}
	if _, err := New(Config{BaseURL: "https://misp.example.org", APIKey: "bad key"}); !errors.Is(err, ErrInvalidAPIKey) || Message(err, "en") != "Invalid MISP API key" {
		t.Fatalf("anahtar hatası: %v", err)
	}
	if _, err := ParseTags(strings.Repeat("x", maxTagRunes+1)); err == nil || !strings.Contains(Message(err, "en"), "at most") {
		t.Fatalf("etiket hatası: %v", err)
	}
	plain := errors.New("plain")
	if Message(plain, "en") != "plain" || Message(nil, "en") != "" {
		t.Fatal("iki dilli olmayan hata olduğu gibi dönmeli")
	}
}
