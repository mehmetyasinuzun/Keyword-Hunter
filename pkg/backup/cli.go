package backup

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"keywordhunter-mvp/pkg/config"
	"keywordhunter-mvp/pkg/storage"
)

// Komut satırı: keywordhunter backup | verify-db
//
//	keywordhunter backup [-out dosya.db] [-db kaynak.db]
//	keywordhunter verify-db [-json] dosya.db
//
// backup çalışan uygulamanın veritabanından (DB_PATH: ortam → .env) tutarlı
// bir yedek alır; migrasyon çalıştırmaz, kaynağa yazmaz. verify-db bir
// dosyayı salt okunur doğrular: integrity_check + denetim zinciri + şema sürümü.

// Commands komut satırında tanınan alt komutlar.
var Commands = map[string]bool{"backup": true, "verify-db": true}

// RunCommand alt komutu çalıştırır. Hata varsa çağıran 1 ile çıkar.
func RunCommand(name string, args []string, version, envFile string, stdout, stderr io.Writer) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch name {
	case "backup":
		return runBackupCmd(ctx, args, version, envFile, stdout, stderr)
	case "verify-db":
		return runVerifyCmd(ctx, args, stdout, stderr)
	}
	return fmt.Errorf("bilinmeyen komut: %s", name)
}

func runBackupCmd(ctx context.Context, args []string, version, envFile string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "", "yedek dosyası (varsayılan: ./keywordhunter-<sürüm>-<UTC zaman>.db; var olan dosyanın üzerine yazılmaz)")
	src := fs.String("db", "", "kaynak veritabanı (varsayılan: DB_PATH — ortam, sonra .env)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Kullanım: keywordhunter backup [-out dosya.db] [-db kaynak.db]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("beklenmeyen argüman: %s", strings.Join(fs.Args(), " "))
	}
	source := strings.TrimSpace(*src)
	if source == "" {
		p, err := config.ResolveDBPath(envFile)
		if err != nil {
			return err
		}
		source = p
	}
	dest := strings.TrimSpace(*out)
	if dest == "" {
		dest = FileName(version, time.Now())
	}
	n, err := WriteFile(ctx, FromFile(source), dest)
	if err != nil {
		return fmt.Errorf("yedek alınamadı: %w", err)
	}
	fmt.Fprintf(stdout, "Yedek yazıldı: %s (%d bayt; kaynak %s; integrity_check: ok)\n", dest, n, source)
	fmt.Fprintln(stdout, "Not: APP_SECRET yedekte yoktur; ayrıca ve yedekten ayrı saklayın.")
	return nil
}

func runVerifyCmd(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("verify-db", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "raporu JSON olarak yaz")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Kullanım: keywordhunter verify-db [-json] dosya.db")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errors.New("tam olarak bir veritabanı dosyası verin")
	}
	rep, err := storage.VerifyFile(ctx, fs.Arg(0))
	if err != nil {
		return fmt.Errorf("doğrulanamadı: %w", err)
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(struct {
			*storage.VerifyReport
			OK bool `json:"ok"`
		}{rep, rep.OK()}); err != nil {
			return err
		}
	} else {
		printReport(stdout, rep)
	}
	if !rep.OK() {
		return errors.New("doğrulama başarısız")
	}
	return nil
}

func printReport(w io.Writer, r *storage.VerifyReport) {
	fmt.Fprintf(w, "Dosya:            %s (%d bayt)\n", r.Path, r.SizeBytes)
	if r.IntegrityOK {
		fmt.Fprintln(w, "integrity_check:  ok")
	} else {
		fmt.Fprintln(w, "integrity_check:  BAŞARISIZ")
		for _, m := range r.Integrity {
			fmt.Fprintf(w, "  - %s\n", m)
		}
	}
	note := ""
	switch {
	case r.SchemaVersion > r.LatestSchemaVersion:
		note = " — bu ikiliden YENİ: bu sürümle açılamaz"
	case r.SchemaVersion == 0:
		note = " — sürümsüz (v0.17 öncesi): açılışta benimsenir"
	case r.SchemaVersion < r.LatestSchemaVersion:
		note = " — eski: açılışta yükseltilir"
	}
	fmt.Fprintf(w, "Şema sürümü:      %d (bu ikili: %d)%s\n", r.SchemaVersion, r.LatestSchemaVersion, note)
	switch {
	case !r.IntegrityOK:
		fmt.Fprintln(w, "Denetim zinciri:  atlandı (bütünlük hatası)")
	case r.AuditChain == nil:
		fmt.Fprintln(w, "Denetim zinciri:  tablo yok")
	case r.AuditChain.OK:
		fmt.Fprintf(w, "Denetim zinciri:  ok (%d olay doğrulandı, baş #%d, budanan %d)\n", r.AuditChain.Checked, r.AuditChain.HeadID, r.AuditChain.PrunedTotal)
	default:
		fmt.Fprintf(w, "Denetim zinciri:  KIRIK — #%d (%s)\n", r.AuditChain.BrokenAt, r.AuditChain.Reason)
	}
	if len(r.Counts) > 0 {
		var parts []string
		for _, t := range []string{"search_results", "result_artifacts", "screenshots", "users", "audit_events"} {
			if n, ok := r.Counts[t]; ok {
				parts = append(parts, fmt.Sprintf("%s=%d", t, n))
			}
		}
		fmt.Fprintf(w, "Satırlar:         %s\n", strings.Join(parts, " "))
	}
	if r.OK() {
		fmt.Fprintln(w, "Sonuç:            OK")
	} else {
		fmt.Fprintln(w, "Sonuç:            BAŞARISIZ")
	}
}
