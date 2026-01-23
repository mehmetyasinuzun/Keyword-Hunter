package storage

import (
	"database/sql"
	"time"

	"keywordhunter-mvp/pkg/logger"
)

// GraphNode D3.js için ağaç node yapısı
type GraphNode struct {
	Name       string       `json:"name"`
	URL        string       `json:"url,omitempty"`
	Type       string       `json:"type"` // "root", "query", "engine", "result", "internal", "external"
	Children   []*GraphNode `json:"children,omitempty"`
	Count      int          `json:"count,omitempty"`      // Kaç kez bulunduğu
	NodeID     int64        `json:"nodeId,omitempty"`     // DB'deki ID (derinleştir için)
	IsExpanded bool         `json:"isExpanded,omitempty"` // Expand edilmiş mi?
	Domain     string       `json:"domain,omitempty"`     // Domain bilgisi
}

// GraphNodeDB veritabanındaki graph node modeli
type GraphNodeDB struct {
	ID           int64
	URL          string
	Title        string
	Domain       string
	ParentID     *int64 // NULL olabilir (root için)
	Depth        int
	LinkType     string // "root", "search", "internal", "external"
	SourceQuery  string
	DiscoveredAt time.Time
	IsExpanded   bool
}

// GetGraphData graph görselleştirmesi için veri döndürür
func (db *DB) GetGraphData(queryFilter string) (*GraphNode, error) {
	// Ana root node
	root := &GraphNode{
		Name:     "🕵️ KeywordHunter",
		Type:     "root",
		Children: []*GraphNode{},
	}

	// Önce tüm unique sorguları al
	var querySQL string
	var queryArgs []interface{}
	if queryFilter != "" {
		querySQL = "SELECT DISTINCT query FROM search_results WHERE query LIKE ? ORDER BY query"
		queryArgs = []interface{}{"%" + queryFilter + "%"}
	} else {
		querySQL = "SELECT DISTINCT query FROM search_results ORDER BY query"
	}

	queryRows, err := db.conn.Query(querySQL, queryArgs...)
	if err != nil {
		return root, err
	}
	defer queryRows.Close()

	var queries []string
	for queryRows.Next() {
		var q string
		if err := queryRows.Scan(&q); err == nil {
			queries = append(queries, q)
		}
	}

	// Global URL count hesapla - aynı URL kaç farklı source'ta bulundu
	globalURLCount := make(map[string]int)
	countRows, err := db.conn.Query(`
		SELECT url, COUNT(DISTINCT source) as source_count 
		FROM search_results 
		GROUP BY url 
		HAVING source_count > 1
	`)
	if err == nil {
		for countRows.Next() {
			var url string
			var count int
			if countRows.Scan(&url, &count) == nil {
				globalURLCount[url] = count
			}
		}
		countRows.Close()
	}

	// Her sorgu için sonuçları grupla
	for _, q := range queries {
		queryNode := &GraphNode{
			Name:     "🔍 " + q,
			Type:     "query",
			Children: []*GraphNode{},
		}

		// Bu sorguya ait sonuçları kaynak bazlı grupla
		rows, err := db.conn.Query(`
			SELECT id, source, title, url 
			FROM search_results 
			WHERE query = ? 
			ORDER BY source, title
		`, q)
		if err != nil {
			continue
		}

		// Kaynak bazlı gruplama için map
		engineResults := make(map[string][]*GraphNode)

		for rows.Next() {
			var id int64
			var source, title, url string
			if err := rows.Scan(&id, &source, &title, &url); err != nil {
				continue
			}

			// Global URL count kullan (çoklu kaynaklarda bulunanlar)
			count := 1
			if c, ok := globalURLCount[url]; ok {
				count = c
			}

			// Kaynak engine'e ekle
			resultNode := &GraphNode{
				Name:   title,
				URL:    url,
				Type:   "result",
				Count:  count,
				NodeID: id,
			}

			// Bu node daha önce expand edilmiş mi kontrol et (graph_nodes tablosundan)
			gn, err := db.GetGraphNodeByURL(url)
			if err == nil && gn != nil && gn.IsExpanded {
				resultNode.IsExpanded = true
				resultNode.Children = db.loadGraphChildren(gn.ID)
			}

			engineResults[source] = append(engineResults[source], resultNode)
		}
		rows.Close()

		// Engine node'larını oluştur
		for engine, results := range engineResults {
			engineNode := &GraphNode{
				Name:     "🌐 " + engine,
				Type:     "engine",
				Children: results,
			}
			queryNode.Children = append(queryNode.Children, engineNode)
		}

		if len(queryNode.Children) > 0 {
			root.Children = append(root.Children, queryNode)
		}
	}

	return root, nil
}

