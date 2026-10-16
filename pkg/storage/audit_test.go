package storage

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func appendAudit(t *testing.T, db *DB, e AuditEvent) AuditEvent {
	t.Helper()
	out, err := db.AppendAuditEvent(e)
	if err != nil {
		t.Fatalf("AppendAuditEvent(%s): %v", e.Action, err)
	}
	return out
}

func mustVerify(t *testing.T, db *DB) AuditChainStatus {
	t.Helper()
	st, err := db.AuditChainReport()
	if err != nil {
		t.Fatalf("AuditChainReport: %v", err)
	}
	return st
}

func seedAudit(t *testing.T, db *DB, n int) []AuditEvent {
	t.Helper()
	out := make([]AuditEvent, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, appendAudit(t, db, AuditEvent{
			Actor: "admin", Role: RoleAdmin, AuthMethod: AuthMethodSession, IP: "10.0.0.1",
			Action: "results.delete", Target: "results", Details: `{"count": 3}`, Success: true,
		}))
	}
	return out
}

// Zincir: kimlikler ardışık, her satır bir öncekinin hash'ine bağlı, doğrulama temiz.
func TestAudit_AppendBuildsVerifiableChain(t *testing.T) {
	db := newTestDB(t)
	if ok, broken, err := db.VerifyAuditChain(); err != nil || !ok || broken != 0 {
		t.Fatalf("boş zincir geçerli olmalı: ok=%v broken=%d err=%v", ok, broken, err)
	}
	evs := seedAudit(t, db, 5)
	for i, e := range evs {
		if e.ID != int64(i+1) {
			t.Fatalf("kimlik ardışık olmalı: %d ≠ %d", e.ID, i+1)
		}
		if i == 0 && e.PrevHash != "" {
			t.Fatal("ilk satırın prev_hash'i boş olmalı (genesis)")
		}
		if i > 0 && e.PrevHash != evs[i-1].Hash {
			t.Fatalf("satır %d önceki hash'e bağlı değil", e.ID)
		}
		if len(e.Hash) != 64 {
			t.Fatalf("sha256 hex bekleniyordu: %q", e.Hash)
		}
	}
	if evs[0].Details != `{"count":3}` {
		t.Fatalf("ayrıntı kompakt JSON olmalı: %q", evs[0].Details)
	}
	st := mustVerify(t, db)
	if !st.OK || st.Checked != 5 || st.HeadID != 5 || st.HeadHash != evs[4].Hash {
		t.Fatalf("doğrulama: %+v", st)
	}
	// Yeniden okunan satırın hash'i aynı kanonik girdiden yeniden üretilebilmeli
	list, total, err := db.ListAuditEvents(AuditFilter{Limit: 10})
	if err != nil || total != 5 || len(list) != 5 || list[0].ID != 5 {
		t.Fatalf("liste: total=%d len=%d err=%v", total, len(list), err)
	}
	if list[0].Time.IsZero() || list[0].Time.Location() != time.UTC {
		t.Fatalf("zaman UTC olarak okunmalı: %v", list[0].Time)
	}
}

// Bir satır değiştirilirse doğrulama TAM o satırda kırılmalı.
func TestAudit_TamperedRowDetected(t *testing.T) {
	for _, tc := range []struct{ name, sql string }{
		{"details", `UPDATE audit_events SET details = '{"count":999}' WHERE id = 3`},
		{"success", `UPDATE audit_events SET success = 0 WHERE id = 3`},
		{"actor", `UPDATE audit_events SET actor = 'mallory' WHERE id = 3`},
		{"ts", `UPDATE audit_events SET ts = '2001-01-01T00:00:00.000000000Z' WHERE id = 3`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestDB(t)
			seedAudit(t, db, 5)
			// Uygulama düzeyi koruma: UPDATE tetikleyiciyle reddedilir
			if _, err := db.conn.Exec(tc.sql); err == nil || !strings.Contains(err.Error(), "yalnız eklenebilir") {
				t.Fatalf("UPDATE tetikleyiciyle reddedilmeliydi: %v", err)
			}
			// Dosyaya doğrudan erişen saldırgan tetikleyiciyi kaldırabilir — zincir yine yakalar
			if _, err := db.conn.Exec(`DROP TRIGGER audit_events_no_update`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.conn.Exec(tc.sql); err != nil {
				t.Fatal(err)
			}
			ok, broken, err := db.VerifyAuditChain()
			if err != nil || ok || broken != 3 {
				t.Fatalf("kurcalama #3'te yakalanmalıydı: ok=%v broken=%d err=%v", ok, broken, err)
			}
			if st := mustVerify(t, db); st.Reason != "hash" {
				t.Fatalf("neden 'hash' olmalı: %+v", st)
			}
		})
	}
}

