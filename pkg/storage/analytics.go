package storage

import (
	"fmt"
	"net/url"
	"strings"
)

// AnalyticsData grafikler için veri yapısı
type AnalyticsData struct {
	TotalSites int `json:"totalSites"` // Toplam site sayısı
	Timeline   []struct {
		Date   string `json:"date"`
		Source string `json:"source"`
		Count  int    `json:"count"`
	} `json:"timeline"`
	Sources []struct {
		Source string `json:"source"`
		Count  int    `json:"count"`
	} `json:"sources"`
	Queries []struct {
		Query string `json:"query"`
		Count int    `json:"count"`
	} `json:"queries"`
	// YENİ: Domain analizi
	Domains []struct {
		Domain string `json:"domain"`
		Count  int    `json:"count"`
	} `json:"domains"`
	// YENİ: Kritiklik dağılımı
	Criticality []struct {
		Level int `json:"level"`
		Count int `json:"count"`
	} `json:"criticality"`
	// YENİ: Kategori dağılımı
	Categories []struct {
		Category string `json:"category"`
		Count    int    `json:"count"`
	} `json:"categories"`
	// YENİ: Kelime sıklığı istatistikleri
	KeywordStats struct {
		TotalHits   int `json:"totalHits"`
		AvgHits     int `json:"avgHits"`
		MaxHits     int `json:"maxHits"`
		WithHits    int `json:"withHits"`    // Hit > 0 olan sonuç sayısı
		WithoutHits int `json:"withoutHits"` // Hit = 0 olan sonuç sayısı
	} `json:"keywordStats"`
}

