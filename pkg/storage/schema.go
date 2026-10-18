package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"keywordhunter-mvp/pkg/logger"
)

// Şema 1 (taban): v0.16'nın boş bir veritabanında ürettiği şemanın tamamı.
// Bu dosyadaki metinler DEĞİŞTİRİLMEZ; yeni şema değişikliği migrate.go'daki
// listeye yeni bir adım olarak eklenir (bkz. docs/MIGRATIONS.md).
//
// Sütun sırası ve metni, v0.5 ve sonrasının CREATE + ALTER TABLE ADD COLUMN
// ile ulaştığı şemayla birebir aynıdır (SQLite eklenen sütunu son sütunun
// hemen arkasına yazar); böylece benimsenen eski veritabanı ile yeni
// veritabanı sqlite_master üzerinde aynı görünür. Tek istisna v0.3/v0.4'te
// oluşturulmuş veritabanlarıdır: orada auto_tags sonradan eklendiği için
// created_at'in ARKASINDADIR; benimseme bu tabloyu yeniden kurar (bkz.
// canonicalizeSearchResults).

// baselineSearchResults search_results tablosunun taban tanımı (yeniden
// kurmada da aynı metin kullanılır).
const baselineSearchResults = `
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
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP, note TEXT DEFAULT '', case_status TEXT DEFAULT '', feed_at DATETIME,
	UNIQUE(url, source, query)
);`

// baselineSearchResultsColumns taban search_results sütunları, sırasıyla.
var baselineSearchResultsColumns = []string{
	"id", "title", "url", "source", "query", "criticality", "category", "keyword_count",
	"auto_tags", "created_at", "note", "case_status", "feed_at",
}

