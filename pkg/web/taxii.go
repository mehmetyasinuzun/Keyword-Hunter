package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"mime"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"keywordhunter-mvp/pkg/export"
	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/storage"
)

// ─────────────────────────── TAXII 2.1 sunucusu (salt okunur) ───────────────────────────
//
// OpenCTI, Microsoft Sentinel, MISP gibi CTI platformları bulguları OASIS
// TAXII 2.1 ile doğrudan çeker. Kimlik doğrulama YALNIZ kişisel API token'ı
// iledir: "Authorization: Bearer kh_…" ya da HTTP Basic (parola = token;
// kullanıcı adı boş bırakılabilir ya da token sahibinin adı olmalıdır — çoğu
// TAXII istemcisi yalnız Basic destekler). Oturum çerezi bu uçlarda hiç
// okunmaz; uçlar yalnız GET olduğundan CSRF yüzeyi yoktur. "read" kapsamı
// yeterlidir. Global IP hız sınırı geçerlidir.
//
// Koleksiyonlar sabit ve deterministik kimliklidir; nesneler pkg/export'un
// STIX kurucularından gelir (aynı kimlikler, veriye bağlı zaman damgaları):
// aynı veri her istekte birebir aynı nesneyi üretir. date_added = bulgunun
// feed_at'i (eklenme anı; sonradan yeni IOC çıkarılınca ya da kritiklik
// yükselince ilerler, bkz. storage.FeedFindings). Sayfalama opak "next"
// imleciyle (feed_at, id, nesne sırası) yapılır; sıralama date_added artandır.
// Her istek en fazla taxiiScanBudget bulgu tarar, istemci giderse durur ve
// eşzamanlı taramalar sınırlıdır (tek SQLite bağlantısı paylaşılır).

const (
	taxiiMediaType = "application/taxii+json;version=2.1"
	stixMediaType  = "application/stix+json;version=2.1"
	taxiiAuthRealm = `Basic realm="KeywordHunter TAXII"`
	taxiiAPIRoot   = "kh"

	taxiiDefaultLimit = 100
	taxiiMaxLimit     = 1000
	// taxiiBatch bir DB turunda okunan bulgu sayısı.
	taxiiBatch = 200
	// taxiiScanBudget bir istekte (liste, manifest ya da tek nesne) taranan en
	// fazla bulgu: süzgeçler (match[id] vb.) çok seyrek eşleşse ya da aranan
	// nesne hiç olmasa bile istek sınırlı sürer; bütçe dolarsa yanıt
	// "more": true + "next" ile kaldığı yerden devam ettirir. Tek nesne araması
	// da aynı bütçeyi kullanır (kimlikler özetten türetildiği için tersine
	// çevrilemez; koleksiyon sayfa sayfa taranır).
	taxiiScanBudget = 5000
	// taxiiMaxScans aynı anda yürüyen en fazla TAXII taraması (tüm sunucu);
	// taxiiMaxScansPerUser bir token sahibininki. Aşılırsa 429.
	taxiiMaxScans        = 4
	taxiiMaxScansPerUser = 2
	// taxiiMaxMatchValues bir match[...] süzgecindeki en fazla değer.
	taxiiMaxMatchValues = 100
	// taxiiCursorDone imleçte "bu bulgunun tüm nesneleri teslim edildi".
	taxiiCursorDone = 1 << 20
	// taxiiAuditEvery bir token + koleksiyon için taxii.read denetim aralığı.
	taxiiAuditEvery = time.Minute
)

// taxiiNS koleksiyon kimliklerinin UUIDv5 ad alanı (sabit: kimlikler sürümler
// ve kurulumlar arasında değişmez, istemci yapılandırmaları kırılmaz).
var taxiiNS = uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://github.com/mehmetyasinuzun/Keyword-Hunter/taxii2"))

// taxiiCollection sabit bir koleksiyon tanımı.
type taxiiCollection struct {
	ID, Alias, Title, Description string
	MinCriticality                int
	Opts                          export.FeedOptions
	Types                         []string // koleksiyonda bulunabilen STIX türleri
}

