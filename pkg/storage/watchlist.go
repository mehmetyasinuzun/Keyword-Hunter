package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// WatchlistItem izlenen tek bir onion sitesi
type WatchlistItem struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Category  string    `json:"category"`
	Notes     string    `json:"notes"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"createdAt"`
}

// WatchlistStatus izlenen site + son kontrol durumu
type WatchlistStatus struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	URL         string     `json:"url"`
	Category    string     `json:"category"`
	Notes       string     `json:"notes"`
	Enabled     bool       `json:"enabled"`
	Status      string     `json:"status"`
	LastChecked *time.Time `json:"lastChecked"`
	HTTPCode    int        `json:"httpCode"`
	ResponseMs  int        `json:"responseMs"`
	Title       string     `json:"title"`
	ContentHash string     `json:"contentHash"`
	Changed     bool       `json:"changed"`
	UptimePct   int        `json:"uptimePct"`
}

const uptimeSampleSize = 20

// EnsureWatchlistSchema watchlist tablolarını oluşturur
func (db *DB) EnsureWatchlistSchema() error {
	_, err := db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS watchlist (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			url TEXT NOT NULL UNIQUE,
			category TEXT DEFAULT 'Genel',
			notes TEXT DEFAULT '',
			enabled INTEGER DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE INDEX IF NOT EXISTS idx_watchlist_enabled ON watchlist(enabled);
	`)
	if err != nil {
		return fmt.Errorf("watchlist tablosu oluşturulamadı: %w", err)
	}

	_, err = db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS watchlist_checks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			watchlist_id INTEGER NOT NULL,
			checked_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			status TEXT NOT NULL,
			http_code INTEGER DEFAULT 0,
			response_ms INTEGER DEFAULT 0,
			content_hash TEXT DEFAULT '',
			title TEXT DEFAULT '',
			changed INTEGER DEFAULT 0,
			FOREIGN KEY (watchlist_id) REFERENCES watchlist(id) ON DELETE CASCADE
		);

		CREATE INDEX IF NOT EXISTS idx_watchlist_checks_item ON watchlist_checks(watchlist_id);
		CREATE INDEX IF NOT EXISTS idx_watchlist_checks_time ON watchlist_checks(checked_at);
	`)
	if err != nil {
		return fmt.Errorf("watchlist_checks tablosu oluşturulamadı: %w", err)
	}

	return nil
}

// normalizeWatchlistURL URL'yi normalize eder
func normalizeWatchlistURL(url string) string {
	url = strings.TrimSpace(url)
	url = strings.TrimRight(url, "/")
	return url
}

// AddWatchlistItem yeni izleme öğesi ekler, çakışmada günceller
func (db *DB) AddWatchlistItem(name, url, category, notes string) (int64, error) {
	name = strings.TrimSpace(name)
	url = normalizeWatchlistURL(url)
	category = strings.TrimSpace(category)
	if category == "" {
		category = "Genel"
	}
	notes = strings.TrimSpace(notes)

	res, err := db.conn.Exec(`
		INSERT INTO watchlist (name, url, category, notes, enabled)
		VALUES (?, ?, ?, ?, 1)
		ON CONFLICT(url) DO UPDATE SET
			name = excluded.name,
			category = excluded.category,
			notes = excluded.notes
	`, name, url, category, notes)
	if err != nil {
		return 0, err
	}

	id, err := res.LastInsertId()
	if err != nil || id == 0 {
		// ON CONFLICT güncellemesinde LastInsertId güvenilir olmayabilir
		var existingID int64
		if qErr := db.conn.QueryRow("SELECT id FROM watchlist WHERE url = ?", url).Scan(&existingID); qErr == nil {
			return existingID, nil
		}
	}
	return id, nil
}

// DeleteWatchlistItem izleme öğesini ve kontrollerini siler
func (db *DB) DeleteWatchlistItem(id int64) error {
	if _, err := db.conn.Exec("DELETE FROM watchlist_checks WHERE watchlist_id = ?", id); err != nil {
		return err
	}
	_, err := db.conn.Exec("DELETE FROM watchlist WHERE id = ?", id)
	return err
}

// SetWatchlistEnabled izleme öğesinin durumunu değiştirir
func (db *DB) SetWatchlistEnabled(id int64, enabled bool) error {
	val := 0
	if enabled {
		val = 1
	}
	_, err := db.conn.Exec("UPDATE watchlist SET enabled = ? WHERE id = ?", val, id)
	return err
}

// GetWatchlistItems izleme öğelerini döndürür
func (db *DB) GetWatchlistItems(onlyEnabled bool) ([]WatchlistItem, error) {
	query := `
		SELECT id, name, url, category, COALESCE(notes, ''), enabled, created_at
		FROM watchlist
	`
	if onlyEnabled {
		query += " WHERE enabled = 1"
	}
	query += " ORDER BY created_at ASC"

	rows, err := db.conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []WatchlistItem
	for rows.Next() {
		var item WatchlistItem
		var enabled int
		if err := rows.Scan(&item.ID, &item.Name, &item.URL, &item.Category, &item.Notes, &enabled, &item.CreatedAt); err != nil {
			continue
		}
		item.Enabled = enabled == 1
		items = append(items, item)
	}
	return items, rows.Err()
}

