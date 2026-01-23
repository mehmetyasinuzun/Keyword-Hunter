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
