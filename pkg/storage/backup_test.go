package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// seedBackupData yedek testleri için her tablodan örnek satırlar yazar.
func seedBackupData(t *testing.T, db *DB) {
	t.Helper()
	ids := []int64{}
	if _, err := db.SaveResultsWithIDs([]SearchResult{
		{Title: "A", URL: "http://a.onion/", Source: "Torch", Query: "leak"},
		{Title: "B", URL: "http://b.onion/", Source: "Ahmia", Query: "leak"},
	}, &ids); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ReplaceArtifacts(ids[0], []ResultArtifact{{Type: "btc", Value: "bc1qexample"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveScreenshot(Screenshot{TargetURL: "http://a.onion/", FilePath: "x.png"}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSession("live-session-id", "admin", "csrf", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	seedAudit(t, db, 4)
}

func TestBackup_ConsistentCopyWithoutSessions(t *testing.T) {
	db := newTestDB(t)
	seedBackupData(t, db)
	dest := filepath.Join(t.TempDir(), "backup.db")
	if err := db.BackupTo(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	rep, err := VerifyFile(context.Background(), dest)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK() || !rep.IntegrityOK || rep.SchemaVersion != LatestSchemaVersion() {
		t.Fatalf("yedek doğrulanmalı: %+v", rep)
	}
	if rep.AuditChain == nil || !rep.AuditChain.OK || rep.AuditChain.Checked != 4 {
		t.Fatalf("yedekte denetim zinciri geçerli olmalı: %+v", rep.AuditChain)
	}
	if rep.Counts["search_results"] != 2 || rep.Counts["screenshots"] != 1 || rep.Counts["audit_events"] != 4 {
		t.Fatalf("sayımlar: %+v", rep.Counts)
	}

	// Oturumlar hariç tüm tablolar aynı satır sayısında
	raw := openRaw(t, dest)
	defer func() { _ = raw.Close() }()
	src, got := tableCounts(t, db.conn), tableCounts(t, raw)
	for tb, n := range src {
		want := n
		if tb == "sessions" {
			want = 0
		}
		if got[tb] != want {
			t.Errorf("%s: kaynak %d, yedek %d (beklenen %d)", tb, n, got[tb], want)
		}
	}
	if src["sessions"] != 1 {
		t.Fatal("kaynaktaki oturum silinmemeli")
	}
	// Oturum kimliği dosyada (serbest sayfalarda bile) kalmamalı
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if containsBytes(b, []byte("live-session-id")) {
		t.Fatal("oturum kimliği yedek dosyasında kaldı")
	}
	// Geri yükleme: yedek uygulama tarafından açılır
	restored, err := New(dest)
	if err != nil {
		t.Fatalf("yedek açılamadı: %v", err)
	}
	defer func() { _ = restored.Close() }()
	if ok, _, err := restored.VerifyAuditChain(); !ok || err != nil {
		t.Fatalf("geri yüklenen zincir: %v %v", ok, err)
	}
}

func containsBytes(b, sub []byte) bool {
	for i := 0; i+len(sub) <= len(b); i++ {
		if string(b[i:i+len(sub)]) == string(sub) {
			return true
		}
	}
	return false
}

func TestBackup_RefusesNonEmptyDestination(t *testing.T) {
	db := newTestDB(t)
	dest := filepath.Join(t.TempDir(), "exists.db")
	if err := os.WriteFile(dest, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := db.BackupTo(context.Background(), dest); err == nil {
		t.Fatal("dolu dosyanın üzerine yazılmamalı")
	}
	if b, _ := os.ReadFile(dest); string(b) != "keep me" {
		t.Fatal("mevcut dosya değişmemeli")
	}
}

// BackupFile migrasyon çalıştırmaz: eski (sürümsüz) kaynak olduğu gibi kalır.
func TestBackupFile_DoesNotMigrateSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "legacy.db")
	raw := openRaw(t, src)
	loadFixture(t, raw, "legacy_v0.16.sql")
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "copy.db")
	if err := BackupFile(context.Background(), src, dest); err != nil {
		t.Fatal(err)
	}
	raw = openRaw(t, src)
	defer func() { _ = raw.Close() }()
	if v := userVersionOf(t, raw); v != 0 {
		t.Fatalf("kaynak migrasyona uğramamalı: user_version=%d", v)
	}
	rep, err := VerifyFile(context.Background(), dest)
	if err != nil || !rep.IntegrityOK || rep.SchemaVersion != 0 {
		t.Fatalf("eski yedek raporu: %+v %v", rep, err)
	}
	if err := BackupFile(context.Background(), filepath.Join(dir, "missing.db"), filepath.Join(dir, "x.db")); err == nil {
		t.Fatal("olmayan kaynak hata vermeli (boş veritabanı oluşturulmamalı)")
	}
	if _, err := os.Stat(filepath.Join(dir, "missing.db")); !os.IsNotExist(err) {
		t.Fatal("olmayan kaynak oluşturulmamalı")
	}
}

func TestIntegrityCheck_DetectsCorruption(t *testing.T) {
	db := newTestDB(t)
	seedBackupData(t, db)
	for i := 0; i < 200; i++ {
		seedAudit(t, db, 1)
	}
	dest := filepath.Join(t.TempDir(), "backup.db")
	if err := db.BackupTo(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	if err := IntegrityCheck(context.Background(), dest); err != nil {
		t.Fatalf("sağlam yedek: %v", err)
	}
	b, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 4*4096 {
		t.Fatalf("yedek beklenenden küçük: %d", len(b))
	}
	// Başlık (1. sayfa) sağlam; sondan bir önceki sayfa çöp.
	page := len(b)/4096 - 2
	for i := page * 4096; i < (page+1)*4096; i++ {
		b[i] = byte(i * 7) //nolint:gosec // G115: bilinçli taşan bayt: sayfayı çöple doldurur
	}
	if err := os.WriteFile(dest, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := IntegrityCheck(context.Background(), dest); err == nil {
		t.Fatal("bozuk dosya integrity_check'ten geçmemeli")
	}
	if rep, err := VerifyFile(context.Background(), dest); err == nil && rep.OK() {
		t.Fatalf("bozuk dosya raporu OK olmamalı: %+v", rep)
	}
}

func TestVerifyFile_ReportsBrokenChainAndNewerSchema(t *testing.T) {
	db := newTestDB(t)
	seedAudit(t, db, 3)
	dest := filepath.Join(t.TempDir(), "backup.db")
	if err := db.BackupTo(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	raw := openRaw(t, dest)
	mustExec(t, raw, `DROP TRIGGER audit_events_no_update`)
	mustExec(t, raw, `UPDATE audit_events SET actor = 'mallory' WHERE id = 2`)
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	rep, err := VerifyFile(context.Background(), dest)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK() || rep.AuditChain == nil || rep.AuditChain.OK || rep.AuditChain.BrokenAt != 2 {
		t.Fatalf("kurcalanmış zincir raporlanmalı: %+v", rep.AuditChain)
	}

	dest2 := filepath.Join(t.TempDir(), "future.db")
	if err := db.BackupTo(context.Background(), dest2); err != nil {
		t.Fatal(err)
	}
	raw = openRaw(t, dest2)
	mustExec(t, raw, `PRAGMA user_version = 999`)
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	rep, err = VerifyFile(context.Background(), dest2)
	if err != nil || rep.OK() || rep.SchemaVersion != 999 {
		t.Fatalf("yeni şema OK sayılmamalı: %+v %v", rep, err)
	}
	if _, err := VerifyFile(context.Background(), filepath.Join(t.TempDir(), "nope.db")); err == nil {
		t.Fatal("olmayan dosya hata vermeli")
	}
}

func TestMaintenanceRuns_KeepLastSuccess(t *testing.T) {
	db := newTestDB(t)
	if r, err := db.GetMaintenanceRun(MaintenanceBackup); r != nil || err != nil {
		t.Fatalf("boş: %+v %v", r, err)
	}
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := db.RecordMaintenanceRun(MaintenanceRun{Kind: MaintenanceBackup, StartedAt: t0, FinishedAt: t0.Add(time.Second), OK: true, Details: `{"file":"a.db"}`}); err != nil {
		t.Fatal(err)
	}
	t1 := t0.Add(24 * time.Hour)
	if err := db.RecordMaintenanceRun(MaintenanceRun{Kind: MaintenanceBackup, StartedAt: t1, FinishedAt: t1, OK: false, Message: "disk dolu"}); err != nil {
		t.Fatal(err)
	}
	r, err := db.GetMaintenanceRun(MaintenanceBackup)
	if err != nil || r == nil {
		t.Fatal(err)
	}
	if r.OK || r.Message != "disk dolu" || !r.FinishedAt.Equal(t1) || r.LastSuccessAt == nil || !r.LastSuccessAt.Equal(t0.Add(time.Second)) {
		t.Fatalf("son çalışma/son başarı: %+v", r)
	}
}

// Hedef dizin adında '?' olsa da oturum temizliği gerçek yedek dosyasına
// uygulanır (DSN '?'ta bölünüp başka bir dosya açılmaz/oluşturulmaz).
func TestBackup_ScrubsSessionsWhenPathHasQuestionMark(t *testing.T) {
	db := newTestDB(t)
	seedBackupData(t, db)
	base := t.TempDir()
	dir := filepath.Join(base, "bk?x=1")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "backup.db")
	if err := db.BackupTo(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	c, err := openReadOnly(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	var n int
	if err := c.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("yedekte oturum kalmamalı: %d %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(base, "bk")); !os.IsNotExist(err) {
		t.Fatal("kesilmiş yolda boş bir veritabanı oluşturulmamalı")
	}
}

// Oturum temizliği kapalı başarısız olur: dosya yoksa oluşturmaz, sürümlü bir
// yedekte sessions tablosu yoksa hata verir.
func TestScrubBackup_FailsClosed(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "yok.db")
	if err := scrubBackup(context.Background(), missing); err == nil {
		t.Fatal("olmayan dosya hata vermeli")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("olmayan dosya oluşturulmamalı")
	}
	noSessions := filepath.Join(dir, "v2.db")
	raw := openRaw(t, noSessions)
	mustExec(t, raw, `CREATE TABLE search_results (id INTEGER PRIMARY KEY)`)
	mustExec(t, raw, `PRAGMA user_version = 2`)
	mustExec(t, raw, `PRAGMA journal_mode = DELETE`)
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := scrubBackup(context.Background(), noSessions); err == nil {
		t.Fatal("sürümlü yedekte sessions tablosunun yokluğu hata olmalı")
	}
	// Sürümsüz, oturum tablosundan önceki bir veritabanı (ör. v0.4) geçer
	legacy := filepath.Join(dir, "v04.db")
	raw = openRaw(t, legacy)
	loadFixture(t, raw, "legacy_v0.4.sql")
	mustExec(t, raw, `PRAGMA journal_mode = DELETE`)
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := scrubBackup(context.Background(), legacy); err != nil {
		t.Fatalf("oturum tablosu olmayan sürümsüz veritabanı: %v", err)
	}
}

// Komut satırı yedeği kaynağa yazmaz: geri alma günlüklü (ör. daha önce
// alınmış bir yedek) ya da WAL kipindeki kaynağın baytları ve günlük kipi değişmez.
func TestBackupFile_LeavesSourceUntouched(t *testing.T) {
	db := newTestDB(t)
	seedBackupData(t, db)
	dir := t.TempDir()
	archived := filepath.Join(dir, "arsiv.db")
	if err := db.BackupTo(context.Background(), archived); err != nil {
		t.Fatal(err)
	}
	walSrc := filepath.Join(dir, "wal.db")
	w, err := New(walSrc)
	if err != nil {
		t.Fatal(err)
	}
	seedBackupData(t, w)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	for _, src := range []string{archived, walSrc} {
		before, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		st0, _ := os.Stat(src)
		dest := filepath.Join(dir, "kopya-"+filepath.Base(src))
		if err := BackupFile(context.Background(), src, dest); err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		after, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		st1, _ := os.Stat(src)
		if string(before) != string(after) || !st0.ModTime().Equal(st1.ModTime()) {
			t.Fatalf("%s: kaynak değişti (başlık %d/%d → %d/%d)", filepath.Base(src), before[18], before[19], after[18], after[19])
		}
		if rep, err := VerifyFile(context.Background(), dest); err != nil || !rep.OK() || rep.Counts["search_results"] != 2 {
			t.Fatalf("%s kopyası: %+v %v", filepath.Base(src), rep, err)
		}
	}
	if _, err := os.Stat(archived + "-wal"); !os.IsNotExist(err) {
		t.Fatal("geri alma günlüklü kaynak WAL'a çevrilmemeli")
	}
}
