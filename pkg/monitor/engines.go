// Package monitor arama motorlarının sağlık durumunu izler.
// Kaynak verimliliği için basit HTTP HEAD/GET ile ping yapar,
// sonuçları engine_stats tablosuna yazar.
package monitor

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/search"
	"keywordhunter-mvp/pkg/storage"
)

// EngineMonitor arama motorlarını periyodik olarak kontrol eder
type EngineMonitor struct {
	db       *storage.DB
	client   *http.Client
	stopChan chan struct{}
	once     sync.Once
	interval time.Duration
}

// New yeni EngineMonitor oluşturur
// interval: kontrol aralığı (örn: 5*time.Minute)
// torProxy: SOCKS5 proxy adresi (örn: "127.0.0.1:9150")
func New(db *storage.DB, torProxy string, interval time.Duration) (*EngineMonitor, error) {
	// Tor üzerinden bağlantı için transport oluştur
	transport, err := buildTorTransport(torProxy)
	if err != nil {
		return nil, fmt.Errorf("engine monitor transport hatası: %w", err)
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		// Yönlendirmeyi takip etme — sadece engine'in cevap verip vermediğini kontrol et
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	em := &EngineMonitor{
		db:       db,
		client:   client,
		stopChan: make(chan struct{}),
		interval: interval,
	}

	// Engine'leri DB'ye başlangıç kaydı olarak ekle
	for _, eng := range search.SearchEngines {
		if err := db.UpsertEngineStat(eng.Name, eng.URL); err != nil {
			logger.Warn("Engine stat upsert hatası (%s): %v", eng.Name, err)
		}
	}

	return em, nil
}

// Start izlemeyi başlatır (goroutine ile)
func (em *EngineMonitor) Start() {
	go func() {
		// İlk kontrol hemen yap
		em.checkAll()

		ticker := time.NewTicker(em.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				em.checkAll()
			case <-em.stopChan:
				return
			}
		}
	}()
	logger.Info("Engine Monitor başlatıldı (aralık: %v, %d motor)", em.interval, len(search.SearchEngines))
}

// Stop izlemeyi durdurur
func (em *EngineMonitor) Stop() {
	em.once.Do(func() {
		close(em.stopChan)
	})
}

// CheckNow tüm motorları hemen kontrol eder (API çağrısı için)
func (em *EngineMonitor) CheckNow() {
	go em.checkAll()
}

// checkAll tüm motorları paralel olarak kontrol eder
// Kaynak kısıtlaması: max 5 eş zamanlı istek
func (em *EngineMonitor) checkAll() {
	sem := make(chan struct{}, 5)
	var wg sync.WaitGroup

	for _, eng := range search.SearchEngines {
		wg.Add(1)
		sem <- struct{}{}
		go func(e search.Engine) {
			defer wg.Done()
			defer func() { <-sem }()
			em.checkEngine(e)
		}(eng)
	}
	wg.Wait()
	logger.Info("Engine Monitor: tüm motorlar kontrol edildi")
}

// checkEngine tek bir motoru kontrol eder
func (em *EngineMonitor) checkEngine(eng search.Engine) {
	start := time.Now()

	// Engine URL'sini test sorgusu ile oluştur
	testURL := strings.Replace(eng.URL, "{query}", url.QueryEscape("test"), 1)

	req, err := http.NewRequest("GET", testURL, nil)
	if err != nil {
		em.db.UpdateEngineCheck(eng.Name, "down", 0, 0)
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; rv:109.0) Gecko/20100101 Firefox/115.0")

	resp, err := em.client.Do(req)
	elapsed := int(time.Since(start).Milliseconds())

	if err != nil {
		logger.Debug("ENGINE CHECK FAIL: %s → %v", eng.Name, err)
		em.db.UpdateEngineCheck(eng.Name, "down", elapsed, 0)
		return
	}
	defer resp.Body.Close()

	// 200, 301, 302 → up; diğerleri → down
	if resp.StatusCode == 200 || resp.StatusCode == 301 || resp.StatusCode == 302 || resp.StatusCode == 429 {
		logger.Debug("ENGINE CHECK OK: %s → %d (%dms)", eng.Name, resp.StatusCode, elapsed)
		em.db.UpdateEngineCheck(eng.Name, "up", elapsed, 0)
	} else {
		logger.Debug("ENGINE CHECK FAIL: %s → HTTP %d (%dms)", eng.Name, resp.StatusCode, elapsed)
		em.db.UpdateEngineCheck(eng.Name, "down", elapsed, 0)
	}
}

// buildTorTransport SOCKS5 proxy üzerinden çalışan HTTP transport oluşturur
func buildTorTransport(torProxy string) (http.RoundTripper, error) {
	if torProxy == "" {
		return http.DefaultTransport, nil
	}

	proxyURL, err := url.Parse("socks5://" + torProxy)
	if err != nil {
		return nil, err
	}

	return &http.Transport{
		Proxy:               http.ProxyURL(proxyURL),
		TLSHandshakeTimeout: 20 * time.Second,
		IdleConnTimeout:     30 * time.Second,
	}, nil
}