// baselineTables tablolar (IF NOT EXISTS: eski veritabanlarında mevcut olanlar atlanır).
const baselineTables = baselineSearchResults + `
CREATE TABLE IF NOT EXISTS result_tags (
	result_id INTEGER NOT NULL,
	tag TEXT NOT NULL,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY (result_id, tag),
	FOREIGN KEY (result_id) REFERENCES search_results(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS search_history (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	query TEXT NOT NULL,
	result_count INTEGER DEFAULT 0,
	searched_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
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
CREATE TABLE IF NOT EXISTS alert_config (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	webhook_url TEXT DEFAULT '',
	min_criticality INTEGER DEFAULT 3,
	enabled INTEGER DEFAULT 0,
	updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS sessions (
	id TEXT PRIMARY KEY,
	username TEXT NOT NULL,
	csrf_token TEXT NOT NULL,
	expires_at DATETIME NOT NULL,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
	last_seen_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS watchlist (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL,
	url TEXT NOT NULL UNIQUE,
	category TEXT DEFAULT 'Genel',
	notes TEXT DEFAULT '',
	enabled INTEGER DEFAULT 1,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
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
CREATE TABLE IF NOT EXISTS scheduled_searches (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	query TEXT NOT NULL,
	interval_minutes INTEGER NOT NULL DEFAULT 60,
	enabled INTEGER NOT NULL DEFAULT 1,
	webhook_url TEXT DEFAULT '',
	alert_threshold INTEGER DEFAULT 3,
	last_run_at DATETIME,
	next_run_at DATETIME,
	last_result_count INTEGER DEFAULT 0,
	last_new_count INTEGER DEFAULT 0,
	total_runs INTEGER DEFAULT 0,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS engine_stats (
	name TEXT PRIMARY KEY,
	url TEXT NOT NULL,
	is_active INTEGER DEFAULT 1,
	last_checked_at DATETIME,
	last_status TEXT DEFAULT 'unknown',
	last_response_ms INTEGER DEFAULT 0,
	success_count INTEGER DEFAULT 0,
	fail_count INTEGER DEFAULT 0,
	total_results INTEGER DEFAULT 0,
	consecutive_fails INTEGER DEFAULT 0,
	added_at DATETIME DEFAULT CURRENT_TIMESTAMP, default_active INTEGER DEFAULT 1
);
CREATE TABLE IF NOT EXISTS screenshots (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	target_url TEXT NOT NULL,
	source TEXT NOT NULL DEFAULT 'manual',
	ref_id INTEGER NOT NULL DEFAULT 0,
	file_path TEXT NOT NULL DEFAULT '',
	sha256 TEXT NOT NULL DEFAULT '',
	width INTEGER DEFAULT 0,
	height INTEGER DEFAULT 0,
	bytes INTEGER DEFAULT 0,
	status TEXT NOT NULL DEFAULT 'ok',
	error_msg TEXT DEFAULT '',
	title TEXT DEFAULT '',
	challenge INTEGER DEFAULT 0,
	challenge_kind TEXT DEFAULT '',
	taken_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS result_artifacts (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	result_id INTEGER NOT NULL,
	type TEXT NOT NULL,
	value TEXT NOT NULL,
	context TEXT DEFAULT '',
	confidence REAL DEFAULT 0.5,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(result_id, type, value),
	FOREIGN KEY (result_id) REFERENCES search_results(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS site_profiles (
	host TEXT PRIMARY KEY,
	cookies TEXT DEFAULT '',
	user_agent TEXT DEFAULT '',
	render_js INTEGER DEFAULT 0,
	notes TEXT DEFAULT '',
	updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS crawl_jobs (
	id TEXT PRIMARY KEY,
	seed_url TEXT NOT NULL,
	query TEXT DEFAULT '',
	depth INTEGER DEFAULT 1,
	max_pages INTEGER DEFAULT 50,
	same_domain INTEGER DEFAULT 1,
	status TEXT NOT NULL DEFAULT 'pending',
	pages_done INTEGER DEFAULT 0,
	found_count INTEGER DEFAULT 0,
	saved_count INTEGER DEFAULT 0,
	message TEXT DEFAULT '',
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
	started_at DATETIME,
	finished_at DATETIME
);
CREATE TABLE IF NOT EXISTS users (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	username TEXT NOT NULL UNIQUE COLLATE NOCASE,
	password_hash TEXT NOT NULL,
	role TEXT NOT NULL DEFAULT 'analyst',
	enabled INTEGER NOT NULL DEFAULT 1,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
	last_login_at DATETIME
);
CREATE TABLE IF NOT EXISTS user_2fa (
	user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
	enabled INTEGER NOT NULL DEFAULT 0,
	secret_enc TEXT NOT NULL DEFAULT '',
	pending_enc TEXT NOT NULL DEFAULT '',
	pending_at DATETIME,
	last_step INTEGER NOT NULL DEFAULT 0,
	enabled_at DATETIME,
	updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS user_recovery_codes (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	code_hash TEXT NOT NULL,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
	used_at DATETIME,
	UNIQUE(user_id, code_hash)
);
CREATE TABLE IF NOT EXISTS api_tokens (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL DEFAULT 0,
	username TEXT NOT NULL COLLATE NOCASE,
	name TEXT NOT NULL,
	prefix TEXT NOT NULL,
	token_hash TEXT NOT NULL UNIQUE,
	scope TEXT NOT NULL DEFAULT 'read' CHECK (scope IN ('read', 'write')),
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	last_used_at DATETIME,
	last_used_ip TEXT NOT NULL DEFAULT '',
	expires_at DATETIME,
	revoked_at DATETIME
);
CREATE TABLE IF NOT EXISTS audit_events (
	id INTEGER PRIMARY KEY,
	ts TEXT NOT NULL,
	actor TEXT NOT NULL DEFAULT '',
	role TEXT NOT NULL DEFAULT '',
	auth_method TEXT NOT NULL DEFAULT '',
	ip TEXT NOT NULL DEFAULT '',
	action TEXT NOT NULL,
	target TEXT NOT NULL DEFAULT '',
	details TEXT NOT NULL DEFAULT '',
	success INTEGER NOT NULL DEFAULT 1,
	prev_hash TEXT NOT NULL DEFAULT '',
	hash TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS audit_chain_state (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	anchor_id INTEGER NOT NULL DEFAULT 0,
	anchor_hash TEXT NOT NULL DEFAULT '',
	head_id INTEGER NOT NULL DEFAULT 0,
	head_hash TEXT NOT NULL DEFAULT '',
	pruned_total INTEGER NOT NULL DEFAULT 0,
	pruned_at TEXT NOT NULL DEFAULT '',
	pruning INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS misp_config (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	base_url TEXT NOT NULL DEFAULT '',
	api_key_enc TEXT NOT NULL DEFAULT '',
	verify_tls INTEGER NOT NULL DEFAULT 1,
	distribution INTEGER NOT NULL DEFAULT 0,
	tags TEXT NOT NULL DEFAULT 'tlp:amber,source:keywordhunter',
	allow_private INTEGER NOT NULL DEFAULT 0,
	auto_push INTEGER NOT NULL DEFAULT 0,
	auto_min_criticality INTEGER NOT NULL DEFAULT 4,
	auto_since DATETIME,
	updated_at DATETIME,
	updated_by TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS misp_pushed (
	result_id INTEGER PRIMARY KEY REFERENCES search_results(id) ON DELETE CASCADE,
	event_id TEXT NOT NULL DEFAULT '',
	pushed_at DATETIME NOT NULL
);
`

