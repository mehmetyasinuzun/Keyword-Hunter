package artifact

import "testing"

func TestExtract_FindsCommonIOCs(t *testing.T) {
	text := `Contact: leaks@protonmail.com or admin@example.com. BTC 1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2 wallet.
	Monero 44AFFq5kSiGBoZ4NMDwYtN18obc8AemS33DBLWs3H7otXft3XjrpDtQGv7SqSsaBYBb98uNbr2VBBEt7f2wfn3RVGQBEP3A
	server 8.8.8.8 and 10.0.0.1, hash 5d41402abc4b2a76b9719d911017c592, card 4111111111111111 fake 4111111111111112
	mirror: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.onion self bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.onion
	phone +90 532 123 45 67 price 1500 -----BEGIN RSA PRIVATE KEY-----`
	got := NewExtractor().Extract(text, "http://bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.onion/page")
	byType := map[ArtifactType][]string{}
	for _, a := range got {
		byType[a.Type] = append(byType[a.Type], a.Value)
	}
	if len(byType[TypeEmail]) != 1 || byType[TypeEmail][0] != "leaks@protonmail.com" {
		t.Errorf("email: %v (example.com elenmeli)", byType[TypeEmail])
	}
	if len(byType[TypeBitcoin]) != 1 {
		t.Errorf("bitcoin: %v", byType[TypeBitcoin])
	}
	if len(byType[TypeMonero]) != 1 {
		t.Errorf("monero: %v", byType[TypeMonero])
	}
	if len(byType[TypeIP]) != 1 || byType[TypeIP][0] != "8.8.8.8" {
		t.Errorf("ip: %v (özel IP elenmeli)", byType[TypeIP])
	}
	if len(byType[TypeHash]) != 1 {
		t.Errorf("hash: %v", byType[TypeHash])
	}
	if len(byType[TypeCreditCard]) != 1 || byType[TypeCreditCard][0] != "4111111111111111" {
		t.Errorf("kart: %v (Luhn geçmeyen elenmeli)", byType[TypeCreditCard])
	}
	if len(byType[TypeOnion]) != 1 || byType[TypeOnion][0][:4] != "aaaa" {
		t.Errorf("onion: %v (kaynak sitenin kendi adresi elenmeli)", byType[TypeOnion])
	}
	if len(byType[TypePhone]) != 1 {
		t.Errorf("phone: %v (çıplak sayılar telefon sayılmamalı)", byType[TypePhone])
	}
	if len(byType[TypeSSH]) != 1 {
		t.Errorf("ssh: %v", byType[TypeSSH])
	}
}

func TestLuhn(t *testing.T) {
	if !luhnValid("4111111111111111") || luhnValid("4111111111111112") || luhnValid("abc") {
		t.Fatal("Luhn hatalı")
	}
}