// GetAnalyticsData grafik verilerini getirir (query boşsa tüm veriler, doluysa sadece o sorgunun verileri)
func (db *DB) GetAnalyticsData(interval string, query string) (*AnalyticsData, error) {
	data := &AnalyticsData{
		// Boş slice olarak başlat (nil yerine) - JavaScript'te .map() hatası önlenir
		Timeline: []struct {
			Date   string `json:"date"`
			Source string `json:"source"`
			Count  int    `json:"count"`
		}{},
		Sources: []struct {
			Source string `json:"source"`
			Count  int    `json:"count"`
		}{},
		Queries: []struct {
			Query string `json:"query"`
			Count int    `json:"count"`
		}{},
	}

	// WHERE koşulu oluştur
	whereClause := ""
	var args []interface{}
	if query != "" {
		whereClause = " WHERE query = ?"
		args = append(args, query)
	}

	// 0. Toplam Site Sayısı
	countQuery := "SELECT COUNT(DISTINCT url) FROM search_results" + whereClause
	err := db.conn.QueryRow(countQuery, args...).Scan(&data.TotalSites)
	if err != nil {
		data.TotalSites = 0
	}

	// 1. Zaman Serisi (Timeline)
	dateFmt := "%Y-%m-%d"
	switch interval {
	case "hour":
		dateFmt = "%Y-%m-%d %H:00"
	case "week":
		dateFmt = "%Y-%W"
	case "month":
		dateFmt = "%Y-%m"
	}

	timelineSQL := fmt.Sprintf(`
		SELECT strftime('%s', created_at) as date, source, COUNT(*) as count 
		FROM search_results %s
		GROUP BY date, source 
		ORDER BY date ASC
	`, dateFmt, whereClause)

	timeRows, err := db.conn.Query(timelineSQL, args...)
	if err != nil {
		return nil, fmt.Errorf("timeline hatası: %w", err)
	}
	defer timeRows.Close()

	for timeRows.Next() {
		var d struct {
			Date   string `json:"date"`
			Source string `json:"source"`
			Count  int    `json:"count"`
		}
		if err := timeRows.Scan(&d.Date, &d.Source, &d.Count); err == nil {
			data.Timeline = append(data.Timeline, d)
		}
	}

	// 2. Kaynak Dağılımı
	sourceSQL := `SELECT source, COUNT(*) as count FROM search_results` + whereClause + ` GROUP BY source ORDER BY count DESC`
	sourceRows, err := db.conn.Query(sourceSQL, args...)
	if err != nil {
		return nil, fmt.Errorf("source stats hatası: %w", err)
	}
	defer sourceRows.Close()

	for sourceRows.Next() {
		var s struct {
			Source string `json:"source"`
			Count  int    `json:"count"`
		}
		if err := sourceRows.Scan(&s.Source, &s.Count); err == nil {
			data.Sources = append(data.Sources, s)
		}
	}

	// 3. Sorgu Performansı (sadece query boşsa göster - aksi halde anlamsız)
	if query == "" {
		queryRows, err := db.conn.Query(`
			SELECT query, COUNT(*) as count 
			FROM search_results 
			GROUP BY query 
			ORDER BY count DESC
			LIMIT 20
		`)
		if err == nil {
			defer queryRows.Close()
			for queryRows.Next() {
				var q struct {
					Query string `json:"query"`
					Count int    `json:"count"`
				}
				if err := queryRows.Scan(&q.Query, &q.Count); err == nil {
					data.Queries = append(data.Queries, q)
				}
			}
		}
	}

	// 4. Domain Analizi
	domainSQL := `SELECT url FROM search_results` + whereClause
	domainRows, err := db.conn.Query(domainSQL, args...)
	if err == nil {
		defer domainRows.Close()
		domainCounts := make(map[string]int)
		for domainRows.Next() {
			var rawURL string
			if err := domainRows.Scan(&rawURL); err == nil {
				if parsed, err := url.Parse(rawURL); err == nil {
					host := parsed.Host
					if strings.HasSuffix(host, ".onion") {
						domainCounts[host]++
					}
				}
			}
		}
		for domain, count := range domainCounts {
			data.Domains = append(data.Domains, struct {
				Domain string `json:"domain"`
				Count  int    `json:"count"`
			}{Domain: domain, Count: count})
		}
	}

	// 5. Kritiklik Dağılımı
	critSQL := `SELECT criticality, COUNT(*) as count FROM search_results` + whereClause + ` GROUP BY criticality ORDER BY criticality`
	critRows, err := db.conn.Query(critSQL, args...)
	if err == nil {
		defer critRows.Close()
		for critRows.Next() {
			var c struct {
				Level int `json:"level"`
				Count int `json:"count"`
			}
			if err := critRows.Scan(&c.Level, &c.Count); err == nil {
				data.Criticality = append(data.Criticality, c)
			}
		}
	}

	// 6. Kategori Dağılımı
	catWhere := whereClause
	catArgs := args
	if catWhere == "" {
		catWhere = " WHERE category != '' AND category IS NOT NULL"
	} else {
		catWhere += " AND category != '' AND category IS NOT NULL"
	}
	catSQL := `SELECT category, COUNT(*) as count FROM search_results` + catWhere + ` GROUP BY category ORDER BY count DESC LIMIT 15`
	catRows, err := db.conn.Query(catSQL, catArgs...)
	if err == nil {
		defer catRows.Close()
		for catRows.Next() {
			var c struct {
				Category string `json:"category"`
				Count    int    `json:"count"`
			}
			if err := catRows.Scan(&c.Category, &c.Count); err == nil {
				data.Categories = append(data.Categories, c)
			}
		}
	}

	// 7. Kelime Sıklığı İstatistikleri
	var totalHits, avgHits, maxHits, withHits, withoutHits int
	db.conn.QueryRow(`SELECT COALESCE(SUM(keyword_count), 0) FROM search_results`+whereClause, args...).Scan(&totalHits)
	db.conn.QueryRow(`SELECT COALESCE(AVG(keyword_count), 0) FROM search_results`+whereClause, args...).Scan(&avgHits)
	db.conn.QueryRow(`SELECT COALESCE(MAX(keyword_count), 0) FROM search_results`+whereClause, args...).Scan(&maxHits)

	withWhere := whereClause
	if withWhere == "" {
		withWhere = " WHERE keyword_count > 0"
	} else {
		withWhere += " AND keyword_count > 0"
	}
	db.conn.QueryRow(`SELECT COUNT(*) FROM search_results`+withWhere, args...).Scan(&withHits)

	withoutWhere := whereClause
	if withoutWhere == "" {
		withoutWhere = " WHERE (keyword_count = 0 OR keyword_count IS NULL)"
	} else {
		withoutWhere += " AND (keyword_count = 0 OR keyword_count IS NULL)"
	}
	db.conn.QueryRow(`SELECT COUNT(*) FROM search_results`+withoutWhere, args...).Scan(&withoutHits)

	data.KeywordStats.TotalHits = totalHits
	data.KeywordStats.AvgHits = avgHits
	data.KeywordStats.MaxHits = maxHits
	data.KeywordStats.WithHits = withHits
	data.KeywordStats.WithoutHits = withoutHits

	return data, nil
}
