package storage

import (
	"os"
	"testing"
	"time"
)

func writeFile(path, content string) error { return os.WriteFile(path, []byte(content), 0o600) }

func TestScreenshots_TimelineAndChangeHash(t *testing.T) {
	db := newTestDB(t)
	target := onionURL("s", "/")
	base := time.Now().Add(-time.Hour)
	for i, sh := range []Screenshot{
		{TargetURL: target, Source: "watchlist", SHA256: "h1", TakenAt: base},
		{TargetURL: target, Source: "watchlist", SHA256: "h2", TakenAt: base.Add(10 * time.Minute), Challenge: true, ChallengeKind: "Cloudflare"},
		{TargetURL: target, Source: "watchlist", SHA256: "bad", Status: "error", ErrorMsg: "timeout", TakenAt: base.Add(20 * time.Minute)},
		{TargetURL: onionURL("o", "/"), Source: "manual"},
	} {
		if id, err := db.SaveScreenshot(sh); err != nil || id == 0 {
			t.Fatalf("kayıt %d: %v", i, err)
		}
	}
	if h, err := db.LatestScreenshotHash(target); err != nil || h != "h2" {
		t.Fatalf("son başarılı hash (hatalı çekim sayılmaz): %q %v", h, err)
	}
	if h, _ := db.LatestScreenshotHash(onionURL("z", "/")); h != "" {
		t.Fatal("kayıtsız hedef boş hash")
	}
	at, err := db.LastScreenshotAt(target)
	if err != nil || at.Sub(base.Add(10*time.Minute)).Abs() > time.Second {
		t.Fatalf("son çekim zamanı: %v %v", at, err)
	}
	if at, _ := db.LastScreenshotAt(onionURL("z", "/")); !at.IsZero() {
		t.Fatal("kayıtsız hedef sıfır zaman")
	}
	tl, err := db.GetScreenshotsForTarget(target, 0)
	if err != nil || len(tl) != 2 || tl[0].SHA256 != "h2" || !tl[0].Challenge || tl[0].ChallengeKind != "Cloudflare" {
		t.Fatalf("zaman çizelgesi: %+v %v", tl, err)
	}
	recent, _ := db.GetRecentScreenshots(1000)
	if len(recent) != 3 {
		t.Fatalf("son başarılılar: %d", len(recent))
	}
	one, err := db.GetScreenshotByID(tl[0].ID)
	if err != nil || one.Status != "ok" || one.TargetURL != target {
		t.Fatalf("ID ile: %+v %v", one, err)
	}
	if _, err := db.GetScreenshotByID(99999); err == nil {
		t.Fatal("olmayan görüntü")
	}
}

