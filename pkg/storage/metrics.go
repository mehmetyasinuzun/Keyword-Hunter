package storage

import (
	"fmt"
	"time"
)

// MetricsSnapshot /metrics'in veritabanından türetilen göstergeleri: tek
// seferde okunur ve web katmanında önbelleğe alınır (her kazımada değil, en
// fazla 30 sn'de bir). Yalnız sayımlar ve motor adları içerir; sorgu, URL ya
// da kullanıcı adı taşımaz.
type MetricsSnapshot struct {
	// FindingsBySeverity kritiklik (1-5) → kayıtlı bulgu sayısı.
	FindingsBySeverity map[int]int64
	// ActiveSessions süresi dolmamış etkileşimli oturum sayısı.
	ActiveSessions int64
	// WatchlistEnabled etkin izleme öğesi; WatchlistDown bunlardan son
	// kontrolü "down" olanlar (engel/captcha "down" sayılmaz).
	WatchlistEnabled int64
	WatchlistDown    int64
	// Engines arama motoru sağlık kayıtları (ad sırasıyla).
	Engines []EngineStatDB
	// Backup zamanlanmış yedeğin son çalışması (hiç çalışmadıysa nil).
	Backup *MaintenanceRun
}

// migrateMetricsIndexes şema 5: /metrics önbelleği yenilenirken çalışan iki
// sayım sorgusu için indeksler. Kritikliğe göre bulgu sayımı indeksten
// (tablo taraması olmadan) okunur; izleme öğesinin son kontrolü
// (watchlist_id, checked_at, id) üzerinden tek adımda bulunur.
func migrateMetricsIndexes(q runner) error {
	for _, stmt := range []string{
		`CREATE INDEX idx_search_results_criticality ON search_results(criticality)`,
		`CREATE INDEX idx_watchlist_checks_latest ON watchlist_checks(watchlist_id, checked_at, id)`,
	} {
		if _, err := q.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// MetricsSnapshot göstergeleri okur. Sorgular salt okunur ve indekslidir.
func (db *DB) MetricsSnapshot(now time.Time) (MetricsSnapshot, error) {
	snap := MetricsSnapshot{FindingsBySeverity: map[int]int64{}}

	rows, err := db.conn.Query(`SELECT criticality, COUNT(*) FROM search_results GROUP BY criticality`)
	if err != nil {
		return snap, fmt.Errorf("bulgu sayımı: %w", err)
	}
	for rows.Next() {
		var crit int
		var n int64
		if err := rows.Scan(&crit, &n); err != nil {
			_ = rows.Close()
			return snap, fmt.Errorf("bulgu sayımı: %w", err)
		}
		// Aralık dışı eski değerler (0, >5) uç kovalara toplanır.
		crit = min(max(crit, 1), 5)
		snap.FindingsBySeverity[crit] += n
	}
	if err := rows.Close(); err != nil {
		return snap, err
	}
	if err := rows.Err(); err != nil {
		return snap, err
	}

	if err := db.conn.QueryRow(`SELECT COUNT(*) FROM sessions WHERE datetime(expires_at) > datetime(?)`, now.UTC()).
		Scan(&snap.ActiveSessions); err != nil {
		return snap, fmt.Errorf("oturum sayımı: %w", err)
	}

	if err := db.conn.QueryRow(`
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN (
		           SELECT c.status FROM watchlist_checks c
		           WHERE c.watchlist_id = w.id
		           ORDER BY c.checked_at DESC, c.id DESC LIMIT 1
		       ) = 'down' THEN 1 ELSE 0 END), 0)
		FROM watchlist w WHERE w.enabled = 1`).Scan(&snap.WatchlistEnabled, &snap.WatchlistDown); err != nil {
		return snap, fmt.Errorf("izleme listesi sayımı: %w", err)
	}

	if snap.Engines, err = db.GetAllEngineStats(); err != nil {
		return snap, fmt.Errorf("motor durumu: %w", err)
	}
	if snap.Backup, err = db.GetMaintenanceRun(MaintenanceBackup); err != nil {
		return snap, fmt.Errorf("yedek durumu: %w", err)
	}
	return snap, nil
}
