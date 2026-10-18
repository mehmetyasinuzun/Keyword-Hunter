package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/storage"
)

func TestMain(m *testing.M) {
	// Depolama katmanı logger'ı kullanır: paket dizinine logs/ açılmasın.
	dir, err := os.MkdirTemp("", "khseed-test-logs")
	if err != nil {
		panic(err)
	}
	if err := logger.Init(dir, logger.ERROR, false); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = logger.Close()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

var fixedNow = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

func seedOpts(path string) Options {
	return Options{DBPath: path, Seed: DefaultSeed, Now: fixedNow, Admin: AdminAccount{Username: "admin", Password: "Admin-Demo-Pass-1!"}, BcryptCost: bcrypt.MinCost}
}

func mustSeed(t *testing.T, o Options) Summary {
	t.Helper()
	sum, err := Seed(o)
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	return sum
}

func openDB(t *testing.T, path string) *storage.DB {
	t.Helper()
	db, err := storage.New(path)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// scalar tek değerli bir sorgu sonucunu döndürür.
func scalar(t *testing.T, db *storage.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.GetDBConn().QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// fingerprint tohumlanan içeriğin (bcrypt tuzu gibi doğası gereği rastgele
// alanlar hariç) özetini çıkarır: aynı çekirdek → aynı özet.
func fingerprint(t *testing.T, path string) string {
	t.Helper()
	db := openDB(t, path)
	h := sha256.New()
	queries := []string{
		`SELECT id, title, url, source, query, criticality, category, keyword_count, auto_tags, note, case_status, created_at FROM search_results ORDER BY id`,
		`SELECT result_id, type, value, context FROM result_artifacts ORDER BY result_id, type, value`,
		`SELECT query, result_count, searched_at FROM search_history ORDER BY id`,
		`SELECT query, interval_minutes, enabled, alert_threshold, last_result_count, total_runs FROM scheduled_searches ORDER BY id`,
		`SELECT name, url, category, enabled FROM watchlist ORDER BY id`,
		`SELECT watchlist_id, status, http_code, response_ms, content_hash, changed, checked_at FROM watchlist_checks ORDER BY id`,
		`SELECT name, last_status, success_count, fail_count, total_results FROM engine_stats ORDER BY name`,
		`SELECT ts, actor, role, action, target, details, success, hash FROM audit_events ORDER BY id`,
		`SELECT username, role, enabled, created_at FROM users ORDER BY id`,
	}
	for _, q := range queries {
		rows, err := db.GetDBConn().Query(q)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(h, "%v\n", vals)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestSeed_PopulatesEveryArea(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "demo.db")
	sum := mustSeed(t, seedOpts(path))
	db := openDB(t, path)

	if sum.Findings < 100 || sum.Artifacts == 0 || sum.History == 0 || sum.Scheduled != len(scheduledSpecs) ||
		sum.Watchlist != len(watchSpecs) || sum.WatchChecks == 0 || sum.Engines == 0 || sum.AuditEvents == 0 || sum.Users != 3 {
		t.Fatalf("eksik özet: %+v", sum)
	}
	if got := scalar(t, db, `SELECT COUNT(*) FROM search_results`); got != sum.Findings {
		t.Fatalf("bulgu sayısı %d, özet %d", got, sum.Findings)
	}
	// Çeşitlilik: tüm kritiklik seviyeleri, birden çok motor/sorgu/kategori, etiketler.
	if n := scalar(t, db, `SELECT COUNT(DISTINCT criticality) FROM search_results`); n != 5 {
		t.Errorf("kritiklik seviyesi çeşidi %d, 5 bekleniyordu", n)
	}
	if n := scalar(t, db, `SELECT COUNT(DISTINCT source) FROM search_results`); n < 5 {
		t.Errorf("motor çeşidi %d", n)
	}
	if n := scalar(t, db, `SELECT COUNT(DISTINCT query) FROM search_results`); n != len(querySpecs) {
		t.Errorf("sorgu çeşidi %d", n)
	}
	if n := scalar(t, db, `SELECT COUNT(DISTINCT category) FROM search_results`); n < 6 {
		t.Errorf("kategori çeşidi %d", n)
	}
	if n := scalar(t, db, `SELECT COUNT(*) FROM search_results WHERE auto_tags = ''`); n != 0 {
		t.Errorf("%d bulgu etiketsiz", n)
	}
	if n := scalar(t, db, `SELECT COUNT(DISTINCT tag) FROM result_tags`); n < 20 {
		t.Errorf("etiket çeşidi %d", n)
	}
	for _, st := range []string{"new", "investigating", "confirmed", "dismissed"} {
		if n := scalar(t, db, `SELECT COUNT(*) FROM search_results WHERE case_status = ?`, st); n == 0 {
			t.Errorf("%q vaka durumunda bulgu yok", st)
		}
	}
	if n := scalar(t, db, `SELECT COUNT(*) FROM search_results WHERE note <> ''`); n == 0 {
		t.Error("analist notu yok")
	}
	// Pano "son 24 saat" boş kalmasın; hiçbir kayıt gelecekte olmasın.
	if n := scalar(t, db, `SELECT COUNT(*) FROM search_results WHERE created_at >= ?`, sqlTime(fixedNow.Add(-24*time.Hour))); n == 0 {
		t.Error("son 24 saatte bulgu yok")
	}
	if n := scalar(t, db, `SELECT COUNT(*) FROM search_results WHERE created_at > ?`, sqlTime(fixedNow)); n != 0 {
		t.Errorf("%d bulgu gelecekte", n)
	}
	if n := scalar(t, db, `SELECT COUNT(DISTINCT type) FROM result_artifacts`); n < 4 {
		t.Errorf("IOC türü çeşidi %d", n)
	}
	// Kurgusal IOC: e-postalar yalnız .example, IP'ler yalnız belgeleme bloğu.
	if n := scalar(t, db, `SELECT COUNT(*) FROM result_artifacts WHERE type = 'email' AND value NOT LIKE '%.example'`); n != 0 {
		t.Errorf("%d e-posta .example dışında", n)
	}
	if n := scalar(t, db, `SELECT COUNT(*) FROM result_artifacts WHERE type = 'ip' AND value NOT LIKE '203.0.113.%'`); n != 0 {
		t.Errorf("%d IP belgeleme bloğu dışında", n)
	}

	// Planlı taramalar: biri pasif, gelecekte çalışacak (açılışta Tor'a gitmesin).
	all, err := db.GetAllScheduledSearches()
	if err != nil {
		t.Fatal(err)
	}
	disabled := 0
	for _, s := range all {
		if !s.Enabled {
			disabled++
		}
		// Regresyon: next_run_at = last+interval birkaç dakika sonraya düşüp
		// e2e koşusunun ortasında Tor taraması başlatıyordu.
		if s.Enabled && (s.NextRunAt == nil || s.NextRunAt.Before(fixedNow.Add(seedMinLead))) {
			t.Errorf("planlı tarama %q hemen çalışacak (next_run_at %v)", s.Query, s.NextRunAt)
		}
	}
	if disabled != 1 {
		t.Errorf("pasif planlı tarama %d, 1 bekleniyordu", disabled)
	}

	// İzleme listesi: kontrol geçmişi, değişiklik ve kapalı hedef.
	ws, err := db.GetWatchlistWithStatus()
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != len(watchSpecs) {
		t.Fatalf("izleme öğesi %d", len(ws))
	}
	for _, w := range ws {
		if w.LastChecked == nil {
			t.Errorf("%s hiç kontrol edilmemiş", w.Name)
		}
	}
	if n := scalar(t, db, `SELECT COUNT(*) FROM watchlist_checks WHERE changed = 1`); n == 0 {
		t.Error("içerik değişikliği içeren kontrol yok")
	}

	// Motorlar: varsayılan açık olanlar sağlıklı.
	stats, err := db.GetAllEngineStats()
	if err != nil {
		t.Fatal(err)
	}
	up := 0
	for _, e := range stats {
		if e.IsActive && e.LastStatus == "up" && e.SuccessCount > e.FailCount {
			up++
		}
	}
	if up < 5 {
		t.Errorf("sağlıklı aktif motor %d", up)
	}

	// Kullanıcılar: bilinen parolalar ve roller.
	for _, du := range DemoUsers {
		u, err := db.GetUserByUsername(du.Username)
		if err != nil {
			t.Fatalf("%s: %v", du.Username, err)
		}
		if u.Role != du.Role || !u.Enabled {
			t.Errorf("%s: rol %q etkin %v", du.Username, u.Role, u.Enabled)
		}
		if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(du.Password)) != nil {
			t.Errorf("%s: demo parolası doğrulanmıyor", du.Username)
		}
	}
	a, err := db.GetUserByUsername("admin")
	if err != nil || a.Role != storage.RoleAdmin || bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte("Admin-Demo-Pass-1!")) != nil {
		t.Fatalf("admin hesabı hatalı: %+v %v", a, err)
	}

	// Denetim kaydı normal yoldan (hash zinciri) yazılmış ve doğrulanıyor.
	ok, broken, err := db.VerifyAuditChain()
	if err != nil || !ok {
		t.Fatalf("denetim zinciri doğrulanmadı: ok=%v kırık=%d err=%v", ok, broken, err)
	}
	for _, action := range []string{"user.bootstrap", "user.create", "auth.login.success", "auth.login.failure", "search.run", "result.case.update", "scheduled.create", "watchlist.add"} {
		if n := scalar(t, db, `SELECT COUNT(*) FROM audit_events WHERE action = ?`, action); n == 0 {
			t.Errorf("denetim kaydında %s yok", action)
		}
	}
	if n := scalar(t, db, `SELECT COUNT(*) FROM audit_events WHERE ts > ?`, fixedNow.Format(storage.AuditTimeLayout)); n != 0 {
		t.Errorf("%d denetim olayı gelecekte", n)
	}
}

func TestSeed_DeterministicAndResettable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.db"), filepath.Join(dir, "b.db")
	mustSeed(t, seedOpts(a))
	mustSeed(t, seedOpts(b))
	fa, fb := fingerprint(t, a), fingerprint(t, b)
	if fa != fb {
		t.Fatal("aynı çekirdek farklı içerik üretti")
	}

	// -reset olmadan dolu veritabanına yazılmaz.
	if _, err := Seed(seedOpts(a)); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("dolu veritabanı: ErrNotEmpty bekleniyordu, %v", err)
	}
	// -reset baştan kurar ve aynı içeriği verir (idempotent).
	o := seedOpts(a)
	o.Reset = true
	mustSeed(t, o)
	if got := fingerprint(t, a); got != fa {
		t.Fatal("-reset sonrası içerik değişti")
	}

	// Farklı çekirdek farklı içerik.
	c := filepath.Join(dir, "c.db")
	o = seedOpts(c)
	o.Seed = DefaultSeed + 1
	mustSeed(t, o)
	if fingerprint(t, c) == fa {
		t.Fatal("farklı çekirdek aynı içeriği üretti")
	}
}

