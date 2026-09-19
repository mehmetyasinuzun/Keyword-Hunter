package web

import (
	"net/http"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"keywordhunter-mvp/pkg/storage"
)

const testHookSecret = "https://hooks.slack.com/services/T0AAA/B0BBB/SuperSecretToken123"

func mkUser(t *testing.T, s *Server, name, pass, role string) []*http.Cookie {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.CreateUser(name, string(h), role); err != nil {
		t.Fatal(err)
	}
	return loginAs(t, s, name, pass)
}

func storedHook(t *testing.T, s *Server) string {
	t.Helper()
	cfg, err := s.db.GetAlertConfig()
	if err != nil || cfg == nil {
		t.Fatalf("alert config okunamadı: %v", err)
	}
	return cfg.WebhookURL
}

// TestWebhookSecret_RedactedForNonAdmin: webhook adresi bir sırdır. Admin dışı
// roller ne API'de ne de sayfaya gömülü HTML'de tam adresi görmemeli. Yerleşik
// RBAC sözleşmesi korunur (analist alert-config YAZABİLİR); ancak analist gizlenmiş
// maskeyi geri gönderirse gerçek sır maskeyle ezilmemeli.
func TestWebhookSecret_RedactedForNonAdmin(t *testing.T) {
	s := newTestServer(t)
	if err := s.db.SaveAlertConfig(storage.AlertConfig{WebhookURL: testHookSecret, MinCriticality: 3, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	adminCk := loginAs(t, s, "admin", "correct-horse")
	viewerCk := mkUser(t, s, "v1", "viewer-pass-1", storage.RoleViewer)
	analystCk := mkUser(t, s, "a1", "analyst-pass-1", storage.RoleAnalyst)

	// Admin API + HTML: tam adres (düzenleyebilmeli)
	if w := do(s, http.MethodGet, "/api/alert-config", nil, adminCk, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "SuperSecretToken123") {
		t.Fatalf("admin tam webhook görmeli: %d %s", w.Code, w.Body.String())
	}
	if w := do(s, http.MethodGet, "/scheduled", nil, adminCk, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "SuperSecretToken123") {
		t.Fatalf("admin /scheduled tam webhook görmeli: %d", w.Code)
	}

	// Viewer + analyst: gizlenmiş — sır yok, ana makine görünür; HTML kaynağı da temiz
	for name, ck := range map[string][]*http.Cookie{"viewer": viewerCk, "analyst": analystCk} {
		w := do(s, http.MethodGet, "/api/alert-config", nil, ck, nil)
		b := w.Body.String()
		if w.Code != 200 || strings.Contains(b, "SuperSecret") || strings.Contains(b, "T0AAA") {
			t.Fatalf("%s API'de sır sızdı: %d %s", name, w.Code, b)
		}
		if !strings.Contains(b, "hooks.slack.com") {
			t.Fatalf("%s gizlenmiş ana makineyi görmeli: %s", name, b)
		}
		if w := do(s, http.MethodGet, "/scheduled", nil, ck, nil); w.Code != 200 || strings.Contains(w.Body.String(), "SuperSecret") {
			t.Fatalf("%s /scheduled HTML'de sır sızdı: %d", name, w.Code)
		}
	}

	// Viewer yazamaz (salt-okunur yazma koruması)
	if code := jsonPost(s, "/api/alert-config", `{"webhookUrl":"https://example.com/x","minCriticality":3,"enabled":true}`, viewerCk); code != http.StatusForbidden {
		t.Fatalf("viewer alert-config POST %d aldı (403 bekleniyordu)", code)
	}
	if got := storedHook(t, s); got != testHookSecret {
		t.Fatalf("viewer sırrı değiştirdi: %q", got)
	}

	// Analist yazabilir; maskeyi geri gönderip yalnız eşiği değiştirirse sır KORUNUR
	mask := "https://hooks.slack.com/…"
	if code := jsonPost(s, "/api/alert-config", `{"webhookUrl":"`+mask+`","minCriticality":4,"enabled":true}`, analystCk); code != http.StatusOK {
		t.Fatalf("analist maske ile kaydedemedi: %d", code)
	}
	if got := storedHook(t, s); got != testHookSecret {
		t.Fatalf("maske gerçek sırrı EZDİ: %q", got)
	}
	if cfg, _ := s.db.GetAlertConfig(); cfg.MinCriticality != 4 {
		t.Fatalf("eşik güncellenmeliydi, %d", cfg.MinCriticality)
	}

	// Analist gerçekten yeni bir adres verirse normal kaydedilir
	newHook := "https://discord.com/api/webhooks/1/NewTokenXYZ"
	if code := jsonPost(s, "/api/alert-config", `{"webhookUrl":"`+newHook+`","minCriticality":3,"enabled":true}`, analystCk); code != http.StatusOK {
		t.Fatalf("analist yeni adres kaydedemedi: %d", code)
	}
	if got := storedHook(t, s); got != newHook {
		t.Fatalf("yeni adres kaydedilmedi: %q", got)
	}
}
