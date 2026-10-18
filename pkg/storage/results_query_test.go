package storage

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func onionURL(c string, path string) string {
	return "http://" + strings.Repeat(c, 56) + ".onion" + path
}

// seedFindings sabit bir bulgu kümesi yazar ve URL → ID döndürür.
func seedFindings(t *testing.T, db *DB) map[string]int64 {
	t.Helper()
	rows := []SearchResult{
		{Title: "Bank leak 50% off", URL: onionURL("a", "/1"), Source: "Tordex", Query: "bank_leak", Criticality: 5, Category: "Veri Sızıntısı", KeywordCount: 9},
		{Title: "Bank leak 500 records", URL: onionURL("b", "/2"), Source: "Tor66", Query: "bank leak", Criticality: 3, Category: "Veri Sızıntısı", KeywordCount: 2},
		{Title: "Carding shop", URL: onionURL("c", "/3"), Source: "Tordex", Query: "carding", Criticality: 4, Category: "Kart Dolandırıcılığı", KeywordCount: 5},
		{Title: "Random blog", URL: onionURL("d", "/4"), Source: "Amnesia", Query: "carding", KeywordCount: 0},
	}
	var ids []int64
	n, err := db.SaveResultsWithIDs(rows, &ids)
	if err != nil || n != len(rows) || len(ids) != len(rows) {
		t.Fatalf("seed: n=%d ids=%v err=%v", n, ids, err)
	}
	out := map[string]int64{}
	for i, r := range rows {
		out[r.URL] = ids[i]
	}
	return out
}

func TestSaveResults_DefaultsAndDedup(t *testing.T) {
	db := newTestDB(t)
	ids := seedFindings(t, db)

	r, err := db.GetResultByID(ids[onionURL("d", "/4")])
	if err != nil {
		t.Fatal(err)
	}
	if r.Criticality != 1 || r.Category != "Genel" {
		t.Fatalf("varsayılan kritiklik/kategori: %+v", r)
	}
	// Aynı (url, source, query) tekrar kaydedilmez; farklı kaynak ayrı satırdır.
	var again []int64
	n, err := db.SaveResultsWithIDs([]SearchResult{
		{Title: "x", URL: onionURL("a", "/1"), Source: "Tordex", Query: "bank_leak"},
		{Title: "x", URL: onionURL("a", "/1"), Source: "Tor66", Query: "bank_leak"},
	}, &again)
	if err != nil || n != 1 || len(again) != 1 {
		t.Fatalf("tekrar kaydı: n=%d ids=%v err=%v", n, again, err)
	}
	if err := db.SaveResult("tek", onionURL("e", "/5"), "Torch", "manual"); err != nil {
		t.Fatal(err)
	}
	total, searches, err := db.GetStats()
	if err != nil || total != 6 || searches != 0 {
		t.Fatalf("istatistik: total=%d searches=%d err=%v", total, searches, err)
	}
	set, err := db.ExistingResultIDSet([]int64{ids[onionURL("a", "/1")], -3, 0, 99999, ids[onionURL("a", "/1")]})
	if err != nil || len(set) != 1 || !set[ids[onionURL("a", "/1")]] {
		t.Fatalf("ExistingResultIDSet: %v %v", set, err)
	}
	if set, _ := db.ExistingResultIDSet(nil); len(set) != 0 {
		t.Fatal("boş girdi boş küme")
	}
}

