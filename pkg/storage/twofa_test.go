package storage

import (
	"errors"
	"testing"
	"time"
)

func TestTwoFactorLifecycle(t *testing.T) {
	db := newTestDB(t)
	u, err := db.CreateUser("tfa-user", "x", RoleAnalyst)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()

	// Kayıt yok → etkin değil, hata yok
	tf, err := db.GetTwoFactor(u.ID)
	if err != nil || tf.Enabled || tf.PendingEnc != "" {
		t.Fatalf("boş kayıt: %+v %v", tf, err)
	}

	if err := db.SetTwoFactorPending(u.ID, "v1:pending-a", now); err != nil {
		t.Fatal(err)
	}
	// Yeniden kurulum bekleyeni değiştirir (idempotent migrasyon da sorun çıkarmaz)
	if err := db.EnsureTwoFactorSchema(); err != nil {
		t.Fatal(err)
	}
	if err := db.SetTwoFactorPending(u.ID, "v1:pending-b", now); err != nil {
		t.Fatal(err)
	}
	// Eski bekleyenle etkinleştirme reddedilir (araya yeni kurulum girdi)
	if err := db.EnableTwoFactor(u.ID, "v1:pending-a", 100, []string{"h1"}, now); !errors.Is(err, ErrTwoFactorState) {
		t.Fatalf("eski bekleyen sır etkinleşmemeli: %v", err)
	}
	if err := db.EnableTwoFactor(u.ID, "v1:pending-b", 100, []string{"h1", "h2", "h3"}, now); err != nil {
		t.Fatal(err)
	}
	tf, _ = db.GetTwoFactor(u.ID)
	if !tf.Enabled || tf.SecretEnc != "v1:pending-b" || tf.PendingEnc != "" || tf.LastStep != 100 || tf.EnabledAt == nil {
		t.Fatalf("etkin kayıt: %+v", tf)
	}
	if on, _ := db.TwoFactorEnabled(u.ID); !on {
		t.Fatal("TwoFactorEnabled true olmalı")
	}
	// Etkinken bekleyen sır yazılamaz
	if err := db.SetTwoFactorPending(u.ID, "v1:x", now); !errors.Is(err, ErrTwoFactorState) {
		t.Fatalf("etkin 2FA'da kurulum reddedilmeli: %v", err)
	}

	// Adım CAS: yalnız ileri
	for _, c := range []struct {
		step int64
		ok   bool
	}{{100, false}, {99, false}, {101, true}, {101, false}, {103, true}} {
		ok, err := db.AdvanceTwoFactorStep(u.ID, c.step)
		if err != nil || ok != c.ok {
			t.Fatalf("adım %d: %v %v (beklenen %v)", c.step, ok, err, c.ok)
		}
	}

	// Kurtarma kodu tek seferlik
	if n, _ := db.CountRecoveryCodes(u.ID); n != 3 {
		t.Fatalf("kalan kod: %d", n)
	}
	if ok, _ := db.UseRecoveryCode(u.ID, "h2", now); !ok {
		t.Fatal("geçerli kod kabul edilmeli")
	}
	if ok, _ := db.UseRecoveryCode(u.ID, "h2", now); ok {
		t.Fatal("kod ikinci kez kullanılamamalı")
	}
	if ok, _ := db.UseRecoveryCode(u.ID, "nope", now); ok {
		t.Fatal("bilinmeyen kod kabul edildi")
	}
	if n, _ := db.CountRecoveryCodes(u.ID); n != 2 {
		t.Fatalf("kalan kod: %d", n)
	}
	st, err := db.ListTwoFactorStatus()
	if err != nil || !st[u.ID].Enabled || st[u.ID].RecoveryRemaining != 2 {
		t.Fatalf("durum listesi: %+v %v", st, err)
	}

	// Yenileme eskileri (kullanılmamışlar dahil) geçersiz kılar
	if err := db.ReplaceRecoveryCodes(u.ID, []string{"n1", "n2"}, now); err != nil {
		t.Fatal(err)
	}
	if ok, _ := db.UseRecoveryCode(u.ID, "h1", now); ok {
		t.Fatal("yenilemeden sonra eski kod geçerli kalmamalı")
	}

	// Kapatma: kayıt + kodlar silinir; kod artık kabul edilmez
	if ok, err := db.DisableTwoFactor(u.ID); !ok || err != nil {
		t.Fatalf("kapatma: %v %v", ok, err)
	}
	if ok, _ := db.UseRecoveryCode(u.ID, "n1", now); ok {
		t.Fatal("2FA kapalıyken kurtarma kodu kabul edildi")
	}
	if err := db.ReplaceRecoveryCodes(u.ID, []string{"z"}, now); !errors.Is(err, ErrTwoFactorState) {
		t.Fatalf("2FA kapalıyken kod üretilmemeli: %v", err)
	}
	if ok, _ := db.AdvanceTwoFactorStep(u.ID, 500); ok {
		t.Fatal("2FA kapalıyken adım ilerlememeli")
	}
	if ok, _ := db.DisableTwoFactor(u.ID); ok {
		t.Fatal("ikinci kapatma değişiklik yapmamalı")
	}
}

func TestTwoFactorDeletedWithUser(t *testing.T) {
	db := newTestDB(t)
	u, _ := db.CreateUser("tfa-del", "x", RoleViewer)
	now := time.Now()
	if err := db.SetTwoFactorPending(u.ID, "v1:p", now); err != nil {
		t.Fatal(err)
	}
	if err := db.EnableTwoFactor(u.ID, "v1:p", 1, []string{"a", "b"}, now); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteUser(u.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = db.conn.QueryRow(`SELECT (SELECT COUNT(*) FROM user_2fa) + (SELECT COUNT(*) FROM user_recovery_codes)`).Scan(&n)
	if n != 0 {
		t.Fatalf("silinen kullanıcının 2FA artıkları kaldı: %d", n)
	}
	// Aynı adla yeniden açılan hesap eski 2FA'yı devralmaz
	u2, _ := db.CreateUser("tfa-del", "x", RoleViewer)
	if on, _ := db.TwoFactorEnabled(u2.ID); on {
		t.Fatal("yeni hesap eski 2FA'yı devraldı")
	}
}