func newTAXIICollection(alias, title, desc string, minCrit int, opts export.FeedOptions, types ...string) taxiiCollection {
	return taxiiCollection{
		ID: uuid.NewSHA1(taxiiNS, []byte("collection:"+alias)).String(), Alias: alias,
		Title: title, Description: desc, MinCriticality: minCrit, Opts: opts, Types: types,
	}
}

// taxiiCollections sunulan koleksiyonlar (sıra = listeleme sırası).
var taxiiCollections = []taxiiCollection{
	newTAXIICollection("findings-all", "KeywordHunter — all findings",
		"Every dark web finding as a URL indicator, extracted IOCs as indicators and IOC→finding related-to relationships.",
		1, export.FeedOptions{FindingIndicators: true, Relationships: true}, "identity", "indicator", "relationship"),
	newTAXIICollection("findings-high", "KeywordHunter — high & critical findings",
		"Findings with criticality 4–5 only, with their IOCs and relationships.",
		4, export.FeedOptions{FindingIndicators: true, Relationships: true}, "identity", "indicator", "relationship"),
	newTAXIICollection("indicators", "KeywordHunter — IOC indicators",
		"Only indicators for IOCs extracted from findings (email, IP, onion, hashes, crypto wallets). Payment card and phone numbers are never shared.",
		1, export.FeedOptions{}, "identity", "indicator"),
}

// findTAXIICollection kimlik (UUID) ya da takma ad (alias) ile koleksiyon bulur.
func findTAXIICollection(idOrAlias string) (taxiiCollection, bool) {
	for _, col := range taxiiCollections {
		if strings.EqualFold(idOrAlias, col.ID) || idOrAlias == col.Alias {
			return col, true
		}
	}
	return taxiiCollection{}, false
}

// ─────────────────────────── Yanıt yardımcıları ───────────────────────────

// taxiiJSON TAXII/STIX medya türüyle JSON yazar.
func taxiiJSON(c *gin.Context, status int, mediaType string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		logger.Error("TAXII yanıtı serileştirilemedi: %v", err)
		taxiiError(c, http.StatusInternalServerError, "Internal server error", "")
		return
	}
	c.Data(status, mediaType, b)
}

// taxiiError TAXII 2.1 hata nesnesi döndürür ve zinciri keser.
func taxiiError(c *gin.Context, status int, title, desc string) {
	body := map[string]string{"title": title, "http_status": strconv.Itoa(status)}
	if desc != "" {
		body["description"] = desc
	}
	b, _ := json.Marshal(body)
	c.Data(status, taxiiMediaType, b)
	c.Abort()
}

// negotiateTAXII Accept başlığından yanıt medya türünü seçer. Kabul edilen:
// application/taxii+json (sürümsüz ya da version=2.1), joker türler (*/* ve
// application/*) ve allowSTIX ise application/stix+json(;version=2.1).
// Boş Accept sade araçlar (curl) için TAXII türü sayılır; q=0 yok sayılır.
func negotiateTAXII(accept string, allowSTIX bool) (string, bool) {
	accept = strings.TrimSpace(accept)
	if accept == "" {
		return taxiiMediaType, true
	}
	best, bestQ := "", 0.0
	for i, part := range strings.Split(accept, ",") {
		if i >= 32 {
			break
		}
		mt, params, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil {
			continue
		}
		q := 1.0
		if v, ok := params["q"]; ok {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f < 0 || f > 1 {
				continue
			}
			q = f
		}
		if q <= 0 {
			continue
		}
		ver, hasVer := params["version"]
		cand := ""
		switch mt {
		case "*/*", "application/*":
			cand = taxiiMediaType
		case "application/taxii+json":
			if !hasVer || ver == "2.1" {
				cand = taxiiMediaType
			}
		case "application/stix+json":
			if allowSTIX && (!hasVer || ver == "2.1") {
				cand = stixMediaType
			}
		}
		if cand != "" && q > bestQ {
			best, bestQ = cand, q
		}
	}
	return best, best != ""
}

// taxiiAccept yanıt türünü seçer; desteklenmiyorsa 406 yazar.
func taxiiAccept(c *gin.Context, allowSTIX bool) (string, bool) {
	mt, ok := negotiateTAXII(c.GetHeader("Accept"), allowSTIX)
	if !ok {
		want := taxiiMediaType
		if allowSTIX {
			want += " or " + stixMediaType
		}
		taxiiError(c, http.StatusNotAcceptable, "Not Acceptable", "Supported media type: "+want)
		return "", false
	}
	return mt, true
}

