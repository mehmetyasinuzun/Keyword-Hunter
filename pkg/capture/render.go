package capture

import (
	"context"
	"fmt"
	"keywordhunter-mvp/pkg/shared"
	"net/url"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// RenderResult headless Chromium ile yüklenmiş bir sayfanın çıktısı.
type RenderResult struct {
	HTML  string // document.documentElement.outerHTML (JS çalıştıktan sonra)
	Text  string // document.body.innerText
	Title string
	Final string // yönlendirme sonrası son URL
}

// maxRenderHTML render çıktısı için üst sınır (bayt).
const maxRenderHTML = 6 << 20

// Render sayfayı Tor üzerinden Chromium'da yükler, JavaScript'in çalışmasını
// bekler ve oluşan DOM'u döndürür. cookieHeader "a=1; b=2" biçimindeyse
// hedef host için çerez olarak enjekte edilir (oturumlu/giriş duvarlı siteler).
// Chromium yoksa hata döner; çağıran düz HTTP çıktısına geri düşmelidir.
func (c *Capturer) Render(ctx context.Context, targetURL, cookieHeader, userAgent string) (*RenderResult, error) {
	// Derinlemesine savunma: yalnız .onion hedefleri. host-resolver-rules DNS'i
	// Tor'a zorlar ama düz IP (örn. 127.0.0.1) DNS gerektirmez; böyle bir hedef
	// Tor'a gitse bile koruma Tor'un iç-adres reddine bağımlı kalmasın diye
	// burada kendimiz reddediyoruz. Sayfa içi yönlendirmeler için bkz.
	// chromeOptions (proxy-bypass-list) ve checkFinalURL.
	if !shared.IsOnionURL(targetURL) {
		return nil, fmt.Errorf("yalnız .onion hedefleri render edilebilir")
	}
	if !c.Available() {
		return nil, fmt.Errorf("chromium bulunamadı — JS render devre dışı")
	}
	u, err := url.Parse(targetURL)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("geçersiz URL")
	}

	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	opts := c.chromeOptions()
	if userAgent != "" {
		opts = append(opts, chromedp.UserAgent(userAgent))
	}
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()
	taskCtx, cancelTask := chromedp.NewContext(allocCtx)
	defer cancelTask()
	// Zaman aşımı ilk Run'ın bağlamında: tarayıcının açılışı dahil tüm işlem
	// sınırlıdır ve süre dolunca tarayıcı da kapanır (chromedp tarayıcı ömrünü
	// ilk Run'ın bağlamına bağlar).
	runCtx, cancelRun := context.WithTimeout(taskCtx, c.timeout)
	defer cancelRun()

	res, err := chromedp.Run(runCtx, renderAction(targetURL, u.Hostname(), parseCookieHeader(cookieHeader), c.renderSettle))
	if err != nil {
		return nil, fmt.Errorf("render başarısız: %w", err)
	}
	if err := checkFinalURL(res.Final); err != nil {
		return nil, fmt.Errorf("render başarısız: %w", err)
	}
	if len(res.HTML) > maxRenderHTML {
		res.HTML = res.HTML[:maxRenderHTML]
	}
	res.Title = strings.TrimSpace(res.Title)
	return &res, nil
}

// renderCookieTTL enjekte edilen oturum çerezlerinin ömrü.
const renderCookieTTL = 24 * time.Hour

// renderAction Render'ın sekmede çalıştırdığı adım dizisi: çerezleri hedef host
// için ayarla, sayfaya git, JS'in oturması için settle kadar bekle, ardından
// başlık / son URL / görünür metin / DOM'u oku. Tarayıcı bayraklarından
// (Tor proxy, UA) bağımsızdır; onlar allocator seçeneklerinde verilir.
func renderAction(targetURL, host string, cookies map[string]string, settle time.Duration) chromedp.Action[RenderResult] {
	return func(ctx context.Context, t *chromedp.Target) (RenderResult, error) {
		var res RenderResult
		if len(cookies) > 0 {
			// cdp.TimeSinceEpoch artık düz float64 saniye; eski kodlayıcı da
			// tam saniye gönderiyordu.
			exp := cdp.TimeSinceEpoch(float64(time.Now().Add(renderCookieTTL).Unix()))
			for name, val := range cookies {
				if _, err := cdp.Call(ctx, t, network.SetCookie, network.SetCookieParams{
					Name:    name,
					Value:   val,
					Domain:  host,
					Path:    "/",
					Expires: exp,
				}); err != nil {
					return res, fmt.Errorf("çerez ayarlanamadı (%s): %w", name, err)
				}
			}
		}
		// .onion + JS: ağ ve script'lerin oturması için settle kadar beklenir.
		if _, err := chromedp.Steps(chromedp.Navigate(targetURL), chromedp.Sleep(settle))(ctx, t); err != nil {
			return res, err
		}
		var err error
		if res.Title, err = chromedp.Title()(ctx, t); err != nil {
			return res, err
		}
		if res.Final, err = chromedp.Location()(ctx, t); err != nil {
			return res, err
		}
		if res.Text, err = chromedp.Evaluate[string](`(document.body && document.body.innerText ? document.body.innerText : "").slice(0, 400000)`)(ctx, t); err != nil {
			return res, err
		}
		if res.HTML, err = chromedp.OuterHTML(chromedp.CSS("html"))(ctx, t); err != nil {
			return res, err
		}
		return res, nil
	}
}

