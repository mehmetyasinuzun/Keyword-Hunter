package capture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"keywordhunter-mvp/pkg/shared"
)

// Gerçek tarayıcı testleri: CHROME_BIN verilmemişse atlanır (CI'da Chromium yok).
// Tor yerine sahte bir SOCKS5 proxy kullanılır; böylece Capture/Render'ın
// genel (public) yolu — .onion koruması, proxy-server + host-resolver-rules
// bayrakları, çerez/UA enjeksiyonu, zaman aşımı, PNG — uçtan uca sınanır.
//
//	CHROME_BIN=/path/to/chrome go test -run Browser ./pkg/capture

// testOnionHost IsOnionURL'den geçen (56 karakter base32) sahte v3 adres.
const testOnionHost = "khtestaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.onion"

func requireChrome(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("CHROME_BIN")
	if bin == "" {
		t.Skip("CHROME_BIN boş — gerçek tarayıcı testi atlandı")
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("CHROME_BIN bulunamadı (%s): %v", bin, err)
	}
	return bin
}

// fakeSOCKS5 yalnız testOnionHost'a giden CONNECT isteklerini hedef sunucuya
// yönlendiren minimal SOCKS5 (RFC 1928, kimlik doğrulamasız) proxy'si.
// Gelen her isteğin adres türünü ve host'unu kaydeder: tarayıcı adı yerelde
// çözmeyip proxy'ye (uzak DNS) gönderiyorsa ATYP=3 (alan adı) görülür.
type fakeSOCKS5 struct {
	ln       net.Listener
	upstream string // httptest sunucusunun adresi

	mu       sync.Mutex
	requests []string // "domain:host:port" | "ipv4:..." | "ipv6:..."
	wg       sync.WaitGroup
}

func startFakeSOCKS5(t *testing.T, upstream string) *fakeSOCKS5 {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeSOCKS5{ln: ln, upstream: upstream}
	p.wg.Go(func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			p.wg.Go(func() { p.serve(conn) })
		}
	})
	t.Cleanup(func() {
		_ = ln.Close()
		p.wg.Wait()
	})
	return p
}

func (p *fakeSOCKS5) addr() string { return p.ln.Addr().String() }

func (p *fakeSOCKS5) seen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.requests...)
}

