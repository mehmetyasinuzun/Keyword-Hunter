package shared

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestExtractDomain(t *testing.T) {
	cases := map[string]string{
		"http://ABC.onion/x?y#z":       "abc.onion",
		"https://abc.onion?q=1":        "abc.onion",
		"http://abc.onion#top":         "abc.onion", // parça (#) host'a karışmamalı
		"abc.onion/path":               "abc.onion",
		"http://abc.onion:8080/p":      "abc.onion:8080",
		"":                             "",
		"https://sub.abc.onion/a/b?c#": "sub.abc.onion",
	}
	for in, want := range cases {
		if got := ExtractDomain(in); got != want {
			t.Errorf("ExtractDomain(%q) = %q, beklenen %q", in, got, want)
		}
	}
}

func TestTruncateBytes(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"abc", 5, "abc"},
		{"abcdef", 3, "abc"},
		{"aşb", 2, "a"}, // ş iki bayt: ortadan bölünmez
		{"aşb", 3, "aş"},
		{"😀x", 3, ""},
		{"abc", 0, ""},
		{"abc", -1, ""},
		{"ab\x94\x94\x94\x94", 5, "ab\x94\x94\x94"}, // geçersiz devam baytları: bayt sınırı
	}
	for _, c := range cases {
		got := TruncateBytes(c.in, c.n)
		if got != c.want {
			t.Errorf("TruncateBytes(%q,%d) = %q, beklenen %q", c.in, c.n, got, c.want)
		}
	}
	long := strings.Repeat("ğ", 1000)
	if got := TruncateBytes(long, 301); len(got) != 300 || !utf8.ValidString(got) {
		t.Fatalf("len=%d", len(got))
	}
}

func TestIsLowValueURL(t *testing.T) {
	low := []string{
		"http://x.onion/logo.PNG", "http://x.onion/app.js?v=3", "http://x.onion/search?q=a",
		"http://x.onion/tag/leak", "http://x.onion/wp-content/uploads/a", "http://x.onion/list?page=2",
	}
	for _, u := range low {
		if ok, reason := IsLowValueURL(u); !ok || reason == "" {
			t.Errorf("%s düşük değerli olmalı", u)
		}
	}
	for _, u := range []string{"http://x.onion/", "http://x.onion/forum/thread-12", "http://x.onion/avatar"} {
		if ok, reason := IsLowValueURL(u); ok {
			t.Errorf("%s düşük değerli sayılmamalı (%s)", u, reason)
		}
	}
}

