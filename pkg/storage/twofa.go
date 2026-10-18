package storage

import (
	"database/sql"
	"errors"
	"time"
)

// ─────────────────────── İki adımlı doğrulama (TOTP) ───────────────────────
//
// user_2fa kullanıcı başına tek satırdır. Sırlar (etkin ve kayıt sürecindeki
// "bekleyen") yalnız şifreli (AES-256-GCM, APP_SECRET'tan türetilen anahtar)
// saklanır; şifreleme/çözme web katmanındadır, depo yalnız opak metin görür.
// last_step yeniden oynatma korumasıdır: kabul edilen son TOTP adımı; aynı ya
// da daha eski bir adımın kodu ikinci kez kabul edilmez (atomik CAS ile).
// Kurtarma kodlarından yalnız kullanıcıya bağlı SHA-256 özeti tutulur.

// ErrTwoFactorState işlem sırasında 2FA durumu değişti (ör. bekleyen sır
// başka bir sekmede yenilendi ya da 2FA zaten etkin).
var ErrTwoFactorState = errors.New("2FA durumu değişti")

// TwoFactor bir kullanıcının 2FA kaydı.
type TwoFactor struct {
	UserID     int64
	Enabled    bool
	SecretEnc  string // şifreli etkin sır
	PendingEnc string // şifreli bekleyen sır (kayıt sürüyor)
	PendingAt  *time.Time
	LastStep   int64
	EnabledAt  *time.Time
}

// TwoFactorStatus kullanıcı listesi için özet (sır içermez).
type TwoFactorStatus struct {
	Enabled           bool       `json:"enabled"`
	EnabledAt         *time.Time `json:"enabledAt,omitempty"`
	RecoveryRemaining int        `json:"recoveryRemaining"`
}

// GetTwoFactor kullanıcının 2FA kaydını döndürür; kayıt yoksa etkin olmayan
// boş bir değer (hata değil).
func (db *DB) GetTwoFactor(userID int64) (*TwoFactor, error) {
	tf := &TwoFactor{UserID: userID}
	var enabled int
	var pendingAt, enabledAt sql.NullTime
	err := db.conn.QueryRow(`
		SELECT enabled, secret_enc, pending_enc, pending_at, last_step, enabled_at
		FROM user_2fa WHERE user_id = ?`, userID).
		Scan(&enabled, &tf.SecretEnc, &tf.PendingEnc, &pendingAt, &tf.LastStep, &enabledAt)
	if errors.Is(err, sql.ErrNoRows) {
		return tf, nil
	}
	if err != nil {
		return nil, err
	}
	tf.Enabled = enabled == 1
	if pendingAt.Valid {
		tf.PendingAt = &pendingAt.Time
	}
	if enabledAt.Valid {
		tf.EnabledAt = &enabledAt.Time
	}
	return tf, nil
}

// TwoFactorEnabled kullanıcıda 2FA etkin mi (zorunluluk denetimi için hafif sorgu).
func (db *DB) TwoFactorEnabled(userID int64) (bool, error) {
	var n int
	err := db.conn.QueryRow(`SELECT COUNT(*) FROM user_2fa WHERE user_id = ? AND enabled = 1`, userID).Scan(&n)
	return n > 0, err
}

// SetTwoFactorPending kayıt sürecindeki (henüz doğrulanmamış) şifreli sırrı
// yazar. 2FA zaten etkinse ErrTwoFactorState döner: etkin sır bu yolla
// değiştirilemez (önce kapatılmalı).
func (db *DB) SetTwoFactorPending(userID int64, pendingEnc string, now time.Time) error {
	res, err := db.conn.Exec(`
		INSERT INTO user_2fa (user_id, pending_enc, pending_at, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET pending_enc = excluded.pending_enc, pending_at = excluded.pending_at, updated_at = excluded.updated_at
		WHERE user_2fa.enabled = 0`, userID, pendingEnc, now.UTC(), now.UTC())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrTwoFactorState
	}
	return nil
}

// EnableTwoFactor bekleyen sırrı etkin sır yapar, kabul edilen adımı son adım
// olarak kaydeder ve kurtarma kodlarını (özetleri) tek işlemde değiştirir.
// expectPending doğrulanan bekleyen sırrın ta kendisi olmalıdır: araya giren
// yeni bir "kurulum" isteği eski kodla yeni sırrın etkinleşmesine yol açamaz.
func (db *DB) EnableTwoFactor(userID int64, expectPending string, step int64, codeHashes []string, now time.Time) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`
		UPDATE user_2fa SET enabled = 1, secret_enc = pending_enc, pending_enc = '', pending_at = NULL,
			last_step = ?, enabled_at = ?, updated_at = ?
		WHERE user_id = ? AND enabled = 0 AND pending_enc <> '' AND pending_enc = ?`,
		step, now.UTC(), now.UTC(), userID, expectPending)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrTwoFactorState
	}
	if err := replaceRecoveryCodesTx(tx, userID, codeHashes, now); err != nil {
		return err
	}
	return tx.Commit()
}