// hostPattern Host başlığında kabul edilen karakterler (keşif URL'lerine gömülür).
var hostPattern = regexp.MustCompile(`^[A-Za-z0-9.\-]+(:[0-9]{1,5})?$|^\[[0-9A-Fa-f:.]+\](:[0-9]{1,5})?$`)

// taxiiBaseURL keşif yanıtındaki mutlak adreslerin kökü ("" = göreli adres).
// Şema TLS'li bağlantıdan ya da güvenli çerez yapılandırmasından (TLS'li ters
// vekil arkasında) çıkarılır; doğrudan loopback erişimi (yerel geliştirme)
// gerçek şemasını korur. Host yalnız güvenli karakterlerle kullanılır.
func (s *Server) taxiiBaseURL(c *gin.Context) string {
	host := c.Request.Host
	if !hostPattern.MatchString(host) {
		return ""
	}
	scheme := "http"
	if c.Request.TLS != nil || (s.cookieSecure && !isLoopbackHost(host)) {
		scheme = "https"
	}
	return scheme + "://" + host
}

// isLoopbackHost "host[:port]" yerel makineyi mi gösteriyor.
func isLoopbackHost(hostport string) bool {
	h := hostport
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		h = host
	}
	h = strings.Trim(strings.ToLower(h), "[]")
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// ─────────────────────────── Kimlik doğrulama ───────────────────────────

// taxiiCredentials Bearer ya da Basic'ten token'ı okur. Basic'te parola
// token'dır; kullanıcı adı (varsa) ayrıca döner.
func taxiiCredentials(c *gin.Context) (token, basicUser string, ok bool) {
	if tok, ok := bearerToken(c); ok {
		return tok, "", true
	}
	if user, pass, ok := c.Request.BasicAuth(); ok && pass != "" {
		return strings.TrimSpace(pass), strings.TrimSpace(user), true
	}
	return "", "", false
}

// taxiiUnauthorized 401 + WWW-Authenticate (Basic ve Bearer) döndürür.
func taxiiUnauthorized(c *gin.Context, desc string) {
	c.Writer.Header().Add("WWW-Authenticate", taxiiAuthRealm)
	c.Writer.Header().Add("WWW-Authenticate", `Bearer realm="KeywordHunter TAXII"`)
	taxiiError(c, http.StatusUnauthorized, "Unauthorized", desc)
}

// taxiiTokenMessages token hata nedenlerinin TAXII (İngilizce) açıklaması.
var taxiiTokenMessages = map[string]string{
	storage.TokenStatusRevoked: "API token has been revoked",
	storage.TokenStatusExpired: "API token has expired",
}

// taxiiAuth TAXII uçlarının kimlik doğrulaması. Oturum çerezi yok sayılır.
func (s *Server) taxiiAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Vary", "Accept, Authorization")
		raw, basicUser, ok := taxiiCredentials(c)
		if !ok {
			taxiiUnauthorized(c, "Send a KeywordHunter API token as 'Authorization: Bearer kh_…' or as the HTTP Basic password (create one under /account)")
			return
		}
		t, u, failure := s.verifyAPIToken(c, raw)
		if failure != nil {
			msg := taxiiTokenMessages[failure.Reason]
			if msg == "" {
				msg = "Invalid API token"
			}
			taxiiUnauthorized(c, msg)
			return
		}
		// Basic kullanıcı adı verildiyse token sahibi olmalı: yanlış hesaba
		// yapıştırılmış bir token sessizce başka kimlikle çalışmasın.
		if basicUser != "" && !strings.EqualFold(basicUser, u.Username) {
			s.auditTokenFailure(c, "basic_user_mismatch", t)
			taxiiUnauthorized(c, "HTTP Basic username does not match the token owner (leave it empty or use the owner's username)")
			return
		}
		setTokenContext(c, t, u)
		// Kazıma token'ı ("metrics") yalnız /metrics içindir; bulgu okuyamaz.
		if t.Scope == storage.TokenScopeMetrics {
			s.auditDenied(c, "token_scope", "read")
			taxiiError(c, http.StatusForbidden, "Forbidden", "This API token has the 'metrics' scope and can only scrape /metrics; use a 'read' token for TAXII")
			return
		}
		// REQUIRE_2FA kapsamındaki ama 2FA'sı olmayan hesabın token'ı /api/'de
		// olduğu gibi burada da çalışmaz (bkz. twoFactorGate).
		if s.twoFactorPolicyActive() && s.twoFactorRequiredFor(u.Role) {
			on, err := s.db.TwoFactorEnabled(u.ID)
			if err != nil {
				logger.Error("TAXII 2FA denetimi: %v", err)
				taxiiError(c, http.StatusInternalServerError, "Internal server error", "")
				return
			}
			if !on {
				if s.auditNoise.allow("2fa-required:"+strings.ToLower(u.Username)+":taxii", time.Minute) {
					s.auditDenied(c, "2fa_required", "")
				}
				taxiiError(c, http.StatusForbidden, "Forbidden", "Two-factor authentication is required for this account; enable it under /account")
				return
			}
		}
		s.touchAPIToken(c, t)
		c.Next()
	}
}

