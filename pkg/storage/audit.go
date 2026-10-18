package storage

// Denetim kaydı (audit log): "kim, ne zaman, nereden, ne yaptı".
//
// Kayıt yalnız eklenebilir ve kurcalamaya karşı iz bırakır (tamper-evident):
//
//  1. Hash zinciri — her satırın hash'i sha256(prev_hash || kanonik alanlar)
//     olarak hesaplanır; prev_hash bir önceki satırın hash'idir. Bir satırın
//     herhangi bir alanı değiştirilir, ortadan bir satır silinir ya da araya
//     satır eklenirse VerifyAuditChain ilk kırık satırın kimliğini döndürür.
//  2. Zincir durumu (audit_chain_state, tek satır) — "çapa" (anchor) saklama
//     budamasıyla silinen son satırın kimliği+hash'i, "baş" (head) eklenen son
//     satırın kimliği+hash'idir. Doğrulama çapadan başlar, başta biter: en eski
//     satırları çapayı güncellemeden silmek ya da en yeni satırları sessizce
//     kırpmak da yakalanır.
//  3. SQLite tetikleyicileri — UPDATE her zaman, DELETE yalnız saklama budaması
//     sırasında kabul edilir. Bu, uygulama kodunun kaydı kazara değiştirmesini
//     önler; veritabanı dosyasına doğrudan erişen birine karşı ise zincir
//     doğrulaması devreye girer.
//
// Not: dosyaya tam erişimi olan biri tüm zinciri baştan hesaplayabilir; bunu
// yakalamak için baş hash'i (head) periyodik olarak harici bir yere (SIEM,
// bilet sistemi) not edin — dışa aktarılan CSV her satırın hash'ini taşır.

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Denetim kaydı sınırları: alanlar sınırlıdır ki saldırgan kontrollü metin
// (dark web sorgusu, kullanıcı adı denemesi) tabloyu şişiremesin.
const (
	MaxAuditDetailsBytes = 4096
	maxAuditActorLen     = 128
	maxAuditTargetLen    = 512
	maxAuditActionLen    = 64
	maxAuditIPLen        = 64
	auditBatchSize       = 1000
	auditHashVersion     = "kh-audit-v1"
)

// AuditTimeLayout satır zaman damgası biçimi: UTC, sabit 9 hane kesir. Sabit
// genişlik sayesinde metin karşılaştırması kronolojik sıralamaya eşittir
// (aralık filtresi indeksle çalışır) ve hash girdisi tekrar üretilebilir.
const AuditTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

// Kimlik doğrulama yöntemleri.
const (
	AuthMethodSession   = "session"
	AuthMethodToken     = "token"
	AuthMethodSystem    = "system"
	AuthMethodAnonymous = "anonymous"
)

// ErrInvalidAuditAction eylem kodu boş ya da noktalı küçük harf biçiminde değil.
var ErrInvalidAuditAction = errors.New("geçersiz denetim eylem kodu")

// AuditEvent tek bir denetim olayı.
type AuditEvent struct {
	ID int64 `json:"id"`
	// TS veritabanındaki birebir zaman damgası metni (AuditTimeLayout, sabit 9
	// hane kesir): hash girdisidir; JSON dışa aktarmada zinciri harici olarak
	// yeniden hesaplamak için bu değer kullanılır.
	TS         string    `json:"ts"`
	Time       time.Time `json:"-"`
	Actor      string    `json:"actor"`
	Role       string    `json:"role"`
	AuthMethod string    `json:"authMethod"`
	IP         string    `json:"ip"`
	Action     string    `json:"action"` // noktalı kod: auth.login.success
	Target     string    `json:"target"`
	Details    string    `json:"details"` // kompakt JSON (≤4KB), asla sır içermez
	Success    bool      `json:"success"`
	PrevHash   string    `json:"prevHash"`
	Hash       string    `json:"hash"`
}

