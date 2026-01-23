package storage

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"keywordhunter-mvp/pkg/logger"

	_ "modernc.org/sqlite" // Pure Go SQLite driver (CGO gerektirmez)
)

// DB veritabanı bağlantısı
type DB struct {
	conn *sql.DB
}

// SearchResult arama sonucu modeli
type SearchResult struct {
	ID        int64
	Title     string
	URL       string
	Source    string // Hangi arama motorundan geldi
	Query     string // Hangi arama sorgusu ile bulundu
	CreatedAt time.Time
}

// New yeni veritabanı bağlantısı oluşturur
func New(dbPath string) (*DB, error) {
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("veritabanı açılamadı: %w", err)
	}

	// Bağlantıyı test et
	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("veritabanı bağlantısı başarısız: %w", err)
	}

	db := &DB{conn: conn}

	// Tabloları oluştur
	if err := db.createTables(); err != nil {
		return nil, err
	}

	return db, nil
}

// createTables gerekli tabloları oluşturur
func (db *DB) createTables() error {
	// Arama sonuçları tablosu
	_, err := db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS search_results (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			url TEXT NOT NULL UNIQUE,
			source TEXT NOT NULL,
			query TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		
		CREATE INDEX IF NOT EXISTS idx_search_results_query ON search_results(query);
		CREATE INDEX IF NOT EXISTS idx_search_results_source ON search_results(source);
		CREATE INDEX IF NOT EXISTS idx_search_results_created ON search_results(created_at);
	`)
	if err != nil {
		return fmt.Errorf("tablo oluşturulamadı: %w", err)
	}

	// Arama geçmişi tablosu
	_, err = db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS search_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			query TEXT NOT NULL,
			result_count INTEGER DEFAULT 0,
			searched_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		return fmt.Errorf("search_history tablosu oluşturulamadı: %w", err)
	}

	// Scrape edilmiş içerikler tablosu
	_, err = db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS contents (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			url TEXT NOT NULL UNIQUE,
			title TEXT NOT NULL,
			raw_content TEXT,
			content_size INTEGER DEFAULT 0,
			scraped_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			is_scraped INTEGER DEFAULT 0
		);
		
		CREATE INDEX IF NOT EXISTS idx_contents_scraped ON contents(is_scraped);
	`)
	if err != nil {
		return fmt.Errorf("contents tablosu oluşturulamadı: %w", err)
	}

	// Graph nodes tablosu - Derinleştir özelliği için
	_, err = db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS graph_nodes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			url TEXT NOT NULL,
			title TEXT NOT NULL,
			domain TEXT NOT NULL,
			parent_id INTEGER,
			depth INTEGER DEFAULT 0,
			link_type TEXT DEFAULT 'search',
			source_query TEXT,
			discovered_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			is_expanded INTEGER DEFAULT 0,
			FOREIGN KEY (parent_id) REFERENCES graph_nodes(id),
			UNIQUE(url, parent_id)
		);
		
		CREATE INDEX IF NOT EXISTS idx_graph_nodes_parent ON graph_nodes(parent_id);
		CREATE INDEX IF NOT EXISTS idx_graph_nodes_domain ON graph_nodes(domain);
		CREATE INDEX IF NOT EXISTS idx_graph_nodes_depth ON graph_nodes(depth);
		CREATE INDEX IF NOT EXISTS idx_graph_nodes_query ON graph_nodes(source_query);
	`)
	if err != nil {
		return fmt.Errorf("graph_nodes tablosu oluşturulamadı: %w", err)
	}

	return nil
}

// SaveResult tek bir sonucu kaydeder
func (db *DB) SaveResult(title, url, source, query string) error {
	_, err := db.conn.Exec(`
		INSERT OR IGNORE INTO search_results (title, url, source, query)
		VALUES (?, ?, ?, ?)
	`, title, url, source, query)
	return err
}

// SaveResults birden fazla sonucu kaydeder
func (db *DB) SaveResults(results []SearchResult) (int, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT OR IGNORE INTO search_results (title, url, source, query)
		VALUES (?, ?, ?, ?)
	`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	saved := 0
	for _, r := range results {
		result, err := stmt.Exec(r.Title, r.URL, r.Source, r.Query)
		if err != nil {
			logger.Debug("Sonuç kaydedilemedi (URL: %s): %v", r.URL, err)
			continue
		}
		affected, err := result.RowsAffected()
		if err != nil {
			logger.Debug("RowsAffected hatası: %v", err)
		}
		saved += int(affected)
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}

	return saved, nil
}

// SaveSearchHistory arama geçmişine ekler
func (db *DB) SaveSearchHistory(query string, resultCount int) error {
	_, err := db.conn.Exec(`
		INSERT INTO search_history (query, result_count)
		VALUES (?, ?)
	`, query, resultCount)
	return err
}

