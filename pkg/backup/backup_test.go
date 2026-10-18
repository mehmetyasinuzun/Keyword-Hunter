package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"keywordhunter-mvp/pkg/storage"
)

func newDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.New(filepath.Join(t.TempDir(), "kh.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestFileName(t *testing.T) {
	at := time.Date(2026, 10, 17, 13, 4, 5, 0, time.FixedZone("TR", 3*3600))
	if got := FileName("0.17.0", at); got != "keywordhunter-0.17.0-20261017T100405Z.db" {
		t.Fatalf("ad: %s", got)
	}
	if got := FileName("v1.2/../x y", at); got != "keywordhunter-1.2_.._x_y-20261017T100405Z.db" {
		t.Fatalf("güvensiz sürüm temizlenmeli: %s", got)
	}
	if got := FileName("", at); !namePattern.MatchString(got) || got != "keywordhunter-dev-20261017T100405Z.db" {
		t.Fatalf("boş sürüm: %s", got)
	}
}

func TestRun_WritesVerifiedPrivateBackup(t *testing.T) {
	db := newDB(t)
	if _, err := db.SaveResults([]storage.SearchResult{{Title: "A", URL: "http://a.onion/", Source: "Torch", Query: "q"}}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "backups")
	now := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	res, err := Run(context.Background(), FromDB(db), dir, "0.17.0", 7, now)
	if err != nil || res.RotateErr != nil {
		t.Fatalf("yedek: %v %v", err, res.RotateErr)
	}
	if res.Name != "keywordhunter-0.17.0-20261017T100000Z.db" || res.Bytes <= 0 {
		t.Fatalf("sonuç: %+v", res)
	}
	st, err := os.Stat(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("yedek yalnız sahibine açık olmalı: %v", st.Mode().Perm())
	}
	rep, err := storage.VerifyFile(context.Background(), res.Path)
	if err != nil || !rep.OK() || rep.Counts["search_results"] != 1 {
		t.Fatalf("doğrulama: %+v %v", rep, err)
	}
	// Geçici dosya kalmamalı
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("dizinde yalnız yedek olmalı: %v", ents)
	}
	// Aynı ad ikinci kez yazılmaz
	if _, err := Run(context.Background(), FromDB(db), dir, "0.17.0", 7, now); err == nil {
		t.Fatal("var olan yedeğin üzerine yazılmamalı")
	}
}

