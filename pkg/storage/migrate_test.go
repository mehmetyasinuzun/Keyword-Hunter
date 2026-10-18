package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode"
)

// testDSN storage.New ile aynı PRAGMA'lar; migrasyon çalıştırmadan ham bağlantı.
func testDSN(path string) string {
	return path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_time_format=sqlite"
}

func openRaw(t *testing.T, path string) *sql.DB {
	t.Helper()
	c, err := sql.Open("sqlite", testDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	c.SetMaxOpenConns(1)
	return c
}

// loadFixture eski bir sürümün şemasını (testdata) ham bağlantıya yükler.
func loadFixture(t *testing.T, c *sql.DB, name string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Exec(string(b)); err != nil {
		t.Fatalf("fikstür %s yüklenemedi: %v", name, err)
	}
}

// schemaSnapshot sqlite_master'ın normalize edilmiş hâli: nesne başına
// boşluksuz SQL metni + tablo başına sütun listesi (sıra, tip, NOT NULL,
// varsayılan, PK). Boşluk farkı (girinti, ALTER'ın eklediği ", col") yok sayılır.
func schemaSnapshot(t *testing.T, c *sql.DB) map[string]string {
	t.Helper()
	strip := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, s)
	}
	out := map[string]string{}
	rows, err := c.Query(`SELECT type, name, tbl_name, COALESCE(sql, '') FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var typ, name, tbl, s string
		if err := rows.Scan(&typ, &name, &tbl, &s); err != nil {
			t.Fatal(err)
		}
		out[typ+":"+name] = tbl + "|" + strip(s)
		if typ == "table" {
			tables = append(tables, name)
		}
	}
	rows.Close()
	for _, tb := range tables {
		cr, err := c.Query(`SELECT cid, name, type, "notnull", COALESCE(dflt_value, '<null>'), pk FROM pragma_table_info(?) ORDER BY cid`, tb)
		if err != nil {
			t.Fatal(err)
		}
		var cols []string
		for cr.Next() {
			var cid, nn, pk int
			var name, typ, dflt string
			if err := cr.Scan(&cid, &name, &typ, &nn, &dflt, &pk); err != nil {
				t.Fatal(err)
			}
			cols = append(cols, fmt.Sprintf("%d:%s:%s:%d:%s:%d", cid, name, typ, nn, dflt, pk))
		}
		cr.Close()
		out["columns:"+tb] = strings.Join(cols, ",")
	}
	return out
}

func diffSnapshots(t *testing.T, label string, want, got map[string]string) {
	t.Helper()
	keys := map[string]bool{}
	for k := range want {
		keys[k] = true
	}
	for k := range got {
		keys[k] = true
	}
	var sorted []string
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, k := range sorted {
		if want[k] != got[k] {
			t.Errorf("%s: %s farklı\n  beklenen: %q\n  bulunan:  %q", label, k, want[k], got[k])
		}
	}
}

func userVersionOf(t *testing.T, c *sql.DB) int {
	t.Helper()
	var v int
	if err := c.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// freshSnapshot yeni bir veritabanının (tüm migrasyonlar) şeması.
func freshSnapshot(t *testing.T) map[string]string {
	t.Helper()
	db := newTestDB(t)
	return schemaSnapshot(t, db.conn)
}

// Migrasyon 1 (taban), boş veritabanında v0.16'nın ürettiği şemanın birebir aynısını kurar.
func TestMigrate_BaselineEqualsV016FreshSchema(t *testing.T) {
	dir := t.TempDir()
	legacy := openRaw(t, filepath.Join(dir, "legacy.db"))
	defer func() { _ = legacy.Close() }()
	loadFixture(t, legacy, "legacy_v0.16.sql")

	fresh := openRaw(t, filepath.Join(dir, "fresh.db"))
	defer func() { _ = fresh.Close() }()
	if err := migrateBaseline(fresh); err != nil {
		t.Fatal(err)
	}
	diffSnapshots(t, "taban vs v0.16", schemaSnapshot(t, legacy), schemaSnapshot(t, fresh))
}

func TestMigrate_FreshDBIsAtLatestVersion(t *testing.T) {
	db := newTestDB(t)
	v, err := db.SchemaVersion()
	if err != nil || v != LatestSchemaVersion() || v < 1 {
		t.Fatalf("user_version = %d (%v), beklenen %d", v, err, LatestSchemaVersion())
	}
}

// legacyRow eski şemaya ham SQL ile yazılan örnek veri.
type legacySeed struct {
	fixture string
	seed    func(t *testing.T, c *sql.DB)
	check   func(t *testing.T, db *DB)
}

func TestMigrate_AdoptsLegacyDatabases(t *testing.T) {
	cases := []legacySeed{
		{
			// v0.3/v0.4: auto_tags yok; benimseme onu ekler ve tabloyu taban
			// sütun sırasıyla yeniden kurar (auto_tags created_at'ten önce).
			fixture: "legacy_v0.4.sql",
			seed: func(t *testing.T, c *sql.DB) {
				for i, u := range []string{"http://a.onion/", "http://b.onion/", "http://c.onion/"} {
					mustExec(t, c, `INSERT INTO search_results (title, url, source, query, criticality, keyword_count, created_at)
						VALUES (?, ?, 'Torch', 'leak', ?, 7, '2026-01-25 10:00:00')`, "T"+u, u, i+1)
				}
				// En yüksek id silinmiş: AUTOINCREMENT sayacı (3) korunmalı
				mustExec(t, c, `DELETE FROM search_results WHERE id = 3`)
				mustExec(t, c, `INSERT INTO search_history (query, result_count) VALUES ('leak', 3)`)
				mustExec(t, c, `INSERT INTO graph_nodes (url, title, domain, depth, source_query) VALUES ('http://a.onion/', 'A', 'a.onion', 1, 'leak')`)
			},
			check: func(t *testing.T, db *DB) {
				var title, tags, created string
				var crit, kw int
				if err := db.conn.QueryRow(`SELECT title, criticality, keyword_count, auto_tags, created_at FROM search_results WHERE id = 2`).
					Scan(&title, &crit, &kw, &tags, &created); err != nil {
					t.Fatal(err)
				}
				if title != "Thttp://b.onion/" || crit != 2 || kw != 7 || tags != "" || created != "2026-01-25T10:00:00Z" {
					t.Fatalf("satır değerleri korunmalı: %q %d %d %q %q", title, crit, kw, tags, created)
				}
				var ids []int64
				if _, err := db.SaveResultsWithIDs([]SearchResult{{Title: "D", URL: "http://d.onion/", Source: "Torch", Query: "leak"}}, &ids); err != nil || len(ids) != 1 || ids[0] != 4 {
					t.Fatalf("silinmiş id yeniden kullanılmamalı: %v %v", ids, err)
				}
				var feed sql.NullString
				if err := db.conn.QueryRow(`SELECT feed_at FROM search_results WHERE id = 4`).Scan(&feed); err != nil {
					t.Fatal(err)
				}
				if !feed.Valid {
					t.Fatal("feed_at tetikleyicisi yeniden kurulmalı")
				}
			},
		},
		{
			fixture: "legacy_v0.9.sql",
			seed: func(t *testing.T, c *sql.DB) {
				mustExec(t, c, `INSERT INTO search_results (title, url, source, query, criticality, created_at)
					VALUES ('A', 'http://a.onion/', 'Torch', 'leak', 4, '2026-01-05 10:00:00')`)
				mustExec(t, c, `INSERT INTO result_tags (result_id, tag) VALUES (1, 'ransomware')`)
				// Eski sürümün NULL parent_id yinelenenleri + yinelenene bağlı çocuk
				for i := 0; i < 3; i++ {
					mustExec(t, c, `INSERT INTO graph_nodes (url, title, domain, depth, source_query) VALUES ('http://a.onion/', 'A', 'a.onion', 1, 'leak')`)
				}
				mustExec(t, c, `INSERT INTO graph_nodes (url, title, domain, parent_id, depth) VALUES ('http://b.onion/', 'B', 'b.onion', 3, 2)`)
				mustExec(t, c, `INSERT INTO engine_stats (name, url, is_active) VALUES ('Torch', 'http://torch.onion', 0)`)
				mustExec(t, c, `INSERT INTO screenshots (target_url, file_path) VALUES ('http://a.onion/', 'shot.png')`)
				// Eski Go time.String() biçimli damga
				mustExec(t, c, `INSERT INTO scheduled_searches (query, next_run_at) VALUES ('leak', '2026-01-02 15:04:05.123456 +0300 +03 m=+12.5')`)
			},
			check: func(t *testing.T, db *DB) {
				var n int
				if err := db.conn.QueryRow(`SELECT COUNT(*) FROM graph_nodes WHERE url = 'http://a.onion/'`).Scan(&n); err != nil {
					t.Fatal(err)
				}
				if n != 1 {
					t.Fatalf("graf yinelenenleri birleşmeli: %d", n)
				}
				var orphan int
				if err := db.conn.QueryRow(`SELECT COUNT(*) FROM graph_nodes c WHERE c.parent_id IS NOT NULL
					AND NOT EXISTS (SELECT 1 FROM graph_nodes p WHERE p.id = c.parent_id)`).Scan(&orphan); err != nil {
					t.Fatal(err)
				}
				if orphan != 0 {
					t.Fatalf("yetim çocuk düğüm: %d", orphan)
				}
				var feed, created string
				if err := db.conn.QueryRow(`SELECT feed_at, created_at FROM search_results WHERE id = 1`).Scan(&feed, &created); err != nil || feed != created {
					t.Fatalf("feed_at created_at ile doldurulmalı: %q %q %v", feed, created, err)
				}
				var def int
				if err := db.conn.QueryRow(`SELECT default_active FROM engine_stats WHERE name = 'Torch'`).Scan(&def); err != nil {
					t.Fatal(err)
				}
				if def != 1 {
					t.Fatalf("default_active varsayılanı: %d", def)
				}
				var parsed sql.NullString
				if err := db.conn.QueryRow(`SELECT datetime(next_run_at) FROM scheduled_searches`).Scan(&parsed); err != nil {
					t.Fatal(err)
				}
				if !parsed.Valid || parsed.String != "2026-01-02 12:04:05" {
					t.Fatalf("eski damga normalize edilmeli: %+v", parsed)
				}
				// Yeni sütunlar kullanılabilir
				if n, err := db.UpdateResultCase(1, "confirmed", "not"); err != nil || n != 1 {
					t.Fatalf("vaka sütunları: %d %v", n, err)
				}
				// Yeni tablolar da (kullanıcı/denetim) çalışır
				if _, err := db.AppendAuditEvent(AuditEvent{Actor: "admin", Action: "results.delete", Success: true}); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			fixture: "legacy_v0.16.sql",
			seed: func(t *testing.T, c *sql.DB) {
				mustExec(t, c, `INSERT INTO search_results (title, url, source, query, note, case_status) VALUES ('A', 'http://a.onion/', 'Torch', 'leak', 'n', 'in_progress')`)
				mustExec(t, c, `INSERT INTO result_artifacts (result_id, type, value) VALUES (1, 'btc', 'bc1qxyz')`)
				mustExec(t, c, `INSERT INTO users (username, password_hash, role) VALUES ('admin', '$2a$10$x', 'admin')`)
				mustExec(t, c, `INSERT INTO user_2fa (user_id, enabled, secret_enc) VALUES (1, 1, 'v1:enc')`)
				mustExec(t, c, `INSERT INTO api_tokens (user_id, username, name, prefix, token_hash) VALUES (1, 'admin', 'ci', 'kh_ab', 'h1')`)
				mustExec(t, c, `INSERT INTO misp_config (id, base_url, api_key_enc) VALUES (1, 'https://misp.example', 'v1:key')`)
				mustExec(t, c, `INSERT INTO misp_pushed (result_id, event_id, pushed_at) VALUES (1, '42', '2026-01-01 00:00:00')`)
				// Denetim zinciri eski koddaki gibi (aynı tetikleyicilerle) yazılır.
				legacy := &DB{conn: c}
				for i := 0; i < 5; i++ {
					if _, err := legacy.AppendAuditEvent(AuditEvent{
						Actor: "admin", Role: RoleAdmin, AuthMethod: AuthMethodSession, IP: "10.0.0.1",
						Action: "results.delete", Target: "results", Details: `{"count": 1}`, Success: true,
					}); err != nil {
						t.Fatal(err)
					}
				}
			},
			check: func(t *testing.T, db *DB) {
				var status, note string
				if err := db.conn.QueryRow(`SELECT case_status, note FROM search_results WHERE id = 1`).Scan(&status, &note); err != nil {
					t.Fatal(err)
				}
				if status != "in_progress" || note != "n" {
					t.Fatalf("vaka verisi korunmalı: %q %q", status, note)
				}
				var key string
				if err := db.conn.QueryRow(`SELECT api_key_enc FROM misp_config WHERE id = 1`).Scan(&key); err != nil {
					t.Fatal(err)
				}
				if key != "v1:key" {
					t.Fatalf("MISP anahtarı korunmalı: %q", key)
				}
				st, err := db.AuditChainReport()
				if err != nil || !st.OK || st.HeadID != 5 || st.Checked != 5 {
					t.Fatalf("denetim zinciri benimsemeden sonra geçerli olmalı: %+v %v", st, err)
				}
				// Zincir yeni kodla sürer ve doğrulanır
				if _, err := db.AppendAuditEvent(AuditEvent{Actor: "admin", Action: "results.delete", Success: true}); err != nil {
					t.Fatal(err)
				}
				if ok, broken, err := db.VerifyAuditChain(); !ok || broken != 0 || err != nil {
					t.Fatalf("ekleme sonrası zincir: %v %d %v", ok, broken, err)
				}
				// Yalnız-ekleme tetikleyicileri yerinde
				if _, err := db.conn.Exec(`UPDATE audit_events SET actor = 'x' WHERE id = 1`); err == nil {
					t.Fatal("audit_events UPDATE reddedilmeli")
				}
				if _, err := db.conn.Exec(`DELETE FROM audit_events WHERE id = 1`); err == nil {
					t.Fatal("audit_events DELETE reddedilmeli")
				}
			},
		},
	}
	want := freshSnapshot(t)
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy.db")
			raw := openRaw(t, path)
			loadFixture(t, raw, tc.fixture)
			tc.seed(t, raw)
			before := tableCounts(t, raw)
			if v := userVersionOf(t, raw); v != 0 {
				t.Fatalf("eski veritabanı sürümsüz olmalı: %d", v)
			}
			if err := raw.Close(); err != nil {
				t.Fatal(err)
			}

			db, err := New(path)
			if err != nil {
				t.Fatalf("eski veritabanı benimsenemedi: %v", err)
			}
			defer func() { _ = db.Close() }()
			if v, _ := db.SchemaVersion(); v != LatestSchemaVersion() {
				t.Fatalf("user_version = %d, beklenen %d", v, LatestSchemaVersion())
			}
			diffSnapshots(t, tc.fixture+" benimsenmiş vs yeni", want, schemaSnapshot(t, db.conn))

			// Satır kaybı yok (graf yinelenenlerinin birleşmesi hariç)
			after := tableCounts(t, db.conn)
			for tb, n := range before {
				if tb == "graph_nodes" {
					continue
				}
				if after[tb] != n {
					t.Errorf("%s satır sayısı %d → %d", tb, n, after[tb])
				}
			}
			tc.check(t, db)

			// Yeniden açılış idempotent: şema ve sürüm değişmez
			snap := schemaSnapshot(t, db.conn)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db2, err := New(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db2.Close() }()
			diffSnapshots(t, "yeniden açılış", snap, schemaSnapshot(t, db2.conn))
		})
	}
}

func mustExec(t *testing.T, c *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := c.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func tableCounts(t *testing.T, c *sql.DB) map[string]int {
	t.Helper()
	rows, err := c.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	rows.Close()
	out := map[string]int{}
	for _, n := range names {
		var k int
		if err := c.QueryRow(`SELECT COUNT(*) FROM ` + n).Scan(&k); err != nil {
			t.Fatal(err)
		}
		out[n] = k
	}
	return out
}

// v0.4'te doğup v0.16'ya kadar yükseltilmiş bir veritabanı: search_results'ta
// auto_tags created_at'in arkasında, ama bulguya bağlı çocuk satırlar
// (etiket, IOC, MISP kaydı) var. Benimseme tabloyu yeniden kurarken bunları
// silmemeli (yabancı anahtarlar kapalı) ve yeni veritabanıyla aynı şemaya ulaşmalı.
func TestMigrate_AdoptsV04BornDatabaseKeepingChildren(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw := openRaw(t, path)
	loadFixture(t, raw, "legacy_v0.16.sql")
	mustExec(t, raw, `INSERT INTO search_results (title, url, source, query, auto_tags, note, case_status) VALUES ('A', 'http://a.onion/', 'Torch', 'leak', 'ransomware', 'n', 'confirmed')`)
	mustExec(t, raw, `INSERT INTO search_results (title, url, source, query) VALUES ('B', 'http://b.onion/', 'Torch', 'leak')`)
	mustExec(t, raw, `INSERT INTO result_tags (result_id, tag) VALUES (1, 'ransomware'), (2, 'leak')`)
	mustExec(t, raw, `INSERT INTO result_artifacts (result_id, type, value) VALUES (1, 'btc', 'bc1qxyz')`)
	mustExec(t, raw, `INSERT INTO misp_pushed (result_id, event_id, pushed_at) VALUES (1, '42', '2026-01-01 00:00:00')`)
	// v0.4 sütun sırasına çevir (anahtarlar kapalı; çocuklar yerinde kalır)
	mustExec(t, raw, `PRAGMA foreign_keys = OFF`)
	for _, q := range []string{
		`CREATE TABLE sr_old AS SELECT * FROM search_results`,
		`DROP TABLE search_results`,
		`CREATE TABLE search_results (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			url TEXT NOT NULL,
			source TEXT NOT NULL,
			query TEXT NOT NULL,
			criticality INTEGER DEFAULT 1,
			category TEXT DEFAULT 'Genel',
			keyword_count INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP, auto_tags TEXT DEFAULT '', note TEXT DEFAULT '', case_status TEXT DEFAULT '', feed_at DATETIME,
			UNIQUE(url, source, query)
		)`,
		`INSERT INTO search_results (id, title, url, source, query, criticality, category, keyword_count, created_at, auto_tags, note, case_status, feed_at)
			SELECT id, title, url, source, query, criticality, category, keyword_count, created_at, auto_tags, note, case_status, feed_at FROM sr_old`,
		`DROP TABLE sr_old`,
		`CREATE INDEX idx_search_results_query ON search_results(query)`,
		`CREATE INDEX idx_search_results_case ON search_results(case_status)`,
		`CREATE TRIGGER trg_search_results_feed_at AFTER INSERT ON search_results
		FOR EACH ROW WHEN NEW.feed_at IS NULL
		BEGIN
			UPDATE search_results SET feed_at = COALESCE(NEW.created_at, CURRENT_TIMESTAMP) WHERE id = NEW.id;
		END`,
	} {
		mustExec(t, raw, q)
	}
	before := tableCounts(t, raw)
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := New(path)
	if err != nil {
		t.Fatalf("benimseme: %v", err)
	}
	defer func() { _ = db.Close() }()
	diffSnapshots(t, "v0.4 kökenli benimsenmiş vs yeni", freshSnapshot(t), schemaSnapshot(t, db.conn))
	after := tableCounts(t, db.conn)
	for _, tb := range []string{"search_results", "result_tags", "result_artifacts", "misp_pushed"} {
		if after[tb] != before[tb] {
			t.Errorf("%s satır sayısı %d → %d", tb, before[tb], after[tb])
		}
	}
	var tags, status string
	if err := db.conn.QueryRow(`SELECT auto_tags, case_status FROM search_results WHERE id = 1`).Scan(&tags, &status); err != nil {
		t.Fatal(err)
	}
	if tags != "ransomware" || status != "confirmed" {
		t.Fatalf("değerler doğru sütunlara kopyalanmalı: %q %q", tags, status)
	}
	if n := countFK(t, db.conn); n != 0 {
		t.Fatalf("yabancı anahtar ihlali: %d", n)
	}
	var fk int
	if err := db.conn.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Fatal("benimsemeden sonra yabancı anahtarlar yeniden açık olmalı")
	}
	// Silme hâlâ zincirleme çalışır
	if _, err := db.conn.Exec(`DELETE FROM search_results WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	if n := tableCounts(t, db.conn)["result_tags"]; n != 1 {
		t.Fatalf("ON DELETE CASCADE: %d", n)
	}
}

