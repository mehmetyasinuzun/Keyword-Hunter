package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/config"
	"keywordhunter-mvp/pkg/export"
	"keywordhunter-mvp/pkg/storage"
)

// ─────────────────────────── TAXII test düzeneği ───────────────────────────

type seedFinding struct {
	title, url, source string
	crit               int
	at                 time.Time
	arts               []storage.ResultArtifact
}

var taxiiT0 = time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

// taxiiFixture: yinelenen URL (F1/F3), yinelenen IOC (e-posta F1/F2/F3),
// yinelenen IOC→URL ilişkisi (F1/F3) ve paylaşılmaması gereken kart numarası.
func taxiiFixture() []seedFinding {
	return []seedFinding{
		{"Fresh combo leak", "http://aaaa.onion/leak", "Ahmia", 5, taxiiT0, []storage.ResultArtifact{
			{Type: "email", Value: "seller@proton.me", Confidence: .8},
			{Type: "bitcoin", Value: "bc1qexamplewallet0000000000000000000000", Confidence: .7},
			{Type: "hash", Value: strings.Repeat("a", 32), Confidence: .6},
			{Type: "credit_card", Value: "4111111111111111", Confidence: .9},
		}},
		{"Forum post", "http://bbbb.onion/post", "Tor66", 2, taxiiT0.Add(time.Hour), []storage.ResultArtifact{
			{Type: "email", Value: "seller@proton.me", Confidence: .8},
			{Type: "ip", Value: "185.1.2.3", Confidence: .5},
		}},
		{"Fresh combo leak (mirror)", "http://aaaa.onion/leak", "Torch", 4, taxiiT0.Add(2 * time.Hour), []storage.ResultArtifact{
			{Type: "email", Value: "seller@proton.me", Confidence: .8},
			{Type: "bitcoin", Value: "bc1qexamplewallet0000000000000000000000", Confidence: .7},
		}},
		{"Market listing", "http://cccc.onion/item", "Ahmia", 1, taxiiT0.Add(3 * time.Hour), nil},
	}
}

// seedFeed bulguları belirli zaman damgalarıyla yazar; kimlikleri döndürür.
func seedFeed(t *testing.T, s *Server, list []seedFinding) []int64 {
	t.Helper()
	conn := s.db.GetDBConn()
	var ids []int64
	for _, f := range list {
		var got []int64
		if _, err := s.db.SaveResultsWithIDs([]storage.SearchResult{{Title: f.title, URL: f.url, Source: f.source, Query: "leak", Criticality: f.crit}}, &got); err != nil || len(got) != 1 {
			t.Fatalf("bulgu yazılamadı: %v %v", err, got)
		}
		id := got[0]
		at := f.at.UTC().Format("2006-01-02 15:04:05")
		if _, err := conn.Exec(`UPDATE search_results SET created_at = ?, criticality = ? WHERE id = ?`, at, f.crit, id); err != nil {
			t.Fatal(err)
		}
		if len(f.arts) > 0 {
			if _, err := s.db.ReplaceArtifacts(id, f.arts); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.Exec(`UPDATE result_artifacts SET created_at = ? WHERE result_id = ?`, at, id); err != nil {
				t.Fatal(err)
			}
		}
		// IOC yazımı feed_at'i "şimdi"ye ilerletir; düzenek onu created_at'e sabitler.
		if _, err := conn.Exec(`UPDATE search_results SET feed_at = created_at WHERE id = ?`, id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

// newTAXIIServer bulgu verisi ve "read" kapsamlı bir token'la test sunucusu kurar.
func newTAXIIServer(t *testing.T) (*Server, string, []*http.Cookie) {
	t.Helper()
	s := newTestServer(t)
	ck := mkTokenUser(t, s, "cti", "cti-pass-123", storage.RoleAnalyst)
	tok := createToken(t, s, ck, "opencti", storage.TokenScopeRead, 0)
	seedFeed(t, s, taxiiFixture())
	return s, tok.Token, ck
}

type taxiiAuthFn func(*http.Request)

func withBearer(tok string) taxiiAuthFn {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }
}

func withBasic(user, tok string) taxiiAuthFn {
	return func(r *http.Request) { r.SetBasicAuth(user, tok) }
}

func taxiiGet(s *Server, path string, auth taxiiAuthFn, accept string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if auth != nil {
		auth(req)
	}
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	return w
}

type taxiiEnv struct {
	More    bool             `json:"more"`
	Next    string           `json:"next"`
	Objects []map[string]any `json:"objects"`
}

func decodeEnv(t *testing.T, w *httptest.ResponseRecorder) taxiiEnv {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("beklenen 200, gelen %d: %s", w.Code, w.Body.String())
	}
	var env taxiiEnv
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("zarf çözülemedi: %v — %s", err, w.Body.String())
	}
	return env
}

func colPath(alias, suffix string) string {
	col, _ := findTAXIICollection(alias)
	return "/taxii2/kh/collections/" + col.ID + "/" + suffix
}

func objectIDs(env taxiiEnv) []string {
	out := make([]string, 0, len(env.Objects))
	for _, o := range env.Objects {
		out = append(out, o["id"].(string))
	}
	return out
}

