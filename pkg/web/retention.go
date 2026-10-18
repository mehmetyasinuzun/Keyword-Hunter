package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/metrics"
	"keywordhunter-mvp/pkg/shared"
	"keywordhunter-mvp/pkg/storage"
)

// ─────────────────────────── Saklama süresi temizliği ───────────────────────────
//
// RESULT_RETENTION_DAYS (bulgular; son görülmeden sayılır) ve
// SCREENSHOT_RETENTION_DAYS (ekran görüntüleri) günde bir uygulanır; 0 =
// süresiz (kapalı). İnceleniyor / Doğrulandı vakaları ve onların URL'sine ait
// ekran görüntüleri tutulur (bkz. storage/retention.go). Ekran görüntüsü
// dosyaları her parti silinir silinmez diskten kaldırılır. Her çalışma yalnız
// sayımlarla `retention.purge` olarak denetlenir ve loglanır; kapanışta
// (Shutdown) kalan partiler atlanır.

const (
	retentionEvery = 24 * time.Hour
	// retentionStartupDelay açılıştan sonra ilk temizliğe kadar en az bekleme
	// (zamanlanmış yedekle çakışmasın diye ondan sonra).
	retentionStartupDelay = 5 * time.Minute
)

// retentionEnabled en az bir saklama süresi tanımlı mı.
func (s *Server) retentionEnabled() bool {
	return s.resultRetentionDays > 0 || s.screenshotRetentionDays > 0
}

// startRetention saklama süresi tanımlıysa günlük temizlik işçisini başlatır.
func (s *Server) startRetention() {
	if !s.retentionEnabled() {
		logger.Info("Saklama süresi temizliği kapalı (RESULT_RETENTION_DAYS ve SCREENSHOT_RETENTION_DAYS = 0)")
		return
	}
	logger.Info("Saklama süresi temizliği etkin: bulgular %d gün (son görülmeden), ekran görüntüleri %d gün (0 = süresiz); İnceleniyor/Doğrulandı vakaları tutulur",
		s.resultRetentionDays, s.screenshotRetentionDays)
	first := retentionStartupDelay
	if s.retentionStartDelay > 0 {
		first = s.retentionStartDelay
	}
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		timer := time.NewTimer(s.nextRunDelay(storage.MaintenanceRetention, retentionEvery, first, s.clock()))
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
				_, _ = s.runRetention()
				timer.Reset(retentionEvery)
			case <-s.cleanupStop:
				return
			case <-s.lifetime().Done():
				return
			}
		}
	}()
}

