package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2" // #nosec G404 -- deterministik demo verisi; güvenlik amaçlı rastgelelik değildir
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"keywordhunter-mvp/pkg/search"
	"keywordhunter-mvp/pkg/storage"
)

// AdminAccount demo veritabanına yazılacak admin hesabı. PasswordHash
// (bcrypt) verilirse Password yok sayılır.
type AdminAccount struct {
	Username     string
	Password     string
	PasswordHash string
}

// Options tohumlama ayarları.
type Options struct {
	DBPath string
	Reset  bool
	Seed   uint64
	// Now zaman damgalarının referansı (testlerde sabitlenir).
	Now   time.Time
	Admin AdminAccount
	// BcryptCost parola özetlerinin maliyeti (0 → bcrypt.DefaultCost; testler düşürür).
	BcryptCost int
}

// Summary üretilen kayıt sayıları.
type Summary struct {
	Findings, Artifacts, History, Scheduled, Watchlist, WatchChecks, Engines, AuditEvents, Users int
}

// ErrNotEmpty -reset olmadan dolu bir veritabanına yazılmak istendi.
var ErrNotEmpty = errors.New("veritabanı boş değil: demo verisini yeniden kurmak için -reset kullanın")

// sqlTime SQLite CURRENT_TIMESTAMP ile aynı biçim (UTC, saniye).
func sqlTime(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05") }

// Seed veritabanını açar (gerekirse sıfırlar) ve demo verisini yazar.
func Seed(o Options) (Summary, error) {
	var sum Summary
	if o.DBPath == "" {
		return sum, errors.New("veritabanı yolu boş")
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	if o.Admin.Username == "" || (o.Admin.Password == "" && o.Admin.PasswordHash == "") {
		return sum, errors.New("admin kullanıcı adı ve parolası zorunlu")
	}
	if o.Reset {
		if err := removeDB(o.DBPath); err != nil {
			return sum, err
		}
	}

	db, err := storage.New(o.DBPath)
	if err != nil {
		return sum, err
	}
	defer db.Close()

	if n, err := db.CountUsers(); err != nil {
		return sum, err
	} else if total, _, err := db.GetStats(); err != nil {
		return sum, err
	} else if n > 0 || total > 0 {
		return sum, ErrNotEmpty
	}

	if o.BcryptCost == 0 {
		o.BcryptCost = bcrypt.DefaultCost
	}
	s := &seeder{db: db, rng: rand.New(rand.NewPCG(o.Seed, o.Seed^0x9e3779b97f4a7c15)), now: o.Now.UTC().Truncate(time.Second), cost: o.BcryptCost}
	steps := []struct {
		name string
		fn   func(*Summary) error
	}{
		{"kullanıcılar", func(sm *Summary) error { return s.users(o.Admin, sm) }},
		{"motorlar", s.engines},
		{"bulgular", s.findings},
		{"arama geçmişi", s.history},
		{"planlı taramalar", s.scheduled},
		{"izleme listesi", s.watchlist},
		{"denetim kaydı", s.audit},
	}
	for _, st := range steps {
		if err := st.fn(&sum); err != nil {
			return sum, fmt.Errorf("%s: %w", st.name, err)
		}
	}
	return sum, nil
}

// removeDB veritabanı dosyasını ve SQLite yan dosyalarını siler. Yalnız
// düzenli dosyalara dokunur (yanlışlıkla bir dizin verilirse reddeder).
func removeDB(path string) error {
	for _, p := range []string{path, path + "-wal", path + "-shm", path + "-journal"} {
		fi, err := os.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("%s düzenli bir dosya değil; silinmedi", p)
		}
		if err := os.Remove(p); err != nil {
			return err
		}
	}
	return nil
}

type seeder struct {
	db   *storage.DB
	rng  *rand.Rand
	now  time.Time
	cost int // bcrypt maliyeti

	admin   string
	userIDs map[string]int64
	seeded  []seededFinding
	audits  []storage.AuditEvent
}

type seededFinding struct {
	ID       int64
	Query    string
	Title    string
	Crit     int
	Status   string
	Created  time.Time
	Artifact int
}

func (s *seeder) pick(list []string) string { return list[s.rng.IntN(len(list))] }

// ago şimdiden geriye verilen süre.
func (s *seeder) ago(d time.Duration) time.Time { return s.now.Add(-d) }

// onion deterministik v3 biçimli (56 karakter base32) kurgusal adres üretir.
func (s *seeder) onion() string {
	const b32 = "abcdefghijklmnopqrstuvwxyz234567"
	var b strings.Builder
	for i := 0; i < 55; i++ {
		b.WriteByte(b32[s.rng.IntN(len(b32))])
	}
	b.WriteByte('d') // v3 adresleri "d" ile biter (sürüm baytı)
	return b.String() + ".onion"
}

func (s *seeder) hexStr(n int) string {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = byte(s.rng.IntN(256))
	}
	return hex.EncodeToString(buf)
}

