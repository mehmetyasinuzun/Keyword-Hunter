package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// retentionFixture saklama testleri için sabit "şimdi"ye göre eski/yeni bulgular kurar.
type retentionFixture struct {
	now time.Time
	ids map[string]int64
}

func setupRetention(t *testing.T, db *DB) retentionFixture {
	t.Helper()
	fx := retentionFixture{now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), ids: map[string]int64{}}
	old := fx.now.Add(-40 * 24 * time.Hour)
	recent := fx.now.Add(-2 * 24 * time.Hour)
	add := func(key, url, source string, created time.Time, status string) int64 {
		var ids []int64
		if _, err := db.SaveResultsWithIDs([]SearchResult{{Title: key, URL: url, Source: source, Query: "leak"}}, &ids); err != nil || len(ids) != 1 {
			t.Fatalf("%s: %v %v", key, ids, err)
		}
		mustExec(t, db.conn, `UPDATE search_results SET created_at = ? WHERE id = ?`, created, ids[0])
		if status != "" {
			if _, err := db.UpdateResultCase(ids[0], status, "not"); err != nil {
				t.Fatal(err)
			}
		}
		fx.ids[key] = ids[0]
		return ids[0]
	}
	plain := add("plain", "http://plain.onion/", "Torch", old, "")
	add("inv", "http://inv.onion/", "Torch", old, "investigating")
	add("conf", "http://conf.onion/", "Torch", old, "confirmed")
	add("dis", "http://dis.onion/", "Torch", old, "dismissed")
	add("fresh", "http://shared.onion/", "Torch", recent, "")
	add("sharedOld", "http://shared.onion/", "Ahmia", old, "") // URL taze bir bulguda yaşıyor
	add("watched", "http://watched.onion", "Torch", old, "")

	// plain: etiket, IOC, MISP kaydı, graf kökü + iki seviye derinleştirme, ekran görüntüsü
	if err := db.ApplyTagging(plain, "ransomware, leak", 2, 4, "Fidye"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ReplaceArtifacts(plain, []ResultArtifact{{Type: "btc", Value: "bc1qa"}, {Type: "email", Value: "a@b.c"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordMISPPush([]int64{plain}, "42", fx.now); err != nil {
		t.Fatal(err)
	}
	var root int64
	if err := db.conn.QueryRow(`SELECT id FROM graph_nodes WHERE url = 'http://plain.onion/' AND parent_id IS NULL`).Scan(&root); err != nil {
		t.Fatal(err)
	}
	child, err := db.SaveGraphNode("http://child.onion/", "c", "child.onion", &root, 2, "external", "leak")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveGraphNode("http://grandchild.onion/", "g", "grandchild.onion", &child, 3, "external", "leak"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddWatchlistItem("w", "http://watched.onion/", "Genel", ""); err != nil {
		t.Fatal(err)
	}
	shot := func(url, file string, at time.Time) {
		if _, err := db.SaveScreenshot(Screenshot{TargetURL: url, FilePath: file, TakenAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	shot("http://plain.onion/", "plain.png", recent)     // bulguyla gider (yeni olsa da)
	shot("http://shared.onion/", "shared.png", old)      // URL yaşıyor: bulgu temizliğinde kalır
	shot("http://watched.onion", "watched.png", old)     // izleme kanıtı: bulgu temizliğinde kalır
	shot("http://inv.onion/", "inv.png", old)            // korunan vakanın kanıtı: hep kalır
	shot("http://other.onion/", "other-old.png", old)    // ekran görüntüsü saklamasıyla gider
	shot("http://other.onion/", "other-new.png", recent) // yeni: kalır
	shot("http://other.onion/", "", old)                 // dosyasız hata kaydı: satır gider
	return fx
}

func countWhere(t *testing.T, db *DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.conn.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPurgeResultsBefore_KeepsCasesAndCleansDependents(t *testing.T) {
	db := newTestDB(t)
	fx := setupRetention(t, db)
	before := countWhere(t, db, `SELECT COUNT(*) FROM search_results`)

	st, err := db.PurgeResultsBefore(context.Background(), fx.now.Add(-30*24*time.Hour), nil)
	if err != nil {
		t.Fatal(err)
	}
	// plain, dis, sharedOld, watched silinir; inv + conf (vaka) ve fresh (yeni) kalır
	if st.Results != 4 || st.KeptCases != 2 {
		t.Fatalf("sayımlar: %+v", st)
	}
	if left := countWhere(t, db, `SELECT COUNT(*) FROM search_results`); left != before-4 {
		t.Fatalf("kalan bulgu: %d", left)
	}
	for _, k := range []string{"inv", "conf", "fresh"} {
		if countWhere(t, db, `SELECT COUNT(*) FROM search_results WHERE id = ?`, fx.ids[k]) != 1 {
			t.Errorf("%s tutulmalı", k)
		}
	}
	if st.Tags != 2 || st.Artifacts != 2 || st.MISPPushed != 1 {
		t.Fatalf("bağlı satır sayımları: %+v", st)
	}
	for _, q := range []string{
		`SELECT COUNT(*) FROM result_tags WHERE result_id NOT IN (SELECT id FROM search_results)`,
		`SELECT COUNT(*) FROM result_artifacts WHERE result_id NOT IN (SELECT id FROM search_results)`,
		`SELECT COUNT(*) FROM misp_pushed WHERE result_id NOT IN (SELECT id FROM search_results)`,
		`SELECT COUNT(*) FROM graph_nodes WHERE url IN ('http://plain.onion/', 'http://child.onion/', 'http://grandchild.onion/', 'http://dis.onion/')`,
		`SELECT COUNT(*) FROM graph_nodes c WHERE c.parent_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM graph_nodes p WHERE p.id = c.parent_id)`,
	} {
		if n := countWhere(t, db, q); n != 0 {
			t.Errorf("%s = %d (0 bekleniyordu)", q, n)
		}
	}
	// plain (+2 alt düğüm), dis, watched kökleri; shared kökü yaşıyor (fresh)
	if st.GraphNodes != 5 {
		t.Fatalf("graf düğümü: %d", st.GraphNodes)
	}
	if countWhere(t, db, `SELECT COUNT(*) FROM graph_nodes WHERE url = 'http://shared.onion/'`) != 1 {
		t.Fatal("hâlâ kullanılan URL'nin graf kökü kalmalı")
	}
	if st.Screenshots != 1 || len(st.Files) != 1 || st.Files[0] != "plain.png" {
		t.Fatalf("ekran görüntüsü: %d %v", st.Screenshots, st.Files)
	}
	for _, f := range []string{"shared.png", "watched.png", "inv.png"} {
		if countWhere(t, db, `SELECT COUNT(*) FROM screenshots WHERE file_path = ?`, f) != 1 {
			t.Errorf("%s kalmalı", f)
		}
	}
	// Idempotent: ikinci koşu bir şey silmez
	st2, err := db.PurgeResultsBefore(context.Background(), fx.now.Add(-30*24*time.Hour), nil)
	if err != nil || st2.Results != 0 || st2.Screenshots != 0 || st2.GraphNodes != 0 || st2.KeptCases != 2 {
		t.Fatalf("ikinci koşu: %+v %v", st2, err)
	}
	// Denetim/şema tutarlılığı: yabancı anahtar ihlali yok
	if n := countWhere(t, db, `SELECT COUNT(*) FROM pragma_foreign_key_check`); n != 0 {
		t.Fatalf("yabancı anahtar ihlali: %d", n)
	}
}

func TestPurgeScreenshotsBefore_KeepsCaseEvidence(t *testing.T) {
	db := newTestDB(t)
	fx := setupRetention(t, db)
	st, err := db.PurgeScreenshotsBefore(context.Background(), fx.now.Add(-30*24*time.Hour), nil)
	if err != nil {
		t.Fatal(err)
	}
	// Eski: shared, watched, other-old, dosyasız hata (4); inv korunur
	sort.Strings(st.Files)
	if st.Screenshots != 4 || len(st.Files) != 3 || st.Files[0] != "other-old.png" || st.Files[1] != "shared.png" || st.Files[2] != "watched.png" {
		t.Fatalf("silinen: %d %v", st.Screenshots, st.Files)
	}
	var left []string
	rows, _ := db.conn.Query(`SELECT file_path FROM screenshots ORDER BY file_path`)
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			t.Fatal(err)
		}
		left = append(left, f)
	}
	rows.Close()
	if len(left) != 3 || left[0] != "inv.png" || left[1] != "other-new.png" || left[2] != "plain.png" {
		t.Fatalf("kalan: %v", left)
	}
	// Bulgular ekran görüntüsü saklamasından etkilenmez
	if n := countWhere(t, db, `SELECT COUNT(*) FROM search_results`); n != 7 {
		t.Fatalf("bulgu sayısı değişmemeli: %d", n)
	}
}

func TestPurge_LargeBatches(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	var rs []SearchResult
	for i := 0; i < retentionBatch*2+7; i++ {
		rs = append(rs, SearchResult{Title: "x", URL: "http://bulk" + strconv.Itoa(i) + ".onion/", Source: "Torch", Query: "q"})
	}
	if _, err := db.SaveResults(rs); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db.conn, `UPDATE search_results SET created_at = ?`, now.Add(-100*24*time.Hour))
	st, err := db.PurgeResultsBefore(context.Background(), now.Add(-24*time.Hour), nil)
	if err != nil || st.Results != int64(len(rs)) || st.GraphNodes != int64(len(rs)) {
		t.Fatalf("toplu temizlik: %+v %v", st, err)
	}
}

// Bulgunun yaşı son görülmeden sayılır: aramalar hâlâ döndürdüğü (yeniden
// kaydedilen) bulgu silinmez, bu yüzden bir sonraki aramada "yeni" sayılıp
// uyarı/MISP yeniden tetiklenmez. Uzun süredir görülmeyen bulgu silinir.
func TestPurgeResults_AgesByLastSeen(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	old := now.Add(-40 * 24 * time.Hour)
	save := func(url string) int64 {
		var ids []int64
		if _, err := db.SaveResultsWithIDs([]SearchResult{{Title: "t", URL: url, Source: "Torch", Query: "acme"}}, &ids); err != nil || len(ids) != 1 {
			t.Fatalf("%s: %v %v", url, ids, err)
		}
		mustExec(t, db.conn, `UPDATE search_results SET created_at = ?, feed_at = ? WHERE id = ?`, old, old, ids[0])
		return ids[0]
	}
	live := save("http://live.onion/")
	stale := save("http://stale.onion/")
	lapsed := save("http://lapsed.onion/")
	if err := db.RecordMISPPush([]int64{live}, "42", now); err != nil {
		t.Fatal(err)
	}
	// Zamanlanmış aramanın yaptığı gibi yeniden kaydet: yeni satır yok, last_seen_at yenilenir.
	if n, err := db.SaveResults([]SearchResult{{Title: "t", URL: "http://live.onion/", Source: "Torch", Query: "acme"}}); err != nil || n != 0 {
		t.Fatalf("yeniden görülen bulgu yeni satır olmamalı: %d %v", n, err)
	}
	var seen string
	if err := db.conn.QueryRow(`SELECT COALESCE(last_seen_at, '') FROM search_results WHERE id = ?`, live).Scan(&seen); err != nil || seen == "" {
		t.Fatalf("last_seen_at yenilenmeli: %q %v", seen, err)
	}
	mustExec(t, db.conn, `UPDATE search_results SET last_seen_at = ? WHERE id = ?`, now.Add(-35*24*time.Hour), lapsed)

	st, err := db.PurgeResultsBefore(context.Background(), now.Add(-30*24*time.Hour), nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Results != 2 || st.MISPPushed != 0 {
		t.Fatalf("yalnız görülmeyen bulgular silinmeli: %+v", st)
	}
	for id, want := range map[int64]int{live: 1, stale: 0, lapsed: 0} {
		if n := countWhere(t, db, `SELECT COUNT(*) FROM search_results WHERE id = ?`, id); n != want {
			t.Errorf("bulgu %d: %d (beklenen %d)", id, n, want)
		}
	}
	// Hâlâ bilinen URL: bir sonraki zamanlanmış arama onu yeni saymaz; MISP kaydı duruyor.
	known, err := db.GetKnownURLsForQuery("acme")
	if err != nil || !known["http://live.onion/"] || known["http://stale.onion/"] {
		t.Fatalf("bilinen URL'ler: %v %v", known, err)
	}
	if n := countWhere(t, db, `SELECT COUNT(*) FROM misp_pushed WHERE result_id = ?`, live); n != 1 {
		t.Fatal("canlı bulgunun MISP kaydı kalmalı (yeniden gönderilmesin)")
	}
}

// Silinen bulgu metni veritabanı ve WAL dosyalarında okunur kalmaz
// (secure_delete + WAL kesme).
func TestPurgeResults_LeavesNoRemnantsOnDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kh.db")
	db, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if n := countWhere(t, db, `PRAGMA secure_delete`); n != 2 {
		t.Fatalf("uygulama bağlantısı secure_delete=FAST (2) olmalı: %d", n)
	}
	const marker = "SIZINTI-ISARETI-7f3a"
	old := time.Now().Add(-400 * 24 * time.Hour).UTC()
	keep := []SearchResult{}
	for i := 0; i < 40; i++ {
		keep = append(keep, SearchResult{Title: "kalan", URL: "http://keep" + strconv.Itoa(i) + ".onion/", Source: "Torch", Query: "q"})
	}
	if _, err := db.SaveResults(keep); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		u := "http://" + marker + strconv.Itoa(i) + ".onion/"
		var ids []int64
		if _, err := db.SaveResultsWithIDs([]SearchResult{{Title: marker + " başlık", URL: u, Source: "Torch", Query: "q"}}, &ids); err != nil {
			t.Fatal(err)
		}
		// Analist notu ve yaş (sonradan güncellenen satır: eski hücre kopyası da kalmamalı)
		mustExec(t, db.conn, `UPDATE search_results SET note = ?, created_at = ? WHERE id = ?`, marker+" not "+strings.Repeat("x", i), old, ids[0])
	}
	mustExec(t, db.conn, `PRAGMA wal_checkpoint(TRUNCATE)`)
	st, err := db.PurgeResultsBefore(context.Background(), time.Now().Add(-180*24*time.Hour), nil)
	if err != nil || st.Results != 50 {
		t.Fatalf("temizlik: %+v %v", st, err)
	}
	for _, f := range []string{path, path + "-wal"} {
		b, err := os.ReadFile(f)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if n := strings.Count(string(b), marker); n != 0 {
			t.Errorf("%s içinde silinen bulgu metni %d kez okunuyor", filepath.Base(f), n)
		}
	}
	if n := countWhere(t, db, `SELECT COUNT(*) FROM search_results`); n != 40 {
		t.Fatalf("kalan bulgu: %d", n)
	}
}

// Ekran görüntüsü dosyaları her parti işlendikten hemen sonra bildirilir;
// iptal edilen bağlam kalan partileri atlar (yapılan silmeler kalıcıdır).
func TestPurgeScreenshots_PerBatchFilesAndCancel(t *testing.T) {
	db := newTestDB(t)
	old := time.Now().Add(-100 * 24 * time.Hour)
	for i := 0; i < retentionBatch*2+3; i++ {
		if _, err := db.SaveScreenshot(Screenshot{TargetURL: "http://s.onion/", FilePath: "s" + strconv.Itoa(i) + ".png", TakenAt: old}); err != nil {
			t.Fatal(err)
		}
	}
	var batches []int
	st, err := db.PurgeScreenshotsBefore(context.Background(), time.Now().Add(-24*time.Hour), func(files []string) {
		batches = append(batches, len(files))
	})
	if err != nil || st.Screenshots != int64(retentionBatch*2+3) {
		t.Fatalf("temizlik: %+v %v", st.Screenshots, err)
	}
	if len(batches) != 3 || batches[0] != retentionBatch || batches[1] != retentionBatch || batches[2] != 3 {
		t.Fatalf("parti başına dosya bildirimi: %v", batches)
	}

	for i := 0; i < retentionBatch*2; i++ {
		if _, err := db.SaveScreenshot(Screenshot{TargetURL: "http://s.onion/", FilePath: "t" + strconv.Itoa(i) + ".png", TakenAt: old}); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	st, err = db.PurgeScreenshotsBefore(ctx, time.Now().Add(-24*time.Hour), func(files []string) {
		calls++
		cancel() // kapanış: ilk partiden sonra dur
	})
	if !errors.Is(err, context.Canceled) || calls != 1 || st.Screenshots != retentionBatch {
		t.Fatalf("iptal: %v çağrı=%d silinen=%d", err, calls, st.Screenshots)
	}
	if n := countWhere(t, db, `SELECT COUNT(*) FROM screenshots`); n != retentionBatch {
		t.Fatalf("kalan: %d", n)
	}
}

// Dosya yolu araması indeksli: temizlik parti başına tabloyu taramaz.
func TestScreenshotsFilePathLookupUsesIndex(t *testing.T) {
	db := newTestDB(t)
	rows, err := db.conn.Query(`EXPLAIN QUERY PLAN SELECT EXISTS(SELECT 1 FROM screenshots WHERE file_path = ?)`, "x.png")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	joined := strings.Join(plan, " | ")
	if !strings.Contains(joined, "idx_screenshots_file") || strings.Contains(joined, "SCAN screenshots") {
		t.Fatalf("sorgu planı indeks kullanmalı: %s", joined)
	}
}
