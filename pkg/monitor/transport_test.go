package monitor

import (
	"net/http"
	"testing"
)

// TestBuildTorTransport_NoDirectFallback: boş proxy doğrudan clearnet'e DÜŞMEMELİ
// (.onion DNS sızıntısı); geçerli proxy socks5 üzerinden yönlendirmeli.
func TestBuildTorTransport_NoDirectFallback(t *testing.T) {
	if rt, err := buildTorTransport(""); err == nil || rt != nil {
		t.Fatalf("boş proxy hata vermeli ve transport dönmemeli: rt=%v err=%v", rt, err)
	}

	rt, err := buildTorTransport("127.0.0.1:9050")
	if err != nil || rt == nil {
		t.Fatalf("geçerli proxy transport üretmeli: %v", err)
	}
	tr, ok := rt.(*http.Transport)
	if !ok || tr.Proxy == nil {
		t.Fatalf("*http.Transport ve Proxy fonksiyonu bekleniyordu: %T", rt)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://abc.onion/", nil)
	pu, err := tr.Proxy(req)
	if err != nil || pu == nil || pu.Scheme != "socks5" || pu.Host != "127.0.0.1:9050" {
		t.Fatalf("socks5://127.0.0.1:9050 bekleniyordu: %v err=%v", pu, err)
	}
	// Ortam proxy'si değil, sabit socks5 — DefaultTransport OLMAMALI
	if rt == http.DefaultTransport {
		t.Fatal("DefaultTransport döndü — doğrudan clearnet geri düşüşü")
	}
}
