package totp

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"

	qrcode "github.com/skip2/go-qrcode"
)

// QR görüntüsü parametreleri: modül başına 6 piksel ve 4 modüllük sessiz
// bölge (ISO/IEC 18004 asgarisi). otpauth URI'si tipik olarak sürüm 6–7
// (41–45 modül) üretir → ~290 piksel; sayfada CSS ile ölçeklenir.
const (
	qrModulePx  = 6
	qrQuietZone = 4
	// qrMaxInput URI'nin üst sınırı (kullanıcı adı 64 rün ile sınırlı; pay bırakıldı).
	qrMaxInput = 1024
)

// QRDataURI metni (otpauth:// URI'si) orta düzey hata düzeltmeli bir QR
// koda çevirir ve "data:image/png;base64,…" olarak döndürür. Görüntü her
// zaman beyaz zemin üzerine siyahtır: koyu temada ters renkli QR'ları bazı
// tarayıcı uygulamaları okuyamaz. CSP img-src data: izin verir.
func QRDataURI(text string) (string, error) {
	if len(text) > qrMaxInput {
		return "", errQRTooLong
	}
	q, err := qrcode.New(text, qrcode.Medium)
	if err != nil {
		return "", err
	}
	q.DisableBorder = true // sessiz bölgeyi biz çiziyoruz (boyut kontrolü)
	bm := q.Bitmap()
	n := len(bm)
	side := (n + 2*qrQuietZone) * qrModulePx
	// 1 bitlik palet: en küçük PNG (~1–2 KB).
	img := image.NewPaletted(image.Rect(0, 0, side, side), color.Palette{color.White, color.Black})
	for y, row := range bm {
		for x, black := range row {
			if !black {
				continue
			}
			x0, y0 := (x+qrQuietZone)*qrModulePx, (y+qrQuietZone)*qrModulePx
			for dy := 0; dy < qrModulePx; dy++ {
				off := img.PixOffset(x0, y0+dy)
				for dx := 0; dx < qrModulePx; dx++ {
					img.Pix[off+dx] = 1
				}
			}
		}
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

var errQRTooLong = errors.New("totp: QR içeriği çok uzun")