// fill başlık şablonundaki %s (kurum) ve %d (sayı) yer tutucularını doldurur.
func (s *seeder) fill(tmpl, org string) string {
	var b strings.Builder
	for i := 0; i < len(tmpl); i++ {
		if tmpl[i] != '%' || i+1 >= len(tmpl) {
			b.WriteByte(tmpl[i])
			continue
		}
		i++
		switch tmpl[i] {
		case 's':
			b.WriteString(org)
		case 'd':
			b.WriteString(strconv.Itoa(2 + s.rng.IntN(480)))
		default:
			b.WriteByte(tmpl[i])
		}
	}
	return b.String()
}

// ── Kullanıcılar ──────────────────────────────────────────────────────────

func (s *seeder) users(admin AdminAccount, sum *Summary) error {
	s.userIDs = map[string]int64{}
	s.admin = admin.Username
	hash := admin.PasswordHash
	if hash == "" {
		h, err := bcrypt.GenerateFromPassword([]byte(admin.Password), s.cost)
		if err != nil {
			return err
		}
		hash = string(h)
	}
	accounts := []struct{ name, hash, role string }{{admin.Username, hash, storage.RoleAdmin}}
	for _, u := range DemoUsers {
		if strings.EqualFold(u.Username, admin.Username) {
			return fmt.Errorf("admin kullanıcı adı %q bir demo hesabıyla çakışıyor", admin.Username)
		}
		h, err := bcrypt.GenerateFromPassword([]byte(u.Password), s.cost)
		if err != nil {
			return err
		}
		accounts = append(accounts, struct{ name, hash, role string }{u.Username, string(h), u.Role})
	}
	conn := s.db.GetDBConn()
	for i, a := range accounts {
		u, err := s.db.CreateUser(a.name, a.hash, a.role)
		if err != nil {
			return err
		}
		s.userIDs[a.name] = u.ID
		// Hesaplar 21 gün önce açılmış gibi görünsün (admin önce).
		created := s.ago(21*24*time.Hour - time.Duration(i)*26*time.Hour)
		if _, err := conn.Exec(`UPDATE users SET created_at = ? WHERE id = ?`, sqlTime(created), u.ID); err != nil {
			return err
		}
		sum.Users++
	}
	adminName := admin.Username
	s.addAudit(storage.AuditEvent{Time: s.ago(21 * 24 * time.Hour), Actor: "system", AuthMethod: storage.AuthMethodSystem,
		Action: "user.bootstrap", Target: "user:" + adminName, Success: true}, map[string]any{"role": storage.RoleAdmin})
	for i, u := range DemoUsers {
		s.addAudit(storage.AuditEvent{Time: s.ago(21*24*time.Hour - time.Duration(i+1)*26*time.Hour), Actor: adminName, Role: storage.RoleAdmin,
			AuthMethod: storage.AuthMethodSession, IP: "192.0.2.10", Action: "user.create", Target: "user:" + u.Username, Success: true},
			map[string]any{"role": u.Role, "userId": s.userIDs[u.Username]})
	}
	return nil
}

// ── Motor istatistikleri ─────────────────────────────────────────────────

func (s *seeder) engines(sum *Summary) error {
	conn := s.db.GetDBConn()
	for _, e := range search.SearchEngines {
		if err := s.db.UpsertEngineStat(e.Name, e.URL, e.DefaultActive); err != nil {
			return err
		}
		ok, fail, total := 0, 0, 0
		status, ms := "up", 900+s.rng.IntN(2600)
		if e.DefaultActive {
			ok = 180 + s.rng.IntN(240)
			fail = s.rng.IntN(ok / 12)
			total = ok * (8 + s.rng.IntN(22))
		} else {
			// Varsayılan kapalı motorlar: notlarındaki gerekçeyle tutarlı olarak düşük.
			fail = 20 + s.rng.IntN(40)
			ok = s.rng.IntN(5)
			status, ms = "down", 0
		}
		consecutive := 0
		if status == "down" {
			consecutive = 3 + s.rng.IntN(9)
		}
		checked := s.ago(time.Duration(1+s.rng.IntN(4)) * time.Minute)
		if _, err := conn.Exec(`UPDATE engine_stats SET last_checked_at = ?, last_status = ?, last_response_ms = ?,
			success_count = ?, fail_count = ?, total_results = ?, consecutive_fails = ?, added_at = ? WHERE name = ?`,
			checked.UTC(), status, ms, ok, fail, total, consecutive, sqlTime(s.ago(30*24*time.Hour)), e.Name); err != nil {
			return err
		}
		sum.Engines++
	}
	return nil
}