// taxiiAudit taxii.read olayını token + kapsam başına dakikada en fazla bir
// kez yazar: düzenli çeken platformlar denetim kaydını boğmaz.
func (s *Server) taxiiAudit(c *gin.Context, scope, endpoint string, objects int, more bool) {
	key := "taxii:" + strconv.FormatInt(c.GetInt64("tokenID"), 10) + ":" + scope
	if !s.auditNoise.allow(key, taxiiAuditEvery) {
		return
	}
	details := map[string]any{"endpoint": endpoint}
	if endpoint == "objects" || endpoint == "manifest" || endpoint == "object" {
		details["objects"] = objects
		details["more"] = more
	}
	s.audit(c, "taxii.read", "taxii:"+scope, details, true)
}

// ─────────────────────────── Sorgu + sayfalama ───────────────────────────

// taxiiCursor opak "next" imlecinin içeriği: (feed_at ham, id) bulgusunun
// ilk Skip nesnesi teslim edildi. Kimlik nesnesi ("", 0) konumundadır.
type taxiiCursor struct {
	Key  string `json:"k"`
	ID   int64  `json:"i"`
	Skip int    `json:"s"`
}

func (cur taxiiCursor) encode() string {
	b, _ := json.Marshal(cur)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeTAXIICursor(raw string) (taxiiCursor, bool) {
	var cur taxiiCursor
	if raw == "" || len(raw) > 256 {
		return cur, false
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || json.Unmarshal(b, &cur) != nil {
		return cur, false
	}
	if len(cur.Key) > 64 || cur.ID < 0 || cur.Skip < 0 || cur.Skip > taxiiCursorDone || (cur.Key == "") != (cur.ID == 0) {
		return cur, false
	}
	return cur, true
}

// taxiiQuery doğrulanmış sorgu parametreleri.
type taxiiQuery struct {
	Limit      int
	AddedAfter time.Time
	Cursor     taxiiCursor
	IDs        map[string]bool
	Types      map[string]bool
	Versions   map[string]bool // boş = tümü (tek sürüm sunulur)
	SpecOK     bool
}

// match nesnenin süzgeçlere uyup uymadığı.
func (q taxiiQuery) match(o export.Object) bool {
	return q.SpecOK && (len(q.Types) == 0 || q.Types[o.Type]) &&
		(len(q.IDs) == 0 || q.IDs[o.ID]) && (len(q.Versions) == 0 || q.Versions[o.Version])
}

// matchValues match[alan] değerlerini (tekrarlanan parametre ve/veya virgül) toplar.
func matchValues(c *gin.Context, field string) ([]string, bool) {
	var out []string
	for _, v := range c.QueryArray("match[" + field + "]") {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				if len(p) > 256 || len(out) >= taxiiMaxMatchValues {
					return nil, false
				}
				out = append(out, p)
			}
		}
	}
	return out, true
}

// parseTAXIITime TAXII zaman damgasını (RFC 3339) ayrıştırır.
func parseTAXIITime(v string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(v))
	return t, err == nil
}