// ─────────────────────────── Kimlik doğrulama ───────────────────────────

func TestTAXII_DiscoveryWithBasicAndBearer(t *testing.T) {
	s, tok, _ := newTAXIIServer(t)
	for name, auth := range map[string]taxiiAuthFn{
		"bearer":      withBearer(tok),
		"basic-empty": withBasic("", tok),
		"basic-owner": withBasic("cti", tok),
		"basic-case":  withBasic("CTI", tok),
	} {
		w := taxiiGet(s, "/taxii2/", auth, taxiiMediaType)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body.String())
		}
		if ct := w.Header().Get("Content-Type"); ct != taxiiMediaType {
			t.Fatalf("%s: Content-Type = %q", name, ct)
		}
		var d struct {
			Title    string   `json:"title"`
			Default  string   `json:"default"`
			APIRoots []string `json:"api_roots"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil || len(d.APIRoots) != 1 || !strings.HasSuffix(d.APIRoots[0], "/taxii2/kh/") || d.Default != d.APIRoots[0] {
			t.Fatalf("%s: keşif yanıtı: %s", name, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: TAXII yanıtları önbelleğe alınmamalı", name)
		}
	}
	// API kökü
	w := taxiiGet(s, "/taxii2/kh/", withBasic("", tok), "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"versions":["application/taxii+json;version=2.1"]`) {
		t.Fatalf("API kökü: %d %s", w.Code, w.Body.String())
	}
}

func TestTAXII_UnauthorizedPaths(t *testing.T) {
	s, tok, ck := newTAXIIServer(t)
	check := func(name string, w *httptest.ResponseRecorder) {
		t.Helper()
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s: beklenen 401, gelen %d %s", name, w.Code, w.Body.String())
		}
		if got := w.Header().Values("WWW-Authenticate"); len(got) == 0 || got[0] != `Basic realm="KeywordHunter TAXII"` {
			t.Fatalf("%s: WWW-Authenticate = %v", name, got)
		}
		if ct := w.Header().Get("Content-Type"); ct != taxiiMediaType {
			t.Fatalf("%s: hata medya türü = %q", name, ct)
		}
		var e map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e["http_status"] != "401" || e["title"] == "" {
			t.Fatalf("%s: TAXII hata nesnesi değil: %s", name, w.Body.String())
		}
	}
	check("kimlik yok", taxiiGet(s, "/taxii2/", nil, ""))
	check("geçersiz token", taxiiGet(s, "/taxii2/", withBearer("kh_"+strings.Repeat("x", 43)), ""))
	check("biçimsiz token", taxiiGet(s, "/taxii2/", withBasic("", "hunter2"), ""))
	check("yanlış Basic kullanıcı", taxiiGet(s, "/taxii2/", withBasic("someone-else", tok), ""))
	check("koleksiyon (kimliksiz)", taxiiGet(s, colPath("findings-all", "objects/"), nil, ""))

	// Oturum çerezi TAXII'de kimlik değildir.
	req := httptest.NewRequest(http.MethodGet, "/taxii2/", nil)
	for _, c := range ck {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	check("yalnız oturum çerezi", w)

	// Çerez + geçerli token: çerez yok sayılır, token geçerli.
	req = httptest.NewRequest(http.MethodGet, "/taxii2/", nil)
	for _, c := range ck {
		req.AddCookie(c)
	}
	req.SetBasicAuth("", tok)
	w = httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("çerez + token: %d", w.Code)
	}
}

func TestTAXII_RevokedAndExpiredTokensRejected(t *testing.T) {
	s, tok, ck := newTAXIIServer(t)
	expiring := createToken(t, s, ck, "short", storage.TokenScopeRead, 1)
	if w := taxiiGet(s, "/taxii2/kh/collections/", withBasic("", expiring.Token), ""); w.Code != http.StatusOK {
		t.Fatalf("taze token: %d", w.Code)
	}
	if _, err := s.db.GetDBConn().Exec(`UPDATE api_tokens SET expires_at = ? WHERE id = ?`, time.Now().Add(-time.Hour).UTC(), expiring.Meta.ID); err != nil {
		t.Fatal(err)
	}
	w := taxiiGet(s, "/taxii2/kh/collections/", withBasic("", expiring.Token), "")
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "expired") {
		t.Fatalf("süresi dolmuş token: %d %s", w.Code, w.Body.String())
	}

	// İptal edilen token
	list, err := s.db.ListAPITokens("cti")
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	for _, x := range list {
		if x.Prefix == tok[:storage.APITokenDisplayLen] {
			id = x.ID
		}
	}
	if w := doJSON(s, http.MethodPost, "/api/tokens/"+strconvFormat(id)+"/revoke", `{}`, ck, csrfOf(ck)); w.Code != http.StatusOK {
		t.Fatalf("iptal: %d %s", w.Code, w.Body.String())
	}
	w = taxiiGet(s, "/taxii2/", withBearer(tok), "")
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "revoked") {
		t.Fatalf("iptal edilmiş token: %d %s", w.Code, w.Body.String())
	}
	if countAudit(t, s, "auth.token.failure") == 0 {
		t.Fatal("geçersiz token denemesi denetlenmeli")
	}
}