func TestRotate_KeepsNewestAndIgnoresForeignFiles(t *testing.T) {
	db := newDB(t)
	dir := t.TempDir()
	foreign := []string{"keywordhunter.db", "notes.txt", "keywordhunter-backup.db"}
	for _, f := range foreign {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var names []string
	for i := 0; i < 5; i++ {
		// Sürüm değişse de sıralama addaki zaman damgasına göre
		ver := "0.17.0"
		if i == 3 {
			ver = "0.9.0"
		}
		res, err := Run(context.Background(), FromDB(db), dir, ver, 3, base.Add(time.Duration(i)*24*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, res.Name)
		if i == 3 && (len(res.Removed) != 1 || res.Removed[0] != names[0]) {
			t.Fatalf("4. çalışmada en eski silinmeli: %v", res.Removed)
		}
	}
	list, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[0].Name != names[4] || list[1].Name != names[3] || list[2].Name != names[2] {
		t.Fatalf("kalan yedekler: %+v", list)
	}
	for _, f := range foreign {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("yabancı dosya silinmemeli: %s", f)
		}
	}
	if removed, err := Rotate(dir, 0, ""); err != nil || removed != nil {
		t.Fatalf("keep=0 hiçbir şey silmemeli: %v %v", removed, err)
	}
}

func TestRun_IntegrityFailureLeavesNoFile(t *testing.T) {
	db := newDB(t)
	dir := t.TempDir()
	orig := integrityCheck
	t.Cleanup(func() { integrityCheck = orig })
	integrityCheck = func(ctx context.Context, path string) error {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("denetlenen geçici dosya yok: %v", err)
		}
		return errors.New("integrity_check başarısız: page 3 is never used")
	}
	res, err := Run(context.Background(), FromDB(db), dir, "0.17.0", 7, time.Now())
	if err == nil {
		t.Fatal("bütünlük hatası yedeği başarısız kılmalı")
	}
	if _, serr := os.Stat(res.Path); !os.IsNotExist(serr) {
		t.Fatal("bozuk yedek hedef adla görünmemeli")
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 0 {
		t.Fatalf("geçici dosya temizlenmeli: %v", ents)
	}
}

func TestRun_SourceErrorAndMissingDir(t *testing.T) {
	dir := t.TempDir()
	failing := func(ctx context.Context, dest string) error { return errors.New("disk dolu") }
	if _, err := Run(context.Background(), failing, dir, "x", 1, time.Now()); err == nil {
		t.Fatal("kaynak hatası dönmeli")
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Fatalf("geçici dosya temizlenmeli: %v", ents)
	}
	if _, err := Run(context.Background(), failing, " ", "x", 1, time.Now()); err == nil {
		t.Fatal("boş dizin reddedilmeli")
	}
}

func TestFromFile_BacksUpWithoutMigrating(t *testing.T) {
	src := filepath.Join(t.TempDir(), "kh.db")
	d2, err := storage.New(src)
	if err != nil {
		t.Fatal(err)
	}
	d2.Close()
	dest := filepath.Join(t.TempDir(), "out.db")
	if _, err := WriteFile(context.Background(), FromFile(src), dest); err != nil {
		t.Fatal(err)
	}
	if err := storage.IntegrityCheck(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
}

// Kesintiye uğramış yedeklerin artıkları (eski .tmp ve -journal) süpürülür;
// taze (süren olabilecek) geçici dosyalara ve başka dosyalara dokunulmaz.
func TestSweepStale(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old := now.Add(-2 * StaleTempAge)
	files := map[string]time.Time{
		".keywordhunter-backup-1.tmp":              old,
		".keywordhunter-backup-1.tmp-journal":      old,
		".keywordhunter-backup-2.tmp":              now, // süren yedek
		"keywordhunter-0.17.0-20260101T000000Z.db": old,
		".keywordhunter-backup-3.txt":              old,
		"notes.tmp":                                old,
	}
	for f, at := range files {
		p := filepath.Join(dir, f)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := SweepStale(dir, StaleTempAge, now)
	if err != nil || len(removed) != 2 {
		t.Fatalf("süpürülen: %v %v", removed, err)
	}
	for f := range files {
		_, err := os.Stat(filepath.Join(dir, f))
		gone := os.IsNotExist(err)
		want := f == ".keywordhunter-backup-1.tmp" || f == ".keywordhunter-backup-1.tmp-journal"
		if gone != want {
			t.Errorf("%s silindi=%v (beklenen %v)", f, gone, want)
		}
	}
	if r, err := SweepStale(filepath.Join(dir, "yok"), StaleTempAge, now); err != nil || r != nil {
		t.Fatalf("olmayan dizin hata değil: %v %v", r, err)
	}
	// Run da süpürür
	db := newDB(t)
	p := filepath.Join(dir, ".keywordhunter-backup-9.tmp")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	res, err := Run(context.Background(), FromDB(db), dir, "0.17.0", 7, now)
	if err != nil || len(res.Swept) != 1 {
		t.Fatalf("Run süpürmeli: %+v %v", res.Swept, err)
	}
}

// Saat geri alınmışsa (dizinde ileri tarihli yedekler) yeni yedek döndürmede
// silinmez ve keep'e dahil sayılır; durum bunu raporlar.
func TestRun_ClockSkewNeverDeletesNewBackup(t *testing.T) {
	db := newDB(t)
	dir := t.TempDir()
	future := time.Date(2027, 5, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if _, err := Run(context.Background(), FromDB(db), dir, "0.17.0", 3, future.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	res, err := Run(context.Background(), FromDB(db), dir, "0.17.0", 3, time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(res.Path); err != nil {
		t.Fatalf("yeni yedek silinmemeli: %v", err)
	}
	for _, r := range res.Removed {
		if r == res.Name {
			t.Fatal("silinenler arasında yeni yedek olmamalı")
		}
	}
	list, _ := List(dir)
	if len(list) != 3 || res.NewerExisting != 3 || len(res.Removed) != 1 {
		t.Fatalf("keep=3: %d yedek, daha yeni %d, silinen %v", len(list), res.NewerExisting, res.Removed)
	}
}
