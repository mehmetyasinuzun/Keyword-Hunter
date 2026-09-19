package storage

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // Pure Go SQLite driver

	"keywordhunter-mvp/pkg/logger"
)

// DB veritabanı bağlantısı
type DB struct {
	conn *sql.DB
}

// New yeni veritabanı bağlantısı oluşturur
func New(dbPath string) (*DB, error) {
	// PRAGMA'ları DSN üzerinden ayarla - her bağlantıda tutarlı uygulanır.
	// _time_format=sqlite: time.Time değerleri SQLite'ın datetime() fonksiyonunun
	// parse edebildiği "YYYY-MM-DD HH:MM:SS.SSS+HH:MM" biçiminde yazılır. Sürücünün
	// varsayılan biçimi (Go time.String, monotonic saat dahil) SQL tarafında
	// karşılaştırılamıyordu.
	dsn := dbPath + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_time_format=sqlite"
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("veritabanı açılamadı: %w", err)
	}

	// SQLite'da tek writer modeli ve dosya kilitlenmelerini azaltmak için
	// bağlantı havuzunu tek bağlantı ile sınırla.
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	conn.SetConnMaxLifetime(0)

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
			url TEXT NOT NULL,
			source TEXT NOT NULL,
			query TEXT NOT NULL,
			criticality INTEGER DEFAULT 1,
			category TEXT DEFAULT 'Genel',
			keyword_count INTEGER DEFAULT 0,
			auto_tags TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(url, source, query)
		);
		
		CREATE INDEX IF NOT EXISTS idx_search_results_query ON search_results(query);
		CREATE INDEX IF NOT EXISTS idx_search_results_source ON search_results(source);
		CREATE INDEX IF NOT EXISTS idx_search_results_created ON search_results(created_at);
	`)
	if err != nil {
		return fmt.Errorf("tablo oluşturulamadı: %w", err)
	}

	// Sonuç-etiket normalize tablosu
	_, err = db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS result_tags (
			result_id INTEGER NOT NULL,
			tag TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (result_id, tag),
			FOREIGN KEY (result_id) REFERENCES search_results(id) ON DELETE CASCADE
		);

		CREATE INDEX IF NOT EXISTS idx_result_tags_tag ON result_tags(tag);
		CREATE INDEX IF NOT EXISTS idx_result_tags_result ON result_tags(result_id);
	`)
	if err != nil {
		return fmt.Errorf("result_tags tablosu oluşturulamadı: %w", err)
	}

	// Migration: auto_tags sütununu mevcut tablolara ekle (varsa sessizce atla)
	db.conn.Exec(`ALTER TABLE search_results ADD COLUMN auto_tags TEXT DEFAULT ''`)

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
	if err := db.EnsureGraphNodeUniqueness(); err != nil {
		return err
	}

	// Tagging jobs tablosu - toplu etiketleme iş kuyruğu
	_, err = db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS tagging_jobs (
			id TEXT PRIMARY KEY,
			query TEXT,
			total_count INTEGER NOT NULL,
			processed_count INTEGER DEFAULT 0,
			success_count INTEGER DEFAULT 0,
			failure_count INTEGER DEFAULT 0,
			status TEXT NOT NULL,
			error_message TEXT DEFAULT '',
			result_ids_json TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			started_at DATETIME,
			finished_at DATETIME
		);

		CREATE INDEX IF NOT EXISTS idx_tagging_jobs_status ON tagging_jobs(status);
		CREATE INDEX IF NOT EXISTS idx_tagging_jobs_created ON tagging_jobs(created_at);
	`)
	if err != nil {
		return fmt.Errorf("tagging_jobs tablosu oluşturulamadı: %w", err)
	}

	// Alert config tablosu - tek satır (id=1 zorunlu)
	_, err = db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS alert_config (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			webhook_url TEXT DEFAULT '',
			min_criticality INTEGER DEFAULT 3,
			enabled INTEGER DEFAULT 0,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		INSERT OR IGNORE INTO alert_config (id) VALUES (1);
	`)
	if err != nil {
		return fmt.Errorf("alert_config tablosu oluşturulamadı: %w", err)
	}

	// Kalıcı oturumlar tablosu
	_, err = db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			csrf_token TEXT NOT NULL,
			expires_at DATETIME NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			last_seen_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);
		CREATE INDEX IF NOT EXISTS idx_sessions_username ON sessions(username);
	`)
	if err != nil {
		return fmt.Errorf("sessions tablosu oluşturulamadı: %w", err)
	}

	// İzleme listesi (watchlist) tabloları
	if err := db.EnsureWatchlistSchema(); err != nil {
		return err
	}
	if err := db.SeedDefaultWatchlist(); err != nil {
		return fmt.Errorf("watchlist seed başarısız: %w", err)
	}

	// Planlı arama (scheduler) + motor sağlık izleme tabloları
	if err := db.EnsureScheduledSchema(); err != nil {
		return fmt.Errorf("scheduled şeması başarısız: %w", err)
	}
	if err := db.EnsureEngineStatsSchema(); err != nil {
		return fmt.Errorf("engine_stats şeması başarısız: %w", err)
	}

	// Ekran görüntüsü tablosu
	if err := db.EnsureScreenshotSchema(); err != nil {
		return fmt.Errorf("screenshots şeması başarısız: %w", err)
	}

	// IOC / artifact tablosu
	if err := db.EnsureArtifactSchema(); err != nil {
		return err
	}

	// Site profilleri (çerez/UA/JS render)
	if err := db.EnsureSiteProfileSchema(); err != nil {
		return fmt.Errorf("site_profiles şeması başarısız: %w", err)
	}

	// Örümcek (site tarama) işleri
	if err := db.EnsureCrawlSchema(); err != nil {
		return fmt.Errorf("crawl_jobs şeması başarısız: %w", err)
	}

	// Kullanıcılar (çoklu kullanıcı + rol)
	if err := db.EnsureUserSchema(); err != nil {
		return fmt.Errorf("users şeması başarısız: %w", err)
	}

	// Eski sürümlerin yazdığı, SQL tarafında parse edilemeyen zaman damgalarını normalize et
	if err := db.normalizeLegacyTimestamps(); err != nil {
		return fmt.Errorf("zaman damgası migrasyonu başarısız: %w", err)
	}

	return nil
}

// normalizeLegacyTimestamps eski sürümlerde Go'nun time.String() biçimiyle
// ("2026-01-02 15:04:05.123 +0300 +03 m=+0.5") yazılmış ve SQLite datetime()
// tarafından parse edilemeyen sütunları standart biçime çevirir. Idempotenttir:
// yalnızca datetime(col) IS NULL olan satırlara dokunur.
func (db *DB) normalizeLegacyTimestamps() error {
	targets := []struct{ table, id, col string }{
		{"scheduled_searches", "id", "next_run_at"},
		{"scheduled_searches", "id", "last_run_at"},
		{"sessions", "id", "expires_at"},
		{"engine_stats", "name", "last_checked_at"},
		{"screenshots", "id", "taken_at"},
	}
	for _, t := range targets {
		q := fmt.Sprintf(`SELECT %s, %s FROM %s WHERE %s IS NOT NULL AND datetime(%s) IS NULL`, t.id, t.col, t.table, t.col, t.col)
		rows, err := db.conn.Query(q)
		if err != nil {
			return err
		}
		type fix struct {
			id  interface{}
			val time.Time
		}
		var fixes []fix
		for rows.Next() {
			var id interface{}
			var val time.Time
			if err := rows.Scan(&id, &val); err != nil {
				continue // parse edilemeyen değer: dokunma
			}
			fixes = append(fixes, fix{id: id, val: val})
		}
		rows.Close()
		for _, f := range fixes {
			if _, err := db.conn.Exec(fmt.Sprintf(`UPDATE %s SET %s = ? WHERE %s = ?`, t.table, t.col, t.id), f.val.UTC(), f.id); err != nil {
				return err
			}
		}
		if len(fixes) > 0 {
			logger.Info("Zaman damgası migrasyonu: %s.%s için %d satır normalize edildi", t.table, t.col, len(fixes))
		}
	}
	return nil
}

// Close veritabanı bağlantısını kapatır
func (db *DB) Close() error {
	return db.conn.Close()
}

// GetDBConn veritabanı bağlantısını döndürür
func (db *DB) GetDBConn() *sql.DB {
	return db.conn
}