// activeEngines bulguların dağıtılacağı (varsayılan açık) motor adları.
func activeEngines() []string {
	var out []string
	for _, e := range search.SearchEngines {
		if e.DefaultActive {
			out = append(out, e.Name)
		}
	}
	return out
}

// ── Bulgular + etiketler + IOC + vaka durumu ─────────────────────────────

func (s *seeder) findings(sum *Summary) error {
	engines := activeEngines()
	if len(engines) == 0 {
		return errors.New("aktif motor yok")
	}
	conn := s.db.GetDBConn()
	statuses := []string{"", "", "new", "investigating", "confirmed", "dismissed"}
	for _, q := range querySpecs {
		host := s.onion()
		hosts := []string{host, s.onion(), s.onion()}
		for i := 0; i < q.Count; i++ {
			title := s.fill(q.Titles[i%len(q.Titles)], q.Org)
			if i >= len(q.Titles) {
				title += " #" + strconv.Itoa(i/len(q.Titles)+1)
			}
			url := "http://" + hosts[s.rng.IntN(len(hosts))] + "/" + []string{"thread", "post", "item", "leak", "t"}[s.rng.IntN(5)] + "/" + strconv.Itoa(1000+s.rng.IntN(90000))
			engine := engines[s.rng.IntN(len(engines))]
			crit := q.BaseCrit + s.rng.IntN(3) - 1
			crit = max(1, min(5, crit))

			var ids []int64
			if _, err := s.db.SaveResultsWithIDs([]storage.SearchResult{{Title: title, URL: url, Source: engine, Query: q.Query}}, &ids); err != nil {
				return err
			}
			if len(ids) == 0 {
				continue // (url, motor, sorgu) çakışması: deterministik olarak atlanır
			}
			id := ids[0]

			// Etiketler: kategori etiketlerinden 2–4 tanesi (sıra korunur, tekrar yok).
			n := 2 + s.rng.IntN(3)
			perm := s.rng.Perm(len(q.Tags))[:min(n, len(q.Tags))]
			sort.Ints(perm)
			tags := make([]string, 0, len(perm))
			for _, k := range perm {
				tags = append(tags, q.Tags[k])
			}
			if err := s.db.ApplyTagging(id, strings.Join(tags, ", "), 1+s.rng.IntN(14), crit, q.Category); err != nil {
				return err
			}

			// Vaka durumu: yüksek kritiklikte inceleme olasılığı daha yüksek.
			status := statuses[s.rng.IntN(len(statuses))]
			if crit >= 5 && status == "" {
				status = "investigating"
			}
			note := ""
			if status != "" && s.rng.IntN(3) == 0 {
				note = s.pick(analystNotes)
			}
			if status != "" || note != "" {
				if _, err := s.db.UpdateResultCase(id, status, note); err != nil {
					return err
				}
			}

			arts := s.artifacts(id, q)
			if len(arts) > 0 {
				saved, err := s.db.ReplaceArtifacts(id, arts)
				if err != nil {
					return err
				}
				sum.Artifacts += saved
			}

			// Zaman: son 30 gün, yakın geçmişe yoğun (pano "son 24 saat" dolu gelsin).
			h := math.Pow(s.rng.Float64(), 1.7) * 30 * 24
			created := s.ago(time.Duration(h*float64(time.Hour)) + time.Duration(s.rng.IntN(3600))*time.Second)
			if _, err := conn.Exec(`UPDATE search_results SET created_at = ?, feed_at = ? WHERE id = ?`, sqlTime(created), sqlTime(created), id); err != nil {
				return err
			}
			if _, err := conn.Exec(`UPDATE result_artifacts SET created_at = ? WHERE result_id = ?`, sqlTime(created), id); err != nil {
				return err
			}
			if _, err := conn.Exec(`UPDATE graph_nodes SET discovered_at = ? WHERE url = ?`, sqlTime(created), url); err != nil {
				return err
			}
			s.seeded = append(s.seeded, seededFinding{ID: id, Query: q.Query, Title: title, Crit: crit, Status: status, Created: created, Artifact: len(arts)})
			sum.Findings++
		}
	}
	return nil
}