func (p *fakeSOCKS5) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	// Selamlaşma: VER NMETHODS METHODS... → "kimlik doğrulama yok"
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(conn, hdr); err != nil || hdr[0] != 5 {
		return
	}
	if _, err := io.ReadFull(conn, make([]byte, hdr[1])); err != nil {
		return
	}
	if _, err := conn.Write([]byte{5, 0}); err != nil {
		return
	}

	// İstek: VER CMD RSV ATYP DST.ADDR DST.PORT
	req := make([]byte, 4)
	if _, err := io.ReadFull(conn, req); err != nil || req[0] != 5 || req[1] != 1 {
		return
	}
	var kind, host string
	switch req[3] {
	case 1:
		b := make([]byte, 4)
		if _, err := io.ReadFull(conn, b); err != nil {
			return
		}
		kind, host = "ipv4", net.IP(b).String()
	case 3:
		n := make([]byte, 1)
		if _, err := io.ReadFull(conn, n); err != nil {
			return
		}
		b := make([]byte, n[0])
		if _, err := io.ReadFull(conn, b); err != nil {
			return
		}
		kind, host = "domain", string(b)
	case 4:
		b := make([]byte, 16)
		if _, err := io.ReadFull(conn, b); err != nil {
			return
		}
		kind, host = "ipv6", net.IP(b).String()
	default:
		return
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(conn, pb); err != nil {
		return
	}
	port := binary.BigEndian.Uint16(pb)

	p.mu.Lock()
	p.requests = append(p.requests, fmt.Sprintf("%s:%s:%d", kind, host, port))
	p.mu.Unlock()

	// Yalnız sahte onion hedefi yönlendirilir; geri kalan her şey reddedilir
	// (0x02: kural setince izin verilmedi).
	if kind != "domain" || host != testOnionHost {
		_, _ = conn.Write([]byte{5, 2, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	up, err := net.Dial("tcp", p.upstream)
	if err != nil {
		_, _ = conn.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer func() { _ = up.Close() }()
	if _, err := conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, conn); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, up); done <- struct{}{} }()
	<-done
}

// browserFixture sahte onion sitesi + SOCKS5 proxy + kısa bekleme süreli Capturer.
type browserFixture struct {
	c     *Capturer
	proxy *fakeSOCKS5
	base  string // http://<testOnionHost>

	mu     sync.Mutex
	uas    []string                    // sunucunun gördüğü User-Agent başlıkları
	custom map[string]http.HandlerFunc // testin eklediği ek sayfalar (bkz. handle)
}

// handle sahte onion sitesine test özel bir sayfa ekler.
func (f *browserFixture) handle(path string, h http.HandlerFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.custom == nil {
		f.custom = map[string]http.HandlerFunc{}
	}
	f.custom[path] = h
}

func newBrowserFixture(t *testing.T) *browserFixture {
	t.Helper()
	bin := requireChrome(t)
	f := &browserFixture{base: "http://" + testOnionHost}

	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.uas = append(f.uas, r.UserAgent())
		f.mu.Unlock()
		if r.Host != testOnionHost {
			http.Error(w, "beklenmeyen Host: "+r.Host, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/go":
			http.Redirect(w, r, "/page", http.StatusFound)
		case "/page":
			// Sunucu tarafı: gelen Cookie + UA sayfaya yansıtılır. İstemci
			// tarafı: JS çalışırsa #js düğümü ve document.cookie metne düşer.
			fmt.Fprintf(w, `<!doctype html><html><head><title>  KH Render Test  </title></head>
<body><h1>Merhaba onion</h1>
<pre id="srv-cookie">%s</pre><pre id="srv-ua">%s</pre>
<p id="js">js-yok</p>
<script>document.getElementById("js").textContent = "JS-OK " + document.cookie;</script>
</body></html>`, html.EscapeString(r.Header.Get("Cookie")), html.EscapeString(r.UserAgent()))
		case "/challenge":
			fmt.Fprint(w, `<!doctype html><html><head><title>Just a moment...</title></head>
<body style="height:3000px"><p>Checking your browser before accessing the site.</p></body></html>`)
		case "/slow":
			select {
			case <-release:
			case <-r.Context().Done():
			}
		default:
			f.mu.Lock()
			h := f.custom[r.URL.Path]
			f.mu.Unlock()
			if h == nil {
				http.NotFound(w, r)
				return
			}
			h(w, r)
		}
	})
	srv := httptest.NewServer(mux)
	// Kapanış sırası (LIFO): önce yavaş handler serbest, sonra sunucu.
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	f.proxy = startFakeSOCKS5(t, srv.Listener.Addr().String())
	f.c = New(f.proxy.addr(), bin, t.TempDir())
	f.c.captureSettle = 300 * time.Millisecond
	f.c.renderSettle = 300 * time.Millisecond
	f.c.timeout = 45 * time.Second
	return f
}

func (f *browserFixture) userAgents() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.uas...)
}

// assertOnlyRemoteDNS: proxy'ye gelen her istek alan adı (ATYP=3) olmalı ve
// hedef onion host'u görülmeli — tarayıcı hedefi proxy'ye ad olarak iletir,
// IP'ye kendisi çözüp göndermez. (DNS kilidinin bayrak olarak Chromium'a
// geçtiği ayrıca TestChromeOptions_CommandLine'da doğrulanır; socks5:// proxy'de
// Chromium bayrak olmadan da adı iletir, bu yüzden buradaki kontrol tek başına
// --host-resolver-rules'u kanıtlamaz.)
func (f *browserFixture) assertOnlyRemoteDNS(t *testing.T) {
	t.Helper()
	reqs := f.proxy.seen()
	want := fmt.Sprintf("domain:%s:80", testOnionHost)
	found := false
	for _, r := range reqs {
		if !strings.HasPrefix(r, "domain:") {
			t.Errorf("proxy'ye alan adı yerine adres geldi (yerel DNS sızıntısı?): %s", r)
		}
		if r == want {
			found = true
		}
	}
	if !found {
		t.Errorf("proxy %q isteğini görmedi; görülenler: %v", want, reqs)
	}
}

