package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/backup"
	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/shared"
	"keywordhunter-mvp/pkg/storage"
)

// ─────────────────────────── Yedekleme ───────────────────────────
//
// İki yol: (1) yöneticinin Ayarlar'dan indirdiği anlık yedek
// (POST /api/admin/backup) ve (2) BACKUP_DIR tanımlıysa zamanlanmış yedek
// (BACKUP_INTERVAL_HOURS'ta bir, en yeni BACKUP_KEEP tutulur). İkisi de
// VACUUM INTO ile çevrimiçi ve tutarlı alınır, integrity_check'ten geçer ve
// denetlenir (backup.download / backup.scheduled). Yedekte APP_SECRET yoktur;
// ayrıca yedeklenmelidir (bkz. README → Yedekleme).

const (
	// backupStartupDelay açılıştan sonra ilk zamanlanmış yedeğe kadar en az
	// bekleme: açılış yükünü (migrasyon, iş kurtarma) yedekle çakıştırma.
	backupStartupDelay = 2 * time.Minute
	// backupRetryAfter başarısız zamanlanmış yedeğin yeniden deneme süresi (aralık daha kısaysa aralık).
	backupRetryAfter = time.Hour
	// backupTimeout tek bir yedeğin (üretim + doğrulama) üst süresi.
	backupTimeout = 30 * time.Minute
	// minDownloadRate indirmenin aktarım süresi hesabında varsayılan en düşük
	// hız: yazma süresi = backupTimeout + boyut / minDownloadRate.
	minDownloadRate = 64 << 10 // bayt/sn
	// maintenanceStopWait kapanışta süren bakım işinin en çok beklenmesi.
	maintenanceStopWait = 8 * time.Second
)

// clock test dikişi: retention/yedek işleri "şimdi"yi buradan alır.
func (s *Server) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// lifetime sunucu ömrü bağlamı (Shutdown iptal eder). New ile kurulmayan
// (test) sunucuda iptal edilmeyen arka plan bağlamıdır.
func (s *Server) lifetime() context.Context {
	if s.bgCtx != nil {
		return s.bgCtx
	}
	return context.Background()
}

// stopMaintenance süren yedek/temizlik işlerini iptal eder ve işçilerin
// çıkmasını en çok maintenanceStopWait (ya da ctx) kadar bekler.
func (s *Server) stopMaintenance(ctx context.Context) {
	if s.bgCancel != nil {
		s.bgCancel()
	}
	done := make(chan struct{})
	go func() {
		s.bgWG.Wait()
		close(done)
	}()
	wait := time.NewTimer(maintenanceStopWait)
	defer wait.Stop()
	select {
	case <-done:
	case <-ctx.Done():
		logger.Warn("Bakım işleri kapanışta tamamlanmadı: %v", ctx.Err())
	case <-wait.C:
		logger.Warn("Bakım işleri kapanışta %v içinde tamamlanmadı", maintenanceStopWait)
	}
}

// backupSource yedek kaynağı: açık veritabanı (testlerde değiştirilebilir).
func (s *Server) backupSource() backup.Source {
	if s.backupSrc != nil {
		return s.backupSrc
	}
	return backup.FromDB(s.db)
}

// backupStagingDir indirme yedeğinin geçici kopyası için dizin: veritabanının
// dizini (aynı birim; os.TempDir küçük ya da RAM tabanlı olabilir, Docker'da
// ise kapsayıcının atılan yazılabilir katmanıdır).
func (s *Server) backupStagingDir() string {
	if s.db != nil {
		if p := s.db.Path(); p != "" && p != ":memory:" && !strings.HasPrefix(p, "file:") {
			return filepath.Dir(p)
		}
	}
	return os.TempDir()
}

// sweepBackupTemps önceki bir kesintiden (SIGKILL, OOM) kalmış yarım yedek
// kopyalarını siler: indirme hazırlık dizini ve BACKUP_DIR.
func (s *Server) sweepBackupTemps() {
	dirs := []string{s.backupStagingDir()}
	if s.backupDir != "" {
		dirs = append(dirs, s.backupDir)
	}
	for _, d := range dirs {
		removed, err := backup.SweepStale(d, backup.StaleTempAge, time.Now())
		if len(removed) > 0 {
			logger.Warn("Yarım kalmış %d yedek kopyası silindi (%s)", len(removed), d)
		}
		if err != nil {
			logger.Warn("Yarım yedek kopyaları temizlenemedi (%s): %v", d, err)
		}
	}
}

