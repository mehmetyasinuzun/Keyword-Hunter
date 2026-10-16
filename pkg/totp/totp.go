// Package totp RFC 6238 (TOTP) ve RFC 4226 (HOTP) tek seferlik parolalarını
// üretir ve doğrular; TOTP sırlarının veritabanında AES-256-GCM ile şifreli
// saklanması için küçük bir mühürleyici (Sealer) sağlar.
//
// Parametreler kimlik doğrulayıcı uygulamalarının (Google Authenticator,
// Authy, 1Password, Aegis…) varsayılanlarıdır: HMAC-SHA1, 6 hane, 30 sn adım.
// Doğrulama ±1 adım saat kaymasını tolere eder; kabul edilen adım çağırana
// döndürülür ki aynı kod (ya da daha eski bir adım) ikinci kez kabul edilmesin
// (yeniden oynatma koruması çağıranın kalıcı "son adım" kaydıyla yapılır).
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- RFC 6238/4226 HMAC-SHA1; kimlik doğrulayıcı uygulamalarının ortak paydası (HMAC'te SHA-1 çakışmaları istismar edilemez)
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// Digits üretilen kodun hane sayısı.
	Digits = 6
	// Period bir zaman adımının süresi.
	Period = 30 * time.Second
	// Skew doğrulamada şimdiki adımın iki yanında kabul edilen adım sayısı.
	Skew = 1
	// SecretSize üretilen sırrın bayt uzunluğu (RFC 4226 §4: en az 128, önerilen 160 bit).
	SecretSize = 20
)

// ErrInvalidSecret sır base32 olarak çözülemedi ya da çok kısa.
var ErrInvalidSecret = errors.New("totp: geçersiz sır")

// b32 dolgusuz büyük harf base32 (otpauth:// URI'lerinin beklediği biçim).
var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateSecret crypto/rand ile 20 baytlık yeni bir sır üretir ve dolgusuz
// base32 olarak döndürür (32 karakter).
func GenerateSecret() (string, error) {
	buf := make([]byte, SecretSize)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return b32.EncodeToString(buf), nil
}

// DecodeSecret kullanıcıdan/depodan gelen base32 sırrı baytlara çevirir:
// boşluk ve tireler yok sayılır, büyük/küçük harf duyarsızdır, dolgu
// karakterleri kabul edilir. 80 bitten (10 bayt) kısa sırlar reddedilir.
func DecodeSecret(secret string) ([]byte, error) {
	s := strings.ToUpper(strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '\t', '=':
			return -1
		}
		return r
	}, secret))
	key, err := b32.DecodeString(s)
	if err != nil || len(key) < 10 {
		return nil, ErrInvalidSecret
	}
	return key, nil
}

// FormatSecret sırrı elle girişte kolay okunsun diye 4'erli gruplar
// ("ABCD EFGH …"). Kimlik doğrulayıcı uygulamaları boşlukları yok sayar.
func FormatSecret(secret string) string {
	var b strings.Builder
	for i, r := range secret {
		if i > 0 && i%4 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Step verilen anın zaman adımını (Unix zamanı / 30) döndürür.
func Step(t time.Time) int64 {
	return t.Unix() / int64(Period/time.Second)
}

// hotp RFC 4226 §5.3: HMAC-SHA1(key, sayaç) → dinamik kesme → 10^digits modu.
func hotp(key []byte, counter uint64, digits int) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	mod := uint32(1)
	for i := 0; i < digits; i++ {
		mod *= 10
	}
	code := strconv.FormatUint(uint64(bin%mod), 10)
	if pad := digits - len(code); pad > 0 {
		code = strings.Repeat("0", pad) + code
	}
	return code
}

// CodeAt sırrın verilen andaki 6 haneli kodunu üretir (testler ve tanılama).
func CodeAt(key []byte, t time.Time) string {
	return hotp(key, uint64(Step(t)), Digits)
}

// NormalizeCode kullanıcı girdisindeki boşluk/tireyi atar ("123 456" → "123456").
func NormalizeCode(code string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '\t':
			return -1
		}
		return r
	}, strings.TrimSpace(code))
}

// ValidCodeFormat kodun tam olarak 6 ASCII rakamı olup olmadığını söyler.
func ValidCodeFormat(code string) bool {
	if len(code) != Digits {
		return false
	}
	for i := 0; i < len(code); i++ {
		if code[i] < '0' || code[i] > '9' {
			return false
		}
	}
	return true
}

// Verify kodu now anındaki adım ±Skew penceresinde doğrular ve eşleşen adımı
// döndürür. lastStep'ten küçük ya da ona eşit adımlar (zaten kullanılmış ya da
// daha eski) kabul edilmez: yeniden oynatma koruması. Pencerenin tüm adımları
// her zaman hesaplanır ve sabit-zamanlı karşılaştırılır (erken çıkış yok).
func Verify(key []byte, code string, now time.Time, lastStep int64) (int64, bool) {
	code = NormalizeCode(code)
	if !ValidCodeFormat(code) || len(key) == 0 {
		return 0, false
	}
	cur := Step(now)
	matched := int64(-1)
	for d := int64(-Skew); d <= Skew; d++ {
		st := cur + d
		if st < 0 {
			continue
		}
		eq := subtle.ConstantTimeCompare([]byte(hotp(key, uint64(st), Digits)), []byte(code)) == 1
		// En yeni eşleşen adım tutulur (aynı kodun iki adımda çıkması çok nadirdir).
		if eq && st > lastStep {
			matched = st
		}
	}
	if matched < 0 {
		return 0, false
	}
	return matched, true
}

// URI kimlik doğrulayıcı uygulamalarının QR koddan okuduğu otpauth:// adresini
// üretir (Key Uri Format): otpauth://totp/Issuer:hesap?secret=…&issuer=…
func URI(issuer, account, secret string) string {
	label := url.PathEscape(issuer) + ":" + url.PathEscape(account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", strconv.Itoa(Digits))
	q.Set("period", strconv.Itoa(int(Period/time.Second)))
	return "otpauth://totp/" + label + "?" + q.Encode()
}
