package capture

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"keywordhunter-mvp/pkg/shared"
)

// findChrome hiçbir platformda panic etmemeli; CHROME_BIN öncelikli olmalı.
func TestNewCapturer_ChromeDiscovery(t *testing.T) {
	_ = findChrome()
	t.Setenv("CHROME_BIN", filepath.Join(t.TempDir(), "yok"))
	c := New("127.0.0.1:9150", "", t.TempDir())
	if c.Available() {
		t.Fatal("var olmayan CHROME_BIN ile Available() false olmalı")
	}
	if c.OutDir() == "" {
		t.Fatal("outDir boş olmamalı")
	}
	if _, err := c.Capture(t.Context(), "http://x.onion"); err == nil {
		t.Fatal("chrome yokken Capture hata döndürmeli")
	}
	if c.safeHost("http://ABC.onion/p?q=1") != "abc.onion" || c.safeHost("garbage") != "garbage" {
		t.Fatal("safeHost beklenen gibi değil")
	}
}

// DNS kilidi: tüm adlar yerelde NOTFOUND'a eşlenmeli, yalnız proxy host'u
// (Tor'a ulaşmak için) hariç tutulmalı.
func TestHostResolverRules(t *testing.T) {
	cases := map[string]string{
		"tor:9050":       "MAP * ~NOTFOUND , EXCLUDE tor",
		"127.0.0.1:9150": "MAP * ~NOTFOUND , EXCLUDE 127.0.0.1",
	}
	for proxy, want := range cases {
		c := New(proxy, "", t.TempDir())
		if got := hostResolverRules(c.proxyHost()); got != want {
			t.Errorf("%s: host-resolver-rules=%q, beklenen %q", proxy, got, want)
		}
	}
}

// fakeChrome argümanlarını dosyaya yazıp hemen çıkan sahte bir "chrome".
// chromedp başlatmayı başarısız sayar ama Chromium'a gidecek gerçek komut
// satırı kaydedilmiş olur.
func fakeChrome(t *testing.T) (bin, argsFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("sahte chrome betiği POSIX sh gerektirir")
	}
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args.txt")
	bin = filepath.Join(dir, "chrome")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > '" + argsFile + "'\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argsFile
}

// Render ve Capture'ın Chromium'a geçirdiği gerçek komut satırı: Tor proxy'si,
// DNS kilidi, loopback'in proxy'yi atlamaması, WebRTC UDP kilidi ve HTTPS
// yükseltmelerinin kapatılması (http-only .onion'lar için) mevcut olmalı.
// Gerçek tarayıcı gerektirmez; bayrak adındaki bir yazım hatası da yakalanır.
func TestChromeOptions_CommandLine(t *testing.T) {
	bin, argsFile := fakeChrome(t)
	const proxy = "127.0.0.1:9150"
	c := New(proxy, bin, t.TempDir())
	target := "http://" + strings.Repeat("a", 56) + ".onion/"

	run := map[string]func() error{
		"Render":  func() error { _, err := c.Render(t.Context(), target, "", "KH-UA/1"); return err },
		"Capture": func() error { _, err := c.Capture(t.Context(), target); return err },
	}
	for name, fn := range run {
		_ = os.Remove(argsFile)
		if err := fn(); err == nil {
			t.Fatalf("%s: sahte chrome ile hata bekleniyordu", name)
		}
		raw, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatalf("%s: chrome çağrılmadı: %v", name, err)
		}
		args := map[string]bool{}
		for _, a := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			args[a] = true
		}
		for _, want := range []string{
			"--proxy-server=socks5://" + proxy,
			"--host-resolver-rules=MAP * ~NOTFOUND , EXCLUDE 127.0.0.1",
			"--proxy-bypass-list=<-loopback>",
			"--webrtc-ip-handling-policy=disable_non_proxied_udp",
			"--disable-features=Translate,BlinkGenPropertyTrees,HttpsUpgrades,HttpsFirstBalancedModeAutoEnable",
			"--headless",
		} {
			if !args[want] {
				t.Errorf("%s: komut satırında %q yok; argümanlar:\n%s", name, want, raw)
			}
		}
		ua := "--user-agent=KH-UA/1"
		if name == "Capture" {
			ua = "--user-agent=" + shared.ChromeUserAgent
		}
		if !args[ua] {
			t.Errorf("%s: %q yok", name, ua)
		}
	}
}

// Yönlendirme / JS gezinmesi sonrası iç adrese ya da hata sayfasına varış
// reddedilmeli; onion ve Tor üzerinden genel adres kabul edilmeli.
func TestCheckFinalURL(t *testing.T) {
	ok := []string{
		"http://" + strings.Repeat("a", 56) + ".onion/page",
		"https://example.com/",
	}
	bad := []string{
		"http://127.0.0.1:8080/admin", "http://localhost/", "http://[::1]:9/",
		"http://169.254.169.254/latest/meta-data/", "http://10.0.0.5/", "http://192.168.1.1/",
		"chrome-error://chromewebdata/", "about:blank", "data:text/html,x", "file:///etc/passwd", "",
	}
	for _, u := range ok {
		if err := checkFinalURL(u); err != nil {
			t.Errorf("%s kabul edilmeli: %v", u, err)
		}
	}
	for _, u := range bad {
		if checkFinalURL(u) == nil {
			t.Errorf("%q reddedilmeli", u)
		}
	}
}

func TestParseCookieHeader(t *testing.T) {
	got := parseCookieHeader(" a=1; b = x=y ;;=z; c; d= ")
	want := map[string]string{"a": "1", "b": "x=y", "d": ""}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s=%q, beklenen %q", k, got[k], v)
		}
	}
}
