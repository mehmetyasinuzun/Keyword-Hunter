// Package backup çevrimiçi veritabanı yedeklerini üretir, dizine atomik
// yazar, eski yedekleri döndürür (rotate) ve doğrular. Yedeğin kendisi
// storage katmanında VACUUM INTO ile alınır (bkz. storage/backup.go); bu
// paket dosya yaşam döngüsünü yönetir ve hem web sunucusu (zamanlanmış yedek,
// indirme) hem komut satırı (keywordhunter backup) tarafından kullanılır.
package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"keywordhunter-mvp/pkg/storage"
)

// stampLayout dosya adındaki UTC zaman damgası.
const stampLayout = "20060102T150405Z"

// namePattern yalnız bu paketin ürettiği yedek adları (döndürme yalnız
// bunlara dokunur; dizindeki başka dosyalar asla silinmez).
var namePattern = regexp.MustCompile(`^keywordhunter-[0-9A-Za-z._+-]+-(\d{8}T\d{6}Z)\.db$`)

var unsafeVersion = regexp.MustCompile(`[^0-9A-Za-z._+-]+`)

// tempPrefix/tempSuffix yarım yedeklerin (geçici dosya) adı:
// .keywordhunter-backup-*.tmp. Zamanlanmış yedek, komut satırı ve indirme
// aynı adı kullanır; böylece kesintiye uğramış bir yedeğin artığı
// SweepStale ile bulunur.
const (
	tempPrefix = ".keywordhunter-backup-"
	tempSuffix = ".tmp"
)

// StaleTempAge yarım yedek artığının silinebilmesi için son değişikliğinden
// bu yana geçmesi gereken süre. Süren bir yedeğin dosyası (VACUUM INTO,
// doğrulama) bu kadar uzun hiç değişmeden kalmaz.
const StaleTempAge = time.Hour

// FileName yedek dosyası adı: keywordhunter-<sürüm>-<UTC zaman damgası>.db
func FileName(version string, t time.Time) string {
	v := unsafeVersion.ReplaceAllString(strings.TrimPrefix(strings.TrimSpace(version), "v"), "_")
	if v == "" {
		v = "dev"
	}
	return "keywordhunter-" + v + "-" + t.UTC().Format(stampLayout) + ".db"
}

// Source yedeği verilen (boş) dosyaya yazan işlev: (*storage.DB).BackupTo ya
// da migrasyonsuz storage.BackupFile.
type Source func(ctx context.Context, dest string) error

// FromDB açık veritabanından yedek kaynağı.
func FromDB(db *storage.DB) Source { return db.BackupTo }

// FromFile kapalı/başka süreçte açık veritabanı dosyasından (migrasyonsuz) kaynak.
func FromFile(path string) Source {
	return func(ctx context.Context, dest string) error { return storage.BackupFile(ctx, path, dest) }
}

// integrityCheck test dikişi (bütünlük denetimi başarısızlık yolu).
var integrityCheck = storage.IntegrityCheck

// Produce yedeği path'e (var olan BOŞ dosya ya da yok) yazar ve
// integrity_check ile doğrular. Başarısızlıkta path'i silmez; çağıran temizler.
func Produce(ctx context.Context, src Source, path string) error {
	if err := src(ctx, path); err != nil {
		return err
	}
	if err := integrityCheck(ctx, path); err != nil {
		return fmt.Errorf("yedek doğrulanamadı: %w", err)
	}
	return nil
}

// WriteFile yedeği dest'e atomik yazar: aynı dizinde 0600 izinli geçici
// dosya → VACUUM INTO → integrity_check → fsync → rename. Yarım ya da bozuk
// bir yedek hiçbir zaman dest adıyla görünmez; dest zaten varsa reddedilir.
func WriteFile(ctx context.Context, src Source, dest string) (int64, error) {
	if _, err := os.Lstat(dest); err == nil {
		return 0, fmt.Errorf("hedef zaten var: %s", dest)
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	dir := filepath.Dir(dest)
	name, err := CreateTemp(dir)
	if err != nil {
		return 0, err
	}
	done := false
	defer func() {
		if !done {
			RemoveTemp(name)
		}
	}()
	if err := Produce(ctx, src, name); err != nil {
		return 0, err
	}
	f, err := os.Open(name)
	if err != nil {
		return 0, err
	}
	st, err := f.Stat()
	if err == nil {
		err = f.Sync()
	}
	f.Close()
	if err != nil {
		return 0, err
	}
	if err := os.Rename(name, dest); err != nil {
		return 0, err
	}
	done = true
	syncDir(dir)
	return st.Size(), nil
}

// CreateTemp dir'de yedek için boş, 0600 izinli bir geçici dosya oluşturur
// (.keywordhunter-backup-*.tmp) ve yolunu döndürür. Çağıran işi bitince
// RemoveTemp ile siler; süreç öldürülürse artığı SweepStale temizler.
func CreateTemp(dir string) (string, error) {
	tmp, err := os.CreateTemp(dir, tempPrefix+"*"+tempSuffix)
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

// RemoveTemp geçici yedeği ve SQLite'ın yanına açtığı günlük dosyasını siler.
func RemoveTemp(name string) {
	_ = os.Remove(name)
	_ = os.Remove(name + "-journal")
}

// SweepStale dir'deki yarım yedek artıklarını (.keywordhunter-backup-*.tmp
// ve -journal eşleri) siler: son değişikliği olderThan'dan eski olanlar.
// Kesintiye uğrayan bir yedeğin (SIGKILL, OOM, kapanma) bıraktığı tam boyutlu
// kopya hem diski doldurur hem de oturum temizliğinden önce kesildiyse canlı
// oturum kimlikleri taşıyabilir; List/Rotate bu adları görmez. Dizin yoksa
// hata değildir.
func SweepStale(dir string, olderThan time.Duration, now time.Time) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var removed []string
	var errs []error
	for _, e := range ents {
		name := e.Name()
		base := strings.TrimSuffix(name, "-journal")
		if !strings.HasPrefix(base, tempPrefix) || !strings.HasSuffix(base, tempSuffix) || !e.Type().IsRegular() {
			continue
		}
		fi, err := e.Info()
		if err != nil || now.Sub(fi.ModTime()) < olderThan {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
			continue
		}
		removed = append(removed, name)
	}
	return removed, errors.Join(errs...)
}

// syncDir yeniden adlandırmanın dizin girdisini diske yazar (en iyi çaba;
// bazı platformlar dizin fsync'ini desteklemez).
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
}

