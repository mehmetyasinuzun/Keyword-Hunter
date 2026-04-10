package storage

import (
	"database/sql"
	"fmt"
	"strings"
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
	AutoTags     string // Otomatik çıkarılan etiketler (virgülle ayrılmış)
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

// SaveResults birden fazla sonucu kaydeder ve kaydedilen ID'leri döndürür
func (db *DB) SaveResults(results []SearchResult) (int, error) {
	return db.SaveResultsWithIDs(results, nil)
}

// SaveResultsWithIDs birden fazla sonucu kaydeder ve kaydedilen ID'leri alır
func (db *DB) SaveResultsWithIDs(results []SearchResult, savedIDs *[]int64) (int, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT OR IGNORE INTO search_results (title, url, source, query, criticality, category, keyword_count)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	saved := 0
	for _, r := range results {
		// Default values
		crit := r.Criticality
		if crit == 0 {
			crit = 1
		}
		cat := r.Category
		if cat == "" {
			cat = "Genel"
		}
		result, err := stmt.Exec(r.Title, r.URL, r.Source, r.Query, crit, cat, r.KeywordCount)
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
			// Kaydedilen ID'yi al
			if savedIDs != nil {
				lastID, err := result.LastInsertId()
				if err == nil {
					*savedIDs = append(*savedIDs, lastID)
				}
			}
		}

		// Graph nodes tablosuna da kaydet (derinleştirme için altyapı)
		domain := shared.ExtractDomain(r.URL)
		_, _ = tx.Exec(`
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

// UpdateAutoTags otomatik etiketleri günceller
func (db *DB) UpdateAutoTags(id int64, tags string) error {
	_, err := db.conn.Exec("UPDATE search_results SET auto_tags = ? WHERE id = ?", tags, id)
	return err
}

// GetResultByID ID ile sonuç getirir
func (db *DB) GetResultByID(id int64) (*SearchResult, error) {
	var r SearchResult
	err := db.conn.QueryRow(`
		SELECT id, title, url, source, query, criticality, category, keyword_count, COALESCE(auto_tags, ''), created_at 
		FROM search_results WHERE id = ?
	`, id).Scan(&r.ID, &r.Title, &r.URL, &r.Source, &r.Query, &r.Criticality, &r.Category, &r.KeywordCount, &r.AutoTags, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// ExistingResultIDSet verilen ID'lerden veritabanında olanları set olarak döndürür.
func (db *DB) ExistingResultIDSet(ids []int64) (map[int64]bool, error) {
	result := make(map[int64]bool)
	if len(ids) == 0 {
		return result, nil
	}

	uniqueIDs := make([]int64, 0, len(ids))
	seen := make(map[int64]bool)
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		uniqueIDs = append(uniqueIDs, id)
	}

	if len(uniqueIDs) == 0 {
		return result, nil
	}

	placeholders := strings.TrimRight(strings.Repeat("?,", len(uniqueIDs)), ",")
	query := fmt.Sprintf("SELECT id FROM search_results WHERE id IN (%s)", placeholders)

	args := make([]interface{}, 0, len(uniqueIDs))
	for _, id := range uniqueIDs {
		args = append(args, id)
	}

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			continue
		}
		result[id] = true
	}

	return result, rows.Err()
}

// GetResults sonuçları getirir (opsiyonel filtrelerle)
func (db *DB) GetResults(limit int, query string) ([]SearchResult, error) {
	var rows *sql.Rows
	var err error

	if query != "" {
		rows, err = db.conn.Query(`
			SELECT id, title, url, source, query, criticality, category, keyword_count, COALESCE(auto_tags, ''), created_at 
			FROM search_results 
			WHERE query LIKE ?
			ORDER BY created_at DESC
			LIMIT ?
		`, "%"+query+"%", limit)
	} else {
		rows, err = db.conn.Query(`
			SELECT id, title, url, source, query, criticality, category, keyword_count, COALESCE(auto_tags, ''), created_at 
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
		if err := rows.Scan(&r.ID, &r.Title, &r.URL, &r.Source, &r.Query, &r.Criticality, &r.Category, &r.KeywordCount, &r.AutoTags, &r.CreatedAt); err != nil {
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

// TagStat etiket istatistiği
type TagStat struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

// GetTagStats tüm etiketlerin istatistiklerini döndürür (tag cloud için)
func (db *DB) GetTagStats() ([]TagStat, error) {
	// auto_tags sütunundaki virgülle ayrılmış etiketleri sayar
	rows, err := db.conn.Query(`
		SELECT auto_tags FROM search_results WHERE auto_tags != '' AND auto_tags IS NOT NULL
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Tag sayaçları
	tagCounts := make(map[string]int)

	for rows.Next() {
		var tagsStr string
		if err := rows.Scan(&tagsStr); err != nil {
			continue
		}
		// Virgülle ayrılmış etiketleri parse et
		tags := splitTags(tagsStr)
		for _, tag := range tags {
			tag = trimSpace(tag)
			if tag != "" {
				tagCounts[tag]++
			}
		}
	}

	// Map'i slice'a çevir ve sırala
	var stats []TagStat
	for tag, count := range tagCounts {
		stats = append(stats, TagStat{Tag: tag, Count: count})
	}

	// Count'a göre sırala (büyükten küçüğe)
	for i := 0; i < len(stats); i++ {
		for j := i + 1; j < len(stats); j++ {
			if stats[j].Count > stats[i].Count {
				stats[i], stats[j] = stats[j], stats[i]
			}
		}
	}

	return stats, nil
}

// GetResultsByTag belirli bir etikete sahip sonuçları döndürür
func (db *DB) GetResultsByTag(tag string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := db.conn.Query(`
		SELECT id, title, url, source, query, criticality, category, keyword_count, COALESCE(auto_tags, ''), created_at 
		FROM search_results 
		WHERE auto_tags LIKE ?
		ORDER BY created_at DESC
		LIMIT ?
	`, "%"+tag+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.ID, &r.Title, &r.URL, &r.Source, &r.Query, &r.Criticality, &r.Category, &r.KeywordCount, &r.AutoTags, &r.CreatedAt); err != nil {
			continue
		}
		results = append(results, r)
	}

	return results, nil
}

// GetTaggedResultsCount etiketli sonuç sayısını döndürür
func (db *DB) GetTaggedResultsCount() (int, int, error) {
	var tagged, total int
	db.conn.QueryRow("SELECT COUNT(*) FROM search_results WHERE auto_tags != '' AND auto_tags IS NOT NULL").Scan(&tagged)
	db.conn.QueryRow("SELECT COUNT(*) FROM search_results").Scan(&total)
	return tagged, total, nil
}

// Helper functions
func splitTags(s string) []string {
	var result []string
	current := ""
	for _, c := range s {
		if c == ',' {
			result = append(result, current)
			current = ""
		} else {
			current += string(c)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}

func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