// downloadDeadline indirmenin aktarım aşaması için yazma süresi.
func downloadDeadline(size int64) time.Duration {
	return backupTimeout + time.Duration(size/minDownloadRate)*time.Second
}

// handleBackupDownload POST /api/admin/backup — veritabanının anlık, tutarlı
// yedeğini geçici dosyaya üretir, doğrular ve indirme olarak akıtır. Yalnız
// admin ve etkileşimli oturum (token ile çağrılamaz: tokenForbiddenRoutes);
// CSRF zorunlu, başka siteden tetiklenen istek reddedilir.
//
// Sunucunun genel 5 dk WriteTimeout'u büyük bir veritabanının üretimi ve
// aktarımı için yetmez: yazma süresi önce üretim için backupTimeout'a, sonra
// boyuta göre uzatılır. Denetim olayı aktarımdan SONRA yazılır ve gönderilen
// bayt sayısıyla tam aktarılıp aktarılmadığını taşır.
func (s *Server) handleBackupDownload(c *gin.Context) {
	if refuseCrossSiteExport(c, "/settings") {
		return
	}
	// Aynı anda tek yedek üretimi (indirme ya da zamanlanmış): her biri tüm
	// veritabanını kopyalar. Kilit yalnız üretim boyunca tutulur.
	if !s.backupMu.TryLock() {
		apiError(c, http.StatusConflict, "backup_busy")
		return
	}
	locked := true
	unlock := func() {
		if locked {
			locked = false
			s.backupMu.Unlock()
		}
	}
	defer unlock()

	rc := http.NewResponseController(c.Writer)
	setWriteDeadline(rc, time.Now().Add(backupTimeout))

	now := s.clock()
	name := backup.FileName(Version, now)
	dir := s.backupStagingDir()
	if removed, _ := backup.SweepStale(dir, backup.StaleTempAge, time.Now()); len(removed) > 0 {
		logger.Warn("Yarım kalmış %d yedek kopyası silindi (%s)", len(removed), dir)
	}
	path, err := backup.CreateTemp(dir)
	if err != nil {
		s.audit(c, "backup.download", "database", map[string]any{"file": name, "error": "temp_file"}, false)
		respondBackupError(c, err)
		return
	}
	defer backup.RemoveTemp(path)

	// Kapanışta üretim iptal edilir (geçici dosya yukarıdaki defer ile silinir).
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	stop := context.AfterFunc(s.lifetime(), cancel)
	defer stop()
	if err := backup.Produce(ctx, s.backupSource(), path); err != nil {
		s.audit(c, "backup.download", "database", map[string]any{"file": name, "error": "backup_failed"}, false)
		respondBackupError(c, err)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		s.audit(c, "backup.download", "database", map[string]any{"file": name, "error": "backup_failed"}, false)
		respondBackupError(c, err)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		s.audit(c, "backup.download", "database", map[string]any{"file": name, "error": "backup_failed"}, false)
		respondBackupError(c, err)
		return
	}
	// Açık tanıtıcı okumaya yeter: ad hemen silinir; süreç aktarım sırasında
	// öldürülse bile diskte tam bir kopya kalmaz (POSIX; olmazsa defer siler).
	backup.RemoveTemp(path)
	unlock()

	size := st.Size()
	setWriteDeadline(rc, time.Now().Add(downloadDeadline(size)))
	c.Header("Content-Type", "application/vnd.sqlite3")
	c.Header("Content-Length", strconv.FormatInt(size, 10))
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	c.Header("Cache-Control", "no-store")
	c.Status(http.StatusOK)
	sent, copyErr := io.Copy(c.Writer, f)
	complete := copyErr == nil && sent == size
	s.audit(c, "backup.download", "database", map[string]any{
		"file": name, "bytes": sent, "size": size, "complete": complete, "schemaVersion": storage.LatestSchemaVersion(),
	}, complete)
	if complete {
		logger.Info("YEDEK İNDİRME: %s (%s) — kullanıcı=%s", name, humanBytes(size), c.GetString("username"))
	} else {
		logger.Warn("YEDEK İNDİRME YARIDA KESİLDİ: %s (%s / %s gönderildi) — kullanıcı=%s: %v",
			name, humanBytes(sent), humanBytes(size), c.GetString("username"), copyErr)
	}
}

