## Özet / Summary
<!-- Bu PR neyi, neden değiştiriyor? -->

## Değişiklik türü / Type
- [ ] Hata düzeltme / Bug fix
- [ ] Yeni özellik / Feature
- [ ] Güvenlik / Security hardening
- [ ] Arayüz / UI-UX
- [ ] Belgeler / Docs

## Kontrol listesi / Checklist
- [ ] `gofmt -l cmd pkg` boş, `go vet ./...` temiz
- [ ] `go test -race ./...` geçiyor (CSP regresyon testleri dahil)
- [ ] Arayüz değişikliğinde: satır içi `<script>`/`on*=` yok, `data-action` + `static/js/pages/*.js` kullanıldı ([docs/FRONTEND.md](../docs/FRONTEND.md))
- [ ] Arayüz değişikliğinde: açık **ve** koyu temada, masaüstü **ve** mobilde kontrol edildi
- [ ] Dark web kaynaklı metin kaçışlanıyor; yeni yazma uçları CSRF + rol kontrolü altında
- [ ] Kullanıcıya görünen metinler TR + EN katalogda (`pkg/i18n/catalog_*.go`)

## Ekran görüntüleri / Screenshots
