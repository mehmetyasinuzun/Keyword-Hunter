package storage

import (
	"database/sql"
	"fmt"
	"sort"
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
	Note         string // Analist notu (vaka yönetimi)
	CaseStatus   string // "", "new", "investigating", "confirmed", "dismissed"
	CreatedAt    time.Time
}

// ValidCaseStatus vaka durumunun geçerli olup olmadığını döndürür.
func ValidCaseStatus(s string) bool {
	switch s {
	case "", "new", "investigating", "confirmed", "dismissed":
		return true
	}
	return false
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

// ApplyTagging tek adımda etiketleme alanlarını günceller.
func (db *DB) ApplyTagging(id int64, tags string, keywordCount int, criticality int, category string) error {
	if criticality < 1 {
		criticality = 1
	}
	if criticality > 5 {
		criticality = 5
	}

	category = strings.TrimSpace(category)
	if category == "" {
		category = "Genel"
	}

	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		UPDATE search_results
		SET auto_tags = ?, keyword_count = ?, criticality = ?, category = ?
		WHERE id = ?
	`, tags, keywordCount, criticality, category, id)
	if err != nil {
		return err
	}

	if err := syncResultTags(tx, id, tags); err != nil {
		return err
	}

	return tx.Commit()
}

// GetResultByID ID ile sonuç getirir
func (db *DB) GetResultByID(id int64) (*SearchResult, error) {
	var r SearchResult
	err := db.conn.QueryRow(`
		SELECT id, title, url, source, query, criticality, category, keyword_count, COALESCE(auto_tags, ''), COALESCE(note,''), COALESCE(case_status,''), created_at 
		FROM search_results WHERE id = ?
	`, id).Scan(&r.ID, &r.Title, &r.URL, &r.Source, &r.Query, &r.Criticality, &r.Category, &r.KeywordCount, &r.AutoTags, &r.Note, &r.CaseStatus, &r.CreatedAt)
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

	return results, rows.Err()
}

// GetNewResults son N saat içinde eklenen sonuçları döndürür
func (db *DB) GetNewResults(hours int, limit int) ([]SearchResult, error) {
	rows, err := db.conn.Query(`
		SELECT id, title, url, source, query, criticality, category, keyword_count, COALESCE(auto_tags, ''), created_at
		FROM search_results
		WHERE created_at >= datetime('now', ? || ' hours')
		ORDER BY created_at DESC
		LIMIT ?
	`, -hours, limit)
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
	return history, rows.Err()
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
	rows, err := db.conn.Query(`
		SELECT tag, COUNT(*) as count
		FROM result_tags
		GROUP BY tag
		ORDER BY count DESC
		LIMIT 300
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stats := make([]TagStat, 0, 128)
	for rows.Next() {
		var s TagStat
		if err := rows.Scan(&s.Tag, &s.Count); err == nil {
			stats = append(stats, s)
		}
	}

	if len(stats) > 0 {
		return stats, nil
	}

	// Backward compatibility: eski kayitlar için auto_tags parse fallback.
	legacyRows, err := db.conn.Query(`
		SELECT auto_tags FROM search_results WHERE auto_tags != '' AND auto_tags IS NOT NULL
	`)
	if err != nil {
		return []TagStat{}, nil
	}
	defer legacyRows.Close()

	tagCounts := make(map[string]int)
	for legacyRows.Next() {
		var tagsStr string
		if err := legacyRows.Scan(&tagsStr); err != nil {
			continue
		}
		for _, tag := range splitTags(tagsStr) {
			tag = trimSpace(tag)
			if tag != "" {
				tagCounts[tag]++
			}
		}
	}

	for tag, count := range tagCounts {
		stats = append(stats, TagStat{Tag: tag, Count: count})
	}

	sort.Slice(stats, func(i, j int) bool {
		if stats[i].Count == stats[j].Count {
			return stats[i].Tag < stats[j].Tag
		}
		return stats[i].Count > stats[j].Count
	})

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
		WHERE id IN (
			SELECT result_id FROM result_tags WHERE tag = ?
		)
		ORDER BY created_at DESC
		LIMIT ?
	`, strings.TrimSpace(tag), limit)
	if err != nil {
		rows, err = db.conn.Query(`
			SELECT id, title, url, source, query, criticality, category, keyword_count, COALESCE(auto_tags, ''), created_at 
			FROM search_results 
			WHERE auto_tags LIKE ?
			ORDER BY created_at DESC
			LIMIT ?
		`, "%"+tag+"%", limit)
		if err != nil {
			return nil, err
		}
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

func syncResultTags(tx *sql.Tx, resultID int64, tags string) error {
	if _, err := tx.Exec(`DELETE FROM result_tags WHERE result_id = ?`, resultID); err != nil {
		return err
	}

	normalized := normalizeTags(tags)
	if len(normalized) == 0 {
		return nil
	}

	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO result_tags (result_id, tag) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, tag := range normalized {
		if _, err := stmt.Exec(resultID, tag); err != nil {
			return err
		}
	}

	return nil
}

func normalizeTags(tags string) []string {
	parts := splitTags(tags)
	seen := make(map[string]bool)
	result := make([]string, 0, len(parts))

	for _, part := range parts {
		n := strings.ToLower(trimSpace(part))
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		result = append(result, n)
	}

	return result
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

// ResultFilter bulgu listesi için filtre/sıralama/sayfalama seçenekleri.
type ResultFilter struct {
	Query          string // sorgu adı içinde LIKE
	Text           string // başlık veya URL içinde LIKE
	Source         string // tam eşleşme
	Category       string // tam eşleşme
	MinCriticality int
	Tag            string // result_tags tam eşleşme
	CaseStatus     string // "", "new", "investigating", "confirmed", "dismissed", "any" (durumu olan)
	Sort           string // "newest" (varsayılan), "oldest", "criticality", "hits"
	Limit          int
	Offset         int
}

// Sources ve Categories filtre menüleri için ayrık değerleri döndürür.
func (db *DB) DistinctSourcesAndCategories() (sources []string, categories []string, err error) {
	rows, err := db.conn.Query(`SELECT DISTINCT source FROM search_results ORDER BY source`)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var v string
		if rows.Scan(&v) == nil {
			sources = append(sources, v)
		}
	}
	rows.Close()
	rows, err = db.conn.Query(`SELECT DISTINCT category FROM search_results WHERE category != '' ORDER BY category`)
	if err != nil {
		return sources, nil, err
	}
	for rows.Next() {
		var v string
		if rows.Scan(&v) == nil {
			categories = append(categories, v)
		}
	}
	rows.Close()
	return sources, categories, nil
}

// GetResultsFiltered filtreye uyan bulguları ve toplam eşleşme sayısını döndürür.
func (db *DB) GetResultsFiltered(f ResultFilter) ([]SearchResult, int, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 50
	}
	if f.Offset < 0 {
		f.Offset = 0
	}

	where := []string{"1=1"}
	args := []interface{}{}
	if q := strings.TrimSpace(f.Query); q != "" {
		where = append(where, "query LIKE ? ESCAPE '\\'")
		args = append(args, "%"+escapeLike(q)+"%")
	}
	if t := strings.TrimSpace(f.Text); t != "" {
		where = append(where, "(title LIKE ? ESCAPE '\\' OR url LIKE ? ESCAPE '\\')")
		args = append(args, "%"+escapeLike(t)+"%", "%"+escapeLike(t)+"%")
	}
	if f.Source != "" {
		where = append(where, "source = ?")
		args = append(args, f.Source)
	}
	if f.Category != "" {
		where = append(where, "category = ?")
		args = append(args, f.Category)
	}
	if f.MinCriticality > 1 {
		where = append(where, "criticality >= ?")
		args = append(args, f.MinCriticality)
	}
	if tag := strings.ToLower(strings.TrimSpace(f.Tag)); tag != "" {
		where = append(where, "id IN (SELECT result_id FROM result_tags WHERE tag = ?)")
		args = append(args, tag)
	}
	if cs := strings.TrimSpace(f.CaseStatus); cs != "" {
		if cs == "any" {
			where = append(where, "COALESCE(case_status,'') <> ''")
		} else if ValidCaseStatus(cs) {
			where = append(where, "case_status = ?")
			args = append(args, cs)
		}
	}
	whereSQL := strings.Join(where, " AND ")

	var total int
	if err := db.conn.QueryRow("SELECT COUNT(*) FROM search_results WHERE "+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	order := "created_at DESC, id DESC"
	switch f.Sort {
	case "oldest":
		order = "created_at ASC, id ASC"
	case "criticality":
		order = "criticality DESC, created_at DESC"
	case "hits":
		order = "keyword_count DESC, created_at DESC"
	}

	listArgs := append(append([]interface{}{}, args...), f.Limit, f.Offset)
	rows, err := db.conn.Query(`
		SELECT id, title, url, source, query, criticality, category, keyword_count, COALESCE(auto_tags, ''), COALESCE(note,''), COALESCE(case_status,''), created_at
		FROM search_results
		WHERE `+whereSQL+`
		ORDER BY `+order+`
		LIMIT ? OFFSET ?
	`, listArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	results := make([]SearchResult, 0, f.Limit)
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.ID, &r.Title, &r.URL, &r.Source, &r.Query, &r.Criticality, &r.Category, &r.KeywordCount, &r.AutoTags, &r.Note, &r.CaseStatus, &r.CreatedAt); err != nil {
			continue
		}
		results = append(results, r)
	}
	return results, total, rows.Err()
}

// DeleteResults verilen ID'lerdeki bulguları siler (etiketler CASCADE ile gider).
func (db *DB) DeleteResults(ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]interface{}, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	res, err := db.conn.Exec("DELETE FROM search_results WHERE id IN ("+placeholders+")", args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}

// EnsureCaseColumns search_results tablosuna vaka yönetimi sütunlarını ekler (idempotent).
func (db *DB) EnsureCaseColumns() error {
	// "duplicate column" hatası yutulur
	db.conn.Exec(`ALTER TABLE search_results ADD COLUMN note TEXT DEFAULT ''`)
	db.conn.Exec(`ALTER TABLE search_results ADD COLUMN case_status TEXT DEFAULT ''`)
	_, _ = db.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_search_results_case ON search_results(case_status)`)
	return nil
}

// UpdateResultCase bir bulgunun vaka durumunu ve/veya notunu günceller.
func (db *DB) UpdateResultCase(id int64, status, note string) (int64, error) {
	if !ValidCaseStatus(status) {
		return 0, fmt.Errorf("geçersiz vaka durumu")
	}
	if len(note) > 4000 {
		note = note[:4000]
	}
	res, err := db.conn.Exec(`UPDATE search_results SET case_status = ?, note = ? WHERE id = ?`, status, note, id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CaseCounts vaka durumu başına sayım döndürür (dashboard/analytics).
func (db *DB) CaseCounts() (map[string]int, error) {
	rows, err := db.conn.Query(`SELECT COALESCE(NULLIF(case_status,''),'none') AS st, COUNT(*) FROM search_results GROUP BY st`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if rows.Scan(&st, &n) == nil {
			out[st] = n
		}
	}
	return out, rows.Err()
}
