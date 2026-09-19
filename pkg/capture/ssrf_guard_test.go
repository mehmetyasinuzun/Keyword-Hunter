package capture

import (
	"context"
	"strings"
	"testing"
)

// TestRenderAndCapture_RejectNonOnion: Render/Capture yalnız .onion hedefi kabul
// etmeli. host-resolver-rules DNS'i Tor'a zorlar ama düz IP DNS gerektirmez; bu
// yüzden 127.0.0.1 gibi hedefler Tor yapılandırmasına bakılmaksızın bizim
// kodumuzda reddedilmeli. Koruma chromium kontrolünden ÖNCE çalışır, dolayısıyla
// chromium olmadan da deterministik test edilir.
func TestRenderAndCapture_RejectNonOnion(t *testing.T) {
	c := New("127.0.0.1:9050", "", t.TempDir()) // chromePath boş → Available()=false
	ctx := context.Background()

	bad := []string{
		"http://127.0.0.1:18080/",
		"http://10.0.0.5/admin",
		"http://[::1]/",
		"http://169.254.169.254/latest/meta-data/",
		"http://example.com/",
		"not a url",
		"",
	}
	for _, u := range bad {
		if _, err := c.Render(ctx, u, "", ""); err == nil || !strings.Contains(err.Error(), ".onion") {
			t.Errorf("Render(%q): .onion reddi bekleniyordu, err=%v", u, err)
		}
		if _, err := c.Capture(ctx, u); err == nil || !strings.Contains(err.Error(), ".onion") {
			t.Errorf("Capture(%q): .onion reddi bekleniyordu, err=%v", u, err)
		}
	}

	// Geçerli .onion korumayı geçmeli; chromium yoksa bir SONRAKİ kontrolde düşer.
	// (Onion hatası DEĞİL — yani koruma geçildi.)
	good := "http://submariwvjhyg7acbrw3thxjxxsatwpieoioenyd4lde6y2rrnzkcwad.onion/"
	if _, err := c.Render(ctx, good, "", ""); err == nil || strings.Contains(err.Error(), ".onion hedefleri") {
		t.Errorf("Render(onion): koruma geçilmeliydi, err=%v", err)
	}
	if _, err := c.Capture(ctx, good); err == nil || strings.Contains(err.Error(), ".onion hedefleri") {
		t.Errorf("Capture(onion): koruma geçilmeliydi, err=%v", err)
	}
}
