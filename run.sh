#!/usr/bin/env bash
# KeywordHunter — macOS / Linux başlatıcı
#   ./run.sh            derle + çalıştır
#   ./run.sh build      yalnızca derle (./bin/keywordhunter)
#   ./run.sh test       testler (race)
#   ./run.sh docker     docker compose up -d --build
#   ./run.sh release    tüm platformlar için ikili üret (./dist)
set -euo pipefail
cd "$(dirname "$0")"

need_go() {
  if ! command -v go >/dev/null 2>&1; then
    echo "[HATA] Go bulunamadı. Kurulum: https://go.dev/dl/  (macOS: brew install go)"; exit 1
  fi
}

ensure_env() {
  if [ ! -f .env ]; then
    cp .env.example .env
    echo "[*] .env oluşturuldu — ADMIN_PASS değerini değiştirmeden üretimde kullanmayın."
  fi
}

check_tor() {
  local proxy; proxy="$(grep -E '^TOR_PROXY=' .env 2>/dev/null | cut -d= -f2- | tr -d '"' || true)"
  proxy="${proxy:-127.0.0.1:9150}"
  local host="${proxy%%:*}" port="${proxy##*:}"
  if command -v nc >/dev/null 2>&1 && ! nc -z -w 2 "$host" "$port" 2>/dev/null; then
    echo "[UYARI] Tor $proxy adresinde yanıt vermiyor. Tor Browser'ı açın (9150) veya 'tor' servisini başlatın (9050)."
    echo "        macOS: brew install tor && brew services start tor   → .env içinde TOR_PROXY=127.0.0.1:9050"
    echo "        Linux: sudo apt install tor && sudo systemctl start tor"
  fi
}

case "${1:-run}" in
  build)   need_go; mkdir -p bin; go build -trimpath -ldflags="-s -w -X keywordhunter-mvp/pkg/web.Version=$(git describe --tags --always 2>/dev/null || echo dev)" -o bin/keywordhunter ./cmd; echo "[+] bin/keywordhunter" ;;
  test)    need_go; go vet ./... && go test -race ./... ;;
  docker)  mkdir -p data; docker compose up -d --build; echo "[+] http://localhost:8080  — ilk parola: docker compose logs app | grep Parola" ;;
  release) need_go; rm -rf dist; mkdir -p dist
           for pair in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
             os="${pair%/*}"; arch="${pair#*/}"; ext=""; [ "$os" = windows ] && ext=".exe"
             CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags="-s -w" -o "dist/keywordhunter-$os-$arch$ext" ./cmd && echo "[+] dist/keywordhunter-$os-$arch$ext"
           done ;;
  run|*)   need_go; ensure_env; check_tor; mkdir -p bin
           go build -trimpath -o bin/keywordhunter ./cmd
           echo "[*] Başlatılıyor → http://localhost$(grep -E '^WEB_ADDR=' .env | cut -d= -f2- | tr -d '"' || echo :8080)"
           exec ./bin/keywordhunter ;;
esac
