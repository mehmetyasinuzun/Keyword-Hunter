package web

import (
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// maxVisitors visitors map'inin alabileceği maksimum girdi sayısı (bellek tükenmesi koruması)
const maxVisitors = 50000

type rateVisitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// IPRateLimiter basit IP bazli token-bucket limitleyici.
type IPRateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*rateVisitor
	rps      rate.Limit
	burst    int
}

func NewIPRateLimiter(rps float64, burst int) *IPRateLimiter {
	if rps <= 0 {
		rps = 12
	}
	if burst <= 0 {
		burst = 30
	}

	return &IPRateLimiter{
		visitors: make(map[string]*rateVisitor),
		rps:      rate.Limit(rps),
		burst:    burst,
	}
}

func (rl *IPRateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		limiter := rl.getLimiter(ip)
		if !limiter.Allow() {
			c.AbortWithStatusJSON(429, gin.H{
				"error": "Cok fazla istek gonderildi. Lutfen kisa sure sonra tekrar deneyin",
			})
			return
		}
		c.Next()
	}
}

func (rl *IPRateLimiter) getLimiter(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	visitor, exists := rl.visitors[ip]
	if !exists {
		// Üst sınıra ulaşıldıysa idle girdileri temizle, hâlâ doluysa en eskiyi at
		if len(rl.visitors) >= maxVisitors {
			rl.evictLocked()
		}
		limiter := rate.NewLimiter(rl.rps, rl.burst)
		rl.visitors[ip] = &rateVisitor{limiter: limiter, lastSeen: time.Now()}
		return limiter
	}

	visitor.lastSeen = time.Now()
	return visitor.limiter
}

// evictLocked idle girdileri temizler; hiçbiri idle değilse en eski girdiyi siler.
// Çağıran rl.mu kilidini tutmalıdır.
func (rl *IPRateLimiter) evictLocked() {
	now := time.Now()
	for ip, visitor := range rl.visitors {
		if now.Sub(visitor.lastSeen) > 10*time.Minute {
			delete(rl.visitors, ip)
		}
	}
	if len(rl.visitors) < maxVisitors {
		return
	}
	var oldestIP string
	var oldestSeen time.Time
	for ip, visitor := range rl.visitors {
		if oldestIP == "" || visitor.lastSeen.Before(oldestSeen) {
			oldestIP = ip
			oldestSeen = visitor.lastSeen
		}
	}
	if oldestIP != "" {
		delete(rl.visitors, oldestIP)
	}
}

func (rl *IPRateLimiter) Cleanup(maxIdle time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	for ip, visitor := range rl.visitors {
		if now.Sub(visitor.lastSeen) > maxIdle {
			delete(rl.visitors, ip)
		}
	}
}

func (rl *IPRateLimiter) UpdatePolicy(rps float64, burst int) {
	if rps <= 0 || burst <= 0 {
		return
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.rps = rate.Limit(rps)
	rl.burst = burst
	for _, visitor := range rl.visitors {
		visitor.limiter = rate.NewLimiter(rl.rps, rl.burst)
	}
}
