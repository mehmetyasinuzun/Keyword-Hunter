// Package capture Tor SOCKS proxy üzerinden headless Chromium (chromedp) ile
// .onion ve clearnet sayfaların ekran görüntüsünü alır.
//
// Tasarım ilkeleri:
//   - Gerçek veri: chromium yoksa özellik sessizce "kullanılamaz" döner, asla
//     sahte/placeholder görüntü üretmez.
//   - Kaynak koruması: aynı anda sınırlı sayıda capture (Tor + bellek).
//   - .onion çözümü: --proxy-server=socks5://<tor> + --host-resolver-rules ile
//     tüm isim çözümü proxy'ye (uzak DNS) yönlendirilir; proxy host'u hariç tutulur.
package capture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/chromedp/chromedp"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/shared"
)

// Capturer ekran görüntüsü alıcı
type Capturer struct {
	torProxy   string // "tor:9050" veya "127.0.0.1:9150"
	chromePath string // chromium ikilisi
	outDir     string // PNG çıktı dizini
	timeout    time.Duration
	sem        chan struct{} // eşzamanlılık sınırı
}

// Shot tek bir ekran görüntüsünün sonucu
type Shot struct {
	Path          string
	SHA256        string
	Width         int
	Height        int
	Bytes         int
	Title         string
	Challenge     bool   // sayfa bir captcha/doğrulama/engel ekranı mı
	ChallengeKind string // "Cloudflare", "captcha", "doğrulama" ...
	TakenAt       time.Time
}

var hostSanitize = regexp.MustCompile(`[^a-z0-9.-]+`)

// New yeni Capturer oluşturur. chromePath boşsa CHROME_BIN env'inden okunur.
func New(torProxy, chromePath, outDir string) *Capturer {
	if chromePath == "" {
		chromePath = os.Getenv("CHROME_BIN")
	}
	if chromePath == "" {
		chromePath = findChrome()
	}
	if outDir == "" {
		outDir = os.Getenv("SCREENSHOT_DIR")
	}
	if outDir == "" {
		// Docker'da /data bağlı; yerelde çalışma dizinine göreli "screenshots"
		if st, err := os.Stat("/data"); err == nil && st.IsDir() {
			outDir = "/data/screenshots"
		} else {
			outDir = "screenshots"
		}
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		logger.Warn("Ekran görüntüsü dizini oluşturulamadı (%s): %v", outDir, err)
	}

	return &Capturer{
		torProxy:   torProxy,
		chromePath: chromePath,
		outDir:     outDir,
		timeout:    70 * time.Second,
		sem:        make(chan struct{}, 2),
	}
}

// findChrome işletim sistemine göre bilinen Chromium/Chrome konumlarını ve PATH'i tarar.
// Bulunamazsa "" döner; özellik kapalı kalır (README: CHROME_BIN ile elle verilebilir).
func findChrome() string {
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		}
	case "windows":
		for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LocalAppData")} {
			if base == "" {
				continue
			}
			candidates = append(candidates,
				filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"),
				filepath.Join(base, "Chromium", "Application", "chrome.exe"),
				filepath.Join(base, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
				filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe"),
			)
		}
	default: // linux / bsd
		candidates = []string{"/usr/bin/chromium-browser", "/usr/bin/chromium", "/usr/bin/google-chrome", "/usr/bin/google-chrome-stable", "/snap/bin/chromium", "/usr/bin/brave-browser"}
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "chrome", "brave-browser", "msedge"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// Available chromium kullanılabilir mi (yoksa özellik devre dışı)
func (c *Capturer) Available() bool {
	if c.chromePath == "" {
		return false
	}
	_, err := os.Stat(c.chromePath)
	return err == nil
}

// OutDir PNG dizinini döndürür (dosya servis etmek için)
func (c *Capturer) OutDir() string {
	return c.outDir
}

// proxyHost torProxy'den host kısmını çıkarır (host-resolver EXCLUDE için)
func (c *Capturer) proxyHost() string {
	h := c.torProxy
	if i := strings.LastIndex(h, ":"); i > 0 {
		h = h[:i]
	}
	if h == "" {
		h = "localhost"
	}
	return h
}

// Capture verilen URL'nin ekran görüntüsünü Tor üzerinden alır ve PNG olarak kaydeder.
func (c *Capturer) Capture(ctx context.Context, targetURL string) (*Shot, error) {
	if !c.Available() {
		return nil, fmt.Errorf("chromium bulunamadı (chromePath=%q) — ekran görüntüsü devre dışı", c.chromePath)
	}

	// Eşzamanlılık sınırı
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	opts := c.chromeOptions()
	opts = append(opts, chromedp.UserAgent(shared.ChromeUserAgent))

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()
	taskCtx, cancelTask := chromedp.NewContext(allocCtx)
	defer cancelTask()
	runCtx, cancelRun := context.WithTimeout(taskCtx, c.timeout)
	defer cancelRun()

	var buf []byte
	var pageTitle, bodyText string
	err := chromedp.Run(runCtx,
		chromedp.Navigate(targetURL),
		chromedp.Sleep(5*time.Second), // .onion sayfalar yavaş çözülür/yüklenir
		chromedp.Title(&pageTitle),
		chromedp.Evaluate(`(document.body && document.body.innerText ? document.body.innerText : "").slice(0,4000)`, &bodyText),
		chromedp.CaptureScreenshot(&buf),
	)
	if err != nil {
		return nil, fmt.Errorf("ekran görüntüsü alınamadı: %w", err)
	}
	if len(buf) == 0 {
		return nil, fmt.Errorf("boş ekran görüntüsü")
	}

	// Engel/doğrulama (captcha, Cloudflare) sayfası mı?
	challenge, kind := shared.DetectChallenge(pageTitle, bodyText)
	if challenge {
		logger.Info("SCREENSHOT: %s — engel tespit edildi (%s)", targetURL, kind)
	}

	now := time.Now()
	sum := sha256.Sum256(buf)
	hash := hex.EncodeToString(sum[:])

	name := fmt.Sprintf("%s_%d.png", c.safeHost(targetURL), now.Unix())
	full := filepath.Join(c.outDir, name)
	if err := os.WriteFile(full, buf, 0o644); err != nil {
		return nil, fmt.Errorf("PNG yazılamadı: %w", err)
	}

	logger.Info("SCREENSHOT: %s alındı (%d bayt) → %s", targetURL, len(buf), name)
	return &Shot{
		Path:          name, // dizine göreli ad (servis için)
		SHA256:        hash,
		Width:         1280,
		Height:        900,
		Bytes:         len(buf),
		Title:         strings.TrimSpace(pageTitle),
		Challenge:     challenge,
		ChallengeKind: kind,
		TakenAt:       now,
	}, nil
}

// safeHost URL'den dosya adına uygun host parçası üretir
func (c *Capturer) safeHost(rawURL string) string {
	s := strings.ToLower(rawURL)
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "https://")
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	s = hostSanitize.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-.")
	if len(s) > 40 {
		s = s[:40]
	}
	if s == "" {
		s = "shot"
	}
	return s
}
