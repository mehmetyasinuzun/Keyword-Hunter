package storage

import (
	"database/sql"
	"time"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/shared"
)

// SearchResult arama sonucu modeli
type SearchResult struct {
	ID           int64
	Title        string
	URL          string
	Source       string // Hangi arama motorundan geldi
	Query        string // Hangi arama sorgusu ile bulundu
	Criticality  int    // 1-5 arası kritiklik seviyesi
	Category     string // Veri kategorisi
	KeywordCount int    // İçerikteki anahtar kelime sayısı
	CreatedAt    time.Time
}

// QueryInfo sorgu bilgisi
type QueryInfo struct {
	Query string `json:"query"`
	Count int    `json:"count"`
}

// SaveResult tek bir sonucu kaydeder
func (db *DB) SaveResult(title, url, source, query string) error {
	_, err := db.conn.Exec(`
		INSERT OR IGNORE INTO search_results (title, url, source, query, criticality, category)
		VALUES (?, ?, ?, ?, 1, 'Genel')
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
		INSERT OR IGNORE INTO search_results (title, url, source, query, criticality, category)
		VALUES (?, ?, ?, ?, 1, 'Genel')
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
		if affected > 0 {
			saved++
		}

		// Graph nodes tablosuna da kaydet (derinleştirme için altyapı)
		domain := shared.ExtractDomain(r.URL)
		db.conn.Exec(`
			INSERT OR IGNORE INTO graph_nodes (url, title, domain, depth, link_type, source_query)
			VALUES (?, ?, ?, 1, 'search', ?)
		`, r.URL, r.Title, domain, r.Query)
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}

	return saved, nil
}

// UpdateKeywordCount anahtar kelime sayısını günceller
func (db *DB) UpdateKeywordCount(id int64, count int) error {
	_, err := db.conn.Exec("UPDATE search_results SET keyword_count = ? WHERE id = ?", count, id)
	return err
}

// GetResultByID ID ile sonuç getirir
func (db *DB) GetResultByID(id int64) (*SearchResult, error) {
	var r SearchResult
	err := db.conn.QueryRow(`
		SELECT id, title, url, source, query, criticality, category, keyword_count, created_at 
		FROM search_results WHERE id = ?
	`, id).Scan(&r.ID, &r.Title, &r.URL, &r.Source, &r.Query, &r.Criticality, &r.Category, &r.KeywordCount, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// GetResults sonuçları getirir (opsiyonel filtrelerle)
func (db *DB) GetResults(limit int, query string) ([]SearchResult, error) {
	var rows *sql.Rows
	var err error

	if query != "" {
		rows, err = db.conn.Query(`
			SELECT id, title, url, source, query, criticality, category, keyword_count, created_at 
			FROM search_results 
			WHERE query LIKE ?
			ORDER BY created_at DESC
			LIMIT ?
		`, "%"+query+"%", limit)
	} else {
		rows, err = db.conn.Query(`
			SELECT id, title, url, source, query, criticality, category, keyword_count, created_at 
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
		if err := rows.Scan(&r.ID, &r.Title, &r.URL, &r.Source, &r.Query, &r.Criticality, &r.Category, &r.KeywordCount, &r.CreatedAt); err != nil {
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

// SaveSearchHistory arama geçmişine ekler
func (db *DB) SaveSearchHistory(query string, resultCount int) error {
	_, err := db.conn.Exec(`
		INSERT INTO search_history (query, result_count)
		VALUES (?, ?)
	`, query, resultCount)
	return err
}

// SearchHistoryItem arama geçmişi öğesi
type SearchHistoryItem struct {
	Query       string
	ResultCount int
	SearchedAt  time.Time
}

// GetSearchHistory son aramaları getirir
func (db *DB) GetSearchHistory(limit int) ([]SearchHistoryItem, error) {
	rows, err := db.conn.Query(`
		SELECT query, result_count, searched_at 
		FROM search_history 
		ORDER BY searched_at DESC 
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []SearchHistoryItem
	for rows.Next() {
		var item SearchHistoryItem
		if err := rows.Scan(&item.Query, &item.ResultCount, &item.SearchedAt); err != nil {
			continue
		}
		history = append(history, item)
	}
	return history, nil
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
