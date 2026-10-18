// Command khseed boş bir KeywordHunter veritabanını deterministik, gerçekçi
// bir demo veri kümesiyle doldurur: farklı sorgu/motor/kritiklik/kategoride
// etiketli bulgular, IOC'ler, vaka durumları ve notlar, arama geçmişi, planlı
// taramalar, kontrol geçmişli izleme listesi, sağlıklı motor istatistikleri,
// hash zincirli denetim olayları ve bilinen parolalı demo kullanıcıları
// (analyst, viewer).
//
// Kullanım:
//
//	go run ./cmd/khseed -db data/demo.db -reset
//	go run ./cmd/khseed -env .env -db data/demo.db -reset   # admin kimliği .env'den
//
// Aynı çekirdekle (-seed) her çalıştırma aynı içeriği üretir; zaman damgaları
// "şimdi"ye göredir ki pano ve "son 24 saat" görünümleri dolu gelsin. -reset
// mevcut veritabanı dosyasını (WAL/SHM dahil) silip baştan kurar; -reset
// olmadan dolu bir veritabanına yazılmaz.
//
// UYARI: Demo parolaları herkesçe bilinir (CONTRIBUTING.md). Bu araç yalnız
// geliştirme, ekran görüntüsü ve uçtan uca testler içindir; üretim
// veritabanında çalıştırmayın.
package main

import (
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"keywordhunter-mvp/pkg/config"
	"keywordhunter-mvp/pkg/logger"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "khseed: %v\n", err)
		os.Exit(1)
	}
}

// run bayrakları çözümler ve tohumlamayı çalıştırır (test edilebilir giriş noktası).
func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("khseed", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "demo veritabanı dosyası (zorunlu; .env'deki DB_PATH asla kullanılmaz)")
	envPath := fs.String("env", "", "isteğe bağlı .env: yalnız ADMIN_USER / ADMIN_PASS / ADMIN_PASS_HASH okunur")
	reset := fs.Bool("reset", false, "mevcut veritabanını silip baştan kur")
	adminUser := fs.String("admin-user", "", "admin kullanıcı adı (varsayılan: $ADMIN_USER ya da admin)")
	adminPass := fs.String("admin-pass", "", "admin parolası (varsayılan: $ADMIN_PASS; o da yoksa rastgele üretilip yazdırılır)")
	seed := fs.Uint64("seed", DefaultSeed, "deterministik rastgelelik çekirdeği")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Kullanım: khseed -db <yol> [-reset] [-env .env] [-admin-user u] [-admin-pass p] [-seed n]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("beklenmeyen argüman: %s", strings.Join(fs.Args(), " "))
	}

	admin := AdminAccount{Username: strings.TrimSpace(*adminUser), Password: *adminPass}
	// Hedef yalnız -db'den gelir: .env'deki DB_PATH'e düşmek, -reset ile
	// birlikte uygulamanın gerçek veritabanını sessizce silerdi.
	path := strings.TrimSpace(*dbPath)
	if path == "" {
		fs.Usage()
		return errors.New("-db zorunlu (DB_PATH'e asla düşülmez; demo için ayrı bir dosya verin, ör. data/demo.db)")
	}
	if *envPath != "" {
		cfg, err := config.Load(*envPath)
		if err != nil {
			return fmt.Errorf("env okunamadı: %w", err)
		}
		if admin.Username == "" {
			admin.Username = cfg.AdminUser
		}
		if admin.Password == "" {
			admin.Password, admin.PasswordHash = cfg.AdminPass, cfg.AdminPassHash
		}
		// Demo hesaplarının parolaları herkesçe bilinir: -db, .env'nin işaret
		// ettiği uygulama veritabanıyla aynı dosyaysa (-reset olsun olmasın)
		// hiçbir şeye dokunmadan reddet.
		if sameDBFile(path, cfg.DBPath) {
			return fmt.Errorf("-db (%s) %s içindeki DB_PATH ile aynı: khseed uygulamanın veritabanına yazmaz; ayrı bir demo dosyası verin", path, *envPath)
		}
	}
	if admin.Username == "" {
		admin.Username = strings.TrimSpace(os.Getenv("ADMIN_USER"))
	}
	if admin.Username == "" {
		admin.Username = "admin"
	}
	if admin.Password == "" && admin.PasswordHash == "" {
		admin.Password = os.Getenv("ADMIN_PASS")
	}
	generated := false
	if admin.Password == "" && admin.PasswordHash == "" {
		p, err := randomPassword(20)
		if err != nil {
			return err
		}
		admin.Password, generated = p, true
	}

	// Depolama katmanı logger'ı kullanır: proje dizinine logs/ açmamak için
	// geçici dizine ve yalnız uyarı seviyesinde yaz.
	if err := logger.Init(os.TempDir()+string(os.PathSeparator)+"khseed-logs", logger.WARN, true); err != nil {
		return fmt.Errorf("logger başlatılamadı: %w", err)
	}
	defer func() { _ = logger.Close() }()

	start := time.Now()
	sum, err := Seed(Options{
		DBPath: path,
		Reset:  *reset,
		Seed:   *seed,
		Now:    start,
		Admin:  admin,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "KeywordHunter demo verisi hazır: %s (%s)\n", path, time.Since(start).Round(time.Millisecond))
	fmt.Fprintf(stdout, "  bulgu: %d · IOC: %d · arama geçmişi: %d · planlı tarama: %d · izleme: %d (%d kontrol) · motor: %d · denetim olayı: %d\n",
		sum.Findings, sum.Artifacts, sum.History, sum.Scheduled, sum.Watchlist, sum.WatchChecks, sum.Engines, sum.AuditEvents)
	fmt.Fprintln(stdout, "  Demo hesapları (yalnız geliştirme/test — üretimde KULLANMAYIN):")
	switch {
	case generated:
		fmt.Fprintf(stdout, "    %-8s admin    parola: %s  (rastgele üretildi; bir kez gösterilir)\n", admin.Username, admin.Password)
	case admin.PasswordHash != "" && admin.Password == "":
		fmt.Fprintf(stdout, "    %-8s admin    parola: ADMIN_PASS_HASH ile verilen\n", admin.Username)
	default:
		fmt.Fprintf(stdout, "    %-8s admin    parola: ADMIN_PASS / -admin-pass ile verilen\n", admin.Username)
	}
	for _, u := range DemoUsers {
		fmt.Fprintf(stdout, "    %-8s %-8s parola: %s\n", u.Username, u.Role, u.Password)
	}
	return nil
}

// randomPassword crypto/rand ile okunabilir güçlü bir parola üretir.
func randomPassword(n int) (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
	var b strings.Builder
	for i := 0; i < n; i++ {
		k, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		b.WriteByte(alphabet[k.Int64()])
	}
	return b.String() + "-9!", nil
}

// sameDBFile a ve b aynı veritabanı dosyasını gösteriyorsa true döner: önce
// temizlenmiş mutlak yollar karşılaştırılır, ikisi de varsa os.SameFile ile
// sembolik/sabit bağlantılar da yakalanır.
func sameDBFile(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA == nil && errB == nil && absA == absB {
		return true
	}
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}