func TestSeed_Validation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := Seed(Options{DBPath: filepath.Join(dir, "x.db"), Admin: AdminAccount{Username: "admin"}}); err == nil {
		t.Error("parolasız admin kabul edildi")
	}
	o := seedOpts(filepath.Join(dir, "y.db"))
	o.Admin.Username = "Analyst" // demo hesabıyla çakışır (büyük/küçük harf duyarsız)
	if _, err := Seed(o); err == nil || !strings.Contains(err.Error(), "çakışıyor") {
		t.Errorf("çakışan admin adı: %v", err)
	}
	// -reset yalnız düzenli dosyaları siler.
	sub := filepath.Join(dir, "dir.db")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := removeDB(sub); err == nil {
		t.Error("dizin silinmeye çalışıldı")
	}
	if _, err := os.Stat(sub); err != nil {
		t.Errorf("dizin silindi: %v", err)
	}
}

func TestSeed_AdminHashIsUsedVerbatim(t *testing.T) {
	t.Parallel()
	h, err := bcrypt.GenerateFromPassword([]byte("Hash-Only-Pass-1!"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "h.db")
	o := seedOpts(path)
	o.Admin = AdminAccount{Username: "root", PasswordHash: string(h)}
	mustSeed(t, o)
	u, err := openDB(t, path).GetUserByUsername("root")
	if err != nil || u.PasswordHash != string(h) || u.Role != storage.RoleAdmin {
		t.Fatalf("admin hash'i birebir yazılmadı: %+v %v", u, err)
	}
}

func TestRun_Flags(t *testing.T) {
	t.Setenv("ADMIN_USER", "")
	t.Setenv("ADMIN_PASS", "")
	dir := t.TempDir()
	var out, errb bytes.Buffer

	if err := run([]string{"-reset"}, &out, &errb); err == nil {
		t.Error("-db olmadan çalıştı")
	}
	if err := run([]string{"-db", filepath.Join(dir, "a.db"), "fazla"}, &out, &errb); err == nil {
		t.Error("fazladan argüman kabul edildi")
	}

	// Parola verilmezse güçlü bir parola üretilir ve bir kez yazdırılır.
	out.Reset()
	path := filepath.Join(dir, "gen.db")
	if err := run([]string{"-db", path, "-reset"}, &out, &errb); err != nil {
		t.Fatalf("run: %v (%s)", err, errb.String())
	}
	if !strings.Contains(out.String(), "rastgele üretildi") || !strings.Contains(out.String(), "Analyst-Demo-Pass-1!") {
		t.Fatalf("beklenmeyen çıktı:\n%s", out.String())
	}

	// .env yalnız admin kimliğini verir; hedef her zaman -db'dir.
	envPath := filepath.Join(dir, ".env")
	appDB := filepath.Join(dir, "app.db")
	env := "ADMIN_USER=chief\nADMIN_PASS=Env-Admin-Pass-1!\nDB_PATH=" + appDB + "\n"
	if err := os.WriteFile(envPath, []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	demoDB := filepath.Join(dir, "demo.db")
	out.Reset()
	if err := run([]string{"-env", envPath, "-db", demoDB, "-reset"}, &out, &errb); err != nil {
		t.Fatalf("run -env: %v (%s)", err, errb.String())
	}
	if strings.Contains(out.String(), "Env-Admin-Pass-1!") {
		t.Fatal("verilen admin parolası çıktıya yazıldı")
	}
	u, err := openDB(t, demoDB).GetUserByUsername("chief")
	if err != nil || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte("Env-Admin-Pass-1!")) != nil {
		t.Fatalf(".env admin'i yazılmadı: %v", err)
	}
}

// Regresyon: -env verilip -db unutulduğunda khseed .env'deki DB_PATH'e (ya da
// varsayılan keywordhunter.db'ye) düşüp -reset ile uygulamanın gerçek
// veritabanını silerdi. Artık -db zorunlu; -db uygulama veritabanını
// gösteriyorsa da reddedilir ve dosyaya dokunulmaz.
func TestRun_NeverTouchesAppDB(t *testing.T) {
	t.Setenv("ADMIN_USER", "")
	t.Setenv("ADMIN_PASS", "")
	t.Setenv("DB_PATH", "")
	dir := t.TempDir()
	t.Chdir(dir)

	appDB := filepath.Join(dir, "prod.db")
	sentinel := []byte("gerçek uygulama verisi")
	if err := os.WriteFile(appDB, sentinel, 0o600); err != nil {
		t.Fatal(err)
	}
	defaultDB := filepath.Join(dir, "keywordhunter.db") // config varsayılanı
	if err := os.WriteFile(defaultDB, sentinel, 0o600); err != nil {
		t.Fatal(err)
	}
	withPath := filepath.Join(dir, "with.env")
	if err := os.WriteFile(withPath, []byte("ADMIN_USER=admin\nADMIN_PASS=Env-Admin-Pass-1!\nDB_PATH="+appDB+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	noPath := filepath.Join(dir, "nopath.env")
	if err := os.WriteFile(noPath, []byte("ADMIN_USER=admin\nADMIN_PASS=Env-Admin-Pass-1!\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := map[string][]string{
		"-env (DB_PATH) -reset, -db yok":    {"-env", withPath, "-reset"},
		"-env (varsayılan) -reset, -db yok": {"-env", noPath, "-reset"},
		"-db == DB_PATH (mutlak)":           {"-env", withPath, "-db", appDB, "-reset"},
		"-db == DB_PATH (göreli)":           {"-env", withPath, "-db", "./prod.db", "-reset"},
		"-db == varsayılan DB_PATH":         {"-env", noPath, "-db", "keywordhunter.db", "-reset"},
		"-db == DB_PATH, -reset yok":        {"-env", withPath, "-db", appDB},
	}
	for name, args := range cases {
		var out, errb bytes.Buffer
		if err := run(args, &out, &errb); err == nil {
			t.Errorf("%s: reddedilmedi", name)
		}
	}
	for _, p := range []string{appDB, defaultDB} {
		got, err := os.ReadFile(p)
		if err != nil || !bytes.Equal(got, sentinel) {
			t.Fatalf("%s değişti/silindi: %v", p, err)
		}
	}
}