// setWriteDeadline yanıtın yazma süresini ayarlar (desteklemeyen yazıcıda,
// ör. testlerde, sessizce geçilir).
func setWriteDeadline(rc *http.ResponseController, t time.Time) {
	if err := rc.SetWriteDeadline(t); err != nil && !errors.Is(err, http.ErrNotSupported) {
		logger.Debug("Yedek indirme yazma süresi ayarlanamadı: %v", err)
	}
}

func respondBackupError(c *gin.Context, err error) {
	logger.Error("Yedek indirme başarısız: %v", err)
	apiError(c, http.StatusInternalServerError, "backup_failed")
}

// startBackupScheduler BACKUP_DIR tanımlıysa zamanlanmış yedek işçisini başlatır.
func (s *Server) startBackupScheduler() {
	if s.backupDir == "" {
		logger.Info("Zamanlanmış yedekleme kapalı (BACKUP_DIR boş)")
		return
	}
	if s.backupInterval <= 0 {
		s.backupInterval = 24 * time.Hour
	}
	logger.Info("Zamanlanmış yedekleme etkin: %s, her %v, en yeni %d yedek tutulur", absPath(s.backupDir), s.backupInterval, s.backupKeep)
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		timer := time.NewTimer(s.nextBackupDelay(s.clock()))
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
				next := s.backupInterval
				if _, err := s.runScheduledBackup(); err != nil && next > backupRetryAfter {
					next = backupRetryAfter
				}
				timer.Reset(next)
			case <-s.cleanupStop:
				return
			case <-s.lifetime().Done():
				return
			}
		}
	}()
}

