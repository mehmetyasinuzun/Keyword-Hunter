package web

import (
	"crypto/subtle"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// verifyBcrypt hash ile parolayı karşılaştırır (hash boşsa false).
func verifyBcrypt(hash, password string) bool {
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// subtleEqual sabit-zamanlı string karşılaştırma.
func subtleEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// hashPassword bcrypt hash üretir.
func hashPassword(p string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
	return string(h), err
}

// credentialStore çalışma zamanında değiştirilebilen yönetici kimlik bilgileri.
// Parola her zaman bcrypt hash olarak tutulur; düz metin ADMIN_PASS ile
// başlatıldıysa açılışta hash'lenir. Böylece login karşılaştırması sabit
// maliyetli ve yavaştır (kaba kuvvete karşı doğal fren).
type credentialStore struct {
	mu       sync.RWMutex
	username string
	hash     []byte
}

func newCredentialStore(username, plainPass, hash string) (*credentialStore, error) {
	cs := &credentialStore{username: strings.TrimSpace(username)}
	if hash != "" {
		cs.hash = []byte(hash)
		return cs, nil
	}
	h, err := bcrypt.GenerateFromPassword([]byte(plainPass), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	cs.hash = h
	return cs, nil
}

// Verify kullanıcı adı ve parolayı doğrular. Kullanıcı adı eşleşmese bile
// bcrypt çalıştırılır ki yanıt süresi kullanıcı adının varlığını ele vermesin.
func (cs *credentialStore) Verify(username, password string) bool {
	cs.mu.RLock()
	user := cs.username
	hash := cs.hash
	cs.mu.RUnlock()

	userOK := subtle.ConstantTimeCompare([]byte(username), []byte(user)) == 1
	passOK := bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil
	return userOK && passOK
}

// Username mevcut yönetici kullanıcı adını döndürür.
func (cs *credentialStore) Username() string {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.username
}

// Update kullanıcı adını ve (boş değilse) parolayı anında değiştirir; yeni
// bcrypt hash'ini döndürür (kalıcı .env yazımı için).
func (cs *credentialStore) Update(username, newPassword string) (string, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if u := strings.TrimSpace(username); u != "" {
		cs.username = u
	}
	if newPassword != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
		if err != nil {
			return "", err
		}
		cs.hash = h
	}
	return string(cs.hash), nil
}

// ---------------------------------------------------------------------------
// Login kaba kuvvet koruması
// ---------------------------------------------------------------------------

const (
	loginMaxFailures   = 5                // bu kadar başarısız denemeden sonra kilit
	loginFailureWindow = 15 * time.Minute // sayaç bu pencere içinde tutulur
	loginBaseLock      = 1 * time.Minute  // ilk kilit süresi; her ek hata ile ikiye katlanır
	loginMaxLock       = 30 * time.Minute
	loginGlobalMax     = 200 // tüm IP'lerden toplam hata (dağıtık deneme koruması)
)

type loginAttempt struct {
	failures  int
	firstFail time.Time
	lockUntil time.Time
}

// loginGuard IP başına ve global başarısız giriş sayacı tutar.
type loginGuard struct {
	mu       sync.Mutex
	attempts map[string]*loginAttempt
	global   loginAttempt
	now      func() time.Time
}

func newLoginGuard() *loginGuard {
	return &loginGuard{attempts: make(map[string]*loginAttempt), now: time.Now}
}

// Blocked bir IP'nin (veya tüm sistemin) şu an kilitli olup olmadığını ve
// kalan süreyi döndürür.
func (g *loginGuard) Blocked(ip string) (bool, time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	if g.global.lockUntil.After(now) {
		return true, g.global.lockUntil.Sub(now)
	}
	if a, ok := g.attempts[ip]; ok && a.lockUntil.After(now) {
		return true, a.lockUntil.Sub(now)
	}
	return false, 0
}

// Fail başarısız denemeyi kaydeder; eşik aşıldıysa üstel kilit uygular.
func (g *loginGuard) Fail(ip string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()

	a, ok := g.attempts[ip]
	if !ok || now.Sub(a.firstFail) > loginFailureWindow {
		a = &loginAttempt{firstFail: now}
		g.attempts[ip] = a
	}
	a.failures++
	if a.failures >= loginMaxFailures {
		a.lockUntil = now.Add(lockDuration(a.failures - loginMaxFailures))
	}

	if now.Sub(g.global.firstFail) > loginFailureWindow {
		g.global = loginAttempt{firstFail: now}
	}
	g.global.failures++
	if g.global.failures >= loginGlobalMax {
		g.global.lockUntil = now.Add(loginBaseLock)
	}
}

// Success başarılı girişte IP sayacını sıfırlar.
func (g *loginGuard) Success(ip string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.attempts, ip)
}

// Cleanup eski kayıtları temizler (periyodik).
func (g *loginGuard) Cleanup() {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	for ip, a := range g.attempts {
		if now.Sub(a.firstFail) > loginFailureWindow && !a.lockUntil.After(now) {
			delete(g.attempts, ip)
		}
	}
}

func lockDuration(extraFailures int) time.Duration {
	d := loginBaseLock
	for i := 0; i < extraFailures && d < loginMaxLock; i++ {
		d *= 2
	}
	if d > loginMaxLock {
		d = loginMaxLock
	}
	return d
}