func TestBrowser_RenderThroughSOCKS(t *testing.T) {
	f := newBrowserFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	const ua = "KH-Browser-Test/1.0"
	res, err := f.c.Render(ctx, f.base+"/go", "kh_sid=abc123; lang=tr", ua)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if res.Title != "KH Render Test" {
		t.Errorf("başlık kırpılmış olmalı: %q", res.Title)
	}
	if res.Final != f.base+"/page" {
		t.Errorf("yönlendirme sonrası son URL: %q", res.Final)
	}
	// Çerezler hem HTTP isteğinde (sunucu) hem document.cookie'de (JS) görünmeli.
	for _, ck := range []string{"kh_sid=abc123", "lang=tr"} {
		if !strings.Contains(res.HTML, ck) {
			t.Errorf("sunucu çerezi görmedi (%s): %s", ck, res.HTML)
		}
		if !strings.Contains(res.Text, ck) {
			t.Errorf("JS çerezi görmedi (%s): %q", ck, res.Text)
		}
	}
	if !strings.Contains(res.Text, "JS-OK") || strings.Contains(res.Text, "js-yok") {
		t.Errorf("JS çalışmamış görünüyor: %q", res.Text)
	}
	if !strings.HasPrefix(res.HTML, "<html") || !strings.Contains(res.HTML, "Merhaba onion") {
		t.Errorf("outerHTML beklenen gibi değil: %.200s", res.HTML)
	}
	for _, got := range f.userAgents() {
		if got != ua {
			t.Errorf("profil User-Agent'ı uygulanmadı: %q", got)
		}
	}
	f.assertOnlyRemoteDNS(t)

	// ScraperRenderer sarmalayıcısı aynı sonucu döndürmeli.
	sr := f.c.AsScraperRenderer()
	if sr == nil {
		t.Fatal("chromium varken AsScraperRenderer nil olmamalı")
	}
	h, txt, title, err := sr.Render(ctx, f.base+"/page", "", "")
	if err != nil || title != "KH Render Test" || !strings.Contains(h, "Merhaba onion") || !strings.Contains(txt, "JS-OK") {
		t.Errorf("ScraperRenderer: title=%q err=%v", title, err)
	}
}

