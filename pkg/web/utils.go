package web

import (
	"fmt"

	"github.com/google/uuid"

	"keywordhunter-mvp/pkg/scraper"
	"keywordhunter-mvp/pkg/storage"
)

// generateSessionID rastgele session ID üretir
func generateSessionID() string {
	return uuid.NewString()
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