// TestGetResultsFiltered_FiltersSortAndPaging bulgu listesinin filtre, LIKE
// kaçışı, sıralama ve sayfalama davranışı. total, sayfadan bağımsız eşleşme sayısıdır.
func TestGetResultsFiltered_FiltersSortAndPaging(t *testing.T) {
	db := newTestDB(t)
	ids := seedFindings(t, db)
	if err := db.ApplyTagging(ids[onionURL("c", "/3")], "CVV, carding ,cvv", 5, 4, "Kart Dolandırıcılığı"); err != nil {
		t.Fatal(err)
	}

	urlsOf := func(rs []SearchResult) string {
		var out []string
		for _, r := range rs {
			out = append(out, r.URL[len(r.URL)-2:])
		}
		return strings.Join(out, ",")
	}
	cases := []struct {
		name  string
		f     ResultFilter
		total int
		urls  string
	}{
		{"hepsi (en yeni önce)", ResultFilter{}, 4, "/4,/3,/2,/1"},
		{"en eski", ResultFilter{Sort: "oldest"}, 4, "/1,/2,/3,/4"},
		{"kritiklik", ResultFilter{Sort: "criticality"}, 4, "/1,/3,/2,/4"},
		{"hit", ResultFilter{Sort: "hits"}, 4, "/1,/3,/2,/4"},
		{"kaynak", ResultFilter{Source: "Tordex"}, 2, "/3,/1"},
		{"kategori", ResultFilter{Category: "Veri Sızıntısı"}, 2, "/2,/1"},
		{"min kritiklik 4", ResultFilter{MinCriticality: 4}, 2, "/3,/1"},
		{"min kritiklik 1 filtre değil", ResultFilter{MinCriticality: 1}, 4, "/4,/3,/2,/1"},
		{"etiket (büyük harf)", ResultFilter{Tag: " CVV "}, 1, "/3"},
		// LIKE joker karakterleri literal aranır
		{"metin % literal", ResultFilter{Text: "50%"}, 1, "/1"},
		{"sorgu _ literal", ResultFilter{Query: "bank_leak"}, 1, "/1"},
		{"sorgu kısmi", ResultFilter{Query: "bank"}, 2, "/2,/1"},
		{"metin url içinde", ResultFilter{Text: strings.Repeat("c", 10)}, 1, "/3"},
		{"sayfa 2", ResultFilter{Sort: "oldest", Limit: 2, Offset: 2}, 4, "/3,/4"},
		{"negatif offset", ResultFilter{Sort: "oldest", Limit: 1, Offset: -5}, 4, "/1"},
		{"limit sınır dışı → 50", ResultFilter{Limit: 100000}, 4, "/4,/3,/2,/1"},
		{"bileşik", ResultFilter{Source: "Tordex", MinCriticality: 5}, 1, "/1"},
	}
	for _, c := range cases {
		res, total, err := db.GetResultsFiltered(c.f)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if total != c.total || urlsOf(res) != c.urls {
			t.Errorf("%s: total=%d urls=%s, beklenen total=%d urls=%s", c.name, total, urlsOf(res), c.total, c.urls)
		}
	}

	srcs, cats, err := db.DistinctSourcesAndCategories()
	if err != nil || strings.Join(srcs, ",") != "Amnesia,Tor66,Tordex" || len(cats) != 3 {
		t.Fatalf("ayrık değerler: %v %v %v", srcs, cats, err)
	}
}

// TestCaseStatus_FilterUpdateAndCounts vaka yönetimi: geçersiz durum reddi, not
// rune-güvenli 4000'e kırpılır, durum filtresi ("any" = durumu olan) ve sayımlar.
func TestCaseStatus_FilterUpdateAndCounts(t *testing.T) {
	db := newTestDB(t)
	ids := seedFindings(t, db)
	a, b := ids[onionURL("a", "/1")], ids[onionURL("b", "/2")]

	for _, s := range []string{"", "new", "investigating", "confirmed", "dismissed"} {
		if !ValidCaseStatus(s) {
			t.Errorf("%q geçerli olmalı", s)
		}
	}
	if ValidCaseStatus("closed") {
		t.Error("bilinmeyen durum geçersiz olmalı")
	}
	if _, err := db.UpdateResultCase(a, "closed", ""); err == nil {
		t.Fatal("geçersiz durum reddedilmeli")
	}
	longNote := strings.Repeat("ş", 4100)
	if n, err := db.UpdateResultCase(a, "investigating", longNote); err != nil || n != 1 {
		t.Fatalf("güncelleme: %d %v", n, err)
	}
	if n, _ := db.UpdateResultCase(99999, "new", ""); n != 0 {
		t.Fatal("olmayan kayıt 0 satır etkilemeli")
	}
	got, _ := db.GetResultByID(a)
	if utf8.RuneCountInString(got.Note) != 4000 || !utf8.ValidString(got.Note) {
		t.Fatalf("not rune-güvenli kırpılmalı: %d", utf8.RuneCountInString(got.Note))
	}
	if _, err := db.UpdateResultCase(b, "dismissed", "yanlış pozitif"); err != nil {
		t.Fatal(err)
	}

	for status, want := range map[string]int{"investigating": 1, "dismissed": 1, "any": 2, "new": 0, "bogus": 4} {
		_, total, err := db.GetResultsFiltered(ResultFilter{CaseStatus: status})
		if err != nil || total != want {
			t.Errorf("CaseStatus=%q total=%d, beklenen %d (%v)", status, total, want, err)
		}
	}
	counts, err := db.CaseCounts()
	if err != nil || counts["investigating"] != 1 || counts["dismissed"] != 1 || counts["none"] != 2 {
		t.Fatalf("sayımlar: %v %v", counts, err)
	}
	// Durumu temizlemek "none"a döndürür.
	if _, err := db.UpdateResultCase(b, "", ""); err != nil {
		t.Fatal(err)
	}
	if counts, _ := db.CaseCounts(); counts["none"] != 3 {
		t.Fatalf("temizleme sonrası: %v", counts)
	}
}