func TestBrowser_CaptureThroughSOCKS(t *testing.T) {
	f := newBrowserFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	shot, err := f.c.Capture(ctx, f.base+"/challenge")
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if shot.Title != "Just a moment..." {
		t.Errorf("başlık: %q", shot.Title)
	}
	if !shot.Challenge || shot.ChallengeKind != "Cloudflare" {
		t.Errorf("engel tespiti: challenge=%v kind=%q", shot.Challenge, shot.ChallengeKind)
	}

	data, err := os.ReadFile(filepath.Join(f.c.OutDir(), shot.Path))
	if err != nil {
		t.Fatalf("PNG dosyası: %v", err)
	}
	if len(data) != shot.Bytes {
		t.Errorf("Bytes=%d, dosya=%d", shot.Bytes, len(data))
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != shot.SHA256 {
		t.Error("SHA256 dosya içeriğiyle uyuşmuyor")
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("geçerli PNG değil: %v", err)
	}
	if b := img.Bounds(); b.Dx() != shot.Width || b.Dy() != shot.Height || shot.Width != shotWidth || shot.Height != shotHeight {
		t.Errorf("boyut: görüntü %dx%d, Shot %dx%d", b.Dx(), b.Dy(), shot.Width, shot.Height)
	}
	for _, got := range f.userAgents() {
		if got != shared.ChromeUserAgent {
			t.Errorf("Capture motor-tutarlı UA kullanmalı: %q", got)
		}
	}
	f.assertOnlyRemoteDNS(t)
}

// Zaman aşımı tarayıcı dahil tüm işlemi sınırlamalı; asılı kalan bir .onion
// sayfası Render'ı süresiz bekletmemeli.
func TestBrowser_RenderTimeout(t *testing.T) {
	f := newBrowserFixture(t)
	f.c.timeout = 3 * time.Second

	start := time.Now()
	_, err := f.c.Render(t.Context(), f.base+"/slow", "", "")
	if err == nil {
		t.Fatal("asılı sayfa için hata bekleniyordu")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("DeadlineExceeded sarmalanmalı: %v", err)
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Errorf("zaman aşımı uygulanmadı: %v", d)
	}
}

// Proxy'nin reddettiği (onion olmayan / ulaşılamayan) hedef sessizce boş sonuç
// değil, hata üretmeli.
func TestBrowser_UnreachableOnion(t *testing.T) {
	f := newBrowserFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	other := "http://" + strings.Repeat("b", 56) + ".onion/"
	if _, err := f.c.Render(ctx, other, "", ""); err == nil {
		t.Fatal("ulaşılamayan onion için Render hata döndürmeli")
	}
	if _, err := f.c.Capture(ctx, other); err == nil {
		t.Fatal("ulaşılamayan onion için Capture hata döndürmeli")
	}
}

// loopbackServer yalnız bu makineden erişilebilen "iç" bir HTTP servisi
// (127.0.0.1 ve mümkünse [::1]) — Tor üzerinden asla ulaşılamamalı. Gelen
// istekleri sayar.
type loopbackServer struct {
	port string
	mu   sync.Mutex
	hits []string
}

const internalSecret = "SECRET-INTERNAL-TOKEN-42"

func startLoopbackServer(t *testing.T) (*loopbackServer, bool) {
	t.Helper()
	ls := &loopbackServer{}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ls.mu.Lock()
		ls.hits = append(ls.hits, r.Host+r.URL.Path)
		ls.mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		fmt.Fprint(w, `<!doctype html><html><head><title>INTERNAL-ADMIN</title></head><body>`+internalSecret+`</body></html>`)
	})
	ln4, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, ls.port, _ = net.SplitHostPort(ln4.Addr().String())
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln4) }()
	v6 := false
	if ln6, err := net.Listen("tcp", "[::1]:"+ls.port); err == nil {
		v6 = true
		go func() { _ = srv.Serve(ln6) }()
	}
	t.Cleanup(func() { _ = srv.Close() })
	return ls, v6
}

func (ls *loopbackServer) seen() []string {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	return append([]string(nil), ls.hits...)
}

// Düşman bir .onion sayfası tarayıcıyı loopback'teki iç servislere
// yönlendirememeli / yükletememeli: Chromium loopback'i (ve 169.254/16
// link-local'i) varsayılan olarak proxy'yi atlayıp doğrudan bağlar. Bu,
// sunucudaki iç panelin içeriğini Render/Capture çıktısına (DB, PNG) sızdırırdı.
func TestBrowser_LoopbackSSRFBlocked(t *testing.T) {
	f := newBrowserFixture(t)
	internal, v6 := startLoopbackServer(t)

	targets := map[string]string{
		"/r-v4":    "http://127.0.0.1:" + internal.port + "/admin",
		"/r-local": "http://localhost:" + internal.port + "/admin",
	}
	if v6 {
		targets["/r-v6"] = "http://[::1]:" + internal.port + "/admin"
	}
	for path, to := range targets {
		f.handle(path, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, to, http.StatusFound)
		})
	}
	// JS ile sonradan gezinme ve alt kaynaklar (iframe/img/fetch).
	jsTarget := "http://127.0.0.1:" + internal.port + "/js-nav"
	f.handle("/js-nav", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<!doctype html><html><head><title>nav</title></head><body>
<script>location.href = %q;</script></body></html>`, jsTarget)
	})
	sub := "http://127.0.0.1:" + internal.port
	f.handle("/embed", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<!doctype html><html><head><title>embed</title></head><body>
<iframe src="%[1]s/iframe"></iframe><img src="%[1]s/img.png">
<script>fetch("%[1]s/fetch").catch(function(){});</script></body></html>`, sub)
	})

	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	for path := range targets {
		if res, err := f.c.Render(ctx, f.base+path, "", ""); err == nil {
			t.Errorf("Render %s loopback'e yönlenmeyi kabul etti: final=%q title=%q sızıntı=%v",
				path, res.Final, res.Title, strings.Contains(res.HTML, internalSecret))
		}
		if shot, err := f.c.Capture(ctx, f.base+path); err == nil {
			t.Errorf("Capture %s loopback'e yönlenmeyi kabul etti: title=%q", path, shot.Title)
		}
	}
	if res, err := f.c.Render(ctx, f.base+"/js-nav", "", ""); err == nil {
		t.Errorf("Render JS gezinmesi loopback'te bitti: final=%q title=%q", res.Final, res.Title)
	}
	if res, err := f.c.Render(ctx, f.base+"/embed", "", ""); err != nil {
		t.Errorf("Render /embed (sayfa onion): %v", err)
	} else if strings.Contains(res.HTML, internalSecret) {
		t.Error("iç servis içeriği render çıktısına sızdı")
	}
	if hits := internal.seen(); len(hits) > 0 {
		t.Errorf("tarayıcı loopback iç servisine proxy'siz ulaştı: %v", hits)
	}
}

