package web

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/i18n"
	"keywordhunter-mvp/pkg/shared"
)

// API hata sözleşmesi
//
// /api altındaki her hata yanıtı iki alan taşır:
//
//	{"error": "<istek dilinde insan okunur ileti>", "code": "<kararlı kod>"}
//
// "code" makinelerin eşleşeceği sözleşmedir: snake_case, dilden bağımsızdır ve
// sürümler arasında değişmez. "error" metni ise çeviridir; yeniden yazılabilir,
// istemciler onunla eşleşmemelidir. İletiler pkg/i18n/catalog_apierr.go
// içinde "apierr.<code>" anahtarıyla TR+EN tutulur. Dinamik parçalar katalog
// metnindeki fmt fiilleriyle (%d, %s) eklenir: biçim dizesi her zaman katalogdan
// gelir, güvenilmeyen girdi yalnızca argüman olarak geçer (biçim dizesine asla).
//
// Mevcut ek alanlar (success:false, retryAfter…) geriye uyumluluk için korunur:
// apiFail success:false ekler, apiErrBody ise ek alan gereken özel durumlar
// için gövdeyi döndürür.

// maxAcceptLanguage Accept-Language başlığının ayrıştırılan en uzun kısmı
// (bayt). Sınırsız başlık her istekte boşuna CPU yakmasın.
const maxAcceptLanguage = 512

// maxAcceptLanguageTags ayrıştırılan en fazla dil etiketi.
const maxAcceptLanguageTags = 16

// apiLang API yanıtlarının dili: önce "lang" çerezi (arayüzde seçilen dil),
// yoksa Accept-Language (token/TAXII gibi tarayıcı dışı istemciler), o da
// yoksa varsayılan (tr). Tarayıcı arayüzü fetch çağrılarına sayfanın dilini
// Accept-Language olarak ekler (app.js), böylece çerezsiz bir kullanıcı da
// sayfayla aynı dilde hata görür.
func apiLang(c *gin.Context) string {
	if lang, err := c.Cookie("lang"); err == nil && i18n.IsSupported(strings.ToLower(strings.TrimSpace(lang))) {
		return strings.ToLower(strings.TrimSpace(lang))
	}
	if lang, ok := parseAcceptLanguage(c.GetHeader("Accept-Language")); ok {
		return lang
	}
	return i18n.DefaultLang
}

// parseAcceptLanguage RFC 9110 Accept-Language başlığından desteklenen en
// yüksek öncelikli dili seçer ("en-US;q=0.9, tr;q=0.8" → en). Eşit q
// değerinde başlıktaki sıra belirleyicidir; q=0 "istemiyorum" demektir ve
// "*" belirli bir dil seçtirmez (varsayılana bırakılır).
func parseAcceptLanguage(header string) (string, bool) {
	if len(header) > maxAcceptLanguage {
		header = header[:maxAcceptLanguage]
	}
	best, bestQ := "", 0.0
	for i, part := range strings.Split(header, ",") {
		if i >= maxAcceptLanguageTags {
			break
		}
		tag, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		tag = strings.ToLower(strings.TrimSpace(tag))
		if j := strings.IndexAny(tag, "-_"); j > 0 {
			tag = tag[:j]
		}
		if !i18n.IsSupported(tag) {
			continue
		}
		q := 1.0
		for _, p := range strings.Split(params, ";") {
			k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
			if !ok || strings.ToLower(strings.TrimSpace(k)) != "q" {
				continue
			}
			f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil || f < 0 || f > 1 {
				f = 0 // bozuk q değeri: güvenli tarafta kal, bu etiketi yok say
			}
			q = f
		}
		if q > bestQ {
			best, bestQ = tag, q
		}
	}
	return best, best != ""
}

// apiMsg "apierr.<code>" iletisini verilen dilde biçimler. Argüman yoksa
// metin olduğu gibi döner (katalogdaki olası bir % karakteri bozulmasın).
func apiMsg(lang, code string, args ...any) string {
	format := i18n.T(lang, "apierr."+code)
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}

// apiErrBody hata gövdesini {"error","code"} olarak kurar. Ek alan gereken
// yanıtlar (retryAfter vb.) gövdeyi bununla alıp genişletir.
func apiErrBody(c *gin.Context, code string, args ...any) gin.H {
	return gin.H{"error": apiMsg(apiLang(c), code, args...), "code": code}
}

