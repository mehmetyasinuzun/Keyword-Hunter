package misp

import (
	"errors"
	"fmt"
)

// Error kullanıcıya gösterilebilen iki dilli hata: Error() Türkçe (birincil
// dil, loglar) metni döndürür; Text(lang) arayüz diline göre seçer. Kind,
// errors.Is ile sınanabilecek sentinel hatalardır (ör. ErrInvalidURL).
type Error struct {
	TR, EN string
	Kind   []error
}

func (e *Error) Error() string { return e.TR }

// Unwrap sentinel hataları errors.Is/As için açar.
func (e *Error) Unwrap() []error { return e.Kind }

// Text hatanın istenen dildeki metni ("en" dışı her dil Türkçe).
func (e *Error) Text(lang string) string {
	if lang == "en" && e.EN != "" {
		return e.EN
	}
	return e.TR
}

// Errorf iki dilli hata kurar; args her iki biçime de uygulanır.
func Errorf(kind []error, tr, en string, args ...any) *Error {
	if len(args) > 0 {
		tr, en = fmt.Sprintf(tr, args...), fmt.Sprintf(en, args...)
	}
	return &Error{TR: tr, EN: en, Kind: kind}
}

// Message err'in dile göre metnini döndürür: zincirde iki dilli bir *Error
// varsa onun metni, yoksa err.Error().
func Message(err error, lang string) string {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Text(lang)
	}
	return err.Error()
}
