package shared

import (
	"errors"
	"fmt"
	"testing"
)

func TestClassifyError_ReturnsSentinels(t *testing.T) {
	cases := []struct {
		in   string
		want error
	}{
		{"socks connect tcp 127.0.0.1:9150->x.onion:80: unknown error unknown code: 240", ErrSiteUnreachable},
		{"socks connect: general SOCKS server failure", ErrSiteUnreachable},
		{"dial tcp 127.0.0.1:9150: connect: connection refused", ErrTorUnreachable},
		{"socks connect tcp 127.0.0.1:9150->x.onion:80: host unreachable", ErrSiteNotResponding},
		{"Get \"http://x.onion\": context deadline exceeded", ErrFetchTimeout},
		{"i/o timeout", ErrFetchTimeout},
	}
	for _, tc := range cases {
		got := ClassifyError(errors.New(tc.in))
		if !errors.Is(got, tc.want) {
			t.Errorf("ClassifyError(%q) = %v, want %v", tc.in, got, tc.want)
		}
		if !errors.Is(FetchErrorKind(got), tc.want) {
			t.Errorf("FetchErrorKind(ClassifyError(%q)) != %v", tc.in, tc.want)
		}
	}
	other := errors.New("HTTP 404")
	if got := ClassifyError(other); got != other { //nolint:errorlint // kimlik karşılaştırması bilinçli: tanınmayan hata sarmalanmadan aynen dönmeli
		t.Errorf("tanınmayan hata değişmemeli: %v", got)
	}
	if ClassifyError(nil) != nil {
		t.Error("nil girdi nil dönmeli")
	}
}

// Scraper/tagging hatayı metin olarak taşır (Content.Error); FetchErrorKind
// sarmalanmış ve metne dönüşmüş hatayı da tanımalı.
func TestFetchErrorKind_WrappedAndStringified(t *testing.T) {
	for _, s := range fetchErrors {
		if got := FetchErrorKind(fmt.Errorf("üst katman: %w", s)); !errors.Is(got, s) {
			t.Errorf("%%w ile sarmalanmış %v tanınmadı: %v", s, got)
		}
		if got := FetchErrorKind(fmt.Errorf("%s", s.Error())); !errors.Is(got, s) {
			t.Errorf("metne dönüşmüş %v tanınmadı: %v", s, got)
		}
		if got := FetchErrorKind(fmt.Errorf("sayfa çekilemedi: %s", s.Error())); !errors.Is(got, s) {
			t.Errorf("önekli %v tanınmadı: %v", s, got)
		}
	}
	if FetchErrorKind(errors.New("HTTP hatası: 404")) != nil || FetchErrorKind(nil) != nil {
		t.Error("tanınmayan hata için nil beklenirdi")
	}
}
