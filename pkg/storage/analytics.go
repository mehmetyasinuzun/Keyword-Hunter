package storage

import (
	"fmt"
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
}

// GetAnalyticsData grafik verilerini getirir
func (db *DB) GetAnalyticsData(interval string) (*AnalyticsData, error) {
	data := &AnalyticsData{}

	// 0. Toplam Site Sayısı
	err := db.conn.QueryRow("SELECT COUNT(DISTINCT url) FROM search_results").Scan(&data.TotalSites)
	if err != nil {
		// Hata kritik değil, logla devam et veya 0 varsay
		data.TotalSites = 0
	}

	// 1. Zaman Serisi (Timeline)
	// Interval'e göre tarih formatını belirle
	dateFmt := "%Y-%m-%d" // Default: Günlük
	switch interval {
	case "hour":
		dateFmt = "%Y-%m-%d %H:00"
	case "week":
		dateFmt = "%Y-%W"
	case "month":
		dateFmt = "%Y-%m"
	}

	query := fmt.Sprintf(`
		SELECT strftime('%s', created_at) as date, source, COUNT(*) as count 
		FROM search_results 
		GROUP BY date, source 
		ORDER BY date ASC
	`, dateFmt)

	timeRows, err := db.conn.Query(query)
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
	sourceRows, err := db.conn.Query(`
		SELECT source, COUNT(*) as count 
		FROM search_results 
		GROUP BY source 
		ORDER BY count DESC
	`)
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

	// 3. Sorgu Performansı
	queryRows, err := db.conn.Query(`
		SELECT query, COUNT(*) as count 
		FROM search_results 
		GROUP BY query 
		ORDER BY count DESC
		LIMIT 20
	`)
	if err != nil {
		return nil, fmt.Errorf("query stats hatası: %w", err)
	}
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

	return data, nil
}