// RecordWatchlistCheck bir kontrol sonucunu kaydeder
func (db *DB) RecordWatchlistCheck(itemID int64, status string, code, responseMs int, hash, title string, changed bool) error {
	changedVal := 0
	if changed {
		changedVal = 1
	}
	_, err := db.conn.Exec(`
		INSERT INTO watchlist_checks (watchlist_id, status, http_code, response_ms, content_hash, title, changed)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, itemID, status, code, responseMs, hash, title, changedVal)
	return err
}

// GetWatchlistWithStatus her öğeyi son kontrol durumu ve uptime yüzdesiyle döndürür
func (db *DB) GetWatchlistWithStatus() ([]WatchlistStatus, error) {
	rows, err := db.conn.Query(`
		SELECT
			w.id, w.name, w.url, w.category, COALESCE(w.notes, ''), w.enabled,
			COALESCE(c.status, ''),
			c.checked_at,
			COALESCE(c.http_code, 0),
			COALESCE(c.response_ms, 0),
			COALESCE(c.title, ''),
			COALESCE(c.content_hash, ''),
			COALESCE(c.changed, 0)
		FROM watchlist w
		LEFT JOIN watchlist_checks c ON c.id = (
			SELECT id FROM watchlist_checks
			WHERE watchlist_id = w.id
			ORDER BY checked_at DESC, id DESC
			LIMIT 1
		)
		ORDER BY w.created_at ASC
	`)
	if err != nil {
		return nil, err
	}

	var items []WatchlistStatus
	for rows.Next() {
		var s WatchlistStatus
		var enabled, changed int
		var lastChecked sql.NullTime
		if err := rows.Scan(
			&s.ID, &s.Name, &s.URL, &s.Category, &s.Notes, &enabled,
			&s.Status, &lastChecked, &s.HTTPCode, &s.ResponseMs, &s.Title, &s.ContentHash, &changed,
		); err != nil {
			continue
		}
		s.Enabled = enabled == 1
		s.Changed = changed == 1
		if lastChecked.Valid {
			t := lastChecked.Time
			s.LastChecked = &t
		}
		items = append(items, s)
	}
	rowsErr := rows.Err()
	rows.Close()
	if rowsErr != nil {
		return nil, rowsErr
	}

	// Uptime hesabını rows kapandıktan SONRA yap: tek DB bağlantısı (SetMaxOpenConns=1)
	// açık bir result-set varken ikinci sorguyu bekletip kilitlenmeye yol açar.
	for i := range items {
		items[i].UptimePct = db.watchlistUptime(items[i].ID)
	}
	return items, nil
}

// watchlistUptime son N kontroldeki up oranını yüzde olarak döndürür
func (db *DB) watchlistUptime(itemID int64) int {
	rows, err := db.conn.Query(`
		SELECT status FROM watchlist_checks
		WHERE watchlist_id = ?
		ORDER BY checked_at DESC, id DESC
		LIMIT ?
	`, itemID, uptimeSampleSize)
	if err != nil {
		return 0
	}
	defer rows.Close()

	total := 0
	up := 0
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			continue
		}
		total++
		if status == "up" {
			up++
		}
	}
	if total == 0 {
		return 0
	}
	return (up * 100) / total
}

// GetLastWatchlistHash bir öğenin son içerik hash'ini döndürür (yoksa boş)
func (db *DB) GetLastWatchlistHash(itemID int64) (string, error) {
	var hash string
	err := db.conn.QueryRow(`
		SELECT COALESCE(content_hash, '') FROM watchlist_checks
		WHERE watchlist_id = ?
		ORDER BY checked_at DESC, id DESC
		LIMIT 1
	`, itemID).Scan(&hash)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return hash, err
}

// GetWatchlistItem tek bir öğeyi ID ile döndürür
func (db *DB) GetWatchlistItem(id int64) (*WatchlistItem, error) {
	var item WatchlistItem
	var enabled int
	err := db.conn.QueryRow(`
		SELECT id, name, url, category, COALESCE(notes, ''), enabled, created_at
		FROM watchlist WHERE id = ?
	`, id).Scan(&item.ID, &item.Name, &item.URL, &item.Category, &item.Notes, &enabled, &item.CreatedAt)
	if err != nil {
		return nil, err
	}
	item.Enabled = enabled == 1
	return &item, nil
}

// SeedDefaultWatchlist doğrulanmış Türk onion'larını ekler (CTI seed/IOC listesi).
//
// İsteğe bağlı: WATCHLIST_SEED_FILE (veya ./data/watchlist-seed.json) varsa, hedefler
// oradan yüklenir; yoksa aşağıdaki gömülü varsayılan liste kullanılır. INSERT OR IGNORE
// + url UNIQUE sayesinde idempotenttir: her açılışta çalışır, mevcut kayıtları atlar,
// yalnızca eksik hedefleri tamamlar.
func (db *DB) SeedDefaultWatchlist() error {
	defaults := loadWatchlistSeed()
	if len(defaults) == 0 {
		defaults = defaultWatchlistSeed()
	}

	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT OR IGNORE INTO watchlist (name, url, category, notes, enabled)
		VALUES (?, ?, ?, ?, 1)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, d := range defaults {
		if _, err := stmt.Exec(d.Name, normalizeWatchlistURL(d.URL), d.Category, d.Notes); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// watchlistSeed varsayılan izleme hedefi
type watchlistSeed struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Category string `json:"category"`
	Notes    string `json:"notes"`
}

// loadWatchlistSeed isteğe bağlı yerel seed dosyasını yükler (WATCHLIST_SEED_FILE
// veya ./data/watchlist-seed.json). Dosya yoksa nil döner; gömülü varsayılan kullanılır.
func loadWatchlistSeed() []watchlistSeed {
	path := os.Getenv("WATCHLIST_SEED_FILE")
	if path == "" {
		path = "data/watchlist-seed.json"
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var seeds []watchlistSeed
	if err := json.Unmarshal(b, &seeds); err != nil {
		return nil
	}
	return seeds
}

// defaultWatchlistSeed gömülü varsayılan Türk onion izleme listesi (CTI seed/IOC).
func defaultWatchlistSeed() []watchlistSeed {
	return []watchlistSeed{
		{"Shadow Forum", "http://w4ljqtyjnxinknz4hszn4bsof7zhfy5z2h4srfss4vvkoikiwz36o3id.onion", "Forum", ""},
		{"MSR Index", "http://msrindexe5vujl5bqbfmycjlmxcr3dluwvksfwqnrlrkrzw447itsnqd.onion", "Dizin", ""},
		{"MSR Forum", "http://msrforumxpzz7tvqmncc2koqhrd56u2hsq2mkg3lqd2a6m7knrlv6hyd.onion", "Forum", ""},
		{"MSR Panel", "http://msrpanelsylp5ue3kymumb65xh7dpyquamvbk75rzwn765jyjhvzxtid.onion", "Sorgu Paneli", "TC/adres/plaka/vesika sorgu — vatandaş PII"},
		{"MSR Market", "http://msrmarketyujpkxrx5ift3qoc3rgvkabbbo7tgpuewn7p4trpdhnviyd.onion", "Market", ""},
		{"MSR Chat", "http://msrchat6ucldc45gzsrldnhrkgc5faowqxcey6c5qxv2udtievr3ytid.onion", "Chat", ""},
		{"Atlantis Market", "http://atlantisrdcrud2ukxls34sihvidtww7la6rqrfvbygmovvadebnj2id.onion", "Market", ""},
		{"Altenen Forum", "http://dydaofm5uefuulnzb63uh6coodgbxlgndk4eosoopbekebttkdxshlyd.onion", "Forum", "RelatedX/Genesis ile aynı nexus"},
		{"Deep Web Türkçe İndex", "http://indexzz7n3cq4slh5bh2lcctmiwk2y7epxjvkpyaemtuat2alprveyid.onion", "Dizin", "Türk onion master dizini"},
		{"Anarcho-Copy Arşivi", "http://anarcopym4ckplfg3uljfj37y27ko3vrnrp43msyu5k5rp3h46wtf7yd.onion", "Arşiv", ""},
		{"Personality (kimlik sorgu)", "http://gsyorszxsxt57kwa5hnmqvgqu22en6rgif37uormbu6nafdxja62p5qd.onion", "Sorgu Paneli", ""},
		{"Dark River Forum", "http://darkrw6bufwiygpzazyo4deboh4xxvhvwaixstshl6jbpere64rpweqd.onion", "Forum", ""},
		{"Liberty Forum", "http://libertyax7z735whsmbirn5lzpiv4bq6vz6bd3ivrgvv3yjw4xos54qd.onion", "Forum", ""},
		{"Azrail (kiralık katil)", "http://azrailgggtfmgboqumc3li3bltcj36xoxrzum7ofi7lqhcau2lsp2gid.onion", "Market", "eski azrail4b42 adresi kapandı"},
		{"Tenebris Forum", "http://bv3g7djr2lxggud4zp6uqikky4wueiep3nnz7xcdy5z6i4exau5z6jyd.onion", "Forum", "Codex-Tenebris, 2023"},
		{"Tenebris Haber", "http://tenebrsi5ougn2slw7i47emfuzuebulhllycuxpihozb7wrpqslfwvqd.onion", "Haber", "Codex-Tenebris haber kolu"},
		{"Pablo Chat TR", "http://lqxqynuoppwk5w5qoybyetwsrtru6dhgn2i77s2vfra6c5cn2u3scgyd.onion", "Chat", "Türkçe sohbet"},
		{"RelatedX (eski adres)", "http://u5qv3yvsykj673jlpyw7udpxablrcu4szuvlaee2eljudwemr2ddv3yd.onion", "Forum", "artık Altenen'e yönlendiriyor"},
	}
}
