package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"keywordhunter-mvp/pkg/backup"
	"keywordhunter-mvp/pkg/capture"
	"keywordhunter-mvp/pkg/storage"
)

// addUser verilen rolde kullanıcı oluşturur ve oturum açar.
func addUser(t *testing.T, s *Server, name, role string) []*http.Cookie {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(name+"-pass-1"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.CreateUser(name, string(hash), role); err != nil {
		t.Fatal(err)
	}
	return loginAs(t, s, name, name+"-pass-1")
}

func TestBackupDownload_AuthzAndAudit(t *testing.T) {
	s := newMultiUserServer(t)
	admin := loginAs(t, s, "admin", "adminpass1")
	if _, err := s.db.SaveResults([]storage.SearchResult{{Title: "A", URL: "http://a.onion/", Source: "Torch", Query: "leak"}}); err != nil {
		t.Fatal(err)
	}

	for _, role := range []string{storage.RoleViewer, storage.RoleAnalyst} {
		ck := addUser(t, s, "u-"+role, role)
		if w := doJSON(s, http.MethodPost, "/api/admin/backup", "", ck, csrfOf(ck)); w.Code != http.StatusForbidden {
			t.Fatalf("%s: %d (403 bekleniyordu)", role, w.Code)
		}
	}
	// CSRF'siz admin isteği reddedilir
	if w := doJSON(s, http.MethodPost, "/api/admin/backup", "", admin, ""); w.Code != http.StatusForbidden {
		t.Fatalf("CSRF'siz: %d", w.Code)
	}
	// Başka siteden tetiklenen istek yedek üretmez
	if w := do(s, http.MethodPost, "/api/admin/backup", nil, admin, map[string]string{"X-CSRF-Token": csrfOf(admin), "Sec-Fetch-Site": "cross-site"}); w.Code != http.StatusSeeOther {
		t.Fatalf("cross-site: %d", w.Code)
	}
	if countAudit(t, s, "backup.download") != 0 {
		t.Fatal("reddedilen istekler yedek olayı yazmamalı")
	}
	// API token (admin, write kapsamı) ile çağrılamaz: yalnız etkileşimli oturum
	tok := createToken(t, s, admin, "ci", storage.TokenScopeWrite, 30)
	if w := bearer(s, http.MethodPost, "/api/admin/backup", "", tok.Token, nil, ""); w.Code != http.StatusForbidden {
		t.Fatalf("token: %d (403 bekleniyordu)", w.Code)
	}

	// Başka bir yedek sürerken ikinci yedek reddedilir
	s.backupMu.Lock()
	if w := doJSON(s, http.MethodPost, "/api/admin/backup", "", admin, csrfOf(admin)); w.Code != http.StatusConflict || apiCode(w) != "backup_busy" {
		t.Fatalf("eşzamanlı yedek: %d %s", w.Code, w.Body.String())
	}
	s.backupMu.Unlock()

	w := doJSON(s, http.MethodPost, "/api/admin/backup", "", admin, csrfOf(admin))
	if w.Code != http.StatusOK {
		t.Fatalf("admin: %d %s", w.Code, w.Body.String())
	}
	cd := w.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, `attachment; filename="keywordhunter-`+Version+`-`) || !strings.HasSuffix(cd, `Z.db"`) {
		t.Fatalf("Content-Disposition: %q", cd)
	}
	if w.Header().Get("Content-Type") != "application/vnd.sqlite3" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("başlıklar: %v", w.Header())
	}
	path := filepath.Join(t.TempDir(), "dl.db")
	if err := os.WriteFile(path, w.Body.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, err := storage.VerifyFile(context.Background(), path)
	if err != nil || !rep.OK() || rep.Counts["search_results"] != 1 || rep.SchemaVersion != storage.LatestSchemaVersion() {
		t.Fatalf("indirilen yedek: %+v %v", rep, err)
	}

	ev := lastAudit(t, s, "backup.download")
	if !ev.Success || ev.Actor != "admin" || ev.Target != "database" {
		t.Fatalf("denetim olayı: %+v", ev)
	}
	var d map[string]any
	if err := json.Unmarshal([]byte(ev.Details), &d); err != nil {
		t.Fatal(err)
	}
	// Denetim olayı aktarımdan sonra yazılır: gönderilen bayt = dosya boyutu = gövde.
	if !strings.HasPrefix(d["file"].(string), "keywordhunter-") || d["bytes"].(float64) != float64(w.Body.Len()) ||
		d["size"].(float64) != float64(w.Body.Len()) || d["complete"] != true {
		t.Fatalf("ayrıntılar: %v (gövde %d bayt)", d, w.Body.Len())
	}
	for k := range d {
		switch k {
		case "file", "bytes", "size", "complete", "schemaVersion":
		default:
			t.Fatalf("beklenmeyen ayrıntı alanı (sır sızabilir): %s", k)
		}
	}
	// Geçici kopya veritabanının dizininde hazırlanır ve kalmaz
	if left := backupTemps(t, filepath.Dir(s.db.Path())); len(left) != 0 {
		t.Fatalf("geçici indirme dosyası kaldı: %v", left)
	}
}