// legacyColumns eski sürümlerin sonradan ALTER TABLE ADD COLUMN ile eklediği
// sütunlar. Benimseme sırasında eksik olan (ve yalnız eksik olan) eklenir;
// sıra, eski sürümlerin ekleme sırasıdır (sütun sırası korunur).
var legacyColumns = []struct{ table, column, def string }{
	{"search_results", "auto_tags", "TEXT DEFAULT ''"},
	{"search_results", "note", "TEXT DEFAULT ''"},
	{"search_results", "case_status", "TEXT DEFAULT ''"},
	{"search_results", "feed_at", "DATETIME"},
	{"engine_stats", "default_active", "INTEGER DEFAULT 1"},
	{"screenshots", "title", "TEXT DEFAULT ''"},
	{"screenshots", "challenge", "INTEGER DEFAULT 0"},
	{"screenshots", "challenge_kind", "TEXT DEFAULT ''"},
}

// baselineObjects indeksler, tetikleyiciler ve tek satırlık tablo tohumları.
// Sütun eklemeleri ve veri düzeltmelerinden SONRA çalışır (ör. case_status ve
// feed_at indeksleri eski veritabanında sütun eklenmeden kurulamaz; graf
// tekillik indeksi önce yinelenenlerin birleştirilmesini gerektirir).
const baselineObjects = `
CREATE INDEX IF NOT EXISTS idx_search_results_query ON search_results(query);
CREATE INDEX IF NOT EXISTS idx_search_results_source ON search_results(source);
CREATE INDEX IF NOT EXISTS idx_search_results_created ON search_results(created_at);
CREATE INDEX IF NOT EXISTS idx_search_results_case ON search_results(case_status);
CREATE INDEX IF NOT EXISTS idx_search_results_feed_at ON search_results(julianday(feed_at), id);
CREATE INDEX IF NOT EXISTS idx_result_tags_tag ON result_tags(tag);
CREATE INDEX IF NOT EXISTS idx_result_tags_result ON result_tags(result_id);
CREATE INDEX IF NOT EXISTS idx_graph_nodes_parent ON graph_nodes(parent_id);
CREATE INDEX IF NOT EXISTS idx_graph_nodes_domain ON graph_nodes(domain);
CREATE INDEX IF NOT EXISTS idx_graph_nodes_depth ON graph_nodes(depth);
CREATE INDEX IF NOT EXISTS idx_graph_nodes_query ON graph_nodes(source_query);
-- UNIQUE(url, parent_id) NULL parent_id (kök düğüm) için çalışmaz; bu ifade
-- indeksi INSERT OR IGNORE'u kök düğümler için de geçerli kılar.
CREATE UNIQUE INDEX IF NOT EXISTS idx_graph_nodes_url_parentkey ON graph_nodes(url, COALESCE(parent_id, 0));
CREATE INDEX IF NOT EXISTS idx_tagging_jobs_status ON tagging_jobs(status);
CREATE INDEX IF NOT EXISTS idx_tagging_jobs_created ON tagging_jobs(created_at);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);
CREATE INDEX IF NOT EXISTS idx_sessions_username ON sessions(username);
CREATE INDEX IF NOT EXISTS idx_watchlist_enabled ON watchlist(enabled);
CREATE INDEX IF NOT EXISTS idx_watchlist_checks_item ON watchlist_checks(watchlist_id);
CREATE INDEX IF NOT EXISTS idx_watchlist_checks_time ON watchlist_checks(checked_at);
CREATE INDEX IF NOT EXISTS idx_scheduled_due ON scheduled_searches(enabled, next_run_at);
CREATE INDEX IF NOT EXISTS idx_screenshots_target ON screenshots(target_url, taken_at);
CREATE INDEX IF NOT EXISTS idx_screenshots_taken ON screenshots(taken_at);
CREATE INDEX IF NOT EXISTS idx_result_artifacts_result ON result_artifacts(result_id);
CREATE INDEX IF NOT EXISTS idx_result_artifacts_type ON result_artifacts(type);
CREATE INDEX IF NOT EXISTS idx_result_artifacts_value ON result_artifacts(value);
CREATE INDEX IF NOT EXISTS idx_crawl_jobs_status ON crawl_jobs(status);
CREATE INDEX IF NOT EXISTS idx_crawl_jobs_created ON crawl_jobs(created_at);
CREATE INDEX IF NOT EXISTS idx_users_role ON users(role);
CREATE INDEX IF NOT EXISTS idx_recovery_codes_user ON user_recovery_codes(user_id);
CREATE INDEX IF NOT EXISTS idx_api_tokens_username ON api_tokens(username);
CREATE INDEX IF NOT EXISTS idx_audit_events_ts ON audit_events(ts);
CREATE INDEX IF NOT EXISTS idx_audit_events_actor ON audit_events(actor COLLATE NOCASE);
CREATE INDEX IF NOT EXISTS idx_audit_events_action ON audit_events(action);

-- Yeni bulgu: feed_at = created_at (INSERT'ler bu sütunu bilmek zorunda değil).
CREATE TRIGGER IF NOT EXISTS trg_search_results_feed_at AFTER INSERT ON search_results
FOR EACH ROW WHEN NEW.feed_at IS NULL
BEGIN
	UPDATE search_results SET feed_at = COALESCE(NEW.created_at, CURRENT_TIMESTAMP) WHERE id = NEW.id;
END;

-- Denetim kaydı yalnız eklenebilir (hash zinciri; bkz. audit.go).
CREATE TRIGGER IF NOT EXISTS audit_events_no_update BEFORE UPDATE ON audit_events
BEGIN
	SELECT RAISE(ABORT, 'audit_events yalnız eklenebilir');
END;
CREATE TRIGGER IF NOT EXISTS audit_events_no_delete BEFORE DELETE ON audit_events
WHEN COALESCE((SELECT pruning FROM audit_chain_state WHERE id = 1), 0) = 0
BEGIN
	SELECT RAISE(ABORT, 'audit_events yalnız saklama budamasıyla silinebilir');
END;
-- Ekleme yalnız zincirin sonuna: id = baş+1 ve prev_hash = baş hash'i.
CREATE TRIGGER IF NOT EXISTS audit_events_append_only BEFORE INSERT ON audit_events
WHEN NEW.id IS NOT (SELECT head_id + 1 FROM audit_chain_state WHERE id = 1)
  OR NEW.prev_hash IS NOT (SELECT head_hash FROM audit_chain_state WHERE id = 1)
BEGIN
	SELECT RAISE(ABORT, 'audit_events yalnız zincirin sonuna eklenebilir');
END;
-- Zincir durumu silinemez; baş yalnız yeni eklenen satıra bir adım ilerler,
-- çapa yalnız budama sırasında ve yalnız ileri gider, sayaçlar azalmaz.
CREATE TRIGGER IF NOT EXISTS audit_chain_state_no_delete BEFORE DELETE ON audit_chain_state
BEGIN
	SELECT RAISE(ABORT, 'audit_chain_state silinemez');
END;
CREATE TRIGGER IF NOT EXISTS audit_chain_state_guard BEFORE UPDATE ON audit_chain_state
WHEN ((NEW.head_id IS NOT OLD.head_id OR NEW.head_hash IS NOT OLD.head_hash)
      AND (NEW.head_id IS NOT OLD.head_id + 1
           OR NEW.head_hash IS NOT (SELECT hash FROM audit_events WHERE id = NEW.head_id)))
  OR ((NEW.anchor_id IS NOT OLD.anchor_id OR NEW.anchor_hash IS NOT OLD.anchor_hash)
      AND (OLD.pruning = 0 OR NEW.anchor_id < OLD.anchor_id OR NEW.anchor_id > NEW.head_id))
  OR NEW.pruned_total < OLD.pruned_total
BEGIN
	SELECT RAISE(ABORT, 'audit_chain_state yalnız zincirle birlikte ilerleyebilir');
END;

-- Tek satırlık tablolar (id=1 zorunlu)
INSERT OR IGNORE INTO alert_config (id) VALUES (1);
INSERT OR IGNORE INTO audit_chain_state (id) VALUES (1);
`