// AdvanceTwoFactorStep kabul edilen TOTP adımını atomik olarak kaydeder:
// yalnız adım son kaydedilenden büyükse günceller (karşılaştır-ve-yaz).
// false dönerse adım zaten kullanılmıştır (eşzamanlı iki istekte aynı kod
// yalnız birinde kabul edilir) ya da 2FA artık etkin değildir.
func (db *DB) AdvanceTwoFactorStep(userID, step int64) (bool, error) {
	res, err := db.conn.Exec(`UPDATE user_2fa SET last_step = ?, updated_at = ? WHERE user_id = ? AND enabled = 1 AND last_step < ?`,
		step, time.Now().UTC(), userID, step)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// DisableTwoFactor kullanıcının 2FA kaydını ve tüm kurtarma kodlarını siler.
// Kayıt yoksa false döner.
func (db *DB) DisableTwoFactor(userID int64) (bool, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`DELETE FROM user_2fa WHERE user_id = ?`, userID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if _, err := tx.Exec(`DELETE FROM user_recovery_codes WHERE user_id = ?`, userID); err != nil {
		return false, err
	}
	return n > 0, tx.Commit()
}

// ReplaceRecoveryCodes kullanıcının kurtarma kodlarını yenileriyle değiştirir
// (eski kodların tümü — kullanılmamış olanlar dahil — geçersiz olur). 2FA etkin
// değilse ErrTwoFactorState.
func (db *DB) ReplaceRecoveryCodes(userID int64, codeHashes []string, now time.Time) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM user_2fa WHERE user_id = ? AND enabled = 1`, userID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrTwoFactorState
	}
	if err := replaceRecoveryCodesTx(tx, userID, codeHashes, now); err != nil {
		return err
	}
	return tx.Commit()
}

func replaceRecoveryCodesTx(tx *sql.Tx, userID int64, codeHashes []string, now time.Time) error {
	if _, err := tx.Exec(`DELETE FROM user_recovery_codes WHERE user_id = ?`, userID); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO user_recovery_codes (user_id, code_hash, created_at) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, h := range codeHashes {
		if _, err := stmt.Exec(userID, h, now.UTC()); err != nil {
			return err
		}
	}
	return nil
}

// UseRecoveryCode kullanılmamış bir kurtarma kodunu tek seferlik tüketir
// (atomik: eşzamanlı iki istekte yalnız biri başarılı olur). 2FA etkin değilse
// kod kabul edilmez.
func (db *DB) UseRecoveryCode(userID int64, codeHash string, now time.Time) (bool, error) {
	res, err := db.conn.Exec(`
		UPDATE user_recovery_codes SET used_at = ?
		WHERE user_id = ? AND code_hash = ? AND used_at IS NULL
		  AND EXISTS (SELECT 1 FROM user_2fa WHERE user_id = ? AND enabled = 1)`,
		now.UTC(), userID, codeHash, userID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// CountRecoveryCodes kullanılmamış kurtarma kodu sayısı.
func (db *DB) CountRecoveryCodes(userID int64) (int, error) {
	var n int
	err := db.conn.QueryRow(`SELECT COUNT(*) FROM user_recovery_codes WHERE user_id = ? AND used_at IS NULL`, userID).Scan(&n)
	return n, err
}

// ListTwoFactorStatus tüm kullanıcıların 2FA özetini kullanıcı kimliğine göre
// döndürür (yalnız kaydı olanlar; etkin olmayan bekleyen kayıtlar dahil değil).
func (db *DB) ListTwoFactorStatus() (map[int64]TwoFactorStatus, error) {
	rows, err := db.conn.Query(`
		SELECT f.user_id, f.enabled_at,
			(SELECT COUNT(*) FROM user_recovery_codes r WHERE r.user_id = f.user_id AND r.used_at IS NULL)
		FROM user_2fa f WHERE f.enabled = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]TwoFactorStatus)
	for rows.Next() {
		var id int64
		var at sql.NullTime
		var remaining int
		if err := rows.Scan(&id, &at, &remaining); err != nil {
			return nil, err
		}
		st := TwoFactorStatus{Enabled: true, RecoveryRemaining: remaining}
		if at.Valid {
			t := at.Time
			st.EnabledAt = &t
		}
		out[id] = st
	}
	return out, rows.Err()
}