func TestTAXII_TwoFactorPolicyBlocksTokenOwnerWithout2FA(t *testing.T) {
	s, tok, _ := newTAXIIServer(t)
	s.require2FA = config.Require2FAAll
	w := taxiiGet(s, "/taxii2/", withBasic("", tok), "")
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"http_status":"403"`) {
		t.Fatalf("2FA zorunluyken 2FA'sız hesabın token'ı reddedilmeli: %d %s", w.Code, w.Body.String())
	}
}

// ─────────────────────────── İçerik anlaşması + hatalar ───────────────────────────

func TestTAXII_AcceptNegotiation(t *testing.T) {
	s, tok, _ := newTAXIIServer(t)
	auth := withBasic("", tok)
	for _, acc := range []string{"text/html", "application/json", "application/taxii+json;version=2.0", "application/taxii+json;q=0"} {
		w := taxiiGet(s, "/taxii2/kh/collections/", auth, acc)
		if w.Code != http.StatusNotAcceptable || w.Header().Get("Content-Type") != taxiiMediaType || !strings.Contains(w.Body.String(), `"http_status":"406"`) {
			t.Fatalf("Accept %q: beklenen 406 TAXII hatası, gelen %d %s", acc, w.Code, w.Body.String())
		}
	}
	// STIX medya türü yalnız nesne uçlarında kabul edilir.
	if w := taxiiGet(s, "/taxii2/kh/collections/", auth, stixMediaType); w.Code != http.StatusNotAcceptable {
		t.Fatalf("koleksiyon listesinde stix+json 406 olmalı: %d", w.Code)
	}
	w := taxiiGet(s, colPath("findings-all", "objects/"), auth, stixMediaType)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != stixMediaType {
		t.Fatalf("stix+json zarf: %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	for _, acc := range []string{"", "*/*", "application/taxii+json", "text/html;q=0.9, application/taxii+json;version=2.1"} {
		w := taxiiGet(s, colPath("findings-all", "objects/"), auth, acc)
		if w.Code != http.StatusOK || w.Header().Get("Content-Type") != taxiiMediaType {
			t.Fatalf("Accept %q: %d %q", acc, w.Code, w.Header().Get("Content-Type"))
		}
	}
}

func TestTAXII_ErrorsAreTAXIIObjects(t *testing.T) {
	s, tok, _ := newTAXIIServer(t)
	auth := withBasic("", tok)
	cases := []struct {
		path string
		code int
	}{
		{"/taxii2/kh/collections/00000000-0000-0000-0000-000000000000/", 404},
		{"/taxii2/other/", 404},
		{colPath("findings-all", "objects/?limit=0"), 400},
		{colPath("findings-all", "objects/?limit=abc"), 400},
		{colPath("findings-all", "objects/?added_after=yesterday"), 400},
		{colPath("findings-all", "objects/?next=%21%21"), 400},
		{colPath("findings-all", "objects/?match[version]=newest"), 400},
		{colPath("findings-all", "objects/indicator--00000000-0000-0000-0000-000000000000/"), 404},
		{colPath("findings-all", "objects/not-an-id/"), 404},
	}
	for _, tc := range cases {
		w := taxiiGet(s, tc.path, auth, "")
		if w.Code != tc.code || w.Header().Get("Content-Type") != taxiiMediaType || !strings.Contains(w.Body.String(), `"http_status"`) {
			t.Fatalf("%s: beklenen %d TAXII hatası, gelen %d %q %s", tc.path, tc.code, w.Code, w.Header().Get("Content-Type"), w.Body.String())
		}
	}
	// Koleksiyonlar salt okunur: yazma 403 (token ile; CSRF yüzeyi yok).
	req := httptest.NewRequest(http.MethodPost, colPath("findings-all", "objects/"), strings.NewReader(`{"objects":[]}`))
	req.Header.Set("Content-Type", taxiiMediaType)
	req.SetBasicAuth("", tok)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "read-only") {
		t.Fatalf("salt okunur koleksiyona yazma: %d %s", w.Code, w.Body.String())
	}
}

// ─────────────────────────── Koleksiyonlar + nesneler ───────────────────────────

func TestTAXII_CollectionsAreFixedAndDeterministic(t *testing.T) {
	s, tok, _ := newTAXIIServer(t)
	w := taxiiGet(s, "/taxii2/kh/collections/", withBearer(tok), "")
	var d struct {
		Collections []struct {
			ID         string   `json:"id"`
			Alias      string   `json:"alias"`
			CanRead    bool     `json:"can_read"`
			CanWrite   bool     `json:"can_write"`
			MediaTypes []string `json:"media_types"`
		} `json:"collections"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil || len(d.Collections) != 3 {
		t.Fatalf("koleksiyonlar: %s", w.Body.String())
	}
	want := map[string]string{
		// Kimlikler sabittir: istemci yapılandırmaları sürüm yükseltmesinde kırılmamalı.
		"findings-all":  "4e8e1b28-53c1-5eee-a771-b92fbf71fb00",
		"findings-high": "a967eef9-6fab-5e3f-a4b3-3e235c1dc779",
		"indicators":    "8d76704e-d6e7-5396-bf6a-07de1854d968",
	}
	for _, col := range d.Collections {
		if _, ok := want[col.Alias]; !ok || !col.CanRead || col.CanWrite || len(col.MediaTypes) != 1 || col.MediaTypes[0] != stixMediaType {
			t.Fatalf("koleksiyon: %+v", col)
		}
		ref, _ := findTAXIICollection(col.Alias)
		if col.ID != ref.ID {
			t.Fatalf("kimlik tutarsız: %s", col.Alias)
		}
		if col.ID != want[col.Alias] {
			t.Fatalf("%s kimliği değişti: %s (beklenen %s)", col.Alias, col.ID, want[col.Alias])
		}
		// Tek koleksiyon: kimlik ve takma adla erişilebilir
		for _, key := range []string{col.ID, col.Alias} {
			if w := taxiiGet(s, "/taxii2/kh/collections/"+key+"/", withBearer(tok), ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), col.ID) {
				t.Fatalf("koleksiyon %s: %d", key, w.Code)
			}
		}
	}
}