// GetResults sonuçları getirir (opsiyonel filtrelerle)
func (db *DB) GetResults(limit int, query string) ([]SearchResult, error) {
	var rows *sql.Rows
	var err error

	if query != "" {
		rows, err = db.conn.Query(`
			SELECT id, title, url, source, query, created_at 
			FROM search_results 
			WHERE query LIKE ?
			ORDER BY created_at DESC
			LIMIT ?
		`, "%"+query+"%", limit)
	} else {
		rows, err = db.conn.Query(`
			SELECT id, title, url, source, query, created_at 
			FROM search_results 
			ORDER BY created_at DESC
			LIMIT ?
		`, limit)
	}

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.ID, &r.Title, &r.URL, &r.Source, &r.Query, &r.CreatedAt); err != nil {
			continue
		}
		results = append(results, r)
	}

	return results, nil
}

// GetStats istatistikleri getirir
func (db *DB) GetStats() (totalResults int, totalSearches int, err error) {
	err = db.conn.QueryRow("SELECT COUNT(*) FROM search_results").Scan(&totalResults)
	if err != nil {
		return
	}
	err = db.conn.QueryRow("SELECT COUNT(*) FROM search_history").Scan(&totalSearches)
	return
}

// QueryInfo sorgu bilgisi
type QueryInfo struct {
	Query string `json:"query"`
	Count int    `json:"count"`
}

// GetQueries benzersiz sorguları ve sonuç sayılarını getirir
func (db *DB) GetQueries() ([]QueryInfo, error) {
	rows, err := db.conn.Query(`
		SELECT query, COUNT(*) as count 
		FROM search_results 
		GROUP BY query 
		ORDER BY count DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var queries []QueryInfo
	for rows.Next() {
		var q QueryInfo
		if err := rows.Scan(&q.Query, &q.Count); err != nil {
			continue
		}
		queries = append(queries, q)
	}
	return queries, nil
}

// Content scrape edilmiş içerik modeli
type Content struct {
	ID          int64
	URL         string
	Title       string
	RawContent  string
	ContentSize int
	ScrapedAt   time.Time
	IsScraped   bool
}

// SaveContent içerik kaydeder
func (db *DB) SaveContent(url, title, rawContent string, contentSize int) error {
	_, err := db.conn.Exec(`
		INSERT INTO contents (url, title, raw_content, content_size, is_scraped)
		VALUES (?, ?, ?, ?, 1)
		ON CONFLICT(url) DO UPDATE SET
			title = excluded.title,
			raw_content = excluded.raw_content,
			content_size = excluded.content_size,
			scraped_at = CURRENT_TIMESTAMP,
			is_scraped = 1
	`, url, title, rawContent, contentSize)
	return err
}

// GetUnscrapedURLs henüz scrape edilmemiş URL'leri getirir
func (db *DB) GetUnscrapedURLs(limit int) ([]SearchResult, error) {
	rows, err := db.conn.Query(`
		SELECT sr.id, sr.title, sr.url, sr.source, sr.query, sr.created_at
		FROM search_results sr
		LEFT JOIN contents c ON sr.url = c.url
		WHERE c.id IS NULL OR c.is_scraped = 0
		ORDER BY sr.created_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.ID, &r.Title, &r.URL, &r.Source, &r.Query, &r.CreatedAt); err != nil {
			continue
		}
		results = append(results, r)
	}
	return results, nil
}

// GetContents scrape edilmiş içerikleri getirir
func (db *DB) GetContents(limit int, query string) ([]Content, error) {
	var rows *sql.Rows
	var err error

	if query != "" {
		rows, err = db.conn.Query(`
			SELECT id, url, title, raw_content, content_size, scraped_at, is_scraped
			FROM contents
			WHERE is_scraped = 1 AND (title LIKE ? OR raw_content LIKE ?)
			ORDER BY scraped_at DESC
			LIMIT ?
		`, "%"+query+"%", "%"+query+"%", limit)
	} else {
		rows, err = db.conn.Query(`
			SELECT id, url, title, raw_content, content_size, scraped_at, is_scraped
			FROM contents
			WHERE is_scraped = 1
			ORDER BY scraped_at DESC
			LIMIT ?
		`, limit)
	}

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var contents []Content
	for rows.Next() {
		var c Content
		var isScraped int
		if err := rows.Scan(&c.ID, &c.URL, &c.Title, &c.RawContent, &c.ContentSize, &c.ScrapedAt, &isScraped); err != nil {
			continue
		}
		c.IsScraped = isScraped == 1
		contents = append(contents, c)
	}
	return contents, nil
}

