package web

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"keywordhunter-mvp/pkg/scraper"
	"keywordhunter-mvp/pkg/storage"
)

// isValidWebhookURL webhook adresini SSRF'e karşı doğrular: yalnız http(s) şeması
// ve dahili/özel/loopback hedeflere izin verilmez. (Slack/Discord gibi genel
// servisler kabul; 127.0.0.1, 10.x, 192.168.x, localhost, .onion vb. reddedilir.)
func isValidWebhookURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".onion") || strings.HasSuffix(lower, ".local") || strings.HasSuffix(lower, ".internal") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return false
		}
	}
	return true
}

// generateSessionID kriptografik olarak güvenli rastgele token üretir
func generateSessionID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// Olası fallback - asla boş token döndürme
		return uuid.NewString() + uuid.NewString()
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// buildChildrenNodes linkleri GraphNode formatına çevirir
func buildChildrenNodes(links []scraper.ExtractedLink) []*storage.GraphNode {
	var internalNodes []*storage.GraphNode
	var externalNodes []*storage.GraphNode

	for _, link := range links {
		node := &storage.GraphNode{
			Name:   link.Title,
			URL:    link.URL,
			Type:   "link",
			Domain: link.Domain,
		}

		if link.LinkType == "internal" {
			internalNodes = append(internalNodes, node)
		} else {
			externalNodes = append(externalNodes, node)
		}
	}

	// Grup node'ları oluştur
	var children []*storage.GraphNode

	if len(internalNodes) > 0 {
		children = append(children, &storage.GraphNode{
			Name:     fmt.Sprintf("🔗 İç Linkler (%d)", len(internalNodes)),
			Type:     "internal-group",
			Children: internalNodes,
			Count:    len(internalNodes),
		})
	}

	if len(externalNodes) > 0 {
		children = append(children, &storage.GraphNode{
			Name:     fmt.Sprintf("🌐 Dış Linkler (%d)", len(externalNodes)),
			Type:     "external-group",
			Children: externalNodes,
			Count:    len(externalNodes),
		})
	}

	return children
}

// countByType link tipine göre sayar
func countByType(links []scraper.ExtractedLink, linkType string) int {
	count := 0
	for _, l := range links {
		if l.LinkType == linkType {
			count++
		}
	}
	return count
}