// artifacts bulgu için kategoriyle uyumlu kurgusal IOC'ler üretir (~%65'inde).
func (s *seeder) artifacts(id int64, q querySpec) []storage.ResultArtifact {
	if s.rng.IntN(100) >= 65 {
		return nil
	}
	var out []storage.ResultArtifact
	add := func(typ, val, ctx string, conf float64) {
		out = append(out, storage.ResultArtifact{ResultID: id, Type: typ, Value: val, Context: ctx, Confidence: conf})
	}
	first := []string{"ahmet", "ayse", "mehmet", "zeynep", "can", "elif", "murat", "selin"}
	last := []string{"yilmaz", "kaya", "demir", "sahin", "celik", "arslan"}
	for i := 0; i < 1+s.rng.IntN(3); i++ {
		add("email", s.pick(first)+"."+s.pick(last)+"@"+q.Domain, "iletişim / örnek kayıt", 0.8)
	}
	switch q.Category {
	case "Finansal Dolandırıcılık", "Illegal Market", "Ransomware":
		add("bitcoin", "bc1q"+strings.ToLower(s.hexStr(19))[:38], "ödeme adresi", 0.9)
	case "Initial Access", "Exploit / Vulnerability":
		add("ip", fmt.Sprintf("203.0.113.%d", 1+s.rng.IntN(254)), "hedef sunucu", 0.7)
		add("hash", s.hexStr(32), "örnek dosya SHA-256", 0.6)
	}
	if s.rng.IntN(3) == 0 {
		add("onion", s.onion(), "yedek adres", 0.9)
	}
	if s.rng.IntN(4) == 0 {
		add("phone", fmt.Sprintf("+90 5%02d %03d %02d %02d", 30+s.rng.IntN(25), s.rng.IntN(1000), s.rng.IntN(100), s.rng.IntN(100)), "satıcı iletişim", 0.5)
	}
	if s.rng.IntN(3) == 0 {
		add("username", s.pick([]string{"darkseller", "kuzgun", "ghost_tr", "anka", "z3r0", "boran"})+strconv.Itoa(s.rng.IntN(99)), "forum takma adı", 0.6)
	}
	return out
}

// ── Arama geçmişi ────────────────────────────────────────────────────────

func (s *seeder) history(sum *Summary) error {
	conn := s.db.GetDBConn()
	type entry struct {
		q   string
		n   int
		at  time.Time
		who string
	}
	var list []entry
	for _, q := range querySpecs {
		runs := 2 + s.rng.IntN(4)
		for i := 0; i < runs; i++ {
			at := s.ago(time.Duration(s.rng.IntN(30*24))*time.Hour + time.Duration(s.rng.IntN(60))*time.Minute)
			list = append(list, entry{q.Query, 3 + s.rng.IntN(q.Count+12), at, s.pick([]string{"analyst", "analyst", s.adminName()})})
		}
	}
	// Kronolojik sıra: kimlikler zamana göre artsın (geçmiş listesi id'ye göre sıralanabilir).
	sort.SliceStable(list, func(i, j int) bool { return list[i].at.Before(list[j].at) })
	for _, e := range list {
		if _, err := conn.Exec(`INSERT INTO search_history (query, result_count, searched_at) VALUES (?, ?, ?)`, e.q, e.n, sqlTime(e.at)); err != nil {
			return err
		}
		role := storage.RoleAnalyst
		if e.who != "analyst" {
			role = storage.RoleAdmin
		}
		s.addAudit(storage.AuditEvent{Time: e.at, Actor: e.who, Role: role, AuthMethod: storage.AuthMethodSession, IP: "192.0.2.21",
			Action: "search.run", Target: e.q, Success: true},
			map[string]any{"pages": 1, "found": e.n, "newSaved": e.n / 3, "durationMs": 20000 + s.rng.IntN(60000)})
		sum.History++
	}
	return nil
}

func (s *seeder) adminName() string { return s.admin }

// ── Planlı taramalar ─────────────────────────────────────────────────────

