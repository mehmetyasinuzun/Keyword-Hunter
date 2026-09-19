package capture

import (
	"context"
	"fmt"
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
	runCtx, cancelRun := context.WithTimeout(taskCtx, c.timeout)
	defer cancelRun()

	var res RenderResult
	tasks := chromedp.Tasks{}
	if cookies := parseCookieHeader(cookieHeader); len(cookies) > 0 {
		host := u.Hostname()
		tasks = append(tasks, chromedp.ActionFunc(func(ctx context.Context) error {
			exp := cdp.TimeSinceEpoch(time.Now().Add(24 * time.Hour))
			for name, val := range cookies {
				if err := network.SetCookie(name, val).WithDomain(host).WithPath("/").WithExpires(&exp).Do(ctx); err != nil {
					return fmt.Errorf("çerez ayarlanamadı (%s): %w", name, err)
				}
			}
			return nil
		}))
	}
	tasks = append(tasks,
		chromedp.Navigate(targetURL),
		chromedp.Sleep(4*time.Second), // .onion + JS: ağ ve script'lerin oturması için
		chromedp.Title(&res.Title),
		chromedp.Location(&res.Final),
		chromedp.Evaluate(`(document.body && document.body.innerText ? document.body.innerText : "").slice(0, 400000)`, &res.Text),
		chromedp.OuterHTML("html", &res.HTML, chromedp.ByQuery),
	)
	if err := chromedp.Run(runCtx, tasks); err != nil {
		return nil, fmt.Errorf("render başarısız: %w", err)
	}
	if len(res.HTML) > maxRenderHTML {
		res.HTML = res.HTML[:maxRenderHTML]
	}
	res.Title = strings.TrimSpace(res.Title)
	return &res, nil
}

// chromeOptions Capture ve Render için ortak Chromium bayrakları (Tor proxy dahil).
func (c *Capturer) chromeOptions() []chromedp.ExecAllocatorOption {
	resolverRules := fmt.Sprintf("MAP * ~NOTFOUND , EXCLUDE %s", c.proxyHost())
	return append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(c.chromePath),
		chromedp.NoSandbox,
		chromedp.DisableGPU,
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.Flag("mute-audio", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("proxy-server", "socks5://"+c.torProxy),
		chromedp.Flag("host-resolver-rules", resolverRules),
		chromedp.WindowSize(1280, 900),
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