func TestTAXII_ObjectsDedupedOrderedAndStable(t *testing.T) {
	s, tok, _ := newTAXIIServer(t)
	auth := withBasic("", tok)
	path := colPath("findings-all", "objects/?limit=1000")
	w := taxiiGet(s, path, auth, "")
	env := decodeEnv(t, w)
	if env.More || env.Next != "" {
		t.Fatalf("tek sayfada bitmeli: %+v", env)
	}
	// identity + F1 (URL + 3 IOC + 3 ilişki; kart yok) + F2 (URL + IP + 2 ilişki) + F3 (hepsi tekrar) + F4 (URL)
	if len(env.Objects) != 13 {
		t.Fatalf("nesne sayısı = %d, beklenen 13: %v", len(env.Objects), objectIDs(env))
	}
	seen := map[string]bool{}
	for i, o := range env.Objects {
		id := o["id"].(string)
		if seen[id] {
			t.Fatalf("yinelenen nesne: %s", id)
		}
		seen[id] = true
		if o["spec_version"] != "2.1" {
			t.Fatalf("spec_version: %v", o)
		}
		b, _ := json.Marshal(o)
		if strings.Contains(string(b), "4111111111111111") || strings.Contains(string(b), "payment-card") {
			t.Fatal("kart numarası TAXII ile paylaşılmamalı")
		}
		if i == 0 && o["type"] != "identity" {
			t.Fatalf("ilk nesne üretici kimliği olmalı: %v", o["type"])
		}
	}
	for _, id := range []string{
		export.IdentityID, export.URLIndicatorID("http://aaaa.onion/leak"), export.URLIndicatorID("http://bbbb.onion/post"),
		export.URLIndicatorID("http://cccc.onion/item"), export.ArtifactIndicatorID("email", "seller@proton.me"),
		export.ArtifactIndicatorID("ip", "185.1.2.3"), export.ArtifactIndicatorID("hash", strings.Repeat("a", 32)),
	} {
		if !seen[id] {
			t.Fatalf("beklenen nesne yok: %s", id)
		}
	}
	if got := w.Header().Get("X-TAXII-Date-Added-First"); got != "2024-01-01T00:00:00.000Z" {
		t.Fatalf("X-TAXII-Date-Added-First = %q", got)
	}
	if got := w.Header().Get("X-TAXII-Date-Added-Last"); got != export.Timestamp(taxiiT0.Add(3*time.Hour)) {
		t.Fatalf("X-TAXII-Date-Added-Last = %q", got)
	}
	// Kararlılık: aynı veri, aynı bayt dizisi (aynı kimlik + aynı sürüm).
	if again := taxiiGet(s, path, auth, ""); again.Body.String() != w.Body.String() {
		t.Fatal("aynı istek farklı nesneler üretti (kararlı olmalı)")
	}
}

func TestTAXII_PaginationCoversEverythingOnce(t *testing.T) {
	s, tok, _ := newTAXIIServer(t)
	auth := withBearer(tok)
	full := decodeEnv(t, taxiiGet(s, colPath("findings-all", "objects/?limit=1000"), auth, ""))
	for _, limit := range []string{"1", "2", "3", "5"} {
		var got []string
		next := ""
		for page := 0; ; page++ {
			if page > 20 {
				t.Fatal("sayfalama bitmedi")
			}
			q := url.Values{"limit": {limit}}
			if next != "" {
				q.Set("next", next)
			}
			w := taxiiGet(s, colPath("findings-all", "objects/?"+q.Encode()), auth, "")
			env := decodeEnv(t, w)
			got = append(got, objectIDs(env)...)
			if len(env.Objects) > 0 && w.Header().Get("X-TAXII-Date-Added-First") == "" {
				t.Fatal("tarih başlıkları eksik")
			}
			if !env.More {
				if env.Next != "" {
					t.Fatal("more=false iken next olmamalı")
				}
				break
			}
			if env.Next == "" || strings.Contains(env.Next, "aaaa") {
				t.Fatalf("next opak olmalı: %q", env.Next)
			}
			next = env.Next
		}
		if strings.Join(got, ",") != strings.Join(objectIDs(full), ",") {
			t.Fatalf("limit=%s: sayfalı sonuç tam listeyle aynı olmalı\n got: %v\nwant: %v", limit, got, objectIDs(full))
		}
	}
	// Limit üst sınıra sabitlenir.
	if env := decodeEnv(t, taxiiGet(s, colPath("findings-all", "objects/?limit=999999"), auth, "")); len(env.Objects) != 13 {
		t.Fatalf("büyük limit: %d", len(env.Objects))
	}
}

