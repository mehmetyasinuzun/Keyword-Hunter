package storage

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestGenerateAPIToken_FormatAndUniqueness(t *testing.T) {
	seen := make(map[string]bool, 200)
	for i := 0; i < 200; i++ {
		tok, err := generateAPIToken()
		if err != nil {
			t.Fatal(err)
		}
		if len(tok) != len(APITokenPrefix)+apiTokenRandLen || !LooksLikeAPIToken(tok) {
			t.Fatalf("biçim: %q", tok)
		}
		if strings.ContainsAny(tok, "=+/-") {
			t.Fatalf("dolgu/özel karakter olmamalı: %q", tok)
		}
		if seen[tok] {
			t.Fatal("tekrarlayan token")
		}
		seen[tok] = true
	}
}

func TestLooksLikeAPIToken(t *testing.T) {
	for in, want := range map[string]bool{
		"kh_abcDEF0123":                  true,
		"kh_":                            false,
		"KH_abc":                         false,
		"xx_abc":                         false,
		"kh_abc def":                     false,
		"kh_abc-def":                     false,
		"kh_" + strings.Repeat("a", 200): false,
		"":                               false,
	} {
		if got := LooksLikeAPIToken(in); got != want {
			t.Errorf("LooksLikeAPIToken(%q)=%v want %v", in, got, want)
		}
	}
}

func TestAPITokens_LifecycleAndStatus(t *testing.T) {
	db := newTestDB(t)
	u, err := db.CreateUser("alice", "x", RoleAnalyst)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.CreateAPIToken(u.ID, u.Username, "bad", "admin", nil); err == nil {
		t.Fatal("geçersiz kapsam kabul edildi")
	}
	plain, tok, err := db.CreateAPIToken(u.ID, u.Username, "siem", TokenScopeRead, nil)
	if err != nil {
		t.Fatal(err)
	}
	if tok.TokenHash != HashAPIToken(plain) || tok.Prefix != plain[:APITokenDisplayLen] || tok.UserID != u.ID {
		t.Fatalf("kayıt: %+v", tok)
	}
	got, err := db.GetAPITokenByHash(HashAPIToken(plain))
	if err != nil || got.ID != tok.ID {
		t.Fatalf("özetle arama: %v %+v", err, got)
	}
	if _, err := db.GetAPITokenByHash(HashAPIToken(plain + "x")); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("bilinmeyen özet: %v", err)
	}
	now := time.Now()
	if got.Status(now) != TokenStatusActive {
		t.Fatal("etkin olmalı")
	}

	past := now.Add(-time.Hour)
	_, expired, err := db.CreateAPIToken(u.ID, u.Username, "old", TokenScopeWrite, &past)
	if err != nil {
		t.Fatal(err)
	}
	if expired.Status(now) != TokenStatusExpired {
		t.Fatalf("süresi dolmuş olmalı: %s", expired.Status(now))
	}
	if n, _ := db.CountActiveAPITokens("ALICE", now); n != 1 {
		t.Fatalf("etkin sayısı (büyük/küçük harf duyarsız): %d", n)
	}

	ok, err := db.RevokeAPIToken(tok.ID)
	if err != nil || !ok {
		t.Fatalf("iptal: %v %v", ok, err)
	}
	if ok, _ := db.RevokeAPIToken(tok.ID); ok {
		t.Fatal("ikinci iptal değişiklik yapmamalı")
	}
	got, _ = db.GetAPIToken(tok.ID)
	if got.Status(now) != TokenStatusRevoked || got.RevokedAt == nil {
		t.Fatalf("iptal durumu: %+v", got)
	}
	if n, _ := db.CountActiveAPITokens("alice", now); n != 0 {
		t.Fatalf("iptal sonrası etkin: %d", n)
	}

	if err := db.TouchAPIToken(expired.ID, strings.Repeat("9", 100), now); err != nil {
		t.Fatal(err)
	}
	got, _ = db.GetAPIToken(expired.ID)
	if got.LastUsedAt == nil || len(got.LastUsedIP) != 64 {
		t.Fatalf("touch: %+v", got)
	}

	// Başka kullanıcı + listeleme
	b, _ := db.CreateUser("bob", "x", RoleViewer)
	if _, _, err := db.CreateAPIToken(b.ID, b.Username, "bob-tok", TokenScopeRead, nil); err != nil {
		t.Fatal(err)
	}
	mine, err := db.ListAPITokens("alice")
	if err != nil || len(mine) != 2 || mine[0].ID < mine[1].ID {
		t.Fatalf("alice listesi (en yeni önce): %v %+v", err, mine)
	}
	all, _ := db.ListAPITokens("")
	if len(all) != 3 {
		t.Fatalf("tüm liste: %d", len(all))
	}

	// Kullanıcı silinince token'ları da gider
	if err := db.DeleteUser(u.ID); err != nil {
		t.Fatal(err)
	}
	if left, _ := db.ListAPITokens(""); len(left) != 1 || left[0].Username != "bob" {
		t.Fatalf("silinen kullanıcının token'ları kalmamalı: %+v", left)
	}
}