// AuditFilter listeleme/sayma/dışa aktarma filtresi.
type AuditFilter struct {
	Actor          string   // tam eşleşme (büyük/küçük harf duyarsız)
	ActionPrefixes []string // herhangi biriyle başlayan eylemler ("auth.", "user.")
	Success        *bool
	From, To       time.Time // [From, To)
	Text           string    // hedef / ayrıntı / IP içinde serbest metin
	Limit, Offset  int
	AfterID        int64 // yalnız id > AfterID (artan sıralı dışa aktarma)
	Ascending      bool
}

// AuditChainStatus zincir doğrulamasının ayrıntılı sonucu.
type AuditChainStatus struct {
	OK          bool      `json:"ok"`
	BrokenAt    int64     `json:"brokenAt,omitempty"`
	Reason      string    `json:"reason,omitempty"` // hash | link | gap | anchor | head | extra
	Checked     int       `json:"checked"`
	AnchorID    int64     `json:"anchorId"`
	HeadID      int64     `json:"headId"`
	HeadHash    string    `json:"headHash"`
	PrunedTotal int64     `json:"prunedTotal"`
	CheckedAt   time.Time `json:"checkedAt"`
}

// AuditStats başlık kartları için özet.
type AuditStats struct {
	Total      int `json:"total"`
	Failures24 int `json:"failures24h"`
	Actors24   int `json:"actors24h"`
}