func TestDetectChallenge_Kinds(t *testing.T) {
	cases := []struct {
		title, body, kind string
	}{
		{"Just a moment...", "", "Cloudflare"},
		{"", `<div class="g-recaptcha">`, "reCAPTCHA"},
		{"", "DDoS-Guard", "DDoS-Guard"},
		{"Giriş", "Lütfen insan olduğunuzu doğrulayın", "doğrulama"},
		{"", "<div class=\"h-captcha\">", "hCaptcha"},
		{"Forum", "Welcome back, fresh dumps inside", ""},
	}
	for _, c := range cases {
		found, kind := DetectChallenge(c.title, c.body)
		if kind != c.kind || found != (c.kind != "") {
			t.Errorf("DetectChallenge(%q,%q) = %v,%q; beklenen %q", c.title, c.body, found, kind, c.kind)
		}
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestIsRetryableErrorAndBackoff(t *testing.T) {
	retry := []error{
		timeoutErr{}, io.EOF, errors.New("read: connection reset by peer"),
		errors.New("dial tcp: connection refused"), errors.New("socks connect tcp: general failure"),
		errors.New("unexpected EOF"), errors.New("context deadline exceeded"),
	}
	for _, e := range retry {
		if !IsRetryableError(e) {
			t.Errorf("%v yeniden denenebilir olmalı", e)
		}
	}
	for _, e := range []error{nil, errors.New("x509: certificate signed by unknown authority"), errors.New("HTTP 404")} {
		if IsRetryableError(e) {
			t.Errorf("%v yeniden denenmemeli", e)
		}
	}
	for attempt := 1; attempt < 10; attempt++ {
		d := CalculateBackoff(attempt)
		if d <= 0 || d > MaxBackoffDuration+MaxBackoffDuration/4 {
			t.Errorf("backoff(%d) = %v sınır dışı", attempt, d)
		}
	}
	if RandomUserAgent() == "" {
		t.Error("UA boş")
	}
}

func TestDoWithRetryN_NonRetryableAndGuards(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.NotFound(w, r)
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := DoWithRetry(srv.Client(), req)
	if err != nil || resp.StatusCode != 404 || hits != 1 {
		t.Fatalf("404 yeniden denenmemeli: resp=%v err=%v hits=%d", resp, err, hits)
	}
	closeBody(resp)

	// Gövdeli istek GetBody olmadan reddedilir (yeniden denemede gövde tükenirdi).
	body := io.NopCloser(strings.NewReader("x"))
	req2, _ := http.NewRequest(http.MethodPost, srv.URL, body)
	req2.GetBody = nil
	if resp, err := DoWithRetryN(srv.Client(), req2, 2); err == nil {
		closeBody(resp)
		t.Fatal("GetBody'siz gövdeli istek reddedilmeli")
	}

	// İptal edilmiş bağlam denemeden döner.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req3, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if resp, err := DoWithRetryN(srv.Client(), req3, 0); !errors.Is(err, context.Canceled) {
		closeBody(resp)
		t.Fatalf("iptal: %v", err)
	}
	if hits != 1 {
		t.Fatalf("iptal/ret durumlarında istek gitmemeli: %d", hits)
	}

	// Yeniden denenemez ağ hatası tek denemede sınıflandırılarak döner.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	req4, _ := http.NewRequest(http.MethodGet, "http://"+addr, nil)
	if resp, err := DoWithRetryN(&http.Client{Timeout: time.Second}, req4, 1); !errors.Is(err, ErrTorUnreachable) {
		closeBody(resp)
		t.Fatalf("connection refused → ErrTorUnreachable: %v", err)
	}
}

func TestNewHTTPClient_UsesSocksNotEnvProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	c, err := NewHTTPClient("127.0.0.1:9050")
	if err != nil {
		t.Fatal(err)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok || tr.Proxy != nil || tr.DialContext == nil || c.Timeout != DefaultHTTPTimeout {
		t.Fatalf("Tor istemcisi ortam proxy'si kullanmamalı: %+v", tr)
	}
	if _, err := NewHTTPClient("bad host:%zz"); err == nil {
		t.Fatal("bozuk proxy adresi hata vermeli")
	}
}

func TestClearnetClients_NoRedirectAndPrivateGuard(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer srv.Close()
	plain := NewPlainClearnetClient(5 * time.Second)
	resp, err := plain.Get(srv.URL)
	if err != nil || resp.StatusCode != http.StatusFound {
		t.Fatalf("yönlendirme takip edilmemeli: %v %v", resp, err)
	}
	closeBody(resp)
	safe := NewSafeClearnetClient(5 * time.Second)
	if resp, err := safe.Get(srv.URL); err == nil || !errors.Is(err, ErrPrivateDestination) {
		closeBody(resp)
		t.Fatalf("güvenli istemci loopback'e bağlanmamalı: %v", err)
	}
}

func TestBroadcaster_DeliversAndDropsSlowClients(t *testing.T) {
	b := NewBroadcaster()
	fast := make(chan string, 4)
	slow := make(chan string) // alıcısız: ilk mesajda düşürülür
	b.Register(fast)
	b.Register(slow)
	b.BroadcastLog("info", "merhaba", "Tordex")

	select {
	case msg := <-fast:
		var m LogMessage
		if err := json.Unmarshal([]byte(msg), &m); err != nil || m.Type != "info" || m.Message != "merhaba" || m.Engine != "Tordex" {
			t.Fatalf("mesaj: %q %v", msg, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("mesaj gelmedi")
	}
	// Eşitleme: yayıncı tek döngüdür; bu kayıt ancak yayın turu bittikten sonra
	// alınır. Aksi hâlde aşağıdaki alıcı, yayıncının engellemeyen gönderimine
	// yetişip mesajı alabiliyordu (yarış; test ara sıra düşüyordu).
	b.Register(make(chan string, 1))
	select {
	case _, ok := <-slow:
		if ok {
			t.Fatal("yavaş istemci kapatılmalı")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("yavaş istemci düşürülmedi")
	}
	b.Unregister(fast)
	if _, ok := <-fast; ok {
		t.Fatal("kayıt silinen kanal kapanmalı")
	}
	b.Unregister(slow) // zaten düşürülmüş: ikinci kapatma paniği olmamalı
}

// closeBody beklenmedik biçimde dönen bir yanıtın gövdesini kapatır (nil güvenli).
func closeBody(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
}
