package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := New(filepath.Join(t.TempDir(), "storage_test.db"))
	if err != nil {
		t.Fatalf("db açılamadı: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// Aynı sonuç tekrar kaydedildiğinde graph_nodes'ta kök düğüm çoğalmamalı
// (UNIQUE(url, parent_id) NULL parent için çalışmıyordu).
func TestSaveResults_DoesNotDuplicateRootGraphNodes(t *testing.T) {
	db := newTestDB(t)
	res := []SearchResult{{Title: "A", URL: "http://aaaa.onion/x", Source: "Torch", Query: "leak"}}
	for i := 0; i < 3; i++ {
		if _, err := db.SaveResults(res); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := db.conn.QueryRow("SELECT COUNT(*) FROM graph_nodes WHERE url = ?", res[0].URL).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("graph_nodes satır sayısı = %d, beklenen 1", n)
	}
}

// Eski veritabanlarındaki mevcut duplikasyon açılışta birleştirilmeli ve
// çocuk düğümler korunan satıra bağlanmalı.
func TestEnsureGraphNodeUniqueness_MergesLegacyDuplicates(t *testing.T) {
	db := newTestDB(t)
	// Indeksi kaldırıp eski davranışı taklit et
	if _, err := db.conn.Exec("DROP INDEX idx_graph_nodes_url_parentkey"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := db.conn.Exec(`INSERT INTO graph_nodes (url, title, domain, depth, link_type, source_query, is_expanded)
			VALUES ('http://dup.onion', 'D', 'dup.onion', 1, 'search', 'q', ?)`, map[bool]int{true: 1, false: 0}[i == 1]); err != nil {
			t.Fatal(err)
		}
	}
	// 3. kopyanın çocuğu var
	var thirdID int64
	db.conn.QueryRow("SELECT MAX(id) FROM graph_nodes WHERE url = 'http://dup.onion'").Scan(&thirdID)
	if _, err := db.conn.Exec(`INSERT INTO graph_nodes (url, title, domain, parent_id, depth, link_type)
		VALUES ('http://child.onion', 'C', 'child.onion', ?, 2, 'external')`, thirdID); err != nil {
		t.Fatal(err)
	}

	if err := db.EnsureGraphNodeUniqueness(); err != nil {
		t.Fatalf("migration hatası: %v", err)
	}

	var n int
	db.conn.QueryRow("SELECT COUNT(*) FROM graph_nodes WHERE url = 'http://dup.onion'").Scan(&n)
	if n != 1 {
		t.Fatalf("birleştirme sonrası kopya sayısı = %d, beklenen 1", n)
	}
	var keeperExpanded int
	var keeperID int64
	db.conn.QueryRow("SELECT id, is_expanded FROM graph_nodes WHERE url = 'http://dup.onion'").Scan(&keeperID, &keeperExpanded)
	if keeperExpanded != 1 {
		t.Fatalf("korunan satır is_expanded=1 olmalı")
	}
	var childParent int64
	db.conn.QueryRow("SELECT parent_id FROM graph_nodes WHERE url = 'http://child.onion'").Scan(&childParent)
	if childParent != keeperID {
		t.Fatalf("çocuk parent_id = %d, beklenen %d", childParent, keeperID)
	}
}

// Yerel saat dilimi UTC olmasa bile süresi gelen planlı taramalar tespit edilmeli.
func TestGetDueScheduledSearches_IsTimezoneSafe(t *testing.T) {
	db := newTestDB(t)
	ss, err := db.CreateScheduledSearch("leak", 5, "", 3)
	if err != nil {
		t.Fatal(err)
	}
	// Henüz vakti gelmedi
	due, _ := db.GetDueScheduledSearches()
	if len(due) != 0 {
		t.Fatalf("yeni oluşturulan tarama hemen due olmamalı, due=%d", len(due))
	}
	// +03:00 gibi bir ofsetle, 10 dk önceye kur (eski kayıt formatını taklit)
	loc := time.FixedZone("TR", 3*3600)
	past := time.Now().In(loc).Add(-10 * time.Minute)
	if _, err := db.conn.Exec("UPDATE scheduled_searches SET next_run_at = ? WHERE id = ?", past, ss.ID); err != nil {
		t.Fatal(err)
	}
	due, _ = db.GetDueScheduledSearches()
	if len(due) != 1 {
		t.Fatalf("ofsetli next_run_at ile due=%d, beklenen 1", len(due))
	}
	// Çalıştıktan sonra tekrar due olmamalı
	if err := db.UpdateScheduledSearchAfterRun(ss.ID, 3, 1); err != nil {
		t.Fatal(err)
	}
	due, _ = db.GetDueScheduledSearches()
	if len(due) != 0 {
		t.Fatalf("çalışma sonrası due=%d, beklenen 0", len(due))
	}
}

func TestSessions_ExpiryCleanup(t *testing.T) {
	db := newTestDB(t)
	if err := db.CreateSession("s1", "admin", "c", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSession("s2", "admin", "c", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	n, err := db.CleanupExpiredSessions(time.Now())
	if err != nil || n != 1 {
		t.Fatalf("temizlenen=%d err=%v, beklenen 1", n, err)
	}
	if _, err := db.GetSession("s2"); err != nil {
		t.Fatalf("geçerli oturum silinmemeli: %v", err)
	}
	if k, _ := db.DeleteOtherSessions("s2"); k != 0 {
		t.Fatalf("başka oturum yokken %d silindi", k)
	}
}

// Eski sürümün yazdığı Go time.String() biçimindeki damgalar açılışta normalize edilmeli.
func TestNormalizeLegacyTimestamps(t *testing.T) {
	db := newTestDB(t)
	legacy := "2026-01-02 15:04:05.123456 +0300 +03 m=+12.5"
	if _, err := db.conn.Exec(`INSERT INTO scheduled_searches (query, interval_minutes, next_run_at) VALUES ('q', 5, ?)`, legacy); err != nil {
		t.Fatal(err)
	}
	var parsed *string
	db.conn.QueryRow("SELECT datetime(next_run_at) FROM scheduled_searches").Scan(&parsed)
	if parsed != nil {
		t.Skip("bu sürücü eski biçimi zaten parse ediyor")
	}
	if err := db.normalizeLegacyTimestamps(); err != nil {
		t.Fatal(err)
	}
	db.conn.QueryRow("SELECT datetime(next_run_at) FROM scheduled_searches").Scan(&parsed)
	if parsed == nil || *parsed != "2026-01-02 12:04:05" {
		got := "<NULL>"
		if parsed != nil {
			got = *parsed
		}
		t.Fatalf("normalize sonrası datetime() = %s, beklenen 2026-01-02 12:04:05", got)
	}
}

// Motor varsayılanı değiştiğinde kullanıcı özelleştirmesi korunmalı, aksi halde yeni varsayılan uygulanmalı.
func TestUpsertEngineStat_RespectsUserOverride(t *testing.T) {
	db := newTestDB(t)
	if err := db.UpsertEngineStat("E1", "http://e1/{query}", true); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertEngineStat("E2", "http://e2/{query}", true); err != nil {
		t.Fatal(err)
	}
	// Kullanıcı E2'yi kapattı
	if err := db.SetEngineActive("E2", false); err != nil {
		t.Fatal(err)
	}
	// Yeni sürüm: E1 artık varsayılan pasif, E2 varsayılan pasif
	_ = db.UpsertEngineStat("E1", "http://e1/{query}", false)
	_ = db.UpsertEngineStat("E2", "http://e2/{query}", false)
	stats, _ := db.GetAllEngineStats()
	got := map[string]bool{}
	for _, st := range stats {
		got[st.Name] = st.IsActive
	}
	if got["E1"] {
		t.Fatal("özelleştirilmemiş E1 yeni varsayılana (pasif) geçmeli")
	}
	if got["E2"] {
		t.Fatal("E2 pasif kalmalı")
	}
	// Kullanıcı E1'i açtı; sonraki upsert (varsayılan pasif) seçimi ezmemeli
	_ = db.SetEngineActive("E1", true)
	_ = db.UpsertEngineStat("E1", "http://e1/{query}", false)
	stats, _ = db.GetAllEngineStats()
	for _, st := range stats {
		if st.Name == "E1" && !st.IsActive {
			t.Fatal("kullanıcının açtığı E1 korunmalı")
		}
	}
	if n, _ := db.PruneEngineStats([]string{"E1"}); n != 1 {
		t.Fatalf("budama %d satır sildi, beklenen 1", n)
	}
}

func TestCaseManagement(t *testing.T) {
	db := newTestDB(t)
	n, err := db.SaveResults([]SearchResult{
		{Title: "A", URL: "http://a.onion", Source: "Tordex", Query: "leak"},
		{Title: "B", URL: "http://b.onion", Source: "Tor66", Query: "leak"},
	})
	if err != nil || n != 2 {
		t.Fatalf("seed: %d %v", n, err)
	}
	var id int64
	db.conn.QueryRow(`SELECT id FROM search_results WHERE url='http://a.onion'`).Scan(&id)

	if _, err := db.UpdateResultCase(id, "bogus", "x"); err == nil {
		t.Fatal("geçersiz durum kabul edildi")
	}
	aff, err := db.UpdateResultCase(id, "confirmed", "kritik sızıntı doğrulandı")
	if err != nil || aff != 1 {
		t.Fatalf("update: %d %v", aff, err)
	}
	r, _ := db.GetResultByID(id)
	if r.CaseStatus != "confirmed" || r.Note != "kritik sızıntı doğrulandı" {
		t.Fatalf("kayıt: %+v", r)
	}
	// filter by case
	res, total, err := db.GetResultsFiltered(ResultFilter{CaseStatus: "confirmed", Limit: 10})
	if err != nil || total != 1 || len(res) != 1 || res[0].ID != id {
		t.Fatalf("confirmed filtresi: total=%d len=%d", total, len(res))
	}
	res, total, _ = db.GetResultsFiltered(ResultFilter{CaseStatus: "any", Limit: 10})
	if total != 1 {
		t.Fatalf("any filtresi: total=%d", total)
	}
	counts, _ := db.CaseCounts()
	if counts["confirmed"] != 1 || counts["none"] != 1 {
		t.Fatalf("counts: %v", counts)
	}
}