// migrateBaseline şema 1'i kurar. Boş veritabanında tam şemayı oluşturur;
// sürümsüz (user_version = 0) eski bir veritabanında ise idempotent biçimde
// eksikleri tamamlar: tablo/sütun/indeks/tetikleyici ekler, v0.3/v0.4
// kökenli search_results'u taban sütun sırasına getirir ve eski sürümlerin
// bıraktığı veri tutarsızlıklarını giderir. Graf kök düğümü yinelenenleri
// dışında hiçbir satır silinmez (yinelenenler tek satırda birleştirilir,
// çocukları korunan satıra bağlanır) ve audit_events'e dokunulmaz
// (UPDATE/DELETE tetikleyicileri buna izin vermezdi).
//
// Adım yabancı anahtarlar kapalıyken çalışır (migration.foreignKeysOff):
// search_results'un yeniden kurulması çocuk satırlarını silmemelidir.
func migrateBaseline(q runner) error {
	if _, err := q.Exec(baselineTables); err != nil {
		return fmt.Errorf("tablolar: %w", err)
	}
	for _, c := range legacyColumns {
		has, err := columnExists(q, c.table, c.column)
		if err != nil {
			return err
		}
		if has {
			continue
		}
		if _, err := q.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, c.table, c.column, c.def)); err != nil {
			return fmt.Errorf("%s.%s sütunu eklenemedi: %w", c.table, c.column, err)
		}
	}
	if err := canonicalizeSearchResults(q); err != nil {
		return fmt.Errorf("search_results sütun sırası: %w", err)
	}
	// TAXII akışı: feed_at'i olmayan eski bulgular created_at ile doldurulur.
	if _, err := q.Exec(`UPDATE search_results SET feed_at = COALESCE(created_at, CURRENT_TIMESTAMP) WHERE feed_at IS NULL`); err != nil {
		return fmt.Errorf("feed_at geri doldurma: %w", err)
	}
	// v0.16 öncesi akış indeksi (julianday'siz) yerini idx_search_results_feed_at'e bıraktı.
	if _, err := q.Exec(`DROP INDEX IF EXISTS idx_search_results_feed`); err != nil {
		return err
	}
	if err := mergeDuplicateGraphNodes(q); err != nil {
		return err
	}
	if err := normalizeLegacyTimestamps(q); err != nil {
		return fmt.Errorf("zaman damgası migrasyonu: %w", err)
	}
	if _, err := q.Exec(baselineObjects); err != nil {
		return fmt.Errorf("indeks/tetikleyici: %w", err)
	}
	return nil
}