func TestTAXII_AddedAfterAndMatchFilters(t *testing.T) {
	s, tok, _ := newTAXIIServer(t)
	auth := withBasic("cti", tok)
	get := func(alias, q string) taxiiEnv {
		t.Helper()
		return decodeEnv(t, taxiiGet(s, colPath(alias, "objects/?"+q), auth, ""))
	}
	// added_after kesinlikle sonrası: F2'nin anından sonra yalnız F4'ün URL'si yenidir (F3 tekrar).
	env := get("findings-all", "added_after="+url.QueryEscape(export.Timestamp(taxiiT0.Add(time.Hour))))
	if ids := objectIDs(env); len(ids) != 1 || ids[0] != export.URLIndicatorID("http://cccc.onion/item") {
		t.Fatalf("added_after: %v", ids)
	}
	if env := get("findings-all", "added_after=2030-01-01T00:00:00Z"); len(env.Objects) != 0 || env.More {
		t.Fatalf("gelecekteki added_after boş olmalı: %+v", env)
	}
	// match[type]
	env = get("findings-all", "match[type]=relationship")
	if len(env.Objects) != 5 {
		t.Fatalf("ilişki sayısı = %d", len(env.Objects))
	}
	for _, o := range env.Objects {
		if o["type"] != "relationship" || o["relationship_type"] != "related-to" {
			t.Fatalf("match[type]: %v", o)
		}
	}
	if env := get("findings-all", "match[type]=malware,attack-pattern"); len(env.Objects) != 0 {
		t.Fatal("koleksiyonda olmayan tür boş dönmeli")
	}
	// match[id] (virgüllü ve tekrarlı)
	ip, mail := export.ArtifactIndicatorID("ip", "185.1.2.3"), export.ArtifactIndicatorID("email", "seller@proton.me")
	env = get("findings-all", "match[id]="+ip+"&match[id]="+mail)
	if ids := strings.Join(objectIDs(env), ","); ids != mail+","+ip {
		t.Fatalf("match[id]: %s", ids)
	}
	// match[spec_version] / match[version]
	if env := get("findings-all", "match[spec_version]=2.0"); len(env.Objects) != 0 {
		t.Fatal("2.0 nesnesi yok")
	}
	if env := get("findings-all", "match[spec_version]=2.1&match[version]=last"); len(env.Objects) != 13 {
		t.Fatalf("version=last: %d", len(env.Objects))
	}
	ipObj := get("findings-all", "match[id]="+ip).Objects[0]
	if env := get("findings-all", "match[version]="+url.QueryEscape(ipObj["modified"].(string))+"&match[id]="+ip); len(env.Objects) != 1 {
		t.Fatal("match[version]=<modified> nesneyi döndürmeli")
	}

	// findings-high: yalnız SEV 4+ (F1, F3); F3'ün her şeyi F1'de yayımlandı.
	env = get("findings-high", "limit=1000")
	if len(env.Objects) != 8 {
		t.Fatalf("findings-high nesne sayısı = %d: %v", len(env.Objects), objectIDs(env))
	}
	for _, id := range objectIDs(env) {
		if id == export.URLIndicatorID("http://bbbb.onion/post") || id == ip {
			t.Fatalf("SEV<4 bulgunun nesnesi findings-high'a sızdı: %s", id)
		}
	}
	// indicators: yalnız IOC göstergeleri (+ kimlik); URL göstergesi/ilişki yok.
	env = get("indicators", "")
	if len(env.Objects) != 5 {
		t.Fatalf("indicators nesne sayısı = %d: %v", len(env.Objects), objectIDs(env))
	}
	for _, o := range env.Objects {
		if o["type"] == "relationship" || (o["type"] == "indicator" && strings.Contains(o["pattern"].(string), "url:value")) {
			t.Fatalf("indicators koleksiyonunda yalnız IOC olmalı: %v", o)
		}
	}
}

