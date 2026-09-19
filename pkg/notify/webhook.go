// Package notify Slack/Discord webhook bildirimleri gönderir.
// Basit, kaynak verimlisi — harici bağımlılık yok.
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"keywordhunter-mvp/pkg/shared"
)

// webhookClient yönlendirme takip etmeyen, dahili hedefleri reddeden istemci
// (SSRF koruması). Slack/Discord/Teams gibi genel servisler için yeterlidir.
var webhookClient = shared.NewSafeClearnetClient(15 * time.Second)

// privateClient WEBHOOK_ALLOW_PRIVATE=true iken kullanılır: kurum içi SIEM/SOAR
// alıcıları (10.x, 192.168.x) için dahili hedeflere izin verir; yine yönlendirme yok.
var privateClient = shared.NewPlainClearnetClient(15 * time.Second)

// allowPrivate dahili ağ hedeflerine izin verilip verilmediğini döndürür.
var allowPrivate = func() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("WEBHOOK_ALLOW_PRIVATE")))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

// AllowPrivateTargets yapılandırma bilgisini dışa verir (UI uyarısı için).
func AllowPrivateTargets() bool { return allowPrivate() }

// ValidTarget webhook adresinin kabul edilebilir olup olmadığını söyler.
func ValidTarget(raw string) bool {
	if allowPrivate() {
		return shared.IsHTTPURL(raw)
	}
	return shared.IsPublicWebURL(raw, false)
}

// AlertPayload bildirim verisi
type AlertPayload struct {
	Query       string
	NewCount    int
	TotalCount  int
	TopFindings []Finding
	RunAt       time.Time
}

// Finding önemli bulgu
type Finding struct {
	Title       string
	URL         string
	Category    string
	Criticality int
}

// SendWebhook Slack/Discord uyumlu webhook gönderir
// webhookURL boşsa sessizce döner
func SendWebhook(webhookURL string, payload AlertPayload) error {
	if webhookURL == "" {
		return nil
	}

	// Hem Slack hem Discord'un anlayacağı basit format
	body := buildMessage(payload)

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("webhook marshal hatası: %w", err)
	}

	if !ValidTarget(webhookURL) {
		return fmt.Errorf("webhook adresi geçersiz veya dahili bir hedefe işaret ediyor (kurum içi alıcı için WEBHOOK_ALLOW_PRIVATE=true)")
	}

	client := webhookClient
	if allowPrivate() {
		client = privateClient
	}
	resp, err := client.Post(webhookURL, "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("webhook isteği başarısız: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook HTTP %d döndürdü", resp.StatusCode)
	}

	return nil
}

// SendTest webhook adresine kısa bir doğrulama mesajı gönderir (ayarlar ekranı için).
func SendTest(webhookURL string) error {
	return SendWebhook(webhookURL, AlertPayload{
		Query:      "test",
		NewCount:   0,
		TotalCount: 0,
		RunAt:      time.Now(),
	})
}

// buildMessage Slack/Discord embed formatında mesaj oluşturur
func buildMessage(p AlertPayload) map[string]interface{} {
	critIcon := criticalityIcon(p)

	// Üst bilgi satırı
	summary := fmt.Sprintf("%s **KeywordHunter** | Zamanlanmış Tarama Tamamlandı\n", critIcon)
	summary += fmt.Sprintf("**Sorgu:** `%s`\n", p.Query)
	summary += fmt.Sprintf("**Toplam:** %d bulgu | **Yeni:** %d | **Tarih:** %s\n",
		p.TotalCount, p.NewCount, p.RunAt.Format("02.01.2006 15:04"))

	if p.NewCount > 0 && len(p.TopFindings) > 0 {
		summary += "\n**Öne Çıkan Yeni Bulgular:**\n"
		for i, f := range p.TopFindings {
			if i >= 5 {
				break
			}
			icon := critIcon2(f.Criticality)
			summary += fmt.Sprintf("%s `[Sev %d]` **%s** — %s\n> _%s_\n",
				icon, f.Criticality, f.Category, truncate(f.Title, 60), truncate(f.URL, 80))
		}
	}

	// Discord + Slack uyumlu format (embeds ile Discord'da güzel görünür)
	return map[string]interface{}{
		"username":   "KeywordHunter CTI",
		"avatar_url": "",
		"content":    "",
		"embeds": []map[string]interface{}{
			{
				"title":       fmt.Sprintf("Tarama Sonucu: %s", p.Query),
				"description": summary,
				"color":       embedColor(p),
				"footer": map[string]string{
					"text": "KeywordHunter Dark Web CTI",
				},
				"timestamp": p.RunAt.UTC().Format(time.RFC3339),
			},
		},
		// Slack uyumluluğu için text de ekle
		"text": summary,
	}
}

func criticalityIcon(p AlertPayload) string {
	if len(p.TopFindings) == 0 {
		return "✅"
	}
	maxCrit := 0
	for _, f := range p.TopFindings {
		if f.Criticality > maxCrit {
			maxCrit = f.Criticality
		}
	}
	switch {
	case maxCrit >= 5:
		return "🚨"
	case maxCrit >= 4:
		return "🔴"
	case maxCrit >= 3:
		return "🟠"
	case maxCrit >= 2:
		return "🟡"
	default:
		return "🟢"
	}
}

func critIcon2(crit int) string {
	switch {
	case crit >= 5:
		return "🚨"
	case crit >= 4:
		return "🔴"
	case crit >= 3:
		return "🟠"
	default:
		return "🟡"
	}
}

func embedColor(p AlertPayload) int {
	if p.NewCount == 0 {
		return 0x48bb78 // yeşil
	}
	maxCrit := 0
	for _, f := range p.TopFindings {
		if f.Criticality > maxCrit {
			maxCrit = f.Criticality
		}
	}
	switch {
	case maxCrit >= 5:
		return 0xe53e3e // kırmızı
	case maxCrit >= 4:
		return 0xed8936 // turuncu
	case maxCrit >= 3:
		return 0xecc94b // sarı
	default:
		return 0x63b3ed // mavi
	}
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-3]) + "..."
}

// FilterByThreshold eşik ve altındaki bulguları eler.
func FilterByThreshold(findings []Finding, minCriticality int) []Finding {
	out := make([]Finding, 0, len(findings))
	for _, f := range findings {
		if f.Criticality >= minCriticality {
			out = append(out, f)
		}
	}
	return out
}

// TopFindings kritikliğe göre azalan sıralı ilk n bulguyu döndürür (girdi değiştirilmez).
func TopFindings(findings []Finding, n int) []Finding {
	cp := make([]Finding, len(findings))
	copy(cp, findings)
	for i := 1; i < len(cp); i++ {
		for j := i; j > 0 && cp[j].Criticality > cp[j-1].Criticality; j-- {
			cp[j], cp[j-1] = cp[j-1], cp[j]
		}
	}
	if len(cp) > n {
		return cp[:n]
	}
	return cp
}
