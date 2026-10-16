package storage

// Kişisel API token'ları: SOAR/SIEM/betiklerin tarayıcı oturumu olmadan
// /api/ uçlarını çağırabilmesi için.
//
// Güvenlik modeli:
//   - Token biçimi "kh_" + 43 karakter base62 (crypto/rand, ≈256 bit). Dolgu
//     yoktur; kopyala-yapıştır ve başlıkta taşıma güvenlidir.
//   - Düz metin ASLA saklanmaz: yalnız SHA-256 özeti (hex) tutulur. Token
//     yüksek entropili olduğundan yavaş (bcrypt) özet gerekmez; özet doğrudan
//     benzersiz indeksle aranır.
//   - prefix (ilk 10 karakter) yalnız arayüzde ve denetim kaydında tanımak
//     içindir; tek başına kimlik doğrulamaz.
//   - Token, oluşturulduğu kullanıcının hem adına hem kimliğine (user_id)
//     bağlıdır: kullanıcı silinip aynı adla yeniden açılsa bile eski token
//     yeni hesaba geçmez.

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// Token kapsamları.
const (
	TokenScopeRead  = "read"  // yalnız okuma (viewer eşdeğeri)
	TokenScopeWrite = "write" // kullanıcının rolü kadar yazma
)

const (
	// APITokenPrefix tüm token'ların sabit öneki (sızıntı tarayıcıları için tanınabilir).
	APITokenPrefix = "kh_"
	// APITokenDisplayLen arayüzde gösterilen önek uzunluğu.
	APITokenDisplayLen = 10
	// apiTokenRandLen öndeki "kh_" hariç base62 karakter sayısı: 43 × log2(62) ≈ 256 bit.
	apiTokenRandLen = 43
	// MaxAPITokenLen başlıktan kabul edilen en uzun token (kötü niyetli dev girdiyi erken reddetmek için).
	MaxAPITokenLen = 128
)

const base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// Token durumları (hesaplanır, saklanmaz).
const (
	TokenStatusActive  = "active"
	TokenStatusExpired = "expired"
	TokenStatusRevoked = "revoked"
)

// ErrTokenNotFound token yok.
var ErrTokenNotFound = errors.New("token bulunamadı")

// APIToken kişisel API token kaydı (düz metin içermez).
type APIToken struct {
	ID         int64      `json:"id"`
	UserID     int64      `json:"-"`
	Username   string     `json:"username"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	TokenHash  string     `json:"-"`
	Scope      string     `json:"scope"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	LastUsedIP string     `json:"lastUsedIp"`
	ExpiresAt  *time.Time `json:"expiresAt"`
	RevokedAt  *time.Time `json:"revokedAt"`
}

// Status token'ın verilen andaki durumunu döndürür.
func (t *APIToken) Status(now time.Time) string {
	switch {
	case t.RevokedAt != nil:
		return TokenStatusRevoked
	case t.ExpiresAt != nil && !now.Before(*t.ExpiresAt):
		return TokenStatusExpired
	default:
		return TokenStatusActive
	}
}

// ValidTokenScope kapsamın geçerli olup olmadığını döndürür.
func ValidTokenScope(s string) bool {
	return s == TokenScopeRead || s == TokenScopeWrite
}

// HashAPIToken düz metin token'ın saklanan özetini (hex SHA-256) üretir.
func HashAPIToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// LooksLikeAPIToken biçim denetimi: "kh_" öneki, sınırlı uzunluk ve yalnız
// base62 karakterler. DB'ye gitmeden önce bariz çöpü eler.
func LooksLikeAPIToken(s string) bool {
	if len(s) <= len(APITokenPrefix) || len(s) > MaxAPITokenLen || !strings.HasPrefix(s, APITokenPrefix) {
		return false
	}
	for i := len(APITokenPrefix); i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z') {
			return false
		}
	}
	return true
}

// generateAPIToken crypto/rand ile yeni bir düz metin token üretir. Modulo
// yanlılığını önlemek için ret örneklemesi kullanılır (248 = 4×62).
func generateAPIToken() (string, error) {
	var b strings.Builder
	b.Grow(len(APITokenPrefix) + apiTokenRandLen)
	b.WriteString(APITokenPrefix)
	buf := make([]byte, 64)
	n := 0
	for n < apiTokenRandLen {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, x := range buf {
			if x >= 248 {
				continue
			}
			b.WriteByte(base62Alphabet[x%62])
			n++
			if n == apiTokenRandLen {
				break
			}
		}
	}
	return b.String(), nil
}