// Ortadan satır silmek, yeniden bağlamak ya da sondan kırpmak yakalanır.
func TestAudit_DeletionAndTruncationDetected(t *testing.T) {
	forceDelete := func(t *testing.T, db *DB, where string) {
		t.Helper()
		if _, err := db.conn.Exec(`DELETE FROM audit_events WHERE ` + where); err == nil {
			t.Fatal("DELETE tetikleyiciyle reddedilmeliydi")
		}
		if _, err := db.conn.Exec(`DROP TRIGGER audit_events_no_delete`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.conn.Exec(`DELETE FROM audit_events WHERE ` + where); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("middle", func(t *testing.T) {
		db := newTestDB(t)
		seedAudit(t, db, 5)
		forceDelete(t, db, "id = 3")
		st := mustVerify(t, db)
		if st.OK || st.BrokenAt != 4 || st.Reason != "gap" {
			t.Fatalf("ortadan silme #4'te (boşluk) yakalanmalı: %+v", st)
		}
	})
	t.Run("oldest-without-anchor", func(t *testing.T) {
		db := newTestDB(t)
		seedAudit(t, db, 5)
		forceDelete(t, db, "id <= 2")
		st := mustVerify(t, db)
		if st.OK || st.BrokenAt != 3 || st.Reason != "anchor" {
			t.Fatalf("çapasız baş silme yakalanmalı: %+v", st)
		}
	})
	t.Run("tail", func(t *testing.T) {
		db := newTestDB(t)
		seedAudit(t, db, 5)
		forceDelete(t, db, "id = 5")
		st := mustVerify(t, db)
		if st.OK || st.BrokenAt != 5 || st.Reason != "head" {
			t.Fatalf("sondan kırpma yakalanmalı: %+v", st)
		}
	})
}

// Saklama budaması: eski satırlar silinir, çapa kaydedilir, zincir doğrulanabilir kalır
// ve yeni eklemeler zinciri sürdürür.
func TestAudit_RetentionKeepsChainVerifiable(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	old := now.Add(-400 * 24 * time.Hour)
	for i := 0; i < 3; i++ {
		appendAudit(t, db, AuditEvent{Action: "auth.login.success", Actor: "old", AuthMethod: AuthMethodSession, Success: true, Time: old.Add(time.Duration(i) * time.Minute)})
	}
	for i := 0; i < 2; i++ {
		appendAudit(t, db, AuditEvent{Action: "auth.login.success", Actor: "new", AuthMethod: AuthMethodSession, Success: true})
	}

	n, err := db.PruneAuditEvents(now.Add(-365 * 24 * time.Hour))
	if err != nil || n != 3 {
		t.Fatalf("3 eski satır budanmalı: n=%d err=%v", n, err)
	}
	st := mustVerify(t, db)
	if !st.OK || st.AnchorID != 3 || st.Checked != 2 || st.PrunedTotal != 3 {
		t.Fatalf("budama sonrası zincir geçerli olmalı: %+v", st)
	}
	// Tekrar budama: silinecek bir şey yok
	if n, err := db.PruneAuditEvents(now.Add(-365 * 24 * time.Hour)); err != nil || n != 0 {
		t.Fatalf("ikinci budama no-op olmalı: n=%d err=%v", n, err)
	}
	// Yeni olay zinciri sürdürür
	e := appendAudit(t, db, AuditEvent{Action: "audit.prune", AuthMethod: AuthMethodSystem, Actor: "system", Success: true})
	if e.ID != 6 {
		t.Fatalf("kimlik budamadan sonra da artmalı: %d", e.ID)
	}
	if st := mustVerify(t, db); !st.OK || st.Checked != 3 {
		t.Fatalf("ekleme sonrası zincir: %+v", st)
	}
	// Budama bayrağı işlem sonunda kapanır: elle silme yine reddedilir
	if _, err := db.conn.Exec(`DELETE FROM audit_events WHERE id = 6`); err == nil {
		t.Fatal("budama dışında DELETE reddedilmeli")
	}
	// Her şey budanırsa çapa = baş, boş zincir geçerli
	if n, err := db.PruneAuditEvents(now.Add(time.Hour)); err != nil || n != 3 {
		t.Fatalf("tam budama: n=%d err=%v", n, err)
	}
	if st := mustVerify(t, db); !st.OK || st.Checked != 0 || st.AnchorID != 6 || st.HeadID != 6 {
		t.Fatalf("tam budama sonrası: %+v", st)
	}
}

// Girdi temizliği: geçersiz eylem reddedilir, kontrol karakterleri atılır,
// geçersiz/aşırı büyük ayrıntı sınırlanır, bilinmeyen yöntem anonymous olur.
func TestAudit_InputNormalization(t *testing.T) {
	db := newTestDB(t)
	for _, bad := range []string{"", "Auth.Login", "auth login", ".auth", "auth.", "auth;drop", strings.Repeat("a", 65)} {
		if _, err := db.AppendAuditEvent(AuditEvent{Action: bad}); err != ErrInvalidAuditAction {
			t.Errorf("eylem %q reddedilmeliydi: %v", bad, err)
		}
	}
	e := appendAudit(t, db, AuditEvent{
		Action: "auth.login.failure", Actor: "evil\r\nINJECT\x1b[31m", AuthMethod: "magic",
		Target: strings.Repeat("ş", 900), Details: "not json",
	})
	if strings.ContainsAny(e.Actor, "\r\n\x1b") || e.AuthMethod != AuthMethodAnonymous {
		t.Fatalf("temizlik: actor=%q method=%q", e.Actor, e.AuthMethod)
	}
	if len([]rune(e.Target)) != maxAuditTargetLen {
		t.Fatalf("hedef %d rune'a kırpılmalı: %d", maxAuditTargetLen, len([]rune(e.Target)))
	}
	if e.Details != `{"raw":"not json"}` {
		t.Fatalf("geçersiz JSON sarılmalı: %q", e.Details)
	}
	big := appendAudit(t, db, AuditEvent{Action: "x.y", Details: `{"v":"` + strings.Repeat("a", 5000) + `"}`})
	if len(big.Details) > MaxAuditDetailsBytes || !strings.Contains(big.Details, `"truncated":true`) {
		t.Fatalf("4KB sınırı: %q", big.Details)
	}
	if st := mustVerify(t, db); !st.OK {
		t.Fatalf("normalize edilen satırlar da doğrulanmalı: %+v", st)
	}
}

func TestAudit_FilterCountAndExportOrder(t *testing.T) {
	db := newTestDB(t)
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	add := func(actor, action, target, details string, ok bool, at time.Time) {
		appendAudit(t, db, AuditEvent{Actor: actor, Action: action, Target: target, Details: details, Success: ok, AuthMethod: AuthMethodSession, IP: "192.0.2.7", Time: at})
	}
	add("alice", "auth.login.success", "alice", "", true, base)
	add("", "auth.login.failure", "user:bob", `{"reason":"bad_credentials"}`, false, base.Add(time.Hour))
	add("Alice", "user.create", "user:carol", `{"role":"viewer"}`, true, base.Add(2*time.Hour))
	add("bob", "export.csv", "results", `{"q":"100%_leak"}`, true, base.Add(48*time.Hour))
	add("bob", "settings.env.update", "env", `{"changed":["RATE_LIMIT_RPS"]}`, true, base.Add(72*time.Hour))

	count := func(f AuditFilter) int {
		t.Helper()
		n, err := db.CountAuditEvents(f)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	f, tr := false, true
	cases := []struct {
		name string
		f    AuditFilter
		want int
	}{
		{"all", AuditFilter{}, 5},
		{"actor-nocase", AuditFilter{Actor: "ALICE"}, 2},
		{"prefix-auth", AuditFilter{ActionPrefixes: []string{"auth."}}, 2},
		{"prefix-multi", AuditFilter{ActionPrefixes: []string{"user.", "settings."}}, 2},
		{"prefix-exact-code", AuditFilter{ActionPrefixes: []string{"auth.login.failure"}}, 1},
		{"failures", AuditFilter{Success: &f}, 1},
		{"successes", AuditFilter{Success: &tr}, 4},
		{"from", AuditFilter{From: base.Add(24 * time.Hour)}, 2},
		{"to", AuditFilter{To: base.Add(2 * time.Hour)}, 2},
		{"range", AuditFilter{From: base.Add(time.Hour), To: base.Add(49 * time.Hour)}, 3},
		{"text-target", AuditFilter{Text: "carol"}, 1},
		{"text-details", AuditFilter{Text: "RATE_LIMIT"}, 1},
		{"text-ip", AuditFilter{Text: "192.0.2.7"}, 5},
		{"text-like-escaped", AuditFilter{Text: "100%_"}, 1},
		{"text-wildcard-literal", AuditFilter{Text: "%"}, 1},
		{"combined", AuditFilter{Actor: "bob", ActionPrefixes: []string{"export."}, Success: &tr}, 1},
	}
	for _, c := range cases {
		if got := count(c.f); got != c.want {
			t.Errorf("%s: %d ≠ %d", c.name, got, c.want)
		}
	}

	// Sayfalama: en yeni önce
	page, total, err := db.ListAuditEvents(AuditFilter{Limit: 2, Offset: 2})
	if err != nil || total != 5 || len(page) != 2 || page[0].ID != 3 || page[1].ID != 2 {
		t.Fatalf("sayfalama: total=%d ids=%v err=%v", total, ids(page), err)
	}

	// Dışa aktarma: en yeni 3 kayıt, kronolojik sırayla
	var got []int64
	if err := db.EachAuditEvent(AuditFilter{}, 3, func(e AuditEvent) error { got = append(got, e.ID); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != 3 || got[2] != 5 {
		t.Fatalf("dışa aktarma sırası: %v", got)
	}
	got = nil
	if err := db.EachAuditEvent(AuditFilter{Actor: "bob"}, 100, func(e AuditEvent) error { got = append(got, e.ID); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != 4 || got[1] != 5 {
		t.Fatalf("filtreli dışa aktarma: %v", got)
	}

	actors, err := db.DistinctAuditActors(10)
	if err != nil || len(actors) < 2 {
		t.Fatalf("aktörler: %v %v", actors, err)
	}
	st, err := db.GetAuditStats(base.Add(73 * time.Hour))
	if err != nil || st.Total != 5 || st.Failures24 != 0 || st.Actors24 != 1 {
		t.Fatalf("istatistik: %+v %v", st, err)
	}
	st, _ = db.GetAuditStats(base.Add(2 * time.Hour))
	if st.Failures24 != 1 || st.Actors24 != 2 { // alice + Alice tek aktör sayılır (+ bob)
		t.Fatalf("24s istatistik: %+v", st)
	}
}

// Büyük zincir parti sınırını aşınca da doğru yürünmeli.
func TestAudit_VerifyAcrossBatches(t *testing.T) {
	db := newTestDB(t)
	for i := 0; i < auditBatchSize+25; i++ {
		appendAudit(t, db, AuditEvent{Action: "search.run", Actor: "a", AuthMethod: AuthMethodSession, Success: true})
	}
	if st := mustVerify(t, db); !st.OK || st.Checked != auditBatchSize+25 {
		t.Fatalf("parti sınırı: %+v", st)
	}
}

func ids(list []AuditEvent) []int64 {
	out := make([]int64, 0, len(list))
	for _, e := range list {
		out = append(out, e.ID)
	}
	return out
}

// Artımlı doğrulama: yalnız yeni kuyruk yürünür, sayaç birikir; kuyrukta
// kurcalama yakalanır; budama sonrası tam doğrulamaya düşülür.
func TestAudit_ExtendReport(t *testing.T) {
	db := newTestDB(t)
	seedAudit(t, db, 3)
	base := mustVerify(t, db)
	if st, err := db.ExtendAuditChainReport(base); err != nil || !st.OK || st.Checked != 3 {
		t.Fatalf("değişmeyen zincir: %+v %v", st, err)
	}
	seedAudit(t, db, 2)
	ext, err := db.ExtendAuditChainReport(base)
	if err != nil || !ext.OK || ext.Checked != 5 || ext.HeadID != 5 {
		t.Fatalf("artımlı doğrulama: %+v %v", ext, err)
	}
	if _, err := db.conn.Exec(`DROP TRIGGER audit_events_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.conn.Exec(`UPDATE audit_events SET target = 'x' WHERE id = 5`); err != nil {
		t.Fatal(err)
	}
	if st, _ := db.ExtendAuditChainReport(base); st.OK || st.BrokenAt != 5 {
		t.Fatalf("kuyruktaki kurcalama yakalanmalı: %+v", st)
	}
	// Budama (çapa değişti) → tam doğrulama
	db2 := newTestDB(t)
	appendAudit(t, db2, AuditEvent{Action: "a.b", Time: time.Now().Add(-48 * time.Hour)})
	appendAudit(t, db2, AuditEvent{Action: "a.b"})
	prev := mustVerify(t, db2)
	if _, err := db2.PruneAuditEvents(time.Now().Add(-24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if st, err := db2.ExtendAuditChainReport(prev); err != nil || !st.OK || st.AnchorID != 1 || st.Checked != 1 {
		t.Fatalf("budama sonrası tam doğrulama: %+v %v", st, err)
	}
}

// Başın ötesine doğrudan eklenen satır (hash yeniden hesaplanmadan) ya da geri
// sarılmış zincir durumu: tetikleyiciler reddeder; tetikleyiciler kaldırılsa
// bile doğrulama "extra" ile kırılır (tam ve artımlı yolda).
func TestAudit_RowsPastHeadAndStateRewindDetected(t *testing.T) {
	const forged = `INSERT INTO audit_events (id, ts, actor, role, auth_method, ip, action, target, details, success, prev_hash, hash)
		VALUES (4, '2026-01-01T00:00:00.000000000Z', 'alice', 'admin', 'session', '', 'results.delete', 'results', '{"deleted":9999}', 1, 'x', 'y')`

	t.Run("insert-past-head", func(t *testing.T) {
		db := newTestDB(t)
		seedAudit(t, db, 3)
		base := mustVerify(t, db)
		if _, err := db.conn.Exec(forged); err == nil || !strings.Contains(err.Error(), "zincirin sonuna") {
			t.Fatalf("zincir dışı INSERT tetikleyiciyle reddedilmeliydi: %v", err)
		}
		if _, err := db.conn.Exec(`DROP TRIGGER audit_events_append_only`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.conn.Exec(forged); err != nil {
			t.Fatal(err)
		}
		if st := mustVerify(t, db); st.OK || st.BrokenAt != 4 || st.Reason != "extra" {
			t.Fatalf("başın ötesindeki satır yakalanmalı: %+v", st)
		}
		if st, err := db.ExtendAuditChainReport(base); err != nil || st.OK || st.BrokenAt != 4 {
			t.Fatalf("artımlı doğrulama da yakalamalı: %+v %v", st, err)
		}
		// Kalıcı yazma hatası çağırana döner (sessiz değil)
		if _, err := db.AppendAuditEvent(AuditEvent{Action: "a.b"}); err == nil {
			t.Fatal("çakışan kimlikle ekleme hata döndürmeli")
		}
	})

	t.Run("state-rewind", func(t *testing.T) {
		db := newTestDB(t)
		seedAudit(t, db, 3)
		for _, q := range []string{
			`UPDATE audit_chain_state SET head_id = 0, head_hash = '' WHERE id = 1`,
			`UPDATE audit_chain_state SET head_id = 4, head_hash = 'y' WHERE id = 1`,
			`UPDATE audit_chain_state SET anchor_id = 2 WHERE id = 1`,
			`UPDATE audit_chain_state SET pruning = 1, anchor_id = 9 WHERE id = 1`,
			`UPDATE audit_chain_state SET pruned_total = -1 WHERE id = 1`,
			`DELETE FROM audit_chain_state`,
		} {
			if _, err := db.conn.Exec(q); err == nil {
				t.Fatalf("zincir durumu değişikliği reddedilmeliydi: %s", q)
			}
		}
		if st := mustVerify(t, db); !st.OK || st.Checked != 3 {
			t.Fatalf("reddedilen değişiklikler zinciri bozmamalı: %+v", st)
		}
		// Doğrudan erişimli saldırgan: tetikleyiciyi kaldırıp başı sıfırlar
		if _, err := db.conn.Exec(`DROP TRIGGER audit_chain_state_guard`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.conn.Exec(`UPDATE audit_chain_state SET head_id = 0, head_hash = '' WHERE id = 1`); err != nil {
			t.Fatal(err)
		}
		if st := mustVerify(t, db); st.OK || st.BrokenAt != 1 || st.Reason != "extra" || st.Checked != 0 {
			t.Fatalf("geri sarılmış baş yakalanmalı: %+v", st)
		}
	})
}

// JSON'daki "ts" birebir hash girdisidir: sondaki sıfırlar kırpılmaz, dışarıda
// yeniden hesaplanan hash satırın hash'ine eşittir.
func TestAudit_JSONTimestampIsHashInput(t *testing.T) {
	db := newTestDB(t)
	at := time.Date(2026, 1, 2, 3, 4, 5, 120000000, time.UTC)
	e := appendAudit(t, db, AuditEvent{Action: "a.b", Actor: "x", AuthMethod: AuthMethodSession, Time: at, Success: true})
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var back AuditEvent
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.TS != "2026-01-02T03:04:05.120000000Z" {
		t.Fatalf("ts birebir olmalı: %s", b)
	}
	if got := auditHash(back.PrevHash, &back); got != e.Hash {
		t.Fatalf("JSON alanlarından yeniden hesaplanan hash uyuşmuyor: %s ≠ %s", got, e.Hash)
	}
}
