// Package notify Slack/Discord webhook bildirimleri gönderir.
// Basit, kaynak verimlisi — harici bağımlılık yok.
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

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

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Post(webhookURL, "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("webhook isteği başarısız: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook HTTP %d döndürdü", resp.StatusCode)
	}

	return nil
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
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
