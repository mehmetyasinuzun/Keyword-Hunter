package tor

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"
)

// Sahte kontrol portu: AUTHENTICATE ve SIGNAL NEWNYM'e 250 döner, yanlış parolaya 515.
func fakeControl(t *testing.T, password string) (addr string, got *[]string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cmds := &[]string{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					line = strings.TrimSpace(line)
					*cmds = append(*cmds, line)
					switch {
					case strings.HasPrefix(line, "AUTHENTICATE"):
						if password != "" && line != `AUTHENTICATE "`+password+`"` {
							c.Write([]byte("515 Authentication failed\r\n"))
							return
						}
						c.Write([]byte("250 OK\r\n"))
					case line == "SIGNAL NEWNYM":
						c.Write([]byte("250 OK\r\n"))
					case strings.HasPrefix(line, "GETINFO"):
						c.Write([]byte("250-status/circuit-established=1\r\n250 OK\r\n"))
					default:
						c.Write([]byte("510 Unrecognized command\r\n"))
					}
				}
			}(conn)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String(), cmds
}

func TestNewNym_AuthAndCooldown(t *testing.T) {
	addr, got := fakeControl(t, "secret")
	c := New(addr, "secret", 200*time.Millisecond)
	if err := c.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if err := c.NewNym(false); err != nil {
		t.Fatalf("newnym: %v", err)
	}
	if err := c.NewNym(false); err == nil {
		t.Fatal("bekleme süresi içinde ikinci NEWNYM reddedilmeli")
	}
	if err := c.NewNym(true); err != nil {
		t.Fatalf("force newnym: %v", err)
	}
	time.Sleep(250 * time.Millisecond)
	if err := c.NewNym(false); err != nil {
		t.Fatalf("bekleme sonrası newnym: %v", err)
	}
	joined := strings.Join(*got, "|")
	if !strings.Contains(joined, `AUTHENTICATE "secret"`) || !strings.Contains(joined, "SIGNAL NEWNYM") {
		t.Fatalf("komutlar beklenen gibi değil: %s", joined)
	}
}

func TestNewNym_WrongPasswordAndNilController(t *testing.T) {
	addr, _ := fakeControl(t, "secret")
	c := New(addr, "wrong", time.Minute)
	if err := c.NewNym(true); err == nil {
		t.Fatal("yanlış parola hata vermeli")
	}
	var nilC *Controller
	if err := nilC.NewNym(true); err == nil {
		t.Fatal("nil controller hata vermeli")
	}
	if New("", "", 0) != nil {
		t.Fatal("boş adres nil dönmeli")
	}
}