func TestTAXII_GetObjectAndManifest(t *testing.T) {
	s, tok, _ := newTAXIIServer(t)
	auth := withBearer(tok)
	mail := export.ArtifactIndicatorID("email", "seller@proton.me")
	env := decodeEnv(t, taxiiGet(s, colPath("findings-all", "objects/"+mail+"/"), auth, ""))
	if len(env.Objects) != 1 || env.Objects[0]["id"] != mail || env.More {
		t.Fatalf("tek nesne: %+v", env)
	}
	// indicators koleksiyonunda ilişki yok → 404
	rel := relationshipIDFor()
	if w := taxiiGet(s, colPath("indicators", "objects/"+rel+"/"), auth, ""); w.Code != http.StatusNotFound {
		t.Fatalf("koleksiyonda olmayan nesne 404 olmalı: %d", w.Code)
	}
	if w := taxiiGet(s, colPath("findings-all", "objects/"+rel+"/"), auth, ""); w.Code != http.StatusOK {
		t.Fatalf("ilişki nesnesi: %d %s", w.Code, w.Body.String())
	}

	w := taxiiGet(s, colPath("findings-all", "manifest/?limit=4"), auth, "")
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != taxiiMediaType {
		t.Fatalf("manifest: %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	var m struct {
		More    bool   `json:"more"`
		Next    string `json:"next"`
		Objects []struct {
			ID, DateAdded, Version, MediaType string
		} `json:"objects"`
	}
	if err := json.Unmarshal([]byte(strings.NewReplacer(`"date_added"`, `"DateAdded"`, `"media_type"`, `"MediaType"`).Replace(w.Body.String())), &m); err != nil {
		t.Fatal(err)
	}
	if !m.More || m.Next == "" || len(m.Objects) != 4 || m.Objects[0].ID != export.IdentityID || m.Objects[0].MediaType != stixMediaType || m.Objects[1].Version == "" || m.Objects[1].DateAdded != export.Timestamp(taxiiT0) {
		t.Fatalf("manifest içeriği: %s", w.Body.String())
	}
	if w.Header().Get("X-TAXII-Date-Added-Last") != export.Timestamp(taxiiT0) {
		t.Fatalf("manifest tarih başlığı: %q", w.Header().Get("X-TAXII-Date-Added-Last"))
	}
	// Manifest sürümü nesnenin modified değeridir.
	objs := decodeEnv(t, taxiiGet(s, colPath("findings-all", "objects/?limit=4"), auth, "")).Objects
	for i := range m.Objects {
		if objs[i]["id"] != m.Objects[i].ID || objs[i]["modified"] != m.Objects[i].Version {
			t.Fatalf("manifest ↔ nesne uyumsuz: %v / %+v", objs[i], m.Objects[i])
		}
	}
}

// relationshipIDFor export paketindeki ilişki kimliğini yeniden üretir.
// (e-posta IOC'si → http://aaaa.onion/leak bulgusu).
func relationshipIDFor() string {
	objs := export.FindingObjects(storage.FeedFinding{
		SearchResult: storage.SearchResult{URL: "http://aaaa.onion/leak", CreatedAt: taxiiT0},
		Artifacts: []storage.FeedArtifact{{
			ResultArtifact: storage.ResultArtifact{Type: "email", Value: "seller@proton.me", CreatedAt: taxiiT0},
			FirstRelation:  true,
		}},
	}, export.FeedOptions{Relationships: true})
	for _, o := range objs {
		if o.Type == "relationship" {
			return o.ID
		}
	}
	return ""
}

func TestTAXII_ReadAuditIsThrottled(t *testing.T) {
	s, tok, _ := newTAXIIServer(t)
	auth := withBasic("", tok)
	for i := 0; i < 5; i++ {
		decodeEnv(t, taxiiGet(s, colPath("findings-all", "objects/"), auth, ""))
	}
	decodeEnv(t, taxiiGet(s, colPath("findings-high", "objects/"), auth, ""))
	n := 0
	targets := map[string]int{}
	for _, e := range auditEvents(t, s) {
		if e.Action == "taxii.read" {
			n++
			targets[e.Target]++
			if e.AuthMethod != storage.AuthMethodToken || e.Actor != "cti" || !strings.Contains(e.Details, "tokenPrefix") {
				t.Fatalf("taxii.read aktörü/yöntemi: %+v", e)
			}
			if strings.Contains(e.Details, tok) {
				t.Fatal("token denetim kaydına yazılmamalı")
			}
		}
	}
	if n != 2 || targets["taxii:findings-all"] != 1 || targets["taxii:findings-high"] != 1 {
		t.Fatalf("taxii.read token+koleksiyon başına dakikada bir olmalı: %v", targets)
	}
	if auditCategory("taxii.read") != "export" {
		t.Fatal("taxii.read dışa aktarma kategorisinde olmalı")
	}
}

func TestNegotiateTAXII(t *testing.T) {
	cases := []struct {
		accept    string
		allowSTIX bool
		want      string
	}{
		{"", false, taxiiMediaType},
		{"application/taxii+json;version=2.1", false, taxiiMediaType},
		{"application/taxii+json; version=2.1", true, taxiiMediaType},
		{"application/stix+json;version=2.1", true, stixMediaType},
		{"application/stix+json;version=2.1", false, ""},
		{"application/stix+json;version=2.1;q=0.5, application/taxii+json;version=2.1", true, taxiiMediaType},
		{"application/taxii+json;version=2.1;q=0.4, application/stix+json;version=2.1;q=0.9", true, stixMediaType},
		{"application/vnd.oasis.taxii+json; version=2.0", true, ""},
		{"text/html, image/png", true, ""},
		{"*/*;q=0", true, ""},
	}
	for _, tc := range cases {
		got, ok := negotiateTAXII(tc.accept, tc.allowSTIX)
		if got != tc.want || ok != (tc.want != "") {
			t.Errorf("negotiateTAXII(%q, %v) = %q, %v; beklenen %q", tc.accept, tc.allowSTIX, got, ok, tc.want)
		}
	}
}

func TestTAXIICursor_RoundTripAndValidation(t *testing.T) {
	cur := taxiiCursor{Key: "2026-03-01 10:00:00", ID: 42, Skip: 3}
	back, ok := decodeTAXIICursor(cur.encode())
	if !ok || back != cur {
		t.Fatalf("imleç gidiş-dönüş: %+v %v", back, ok)
	}
	for _, bad := range []string{"", "!!", strings.Repeat("A", 300), taxiiCursor{Key: "", ID: 5}.encode(), taxiiCursor{Key: "x", ID: 1, Skip: -1}.encode()} {
		if _, ok := decodeTAXIICursor(bad); ok {
			t.Errorf("geçersiz imleç kabul edildi: %q", bad)
		}
	}
}

func TestTAXIIBaseURL_Scheme(t *testing.T) {
	s := newTestServer(t)
	cases := []struct {
		host   string
		secure bool
		want   string
	}{
		{"cti.example.org", false, "http://cti.example.org"},
		{"cti.example.org", true, "https://cti.example.org"},
		{"127.0.0.1:8080", true, "http://127.0.0.1:8080"},
		{"[::1]:8080", true, "http://[::1]:8080"},
		{"localhost:8080", true, "http://localhost:8080"},
		{"evil.example/<script>", false, ""},
		{"a b", true, ""},
	}
	for _, tc := range cases {
		s.cookieSecure = tc.secure
		req := httptest.NewRequest(http.MethodGet, "/taxii2/", nil)
		req.Host = tc.host
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = req
		if got := s.taxiiBaseURL(c); got != tc.want {
			t.Errorf("host %q secure=%v: %q, beklenen %q", tc.host, tc.secure, got, tc.want)
		}
	}
}

func TestTAXII_RateLimitReturnsTAXIIError(t *testing.T) {
	s, tok, _ := newTAXIIServer(t)
	s.rateLimiter.UpdatePolicy(0.001, 1)
	var last *httptest.ResponseRecorder
	for i := 0; i < 3; i++ {
		last = taxiiGet(s, "/taxii2/kh/collections/", withBasic("", tok), "")
	}
	if last.Code != http.StatusTooManyRequests || last.Header().Get("Content-Type") != taxiiMediaType ||
		!strings.Contains(last.Body.String(), `"http_status":"429"`) || last.Header().Get("Retry-After") == "" {
		t.Fatalf("hız sınırı TAXII hata nesnesi döndürmeli: %d %q %s", last.Code, last.Header().Get("Content-Type"), last.Body.String())
	}
}

func hasID(env taxiiEnv, id string) bool {
	for _, got := range objectIDs(env) {
		if got == id {
			return true
		}
	}
	return false
}

// Artımlı çeken istemci (added_after = son X-TAXII-Date-Added-Last) bulgu
// eklendikten SONRA çıkarılan IOC'leri ve eşiği sonradan geçen bulguyu da
// almalı (OpenCTI/Sentinel bu şekilde çeker).
func TestTAXII_IncrementalPollSeesLaterIOCsAndRaisedCriticality(t *testing.T) {
	s := newTestServer(t)
	ck := mkTokenUser(t, s, "cti", "cti-pass-123", storage.RoleAnalyst)
	tok := createToken(t, s, ck, "opencti", storage.TokenScopeRead, 0).Token
	ids := seedFeed(t, s, []seedFinding{{"Quiet post", "http://dddd.onion/p", "Ahmia", 2, taxiiT0, nil}})
	auth := withBearer(tok)

	poll := func(alias, after string) (taxiiEnv, string) {
		t.Helper()
		p := colPath(alias, "objects/")
		if after != "" {
			p += "?added_after=" + url.QueryEscape(after)
		}
		w := taxiiGet(s, p, auth, "")
		env := decodeEnv(t, w)
		last := w.Header().Get("X-TAXII-Date-Added-Last")
		if last == "" {
			last = after
		}
		return env, last
	}
	first, lastAll := poll("findings-all", "")
	if len(first.Objects) != 2 || lastAll != export.Timestamp(taxiiT0) {
		t.Fatalf("ilk çekim: kimlik + URL göstergesi beklenirdi: %v %q", objectIDs(first), lastAll)
	}
	_, lastIdx := poll("indicators", "")
	_, lastHigh := poll("findings-high", "")

	// Otomatik etiketleme IOC'yi sonradan çıkarır.
	if _, err := s.db.ReplaceArtifacts(ids[0], []storage.ResultArtifact{{Type: "email", Value: "late@x.io", Confidence: .8}}); err != nil {
		t.Fatal(err)
	}
	iocID := export.ArtifactIndicatorID("email", "late@x.io")
	inc, _ := poll("findings-all", lastAll)
	if !hasID(inc, iocID) || len(inc.Objects) != 3 {
		t.Fatalf("artımlı çekim yeni IOC göstergesini, ilişkisini ve URL göstergesinin yeni sürümünü getirmeli: %v", objectIDs(inc))
	}
	idx, _ := poll("indicators", lastIdx)
	if !hasID(idx, iocID) {
		t.Fatalf("indicators koleksiyonu sonradan çıkarılan IOC'yi getirmeli: %v", objectIDs(idx))
	}

	// Analist kritikliği 5'e çıkarır → bulgu findings-high'a artımlı olarak girer.
	if w := doJSON(s, http.MethodPost, "/api/update-criticality", fmt.Sprintf(`{"id":%d,"criticality":5,"category":"Genel"}`, ids[0]), ck, csrfOf(ck)); w.Code != http.StatusOK {
		t.Fatalf("kritiklik güncellenemedi: %d %s", w.Code, w.Body.String())
	}
	high, _ := poll("findings-high", lastHigh)
	if !hasID(high, export.URLIndicatorID("http://dddd.onion/p")) || !hasID(high, iocID) {
		t.Fatalf("eşiği sonradan geçen bulgu findings-high'a artımlı gelmeli: %v", objectIDs(high))
	}
}

// Tek nesne araması da tarama bütçesiyle sınırlıdır: bütçe dolarsa 404 değil
// "more": true + "next" döner; next ile devam edilince nesne bulunur.
func TestTAXII_ObjectLookupIsBudgetedAndResumable(t *testing.T) {
	s, _, _ := newTAXIIServer(t)
	col, _ := findTAXIICollection("findings-all")
	target := export.URLIndicatorID("http://cccc.onion/item") // son bulgu
	q := taxiiQuery{Limit: taxiiDefaultLimit, SpecOK: true, IDs: map[string]bool{target: true}}
	res, err := s.taxiiPage(context.Background(), col, q, 1)
	if err != nil || len(res.Objects) != 0 || !res.More {
		t.Fatalf("bütçe dolunca more=true beklenirdi: %+v %v", res, err)
	}
	for i := 0; i < 10 && res.More && len(res.Objects) == 0; i++ {
		q.Cursor = res.Next
		if res, err = s.taxiiPage(context.Background(), col, q, 1); err != nil {
			t.Fatal(err)
		}
	}
	if len(res.Objects) != 1 || res.Objects[0].ID != target {
		t.Fatalf("next ile devam edilince nesne bulunmalı: %+v", res)
	}
	// İptal edilen istek taramayı durdurur.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.taxiiPage(ctx, col, taxiiQuery{Limit: 10, SpecOK: true}, taxiiScanBudget); err == nil {
		t.Fatal("iptal edilmiş istekte tarama durmalı")
	}
}