// canonicalizeSearchResults v0.3/v0.4'te oluşturulmuş bir veritabanında
// search_results'u taban sütun sırasıyla yeniden kurar. O sürümlerde tablo
// auto_tags'siz kurulmuş, sütun v0.5'te ALTER ile created_at'in ARKASINA
// eklenmişti; yeni veritabanında ise created_at'ten öncedir. Sütun kümesi
// taban kümesiyle birebir aynı değilse (elle değiştirilmiş şema) hiçbir şeye
// dokunulmaz: bilinmeyen bir sütunun verisi kopyalamada kaybolabilirdi.
//
// Yöntem: satırlar yan tabloya kopyalanır, tablo düşürülür, taban metniyle
// yeniden oluşturulur, satırlar aynı id'lerle geri yazılır ve AUTOINCREMENT
// sayacı korunur. RENAME kullanılmaz (sqlite_master metnini tırnaklı adla
// yeniden yazar ve yeni veritabanıyla birebir eşitliği bozardı). İndeksler
// ve feed_at tetikleyicisi tabloyla birlikte düşer; ardından baselineObjects
// onları taban metinleriyle yeniden kurar. Yabancı anahtarlar kapalı
// olmalıdır (çağıran adım foreignKeysOff): aksi hâlde DROP TABLE etiket, IOC
// ve MISP kayıtlarını ON DELETE CASCADE ile silerdi.
func canonicalizeSearchResults(q runner) error {
	cols, err := tableColumns(q, "search_results")
	if err != nil {
		return err
	}
	if inBaselineOrder(cols, baselineSearchResultsColumns) {
		return nil // taban sırası (v0.5 ve sonrası, yeni veritabanı; sonradan eklenen sütunlar sonda)
	}
	if !sameColumnSet(cols, baselineSearchResultsColumns) {
		logger.Warn("search_results sütunları beklenmeyen biçimde (%s); yeniden kurulmadı", strings.Join(cols, ","))
		return nil
	}
	var fk int
	if err := q.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil {
		return err
	}
	if fk != 0 {
		return fmt.Errorf("yabancı anahtarlar açıkken search_results yeniden kurulamaz (çocuk satırlar silinirdi)")
	}
	var seq sql.NullInt64
	if err := q.QueryRow(`SELECT seq FROM sqlite_sequence WHERE name = 'search_results'`).Scan(&seq); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var before int64
	if err := q.QueryRow(`SELECT COUNT(*) FROM search_results`).Scan(&before); err != nil {
		return err
	}
	colList := strings.Join(baselineSearchResultsColumns, ", ")
	stmts := []string{
		`DROP TABLE IF EXISTS kh_migrate_search_results`,
		`CREATE TABLE kh_migrate_search_results AS SELECT ` + colList + ` FROM search_results`,
		`DROP TABLE search_results`,
		baselineSearchResults,
		`INSERT INTO search_results (` + colList + `) SELECT ` + colList + ` FROM kh_migrate_search_results ORDER BY id`,
		`DROP TABLE kh_migrate_search_results`,
	}
	for _, st := range stmts {
		if _, err := q.Exec(st); err != nil {
			return err
		}
	}
	var after int64
	if err := q.QueryRow(`SELECT COUNT(*) FROM search_results`).Scan(&after); err != nil {
		return err
	}
	if after != before {
		return fmt.Errorf("yeniden kurmada satır sayısı değişti (%d → %d)", before, after)
	}
	if seq.Valid {
		// Silinmiş en yüksek id'ler yeniden kullanılmasın (dış sistemler — MISP,
		// TAXII — bulguları id ile tanır).
		if _, err := q.Exec(`UPDATE sqlite_sequence SET seq = MAX(seq, ?) WHERE name = 'search_results'`, seq.Int64); err != nil {
			return err
		}
		if _, err := q.Exec(`INSERT INTO sqlite_sequence (name, seq) SELECT 'search_results', ?
			WHERE NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'search_results')`, seq.Int64); err != nil {
			return err
		}
	}
	logger.Info("search_results taban sütun sırasıyla yeniden kuruldu (%d satır; v0.3/v0.4 kökenli veritabanı)", after)
	return nil
}