// parseTAXIIQuery limit, added_after, next ve match[...] parametrelerini doğrular.
func parseTAXIIQuery(c *gin.Context) (taxiiQuery, string) {
	q := taxiiQuery{Limit: taxiiDefaultLimit, SpecOK: true}
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return q, "limit must be a positive integer"
		}
		q.Limit = min(n, taxiiMaxLimit)
	}
	if raw := c.Query("added_after"); raw != "" {
		t, ok := parseTAXIITime(raw)
		if !ok {
			return q, "added_after must be an RFC 3339 timestamp (e.g. 2026-01-02T15:04:05.000Z)"
		}
		q.AddedAfter = t
	}
	if raw := c.Query("next"); raw != "" {
		cur, ok := decodeTAXIICursor(raw)
		if !ok {
			return q, "invalid next cursor"
		}
		q.Cursor = cur
	}
	toSet := func(field string) (map[string]bool, string) {
		vals, ok := matchValues(c, field)
		if !ok {
			return nil, "too many or too long match[" + field + "] values"
		}
		if len(vals) == 0 {
			return nil, ""
		}
		set := make(map[string]bool, len(vals))
		for _, v := range vals {
			set[v] = true
		}
		return set, ""
	}
	var msg string
	if q.IDs, msg = toSet("id"); msg != "" {
		return q, msg
	}
	if q.Types, msg = toSet("type"); msg != "" {
		return q, msg
	}
	versions, ok := matchValues(c, "version")
	if !ok {
		return q, "too many or too long match[version] values"
	}
	for _, v := range versions {
		if v == "first" || v == "last" || v == "all" {
			// Her nesnenin tek sürümü sunulur: bu değerlerden biri varsa
			// sürüm süzgeci hiçbir nesneyi elemez.
			q.Versions = nil
			break
		}
		t, ok := parseTAXIITime(v)
		if !ok {
			return q, "match[version] must be first, last, all or an RFC 3339 timestamp"
		}
		if q.Versions == nil {
			q.Versions = map[string]bool{}
		}
		q.Versions[export.Timestamp(t)] = true
	}
	specs, ok := matchValues(c, "spec_version")
	if !ok {
		return q, "too many or too long match[spec_version] values"
	}
	if len(specs) > 0 {
		q.SpecOK = false
		for _, v := range specs {
			if v == "2.1" {
				q.SpecOK = true
			}
		}
	}
	return q, ""
}

// taxiiPageResult bir sayfanın nesneleri ve devam bilgisi.
type taxiiPageResult struct {
	Objects []export.Object
	More    bool
	Next    taxiiCursor
}

// collectionMayMatch tür süzgeci koleksiyonun içerebileceği hiçbir türü
// istemiyorsa taramaya gerek yoktur.
func collectionMayMatch(col taxiiCollection, q taxiiQuery) bool {
	if !q.SpecOK {
		return false
	}
	if len(q.Types) == 0 {
		return true
	}
	for _, t := range col.Types {
		if q.Types[t] {
			return true
		}
	}
	return false
}

