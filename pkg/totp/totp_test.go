package totp

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"net/url"
	"strings"
	"testing"
	"time"
)

// RFC 6238 Ek B (SHA-1, sır "12345678901234567890", 8 hane).
func TestRFC6238Vectors(t *testing.T) {
	key := []byte("12345678901234567890")
	vectors := []struct {
		unix int64
		code string
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
		{20000000000, "65353130"},
	}
	for _, v := range vectors {
		ts := time.Unix(v.unix, 0)
		if got := hotp(key, uint64(Step(ts)), 8); got != v.code {
			t.Errorf("T=%d: %s, beklenen %s", v.unix, got, v.code)
		}
		// 6 haneli kod 8 hanelinin son 6 hanesidir (aynı kesmenin 10^6 modu).
		if got := CodeAt(key, ts); got != v.code[2:] {
			t.Errorf("T=%d (6 hane): %s, beklenen %s", v.unix, got, v.code[2:])
		}
	}
}

// RFC 4226 Ek D (HOTP, 6 hane).
func TestRFC4226Vectors(t *testing.T) {
	key := []byte("12345678901234567890")
	want := []string{"755224", "287082", "359152", "969429", "338314", "254676", "287922", "162583", "399871", "520489"}
	for i, w := range want {
		if got := hotp(key, uint64(i), 6); got != w {
			t.Errorf("sayaç %d: %s, beklenen %s", i, got, w)
		}
	}
}

func TestVerifyWindowAndReplay(t *testing.T) {
	sec, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	key, err := DecodeSecret(sec)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_015, 0)
	cur := Step(now)

	// ±1 adım kabul, ±2 ret
	for d, ok := range map[int64]bool{-2: false, -1: true, 0: true, 1: true, 2: false} {
		code := hotp(key, uint64(cur+d), Digits)
		st, got := Verify(key, code, now, 0)
		if got != ok {
			t.Errorf("sapma %d: kabul=%v, beklenen %v", d, got, ok)
		}
		if got && st != cur+d {
			t.Errorf("sapma %d: dönen adım %d, beklenen %d", d, st, cur+d)
		}
	}

	// Yeniden oynatma: son kabul edilen adım ve öncesi reddedilir
	code := CodeAt(key, now)
	st, ok := Verify(key, code, now, 0)
	if !ok || st != cur {
		t.Fatalf("ilk doğrulama başarısız")
	}
	if _, ok := Verify(key, code, now, st); ok {
		t.Fatal("aynı kod aynı adımda ikinci kez kabul edildi (replay)")
	}
	prev := hotp(key, uint64(cur-1), Digits)
	if _, ok := Verify(key, prev, now, st); ok {
		t.Fatal("kabul edilmiş adımdan eski kod kabul edildi")
	}
	next := hotp(key, uint64(cur+1), Digits)
	if st2, ok := Verify(key, next, now, st); !ok || st2 != cur+1 {
		t.Fatal("sonraki adımın kodu kabul edilmeliydi")
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	key := []byte("12345678901234567890")
	now := time.Unix(59, 0)
	for _, c := range []string{"", "12345", "1234567", "abcdef", "12 34 5x", "٠١٢٣٤٥"} {
		if _, ok := Verify(key, c, now, 0); ok {
			t.Errorf("%q kabul edildi", c)
		}
	}
	// Boşluk/tire normalize edilir
	code := CodeAt(key, now)
	if _, ok := Verify(key, code[:3]+" "+code[3:], now, 0); !ok {
		t.Error("boşluklu kod kabul edilmeliydi")
	}
	if _, ok := Verify(nil, code, now, 0); ok {
		t.Error("boş anahtar ile doğrulama")
	}
}

func TestSecretRoundtripAndFormat(t *testing.T) {
	s, err := GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 32 || strings.Contains(s, "=") {
		t.Fatalf("20 bayt dolgusuz base32 32 karakter olmalı: %q", s)
	}
	k1, err := DecodeSecret(s)
	if err != nil || len(k1) != SecretSize {
		t.Fatalf("çözme: %v %d", err, len(k1))
	}
	k2, err := DecodeSecret(strings.ToLower(FormatSecret(s)))
	if err != nil || !bytes.Equal(k1, k2) {
		t.Fatal("gruplu/küçük harfli sır aynı anahtarı vermeli")
	}
	if g := FormatSecret("ABCDEFGHIJ"); g != "ABCD EFGH IJ" {
		t.Fatalf("gruplama: %q", g)
	}
	if _, err := DecodeSecret("AAAA"); err == nil {
		t.Fatal("kısa sır reddedilmeli")
	}
	if _, err := DecodeSecret("!!!!!!!!!!!!!!!!"); err == nil {
		t.Fatal("geçersiz base32 reddedilmeli")
	}
}

func TestURI(t *testing.T) {
	u := URI("KeywordHunter", "şule ops", "JBSWY3DPEHPK3PXP")
	p, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	if p.Scheme != "otpauth" || p.Host != "totp" {
		t.Fatalf("şema/tür: %s", u)
	}
	if p.Path != "/KeywordHunter:şule ops" {
		t.Fatalf("etiket: %q", p.Path)
	}
	q := p.Query()
	if q.Get("secret") != "JBSWY3DPEHPK3PXP" || q.Get("issuer") != "KeywordHunter" || q.Get("digits") != "6" || q.Get("period") != "30" || q.Get("algorithm") != "SHA1" {
		t.Fatalf("parametreler: %v", q)
	}
}

