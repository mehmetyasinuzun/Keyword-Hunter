package totp

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

// AppSecretLen APP_SECRET'ın onaltılık (hex) karakter uzunluğu (32 bayt).
const AppSecretLen = 64

// sealPrefix şifreli değerin biçim sürümü; ileride anahtar/algoritma
// döndürülürse eski kayıtlar ayırt edilebilir.
const sealPrefix = "v1:"

// hkdfInfo APP_SECRET'tan türetilen alt anahtarın amaç etiketi (alan ayrımı):
// aynı ana sır ileride başka bir amaçla kullanılırsa anahtarlar çakışmaz.
const hkdfInfo = "keywordhunter/totp-secret/aes-256-gcm/v1"

// PurposeIntegrationSecret entegrasyon sırlarını (ör. MISP API anahtarı)
// şifreleyen alt anahtarın amaç etiketi; TOTP anahtarından bağımsızdır.
const PurposeIntegrationSecret = "keywordhunter/integration-secret/aes-256-gcm/v1"

var (
	// ErrInvalidAppSecret APP_SECRET 64 onaltılık karakter değil.
	ErrInvalidAppSecret = errors.New("APP_SECRET 64 onaltılık (hex) karakter olmalı")
	// ErrSealedValue şifreli değer çözülemedi (bozuk, başka anahtarla ya da başka kullanıcı için mühürlenmiş).
	ErrSealedValue = errors.New("totp: şifreli değer çözülemedi")
)

// ValidAppSecret APP_SECRET biçimini doğrular (64 hex karakter).
func ValidAppSecret(s string) bool {
	if len(s) != AppSecretLen {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// GenerateAppSecret crypto/rand ile yeni bir APP_SECRET (32 bayt, hex) üretir.
func GenerateAppSecret() (string, error) {
	buf := make([]byte, AppSecretLen/2)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// Sealer TOTP sırlarını AES-256-GCM ile şifreler. Her değer için rastgele
// 96 bit nonce kullanılır; ek doğrulanmış veri (AAD) olarak kullanıcıya özgü
// bir bağlam verilir ki bir kullanıcının şifreli sırrı veritabanında başka bir
// kullanıcının satırına kopyalanırsa çözülemesin.
type Sealer struct {
	aead cipher.AEAD
}

// NewSealer APP_SECRET'tan (64 hex) HKDF-SHA256 ile türetilen anahtarla bir
// mühürleyici kurar.
func NewSealer(appSecretHex string) (*Sealer, error) {
	return NewSealerFor(appSecretHex, hkdfInfo)
}

// NewSealerFor aynı APP_SECRET'tan farklı bir amaç etiketiyle (HKDF info)
// türetilmiş, birbirinden bağımsız bir mühürleyici kurar: bir amacın şifreli
// değeri başka bir amacın anahtarıyla çözülemez.
func NewSealerFor(appSecretHex, purpose string) (*Sealer, error) {
	if strings.TrimSpace(purpose) == "" {
		return nil, errors.New("totp: mühürleyici amacı boş olamaz")
	}
	appSecretHex = strings.TrimSpace(appSecretHex)
	if !ValidAppSecret(appSecretHex) {
		return nil, ErrInvalidAppSecret
	}
	master, _ := hex.DecodeString(appSecretHex)
	key, err := hkdf.Key(sha256.New, master, nil, purpose, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

// Seal düz metni şifreler: "v1:" + base64url(nonce || şifreli metin || etiket).
func (s *Sealer) Seal(plaintext, context string) (string, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := s.aead.Seal(nonce, nonce, []byte(plaintext), []byte(context))
	return sealPrefix + base64.RawURLEncoding.EncodeToString(out), nil
}

// Open Seal çıktısını aynı bağlamla çözer.
func (s *Sealer) Open(sealed, context string) (string, error) {
	raw, ok := strings.CutPrefix(sealed, sealPrefix)
	if !ok {
		return "", ErrSealedValue
	}
	buf, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(buf) < s.aead.NonceSize()+s.aead.Overhead() {
		return "", ErrSealedValue
	}
	ns := s.aead.NonceSize()
	pt, err := s.aead.Open(nil, buf[:ns], buf[ns:], []byte(context))
	if err != nil {
		return "", ErrSealedValue
	}
	return string(pt), nil
}