// taxiiPage koleksiyonu imleçten itibaren yürür ve en fazla q.Limit eşleşen
// nesne toplar. Bir eşleşme daha bulunursa (ya da tarama bütçesi dolarsa)
// More = true ve Next son teslim edilen nesnenin hemen sonrasıdır.
// ctx iptal edilirse (istemci bağlantıyı kesti) tarama yarıda bırakılır.
func (s *Server) taxiiPage(ctx context.Context, col taxiiCollection, q taxiiQuery, budget int) (taxiiPageResult, error) {
	var res taxiiPageResult
	if !collectionMayMatch(col, q) {
		return res, nil
	}
	pos := q.Cursor
	// consider bir girdinin nesnelerini sayfaya ekler; sayfa dolduğunda ve bir
	// eşleşme daha görüldüğünde true döner (tarama durur).
	consider := func(key string, id int64, objs []export.Object, skip int) bool {
		for i := skip; i < len(objs); i++ {
			if !q.match(objs[i]) {
				continue
			}
			if len(res.Objects) == q.Limit {
				res.More = true
				return true
			}
			res.Objects = append(res.Objects, objs[i])
			res.Next = taxiiCursor{Key: key, ID: id, Skip: i + 1}
		}
		return false
	}
	// 1) Üretici kimliği (sabit, en eski date_added): referans verilen
	// created_by_ref tüketicide çözülebilsin.
	if pos.Key == "" {
		if q.AddedAfter.IsZero() || export.IdentityDateAdded().After(q.AddedAfter) {
			if consider("", 0, []export.Object{export.IdentityObject()}, pos.Skip) {
				return res, nil
			}
		}
	}
	// 2) Bulgular, (julianday(feed_at), id) sırasıyla; imleç bulgusu dahil.
	filter := storage.FeedFilter{
		MinCriticality: col.MinCriticality, AddedAfter: q.AddedAfter,
		AfterKey: pos.Key, AfterID: pos.ID, Limit: taxiiBatch,
	}
	scanned := 0
	for {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		batch, err := s.db.FeedFindings(ctx, filter, true)
		if err != nil {
			return res, err
		}
		for _, f := range batch {
			// İmleç bulgusu (feed_at'i o arada ilerlememişse) kaldığı nesneden
			// sürer ve bütçeye sayılmaz: her sayfa en az bir yeni bulgu tarar,
			// küçük bütçede bile "next" hep ilerler.
			if pos.Key != "" && f.ID == pos.ID && f.Key == pos.Key {
				if consider(f.Key, f.ID, export.FindingObjects(f, col.Opts), pos.Skip) {
					return res, nil
				}
				continue
			}
			if consider(f.Key, f.ID, export.FindingObjects(f, col.Opts), 0) {
				return res, nil
			}
			scanned++
			if scanned >= budget {
				// Bütçe doldu: istemci bir sonraki bulgudan devam eder.
				res.More = true
				res.Next = taxiiCursor{Key: f.Key, ID: f.ID, Skip: taxiiCursorDone}
				return res, nil
			}
		}
		if len(batch) < taxiiBatch {
			return res, nil
		}
		last := batch[len(batch)-1]
		filter.AfterKey, filter.AfterID = last.Key, last.ID+1
	}
}

// setTAXIIDateHeaders sayfadaki ilk ve son nesnenin date_added değerini başlığa yazar.
func setTAXIIDateHeaders(c *gin.Context, objs []export.Object) {
	if len(objs) == 0 {
		return
	}
	c.Header("X-TAXII-Date-Added-First", export.Timestamp(objs[0].DateAdded))
	c.Header("X-TAXII-Date-Added-Last", export.Timestamp(objs[len(objs)-1].DateAdded))
}

// ─────────────────────────── Uçlar ───────────────────────────

// taxiiCollectionResource koleksiyon kaynağı (TAXII 2.1 §5.2).
func taxiiCollectionResource(col taxiiCollection) gin.H {
	return gin.H{
		"id": col.ID, "title": col.Title, "description": col.Description, "alias": col.Alias,
		"can_read": true, "can_write": false, "media_types": []string{stixMediaType},
	}
}

// handleTAXIIDiscovery GET /taxii2/ — sunucu keşfi.
func (s *Server) handleTAXIIDiscovery(c *gin.Context) {
	mt, ok := taxiiAccept(c, false)
	if !ok {
		return
	}
	root := s.taxiiBaseURL(c) + "/taxii2/" + taxiiAPIRoot + "/"
	s.taxiiAudit(c, "discovery", "discovery", 0, false)
	taxiiJSON(c, http.StatusOK, mt, gin.H{
		"title":       "KeywordHunter TAXII 2.1",
		"description": "Read-only TAXII 2.1 feed of KeywordHunter dark web findings and extracted IOCs.",
		"default":     root,
		"api_roots":   []string{root},
	})
}

// handleTAXIIAPIRoot GET /taxii2/kh/ — API kökü bilgisi.
func (s *Server) handleTAXIIAPIRoot(c *gin.Context) {
	mt, ok := taxiiAccept(c, false)
	if !ok {
		return
	}
	s.taxiiAudit(c, "api-root", "api-root", 0, false)
	taxiiJSON(c, http.StatusOK, mt, gin.H{
		"title":              "KeywordHunter",
		"description":        "Dark web CTI findings (Tor search engines) as STIX 2.1.",
		"versions":           []string{taxiiMediaType},
		"max_content_length": 1 << 20,
	})
}