// AppendAuditEvent olayı zincirin sonuna ekler ve kimlik/zaman/hash alanları
// doldurulmuş hâlini döndürür. e.Time sıfırsa şimdiki zaman kullanılır.
func (db *DB) AppendAuditEvent(e AuditEvent) (AuditEvent, error) {
	if err := normalizeAuditEvent(&e); err != nil {
		return AuditEvent{}, err
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	e.Time = e.Time.UTC()
	e.TS = e.Time.Format(AuditTimeLayout)

	db.auditMu.Lock()
	defer db.auditMu.Unlock()

	tx, err := db.conn.Begin()
	if err != nil {
		return AuditEvent{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var headID int64
	var headHash string
	if err := tx.QueryRow(`SELECT head_id, head_hash FROM audit_chain_state WHERE id = 1`).Scan(&headID, &headHash); err != nil {
		return AuditEvent{}, fmt.Errorf("zincir durumu okunamadı: %w", err)
	}
	e.ID = headID + 1
	e.PrevHash = headHash
	e.Hash = auditHash(e.PrevHash, &e)

	if _, err := tx.Exec(`INSERT INTO audit_events
		(id, ts, actor, role, auth_method, ip, action, target, details, success, prev_hash, hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.TS, e.Actor, e.Role, e.AuthMethod, e.IP, e.Action, e.Target, e.Details, boolInt(e.Success), e.PrevHash, e.Hash); err != nil {
		return AuditEvent{}, err
	}
	if _, err := tx.Exec(`UPDATE audit_chain_state SET head_id = ?, head_hash = ? WHERE id = 1`, e.ID, e.Hash); err != nil {
		return AuditEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return AuditEvent{}, err
	}
	return e, nil
}

// normalizeAuditEvent alanları sınırlar, kontrol karakterlerini atar ve
// ayrıntıyı geçerli, kompakt, ≤4KB JSON'a indirger.
func normalizeAuditEvent(e *AuditEvent) error {
	e.Action = strings.TrimSpace(e.Action)
	if !validAuditAction(e.Action) {
		return ErrInvalidAuditAction
	}
	e.Actor = cleanAuditText(e.Actor, maxAuditActorLen)
	e.Role = cleanAuditText(e.Role, 16)
	e.IP = cleanAuditText(e.IP, maxAuditIPLen)
	e.Target = cleanAuditText(e.Target, maxAuditTargetLen)
	switch e.AuthMethod {
	case AuthMethodSession, AuthMethodToken, AuthMethodSystem, AuthMethodAnonymous:
	default:
		e.AuthMethod = AuthMethodAnonymous
	}
	e.Details = normalizeAuditDetails(e.Details)
	return nil
}

// validAuditAction "auth.login.success" biçimini doğrular: küçük harf, rakam,
// nokta, alt çizgi ve tire; nokta ile başlayıp bitemez.
func validAuditAction(a string) bool {
	if a == "" || len(a) > maxAuditActionLen || a[0] == '.' || a[len(a)-1] == '.' {
		return false
	}
	for i := 0; i < len(a); i++ {
		c := a[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

// cleanAuditText geçersiz UTF-8'i onarır, kontrol karakterlerini (log/CSV
// enjeksiyonu: \r \n \x1b…) boşluğa çevirir ve rune sınırına kırpar.
func cleanAuditText(s string, maxRunes int) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > maxRunes {
		s = string([]rune(s)[:maxRunes])
	}
	return s
}

// normalizeAuditDetails son savunma hattı: geçersiz JSON düz metin olarak
// sarılır, sınırı aşan ayrıntı yalnız boyut bilgisine indirgenir.
func normalizeAuditDetails(d string) string {
	d = strings.TrimSpace(d)
	if d == "" || d == "{}" || d == "null" {
		return ""
	}
	if !json.Valid([]byte(d)) {
		b, _ := json.Marshal(map[string]string{"raw": cleanAuditText(d, 256)})
		d = string(b)
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(d)); err == nil {
		d = buf.String()
	}
	if len(d) > MaxAuditDetailsBytes {
		d = `{"truncated":true,"bytes":` + strconv.Itoa(len(d)) + `}`
	}
	return d
}

// auditHash kanonik satır özetini üretir. Her alan uzunluk önekli yazılır
// ("5:admin|") ki alan sınırları kaydırılarak aynı hash elde edilemesin.
func auditHash(prev string, e *AuditEvent) string {
	h := sha256.New()
	field := func(s string) {
		h.Write([]byte(strconv.Itoa(len(s))))
		h.Write([]byte{':'})
		h.Write([]byte(s))
		h.Write([]byte{'|'})
	}
	field(auditHashVersion)
	field(prev)
	field(strconv.FormatInt(e.ID, 10))
	field(e.TS)
	field(e.Actor)
	field(e.Role)
	field(e.AuthMethod)
	field(e.IP)
	field(e.Action)
	field(e.Target)
	field(e.Details)
	if e.Success {
		field("1")
	} else {
		field("0")
	}
	return hex.EncodeToString(h.Sum(nil))
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

const auditColumns = `id, ts, actor, role, auth_method, ip, action, target, details, success, prev_hash, hash`

type rowScanner interface{ Scan(dest ...any) error }

func scanAuditEvent(r rowScanner) (AuditEvent, error) {
	var e AuditEvent
	var success int
	if err := r.Scan(&e.ID, &e.TS, &e.Actor, &e.Role, &e.AuthMethod, &e.IP, &e.Action, &e.Target, &e.Details, &success, &e.PrevHash, &e.Hash); err != nil {
		return e, err
	}
	e.Success = success == 1
	if t, err := time.Parse(time.RFC3339Nano, e.TS); err == nil {
		e.Time = t
	}
	return e, nil
}

// whereClause filtreyi parametreli SQL koşuluna çevirir (değerler asla
// sorguya gömülmez).
func (f AuditFilter) whereClause() (string, []any) {
	var conds []string
	var args []any
	if a := strings.TrimSpace(f.Actor); a != "" {
		conds = append(conds, "actor = ? COLLATE NOCASE")
		args = append(args, a)
	}
	if len(f.ActionPrefixes) > 0 {
		var ors []string
		for _, p := range f.ActionPrefixes {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			// Aralık sorgusu (indeks dostu): action >= p AND action < p+U+10FFFF
			ors = append(ors, "(action >= ? AND action < ?)")
			args = append(args, p, p+"\U0010FFFF")
		}
		if len(ors) > 0 {
			conds = append(conds, "("+strings.Join(ors, " OR ")+")")
		}
	}
	if f.Success != nil {
		conds = append(conds, "success = ?")
		args = append(args, boolInt(*f.Success))
	}
	if !f.From.IsZero() {
		conds = append(conds, "ts >= ?")
		args = append(args, f.From.UTC().Format(AuditTimeLayout))
	}
	if !f.To.IsZero() {
		conds = append(conds, "ts < ?")
		args = append(args, f.To.UTC().Format(AuditTimeLayout))
	}
	if t := strings.TrimSpace(f.Text); t != "" {
		like := "%" + escapeLike(t) + "%"
		conds = append(conds, `(target LIKE ? ESCAPE '\' OR details LIKE ? ESCAPE '\' OR ip LIKE ? ESCAPE '\')`)
		args = append(args, like, like, like)
	}
	if f.AfterID > 0 {
		conds = append(conds, "id > ?")
		args = append(args, f.AfterID)
	}
	if len(conds) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// ListAuditEvents filtreye uyan olayları (varsayılan: en yeni önce) ve toplam
// eşleşme sayısını döndürür.
func (db *DB) ListAuditEvents(f AuditFilter) ([]AuditEvent, int, error) {
	total, err := db.CountAuditEvents(f)
	if err != nil {
		return nil, 0, err
	}
	list, err := db.queryAuditEvents(f)
	if err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (db *DB) queryAuditEvents(f AuditFilter) ([]AuditEvent, error) {
	if f.Limit <= 0 || f.Limit > auditBatchSize {
		f.Limit = 50
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	where, args := f.whereClause()
	order := " ORDER BY id DESC"
	if f.Ascending {
		order = " ORDER BY id ASC"
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := db.conn.Query(`SELECT `+auditColumns+` FROM audit_events`+where+order+` LIMIT ? OFFSET ?`, args...) //nolint:gosec // G202: sütun listesi ve WHERE/ORDER sabit parçalardan kurulur; değerler parametre
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]AuditEvent, 0, f.Limit)
	for rows.Next() {
		e, err := scanAuditEvent(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, e)
	}
	return list, rows.Err()
}

// CountAuditEvents filtreye uyan olay sayısı.
func (db *DB) CountAuditEvents(f AuditFilter) (int, error) {
	where, args := f.whereClause()
	var n int
	err := db.conn.QueryRow(`SELECT COUNT(*) FROM audit_events`+where, args...).Scan(&n)
	return n, err
}

// EachAuditEvent filtreye uyan en yeni max olayı kronolojik (artan) sırayla
// fn'e verir. Satırlar parti parti okunur ve her parti kapatıldıktan sonra fn
// çağrılır; tek bağlantılı havuz yavaş bir istemci yüzünden kilitlenmez.
func (db *DB) EachAuditEvent(f AuditFilter, max int, fn func(AuditEvent) error) error {
	if max <= 0 {
		return nil
	}
	f.AfterID = 0
	// İlk satır: eşleşen en yeni max kaydın en eskisi
	where, args := f.whereClause()
	var startID int64
	err := db.conn.QueryRow(`SELECT id FROM audit_events`+where+` ORDER BY id DESC LIMIT 1 OFFSET ?`, append(args, max-1)...).Scan(&startID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		startID = 0 // max'tan az kayıt var: hepsi
	case err != nil:
		return err
	default:
		startID-- // id > startID-1 ⇒ startID dahil
	}
	f.AfterID = startID
	f.Ascending = true
	f.Offset = 0
	f.Limit = auditBatchSize
	sent := 0
	for sent < max {
		batch, err := db.queryAuditEvents(f)
		if err != nil {
			return err
		}
		for _, e := range batch {
			if sent >= max {
				return nil
			}
			if err := fn(e); err != nil {
				return err
			}
			sent++
		}
		if len(batch) < auditBatchSize {
			return nil
		}
		f.AfterID = batch[len(batch)-1].ID
	}
	return nil
}

// DistinctAuditActors filtre öneri listesi için kayıtlı aktörler.
func (db *DB) DistinctAuditActors(limit int) ([]string, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := db.conn.Query(`SELECT DISTINCT actor FROM audit_events WHERE actor <> '' ORDER BY actor COLLATE NOCASE LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAuditStats toplam olay, son 24 saatteki başarısız olay ve farklı aktör sayısı.
func (db *DB) GetAuditStats(now time.Time) (AuditStats, error) {
	var st AuditStats
	since := now.Add(-24 * time.Hour).UTC().Format(AuditTimeLayout)
	if err := db.conn.QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&st.Total); err != nil {
		return st, err
	}
	err := db.conn.QueryRow(`SELECT COALESCE(SUM(CASE WHEN success = 0 THEN 1 ELSE 0 END), 0),
		COUNT(DISTINCT CASE WHEN actor <> '' THEN lower(actor) END)
		FROM audit_events WHERE ts >= ?`, since).Scan(&st.Failures24, &st.Actors24)
	return st, err
}

type auditChainState struct {
	anchorID, headID     int64
	anchorHash, headHash string
	prunedTotal          int64
}

func (db *DB) readAuditChainState() (auditChainState, error) {
	var s auditChainState
	err := db.conn.QueryRow(`SELECT anchor_id, anchor_hash, head_id, head_hash, pruned_total FROM audit_chain_state WHERE id = 1`).
		Scan(&s.anchorID, &s.anchorHash, &s.headID, &s.headHash, &s.prunedTotal)
	return s, err
}

// VerifyAuditChain tüm zinciri doğrular; kırık varsa ilk kırık satırın
// kimliğini döndürür (sondan kırpma durumunda: eksik ilk kimlik).
func (db *DB) VerifyAuditChain() (ok bool, firstBrokenID int64, err error) {
	st, err := db.AuditChainReport()
	if err != nil {
		return false, 0, err
	}
	return st.OK, st.BrokenAt, nil
}

// AuditChainReport zinciri çapadan başa doğrular ve ayrıntılı rapor döndürür.
// Satırlar parti parti okunur. Eşzamanlı bir budama doğrulamanın ortasında
// en eski satırları silmiş olabileceğinden, kırık bulunursa sonuç zincir
// kilidi altında bir kez daha doğrulanır (yanlış alarm olmaz).
func (db *DB) AuditChainReport() (AuditChainStatus, error) {
	db.auditMu.Lock()
	st, err := db.readAuditChainState()
	db.auditMu.Unlock()
	if err != nil {
		return AuditChainStatus{}, err
	}
	res, err := db.walkAuditChain(st, true)
	if err != nil || res.OK {
		return res, err
	}
	db.auditMu.Lock()
	defer db.auditMu.Unlock()
	if st, err = db.readAuditChainState(); err != nil {
		return AuditChainStatus{}, err
	}
	return db.walkAuditChain(st, true)
}

// ExtendAuditChainReport daha önce geçerli bulunmuş bir rapordan sonra eklenen
// satırları doğrular (yalnız yeni kuyruk, ucuz). Çapa değiştiyse (budama) ya
// da önceki rapor kırıksa tam doğrulamaya düşer. Eski satırlardaki kurcalama
// yalnız tam doğrulamada (AuditChainReport) yakalanır; çağıran taraf tam
// doğrulamayı periyodik olarak yapmalıdır.
func (db *DB) ExtendAuditChainReport(prev AuditChainStatus) (AuditChainStatus, error) {
	db.auditMu.Lock()
	st, err := db.readAuditChainState()
	db.auditMu.Unlock()
	if err != nil {
		return AuditChainStatus{}, err
	}
	if !prev.OK || st.anchorID != prev.AnchorID || st.headID < prev.HeadID {
		return db.AuditChainReport()
	}
	// Baş değişmemiş olsa da kuyruk yürünür: yürüyüş boşsa bile başın ötesine
	// eklenmiş (zincir dışı) satır denetimi yapılır.
	tail := st
	tail.anchorID, tail.anchorHash = prev.HeadID, prev.HeadHash
	res, err := db.walkAuditChain(tail, false)
	if err != nil {
		return res, err
	}
	if !res.OK {
		return db.AuditChainReport() // yarışı ele (ve kesin konumu bul)
	}
	res.Checked += prev.Checked
	res.AnchorID = st.anchorID
	return res, nil
}

func (db *DB) walkAuditChain(st auditChainState, checkStale bool) (AuditChainStatus, error) {
	res := AuditChainStatus{
		OK: true, AnchorID: st.anchorID, HeadID: st.headID, HeadHash: st.headHash,
		PrunedTotal: st.prunedTotal, CheckedAt: time.Now().UTC(),
	}
	broken := func(id int64, reason string) (AuditChainStatus, error) {
		res.OK, res.BrokenAt, res.Reason = false, id, reason
		return res, nil
	}
	expectID := st.anchorID + 1
	prev := st.anchorHash
	after := st.anchorID
	first := checkStale // çapa altı kalıntı denetimi yalnız tam yürüyüşte
	for {
		// Başın ötesindeki satırlar (doğrulama sırasında eklenenler) bu turun dışında.
		rows, err := db.conn.Query(`SELECT `+auditColumns+` FROM audit_events WHERE id > ? AND id <= ? ORDER BY id LIMIT ?`,
			after, st.headID, auditBatchSize)
		if err != nil {
			return res, err
		}
		batch := make([]AuditEvent, 0, auditBatchSize)
		for rows.Next() {
			e, err := scanAuditEvent(rows)
			if err != nil {
				rows.Close()
				return res, err
			}
			batch = append(batch, e)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return res, err
		}
		// Çapanın altında kalmış (budanması gereken ama silinmemiş) satır da kurcalamadır.
		if first {
			first = false
			var stale int64
			if err := db.conn.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM audit_events WHERE id <= ?`, st.anchorID).Scan(&stale); err != nil {
				return res, err
			}
			if stale > 0 {
				return broken(stale, "anchor")
			}
		}
		for i := range batch {
			e := &batch[i]
			switch {
			case e.ID != expectID:
				reason := "gap"
				if expectID == st.anchorID+1 {
					reason = "anchor"
				}
				return broken(e.ID, reason)
			case e.PrevHash != prev:
				return broken(e.ID, "link")
			case auditHash(e.PrevHash, e) != e.Hash:
				return broken(e.ID, "hash")
			}
			prev = e.Hash
			expectID++
			res.Checked++
		}
		if len(batch) < auditBatchSize {
			break
		}
		after = batch[len(batch)-1].ID
	}
	// Baş: son satır silindiyse ya da baş hash'i uyuşmuyorsa
	if expectID-1 != st.headID {
		return broken(expectID, "head")
	}
	if prev != st.headHash {
		return broken(st.headID, "head")
	}
	// Başın ötesindeki satırlar zincire hiç girmemiştir (doğrudan INSERT ya da
	// geri sarılmış baş): listede/dışa aktarmada görünür ama doğrulanmaz, bu
	// yüzden kurcalamadır. Baş, aynı ifade içinde GÜNCEL durumdan okunur; ekleme
	// satır+başı tek işlemde yazdığından eşzamanlı meşru ekleme burada görünmez.
	var extra int64
	if err := db.conn.QueryRow(`SELECT COALESCE(MIN(id), 0) FROM audit_events
		WHERE id > COALESCE((SELECT head_id FROM audit_chain_state WHERE id = 1), 0)`).Scan(&extra); err != nil {
		return res, err
	}
	if extra > 0 {
		return broken(extra, "extra")
	}
	return res, nil
}

// PruneAuditEvents zaman damgası before'dan eski olayları siler ve zincirin
// doğrulanabilir kalması için silinen son satırı çapa olarak kaydeder.
// Kimlikler zamanla birlikte arttığından kesim noktası "before'dan eski en
// büyük id"dir; böylece kalan satırlar kesintisiz bir zincir oluşturur.
func (db *DB) PruneAuditEvents(before time.Time) (int64, error) {
	db.auditMu.Lock()
	defer db.auditMu.Unlock()

	tx, err := db.conn.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var cutID int64
	var cutHash string
	err = tx.QueryRow(`SELECT id, hash FROM audit_events WHERE ts < ? ORDER BY id DESC LIMIT 1`,
		before.UTC().Format(AuditTimeLayout)).Scan(&cutID, &cutHash)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`UPDATE audit_chain_state SET pruning = 1 WHERE id = 1`); err != nil {
		return 0, err
	}
	res, err := tx.Exec(`DELETE FROM audit_events WHERE id <= ?`, cutID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if _, err := tx.Exec(`UPDATE audit_chain_state SET pruning = 0, anchor_id = ?, anchor_hash = ?,
		pruned_total = pruned_total + ?, pruned_at = ? WHERE id = 1`,
		cutID, cutHash, n, time.Now().UTC().Format(AuditTimeLayout)); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return n, nil
}
