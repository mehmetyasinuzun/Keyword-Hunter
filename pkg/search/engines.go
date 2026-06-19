package search

import (
	"net/url"
	"strings"
)

// Dark Web arama motorları — her biri bir .onion adresi ve {query} yer tutucusu içerir.
//
// Liste, Tor üzerinden canlı erişilebilirlik testiyle doğrulanmıştır (son doğrulama: 2026-06).
// Ölü/terk edilmiş motorlar (Anima, DarkHunt, Kaizer, Tornado) kaldırılmış;
// yerlerine canlı doğrulanan yüksek kapsamlı Torch ve Tordex eklenmiştir.
// Yeni bir motor eklerken: yalnızca Tor üzerinden HTTP 2xx/3xx dönen ve arama
// sonucu olarak .onion bağlantıları üreten adresleri ekleyin.
var SearchEngines = []Engine{
	{Name: "Ahmia", URL: "http://juhanurmihxlp77nkq76byazcldy2hlmovfu2epvl5ankdibsot4csyd.onion/search/?q={query}"},
	{Name: "Torch", URL: "http://torchdeedp3i2jigzjdmfpn5ttjhthh5wbmda2rr3jvqjg5p77c54dqd.onion/search?query={query}"},
	{Name: "Tordex", URL: "http://tordexu73joywapk2txdr54jed4imqledpcvcuf75qsas2gwdgksvnyd.onion/search?query={query}"},
	{Name: "OnionLand", URL: "http://3bbad7fauom4d6sgppalyqddsqbf5u5p56b5k5uk2zxsy3d6ey2jobad.onion/search?q={query}"},
	{Name: "Tor66", URL: "http://tor66sewebgixwhcqfnp5inzp5x5uohhdy3kvtnyfxc2e5mxiuh34iid.onion/search?q={query}"},
	{Name: "Torgle", URL: "http://iy3544gmoeclh5de6gez2256v6pjh4omhpqdh2wpeeppjtvqmjhkfwad.onion/torgle/?query={query}"},
	{Name: "Amnesia", URL: "http://amnesia7u5odx5xbwtpnqk3edybgud5bmiagu75bnqx2crntw5kry7ad.onion/search?query={query}"},
	{Name: "TorNet", URL: "http://tornetupfu7gcgidt33ftnungxzyfq2pygui5qdoyss34xbgx2qruzid.onion/search?q={query}"},
	{Name: "Torland", URL: "http://torlbmqwtudkorme6prgfpmsnile7ug2zm4u3ejpcncxuhpu4k2j4kyd.onion/index.php?a=search&q={query}"},
	{Name: "FindTor", URL: "http://findtorroveq5wdnipkaojfpqulxnkhblymc7aramjzajcvpptd4rjqd.onion/search?q={query}"},
	{Name: "Excavator", URL: "http://2fd6cemt4gmccflhm6imvdfvli3nf7zn6rfrwpsy7uhxrgbypvwf5fad.onion/search?query={query}"},
	{Name: "Onionway", URL: "http://oniwayzz74cv2puhsgx4dpjwieww4wdphsydqvf5q7eyz4myjvyw26ad.onion/search.php?s={query}"},
	{Name: "OSS", URL: "http://3fzh7yuupdfyjhwt3ugzqqof6ulbcl27ecev33knxe3u7goi3vfn2qqd.onion/oss/index.php?search={query}"},
	{Name: "Torgol", URL: "http://torgolnpeouim56dykfob6jh5r2ps2j73enc42s2um4ufob3ny4fcdyd.onion/?q={query}"},
	{Name: "DeepSearches", URL: "http://searchgf7gdtauh7bhnbyed4ivxqmuoat3nm6zfrg3ymkq6mtnpye3ad.onion/search?q={query}"},
	{Name: "Submarine", URL: "http://no6m4wzdexe3auiupv2zwif7rm6qwxcyhslkcnzisxgeiw6pvjsgafad.onion/?q={query}"},
}

// EngineCount aktif arama motoru sayısını döndürür (UI ve loglar için).
func EngineCount() int {
	return len(SearchEngines)
}

var searchEngineDomainSet = buildSearchEngineDomainSet()

// Engine arama motoru yapısı
type Engine struct {
	Name string
	URL  string
}

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