// handleTAXIICollections GET /taxii2/kh/collections/ — koleksiyon listesi.
func (s *Server) handleTAXIICollections(c *gin.Context) {
	mt, ok := taxiiAccept(c, false)
	if !ok {
		return
	}
	list := make([]gin.H, 0, len(taxiiCollections))
	for _, col := range taxiiCollections {
		list = append(list, taxiiCollectionResource(col))
	}
	s.taxiiAudit(c, "collections", "collections", 0, false)
	taxiiJSON(c, http.StatusOK, mt, gin.H{"collections": list})
}

// taxiiCollectionParam yoldaki koleksiyonu çözer; yoksa 404 yazar.
func taxiiCollectionParam(c *gin.Context) (taxiiCollection, bool) {
	col, ok := findTAXIICollection(c.Param("id"))
	if !ok {
		taxiiError(c, http.StatusNotFound, "Collection not found", "Unknown collection id")
		return col, false
	}
	return col, true
}

// handleTAXIICollection GET /taxii2/kh/collections/{id}/ — tek koleksiyon.
func (s *Server) handleTAXIICollection(c *gin.Context) {
	col, ok := taxiiCollectionParam(c)
	if !ok {
		return
	}
	mt, ok := taxiiAccept(c, false)
	if !ok {
		return
	}
	s.taxiiAudit(c, col.Alias, "collection", 0, false)
	taxiiJSON(c, http.StatusOK, mt, taxiiCollectionResource(col))
}

// taxiiEnvelope TAXII 2.1 zarfı (§3.7).
type taxiiEnvelope struct {
	More    bool   `json:"more"`
	Next    string `json:"next,omitempty"`
	Objects []any  `json:"objects,omitempty"`
}

func envelopeOf(res taxiiPageResult) taxiiEnvelope {
	env := taxiiEnvelope{More: res.More}
	if res.More {
		env.Next = res.Next.encode()
	}
	for _, o := range res.Objects {
		env.Objects = append(env.Objects, o.STIX)
	}
	return env
}

// taxiiListing objects/manifest/tek nesne uçlarının ortak gövdesi.
func (s *Server) taxiiListing(c *gin.Context, endpoint string, allowSTIX bool, objectID string) (taxiiCollection, string, taxiiPageResult, bool) {
	col, ok := taxiiCollectionParam(c)
	if !ok {
		return col, "", taxiiPageResult{}, false
	}
	mt, ok := taxiiAccept(c, allowSTIX)
	if !ok {
		return col, "", taxiiPageResult{}, false
	}
	q, msg := parseTAXIIQuery(c)
	if msg != "" {
		taxiiError(c, http.StatusBadRequest, "Bad Request", msg)
		return col, "", taxiiPageResult{}, false
	}
	if objectID != "" {
		q.IDs = map[string]bool{objectID: true}
	}
	release, ok := s.taxiiScans.acquire(strings.ToLower(c.GetString("username")))
	if !ok {
		c.Header("Retry-After", "2")
		taxiiError(c, http.StatusTooManyRequests, "Too Many Requests", "Too many concurrent TAXII requests for this account; retry shortly")
		return col, "", taxiiPageResult{}, false
	}
	res, err := s.taxiiPage(c.Request.Context(), col, q, taxiiScanBudget)
	release()
	if err != nil {
		if c.Request.Context().Err() != nil {
			// İstemci gitti: yanıt yazılmaz, hata da loglanmaz.
			c.Abort()
			return col, "", taxiiPageResult{}, false
		}
		logger.Error("TAXII %s (%s): %v", endpoint, col.Alias, err)
		taxiiError(c, http.StatusInternalServerError, "Internal server error", "")
		return col, "", taxiiPageResult{}, false
	}
	s.taxiiAudit(c, col.Alias, endpoint, len(res.Objects), res.More)
	setTAXIIDateHeaders(c, res.Objects)
	return col, mt, res, true
}

// handleTAXIIObjects GET …/collections/{id}/objects/ — nesneler (zarf).
func (s *Server) handleTAXIIObjects(c *gin.Context) {
	_, mt, res, ok := s.taxiiListing(c, "objects", true, "")
	if !ok {
		return
	}
	taxiiJSON(c, http.StatusOK, mt, envelopeOf(res))
}