func countFK(t *testing.T, c *sql.DB) int {
	t.Helper()
	var n int
	if err := c.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// foreignKeysOff adımı başvurulan bir tabloyu SQLite'ın 12 adımlı yöntemiyle
// yeniden kurabilir: çocuk satırlar (etiket, IOC, MISP, 2FA, kurtarma kodu)
// silinmez; adım yeni bir yabancı anahtar ihlali bırakırsa geri alınır.
func TestMigrate_ForeignKeysOffStep(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kh.db")
	db, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	if _, err := db.SaveResultsWithIDs([]SearchResult{{Title: "A", URL: "http://a.onion/", Source: "Torch", Query: "q"}}, &ids); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db.conn, `INSERT INTO result_tags (result_id, tag) VALUES (?, 'x')`, ids[0])
	mustExec(t, db.conn, `INSERT INTO result_artifacts (result_id, type, value) VALUES (?, 'btc', 'bc1q')`, ids[0])
	u, err := db.CreateUser("bob", "$2a$10$x", RoleAnalyst)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db.conn, `INSERT INTO user_2fa (user_id, enabled, secret_enc) VALUES (?, 1, 'v1:s')`, u.ID)
	mustExec(t, db.conn, `INSERT INTO user_recovery_codes (user_id, code_hash) VALUES (?, 'h1'), (?, 'h2')`, u.ID, u.ID)
	before := tableCounts(t, db.conn)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	base := LatestSchemaVersion()

	orig := migrations
	t.Cleanup(func() { migrations = orig })
	rebuild := func(table, ddl, cols string) []string {
		return []string{
			`CREATE TABLE ` + table + `_new ` + ddl,
			`INSERT INTO ` + table + `_new (` + cols + `) SELECT ` + cols + ` FROM ` + table,
			`DROP TABLE ` + table,
			`ALTER TABLE ` + table + `_new RENAME TO ` + table,
		}
	}
	migrations = append(append([]migration{}, orig...), migration{name: "rebuild_parents", foreignKeysOff: true, up: func(q runner) error {
		stmts := rebuild("users", `(id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT NOT NULL UNIQUE COLLATE NOCASE,
			password_hash TEXT NOT NULL, role TEXT NOT NULL DEFAULT 'analyst', enabled INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP, last_login_at DATETIME)`,
			`id, username, password_hash, role, enabled, created_at, last_login_at`)
		stmts = append(stmts, rebuild("search_results", `(id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL, url TEXT NOT NULL,
			source TEXT NOT NULL, query TEXT NOT NULL, criticality INTEGER DEFAULT 1, category TEXT DEFAULT 'Genel',
			keyword_count INTEGER DEFAULT 0, auto_tags TEXT DEFAULT '', created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			note TEXT DEFAULT '', case_status TEXT DEFAULT '', feed_at DATETIME, last_seen_at DATETIME, UNIQUE(url, source, query))`,
			`id, title, url, source, query, criticality, category, keyword_count, auto_tags, created_at, note, case_status, feed_at, last_seen_at`)...)
		for _, st := range stmts {
			if _, err := q.Exec(st); err != nil {
				return err
			}
		}
		return nil
	}})
	db, err = New(path)
	if err != nil {
		t.Fatalf("yeniden kurma adımı: %v", err)
	}
	after := tableCounts(t, db.conn)
	for _, tb := range []string{"search_results", "result_tags", "result_artifacts", "users", "user_2fa", "user_recovery_codes"} {
		if after[tb] != before[tb] {
			t.Errorf("%s satır sayısı %d → %d (çocuk satırlar silinmemeli)", tb, before[tb], after[tb])
		}
	}
	var fk int
	if err := db.conn.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Fatal("adımdan sonra yabancı anahtarlar yeniden açık olmalı")
	}
	if v, _ := db.SchemaVersion(); v != base+1 {
		t.Fatalf("user_version = %d", v)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Yeni ihlal bırakan adım geri alınır
	migrations = append(migrations, migration{name: "orphan", foreignKeysOff: true, up: func(q runner) error {
		_, err := q.Exec(`INSERT INTO result_tags (result_id, tag) VALUES (999999, 'yetim')`)
		return err
	}})
	if _, err := New(path); err == nil || !strings.Contains(err.Error(), "yabancı anahtar ihlali") {
		t.Fatalf("ihlal bırakan adım reddedilmeli: %v", err)
	}
	raw := openRaw(t, path)
	defer func() { _ = raw.Close() }()
	if v := userVersionOf(t, raw); v != base+1 {
		t.Fatalf("user_version değişmemeli: %d", v)
	}
	var orphans int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM result_tags WHERE result_id = 999999`).Scan(&orphans); err != nil {
		t.Fatal(err)
	}
	if orphans != 0 {
		t.Fatal("geri alınan adımın satırı kalmamalı")
	}
}

// Bu ikiliden yeni bir şema sürümü açılışta reddedilir ve veritabanına dokunulmaz.
func TestMigrate_RefusesNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kh.db")
	db, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	future := LatestSchemaVersion() + 1
	raw := openRaw(t, path)
	mustExec(t, raw, fmt.Sprintf(`PRAGMA user_version = %d`, future))
	mustExec(t, raw, `CREATE TABLE from_the_future (x)`)
	before := schemaSnapshot(t, raw)
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := New(path); !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("yeni şema reddedilmeli, hata: %v", err)
	}
	raw = openRaw(t, path)
	defer func() { _ = raw.Close() }()
	if v := userVersionOf(t, raw); v != future {
		t.Fatalf("user_version değişmemeli: %d", v)
	}
	diffSnapshots(t, "reddedilen açılış", before, schemaSnapshot(t, raw))
}

// Başarısız bir adım geri alınır: ne şema değişikliği ne sürüm artışı kalır.
func TestMigrate_FailedStepRollsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kh.db")
	db, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	base := LatestSchemaVersion()

	orig := migrations
	t.Cleanup(func() { migrations = orig })
	migrations = append(append([]migration{}, orig...), migration{name: "boom", up: func(q runner) error {
		if _, err := q.Exec(`CREATE TABLE half_done (x)`); err != nil {
			return err
		}
		return errors.New("boom")
	}})
	if _, err := New(path); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("başarısız migrasyon hata döndürmeli: %v", err)
	}
	raw := openRaw(t, path)
	defer func() { _ = raw.Close() }()
	if v := userVersionOf(t, raw); v != base {
		t.Fatalf("user_version = %d, beklenen %d", v, base)
	}
	var n int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'half_done'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("yarım adımın tablosu geri alınmalı")
	}

	// Adım düzelince uygulanır ve sürüm artar
	migrations[len(migrations)-1].up = func(q runner) error {
		_, err := q.Exec(`CREATE TABLE half_done (x)`)
		return err
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if v, _ := db.SchemaVersion(); v != base+1 {
		t.Fatalf("user_version = %d, beklenen %d", v, base+1)
	}
}

// Aynı dosyayı aynı anda açan iki süreç (burada iki havuz) migrasyonu bir kez uygular.
func TestMigrate_ConcurrentOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kh.db")
	errs := make(chan error, 2)
	dbs := make(chan *DB, 2)
	for i := 0; i < 2; i++ {
		go func() {
			db, err := New(path)
			errs <- err
			dbs <- db
		}()
	}
	deadline := time.After(30 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case err := <-errs:
			if err != nil {
				t.Fatalf("eşzamanlı açılış: %v", err)
			}
			if db := <-dbs; db != nil {
				defer func() { _ = db.Close() }()
			}
		case <-deadline:
			t.Fatal("eşzamanlı açılış zaman aşımı")
		}
	}
}

// Şema 1'deki bir veritabanı sonraki adımlara yükseltilir; satırlar korunur.
func TestMigrate_UpgradesFromVersion1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.db")
	raw := openRaw(t, path)
	if err := migrateBaseline(raw); err != nil {
		t.Fatal(err)
	}
	mustExec(t, raw, `PRAGMA user_version = 1`)
	mustExec(t, raw, `INSERT INTO search_results (title, url, source, query) VALUES ('A', 'http://a.onion/', 'Torch', 'q')`)
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if v, _ := db.SchemaVersion(); v != LatestSchemaVersion() {
		t.Fatalf("user_version = %d", v)
	}
	if err := db.RecordMaintenanceRun(MaintenanceRun{Kind: MaintenanceBackup, StartedAt: time.Now(), FinishedAt: time.Now(), OK: true}); err != nil {
		t.Fatalf("şema 2 tablosu: %v", err)
	}
	if n := tableCounts(t, db.conn)["search_results"]; n != 1 {
		t.Fatalf("satır kaybı: %d", n)
	}
	diffSnapshots(t, "v1 → son", freshSnapshot(t), schemaSnapshot(t, db.conn))
}
