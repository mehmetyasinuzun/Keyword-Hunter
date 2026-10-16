package totp

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// Kurtarma kodları: kimlik doğrulayıcı cihazı kaybedildiğinde girişte bir kez
// kullanılabilen yedek kodlar. Biçim "xxxx-xxxx"; alfabe karışan karakterleri
// (1/l/i, o) içermeyen 32 simgedir → kod başına 40 bit. Veritabanında yalnız
// kullanıcıya bağlı SHA-256 özeti saklanır.
const (
	// RecoveryCodeCount üretilen kod sayısı.
	RecoveryCodeCount = 10
	recoveryAlphabet  = "023456789abcdefghjkmnpqrstuvwxyz" // 32 simge: bayt&31 sapmasız
	recoveryHalf      = 4
)

// GenerateRecoveryCodes n adet benzersiz "xxxx-xxxx" kodu üretir (crypto/rand).
func GenerateRecoveryCodes(n int) ([]string, error) {
	out := make([]string, 0, n)
	seen := make(map[string]bool, n)
	buf := make([]byte, 2*recoveryHalf)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			return nil, err
		}
		var b strings.Builder
		for i, v := range buf {
			if i == recoveryHalf {
				b.WriteByte('-')
			}
			b.WriteByte(recoveryAlphabet[v&31])
		}
		code := b.String()
		if !seen[code] {
			seen[code] = true
			out = append(out, code)
		}
	}
	return out, nil
}

// NormalizeRecoveryCode kullanıcı girdisini karşılaştırma biçimine indirger:
// küçük harf, boşluk/tire yok. Geçerli biçimde değilse "" döner.
func NormalizeRecoveryCode(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	if len(code) > 32 {
		return ""
	}
	var b strings.Builder
	for _, r := range code {
		switch {
		case r == '-' || r == ' ':
			continue
		case r < 128 && strings.IndexByte(recoveryAlphabet, byte(r)) >= 0:
			b.WriteRune(r)
		default:
			return ""
		}
	}
	if b.Len() != 2*recoveryHalf {
		return ""
	}
	return b.String()
}

// HashRecoveryCode normalize edilmiş kodun kullanıcıya bağlı SHA-256 özetini
// (hex) döndürür. Kullanıcı kimliği tuz görevi görür: aynı kod iki kullanıcıda
// farklı özet üretir, önceden hesaplanmış tablo tüm kullanıcılara uygulanamaz.
func HashRecoveryCode(userID int64, normalized string) string {
	sum := sha256.Sum256([]byte("keywordhunter/recovery/v1:" + strconv.FormatInt(userID, 10) + ":" + normalized))
	return hex.EncodeToString(sum[:])
}