// loadGraphChildren recursive olarak children yükler
func (db *DB) loadGraphChildren(parentID int64) []*GraphNode {
	children, err := db.GetGraphChildren(parentID)
	if err != nil {
		return nil
	}

	var result []*GraphNode
	for _, child := range children {
		node := &GraphNode{
			Name:       child.Title,
			URL:        child.URL,
			Type:       child.LinkType,
			NodeID:     child.ID,
			IsExpanded: child.IsExpanded,
			Domain:     child.Domain,
			Children:   []*GraphNode{},
		}

		// Recursive yükleme
		if child.IsExpanded {
			node.Children = db.loadGraphChildren(child.ID)
		}

		result = append(result, node)
	}

	return result
}

// SaveGraphNode yeni bir graph node kaydeder
func (db *DB) SaveGraphNode(url, title, domain string, parentID *int64, depth int, linkType, sourceQuery string) (int64, error) {
	result, err := db.conn.Exec(`
		INSERT OR IGNORE INTO graph_nodes (url, title, domain, parent_id, depth, link_type, source_query)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, url, title, domain, parentID, depth, linkType, sourceQuery)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// SaveGraphNodes birden fazla graph node kaydeder
func (db *DB) SaveGraphNodes(nodes []GraphNodeDB) (int, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT OR IGNORE INTO graph_nodes (url, title, domain, parent_id, depth, link_type, source_query)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	count := 0
	for _, node := range nodes {
		result, err := stmt.Exec(node.URL, node.Title, node.Domain, node.ParentID, node.Depth, node.LinkType, node.SourceQuery)
		if err != nil {
			logger.Debug("Graph node kaydedilemedi (URL: %s): %v", node.URL, err)
			continue
		}
		affected, err := result.RowsAffected()
		if err != nil {
			logger.Debug("Graph RowsAffected hatası: %v", err)
		}
		if affected > 0 {
			count++
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}

	return count, nil
}

// GetGraphNodeByURL URL'ye göre graph node getirir
func (db *DB) GetGraphNodeByURL(url string) (*GraphNodeDB, error) {
	var node GraphNodeDB
	var parentID sql.NullInt64

	err := db.conn.QueryRow(`
		SELECT id, url, title, domain, parent_id, depth, link_type, source_query, discovered_at, is_expanded
		FROM graph_nodes WHERE url = ? LIMIT 1
	`, url).Scan(&node.ID, &node.URL, &node.Title, &node.Domain, &parentID, &node.Depth, &node.LinkType, &node.SourceQuery, &node.DiscoveredAt, &node.IsExpanded)

	if err != nil {
		return nil, err
	}

	if parentID.Valid {
		node.ParentID = &parentID.Int64
	}

	return &node, nil
}

// GetGraphNodeByID ID'ye göre graph node getirir
func (db *DB) GetGraphNodeByID(id int64) (*GraphNodeDB, error) {
	var node GraphNodeDB
	var parentID sql.NullInt64

	err := db.conn.QueryRow(`
		SELECT id, url, title, domain, parent_id, depth, link_type, source_query, discovered_at, is_expanded
		FROM graph_nodes WHERE id = ?
	`, id).Scan(&node.ID, &node.URL, &node.Title, &node.Domain, &parentID, &node.Depth, &node.LinkType, &node.SourceQuery, &node.DiscoveredAt, &node.IsExpanded)

	if err != nil {
		return nil, err
	}

	if parentID.Valid {
		node.ParentID = &parentID.Int64
	}

	return &node, nil
}

// GetGraphChildren bir node'un child'larını getirir
func (db *DB) GetGraphChildren(parentID int64) ([]GraphNodeDB, error) {
	rows, err := db.conn.Query(`
		SELECT id, url, title, domain, parent_id, depth, link_type, source_query, discovered_at, is_expanded
		FROM graph_nodes WHERE parent_id = ?
		ORDER BY link_type, title
	`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []GraphNodeDB
	for rows.Next() {
		var node GraphNodeDB
		var pID sql.NullInt64
		if err := rows.Scan(&node.ID, &node.URL, &node.Title, &node.Domain, &pID, &node.Depth, &node.LinkType, &node.SourceQuery, &node.DiscoveredAt, &node.IsExpanded); err != nil {
			continue
		}
		if pID.Valid {
			node.ParentID = &pID.Int64
		}
		nodes = append(nodes, node)
	}

	return nodes, nil
}

// MarkNodeExpanded bir node'u expanded olarak işaretler
func (db *DB) MarkNodeExpanded(nodeID int64) error {
	_, err := db.conn.Exec(`UPDATE graph_nodes SET is_expanded = 1 WHERE id = ?`, nodeID)
	return err
}