// chromeOptions Capture ve Render için ortak Chromium bayrakları: temel
// bayraklar + Tor SOCKS proxy + sızıntı kilitleri. Sayfalar düşman kabul
// edilir; tarayıcının Tor dışına çıkabildiği her yol kapatılır:
//   - host-resolver-rules: tüm isim çözümü yerelde NOTFOUND'a eşlenir (yalnız
//     proxy host'u hariç); .onion dahil her ad SOCKS5 üzerinden uzak (Tor)
//     tarafta çözülür, yerel DNS'e sızmaz.
//   - proxy-bypass-list=<-loopback>: Chromium loopback'i (127/8, localhost,
//     [::1]) ve link-local'i (169.254/16 — bulut metadata servisi) varsayılan
//     olarak proxy'yi ATLAYARAK doğrudan bağlar. Bu kural o örtük istisnaları
//     kaldırır; düşman sayfanın 127.0.0.1'e yönlendirmesi / iframe'i de Tor'a
//     gider ve Tor iç adresleri reddeder (SSRF).
//   - webrtc-ip-handling-policy=disable_non_proxied_udp: WebRTC, IP sabitli
//     bir STUN sunucusuyla (DNS gerekmez) proxy'yi atlayan UDP gönderip
//     sunucunun gerçek çıkış IP'sini açığa çıkarabilir. SOCKS5 UDP taşımadığı
//     için bu politika WebRTC UDP'sini tamamen kapatır.
func (c *Capturer) chromeOptions() []chromedp.ExecAllocatorOption {
	return append(baseChromeOptions(c.chromePath),
		chromedp.Flag("proxy-server", "socks5://"+c.torProxy),
		chromedp.Flag("proxy-bypass-list", "<-loopback>"),
		chromedp.Flag("host-resolver-rules", hostResolverRules(c.proxyHost())),
		chromedp.Flag("webrtc-ip-handling-policy", "disable_non_proxied_udp"),
	)
}

// checkFinalURL yönlendirmeler / JS gezinmesi sonrası sayfanın vardığı adresi
// denetler (derinlemesine savunma; asıl engel proxy bayraklarıdır). Yalnız
// başlangıç URL'si .onion olarak doğrulanır; düşman bir sayfa tarayıcıyı iç bir
// adrese ya da hata sayfasına (chrome-error://) götürmüşse çıktı saklanmaz.
// Tor üzerinden genel bir adrese (clearnet) varış eskisi gibi kabul edilir.
func checkFinalURL(final string) error {
	u, err := url.Parse(final)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return fmt.Errorf("sayfa beklenmeyen bir adreste sonlandı: %q", final)
	}
	if shared.IsPrivateHost(u.Hostname()) {
		return fmt.Errorf("sayfa iç bir adrese yönlendi, sonuç atıldı: %s", u.Host)
	}
	return nil
}

// hostResolverRules yerel DNS'i kapatan --host-resolver-rules değeri.
func hostResolverRules(proxyHost string) string {
	return fmt.Sprintf("MAP * ~NOTFOUND , EXCLUDE %s", proxyHost)
}

// baseChromeOptions proxy'den bağımsız headless Chromium bayrakları.
func baseChromeOptions(chromePath string) []chromedp.ExecAllocatorOption {
	return append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromePath),
		chromedp.NoSandbox,
		chromedp.DisableGPU,
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.Flag("mute-audio", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		// Varsayılan listeye (Translate,BlinkGenPropertyTrees) ek olarak HTTPS
		// yükseltmeleri kapatılır: güncel Chromium http:// gezinmesini önce
		// https://'e çevirir; çoğu .onion yalnız 80'de dinlediği için gezinme
		// ERR_BLOCKED_BY_CLIENT / ERR_TOO_MANY_REDIRECTS ile düşer. Onion
		// bağlantısı zaten uçtan uca şifreli; yükseltme yalnız fazladan Tor
		// devresi açar.
		chromedp.Flag("disable-features", "Translate,BlinkGenPropertyTrees,HttpsUpgrades,HttpsFirstBalancedModeAutoEnable"),
		chromedp.WindowSize(shotWidth, shotHeight),
	)
}

// parseCookieHeader "a=1; b=2" → map.
func parseCookieHeader(h string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(h, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			continue
		}
		out[k] = strings.TrimSpace(v)
	}
	return out
}