func TestSealer(t *testing.T) {
	if _, err := NewSealer("abc"); err == nil {
		t.Fatal("kısa APP_SECRET reddedilmeli")
	}
	if _, err := NewSealer(strings.Repeat("zz", 32)); err == nil {
		t.Fatal("hex olmayan APP_SECRET reddedilmeli")
	}
	key, err := GenerateAppSecret()
	if err != nil || !ValidAppSecret(key) {
		t.Fatalf("üretilen APP_SECRET geçersiz: %v", err)
	}
	s, err := NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	const secret = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
	a, err := s.Seal(secret, "user:1")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.Seal(secret, "user:1")
	if a == b {
		t.Fatal("her mühür rastgele nonce kullanmalı")
	}
	if strings.Contains(a, secret) || !strings.HasPrefix(a, "v1:") {
		t.Fatalf("şifreli değer düz metni içermemeli: %s", a)
	}
	if got, err := s.Open(a, "user:1"); err != nil || got != secret {
		t.Fatalf("çözme: %v %q", err, got)
	}
	if _, err := s.Open(a, "user:2"); err == nil {
		t.Fatal("başka kullanıcının bağlamıyla çözülmemeli (AAD)")
	}
	other, _ := GenerateAppSecret()
	s2, _ := NewSealer(other)
	if _, err := s2.Open(a, "user:1"); err == nil {
		t.Fatal("başka anahtarla çözülmemeli")
	}
	tampered := a[:len(a)-2] + "AA"
	if _, err := s.Open(tampered, "user:1"); err == nil {
		t.Fatal("değiştirilmiş değer reddedilmeli")
	}
	for _, bad := range []string{"", "v1:", "v2:abc", "v1:!!!"} {
		if _, err := s.Open(bad, "user:1"); err == nil {
			t.Errorf("%q reddedilmeli", bad)
		}
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, err := GenerateRecoveryCodes(RecoveryCodeCount)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != RecoveryCodeCount {
		t.Fatalf("kod sayısı: %d", len(codes))
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if len(c) != 9 || c[4] != '-' {
			t.Fatalf("biçim xxxx-xxxx olmalı: %q", c)
		}
		if seen[c] {
			t.Fatalf("yinelenen kod: %s", c)
		}
		seen[c] = true
		n := NormalizeRecoveryCode(c)
		if n == "" || len(n) != 8 {
			t.Fatalf("normalize: %q → %q", c, n)
		}
		if NormalizeRecoveryCode(" "+strings.ToUpper(c)+" ") != n {
			t.Fatal("büyük harf/boşluk normalize edilmeli")
		}
	}
	for _, bad := range []string{"", "abcd", "abcd-efgh-ijkl", "abcd-efg!", "ıııı-ıııı", strings.Repeat("a", 100)} {
		if NormalizeRecoveryCode(bad) != "" {
			t.Errorf("%q reddedilmeli", bad)
		}
	}
	h1 := HashRecoveryCode(1, "abcd2345")
	if h1 == HashRecoveryCode(2, "abcd2345") {
		t.Fatal("özet kullanıcıya bağlı olmalı")
	}
	if len(h1) != 64 || strings.Contains(h1, "abcd2345") {
		t.Fatalf("özet: %s", h1)
	}
}

func TestQRDataURI(t *testing.T) {
	uri, err := QRDataURI(URI("KeywordHunter", "admin", "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"))
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := strings.CutPrefix(uri, "data:image/png;base64,")
	if !ok {
		t.Fatalf("data URI öneki: %.40s", uri)
	}
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	w := img.Bounds().Dx()
	if w != img.Bounds().Dy() || w%qrModulePx != 0 {
		t.Fatalf("kare ve modül katı olmalı: %v", img.Bounds())
	}
	modules := w/qrModulePx - 2*qrQuietZone
	if modules < 21 || (modules-21)%4 != 0 {
		t.Fatalf("geçerli bir QR sürüm boyutu değil: %d modül", modules)
	}
	dark := func(mx, my int) bool {
		r, _, _, _ := img.At((mx+qrQuietZone)*qrModulePx+1, (my+qrQuietZone)*qrModulePx+1).RGBA()
		return r < 0x8000
	}
	// Sessiz bölge beyaz, üç köşede 7x7 bulucu deseni (dış halka koyu, iç halka açık, 3x3 çekirdek koyu)
	if r, _, _, _ := img.At(1, 1).RGBA(); r < 0x8000 {
		t.Fatal("sessiz bölge beyaz olmalı")
	}
	for _, o := range [][2]int{{0, 0}, {modules - 7, 0}, {0, modules - 7}} {
		if !dark(o[0], o[1]) || !dark(o[0]+6, o[1]+6) || dark(o[0]+1, o[1]+1) || !dark(o[0]+3, o[1]+3) {
			t.Fatalf("bulucu deseni eksik: köşe %v", o)
		}
	}
	if _, err := QRDataURI(strings.Repeat("x", 5000)); err == nil {
		t.Fatal("aşırı uzun içerik reddedilmeli")
	}
}