// Eşzamanlı taramalar kullanıcı başına ve sunucu genelinde sınırlıdır; sınır
// aşılınca TAXII hata nesnesiyle 429 döner, yer bırakılınca istek geçer.
func TestTAXII_ConcurrentScansAreCapped(t *testing.T) {
	s, tok, _ := newTAXIIServer(t)
	var releases []func()
	for i := 0; i < taxiiMaxScansPerUser; i++ {
		rel, ok := s.taxiiScans.acquire("cti")
		if !ok {
			t.Fatal("yer ayrılamadı")
		}
		releases = append(releases, rel)
	}
	w := taxiiGet(s, colPath("findings-all", "objects/"), withBearer(tok), "")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Content-Type") != taxiiMediaType ||
		!strings.Contains(w.Body.String(), `"http_status":"429"`) || w.Header().Get("Retry-After") == "" {
		t.Fatalf("kullanıcı başına sınır: %d %s", w.Code, w.Body.String())
	}
	// Keşif/koleksiyon listesi tarama yapmaz; sınırdan etkilenmez.
	if w := taxiiGet(s, "/taxii2/kh/collections/", withBearer(tok), ""); w.Code != http.StatusOK {
		t.Fatalf("koleksiyon listesi: %d", w.Code)
	}
	for _, rel := range releases {
		rel()
		rel() // ikinci çağrı etkisiz
	}
	if w := taxiiGet(s, colPath("findings-all", "objects/"), withBearer(tok), ""); w.Code != http.StatusOK {
		t.Fatalf("yer bırakılınca istek geçmeli: %d", w.Code)
	}
	// Sunucu geneli sınır: başka kullanıcılar tüm yerleri doldurduysa.
	for i := 0; i < taxiiMaxScans; i++ {
		rel, ok := s.taxiiScans.acquire(fmt.Sprintf("other-%d", i))
		if !ok {
			t.Fatal("genel yer ayrılamadı")
		}
		defer rel()
	}
	if w := taxiiGet(s, colPath("findings-all", "manifest/"), withBearer(tok), ""); w.Code != http.StatusTooManyRequests {
		t.Fatalf("genel sınır: %d", w.Code)
	}
}