// GetContentByID ID ile içerik getirir
func (db *DB) GetContentByID(id int64) (*Content, error) {
	var c Content
	var isScraped int
	err := db.conn.QueryRow(`
		SELECT id, url, title, raw_content, content_size, scraped_at, is_scraped
		FROM contents WHERE id = ?
	`, id).Scan(&c.ID, &c.URL, &c.Title, &c.RawContent, &c.ContentSize, &c.ScrapedAt, &isScraped)
	if err != nil {
		return nil, err
	}
	c.IsScraped = isScraped == 1
	return &c, nil
}

// GetContentStats içerik istatistiklerini getirir
func (db *DB) GetContentStats() (total int, scraped int, err error) {
	err = db.conn.QueryRow("SELECT COUNT(*) FROM search_results").Scan(&total)
	if err != nil {
		return
	}
	err = db.conn.QueryRow("SELECT COUNT(*) FROM contents WHERE is_scraped = 1").Scan(&scraped)
	return
}

// GetDuplicateURLs birden fazla kaynakta bulunan URL'leri getirir
func (db *DB) GetDuplicateURLs() ([]map[string]interface{}, error) {
	rows, err := db.conn.Query(`
		SELECT url, title, COUNT(DISTINCT source) as source_count, 
			   GROUP_CONCAT(DISTINCT source) as sources,
			   COUNT(DISTINCT query) as query_count,
			   GROUP_CONCAT(DISTINCT query) as queries
		FROM search_results 
		GROUP BY url 
		HAVING source_count > 1 OR query_count > 1
		ORDER BY source_count DESC, query_count DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var duplicates []map[string]interface{}
	for rows.Next() {
		var url, title, sources, queries string
		var sourceCount, queryCount int
		if err := rows.Scan(&url, &title, &sourceCount, &sources, &queryCount, &queries); err != nil {
			continue
		}
		duplicates = append(duplicates, map[string]interface{}{
			"url":          url,
			"title":        title,
			"source_count": sourceCount,
			"sources":      sources,
			"query_count":  queryCount,
			"queries":      queries,
		})
	}
	return duplicates, nil
}

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
			SELECT source, title, url 
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
			var source, title, url string
			if err := rows.Scan(&source, &title, &url); err != nil {
				continue
			}

			// Global URL count kullan (çoklu kaynaklarda bulunanlar)
			count := 1
			if c, ok := globalURLCount[url]; ok {
				count = c
			}

			// Kaynak engine'e ekle
			resultNode := &GraphNode{
				Name:  title,
				URL:   url,
				Type:  "result",
				Count: count,
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

// ============================================================================
// GRAPH/DERINLEŞTIR FONKSİYONLARI
// ============================================================================

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

// GetExpandableGraphData derinleştirilebilir graf verisi döndürür
func (db *DB) GetExpandableGraphData(sourceQuery string) (*GraphNode, error) {
	root := &GraphNode{
		Name:     "🕵️ " + sourceQuery,
		Type:     "root",
		Children: []*GraphNode{},
	}

	// Root seviyesindeki node'ları al (depth=1, parent_id=NULL veya root)
	rows, err := db.conn.Query(`
		SELECT id, url, title, domain, depth, link_type, is_expanded
		FROM graph_nodes 
		WHERE source_query = ? AND depth = 1
		ORDER BY title
	`, sourceQuery)
	if err != nil {
		return root, err
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		var url, title, domain, linkType string
		var depth int
		var isExpanded bool

		if err := rows.Scan(&id, &url, &title, &domain, &depth, &linkType, &isExpanded); err != nil {
			continue
		}

		node := &GraphNode{
			Name:       title,
			URL:        url,
			Type:       "result",
			NodeID:     id,
			IsExpanded: isExpanded,
			Domain:     domain,
			Children:   []*GraphNode{},
		}

		// Eğer expand edilmişse, children'ları recursive yükle
		if isExpanded {
			node.Children = db.loadGraphChildren(id)
		}

		root.Children = append(root.Children, node)
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

// ExtractDomain URL'den domain çıkarır
func ExtractDomain(urlStr string) string {
	// http://xxx.onion/path -> xxx.onion
	urlStr = strings.TrimPrefix(urlStr, "http://")
	urlStr = strings.TrimPrefix(urlStr, "https://")

	if idx := strings.Index(urlStr, "/"); idx != -1 {
		urlStr = urlStr[:idx]
	}
	if idx := strings.Index(urlStr, "?"); idx != -1 {
		urlStr = urlStr[:idx]
	}

	return urlStr
}

// Close veritabanı bağlantısını kapatır
func (db *DB) Close() error {
	return db.conn.Close()
}
