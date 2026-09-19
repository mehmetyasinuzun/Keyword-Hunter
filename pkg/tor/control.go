// Package tor Tor kontrol portu ile konuşur: yeni devre (NEWNYM) isteği ve
// devre durumu. Motorlar toplu engellendiğinde veya analist istediğinde çıkış
// düğümünü değiştirmek için kullanılır.
package tor

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"keywordhunter-mvp/pkg/logger"
)

// Controller kontrol portu istemcisi. Tor NEWNYM'i 10 sn'den sık kabul etmez;
// biz de gereksiz devre değişimini önlemek için kendi bekleme süremizi uygularız.
type Controller struct {
	addr     string
	password string
	cooldown time.Duration

	mu      sync.Mutex
	lastNym time.Time
	lastErr string
}

// New addr "host:port" (Tor Browser 9151, tor servisi 9051, Docker tor:9051).
// Boş addr → nil (özellik kapalı).
func New(addr, password string, cooldown time.Duration) *Controller {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil
	}
	if cooldown <= 0 {
		cooldown = 2 * time.Minute
	}
	return &Controller{addr: addr, password: password, cooldown: cooldown}
}

// Addr kontrol adresini döndürür.
func (c *Controller) Addr() string { return c.addr }

// Status son NEWNYM zamanı ve son hatayı döndürür.
func (c *Controller) Status() (last time.Time, lastErr string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastNym, c.lastErr
}

// CanRenew bekleme süresi dolduysa true.
func (c *Controller) CanRenew() (bool, time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	wait := c.cooldown - time.Since(c.lastNym)
	if wait > 0 {
		return false, wait
	}
	return true, 0
}

// NewNym yeni Tor devresi ister. force=false iken bekleme süresine uyar.
func (c *Controller) NewNym(force bool) error {
	if c == nil {
		return fmt.Errorf("tor kontrol portu yapılandırılmamış (TOR_CONTROL)")
	}
	if !force {
		if ok, wait := c.CanRenew(); !ok {
			return fmt.Errorf("devre yakın zamanda yenilendi, %s sonra tekrar deneyin", wait.Round(time.Second))
		}
	}
	err := c.signal("NEWNYM")
	c.mu.Lock()
	if err != nil {
		c.lastErr = err.Error()
	} else {
		c.lastNym = time.Now()
		c.lastErr = ""
	}
	c.mu.Unlock()
	if err != nil {
		logger.Warn("TOR NEWNYM başarısız: %v", err)
		return err
	}
	logger.Info("TOR NEWNYM: yeni devre istendi (%s)", c.addr)
	return nil
}

// Ping kontrol portuna bağlanıp kimlik doğrular (sağlık kontrolü).
func (c *Controller) Ping() error {
	if c == nil {
		return fmt.Errorf("yapılandırılmamış")
	}
	conn, err := c.dial()
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = c.command(conn, `GETINFO status/circuit-established`)
	return err
}

func (c *Controller) signal(sig string) error {
	conn, err := c.dial()
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = c.command(conn, "SIGNAL "+sig)
	return err
}

func (c *Controller) dial() (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", c.addr, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("kontrol portuna bağlanılamadı (%s): %w", c.addr, err)
	}
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	auth := "AUTHENTICATE"
	if c.password != "" {
		auth = fmt.Sprintf("AUTHENTICATE %q", c.password)
	}
	if _, err := c.command(conn, auth); err != nil {
		conn.Close()
		return nil, fmt.Errorf("kimlik doğrulama başarısız (TOR_CONTROL_PASSWORD?): %w", err)
	}
	return conn, nil
}

// command tek bir komut gönderir, 250 dışı yanıtı hata sayar.
func (c *Controller) command(conn net.Conn, cmd string) ([]string, error) {
	if _, err := fmt.Fprintf(conn, "%s\r\n", cmd); err != nil {
		return nil, err
	}
	r := bufio.NewReader(conn)
	var lines []string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return lines, err
		}
		line = strings.TrimRight(line, "\r\n")
		lines = append(lines, line)
		if len(line) < 4 {
			return lines, fmt.Errorf("beklenmeyen yanıt: %q", line)
		}
		code, sep := line[:3], line[3]
		if sep == ' ' { // son satır
			if code != "250" {
				return lines, fmt.Errorf("tor yanıtı: %s", line)
			}
			return lines, nil
		}
	}
}