// tableColumns tablonun sütun adları, tanım sırasıyla.
func tableColumns(q runner, table string) ([]string, error) {
	rows, err := q.Query(`SELECT name FROM pragma_table_info(?) ORDER BY cid`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, strings.ToLower(name))
	}
	return out, rows.Err()
}

// inBaselineOrder cols içindeki taban sütunlarının (base) taban sırasıyla
// ve eksiksiz bulunduğunu söyler; tabanda olmayan sütunlar yok sayılır.
func inBaselineOrder(cols, base []string) bool {
	set := map[string]bool{}
	for _, c := range base {
		set[c] = true
	}
	var known []string
	for _, c := range cols {
		if set[c] {
			known = append(known, c)
		}
	}
	return strings.Join(known, ",") == strings.Join(base, ",")
}

func sameColumnSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := map[string]bool{}
	for _, c := range a {
		set[c] = true
	}
	for _, c := range b {
		if !set[c] {
			return false
		}
	}
	return true
}

// columnExists tabloda sütunun olup olmadığı (PRAGMA table_info).
func columnExists(q runner, table, column string) (bool, error) {
	rows, err := q.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		if strings.EqualFold(name, column) {
			return true, nil
		}
	}
	return false, rows.Err()
}

// mergeDuplicateGraphNodes graph_nodes tablosundaki NULL parent_id kaynaklı
// duplikasyonu giderir (tekillik indeksi kurulmadan önce).
//
// Tablo şemasındaki UNIQUE(url, parent_id) kısıtı SQLite'ta NULL parent_id
// (kök düğümler) için çalışmaz: NULL değerler birbirinden farklı sayıldığı için
// eski sürümlerde her arama aynı URL'yi yeniden ekliyordu. (url,
// COALESCE(parent_id,0)) gruplarındaki fazlalıklar tek satıra indirilir
// (is_expanded=1 olan, sonra çocuğu olan, sonra en küçük id tercih edilir),
// silinen satırların çocukları korunan satıra bağlanır.
func mergeDuplicateGraphNodes(q runner) error {
	type key struct {
		url    string
		parent int64
	}
	for pass := 0; pass < 8; pass++ {
		rows, err := q.Query(`
			SELECT url, COALESCE(parent_id, 0)
			FROM graph_nodes
			GROUP BY url, COALESCE(parent_id, 0)
			HAVING COUNT(*) > 1
		`)
		if err != nil {
			return fmt.Errorf("graph_nodes duplikasyon taraması: %w", err)
		}
		var groups []key
		for rows.Next() {
			var k key
			if err := rows.Scan(&k.url, &k.parent); err != nil {
				rows.Close()
				return err
			}
			groups = append(groups, k)
		}
		rows.Close()
		if len(groups) == 0 {
			return nil
		}
		for _, k := range groups {
			var keeper int64
			err := q.QueryRow(`
				SELECT g.id FROM graph_nodes g
				WHERE g.url = ? AND COALESCE(g.parent_id, 0) = ?
				ORDER BY g.is_expanded DESC,
					(SELECT COUNT(*) FROM graph_nodes c WHERE c.parent_id = g.id) DESC,
					g.id ASC
				LIMIT 1
			`, k.url, k.parent).Scan(&keeper)
			if err != nil {
				return err
			}
			if _, err := q.Exec(`
				UPDATE graph_nodes SET parent_id = ?
				WHERE parent_id IN (
					SELECT id FROM graph_nodes WHERE url = ? AND COALESCE(parent_id, 0) = ? AND id <> ?
				)
			`, keeper, k.url, k.parent, keeper); err != nil {
				return err
			}
			if _, err := q.Exec(`
				DELETE FROM graph_nodes WHERE url = ? AND COALESCE(parent_id, 0) = ? AND id <> ?
			`, k.url, k.parent, keeper); err != nil {
				return err
			}
		}
		logger.Info("graph_nodes: %d duplike grup birleştirildi (geçiş %d)", len(groups), pass+1)
	}
	return nil
}

