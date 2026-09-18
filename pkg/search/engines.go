package search

import (
	"net/url"
	"strings"
)

// Engine bir dark web arama motorunu tanımlar.
type Engine struct {
	Name string
	URL  string // {query} yer tutucusu içerir
	// DefaultActive ilk kurulumda motorun aramaya dahil olup olmayacağı.
	// Kullanıcı /monitor ekranından her motoru açıp kapatabilir; bu yalnızca
	// engine_stats tablosuna ilk yazılan değerdir.
	DefaultActive bool
	// Note motorun durumu hakkında kısa açıklama (UI'da gösterilir).
	Note string
}

// SearchEngines dark web arama motorları.
//
// Liste 2026-09-18 tarihinde gerçek Tor devresi üzerinden üç ayrı turda, iki
// farklı sorguyla ("market", "leak") canlı doğrulanmıştır. Ölçüt: HTTP 200 ve
// yanıt gövdesinde motorun kendi alan adı dışında en az 10 benzersiz v3 .onion
// bağlantısı. Doğrulama sonuçları (benzersiz sonuç sayısı, market/leak):
//
//	Tordex 82/68 · Amnesia 53/45 · Tor66 26/22 · Onionway 24/24 · OnionLand 18/24
//	Torland 23/12 · Excavator 21/21 · TorNet 17/3 · Submarine 10/10 · Danex 10/10
//
// Varsayılan olarak PASİF bırakılanlar (elle etkinleştirilebilir):
//
//	Torch    — üç turda da zaman aşımı (adres hâlâ yayında ama aşırı yüklü/kararsız)
//	Ahmia    — .onion aynası aramayı ana sayfaya yönlendiriyor, ahmia.fi Tor çıkışlarını engelliyor
//	OSS/Torgle — her istekte captcha; HTTP istemcisiyle sonuç alınamıyor
//	Torgol   — arama formu JavaScript ile çalışıyor, sunucu tarafı sonuç yok
//	DeepSearches — dizin boş ("About 0 results found")
//	FindTor/Haystak — erişilemiyor
//
// Yeni bir motor eklerken yalnızca Tor üzerinden 2xx dönen ve arama sonucu
// olarak .onion bağlantıları üreten adresleri ekleyin; DefaultActive'i canlı
// doğrulamadan sonra true yapın.
var SearchEngines = []Engine{
	{Name: "Tordex", URL: "http://tordexu73joywapk2txdr54jed4imqledpcvcuf75qsas2gwdgksvnyd.onion/search?query={query}", DefaultActive: true, Note: "En yüksek kapsam"},
	{Name: "Amnesia", URL: "http://amnesia7u5odx5xbwtpnqk3edybgud5bmiagu75bnqx2crntw5kry7ad.onion/search?query={query}", DefaultActive: true},
	{Name: "Tor66", URL: "http://tor66sewebgixwhcqfnp5inzp5x5uohhdy3kvtnyfxc2e5mxiuh34iid.onion/search?q={query}", DefaultActive: true},
	{Name: "Onionway", URL: "http://oniwayzz74cv2puhsgx4dpjwieww4wdphsydqvf5q7eyz4myjvyw26ad.onion/search.php?s={query}", DefaultActive: true},
	{Name: "OnionLand", URL: "http://3bbad7fauom4d6sgppalyqddsqbf5u5p56b5k5uk2zxsy3d6ey2jobad.onion/search?q={query}", DefaultActive: true},
	{Name: "Torland", URL: "http://torlbmqwtudkorme6prgfpmsnile7ug2zm4u3ejpcncxuhpu4k2j4kyd.onion/index.php?a=search&q={query}", DefaultActive: true},
	{Name: "Excavator", URL: "http://2fd6cemt4gmccflhm6imvdfvli3nf7zn6rfrwpsy7uhxrgbypvwf5fad.onion/search?query={query}", DefaultActive: true},
	{Name: "TorNet", URL: "http://tornetupfu7gcgidt33ftnungxzyfq2pygui5qdoyss34xbgx2qruzid.onion/search?q={query}", DefaultActive: true},
	{Name: "Submarine", URL: "http://submariwvjhyg7acbrw3thxjxxsatwpieoioenyd4lde6y2rrnzkcwad.onion/?q={query}", DefaultActive: true, Note: "Yeni adres (eski no6m4… yönlendiriyor)"},
	{Name: "Danex", URL: "http://danexio627wiswvlpt6ejyhpxl5gla5nt2tgvgm2apj2ofrgm44vbeyd.onion/search?q={query}", DefaultActive: true},
	{Name: "Torch", URL: "http://torchdeedp3i2jigzjdmfpn5ttjhthh5wbmda2rr3jvqjg5p77c54dqd.onion/search?query={query}", DefaultActive: false, Note: "Kararsız: doğrulamada zaman aşımı"},
	{Name: "Ahmia", URL: "http://juhanurmihxlp77nkq76byazcldy2hlmovfu2epvl5ankdibsot4csyd.onion/search/?q={query}", DefaultActive: false, Note: "Onion aynası arama sonucu döndürmüyor"},
	{Name: "Torgle", URL: "http://iy3544gmoeclh5de6gez2256v6pjh4omhpqdh2wpeeppjtvqmjhkfwad.onion/torgle/?query={query}", DefaultActive: false, Note: "Captcha"},
	{Name: "OSS", URL: "http://3fzh7yuupdfyjhwt3ugzqqof6ulbcl27ecev33knxe3u7goi3vfn2qqd.onion/oss/index.php?search={query}", DefaultActive: false, Note: "Captcha"},
	{Name: "Torgol", URL: "http://torgolnpeouim56dykfob6jh5r2ps2j73enc42s2um4ufob3ny4fcdyd.onion/?q={query}", DefaultActive: false, Note: "JavaScript gerektiriyor"},
	{Name: "DeepSearches", URL: "http://searchgf7gdtauh7bhnbyed4ivxqmuoat3nm6zfrg3ymkq6mtnpye3ad.onion/search?q={query}", DefaultActive: false, Note: "Dizin boş"},
	{Name: "FindTor", URL: "http://findtorroveq5wdnipkaojfpqulxnkhblymc7aramjzajcvpptd4rjqd.onion/search?q={query}", DefaultActive: false, Note: "Erişilemiyor"},
}

