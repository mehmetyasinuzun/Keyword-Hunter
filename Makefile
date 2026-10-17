.PHONY: run build test release docker lint demo seed e2e
run:      ; ./run.sh
build:    ; ./run.sh build
test:     ; ./run.sh test
release:  ; ./run.sh release
docker:   ; ./run.sh docker
lint:     ; gofmt -l cmd pkg && go vet ./...
# Demo verisi: deterministik veri kümesini data/demo.db'ye kurar (+ çalıştırır).
# Admin kimliği .env'den; analyst / viewer demo parolaları CONTRIBUTING.md'de.
demo:     ; ./run.sh demo
seed:     ; ./run.sh seed
# Uçtan uca testler: çalışan bir sunucuya karşı (KH_BASE_URL, KH_ADMIN_PASS).
e2e:      ; cd e2e && npm ci && npx playwright test
