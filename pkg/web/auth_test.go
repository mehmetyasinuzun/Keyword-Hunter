package web

import (
	"testing"
	"time"
)

func TestCredentialStore_VerifyAndUpdate(t *testing.T) {
	cs, err := newCredentialStore("admin", "s3cret", "")
	if err != nil {
		t.Fatal(err)
	}
	if !cs.Verify("admin", "s3cret") {
		t.Fatal("doğru bilgiler reddedildi")
	}
	if cs.Verify("admin", "wrong") || cs.Verify("root", "s3cret") {
		t.Fatal("yanlış bilgiler kabul edildi")
	}
	hash, err := cs.Update("analyst", "newpass")
	if err != nil || hash == "" {
		t.Fatalf("update: %v", err)
	}
	if cs.Verify("admin", "s3cret") || !cs.Verify("analyst", "newpass") {
		t.Fatal("güncelleme anında uygulanmadı")
	}
	// Hash ile yeniden yükleme
	cs2, err := newCredentialStore("analyst", "", hash)
	if err != nil || !cs2.Verify("analyst", "newpass") {
		t.Fatalf("hash ile başlatma başarısız: %v", err)
	}
}

func TestLoginGuard_LocksAfterRepeatedFailures(t *testing.T) {
	g := newLoginGuard()
	now := time.Now()
	g.now = func() time.Time { return now }

	for i := 0; i < loginMaxFailures-1; i++ {
		g.Fail("1.2.3.4")
	}
	if blocked, _ := g.Blocked("1.2.3.4"); blocked {
		t.Fatal("eşik altında kilit olmamalı")
	}
	g.Fail("1.2.3.4")
	blocked, wait := g.Blocked("1.2.3.4")
	if !blocked || wait <= 0 {
		t.Fatal("eşikte kilitlenmeli")
	}
	if b, _ := g.Blocked("5.6.7.8"); b {
		t.Fatal("başka IP etkilenmemeli")
	}
	// Kilit süresi dolunca açılmalı
	now = now.Add(loginBaseLock + time.Second)
	if b, _ := g.Blocked("1.2.3.4"); b {
		t.Fatal("kilit süresi dolunca açılmalı")
	}
	// Başarılı giriş sayacı sıfırlar
	g.Success("1.2.3.4")
	g.Fail("1.2.3.4")
	if b, _ := g.Blocked("1.2.3.4"); b {
		t.Fatal("sıfırlama sonrası tek hata kilitlememeli")
	}
}

func TestLockDuration_Exponential(t *testing.T) {
	if lockDuration(0) != loginBaseLock || lockDuration(1) != 2*loginBaseLock {
		t.Fatal("üstel artış beklenen gibi değil")
	}
	if lockDuration(100) != loginMaxLock {
		t.Fatal("üst sınır aşıldı")
	}
}