// EnsureAPITokenSchema api_tokens tablosunu oluşturur (idempotent).
func (db *DB) EnsureAPITokenSchema() error {
	_, err := db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS api_tokens (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL DEFAULT 0,
			username TEXT NOT NULL COLLATE NOCASE,
			name TEXT NOT NULL,
			prefix TEXT NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			scope TEXT NOT NULL DEFAULT 'read' CHECK (scope IN ('read', 'write')),
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			last_used_at DATETIME,
			last_used_ip TEXT NOT NULL DEFAULT '',
			expires_at DATETIME,
			revoked_at DATETIME
		);
		CREATE INDEX IF NOT EXISTS idx_api_tokens_username ON api_tokens(username);
	`)
	return err
}

// ErrTokenLimit kullanıcının etkin token sınırına ulaşıldı.
var ErrTokenLimit = errors.New("etkin token sınırına ulaşıldı")

// CreateAPIToken kullanıcı için yeni token üretir ve yalnız özetini kaydeder.
// Düz metin token yalnız bu çağrının dönüş değerindedir; bir daha elde edilemez.
func (db *DB) CreateAPIToken(userID int64, username, name, scope string, expiresAt *time.Time) (string, *APIToken, error) {
	return db.CreateAPITokenLimited(userID, username, name, scope, expiresAt, 0)
}

// CreateAPITokenLimited CreateAPIToken gibidir; maxActive > 0 ise kullanıcının
// (user_id) etkin token sayısı sınıra ulaştığında ErrTokenLimit döner. Sayım ve
// ekleme TEK bir INSERT … SELECT … WHERE ifadesidir: eşzamanlı istekler ayrı
// "say, sonra ekle" adımları arasında yarışıp sınırı aşamaz.
func (db *DB) CreateAPITokenLimited(userID int64, username, name, scope string, expiresAt *time.Time, maxActive int) (string, *APIToken, error) {
	if !ValidTokenScope(scope) {
		return "", nil, errors.New("geçersiz token kapsamı")
	}
	plain, err := generateAPIToken()
	if err != nil {
		return "", nil, err
	}
	now := time.Now().UTC()
	var exp any
	if expiresAt != nil {
		exp = expiresAt.UTC()
	}
	limit := maxActive
	if limit <= 0 {
		limit = -1 // sınırsız: WHERE'deki ilk koşul (? < 0) sayımı devre dışı bırakır
	}
	res, err := db.conn.Exec(`
		INSERT INTO api_tokens (user_id, username, name, prefix, token_hash, scope, created_at, expires_at)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?
		WHERE ? < 0 OR (
			SELECT COUNT(*) FROM api_tokens
			WHERE user_id = ? AND revoked_at IS NULL
			  AND (expires_at IS NULL OR datetime(expires_at) > datetime(?))
		) < ?`,
		userID, strings.TrimSpace(username), name, plain[:APITokenDisplayLen], HashAPIToken(plain), scope, now, exp,
		limit, userID, now, limit)
	if err != nil {
		return "", nil, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return "", nil, err
	} else if n == 0 {
		return "", nil, ErrTokenLimit
	}
	id, err := res.LastInsertId()
	if err != nil {
		return "", nil, err
	}
	t, err := db.GetAPIToken(id)
	if err != nil {
		return "", nil, err
	}
	return plain, t, nil
}

const apiTokenCols = `id, user_id, username, name, prefix, token_hash, scope, created_at, last_used_at, last_used_ip, expires_at, revoked_at`

func scanAPIToken(sc scanner) (*APIToken, error) {
	var t APIToken
	var lastUsed, expires, revoked sql.NullTime
	if err := sc.Scan(&t.ID, &t.UserID, &t.Username, &t.Name, &t.Prefix, &t.TokenHash, &t.Scope,
		&t.CreatedAt, &lastUsed, &t.LastUsedIP, &expires, &revoked); err != nil {
		return nil, err
	}
	if lastUsed.Valid {
		t.LastUsedAt = &lastUsed.Time
	}
	if expires.Valid {
		t.ExpiresAt = &expires.Time
	}
	if revoked.Valid {
		t.RevokedAt = &revoked.Time
	}
	return &t, nil
}

// GetAPIToken token'ı kimliğiyle getirir.
func (db *DB) GetAPIToken(id int64) (*APIToken, error) {
	t, err := scanAPIToken(db.conn.QueryRow(`SELECT `+apiTokenCols+` FROM api_tokens WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTokenNotFound
	}
	return t, err
}