// runRetention süresi dolan bulguları ve ekran görüntülerini (dosyalarıyla)
// siler; sonucu maintenance_runs'a ve denetim kaydına yazar.
func (s *Server) runRetention() (storage.PurgeStats, error) {
	s.retentionMu.Lock()
	defer s.retentionMu.Unlock()
	start := s.clock()
	ctx := s.lifetime()
	var total storage.PurgeStats
	var errs []error
	// Dosyalar her parti işlendikten hemen sonra silinir: çalışma yarıda
	// kesilirse (kapanış, çökme) satırı gitmiş dosyalar diskte sahipsiz kalmaz.
	var removed, failed int
	onFiles := func(files []string) {
		r, f := s.removeScreenshotFiles(files)
		removed += r
		failed += f
	}
	if s.resultRetentionDays > 0 {
		st, err := s.db.PurgeResultsBefore(ctx, start.Add(-time.Duration(s.resultRetentionDays)*24*time.Hour), onFiles)
		total.Add(st)
		if err != nil {
			errs = append(errs, err)
		}
	}
	if s.screenshotRetentionDays > 0 && ctx.Err() == nil {
		st, err := s.db.PurgeScreenshotsBefore(ctx, start.Add(-time.Duration(s.screenshotRetentionDays)*24*time.Hour), onFiles)
		total.Add(st)
		if err != nil {
			errs = append(errs, err)
		}
	}
	err := errors.Join(errs...)
	// Yarıda kesilen çalışmada da silinmiş olan satırlar sayılır.
	for kind, n := range map[string]int64{
		"results": total.Results, "tags": total.Tags, "artifacts": total.Artifacts,
		"misp_pushed": total.MISPPushed, "graph_nodes": total.GraphNodes,
		"screenshots": total.Screenshots, "screenshot_files": int64(removed),
	} {
		metrics.RetentionPurged(kind, n)
	}
	if err != nil && ctx.Err() != nil {
		err = fmt.Errorf("sunucu kapanırken yarıda kesildi: %w", err)
	}

	details := map[string]any{
		"resultDays": s.resultRetentionDays, "screenshotDays": s.screenshotRetentionDays,
		"results": total.Results, "tags": total.Tags, "artifacts": total.Artifacts,
		"mispPushed": total.MISPPushed, "graphNodes": total.GraphNodes,
		"screenshots": total.Screenshots, "files": removed, "keptCases": total.KeptCases,
	}
	if failed > 0 {
		details["fileErrors"] = failed
	}
	run := storage.MaintenanceRun{Kind: storage.MaintenanceRetention, StartedAt: start, FinishedAt: s.clock(), OK: err == nil}
	if err != nil {
		run.Message = shared.Truncate(err.Error(), 300)
		details["error"] = run.Message
		logger.Error("SAKLAMA TEMİZLİĞİ BAŞARISIZ: %v", err)
	} else {
		// Silinemeyen dosyalar (fileErrors) Ayarlar'da uyarı olarak görünür.
		logger.Info("SAKLAMA TEMİZLİĞİ: %d bulgu (%d etiket, %d IOC, %d graf düğümü), %d ekran görüntüsü (%d dosya) silindi; %d vaka tutuldu",
			total.Results, total.Tags, total.Artifacts, total.GraphNodes, total.Screenshots, removed, total.KeptCases)
	}
	if failed > 0 {
		logger.Warn("Saklama temizliği: %d ekran görüntüsü dosyası silinemedi", failed)
	}
	if b, jerr := json.Marshal(details); jerr == nil {
		run.Details = string(b)
	}
	if rerr := s.db.RecordMaintenanceRun(run); rerr != nil {
		logger.Warn("Saklama durumu kaydedilemedi: %v", rerr)
	}
	s.auditSystem("retention.purge", "search_results", details, err == nil)
	return total, err
}

// removeScreenshotFiles ekran görüntüsü dizinindeki dosyaları siler. Yalnız
// dosya adı kullanılır (dizin dışına çıkılamaz); zaten olmayan dosya silinmiş sayılır.
func (s *Server) removeScreenshotFiles(files []string) (removed, failed int) {
	if len(files) == 0 {
		return 0, 0
	}
	if s.capturer == nil || s.capturer.OutDir() == "" {
		return 0, len(files)
	}
	dir := s.capturer.OutDir()
	for _, f := range files {
		name := filepath.Base(f)
		if name == "." || name == ".." || name == string(filepath.Separator) {
			failed++
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			logger.Warn("Ekran görüntüsü dosyası silinemedi (%s): %v", name, err)
			failed++
			continue
		}
		removed++
	}
	return removed, failed
}

// retentionView Ayarlar → Saklama bölümü.
type retentionView struct {
	Enabled        bool
	ResultDays     int
	ScreenshotDays int
	AuditDays      int // AUDIT_RETENTION_DAYS (denetim kaydı ayrıca budanır)
	Last           *maintRunView
	Counts         map[string]int64
}

func (s *Server) retentionStatus() retentionView {
	v := retentionView{
		Enabled:        s.retentionEnabled(),
		ResultDays:     s.resultRetentionDays,
		ScreenshotDays: s.screenshotRetentionDays,
		AuditDays:      s.auditRetentionDays,
		Counts:         map[string]int64{},
	}
	r, err := s.db.GetMaintenanceRun(storage.MaintenanceRetention)
	if err != nil {
		logger.Warn("Saklama durumu okunamadı: %v", err)
	}
	v.Last = newMaintRunView(r)
	if v.Last != nil {
		for _, k := range []string{"results", "screenshots", "graphNodes", "keptCases", "files", "fileErrors"} {
			if n, ok := v.Last.Details[k].(float64); ok {
				v.Counts[k] = int64(n)
			}
		}
		v.Last.Warn = v.Last.OK && v.Counts["fileErrors"] > 0
	}
	return v
}