// backupTemps dizindeki yarım yedek (geçici) dosyaları.
func backupTemps(t *testing.T, dir string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, ".keywordhunter-backup-*"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// failingWriter belirli bir bayttan sonra yazmayı reddeden yanıt yazıcısı
// (istemci bağlantısı koptu / yazma süresi doldu).
type failingWriter struct {
	*httptest.ResponseRecorder
	limit int
}

func (f *failingWriter) Write(b []byte) (int, error) {
	room := f.limit - f.Body.Len()
	if room <= 0 {
		return 0, errors.New("bağlantı koptu")
	}
	if len(b) > room {
		n, _ := f.ResponseRecorder.Write(b[:room])
		return n, errors.New("bağlantı koptu")
	}
	return f.ResponseRecorder.Write(b)
}

// Yarıda kesilen indirme başarılı sayılmaz: denetim olayı gönderilen baytı
// ve complete=false'u taşır; başarılı olarak yazılmaz.
func TestBackupDownload_TruncatedTransferIsAuditedAsIncomplete(t *testing.T) {
	s := newMultiUserServer(t)
	admin := loginAs(t, s, "admin", "adminpass1")
	req := httptest.NewRequest(http.MethodPost, "/api/admin/backup", nil)
	req.Header.Set("X-CSRF-Token", csrfOf(admin))
	for _, ck := range admin {
		req.AddCookie(ck)
	}
	fw := &failingWriter{ResponseRecorder: httptest.NewRecorder(), limit: 8192}
	s.router.ServeHTTP(fw, req)
	if fw.Code != http.StatusOK || fw.Body.Len() != 8192 {
		t.Fatalf("yanıt: %d, %d bayt", fw.Code, fw.Body.Len())
	}
	ev := lastAudit(t, s, "backup.download")
	var d map[string]any
	if err := json.Unmarshal([]byte(ev.Details), &d); err != nil {
		t.Fatal(err)
	}
	if ev.Success || d["complete"] != false || d["bytes"].(float64) != 8192 || d["size"].(float64) <= 8192 {
		t.Fatalf("yarım aktarım başarılı sayılmamalı: success=%v %v", ev.Success, d)
	}
	if left := backupTemps(t, filepath.Dir(s.db.Path())); len(left) != 0 {
		t.Fatalf("geçici dosya kaldı: %v", left)
	}
}

// Sunucunun genel WriteTimeout'u (gerçekte 5 dk) yedek üretimi ve aktarımını
// kesmez: işleyici yazma süresini uzatır. Burada 300 ms'lik WriteTimeout ve
// 600 ms süren bir üretimle gerçek HTTP bağlantısı üzerinden denenir.
func TestBackupDownload_OutlivesServerWriteTimeout(t *testing.T) {
	s := newMultiUserServer(t)
	admin := loginAs(t, s, "admin", "adminpass1")
	slow := backup.FromDB(s.db)
	s.backupSrc = func(ctx context.Context, dest string) error {
		time.Sleep(600 * time.Millisecond)
		return slow(ctx, dest)
	}
	srv := httptest.NewUnstartedServer(s.router)
	srv.Config.WriteTimeout = 300 * time.Millisecond
	srv.Start()
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/admin/backup", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-CSRF-Token", csrfOf(admin))
	for _, ck := range admin {
		req.AddCookie(ck)
	}
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("istek yazma süresinde kesildi: %v", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil || res.StatusCode != http.StatusOK || int64(len(body)) != res.ContentLength {
		t.Fatalf("indirme: %d, %d/%d bayt, %v", res.StatusCode, len(body), res.ContentLength, err)
	}
	if ev := lastAudit(t, s, "backup.download"); !ev.Success {
		t.Fatalf("tam indirme başarılı denetlenmeli: %+v", ev)
	}
}

// Önceki bir kesintiden kalan (eski) yarım kopya indirme öncesi süpürülür;
// süren bir yedeğe ait olabilecek taze dosyaya dokunulmaz.
func TestBackupDownload_SweepsStaleTemps(t *testing.T) {
	s := newMultiUserServer(t)
	admin := loginAs(t, s, "admin", "adminpass1")
	dir := filepath.Dir(s.db.Path())
	stale := filepath.Join(dir, ".keywordhunter-backup-123.tmp")
	fresh := filepath.Join(dir, ".keywordhunter-backup-456.tmp")
	for _, f := range []string{stale, stale + "-journal", fresh} {
		if err := os.WriteFile(f, []byte("yarım"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * backup.StaleTempAge)
	for _, f := range []string{stale, stale + "-journal"} {
		if err := os.Chtimes(f, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if w := doJSON(s, http.MethodPost, "/api/admin/backup", "", admin, csrfOf(admin)); w.Code != http.StatusOK {
		t.Fatalf("indirme: %d", w.Code)
	}
	if left := backupTemps(t, dir); len(left) != 1 || left[0] != fresh {
		t.Fatalf("yalnız taze geçici dosya kalmalı: %v", left)
	}
}

func TestScheduledBackup_RotatesRecordsAndAudits(t *testing.T) {
	s := newTestServer(t)
	dir := filepath.Join(t.TempDir(), "backups")
	s.backupDir, s.backupInterval, s.backupKeep = dir, 24*time.Hour, 2
	clock := time.Date(2026, 10, 1, 3, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return clock }

	if d := s.nextBackupDelay(clock); d != backupStartupDelay {
		t.Fatalf("ilk yedek açılış gecikmesiyle: %v", d)
	}
	var names []string
	for i := 0; i < 3; i++ {
		res, err := s.runScheduledBackup()
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, res.Name)
		clock = clock.Add(24 * time.Hour)
	}
	list, err := backup.List(dir)
	if err != nil || len(list) != 2 || list[0].Name != names[2] || list[1].Name != names[1] {
		t.Fatalf("döndürme (keep=2): %+v %v", list, err)
	}
	run, err := s.db.GetMaintenanceRun(storage.MaintenanceBackup)
	if err != nil || run == nil || !run.OK || run.LastSuccessAt == nil || !strings.Contains(run.Details, names[2]) {
		t.Fatalf("son çalışma: %+v %v", run, err)
	}
	if n := countAudit(t, s, "backup.scheduled"); n != 3 {
		t.Fatalf("backup.scheduled olayı: %d", n)
	}
	ev := lastAudit(t, s, "backup.scheduled")
	if ev.Actor != "system" || !ev.Success || !strings.Contains(ev.Details, `"removed":1`) {
		t.Fatalf("olay: %+v", ev)
	}
	// Son başarıdan 1 saat sonra açılış: kalan ~23 saat beklenir
	if d := s.nextBackupDelay(run.LastSuccessAt.Add(time.Hour)); d != 23*time.Hour {
		t.Fatalf("sonraki yedek gecikmesi: %v", d)
	}

	// Son başarı gelecekte görünse (saat geri alındı) bile ilk çalışma en çok
	// bir aralık ertelenir.
	if d := s.nextBackupDelay(run.LastSuccessAt.Add(-30 * 24 * time.Hour)); d != s.backupInterval {
		t.Fatalf("gelecekteki son başarı: %v (en çok %v)", d, s.backupInterval)
	}

	// Başarısızlık: dizin yerine dosya
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.backupDir = blocker
	if _, err := s.runScheduledBackup(); err == nil {
		t.Fatal("yazılamayan dizin hata vermeli")
	}
	run, _ = s.db.GetMaintenanceRun(storage.MaintenanceBackup)
	if run.OK || run.Message == "" || run.LastSuccessAt == nil {
		t.Fatalf("başarısız çalışma kaydı (son başarı korunmalı): %+v", run)
	}
	ev = lastAudit(t, s, "backup.scheduled")
	if ev.Success || !strings.Contains(ev.Details, "error") || strings.Contains(ev.Details, `"file"`) {
		t.Fatalf("başarısız olay (yazılmamış dosya adı taşımamalı): %+v", ev)
	}
	if strings.Contains(run.Details, `"file"`) {
		t.Fatalf("başarısız çalışma dosya adı taşımamalı: %s", run.Details)
	}
}

// Saat geri alınmışsa (dizinde ileri tarihli yedekler) yeni yedek döndürmede
// silinmez; durum ve denetim gerçekte var olan dosyayı gösterir.
func TestScheduledBackup_ClockSkewKeepsNewBackup(t *testing.T) {
	s := newTestServer(t)
	dir := filepath.Join(t.TempDir(), "backups")
	s.backupDir, s.backupInterval, s.backupKeep = dir, 24*time.Hour, 2
	future := time.Date(2027, 3, 1, 3, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		s.now = func() time.Time { return future.Add(time.Duration(i) * 24 * time.Hour) }
		if _, err := s.runScheduledBackup(); err != nil {
			t.Fatal(err)
		}
	}
	s.now = func() time.Time { return time.Date(2026, 10, 17, 3, 0, 0, 0, time.UTC) }
	res, err := s.runScheduledBackup()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(res.Path); err != nil {
		t.Fatalf("yeni yedek silinmemeli: %v", err)
	}
	list, _ := backup.List(dir)
	if len(list) != 2 {
		t.Fatalf("keep=2: %+v", list)
	}
	ev := lastAudit(t, s, "backup.scheduled")
	if !ev.Success || !strings.Contains(ev.Details, res.Name) || !strings.Contains(ev.Details, `"newerExisting":2`) {
		t.Fatalf("olay: %+v", ev)
	}
}

// Kapanış süren zamanlanmış yedeği iptal eder ve bekler: yarım kopya silinir,
// çalışma "yarıda kesildi" olarak kaydedilir; işçi çıkar.
func TestShutdown_CancelsRunningScheduledBackup(t *testing.T) {
	s := newTestServer(t)
	s.bgCtx, s.bgCancel = context.WithCancel(context.Background())
	dir := filepath.Join(t.TempDir(), "backups")
	s.backupDir, s.backupInterval, s.backupKeep = dir, time.Hour, 3
	s.backupStartDelay = 10 * time.Millisecond
	entered := make(chan string, 1)
	s.backupSrc = func(ctx context.Context, dest string) error {
		if err := os.WriteFile(dest, []byte("yarım kopya, live-session-id"), 0o600); err != nil {
			return err
		}
		entered <- dest
		<-ctx.Done()
		return ctx.Err()
	}
	s.startBackupScheduler()
	var tmp string
	select {
	case tmp = <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("zamanlanmış yedek başlamadı")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("yarım kopya silinmeli: %v", err)
	}
	if left := backupTemps(t, dir); len(left) != 0 {
		t.Fatalf("geçici dosya kaldı: %v", left)
	}
	run, err := s.db.GetMaintenanceRun(storage.MaintenanceBackup)
	if err != nil || run == nil || run.OK || !strings.Contains(run.Message, "kapanırken") {
		t.Fatalf("kesilen çalışma kaydı: %+v %v", run, err)
	}
}

// İşçiler gerçekten zamanlanır: kısa açılış gecikmesiyle hem yedek hem
// saklama temizliği kendiliğinden çalışır; Shutdown sonrası yeni çalışma olmaz.
func TestMaintenanceWorkers_RunOnScheduleAndStop(t *testing.T) {
	s := newTestServer(t)
	s.bgCtx, s.bgCancel = context.WithCancel(context.Background())
	s.backupDir, s.backupInterval, s.backupKeep = filepath.Join(t.TempDir(), "b"), time.Hour, 3
	s.backupStartDelay, s.retentionStartDelay = 10*time.Millisecond, 10*time.Millisecond
	s.resultRetentionDays = 30
	s.startBackupScheduler()
	s.startRetention()
	deadline := time.Now().Add(15 * time.Second)
	for countAudit(t, s, "backup.scheduled") < 1 || countAudit(t, s, "retention.purge") < 1 {
		if time.Now().After(deadline) {
			t.Fatal("bakım işleri zamanında çalışmadı")
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, k := range []string{storage.MaintenanceBackup, storage.MaintenanceRetention} {
		if r, err := s.db.GetMaintenanceRun(k); err != nil || r == nil || !r.OK {
			t.Fatalf("%s son çalışma: %+v %v", k, r, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { s.bgWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("işçiler kapanışta çıkmadı")
	}
	nb, nr := countAudit(t, s, "backup.scheduled"), countAudit(t, s, "retention.purge")
	if nb != 1 || nr != 1 {
		t.Fatalf("aralık dolmadan ikinci çalışma olmamalı: %d %d", nb, nr)
	}
}

// Ayarlar durumları yanıltmaz: başarısız yedek yazılmamış bir dosya adı
// göstermez; zamanlanmış yedek kapatılınca açma ipucu görünür ve eski çalışma
// geçmiş olarak etiketlenir; dosyası silinemeyen temizlik uyarıdır.
func TestSettingsPage_MaintenanceStates(t *testing.T) {
	s := newTestServer(t)
	ck := loginAs(t, s, "admin", "correct-horse")
	page := func() string {
		w := do(s, http.MethodGet, "/settings", nil, ck, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("/settings: %d", w.Code)
		}
		return w.Body.String()
	}
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.backupDir, s.backupInterval, s.backupKeep = blocker, time.Hour, 3
	res, _ := s.runScheduledBackup()
	body := page()
	if !strings.Contains(body, `id="bk-last"`) || strings.Contains(body, res.Name) {
		t.Fatal("başarısız yedek yazılmamış dosya adını göstermemeli")
	}
	s.backupDir = ""
	body = page()
	if !strings.Contains(body, "BACKUP_DIR=/data/backups") || !strings.Contains(body, `id="bk-history"`) {
		t.Fatal("kapalıyken açma ipucu ve geçmiş çalışma görünmeli")
	}

	// Saklama: dosya silinemezse uyarı (sayılarla); başarısızlıkta son başarı
	s.resultRetentionDays, s.screenshotRetentionDays = 30, 30
	if _, err := s.db.SaveScreenshot(storage.Screenshot{TargetURL: "http://x.onion/", FilePath: "x.png", TakenAt: time.Now().Add(-90 * 24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	// Dizin yerine dosya: silme ENOTDIR verir (yok sayılan ENOENT değil)
	shotDir := filepath.Join(t.TempDir(), "shots")
	if err := os.WriteFile(shotDir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.capturer = capture.New("", "/nonexistent/chromium", shotDir)
	if _, err := s.runRetention(); err != nil {
		t.Fatal(err)
	}
	body = page()
	if !strings.Contains(body, `kh-alert warn" id="ret-last"`) {
		t.Fatalf("silinemeyen dosya uyarı olmalı:\n%s", body[strings.Index(body, `id="sec-retention"`):][:2000])
	}
	s.resultRetentionDays, s.screenshotRetentionDays = 0, 0
	if body = page(); !strings.Contains(body, `id="ret-history"`) || !strings.Contains(body, "RESULT_RETENTION_DAYS=180") {
		t.Fatal("saklama kapalıyken ipucu ve geçmiş çalışma görünmeli")
	}
}

func TestSettingsPage_ShowsBackupStatus(t *testing.T) {
	s := newTestServer(t)
	s.backupDir, s.backupInterval, s.backupKeep = filepath.Join(t.TempDir(), "b"), 6*time.Hour, 3
	if _, err := s.runScheduledBackup(); err != nil {
		t.Fatal(err)
	}
	ck := loginAs(t, s, "admin", "correct-horse")
	w := do(s, http.MethodGet, "/settings", nil, ck, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/settings: %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{`id="sec-backup"`, `data-action="bk-download"`, "BACKUP_KEEP", s.backupDir, `id="bk-last"`, "keywordhunter-" + Version} {
		if !strings.Contains(body, want) {
			t.Errorf("ayarlar sayfasında %q yok", want)
		}
	}
}

// Sahte saatle saklama temizliği: eski/yeni, korunan vakalar, diskten silinen
// dosyalar, sayımlar ve denetim olayı; 0 temizliği kapatır.
func TestRetention_PurgesWithFakeClock(t *testing.T) {
	s := newTestServer(t)
	shotDir := t.TempDir()
	s.capturer = capture.New("", "/nonexistent/chromium", shotDir)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	add := func(url string, age time.Duration, status string) int64 {
		var ids []int64
		if _, err := s.db.SaveResultsWithIDs([]storage.SearchResult{{Title: "t", URL: url, Source: "Torch", Query: "q"}}, &ids); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.GetDBConn().Exec(`UPDATE search_results SET created_at = ? WHERE id = ?`, now.Add(-age), ids[0]); err != nil {
			t.Fatal(err)
		}
		if status != "" {
			if _, err := s.db.UpdateResultCase(ids[0], status, ""); err != nil {
				t.Fatal(err)
			}
		}
		return ids[0]
	}
	shot := func(url, file string, age time.Duration) {
		if err := os.WriteFile(filepath.Join(shotDir, file), []byte("png"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.SaveScreenshot(storage.Screenshot{TargetURL: url, FilePath: file, TakenAt: now.Add(-age)}); err != nil {
			t.Fatal(err)
		}
	}
	day := 24 * time.Hour
	oldID := add("http://old.onion/", 100*day, "")
	newID := add("http://new.onion/", 10*day, "")
	invID := add("http://inv.onion/", 100*day, "investigating")
	confID := add("http://conf.onion/", 100*day, "confirmed")
	shot("http://old.onion/", "old.png", 1*day)      // bulguyla gider
	shot("http://inv.onion/", "inv.png", 100*day)    // korunan vakanın kanıtı
	shot("http://other.onion/", "other.png", 50*day) // ekran görüntüsü saklamasıyla gider
	shot("http://other.onion/", "recent.png", 5*day) // yeni

	// 0 = kapalı: hiçbir şey silinmez, işçi başlamaz
	if s.retentionEnabled() {
		t.Fatal("varsayılan kapalı olmalı")
	}
	s.startRetention()
	if countAudit(t, s, "retention.purge") != 0 {
		t.Fatal("kapalıyken temizlik olmamalı")
	}

	s.resultRetentionDays, s.screenshotRetentionDays = 30, 30
	st, err := s.runRetention()
	if err != nil {
		t.Fatal(err)
	}
	if st.Results != 1 || st.KeptCases != 2 || st.Screenshots != 2 {
		t.Fatalf("sayımlar: %+v", st)
	}
	exists := func(id int64) bool {
		r, err := s.db.GetResultByID(id)
		return err == nil && r != nil
	}
	if exists(oldID) || !exists(newID) || !exists(invID) || !exists(confID) {
		t.Fatal("yanlış bulgular silindi/tutuldu")
	}
	for f, want := range map[string]bool{"old.png": false, "other.png": false, "inv.png": true, "recent.png": true} {
		_, err := os.Stat(filepath.Join(shotDir, f))
		if got := err == nil; got != want {
			t.Errorf("%s diskte: %v (beklenen %v)", f, got, want)
		}
	}
	ev := lastAudit(t, s, "retention.purge")
	if ev.Actor != "system" || !ev.Success {
		t.Fatalf("olay: %+v", ev)
	}
	var d map[string]float64
	if err := json.Unmarshal([]byte(ev.Details), &d); err != nil {
		t.Fatalf("ayrıntılar yalnız sayım olmalı: %v %s", err, ev.Details)
	}
	if d["results"] != 1 || d["screenshots"] != 2 || d["files"] != 2 || d["keptCases"] != 2 || d["resultDays"] != 30 {
		t.Fatalf("ayrıntılar: %v", d)
	}
	run, _ := s.db.GetMaintenanceRun(storage.MaintenanceRetention)
	if run == nil || !run.OK || !run.FinishedAt.Equal(now) {
		t.Fatalf("son çalışma: %+v", run)
	}
	if d := s.nextRunDelay(storage.MaintenanceRetention, retentionEvery, retentionStartupDelay, now.Add(time.Hour)); d != 23*time.Hour {
		t.Fatalf("sonraki temizlik: %v", d)
	}

	// Saat ilerler: "new" bulgu da süresini doldurur
	now = now.Add(25 * day)
	if st, err := s.runRetention(); err != nil || st.Results != 1 || exists(newID) {
		t.Fatalf("saat ilerleyince: %+v %v", st, err)
	}
	if countAudit(t, s, "retention.purge") != 2 {
		t.Fatal("her çalışma bir olay yazmalı")
	}

	// Ayarlar sayfası politikayı ve son çalışmayı gösterir
	ck := loginAs(t, s, "admin", "correct-horse")
	w := do(s, http.MethodGet, "/settings", nil, ck, nil)
	body := w.Body.String()
	for _, want := range []string{`id="sec-retention"`, "RESULT_RETENTION_DAYS", "SCREENSHOT_RETENTION_DAYS", `id="ret-last"`} {
		if !strings.Contains(body, want) {
			t.Errorf("ayarlar sayfasında %q yok", want)
		}
	}
}