func TestSiteProfiles_NormalizeAndCRUD(t *testing.T) {
	cases := map[string]string{
		" HTTP://Abc.ONION/path?x#y ": "abc.onion",
		"https://abc.onion:8080/":     "abc.onion",
		"abc.onion":                   "abc.onion",
		"[::1]:8080":                  "[::1]",
		"[::1]":                       "[::1]",
	}
	for in, want := range cases {
		if got := NormalizeHost(in); got != want {
			t.Errorf("NormalizeHost(%q) = %q, beklenen %q", in, got, want)
		}
	}

	db := newTestDB(t)
	if p, err := db.GetSiteProfile("abc.onion"); p != nil || err != nil {
		t.Fatalf("yok → nil,nil: %+v %v", p, err)
	}
	if err := db.UpsertSiteProfile(SiteProfile{Host: "http://ABC.onion/", Cookies: " s=1 ", UserAgent: " UA ", RenderJS: true}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertSiteProfile(SiteProfile{Host: "abc.onion", Cookies: "s=2", Notes: "güncel"}); err != nil {
		t.Fatal(err)
	}
	p, err := db.GetSiteProfile("https://abc.onion/login")
	if err != nil || p == nil || p.Cookies != "s=2" || p.UserAgent != "" || p.RenderJS || p.Notes != "güncel" {
		t.Fatalf("upsert: %+v %v", p, err)
	}
	_ = db.UpsertSiteProfile(SiteProfile{Host: "b.onion", RenderJS: true})
	list, _ := db.ListSiteProfiles()
	if len(list) != 2 || list[0].Host != "abc.onion" || !list[1].RenderJS {
		t.Fatalf("liste: %+v", list)
	}
	if err := db.DeleteSiteProfile("ABC.onion"); err != nil {
		t.Fatal(err)
	}
	if p, _ := db.GetSiteProfile("abc.onion"); p != nil {
		t.Fatal("silinmeli")
	}
}

func TestUsersAndSessions(t *testing.T) {
	db := newTestDB(t)
	if n, _ := db.CountUsers(); n != 0 {
		t.Fatalf("boş: %d", n)
	}
	admin, err := db.CreateUser(" Admin ", "h1", RoleAdmin)
	if err != nil || admin.Username != "Admin" || admin.Role != RoleAdmin || !admin.Enabled {
		t.Fatalf("admin: %+v %v", admin, err)
	}
	if _, err := db.CreateUser("admin", "h", RoleViewer); err != ErrUserExists {
		t.Fatalf("büyük/küçük harf duyarsız benzersizlik: %v", err)
	}
	an, _ := db.CreateUser("analyst1", "h2", "bogus")
	if an.Role != RoleAnalyst {
		t.Fatalf("geçersiz rol analyst'e düşmeli: %s", an.Role)
	}
	if u, err := db.GetUserByUsername("ADMIN"); err != nil || u.ID != admin.ID {
		t.Fatalf("ada göre: %+v %v", u, err)
	}
	if err := db.UpdateUserPassword(an.ID, "h3"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateUserRole(an.ID, "root"); err == nil {
		t.Fatal("geçersiz rol reddedilmeli")
	}
	if err := db.UpdateUserRole(an.ID, RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.CountAdmins(); n != 2 {
		t.Fatalf("admin sayısı: %d", n)
	}
	if err := db.SetUserEnabled(an.ID, false); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.CountAdmins(); n != 1 {
		t.Fatalf("pasif admin sayılmamalı: %d", n)
	}
	if err := db.TouchUserLogin(" admin "); err != nil {
		t.Fatal(err)
	}
	if u, _ := db.GetUserByID(admin.ID); u.LastLoginAt == nil {
		t.Fatal("son giriş yazılmalı")
	}
	if list, _ := db.ListUsers(); len(list) != 2 || list[0].Username != "Admin" {
		t.Fatalf("liste: %+v", list)
	}

	exp := time.Now().Add(time.Hour)
	if err := db.CreateSession("s1", "analyst1", "csrf", exp); err != nil {
		t.Fatal(err)
	}
	if ok, err := db.SessionExists("s1"); !ok || err != nil {
		t.Fatalf("oturum var: %v %v", ok, err)
	}
	newExp := time.Now().Add(2 * time.Hour)
	if err := db.TouchSession("s1", newExp); err != nil {
		t.Fatal(err)
	}
	if s, _ := db.GetSession("s1"); s.ExpiresAt.Sub(newExp).Abs() > time.Second {
		t.Fatalf("süre uzatılmalı: %v", s.ExpiresAt)
	}
	if err := db.DeleteUser(an.ID); err != nil {
		t.Fatal(err)
	}
	if ok, _ := db.SessionExists("s1"); ok {
		t.Fatal("kullanıcı silinince oturumları da silinmeli")
	}
	if err := db.CreateSession("s2", "Admin", "c", exp); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteSession("s2"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := db.SessionExists("s2"); ok {
		t.Fatal("silinen oturum")
	}
	if err := db.DeleteUser(99999); err == nil {
		t.Fatal("olmayan kullanıcı")
	}
}

func TestEngineCheckCountersAndAlertConfig(t *testing.T) {
	db := newTestDB(t)
	if err := db.UpsertEngineStat("E", "http://e/{query}", true); err != nil {
		t.Fatal(err)
	}
	_ = db.UpdateEngineCheck("E", "down", 100, 0)
	_ = db.UpdateEngineCheck("E", "down", 100, 0)
	_ = db.UpdateEngineCheck("E", "up", 50, 7)
	_ = db.UpdateEngineCheck("E", "down", 0, 0)
	stats, _ := db.GetAllEngineStats()
	if len(stats) != 1 {
		t.Fatal(stats)
	}
	s := stats[0]
	if s.SuccessCount != 1 || s.FailCount != 3 || s.ConsecutiveFails != 1 || s.TotalResults != 7 || s.LastStatus != "down" || s.LastCheckedAt == nil {
		t.Fatalf("sayaçlar: %+v", s)
	}

	cfg, err := db.GetAlertConfig()
	if err != nil || cfg.Enabled || cfg.MinCriticality != 3 {
		t.Fatalf("varsayılan bildirim ayarı: %+v %v", cfg, err)
	}
	if err := db.SaveAlertConfig(AlertConfig{WebhookURL: "https://hooks.example/a", MinCriticality: 4, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	cfg, _ = db.GetAlertConfig()
	if !cfg.Enabled || cfg.MinCriticality != 4 || cfg.WebhookURL != "https://hooks.example/a" {
		t.Fatalf("kaydedilen ayar: %+v", cfg)
	}
	if db.GetDBConn() == nil {
		t.Fatal("ham bağlantı")
	}
}