// nonLoopbackIPv4 makinenin loopback olmayan ilk IPv4 adresi ("" yoksa).
func nonLoopbackIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok {
			if v4 := ipn.IP.To4(); v4 != nil && !v4.IsLoopback() {
				return v4.String()
			}
		}
	}
	return ""
}

// Düşman bir .onion sayfasının JS'i WebRTC ile (IP sabitli STUN sunucusu,
// DNS gerektirmez) proxy'yi atlayan UDP gönderememeli; aksi halde saldırganın
// STUN sunucusu platformun gerçek çıkış IP'sini öğrenir. SOCKS5 UDP taşımadığı
// için WebRTC UDP'si tamamen kapalı olmalı.
func TestBrowser_WebRTCNoDirectUDP(t *testing.T) {
	f := newBrowserFixture(t)
	f.c.renderSettle = 3 * time.Second

	var mu sync.Mutex
	var pkts []string
	var stuns []string
	for _, ip := range []string{"127.0.0.1", nonLoopbackIPv4()} {
		if ip == "" {
			continue
		}
		pc, err := net.ListenPacket("udp4", net.JoinHostPort(ip, "0"))
		if err != nil {
			continue
		}
		t.Cleanup(func() { _ = pc.Close() })
		stuns = append(stuns, `{urls:"stun:`+pc.LocalAddr().String()+`"}`)
		go func() {
			buf := make([]byte, 2048)
			for {
				n, from, err := pc.ReadFrom(buf)
				if err != nil {
					return
				}
				mu.Lock()
				pkts = append(pkts, fmt.Sprintf("%d bayt %s → %s", n, from, pc.LocalAddr()))
				mu.Unlock()
			}
		}()
	}
	if len(stuns) == 0 {
		t.Skip("UDP dinleyicisi açılamadı")
	}
	f.handle("/rtc", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<!doctype html><html><head><title>rtc</title></head><body><p id="o">start</p>
<script>try{var pc=new RTCPeerConnection({iceServers:[%s]});pc.createDataChannel("x");
pc.onicecandidate=function(e){if(e.candidate){document.getElementById("o").textContent+=" | "+e.candidate.candidate}};
pc.createOffer().then(function(o){return pc.setLocalDescription(o)});document.getElementById("o").textContent+=" rtc-started"}
catch(e){document.getElementById("o").textContent="ERR "+e}</script></body></html>`, strings.Join(stuns, ","))
	})
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	res, err := f.c.Render(ctx, f.base+"/rtc", "", "")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(res.Text, "rtc-started") {
		t.Fatalf("sayfa WebRTC denemesini başlatmadı, test anlamsız: %q", res.Text)
	}
	time.Sleep(500 * time.Millisecond) // geç gelen paketler için
	mu.Lock()
	defer mu.Unlock()
	if len(pkts) > 0 {
		t.Errorf("WebRTC proxy'yi atlayıp doğrudan UDP gönderdi (sayfa: %q): %v", res.Text, pkts)
	}
}