func TestAPITokenSchema_IdempotentBaseline(t *testing.T) {
	db := newTestDB(t)
	for i := 0; i < 2; i++ {
		if err := migrateBaseline(db.conn); err != nil {
			t.Fatal(err)
		}
	}
	// token_hash benzersiz: aynı özet iki kez eklenemez
	if _, err := db.conn.Exec(`INSERT INTO api_tokens (username, name, prefix, token_hash) VALUES ('a','n','kh_x','h1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.conn.Exec(`INSERT INTO api_tokens (username, name, prefix, token_hash) VALUES ('b','n','kh_y','h1')`); err == nil {
		t.Fatal("yinelenen token_hash kabul edildi")
	}
	if _, err := db.conn.Exec(`INSERT INTO api_tokens (username, name, prefix, token_hash, scope) VALUES ('a','n','kh_x','h2','root')`); err == nil {
		t.Fatal("geçersiz kapsam CHECK ile reddedilmeli")
	}
}

func TestCreateAPITokenLimited_AtomicUnderConcurrency(t *testing.T) {
	db := newTestDB(t)
	u, err := db.CreateUser("lim", "x", RoleAnalyst)
	if err != nil {
		t.Fatal(err)
	}
	// Süresi dolmuş ve iptal edilmiş token'lar sınıra sayılmaz
	past := time.Now().Add(-time.Hour)
	if _, _, err := db.CreateAPITokenLimited(u.ID, u.Username, "old", TokenScopeRead, &past, 3); err != nil {
		t.Fatal(err)
	}
	_, rv, _ := db.CreateAPITokenLimited(u.ID, u.Username, "rv", TokenScopeRead, nil, 3)
	if _, err := db.RevokeAPIToken(rv.ID); err != nil {
		t.Fatal(err)
	}
	const max, n = 3, 40
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, limited := 0, 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := db.CreateAPITokenLimited(u.ID, u.Username, "c", TokenScopeRead, nil, max)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, ErrTokenLimit):
				limited++
			default:
				t.Errorf("beklenmeyen hata: %v", err)
			}
		}()
	}
	wg.Wait()
	if ok != max || limited != n-max {
		t.Fatalf("ok=%d limited=%d", ok, limited)
	}
	if c, _ := db.CountActiveAPITokens("lim", time.Now()); c != max {
		t.Fatalf("etkin: %d", c)
	}
	// Sınır 0 = sınırsız (CreateAPIToken)
	if _, _, err := db.CreateAPIToken(u.ID, u.Username, "extra", TokenScopeRead, nil); err != nil {
		t.Fatalf("sınırsız oluşturma: %v", err)
	}
}

func TestRevokeUserAPITokens_ByAccountID(t *testing.T) {
	db := newTestDB(t)
	a, _ := db.CreateUser("ra", "x", RoleAnalyst)
	b, _ := db.CreateUser("rb", "x", RoleAnalyst)
	for i := 0; i < 3; i++ {
		if _, _, err := db.CreateAPIToken(a.ID, a.Username, "a", TokenScopeRead, nil); err != nil {
			t.Fatal(err)
		}
	}
	_, bt, _ := db.CreateAPIToken(b.ID, b.Username, "b", TokenScopeRead, nil)
	n, err := db.RevokeUserAPITokens(a.ID)
	if err != nil || n != 3 {
		t.Fatalf("iptal: %d %v", n, err)
	}
	if n, _ := db.RevokeUserAPITokens(a.ID); n != 0 {
		t.Fatalf("ikinci çağrı değişiklik yapmamalı: %d", n)
	}
	if got, _ := db.GetAPIToken(bt.ID); got.RevokedAt != nil {
		t.Fatal("başka kullanıcının token'ı etkilenmemeli")
	}
}

func TestAPITokens_PruneAndPurge(t *testing.T) {
	db := newTestDB(t)
	u, _ := db.CreateUser("pr", "x", RoleAnalyst)
	other, _ := db.CreateUser("po", "x", RoleAnalyst)
	now := time.Now()
	var ids []int64
	for i := 0; i < 6; i++ {
		_, tk, err := db.CreateAPIToken(u.ID, u.Username, "r", TokenScopeRead, nil)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, tk.ID)
		if _, err := db.RevokeAPIToken(tk.ID); err != nil {
			t.Fatal(err)
		}
	}
	past := now.Add(-time.Hour)
	_, exp, _ := db.CreateAPIToken(u.ID, u.Username, "expired", TokenScopeRead, &past)
	_, live, _ := db.CreateAPIToken(u.ID, u.Username, "live", TokenScopeRead, nil)
	_, oth, _ := db.CreateAPIToken(other.ID, other.Username, "other", TokenScopeRead, nil)
	_, _ = db.RevokeAPIToken(oth.ID)

	n, err := db.PruneUserInactiveAPITokens(u.ID, 2, now)
	if err != nil || n != 5 {
		t.Fatalf("budama: %d %v", n, err)
	}
	left, _ := db.ListAPITokens("pr")
	got := map[int64]bool{}
	for _, tk := range left {
		got[tk.ID] = true
	}
	// Kalan: etkin + en yeni 2 etkin olmayan (expired ve son iptal)
	if len(left) != 3 || !got[live.ID] || !got[exp.ID] || !got[ids[5]] {
		t.Fatalf("budama sonrası: %+v", left)
	}
	if _, err := db.GetAPIToken(oth.ID); err != nil {
		t.Fatal("başka kullanıcının satırı budanmamalı")
	}

	// Saklama süresi: iptal anı/bitişi eşikten eski olanlar silinir; etkin olan kalır
	if n, err := db.PurgeStaleAPITokens(now.Add(-24 * time.Hour)); err != nil || n != 0 {
		t.Fatalf("taze satırlar silinmemeli: %d %v", n, err)
	}
	if n, err := db.PurgeStaleAPITokens(now.Add(24 * time.Hour)); err != nil || n != 3 {
		t.Fatalf("eski etkin olmayanlar silinmeli: %d %v", n, err)
	}
	if _, err := db.GetAPIToken(live.ID); err != nil {
		t.Fatal("etkin token silinmemeli")
	}
}