// GetAPITokenByHash token'ı özetiyle getirir (kimlik doğrulama yolu).
func (db *DB) GetAPITokenByHash(hash string) (*APIToken, error) {
	t, err := scanAPIToken(db.conn.QueryRow(`SELECT `+apiTokenCols+` FROM api_tokens WHERE token_hash = ?`, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTokenNotFound
	}
	return t, err
}

// maxListedAPITokens tek bir listelemenin döndürdüğü en fazla satır (yanıt ve
// arayüz sınırlı kalsın; etkin olmayan eski satırlar ayrıca budanır).
const maxListedAPITokens = 1000

// ListAPITokens bir kullanıcının (username boşsa herkesin) token'larını en
// yeni önce döndürür (en fazla maxListedAPITokens).
func (db *DB) ListAPITokens(username string) ([]APIToken, error) {
	q := `SELECT ` + apiTokenCols + ` FROM api_tokens`
	var args []any
	if username != "" {
		q += ` WHERE username = ? COLLATE NOCASE`
		args = append(args, strings.TrimSpace(username))
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, maxListedAPITokens)
	rows, err := db.conn.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]APIToken, 0)
	for rows.Next() {
		t, err := scanAPIToken(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *t)
	}
	return list, rows.Err()
}

// CountActiveAPITokens kullanıcının iptal edilmemiş ve süresi dolmamış token sayısı.
func (db *DB) CountActiveAPITokens(username string, now time.Time) (int, error) {
	var n int
	err := db.conn.QueryRow(`
		SELECT COUNT(*) FROM api_tokens
		WHERE username = ? COLLATE NOCASE AND revoked_at IS NULL
		  AND (expires_at IS NULL OR datetime(expires_at) > datetime(?))`,
		strings.TrimSpace(username), now.UTC()).Scan(&n)
	return n, err
}

// RevokeAPIToken token'ı iptal eder. Zaten iptal edilmişse false döner (idempotent).
func (db *DB) RevokeAPIToken(id int64) (bool, error) {
	res, err := db.conn.Exec(`UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, time.Now().UTC(), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// RevokeUserAPITokens kullanıcının (user_id) tüm etkin token'larını iptal eder
// ve iptal edilen sayıyı döndürür (parola sıfırlama / olay müdahalesi).
func (db *DB) RevokeUserAPITokens(userID int64) (int64, error) {
	res, err := db.conn.Exec(`UPDATE api_tokens SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, time.Now().UTC(), userID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// inactiveAPITokenWhere iptal edilmiş ya da süresi dolmuş satırlar (ilk ? = şimdi).
const inactiveAPITokenWhere = `(revoked_at IS NOT NULL OR (expires_at IS NOT NULL AND datetime(expires_at) <= datetime(?)))`

// PruneUserInactiveAPITokens kullanıcının etkin olmayan (iptal/süresi dolmuş)
// token'larından en yeni `keep` tanesi dışındakileri siler. Oluştur-iptal et
// döngüsü tabloyu sınırsız büyütemez; geçmiş denetim kaydında kalır.
func (db *DB) PruneUserInactiveAPITokens(userID int64, keep int, now time.Time) (int64, error) {
	if keep < 0 {
		keep = 0
	}
	res, err := db.conn.Exec(`
		DELETE FROM api_tokens WHERE id IN (
			SELECT id FROM api_tokens
			WHERE user_id = ? AND `+inactiveAPITokenWhere+`
			ORDER BY id DESC LIMIT -1 OFFSET ?
		)`, userID, now.UTC(), keep)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PurgeStaleAPITokens iptal/son kullanma anı `before`dan eski etkin olmayan
// token satırlarını siler (periyodik temizlik).
func (db *DB) PurgeStaleAPITokens(before time.Time) (int64, error) {
	b := before.UTC()
	res, err := db.conn.Exec(`
		DELETE FROM api_tokens
		WHERE (revoked_at IS NOT NULL AND datetime(revoked_at) < datetime(?))
		   OR (expires_at IS NOT NULL AND datetime(expires_at) < datetime(?))`, b, b)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// TouchAPIToken son kullanım zamanını ve IP'yi yazar.
func (db *DB) TouchAPIToken(id int64, ip string, at time.Time) error {
	if len(ip) > 64 {
		ip = ip[:64]
	}
	_, err := db.conn.Exec(`UPDATE api_tokens SET last_used_at = ?, last_used_ip = ? WHERE id = ?`, at.UTC(), ip, id)
	return err
}
