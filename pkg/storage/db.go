package storage

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

// DB veritabanı bağlantısı
type DB struct {
	conn *sql.DB
}

// New yeni veritabanı bağlantısı oluşturur
func New(dbPath string) (*DB, error) {
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("veritabanı açılamadı: %w", err)
	}

	// SQLite'da tek writer modeli ve dosya kilitlenmelerini azaltmak için
	// bağlantı havuzunu tek bağlantı ile sınırla.
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	conn.SetConnMaxLifetime(0)

	if _, err := conn.Exec("PRAGMA journal_mode = WAL;"); err != nil {
		return nil, fmt.Errorf("sqlite WAL modu ayarlanamadı: %w", err)
	}
	if _, err := conn.Exec("PRAGMA busy_timeout = 5000;"); err != nil {
		return nil, fmt.Errorf("sqlite busy_timeout ayarlanamadı: %w", err)
	}
	if _, err := conn.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		return nil, fmt.Errorf("sqlite foreign_keys ayarlanamadı: %w", err)
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