// seedMinLead, tohumlanan etkin planlı taramaların en erken çalışma zamanıdır
// (tohumlamadan itibaren); demo ve e2e koşuları bundan çok daha kısa sürer.
const seedMinLead = 2 * time.Hour

func (s *seeder) scheduled(sum *Summary) error {
	conn := s.db.GetDBConn()
	for i, sp := range scheduledSpecs {
		ss, err := s.db.CreateScheduledSearch(sp.Query, sp.Interval, "", sp.Threshold)
		if err != nil {
			return err
		}
		created := s.ago(time.Duration(20-i*3) * 24 * time.Hour)
		if sp.Runs > 0 {
			last := s.ago(time.Duration(5+s.rng.IntN(sp.Interval)) * time.Minute)
			// Bir sonraki çalışma en az seedMinLead sonra: kısa aralıklı bir
			// tarama demo/e2e koşusunun ortasında Tor'a gidip sayaçları,
			// geçmişi ve denetim kaydını değiştirmesin.
			next := last.Add(time.Duration(sp.Interval) * time.Minute)
			if minNext := s.now.Add(seedMinLead); next.Before(minNext) {
				next = minNext.Add(time.Duration(s.rng.IntN(30)) * time.Minute)
			}
			total := 3 + s.rng.IntN(20)
			if _, err := conn.Exec(`UPDATE scheduled_searches SET last_run_at = ?, next_run_at = ?, last_result_count = ?, last_new_count = ?, total_runs = ?, created_at = ? WHERE id = ?`,
				last.UTC(), next.UTC(), total, s.rng.IntN(4), sp.Runs*(4+s.rng.IntN(30)), sqlTime(created), ss.ID); err != nil {
				return err
			}
		} else if _, err := conn.Exec(`UPDATE scheduled_searches SET created_at = ? WHERE id = ?`, sqlTime(created), ss.ID); err != nil {
			return err
		}
		if !sp.Enabled {
			if _, err := s.db.ToggleScheduledSearch(ss.ID); err != nil {
				return err
			}
		}
		s.addAudit(storage.AuditEvent{Time: created, Actor: "analyst", Role: storage.RoleAnalyst, AuthMethod: storage.AuthMethodSession, IP: "192.0.2.21",
			Action: "scheduled.create", Target: sp.Query, Success: true},
			map[string]any{"intervalMinutes": sp.Interval, "alertThreshold": sp.Threshold, "webhook": false})
		sum.Scheduled++
	}
	return nil
}

// ── İzleme listesi + kontrol geçmişi ────────────────────────────────────

func (s *seeder) watchlist(sum *Summary) error {
	conn := s.db.GetDBConn()
	const checks = 24 // son 6 saat, 15 dakikada bir
	for i, w := range watchSpecs {
		url := "http://" + s.onion()
		id, err := s.db.AddWatchlistItem(w.Name, url, w.Category, w.Notes)
		if err != nil {
			return err
		}
		added := s.ago(time.Duration(14-i) * 24 * time.Hour)
		if _, err := conn.Exec(`UPDATE watchlist SET created_at = ? WHERE id = ?`, sqlTime(added), id); err != nil {
			return err
		}
		hash := s.hexStr(32)
		for k := checks; k >= 1; k-- {
			at := s.ago(time.Duration(k)*15*time.Minute - time.Duration(s.rng.IntN(90))*time.Second)
			up := s.rng.Float64() < w.Uptime
			if k == 1 {
				// Son durum gerçekçi: etkin ve genelde ayakta olan hedefler şu an
				// ayakta, kapatılmış (terk edilmiş) ayna düşük.
				up = w.Enabled && w.Uptime > 0.5
			}
			status, code, ms, title, changed := "down", 0, 0, "", false
			if up {
				status, code, ms, title = "up", 200, 1500+s.rng.IntN(6000), w.Name
				if w.Changes && k%9 == 0 {
					hash, changed = s.hexStr(32), true
				}
			}
			if err := s.db.RecordWatchlistCheck(id, status, code, ms, hash, title, changed); err != nil {
				return err
			}
			if _, err := conn.Exec(`UPDATE watchlist_checks SET checked_at = ? WHERE id = (SELECT MAX(id) FROM watchlist_checks WHERE watchlist_id = ?)`, sqlTime(at), id); err != nil {
				return err
			}
			sum.WatchChecks++
		}
		if !w.Enabled {
			if err := s.db.SetWatchlistEnabled(id, false); err != nil {
				return err
			}
		}
		s.addAudit(storage.AuditEvent{Time: added, Actor: "analyst", Role: storage.RoleAnalyst, AuthMethod: storage.AuthMethodSession, IP: "192.0.2.21",
			Action: "watchlist.add", Target: url, Success: true}, map[string]any{"name": w.Name})
		sum.Watchlist++
	}
	return nil
}

