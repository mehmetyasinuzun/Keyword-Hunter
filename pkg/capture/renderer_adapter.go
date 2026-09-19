package capture

import "context"

// ScraperRenderer scraper.Renderer arayüzünü karşılayan Capturer sarmalayıcısı.
// (scraper paketi capture'a bağımlı olamaz; arayüz orada, uyum burada.)
type ScraperRenderer struct{ c *Capturer }

// AsScraperRenderer capturer'ı scraper.Renderer olarak döndürür (chromium yoksa nil).
func (c *Capturer) AsScraperRenderer() *ScraperRenderer {
	if c == nil || !c.Available() {
		return nil
	}
	return &ScraperRenderer{c: c}
}

// Render scraper.Renderer imzası: (html, text, title, err).
func (s *ScraperRenderer) Render(ctx context.Context, targetURL, cookieHeader, userAgent string) (string, string, string, error) {
	res, err := s.c.Render(ctx, targetURL, cookieHeader, userAgent)
	if err != nil {
		return "", "", "", err
	}
	return res.HTML, res.Text, res.Title, nil
}