func TestTagging_StatsByTagAndLegacyFallback(t *testing.T) {
	db := newTestDB(t)
	ids := seedFindings(t, db)
	a, c := ids[onionURL("a", "/1")], ids[onionURL("c", "/3")]

	// Etiket yokken eski auto_tags sütunundan sayım (geriye uyumluluk)
	if err := db.UpdateAutoTags(a, "leak, bank,leak"); err != nil {
		t.Fatal(err)
	}
	stats, err := db.GetTagStats()
	if err != nil || len(stats) != 2 || stats[0].Tag != "leak" || stats[0].Count != 2 || stats[1].Tag != "bank" {
		t.Fatalf("eski auto_tags sayımı: %+v %v", stats, err)
	}

	// ApplyTagging kritikliği 1..5'e kırpar, boş kategoriyi Genel yapar, result_tags'i eşitler.
	if err := db.ApplyTagging(a, "Leak, bank , leak", 7, 9, "  "); err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyTagging(c, "leak,carding", 2, -3, "Kart"); err != nil {
		t.Fatal(err)
	}
	ra, _ := db.GetResultByID(a)
	rc, _ := db.GetResultByID(c)
	if ra.Criticality != 5 || ra.Category != "Genel" || ra.KeywordCount != 7 || rc.Criticality != 1 {
		t.Fatalf("kırpma: a=%+v c=%+v", ra, rc)
	}
	stats, _ = db.GetTagStats()
	got := map[string]int{}
	for _, s := range stats {
		got[s.Tag] = s.Count
	}
	if got["leak"] != 2 || got["bank"] != 1 || got["carding"] != 1 || len(got) != 3 {
		t.Fatalf("etiket istatistiği (küçük harf, tekil): %v", got)
	}
	byTag, err := db.GetResultsByTag(" leak ", 0)
	if err != nil || len(byTag) != 2 {
		t.Fatalf("etikete göre: %d %v", len(byTag), err)
	}
	if tagged, total, _ := db.GetTaggedResultsCount(); tagged != 2 || total != 4 {
		t.Fatalf("etiketli sayım: %d/%d", tagged, total)
	}
	// Yeniden etiketleme eski etiketleri kaldırır.
	if err := db.ApplyTagging(c, "", 0, 1, "Genel"); err != nil {
		t.Fatal(err)
	}
	if byTag, _ := db.GetResultsByTag("carding", 10); len(byTag) != 0 {
		t.Fatalf("eski etiket kalmamalı: %+v", byTag)
	}
	if err := db.UpdateKeywordCount(c, 42); err != nil {
		t.Fatal(err)
	}
	if rc, _ := db.GetResultByID(c); rc.KeywordCount != 42 {
		t.Fatalf("keyword_count: %d", rc.KeywordCount)
	}
}