// ── Denetim kaydı ────────────────────────────────────────────────────────

// addAudit olayı sıraya alır; audit() hepsini kronolojik sırayla zincire ekler.
func (s *seeder) addAudit(e storage.AuditEvent, details map[string]any) {
	if details != nil {
		if b, err := json.Marshal(details); err == nil {
			e.Details = string(b)
		}
	}
	s.audits = append(s.audits, e)
}

// audit girişleri, vaka güncellemelerini ve birkaç dışa aktarımı ekleyip tüm
// olayları normal yol olan AppendAuditEvent ile zamana göre zincirler.
func (s *seeder) audit(sum *Summary) error {
	admin := s.adminName()
	// Son 7 günün oturum açılışları (bir de hatalı deneme).
	for d := 7; d >= 1; d-- {
		day := s.ago(time.Duration(d)*24*time.Hour + 9*time.Hour)
		s.addAudit(storage.AuditEvent{Time: day.Add(time.Duration(s.rng.IntN(40)) * time.Minute), Actor: "analyst", Role: storage.RoleAnalyst,
			AuthMethod: storage.AuthMethodSession, IP: "192.0.2.21", Action: "auth.login.success", Target: "user:analyst", Success: true}, nil)
		if d%2 == 1 {
			s.addAudit(storage.AuditEvent{Time: day.Add(time.Duration(60+s.rng.IntN(60)) * time.Minute), Actor: admin, Role: storage.RoleAdmin,
				AuthMethod: storage.AuthMethodSession, IP: "192.0.2.10", Action: "auth.login.success", Target: "user:" + admin, Success: true}, nil)
		}
		if d%3 == 0 {
			s.addAudit(storage.AuditEvent{Time: day.Add(time.Duration(120+s.rng.IntN(60)) * time.Minute), Actor: "viewer", Role: storage.RoleViewer,
				AuthMethod: storage.AuthMethodSession, IP: "198.51.100.7", Action: "auth.login.success", Target: "user:viewer", Success: true}, nil)
		}
	}
	s.addAudit(storage.AuditEvent{Time: s.ago(2*24*time.Hour + 3*time.Hour), IP: "198.51.100.66", AuthMethod: storage.AuthMethodAnonymous,
		Action: "auth.login.failure", Target: "user:" + admin, Success: false}, map[string]any{"reason": "bad_credentials"})

	for _, f := range s.seeded {
		if f.Status == "" || f.Status == "new" {
			continue
		}
		at := f.Created.Add(time.Duration(1+s.rng.IntN(20)) * time.Hour)
		if at.After(s.now) {
			at = s.now.Add(-time.Duration(1+s.rng.IntN(30)) * time.Minute)
		}
		s.addAudit(storage.AuditEvent{Time: at, Actor: "analyst", Role: storage.RoleAnalyst, AuthMethod: storage.AuthMethodSession, IP: "192.0.2.21",
			Action: "result.case.update", Target: "result:" + strconv.FormatInt(f.ID, 10), Success: true}, map[string]any{"status": f.Status})
	}
	s.addAudit(storage.AuditEvent{Time: s.ago(3*24*time.Hour + 2*time.Hour), Actor: "analyst", Role: storage.RoleAnalyst, AuthMethod: storage.AuthMethodSession,
		IP: "192.0.2.21", Action: "export.stix", Target: "results", Success: true}, map[string]any{"count": 42, "minCriticality": 4})
	s.addAudit(storage.AuditEvent{Time: s.ago(5 * 24 * time.Hour), Actor: admin, Role: storage.RoleAdmin, AuthMethod: storage.AuthMethodSession,
		IP: "192.0.2.10", Action: "settings.env.update", Target: "env", Success: true}, map[string]any{"keys": []string{"WATCHLIST_INTERVAL_MIN"}})

	sort.SliceStable(s.audits, func(i, j int) bool { return s.audits[i].Time.Before(s.audits[j].Time) })
	for _, e := range s.audits {
		if _, err := s.db.AppendAuditEvent(e); err != nil {
			return fmt.Errorf("%s: %w", e.Action, err)
		}
		sum.AuditEvents++
	}
	return nil
}