// Info dizindeki bir yedek dosyası.
type Info struct {
	Name    string    `json:"name"`
	Bytes   int64     `json:"bytes"`
	TakenAt time.Time `json:"takenAt"` // addaki UTC damga
}

// List dir'deki yedekleri yeniden eskiye döndürür (yalnız bu paketin adlandırdıkları).
func List(dir string) ([]Info, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Info
	for _, e := range ents {
		m := namePattern.FindStringSubmatch(e.Name())
		if m == nil || !e.Type().IsRegular() {
			continue
		}
		at, err := time.Parse(stampLayout, m[1])
		if err != nil {
			continue
		}
		info := Info{Name: e.Name(), TakenAt: at}
		if fi, err := e.Info(); err == nil {
			info.Bytes = fi.Size()
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].TakenAt.Equal(out[j].TakenAt) {
			return out[i].TakenAt.After(out[j].TakenAt)
		}
		return out[i].Name > out[j].Name
	})
	return out, nil
}

// Rotate en yeni keep yedeği tutar, eskileri siler ve silinen adları döndürür.
// keep < 1 ise hiçbir şey silinmez. protect boş değilse o ad (az önce yazılan
// yedek) hiçbir koşulda silinmez ve keep'e dahil sayılır: saat geri alınmışsa
// (ileri tarihli eski yedekler) yeni yedek sıralamada en eski görünse bile kalır.
func Rotate(dir string, keep int, protect string) ([]string, error) {
	if keep < 1 {
		return nil, nil
	}
	list, err := List(dir)
	if err != nil {
		return nil, err
	}
	others := list[:0:0]
	slots := keep
	for _, b := range list {
		if protect != "" && b.Name == protect {
			slots--
			continue
		}
		others = append(others, b)
	}
	var removed []string
	var errs []error
	for _, b := range others[min(max(slots, 0), len(others)):] {
		if err := os.Remove(filepath.Join(dir, b.Name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
			continue
		}
		removed = append(removed, b.Name)
	}
	return removed, errors.Join(errs...)
}

// Result zamanlanmış yedeğin sonucu.
type Result struct {
	Name      string        `json:"name"`
	Path      string        `json:"-"`
	Bytes     int64         `json:"bytes"`
	Removed   []string      `json:"removed,omitempty"`
	RotateErr error         `json:"-"` // yedek alındı ama eski yedekler silinemedi
	Swept     []string      `json:"swept,omitempty"`
	Duration  time.Duration `json:"-"`
	// NewerExisting dizinde bu yedekten YENİ tarihli yedek sayısı (saat
	// kayması belirtisi: saat geri alınmış ya da ileri tarihli dosyalar var).
	NewerExisting int `json:"newerExisting,omitempty"`
}

// Run dir'e yeni bir yedek yazar (dizin yoksa 0700 ile oluşturulur) ve
// döndürme uygular. Önce önceki kesintilerin yarım yedek artıklarını temizler
// (SweepStale). Döndürme hatası yedeği geçersiz kılmaz; Result.RotateErr'de
// döner. Yeni yazılan yedek döndürmede asla silinmez.
func Run(ctx context.Context, src Source, dir, version string, keep int, now time.Time) (Result, error) {
	start := time.Now()
	res := Result{Name: FileName(version, now)}
	if strings.TrimSpace(dir) == "" {
		return res, errors.New("yedek dizini tanımlı değil")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return res, err
	}
	// Artık temizliği en iyi çabadır: başarısızlığı yedeği engellemez.
	res.Swept, _ = SweepStale(dir, StaleTempAge, time.Now())
	res.Path = filepath.Join(dir, res.Name)
	n, err := WriteFile(ctx, src, res.Path)
	if err != nil {
		return res, err
	}
	res.Bytes = n
	if list, err := List(dir); err == nil {
		at := now.UTC().Truncate(time.Second)
		for _, b := range list {
			if b.TakenAt.After(at) {
				res.NewerExisting++
			}
		}
	}
	res.Removed, res.RotateErr = Rotate(dir, keep, res.Name)
	res.Duration = time.Since(start)
	return res, nil
}