// apiError çevrilmiş hata yanıtını yazar: {"error": <ileti>, "code": <code>}.
func apiError(c *gin.Context, status int, code string, args ...any) {
	c.JSON(status, apiErrBody(c, code, args...))
}

// apiFail apiError'ın success:false taşıyan biçimi (bu alanı bekleyen eski
// istemciler için korunur).
func apiFail(c *gin.Context, status int, code string, args ...any) {
	body := apiErrBody(c, code, args...)
	body["success"] = false
	c.JSON(status, body)
}

// apiAbort ara katmanlar için: hata yanıtını yazar ve zinciri keser.
func apiAbort(c *gin.Context, status int, code string, args ...any) {
	c.AbortWithStatusJSON(status, apiErrBody(c, code, args...))
}

// apiText başarı yanıtlarındaki "message" metni ("apimsg.<key>"), istek dilinde.
func apiText(c *gin.Context, key string) string {
	return i18n.T(apiLang(c), "apimsg."+key)
}

// codedError ayrıştırma/doğrulama yardımcılarının döndürdüğü, istemciye kodla
// iletilecek hata. Error() Türkçe iletiyi verir (log ve testler için); yanıt
// apiErrorFrom ile istek dilinde yazılır.
type codedError struct {
	code string
	args []any
}

func (e *codedError) Error() string { return apiMsg(i18n.DefaultLang, e.code, e.args...) }

// errCode kodlu bir doğrulama hatası kurar (bkz. codedError).
func errCode(code string, args ...any) error {
	return &codedError{code: code, args: args}
}

// apiErrorFrom kodlu hatayı verilen durumla yazar; kodsuz (beklenmeyen) bir
// hata istemciye sızdırılmaz, genel sunucu hatası olarak loglanıp yanıtlanır.
func apiErrorFrom(c *gin.Context, status int, err error) {
	var ce *codedError
	if errors.As(err, &ce) {
		apiError(c, status, ce.code, ce.args...)
		return
	}
	respondInternalError(c, c.FullPath(), err)
}

// errDetail sarmalanmış bir hatanın sentinel önekini atar ("sayfa çekilemedi:
// <ayrıntı>" → "<ayrıntı>"): ileti çevrilmiş sabit metin + ham ayrıntıdan
// oluşsun, ayrıntı içinde ikinci kez Türkçe önek görünmesin.
func errDetail(err, sentinel error) string {
	msg := err.Error()
	if sentinel != nil {
		if rest, ok := strings.CutPrefix(msg, sentinel.Error()+": "); ok {
			return rest
		}
	}
	return msg
}

// fetchFailCode sınıflandırılmış bir çekme hatasının (Tor kapalı, site
// çevrimdışı, zaman aşımı…) kararlı kodunu verir. Tanınmayan hatada ok=false.
func fetchFailCode(err error) (code string, ok bool) {
	switch shared.FetchErrorKind(err) {
	case shared.ErrTorUnreachable:
		code = "tor_unreachable"
	case shared.ErrSiteUnreachable:
		code = "site_unreachable"
	case shared.ErrSiteNotResponding:
		code = "site_not_responding"
	case shared.ErrFetchTimeout:
		code = "site_timeout"
	default:
		return "", false
	}
	return code, true
}

// fetchErrBody bir sayfa çekme hatasının gövdesini kurar. Hata tanınan bir
// sınıfa giriyorsa kod o sınıfınkidir ve ileti tamamen istek dilindedir
// ("Scan error: Could not connect to the Tor proxy…"); alt katmanın (Türkçe)
// metni yanıta girmez. Tanınmayan hatada wrapCode ve kırpılmış ham ayrıntı
// kullanılır. sentinel verilirse ayrıntıdaki önek atılır (bkz. errDetail).
func fetchErrBody(c *gin.Context, wrapCode string, err, sentinel error) gin.H {
	lang := apiLang(c)
	if code, ok := fetchFailCode(err); ok {
		return gin.H{"error": apiMsg(lang, wrapCode, apiMsg(lang, code)), "code": code}
	}
	return gin.H{"error": apiMsg(lang, wrapCode, truncateErr(errDetail(err, sentinel))), "code": wrapCode}
}

// ceilSeconds kalan süreyi kullanıcıya gösterilecek tam saniyeye yukarı
// yuvarlar (en az 1): "0 sn sonra tekrar deneyin" yazılmasın.
func ceilSeconds(d time.Duration) int {
	secs := int((d + time.Second - 1) / time.Second)
	if secs < 1 {
		secs = 1
	}
	return secs
}