func TestNormalizeTagsAndEscapeLike(t *testing.T) {
	if got := strings.Join(normalizeTags(" A ,b,,a,\tB\t, c "), "|"); got != "a|b|c" {
		t.Fatalf("normalizeTags = %q", got)
	}
	if got := splitTags("x,"); len(got) != 1 {
		t.Fatalf("sondaki virgül boş etiket üretmemeli: %q", got)
	}
	if got := escapeLike(`50%_\`); got != `50\%\_\\` {
		t.Fatalf("escapeLike = %q", got)
	}
}

func TestDeleteResults_CascadesTagsAndArtifacts(t *testing.T) {
	db := newTestDB(t)
	ids := seedFindings(t, db)
	a := ids[onionURL("a", "/1")]
	if err := db.ApplyTagging(a, "leak", 1, 3, "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ReplaceArtifacts(a, []ResultArtifact{{Type: "email", Value: "a@b.org", Confidence: 0.9}}); err != nil {
		t.Fatal(err)
	}
	if n, err := db.DeleteResults(nil); n != 0 || err != nil {
		t.Fatal("boş liste no-op")
	}
	n, err := db.DeleteResults([]int64{a, 99999})
	if err != nil || n != 1 {
		t.Fatalf("silme: %d %v", n, err)
	}
	if stats, _ := db.GetTagStats(); len(stats) != 0 {
		t.Fatalf("etiketler CASCADE ile silinmeli: %+v", stats)
	}
	if arts, _ := db.GetArtifacts(a); len(arts) != 0 {
		t.Fatalf("artifact'lar CASCADE ile silinmeli: %+v", arts)
	}
}

func TestResultsListing_HistoryQueriesAndRecent(t *testing.T) {
	db := newTestDB(t)
	seedFindings(t, db)
	for i, q := range []string{"carding", "bank leak", "carding"} {
		if err := db.SaveSearchHistory(q, i+1); err != nil {
			t.Fatal(err)
		}
	}
	hist, err := db.GetSearchHistory(2)
	if err != nil || len(hist) != 2 {
		t.Fatalf("geçmiş: %+v %v", hist, err)
	}
	if _, searches, _ := db.GetStats(); searches != 3 {
		t.Fatalf("arama sayısı %d", searches)
	}
	qs, err := db.GetQueries()
	if err != nil || len(qs) != 3 || qs[0].Query != "carding" || qs[0].Count != 2 {
		t.Fatalf("sorgular: %+v %v", qs, err)
	}
	all, _ := db.GetResults(10, "")
	like, _ := db.GetResults(10, "bank")
	lim, _ := db.GetResults(1, "")
	if len(all) != 4 || len(like) != 2 || len(lim) != 1 {
		t.Fatalf("GetResults: %d %d %d", len(all), len(like), len(lim))
	}
	// Bir bulguyu 2 gün geriye al: son 24 saatte görünmemeli.
	if _, err := db.conn.Exec(`UPDATE search_results SET created_at = datetime('now','-48 hours') WHERE url = ?`, onionURL("a", "/1")); err != nil {
		t.Fatal(err)
	}
	recent, err := db.GetNewResults(24, 10)
	if err != nil || len(recent) != 3 {
		t.Fatalf("son 24 saat: %d %v", len(recent), err)
	}
	if older, _ := db.GetNewResults(72, 10); len(older) != 4 {
		t.Fatalf("son 72 saat: %d", len(older))
	}
}

// TestAnalytics_Aggregates gösterge paneli toplamları; sorgu filtresi tüm
// bölümlere uygulanır. Ortalama hit tam sayı olmayan ortalamada 0'a düşmemeli.
func TestAnalytics_Aggregates(t *testing.T) {
	db := newTestDB(t)
	seedFindings(t, db)
	if _, err := db.SaveResults([]SearchResult{{Title: "cn", URL: "https://clear.example.com/x", Source: "Tordex", Query: "carding", KeywordCount: 0}}); err != nil {
		t.Fatal(err)
	}

	d, err := db.GetAnalyticsData("day", "")
	if err != nil {
		t.Fatal(err)
	}
	if d.TotalSites != 5 || len(d.Queries) != 3 || len(d.Timeline) == 0 {
		t.Fatalf("genel: %+v", d)
	}
	if len(d.Domains) != 4 {
		t.Errorf("yalnız .onion alan adları sayılmalı: %+v", d.Domains)
	}
	srcCount := map[string]int{}
	for _, s := range d.Sources {
		srcCount[s.Source] = s.Count
	}
	if srcCount["Tordex"] != 3 || srcCount["Tor66"] != 1 {
		t.Errorf("kaynaklar: %v", srcCount)
	}
	// hits: 9,2,5,0,0 → toplam 16, en çok 9, ortalama 3.2 → 3
	ks := d.KeywordStats
	if ks.TotalHits != 16 || ks.MaxHits != 9 || ks.WithHits != 3 || ks.WithoutHits != 2 || ks.AvgHits != 3 {
		t.Errorf("kelime istatistiği: %+v", ks)
	}

	q, err := db.GetAnalyticsData("month", "carding")
	if err != nil {
		t.Fatal(err)
	}
	if q.TotalSites != 3 || len(q.Queries) != 0 || q.KeywordStats.TotalHits != 5 {
		t.Fatalf("sorgu filtresi: sites=%d queries=%d hits=%d", q.TotalSites, len(q.Queries), q.KeywordStats.TotalHits)
	}
	crit := map[int]int{}
	for _, c := range q.Criticality {
		crit[c.Level] = c.Count
	}
	if crit[4] != 1 || crit[1] != 2 {
		t.Errorf("kritiklik dağılımı: %v", crit)
	}
	for _, iv := range []string{"hour", "week", "month", "day", "bogus"} {
		d, err := db.GetAnalyticsData(iv, "")
		if err != nil || len(d.Timeline) == 0 {
			t.Errorf("aralık %s: %v", iv, err)
		}
		if iv == "month" && !strings.Contains(d.Timeline[0].Date, "-") {
			t.Errorf("ay biçimi: %q", d.Timeline[0].Date)
		}
	}
}

func TestArtifacts_ReplaceQueryAndPivot(t *testing.T) {
	db := newTestDB(t)
	ids := seedFindings(t, db)
	a, b := ids[onionURL("a", "/1")], ids[onionURL("b", "/2")]

	longCtx := strings.Repeat("ğ", 200) // 400 bayt, karakter ortasından kesilmemeli
	n, err := db.ReplaceArtifacts(a, []ResultArtifact{
		{Type: "email", Value: "  seller@protonmail.com ", Context: longCtx, Confidence: 0.9},
		{Type: "bitcoin", Value: "1BoatSLRHtKNngkdXEeobR76b53LETtpyT", Confidence: 0.8},
		{Type: "email", Value: "seller@protonmail.com", Confidence: 0.9}, // tekrar
		{Type: "hash", Value: "   ", Confidence: 0.5},                    // boş
		{Type: "hash", Value: strings.Repeat("f", 600), Confidence: 0.5}, // çok uzun
	})
	if err != nil || n != 2 {
		t.Fatalf("ReplaceArtifacts: n=%d err=%v", n, err)
	}
	arts, err := db.GetArtifacts(a)
	if err != nil || len(arts) != 2 || arts[0].Type != "email" || arts[0].Value != "seller@protonmail.com" {
		t.Fatalf("artifact'lar (güvene göre sıralı): %+v %v", arts, err)
	}
	if len(arts[0].Context) > 300 || !utf8.ValidString(arts[0].Context) || len(arts[0].Context) < 298 {
		t.Fatalf("bağlam 300 bayta karakter bölmeden kırpılmalı: len=%d valid=%v", len(arts[0].Context), utf8.ValidString(arts[0].Context))
	}
	if _, err := db.ReplaceArtifacts(b, []ResultArtifact{{Type: "email", Value: "seller@protonmail.com", Confidence: 0.7}}); err != nil {
		t.Fatal(err)
	}

	// Yeniden yazım öncekini değiştirir.
	if n, _ := db.ReplaceArtifacts(a, []ResultArtifact{{Type: "ip", Value: "185.220.101.5", Confidence: 0.6}}); n != 1 {
		t.Fatal("yeniden yazım")
	}
	if arts, _ := db.GetArtifacts(a); len(arts) != 1 || arts[0].Type != "ip" {
		t.Fatalf("yeniden yazım sonrası: %+v", arts)
	}

	counts, _ := db.ArtifactCounts()
	byType := map[string]int{}
	for _, c := range counts {
		byType[c.Type] = c.Count
	}
	if byType["email"] != 1 || byType["ip"] != 1 || len(byType) != 2 {
		t.Fatalf("tür sayımı: %v", byType)
	}
	perResult, _ := db.ArtifactCountByResult([]int64{a, b, 99999})
	if perResult[a] != 1 || perResult[b] != 1 || len(perResult) != 2 {
		t.Fatalf("bulgu başına: %v", perResult)
	}
	if m, _ := db.ArtifactCountByResult(nil); len(m) != 0 {
		t.Fatal("boş girdi")
	}

	pivot, err := db.SearchArtifacts("email", "PROTONMAIL", 0)
	if err != nil || len(pivot) != 1 || pivot[0].ResultID != b {
		t.Fatalf("IOC pivotu (büyük/küçük harf duyarsız LIKE): %+v %v", pivot, err)
	}
	if res, _ := db.SearchArtifacts("", "%", 10); len(res) != 0 {
		t.Fatalf("%% literal aranmalı: %+v", res)
	}
	if res, _ := db.SearchArtifacts("", "", 1000); len(res) != 2 {
		t.Fatalf("filtresiz: %+v", res)
	}
}

func TestScheduled_CRUDDueOrderAndKnownURLs(t *testing.T) {
	db := newTestDB(t)
	s1, err := db.CreateScheduledSearch("leak", 15, "https://hooks.example/x", 4)
	if err != nil {
		t.Fatal(err)
	}
	if s1.IntervalMinutes != 15 || !s1.Enabled || s1.AlertThreshold != 4 || s1.NextRunAt == nil || s1.TotalRuns != 0 {
		t.Fatalf("oluşturma: %+v", s1)
	}
	s2, _ := db.CreateScheduledSearch("carding", 60, "", 3)
	s3, _ := db.CreateScheduledSearch("market", 60, "", 3)

	// s3 next_run_at NULL (eski kayıt) → hemen due; s2 s1'den daha eski → önce
	if _, err := db.conn.Exec(`UPDATE scheduled_searches SET next_run_at = NULL WHERE id = ?`, s3.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.conn.Exec(`UPDATE scheduled_searches SET next_run_at = datetime('now','-10 minutes') WHERE id = ?`, s1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.conn.Exec(`UPDATE scheduled_searches SET next_run_at = datetime('now','-60 minutes') WHERE id = ?`, s2.ID); err != nil {
		t.Fatal(err)
	}
	due, err := db.GetDueScheduledSearches()
	if err != nil || len(due) != 3 {
		t.Fatalf("due: %d %v", len(due), err)
	}
	if due[0].ID != s3.ID || due[1].ID != s2.ID || due[2].ID != s1.ID {
		t.Fatalf("due sırası (NULL, en eski önce): %d,%d,%d", due[0].ID, due[1].ID, due[2].ID)
	}
	on, err := db.ToggleScheduledSearch(s2.ID)
	if err != nil || on {
		t.Fatalf("toggle kapat: %v %v", on, err)
	}
	if due, _ := db.GetDueScheduledSearches(); len(due) != 2 {
		t.Fatalf("pasif tarama due olmamalı: %d", len(due))
	}
	if on, _ := db.ToggleScheduledSearch(s2.ID); !on {
		t.Fatal("toggle aç")
	}
	if _, err := db.ToggleScheduledSearch(99999); err == nil {
		t.Fatal("olmayan ID hata vermeli")
	}

	all, err := db.GetAllScheduledSearches()
	if err != nil || len(all) != 3 {
		t.Fatalf("tümü: %d %v", len(all), err)
	}
	if err := db.DeleteScheduledSearch(s3.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetScheduledSearch(s3.ID); err == nil {
		t.Fatal("silinen tarama bulunmamalı")
	}
	if err := db.UpdateScheduledSearchAfterRun(99999, 1, 1); err == nil {
		t.Fatal("olmayan ID için hata beklenirdi")
	}

	if _, err := db.SaveResults([]SearchResult{
		{Title: "a", URL: onionURL("a", "/"), Source: "E", Query: "leak"},
		{Title: "b", URL: onionURL("b", "/"), Source: "E", Query: "LEAK"},
	}); err != nil {
		t.Fatal(err)
	}
	known, err := db.GetKnownURLsForQuery("leak")
	if err != nil || len(known) != 1 || !known[onionURL("a", "/")] {
		t.Fatalf("bilinen URL'ler sorguya özgü olmalı: %v %v", known, err)
	}
}

// TestWatchlist_UptimeChangeAndSeed izleme listesi: URL normalize/upsert, son
// kontrolün durumu, son 20 kontrol üzerinden uptime yüzdesi, içerik hash'i ile
// değişiklik takibi ve tohum listesi modları.
func TestWatchlist_UptimeChangeAndSeed(t *testing.T) {
	db := newTestDB(t)
	id, err := db.AddWatchlistItem(" Forum ", onionURL("w", "/")+"/", "", " not ")
	if err != nil || id == 0 {
		t.Fatalf("ekleme: %d %v", id, err)
	}
	item, err := db.GetWatchlistItem(id)
	if err != nil || item.URL != onionURL("w", "") || item.Name != "Forum" || item.Category != "Genel" || item.Notes != "not" || !item.Enabled {
		t.Fatalf("normalize: %+v %v", item, err)
	}
	// Aynı URL (sondaki / farkıyla) güncellenir, yeni satır açılmaz.
	id2, err := db.AddWatchlistItem("Forum v2", onionURL("w", ""), "Forum", "")
	if err != nil || id2 != id {
		t.Fatalf("upsert aynı ID dönmeli: %d vs %d (%v)", id2, id, err)
	}
	other, _ := db.AddWatchlistItem("Market", onionURL("m", ""), "Market", "")

	if h, err := db.GetLastWatchlistHash(id); err != nil || h != "" {
		t.Fatalf("kontrolsüz öğenin hash'i boş: %q %v", h, err)
	}
	// 5 düşük + 15 up + 5 up (son 20'nin hepsi up) ve en son kontrol değişiklik
	for i := 0; i < 25; i++ {
		status, hash, changed := "up", "h-old", false
		if i < 5 {
			status, hash = "down", ""
		}
		if i == 24 {
			hash, changed = "h-new", true
		}
		if err := db.RecordWatchlistCheck(id, status, 200, 120, hash, "Başlık", changed); err != nil {
			t.Fatal(err)
		}
	}
	for i, st := range []string{"up", "down", "engel", "down"} {
		if err := db.RecordWatchlistCheck(other, st, 503, i, "", "", false); err != nil {
			t.Fatal(err)
		}
	}

	list, err := db.GetWatchlistWithStatus()
	if err != nil || len(list) != 2 {
		t.Fatalf("durum listesi: %+v %v", list, err)
	}
	byID := map[int64]WatchlistStatus{}
	for _, s := range list {
		byID[s.ID] = s
	}
	w := byID[id]
	if w.Name != "Forum v2" || w.Status != "up" || !w.Changed || w.ContentHash != "h-new" || w.LastChecked == nil || w.UptimePct != 100 {
		t.Fatalf("forum durumu: %+v", w)
	}
	if m := byID[other]; m.UptimePct != 25 || m.Status != "down" || m.Changed {
		t.Fatalf("market (1/4 up, 'engel' up sayılmaz): %+v", m)
	}
	if h, _ := db.GetLastWatchlistHash(id); h != "h-new" {
		t.Fatalf("son hash: %q", h)
	}

	if err := db.SetWatchlistEnabled(other, false); err != nil {
		t.Fatal(err)
	}
	if en, _ := db.GetWatchlistItems(true); len(en) != 1 || en[0].ID != id {
		t.Fatalf("yalnız etkinler: %+v", en)
	}
	if all, _ := db.GetWatchlistItems(false); len(all) != 2 {
		t.Fatalf("hepsi: %+v", all)
	}
	if err := db.DeleteWatchlistItem(other); err != nil {
		t.Fatal(err)
	}
	var orphan int
	if err := db.conn.QueryRow(`SELECT COUNT(*) FROM watchlist_checks WHERE watchlist_id = ?`, other).Scan(&orphan); err != nil {
		t.Fatal(err)
	}
	if orphan != 0 {
		t.Fatalf("silinen öğenin kontrolleri kalmamalı: %d", orphan)
	}
	if _, err := db.GetWatchlistItem(other); err == nil {
		t.Fatal("silinen öğe bulunmamalı")
	}

	// Tohum modları
	t.Setenv("WATCHLIST_SEED", "none")
	if err := db.SeedDefaultWatchlist(); err != nil {
		t.Fatal(err)
	}
	if all, _ := db.GetWatchlistItems(false); len(all) != 1 {
		t.Fatalf("none modunda ekleme olmamalı: %d", len(all))
	}
	seedFile := t.TempDir() + "/seed.json"
	if err := writeFile(seedFile, `[{"name":"Dosya Hedefi","url":"`+onionURL("f", "/")+`","category":"Forum"}]`); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WATCHLIST_SEED", seedFile)
	if err := db.SeedDefaultWatchlist(); err != nil {
		t.Fatal(err)
	}
	if err := db.SeedDefaultWatchlist(); err != nil { // idempotent
		t.Fatal(err)
	}
	if all, _ := db.GetWatchlistItems(false); len(all) != 2 {
		t.Fatalf("dosyadan tohum (idempotent): %d", len(all))
	}
	t.Setenv("WATCHLIST_SEED", t.TempDir()+"/yok.json")
	if err := db.SeedDefaultWatchlist(); err != nil {
		t.Fatalf("eksik dosya sessizce atlanmalı: %v", err)
	}
	added, err := db.SeedTurkishWatchlist()
	if err != nil || added != len(defaultWatchlistSeed()) {
		t.Fatalf("Türk listesi: eklenen=%d err=%v", added, err)
	}
	if again, _ := db.SeedTurkishWatchlist(); again != 0 {
		t.Fatalf("ikinci tohumlama 0 eklemeli: %d", again)
	}
	t.Setenv("WATCHLIST_SEED", "turkey")
	if err := db.SeedDefaultWatchlist(); err != nil {
		t.Fatal(err)
	}
}

func TestGraph_TreeExpansionAndLimits(t *testing.T) {
	db := newTestDB(t)
	shared := onionURL("s", "/")
	if _, err := db.SaveResults([]SearchResult{
		{Title: "Ortak", URL: shared, Source: "Tordex", Query: "leak"},
		{Title: "Ortak", URL: shared, Source: "Tor66", Query: "leak"},
		{Title: "Yalnız", URL: onionURL("y", "/"), Source: "Tordex", Query: "leak"},
		{Title: "Market", URL: onionURL("m", "/"), Source: "Amnesia", Query: "market"},
	}); err != nil {
		t.Fatal(err)
	}
	root, err := db.GetGraphNodeByURL(shared)
	if err != nil || root.ParentID != nil || root.LinkType != "search" || root.Depth != 1 {
		t.Fatalf("kök düğüm: %+v %v", root, err)
	}
	// Derin zincir: root → c1 → c2 → c3 → c4 (hepsi açılmış)
	parent := root.ID
	var chain []int64
	for i := 1; i <= 4; i++ {
		p := parent
		id, err := db.SaveGraphNode(onionURL("c", fmt.Sprintf("/%d", i)), fmt.Sprintf("c%d", i), "c.onion", &p, i+1, "internal", "leak")
		if err != nil || id == 0 {
			t.Fatalf("düğüm %d: %v", i, err)
		}
		chain = append(chain, id)
		parent = id
	}
	for _, id := range append([]int64{root.ID}, chain...) {
		if err := db.MarkNodeExpanded(id); err != nil {
			t.Fatal(err)
		}
	}
	p := root.ID
	n, err := db.SaveGraphNodes([]GraphNodeDB{
		{URL: onionURL("x", "/ext"), Title: "dış", Domain: "x.onion", ParentID: &p, Depth: 2, LinkType: "external"},
		{URL: onionURL("c", "/1"), Title: "tekrar", ParentID: &p, Depth: 2, LinkType: "internal"}, // aynı (url,parent) → yok sayılır
	})
	if err != nil || n != 1 {
		t.Fatalf("toplu düğüm: %d %v", n, err)
	}
	kids, err := db.GetGraphChildren(root.ID)
	if err != nil || len(kids) != 2 || kids[0].LinkType != "external" {
		t.Fatalf("çocuklar (link_type sıralı): %+v %v", kids, err)
	}
	if byID, err := db.GetGraphNodeByID(chain[0]); err != nil || byID.ParentID == nil || *byID.ParentID != root.ID || !byID.IsExpanded {
		t.Fatalf("ID ile: %+v %v", byID, err)
	}
	if _, err := db.GetGraphNodeByID(99999); err == nil {
		t.Fatal("olmayan düğüm")
	}

	tree, err := db.GetGraphData("", GraphDataOptions{})
	if err != nil || tree.Type != "root" || len(tree.Children) != 2 {
		t.Fatalf("ağaç: %+v %v", tree, err)
	}
	leak := tree.Children[0]
	if leak.Name != "leak" || len(leak.Children) != 2 || leak.Children[0].Name != "Tor66" || leak.Children[1].Name != "Tordex" {
		t.Fatalf("sorgu → motor (alfabetik): %+v", leak)
	}
	var sharedNode *GraphNode
	for _, r := range leak.Children[1].Children {
		if r.URL == shared {
			sharedNode = r
		}
	}
	if sharedNode == nil || sharedNode.Count != 2 || !sharedNode.IsExpanded || len(sharedNode.Children) != 2 {
		t.Fatalf("çok kaynaklı, açılmış sonuç: %+v", sharedNode)
	}
	// Derinlik sınırı: c1 → c2 → c3 yüklenir, c3'ün çocuğu (c4) yüklenmez.
	var c1 *GraphNode
	for _, c := range sharedNode.Children {
		if c.Name == "c1" {
			c1 = c
		}
	}
	if c1 == nil || len(c1.Children) != 1 || len(c1.Children[0].Children) != 1 {
		t.Fatalf("zincir: %+v", c1)
	}
	if c3 := c1.Children[0].Children[0]; c3.Name != "c3" || len(c3.Children) != 0 {
		t.Fatalf("maxGraphDepth=3'te durmalı: %+v", c3)
	}

	if f, _ := db.GetGraphData("mark", GraphDataOptions{}); len(f.Children) != 1 || f.Children[0].Name != "market" {
		t.Fatalf("sorgu filtresi: %+v", f.Children)
	}
	if lim, _ := db.GetGraphData("", GraphDataOptions{MaxQueries: 1}); len(lim.Children) != 1 {
		t.Fatalf("MaxQueries: %d", len(lim.Children))
	}
	lim, _ := db.GetGraphData("leak", GraphDataOptions{MaxResultsPerEngine: 1, MaxQueries: -1})
	for _, eng := range lim.Children[0].Children {
		if len(eng.Children) != 1 {
			t.Fatalf("motor başına 1 sonuç: %s=%d", eng.Name, len(eng.Children))
		}
	}
}

func TestJobs_CrawlAndTaggingLifecycle(t *testing.T) {
	db := newTestDB(t)
	if err := db.CreateCrawlJob(CrawlJob{ID: "c1", SeedURL: onionURL("a", "/"), Query: "q", Depth: 2, MaxPages: 10, SameDomain: true, Status: "pending"}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateCrawlJob(CrawlJob{ID: "c2", SeedURL: onionURL("b", "/"), Status: "pending"}); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkCrawlRunning("c1"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateCrawlProgress("c1", 3, 2, 1); err != nil {
		t.Fatal(err)
	}
	j, err := db.GetCrawlJob("c1")
	if err != nil || j.Status != "running" || j.StartedAt == nil || j.PagesDone != 3 || j.FoundCount != 2 || j.SavedCount != 1 || !j.SameDomain {
		t.Fatalf("crawl c1: %+v %v", j, err)
	}
	if err := db.ResetRunningCrawlJobs(); err != nil {
		t.Fatal(err)
	}
	jobs, err := db.ListCrawlJobs(0)
	if err != nil || len(jobs) != 2 {
		t.Fatalf("liste: %d %v", len(jobs), err)
	}
	for _, j := range jobs {
		if j.Status != "cancelled" {
			t.Errorf("açılışta yarım iş iptal edilmeli: %+v", j)
		}
	}
	if err := db.MarkCrawlFinished("c2", "completed", ""); err != nil {
		t.Fatal(err)
	}
	if j, _ := db.GetCrawlJob("c2"); j.FinishedAt == nil || j.Status != "completed" {
		t.Fatalf("bitiş: %+v", j)
	}
	if _, err := db.GetCrawlJob("yok"); err == nil {
		t.Fatal("olmayan iş")
	}

	// Etiketleme işleri
	for _, id := range []string{"t1", "t2", "t3"} {
		if err := db.CreateTaggingJob(TaggingJob{ID: id, Query: "q", TotalCount: 2, Status: "pending", ResultIDsJSON: "[1,2]"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.MarkTaggingJobRunning("t1"); err != nil {
		t.Fatal(err)
	}
	_ = db.IncrementTaggingJobProgress("t1", true)
	_ = db.IncrementTaggingJobProgress("t1", false)
	tj, err := db.GetTaggingJob("t1")
	if err != nil || tj.Status != "running" || tj.ProcessedCount != 2 || tj.SuccessCount != 1 || tj.FailureCount != 1 || tj.StartedAt == nil {
		t.Fatalf("t1: %+v %v", tj, err)
	}
	if err := db.ResetRunningTaggingJobs(); err != nil {
		t.Fatal(err)
	}
	if tj, _ := db.GetTaggingJob("t1"); tj.Status != "pending" || tj.ErrorMessage != TaggingJobRestartedMsg {
		t.Fatalf("yeniden başlatmada pending: %+v", tj)
	}
	if err := db.MarkTaggingJobFinished("t3", "failed", "x"); err != nil {
		t.Fatal(err)
	}
	pend, err := db.ListTaggingJobsByStatuses([]string{"pending"}, 0)
	if err != nil || len(pend) != 2 {
		t.Fatalf("pending listesi: %d %v", len(pend), err)
	}
	if none, _ := db.ListTaggingJobsByStatuses(nil, 5); len(none) != 0 {
		t.Fatal("boş durum listesi")
	}
	if ids, err := DecodeTaggingJobIDs(&pend[0]); err != nil || len(ids) != 2 {
		t.Fatalf("decode: %v %v", ids, err)
	}
	if ids, err := DecodeTaggingJobIDs(nil); err != nil || len(ids) != 0 {
		t.Fatal("nil iş boş liste")
	}
	if _, err := DecodeTaggingJobIDs(&TaggingJob{ResultIDsJSON: "{"}); err == nil {
		t.Fatal("bozuk JSON hata vermeli")
	}
}