// absPath yolu mutlak gösterir (göreli BACKUP_DIR çalışma dizinine göre
// çözülür; Docker'da bu kalıcı birim değil /app'tir — görünür olsun).
func absPath(p string) string {
	if p == "" {
		return ""
	}
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// nextBackupDelay ilk zamanlanmış yedeğe kadar bekleme (bkz. nextRunDelay).
func (s *Server) nextBackupDelay(now time.Time) time.Duration {
	first := backupStartupDelay
	if s.backupStartDelay > 0 {
		first = s.backupStartDelay
	}
	return s.nextRunDelay(storage.MaintenanceBackup, s.backupInterval, first, now)
}

// nextRunDelay bakım işinin ilk çalışmasına kadar bekleme: son başarılı
// çalışmadan bu yana aralık dolmadıysa kalan süre, dolduysa kısa bir açılış
// gecikmesi (minDelay). Böylece sık yeniden başlatma işi her seferinde
// tetiklemez. Kalan süre en çok bir aralıktır: son başarı gelecekte görünüyorsa
// (saat geri alınmış) iş saat düzelene dek ertelenmez.
func (s *Server) nextRunDelay(kind string, interval, minDelay time.Duration, now time.Time) time.Duration {
	d := minDelay
	r, err := s.db.GetMaintenanceRun(kind)
	if err == nil && r != nil && r.LastSuccessAt != nil {
		rem := r.LastSuccessAt.Add(interval).Sub(now)
		if rem > interval {
			rem = interval
		}
		if rem > d {
			d = rem
		}
	}
	return d
}

// runScheduledBackup BACKUP_DIR'e bir yedek yazar, eskileri döndürür, sonucu
// maintenance_runs'a ve denetim kaydına (backup.scheduled) yazar. Kapanışta
// (Shutdown) yarıda kesilir; yarım kopya silinir.
func (s *Server) runScheduledBackup() (backup.Result, error) {
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	start := s.clock()
	ctx, cancel := context.WithTimeout(s.lifetime(), backupTimeout)
	defer cancel()
	res, err := backup.Run(ctx, s.backupSource(), s.backupDir, Version, s.backupKeep, start)
	if err != nil && s.lifetime().Err() != nil {
		err = fmt.Errorf("sunucu kapanırken yarıda kesildi: %w", err)
	}
	// İndirme hazırlık dizinindeki artıklar da (öldürülen indirme) burada süpürülür.
	if dir := s.backupStagingDir(); dir != s.backupDir {
		_, _ = backup.SweepStale(dir, backup.StaleTempAge, time.Now())
	}

	details := map[string]any{"keep": s.backupKeep}
	run := storage.MaintenanceRun{Kind: storage.MaintenanceBackup, StartedAt: start, FinishedAt: s.clock(), OK: err == nil}
	if len(res.Swept) > 0 {
		details["sweptTemps"] = len(res.Swept)
		logger.Warn("Yarım kalmış %d yedek kopyası silindi (%s)", len(res.Swept), s.backupDir)
	}
	if err != nil {
		run.Message = shared.Truncate(err.Error(), 300)
		details["error"] = run.Message
		logger.Error("ZAMANLANMIŞ YEDEK BAŞARISIZ (%s): %v", absPath(s.backupDir), err)
	} else {
		// Dosya adı yalnız gerçekten yazılmışsa (başarıda) kaydedilir.
		details["file"] = res.Name
		details["bytes"] = res.Bytes
		details["removed"] = len(res.Removed)
		if res.RotateErr != nil {
			details["rotateError"] = shared.Truncate(res.RotateErr.Error(), 300)
			logger.Warn("Eski yedekler silinemedi: %v", res.RotateErr)
		}
		if res.NewerExisting > 0 {
			details["newerExisting"] = res.NewerExisting
			logger.Warn("Yedek dizininde bu yedekten yeni tarihli %d yedek var: sistem saati geri mi alındı? Yeni yedek döndürmede korunur.", res.NewerExisting)
		}
		logger.Info("ZAMANLANMIŞ YEDEK: %s (%s, %v), %d eski yedek silindi", res.Name, humanBytes(res.Bytes), res.Duration.Round(time.Millisecond), len(res.Removed))
	}
	if b, jerr := json.Marshal(details); jerr == nil {
		run.Details = string(b)
	}
	if rerr := s.db.RecordMaintenanceRun(run); rerr != nil {
		logger.Warn("Yedek durumu kaydedilemedi: %v", rerr)
	}
	s.auditSystem("backup.scheduled", "database", details, err == nil)
	return res, err
}

// humanBytes bayt sayısını okunur biçime çevirir (1024 tabanı).
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// ─────────────────────────── Ayarlar görünümü ───────────────────────────

// maintRunView bakım işinin son çalışması (şablon için hazır alanlar).
type maintRunView struct {
	At          time.Time
	OK          bool
	Warn        bool // tamamlandı ama uyarı var (ör. silinemeyen dosyalar)
	Message     string
	LastSuccess time.Time
	File        string // yalnız başarılı yedekte (yazılan dosya)
	Size        string
	Details     map[string]any
}

// backupView Ayarlar → Yedekleme bölümü.
type backupView struct {
	Enabled       bool
	Dir           string
	IntervalHours int
	Keep          int
	Count         int // dizindeki yedek sayısı
	ListError     bool
	Last          *maintRunView
}

func newMaintRunView(r *storage.MaintenanceRun) *maintRunView {
	if r == nil {
		return nil
	}
	v := &maintRunView{At: r.FinishedAt, OK: r.OK, Message: r.Message, Details: map[string]any{}}
	if r.LastSuccessAt != nil {
		v.LastSuccess = *r.LastSuccessAt
	}
	if r.Details != "" {
		_ = json.Unmarshal([]byte(r.Details), &v.Details)
	}
	if f, ok := v.Details["file"].(string); ok && r.OK {
		v.File = f
		if b, ok := v.Details["bytes"].(float64); ok {
			v.Size = humanBytes(int64(b))
		}
	}
	return v
}

func (s *Server) backupStatus() backupView {
	v := backupView{
		Enabled:       s.backupDir != "",
		Dir:           absPath(s.backupDir),
		IntervalHours: int(s.backupInterval / time.Hour),
		Keep:          s.backupKeep,
	}
	if v.Enabled {
		if list, err := backup.List(s.backupDir); err == nil {
			v.Count = len(list)
		} else if !os.IsNotExist(err) {
			v.ListError = true
		}
	}
	r, err := s.db.GetMaintenanceRun(storage.MaintenanceBackup)
	if err != nil {
		logger.Warn("Yedek durumu okunamadı: %v", err)
	}
	v.Last = newMaintRunView(r)
	return v
}
