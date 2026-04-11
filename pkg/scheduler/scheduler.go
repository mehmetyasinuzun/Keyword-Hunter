// Package scheduler zamanlanmış dark web keyword taramalarını çalıştırır.
// Her dakika DB'yi kontrol eder, süresi gelen taramaları sıraya alır.
// Kaynak verimlisi: sadece 1 eş zamanlı tarama çalışır.
package scheduler

import (
	"sync"
	"time"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/notify"
	"keywordhunter-mvp/pkg/search"
	"keywordhunter-mvp/pkg/storage"
)

// Scheduler zamanlanmış taramaları yönetir
type Scheduler struct {
	db       *storage.DB
	searcher *search.Searcher
	stopChan chan struct{}
	once     sync.Once
	// Aynı anda sadece 1 tarama — kaynak kısıtlaması için
	runSem chan struct{}
}

// New yeni Scheduler oluşturur
func New(db *storage.DB, searcher *search.Searcher) *Scheduler {
	return &Scheduler{
		db:       db,
		searcher: searcher,
		stopChan: make(chan struct{}),
		runSem:   make(chan struct{}, 1),
	}
}

// Start zamanlayıcıyı başlatır
func (s *Scheduler) Start() {
	go func() {
		// İlk tick için 1 dakika bekle
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				s.checkAndRun()
			case <-s.stopChan:
				return
			}
		}
	}()
	logger.Info("Scheduler başlatıldı (her dakika kontrol)")
}

// Stop zamanlayıcıyı durdurur
func (s *Scheduler) Stop() {
	s.once.Do(func() {
		close(s.stopChan)
	})
}

// RunNow belirli bir scheduled search'ü hemen çalıştırır
func (s *Scheduler) RunNow(id int64) {
	go func() {
		ss, err := s.db.GetScheduledSearch(id)
		if err != nil {
			logger.Warn("Scheduler RunNow: schedule bulunamadı (ID: %d): %v", id, err)
			return
		}
		s.runSearch(ss)
	}()
}

// checkAndRun süresi gelen taramaları başlatır
func (s *Scheduler) checkAndRun() {
	due, err := s.db.GetDueScheduledSearches()
	if err != nil {
		logger.Warn("Scheduler: zamanlanmış taramalar alınamadı: %v", err)
		return
	}

	for _, ss := range due {
		// runSem ile kuyruğa al (non-blocking: zaten çalışıyorsa atla)
		select {
		case s.runSem <- struct{}{}:
			go func(scheduled storage.ScheduledSearch) {
				defer func() { <-s.runSem }()
				s.runSearch(&scheduled)
			}(ss)
		default:
			logger.Debug("Scheduler: tarama zaten çalışıyor, %s atlandı", ss.Query)
		}
	}
}

// runSearch tek bir zamanlanmış taramayı çalıştırır
func (s *Scheduler) runSearch(ss *storage.ScheduledSearch) {
	logger.Info("SCHEDULER: Zamanlanmış tarama başlıyor — '%s' (ID: %d)", ss.Query, ss.ID)
	startTime := time.Now()

	// Daha önce bilinen URL'leri al (diff için)
	knownURLs, err := s.db.GetKnownURLsForQuery(ss.Query)
	if err != nil {
		logger.Warn("Scheduler: bilinen URL'ler alınamadı: %v", err)
		knownURLs = make(map[string]bool)
	}

	// Taramayı çalıştır
	results := s.searcher.SearchAll(ss.Query)

	// Yeni URL'leri tespit et (diff)
	var newFindings []notify.Finding
	var storageResults []storage.SearchResult
	newCount := 0

	for _, r := range results {
		isNew := !knownURLs[r.URL]
		if isNew {
			newCount++
			if r.Criticality >= ss.AlertThreshold {
				newFindings = append(newFindings, notify.Finding{
					Title:       r.Title,
					URL:         r.URL,
					Category:    r.Category,
					Criticality: r.Criticality,
				})
			}
		}
		storageResults = append(storageResults, storage.SearchResult{
			Title:       r.Title,
			URL:         r.URL,
			Source:      r.Source,
			Query:       ss.Query,
			Criticality: r.Criticality,
			Category:    r.Category,
		})
	}

	// Sonuçları kaydet
	savedCount, err := s.db.SaveResults(storageResults)
	if err != nil {
		logger.Warn("Scheduler: sonuçlar kaydedilemedi: %v", err)
	}

	// Arama geçmişine ekle
	s.db.SaveSearchHistory(ss.Query, len(results))

	// Scheduled search istatistiklerini güncelle
	if err := s.db.UpdateScheduledSearchAfterRun(ss.ID, len(results), newCount); err != nil {
		logger.Warn("Scheduler: istatistik güncelleme hatası: %v", err)
	}

	elapsed := time.Since(startTime).Round(time.Second)
	logger.Info("SCHEDULER: Tamamlandı — '%s' → %d sonuç, %d yeni, %d kaydedildi (%v)",
		ss.Query, len(results), newCount, savedCount, elapsed)

	// Webhook bildirimi gönder (sadece yeni bulgu varsa veya her zaman — webhook ayarına göre)
	if ss.WebhookURL != "" && newCount > 0 {
		payload := notify.AlertPayload{
			Query:       ss.Query,
			NewCount:    newCount,
			TotalCount:  len(results),
			TopFindings: topFindings(newFindings, 5),
			RunAt:       startTime,
		}
		if err := notify.SendWebhook(ss.WebhookURL, payload); err != nil {
			logger.Warn("Scheduler: webhook gönderilemedi: %v", err)
		} else {
			logger.Info("Scheduler: webhook gönderildi (%s)", ss.WebhookURL)
		}
	}
}

// topFindings kritikliğe göre sıralanmış ilk N bulguyu döndürür
func topFindings(findings []notify.Finding, n int) []notify.Finding {
	// Basit insertion sort — küçük slice için yeterli
	for i := 1; i < len(findings); i++ {
		for j := i; j > 0 && findings[j].Criticality > findings[j-1].Criticality; j-- {
			findings[j], findings[j-1] = findings[j-1], findings[j]
		}
	}
	if len(findings) > n {
		return findings[:n]
	}
	return findings
}
