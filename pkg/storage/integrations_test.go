package storage

import (
	"context"
	"testing"
	"time"
)

// feedSeed bulguları verilen zaman ve kritiklikle yazar.
func feedSeed(t *testing.T, db *DB, url string, crit int, at time.Time, arts ...ResultArtifact) int64 {
	t.Helper()
	var ids []int64
	if _, err := db.SaveResultsWithIDs([]SearchResult{{Title: url, URL: url, Source: "S" + at.Format("150405"), Query: "q", Criticality: crit}}, &ids); err != nil || len(ids) != 1 {
		t.Fatalf("kayıt: %v", err)
	}
	if len(arts) > 0 {
		if _, err := db.ReplaceArtifacts(ids[0], arts); err != nil {
			t.Fatal(err)
		}
	}
	// IOC yazımı feed_at'i "şimdi"ye ilerletir; düzenek iki zamanı da sabitler.
	stamp := at.UTC().Format("2006-01-02 15:04:05")
	if _, err := db.conn.Exec(`UPDATE search_results SET created_at = ?, feed_at = ? WHERE id = ?`, stamp, stamp, ids[0]); err != nil {
		t.Fatal(err)
	}
	return ids[0]
}

func TestFeedFindings_FirstOccurrenceAndKeyset(t *testing.T) {
	db := newTestDB(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ioc := ResultArtifact{Type: "email", Value: "x@proton.me", Confidence: .5}
	a := feedSeed(t, db, "http://a.onion/", 2, t0, ioc)
	b := feedSeed(t, db, "http://a.onion/", 5, t0.Add(time.Hour), ioc) // aynı URL + IOC
	c := feedSeed(t, db, "http://c.onion/", 5, t0.Add(2*time.Hour), ioc)

	all, err := db.FeedFindings(context.Background(), FeedFilter{Limit: 10}, true)
	if err != nil || len(all) != 3 || all[0].ID != a || all[1].ID != b || all[2].ID != c {
		t.Fatalf("sıra: %+v %v", all, err)
	}
	if !all[0].FirstURL || all[1].FirstURL || !all[2].FirstURL {
		t.Fatal("URL ilk-geçiş bayrakları")
	}
	if !all[0].Artifacts[0].FirstIndicator || all[1].Artifacts[0].FirstIndicator || all[2].Artifacts[0].FirstIndicator {
		t.Fatal("IOC ilk-geçiş bayrakları")
	}
	if !all[0].Artifacts[0].FirstRelation || all[1].Artifacts[0].FirstRelation || !all[2].Artifacts[0].FirstRelation {
		t.Fatal("ilişki ilk-geçiş bayrakları (yeni URL → yeni ilişki)")
	}
	// Eşik 4: a koleksiyonda yok → b ilk kez yayımlar.
	high, err := db.FeedFindings(context.Background(), FeedFilter{MinCriticality: 4, Limit: 10}, true)
	if err != nil || len(high) != 2 || !high[0].FirstURL || !high[0].Artifacts[0].FirstIndicator {
		t.Fatalf("eşik içinde ilk-geçiş: %+v %v", high, err)
	}
	// İmleç dahil (>=) ve added_after kesin (>)
	page, _ := db.FeedFindings(context.Background(), FeedFilter{AfterKey: all[1].Key, AfterID: all[1].ID, Limit: 10}, false)
	if len(page) != 2 || page[0].ID != b {
		t.Fatalf("imleç: %+v", page)
	}
	later, _ := db.FeedFindings(context.Background(), FeedFilter{AddedAfter: t0.Add(time.Hour), Limit: 10}, false)
	if len(later) != 1 || later[0].ID != c {
		t.Fatalf("added_after: %+v", later)
	}
}

func TestMISPConfig_RoundTripAndAutoCandidates(t *testing.T) {
	db := newTestDB(t)
	cfg, err := db.GetMISPConfig()
	if err != nil || !cfg.VerifyTLS || cfg.Tags != DefaultMISPTags || cfg.AutoMinCriticality != 4 || cfg.BaseURL != "" {
		t.Fatalf("varsayılanlar: %+v %v", cfg, err)
	}
	since := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	cfg.BaseURL, cfg.APIKeyEnc, cfg.Distribution, cfg.AllowPrivate, cfg.AutoPush, cfg.AutoSince = "https://misp.example.org", "v1:xyz", 2, true, true, &since
	cfg.VerifyTLS = false
	if err := db.SaveMISPConfig(cfg, "admin", time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetMISPConfig()
	if err != nil || got.BaseURL != cfg.BaseURL || got.APIKeyEnc != "v1:xyz" || got.VerifyTLS || !got.AllowPrivate || got.Distribution != 2 ||
		!got.AutoPush || got.AutoSince == nil || !got.AutoSince.Equal(since) || got.UpdatedBy != "admin" {
		t.Fatalf("gidiş-dönüş: %+v %v", got, err)
	}

	old := feedSeed(t, db, "http://old.onion/", 5, since.Add(-time.Hour))
	hi := feedSeed(t, db, "http://hi.onion/", 5, since.Add(time.Second))
	_ = feedSeed(t, db, "http://lo.onion/", 2, since.Add(time.Second))
	cands, err := db.MISPAutoPushCandidates(4, since, 10)
	if err != nil || len(cands) != 1 || cands[0].ID != hi {
		t.Fatalf("adaylar: %+v %v (eski=%d)", cands, err, old)
	}
	if err := db.RecordMISPPush([]int64{hi, 999999}, "12", time.Now()); err != nil {
		t.Fatal(err)
	}
	if cands, _ := db.MISPAutoPushCandidates(4, since, 10); len(cands) != 0 {
		t.Fatal("gönderilen bulgu yeniden aday olmamalı")
	}
	res, err := db.GetResultsByIDs([]int64{hi, old, 424242})
	if err != nil || len(res) != 2 {
		t.Fatalf("GetResultsByIDs: %+v %v", res, err)
	}
}

// feedAt bulgunun feed_at değerini (julianday ile karşılaştırılabilir) okur.
func feedAt(t *testing.T, db *DB, id int64) float64 {
	t.Helper()
	var jd float64
	if err := db.conn.QueryRow(`SELECT julianday(feed_at) FROM search_results WHERE id = ?`, id).Scan(&jd); err != nil {
		t.Fatal(err)
	}
	return jd
}

// Sonradan çıkarılan yeni IOC ve yükselen kritiklik feed_at'i ilerletir;
// aynı IOC kümesinin yeniden yazılması ya da düşen kritiklik ilerletmez.
// Böylece added_after ile artımlı çeken TAXII istemcileri bunları kaçırmaz.
func TestFeedAt_AdvancesOnNewIOCsAndCriticalityRaise(t *testing.T) {
	db := newTestDB(t)
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	id := feedSeed(t, db, "http://late.onion/", 2, old)
	base := feedAt(t, db, id)
	var created float64
	if err := db.conn.QueryRow(`SELECT julianday(created_at) FROM search_results WHERE id = ?`, id).Scan(&created); err != nil || created != base {
		t.Fatalf("feed_at başlangıçta created_at olmalı: %v %v %v", created, base, err)
	}
	// Yeni kayıt: tetikleyici feed_at'i created_at'e eşitler.
	var ids []int64
	if _, err := db.SaveResultsWithIDs([]SearchResult{{Title: "n", URL: "http://n.onion/", Source: "S", Query: "q", Criticality: 1}}, &ids); err != nil {
		t.Fatal(err)
	}
	var same bool
	if err := db.conn.QueryRow(`SELECT feed_at = created_at FROM search_results WHERE id = ?`, ids[0]).Scan(&same); err != nil || !same {
		t.Fatalf("ekleme tetikleyicisi: %v %v", same, err)
	}

	ioc := ResultArtifact{Type: "email", Value: "late@x.io", Confidence: .8}
	if _, err := db.ReplaceArtifacts(id, []ResultArtifact{ioc}); err != nil {
		t.Fatal(err)
	}
	bumped := feedAt(t, db, id)
	if bumped <= base {
		t.Fatal("yeni IOC feed_at'i ilerletmeli")
	}
	later, err := db.FeedFindings(context.Background(), FeedFilter{AddedAfter: old.Add(time.Hour), Limit: 10}, true)
	if err != nil || len(later) != 2 || later[1].ID != id || len(later[1].Artifacts) != 1 || !later[1].FeedAt.After(old) {
		t.Fatalf("added_after sonrası yeni IOC'li bulgu (en son) dönmeli: %+v %v", later, err)
	}
	// Aynı küme yeniden yazılırsa (yeniden etiketleme) ilerlemez.
	if _, err := db.conn.Exec(`UPDATE search_results SET feed_at = ? WHERE id = ?`, "2026-02-01 00:00:00", id); err != nil {
		t.Fatal(err)
	}
	pinned := feedAt(t, db, id)
	if _, err := db.ReplaceArtifacts(id, []ResultArtifact{ioc}); err != nil {
		t.Fatal(err)
	}
	if feedAt(t, db, id) != pinned {
		t.Fatal("değişmeyen IOC kümesi feed_at'i ilerletmemeli")
	}
	// Kritiklik düşerse ilerlemez, yükselirse ilerler (otomatik etiketleme ve elle).
	if err := db.ApplyTagging(id, "x", 1, 1, "Genel"); err != nil || feedAt(t, db, id) != pinned {
		t.Fatalf("düşen kritiklik feed_at'i ilerletmemeli: %v", err)
	}
	if err := db.ApplyTagging(id, "x", 1, 4, "Genel"); err != nil || feedAt(t, db, id) <= pinned {
		t.Fatalf("yükselen kritiklik (etiketleme) feed_at'i ilerletmeli: %v", err)
	}
	if _, err := db.conn.Exec(`UPDATE search_results SET feed_at = ?, criticality = 2 WHERE id = ?`, "2026-02-01 00:00:00", id); err != nil {
		t.Fatal(err)
	}
	if ok, err := db.UpdateCriticality(id, 2, "Genel"); err != nil || !ok || feedAt(t, db, id) != pinned {
		t.Fatalf("aynı kritiklik feed_at'i ilerletmemeli: %v %v", ok, err)
	}
	if ok, err := db.UpdateCriticality(id, 5, "Genel"); err != nil || !ok || feedAt(t, db, id) <= pinned {
		t.Fatalf("yükselen kritiklik (elle) feed_at'i ilerletmeli: %v %v", ok, err)
	}
	if ok, err := db.UpdateCriticality(999999, 5, "Genel"); err != nil || ok {
		t.Fatalf("olmayan kayıt: %v %v", ok, err)
	}
	// Şema kurulumu idempotent; boş feed_at created_at ile doldurulur.
	if _, err := db.conn.Exec(`UPDATE search_results SET feed_at = NULL WHERE id = ?`, ids[0]); err != nil {
		t.Fatal(err)
	}
	if err := migrateBaseline(db.conn); err != nil {
		t.Fatal(err)
	}
	if err := db.conn.QueryRow(`SELECT feed_at = created_at FROM search_results WHERE id = ?`, ids[0]).Scan(&same); err != nil || !same {
		t.Fatalf("geri doldurma: %v %v", same, err)
	}
}

// İptal edilen bağlam akış sorgusunu durdurur.
func TestFeedFindings_HonoursCancelledContext(t *testing.T) {
	db := newTestDB(t)
	feedSeed(t, db, "http://a.onion/", 2, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := db.FeedFindings(ctx, FeedFilter{Limit: 10}, true); err == nil {
		t.Fatal("iptal edilmiş bağlamda sorgu hata vermeli")
	}
}