// stixIDPattern STIX kimlik biçimi (tür--UUID).
var stixIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}--[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// handleTAXIIObject GET …/objects/{object-id}/ — tek nesnenin sürümleri (zarf).
func (s *Server) handleTAXIIObject(c *gin.Context) {
	oid := c.Param("oid")
	if !stixIDPattern.MatchString(oid) {
		if _, ok := taxiiCollectionParam(c); ok {
			taxiiError(c, http.StatusNotFound, "Object not found", "")
		}
		return
	}
	_, mt, res, ok := s.taxiiListing(c, "object", true, strings.ToLower(oid))
	if !ok {
		return
	}
	if len(res.Objects) == 0 && !res.More {
		taxiiError(c, http.StatusNotFound, "Object not found", "")
		return
	}
	taxiiJSON(c, http.StatusOK, mt, envelopeOf(res))
}

// handleTAXIIManifest GET …/collections/{id}/manifest/ — nesne özetleri.
func (s *Server) handleTAXIIManifest(c *gin.Context) {
	_, mt, res, ok := s.taxiiListing(c, "manifest", false, "")
	if !ok {
		return
	}
	type record struct {
		ID        string `json:"id"`
		DateAdded string `json:"date_added"`
		Version   string `json:"version"`
		MediaType string `json:"media_type"`
	}
	out := struct {
		More    bool     `json:"more"`
		Next    string   `json:"next,omitempty"`
		Objects []record `json:"objects,omitempty"`
	}{More: res.More}
	if res.More {
		out.Next = res.Next.encode()
	}
	for _, o := range res.Objects {
		out.Objects = append(out.Objects, record{ID: o.ID, DateAdded: export.Timestamp(o.DateAdded), Version: o.Version, MediaType: stixMediaType})
	}
	taxiiJSON(c, http.StatusOK, mt, out)
}

// handleTAXIIReadOnly koleksiyonlara yazma (Add Objects / Delete Object)
// istekleri: koleksiyonlar salt okunurdur (can_write = false).
func (s *Server) handleTAXIIReadOnly(c *gin.Context) {
	if _, ok := taxiiCollectionParam(c); !ok {
		return
	}
	taxiiError(c, http.StatusForbidden, "Forbidden", "Collections on this server are read-only (can_write: false)")
}

// taxiiInfoCollection Ayarlar sayfasındaki koleksiyon satırı.
type taxiiInfoCollection struct {
	ID, Alias, LabelKey, URL string
	MinCriticality           int
}

// taxiiInfo Ayarlar → Entegrasyonlar kartı için adresler (sunucuda hesaplanır).
func (s *Server) taxiiInfo(c *gin.Context) gin.H {
	root := s.taxiiBaseURL(c) + "/taxii2/" + taxiiAPIRoot + "/"
	cols := make([]taxiiInfoCollection, 0, len(taxiiCollections))
	for _, col := range taxiiCollections {
		cols = append(cols, taxiiInfoCollection{
			ID: col.ID, Alias: col.Alias, LabelKey: "int.taxii.col." + col.Alias,
			URL: root + "collections/" + col.ID + "/objects/", MinCriticality: col.MinCriticality,
		})
	}
	return gin.H{"discovery": s.taxiiBaseURL(c) + "/taxii2/", "apiRoot": root, "collections": cols}
}

// scanLimiter pahalı TAXII taramalarını hem sunucu genelinde hem kullanıcı
// başına sınırlar (beklemez; doluysa hemen reddeder).
type scanLimiter struct {
	mu       sync.Mutex
	total    int
	maxTotal int
	maxPer   int
	perUser  map[string]int
}

func newScanLimiter(maxTotal, maxPer int) *scanLimiter {
	return &scanLimiter{maxTotal: maxTotal, maxPer: maxPer, perUser: map[string]int{}}
}

// acquire bir tarama yeri ayırır; dönen işlev yeri bırakır (bir kez).
func (l *scanLimiter) acquire(user string) (func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.total >= l.maxTotal || l.perUser[user] >= l.maxPer {
		return nil, false
	}
	l.total++
	l.perUser[user]++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.total--
			if l.perUser[user]--; l.perUser[user] <= 0 {
				delete(l.perUser, user)
			}
		})
	}, true
}