// EngineCount tanımlı arama motoru sayısını döndürür (UI ve loglar için).
func EngineCount() int {
	return len(SearchEngines)
}

// DefaultActiveCount varsayılan olarak aktif motor sayısı.
func DefaultActiveCount() int {
	n := 0
	for _, e := range SearchEngines {
		if e.DefaultActive {
			n++
		}
	}
	return n
}

// EngineNames tüm motor adlarını döndürür (engine_stats budaması için).
func EngineNames() []string {
	names := make([]string, 0, len(SearchEngines))
	for _, e := range SearchEngines {
		names = append(names, e.Name)
	}
	return names
}

var searchEngineDomainSet = buildSearchEngineDomainSet()

func buildSearchEngineDomainSet() map[string]struct{} {
	set := make(map[string]struct{}, len(SearchEngines))

	for _, engine := range SearchEngines {
		parsed, err := url.Parse(engine.URL)
		if err != nil {
			continue
		}

		host := strings.ToLower(parsed.Hostname())
		if host != "" {
			set[host] = struct{}{}
		}
	}
	// Eski/yönlendiren adresler de motor sayılır (sonuç olarak listelenmesin)
	for _, legacy := range []string{
		"no6m4wzdexe3auiupv2zwif7rm6qwxcyhslkcnzisxgeiw6pvjsgafad.onion", // Submarine eski
		"haystak5njsmn2hqkewecpaxetahtwhsbsa64jom2k22z5afxhnpxfid.onion",
		"ahmia.fi",
	} {
		set[legacy] = struct{}{}
	}

	return set
}

func isSearchEngineDomain(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err == nil {
		host := strings.ToLower(parsed.Hostname())
		if host != "" {
			if _, ok := searchEngineDomainSet[host]; ok {
				return true
			}
		}
	}

	lowerURL := strings.ToLower(rawURL)
	for domain := range searchEngineDomainSet {
		if strings.Contains(lowerURL, domain) {
			return true
		}
	}

	return false
}
