package storage

import (
	"strings"
	"testing"
	"time"

	"keywordhunter-mvp/pkg/metrics"
)

// MetricsSnapshot tohumlanan durumu birebir yansıtır.
func TestMetricsSnapshot_ReflectsState(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()

	snap, err := db.MetricsSnapshot(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.FindingsBySeverity) != 0 || snap.ActiveSessions != 0 || snap.WatchlistEnabled != 0 || snap.Backup != nil {
		t.Fatalf("boş veritabanı: %+v", snap)
	}

	var res []SearchResult
	for i, crit := range []int{1, 3, 3, 5, 5, 5} {
		res = append(res, SearchResult{Title: "t", URL: "http://x" + string(rune('a'+i)) + ".onion/", Source: "Torch", Query: "q", Criticality: crit})
	}
	if _, err := db.SaveResults(res); err != nil {
		t.Fatal(err)
	}
	// Eski sürümlerden kalmış aralık dışı kritiklik uç kovaya sayılır.
	if _, err := db.conn.Exec(`INSERT INTO search_results (title, url, source, query, criticality) VALUES ('o', 'http://old.onion/', 'Torch', 'q', 9)`); err != nil {
		t.Fatal(err)
	}

	if err := db.CreateSession("s-live", "ops", "c1", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSession("s-dead", "ops", "c2", now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	up, _ := db.AddWatchlistItem("up", "http://up.onion/", "Genel", "")
	down, _ := db.AddWatchlistItem("down", "http://down.onion/", "Genel", "")
	recovered, _ := db.AddWatchlistItem("recovered", "http://rec.onion/", "Genel", "")
	off, _ := db.AddWatchlistItem("off", "http://off.onion/", "Genel", "")
	blocked, _ := db.AddWatchlistItem("blocked", "http://blk.onion/", "Genel", "")
	for _, c := range []struct {
		id     int64
		status string
	}{{up, "up"}, {down, "up"}, {down, "down"}, {recovered, "down"}, {recovered, "up"}, {off, "down"}, {blocked, "engel"}} {
		if err := db.RecordWatchlistCheck(c.id, c.status, 0, 10, "", "", false); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SetWatchlistEnabled(off, false); err != nil {
		t.Fatal(err)
	}

	if err := db.UpsertEngineStat("Ahmia", "http://ahmia/", true); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateEngineCheck("Ahmia", "up", 1500, 3); err != nil {
		t.Fatal(err)
	}
	ok := now.Add(-time.Hour)
	if err := db.RecordMaintenanceRun(MaintenanceRun{Kind: MaintenanceBackup, StartedAt: ok, FinishedAt: ok, OK: true}); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordMaintenanceRun(MaintenanceRun{Kind: MaintenanceBackup, StartedAt: now, FinishedAt: now, OK: false, Message: "disk full"}); err != nil {
		t.Fatal(err)
	}

	snap, err = db.MetricsSnapshot(now)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]int64{1: 1, 3: 2, 5: 4}
	for sev := 1; sev <= 5; sev++ {
		if snap.FindingsBySeverity[sev] != want[sev] {
			t.Errorf("severity %d: %d, beklenen %d", sev, snap.FindingsBySeverity[sev], want[sev])
		}
	}
	if snap.ActiveSessions != 1 {
		t.Errorf("aktif oturum: %d", snap.ActiveSessions)
	}
	if snap.WatchlistEnabled != 4 || snap.WatchlistDown != 1 {
		t.Errorf("izleme: etkin=%d down=%d (4/1 beklenir)", snap.WatchlistEnabled, snap.WatchlistDown)
	}
	if len(snap.Engines) != 1 || snap.Engines[0].LastStatus != "up" || snap.Engines[0].LastResponseMs != 1500 {
		t.Errorf("motorlar: %+v", snap.Engines)
	}
	if snap.Backup == nil || snap.Backup.OK || snap.Backup.LastSuccessAt == nil || snap.Backup.LastSuccessAt.Unix() != ok.Unix() {
		t.Errorf("yedek: %+v", snap.Backup)
	}
}

// Kazıma önbelleği yenilenirken bulgu sayımı tablo taraması yapmaz.
func TestMetricsSnapshot_UsesIndexes(t *testing.T) {
	db := newTestDB(t)
	rows, err := db.conn.Query(`EXPLAIN QUERY PLAN SELECT criticality, COUNT(*) FROM search_results GROUP BY criticality`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if p := strings.Join(plan, " | "); !strings.Contains(p, "idx_search_results_criticality") {
		t.Fatalf("sorgu planı indeksi kullanmıyor: %s", p)
	}
}

// Yeni bulgu sayacı yalnız ilk kez eklenen satırları (kritikliğiyle) sayar.
func TestSaveResults_CountsNewFindings(t *testing.T) {
	db := newTestDB(t)
	read := func(sev string) float64 {
		reg, err := metrics.NewRegistry("test")
		if err != nil {
			t.Fatal(err)
		}
		fams, err := reg.Gather()
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range fams {
			if f.GetName() != "keywordhunter_new_findings_total" {
				continue
			}
			for _, m := range f.GetMetric() {
				for _, l := range m.GetLabel() {
					if l.GetName() == "severity" && l.GetValue() == sev {
						return m.GetCounter().GetValue()
					}
				}
			}
		}
		t.Fatalf("severity=%s serisi yok", sev)
		return 0
	}
	b4, b1 := read("4"), read("1")
	batch := []SearchResult{
		{Title: "a", URL: "http://n1.onion/", Source: "Torch", Query: "q", Criticality: 4},
		{Title: "b", URL: "http://n2.onion/", Source: "Torch", Query: "q", Criticality: 4},
		{Title: "c", URL: "http://n3.onion/", Source: "Torch", Query: "q"}, // 0 → 1
	}
	if n, err := db.SaveResults(batch); err != nil || n != 3 {
		t.Fatalf("kayıt: %d %v", n, err)
	}
	if _, err := db.SaveResults(batch); err != nil { // yinelenen: yeni değil
		t.Fatal(err)
	}
	if d4, d1 := read("4")-b4, read("1")-b1; d4 != 2 || d1 != 1 {
		t.Fatalf("yeni bulgu artışı: sev4=%v sev1=%v (2/1 beklenir)", d4, d1)
	}
}