// normalizeLegacyTimestamps eski sürümlerde Go'nun time.String() biçimiyle
// ("2026-01-02 15:04:05.123 +0300 +03 m=+0.5") yazılmış ve SQLite datetime()
// tarafından parse edilemeyen sütunları standart biçime çevirir. Idempotenttir:
// yalnızca datetime(col) IS NULL olan satırlara dokunur.
func normalizeLegacyTimestamps(q runner) error {
	targets := []struct{ table, id, col string }{
		{"scheduled_searches", "id", "next_run_at"},
		{"scheduled_searches", "id", "last_run_at"},
		{"sessions", "id", "expires_at"},
		{"engine_stats", "name", "last_checked_at"},
		{"screenshots", "id", "taken_at"},
	}
	for _, t := range targets {
		rows, err := q.Query(fmt.Sprintf(`SELECT %s, %s FROM %s WHERE %s IS NOT NULL AND datetime(%s) IS NULL`, t.id, t.col, t.table, t.col, t.col))
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
			if _, err := q.Exec(fmt.Sprintf(`UPDATE %s SET %s = ? WHERE %s = ?`, t.table, t.col, t.id), f.val.UTC(), f.id); err != nil {
				return err
			}
		}
		if len(fixes) > 0 {
			logger.Info("Zaman damgası migrasyonu: %s.%s için %d satır normalize edildi", t.table, t.col, len(fixes))
		}
	}
	return nil
}

// runner *sql.Tx ve migrasyon bağlantısının ortak yüzü.
type runner interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}
