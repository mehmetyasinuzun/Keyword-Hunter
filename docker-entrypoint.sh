#!/bin/sh
# KeywordHunter Docker giriş betiği.
#  - /data/.env yoksa şablondan oluşturur.
#  - Docker içi varsayılanları (tor:9050, /data/...) uygular; kullanıcı değerlerine dokunmaz.
#  - ADMIN_PASS yoksa RASTGELE güçlü bir parola üretir ve loga yazar (admin123 gibi
#    tahmin edilebilir bir varsayılan asla ayarlanmaz).
#  - /data izinlerini düzeltip uygulamayı root olmayan 'kh' kullanıcısıyla başlatır.
set -eu

ENV_FILE_PATH="${ENV_FILE:-/data/.env}"
DATA_DIR="$(dirname "$ENV_FILE_PATH")"

mkdir -p "$DATA_DIR" /data/logs /data/screenshots

if [ ! -f "$ENV_FILE_PATH" ]; then
  # Şablondaki örnek parolayı ASLA kopyalama; aşağıda rastgele üretilecek
  grep -vE '^(ADMIN_PASS|ADMIN_PASS_HASH)=' /app/.env.example > "$ENV_FILE_PATH"
fi

set_key() {
  key="$1"; value="$2"; file="$3"
  # sed ayırıcı olarak '|' kullanılamaz (Tor adresleri/parolalar içerebilir); awk ile güvenli değiştir
  if grep -q "^${key}=" "$file"; then
    awk -v k="$key" -v v="$value" 'BEGIN{FS=OFS="="} $1==k {print k "=" v; next} {print}' "$file" > "$file.tmp" && mv "$file.tmp" "$file"
  else
    printf "%s=%s\n" "$key" "$value" >> "$file"
  fi
}

read_key() {
  key="$1"; file="$2"
  grep "^${key}=" "$file" | head -n 1 | cut -d'=' -f2- | tr -d '\r' | sed -e 's/^"//' -e 's/"$//' || true
}

# Docker içinde /data/.env kullanılıyorsa yerel varsayılanları docker değerlerine taşı
case "$ENV_FILE_PATH" in
  /data/*)
    cur="$(read_key TOR_PROXY "$ENV_FILE_PATH")"; [ -z "$cur" ] || [ "$cur" = "127.0.0.1:9150" ] && set_key TOR_PROXY "tor:9050" "$ENV_FILE_PATH"
    cur="$(read_key DB_PATH "$ENV_FILE_PATH")";   [ -z "$cur" ] || [ "$cur" = "keywordhunter.db" ] && set_key DB_PATH "/data/keywordhunter.db" "$ENV_FILE_PATH"
    cur="$(read_key LOG_DIR "$ENV_FILE_PATH")";   [ -z "$cur" ] || [ "$cur" = "logs" ] && set_key LOG_DIR "/data/logs" "$ENV_FILE_PATH"
    ;;
esac

[ -n "$(read_key ADMIN_USER "$ENV_FILE_PATH")" ] || set_key ADMIN_USER "admin" "$ENV_FILE_PATH"
[ -n "$(read_key LOG_LEVEL "$ENV_FILE_PATH")" ] || set_key LOG_LEVEL "info" "$ENV_FILE_PATH"

if [ -z "$(read_key ADMIN_PASS "$ENV_FILE_PATH")" ] && [ -z "$(read_key ADMIN_PASS_HASH "$ENV_FILE_PATH")" ]; then
  GEN_PASS="$(head -c 24 /dev/urandom | base64 | tr -d '/+=' | cut -c1-20)"
  set_key ADMIN_PASS "$GEN_PASS" "$ENV_FILE_PATH"
  echo "==========================================================="
  echo " KeywordHunter: ilk kurulum — yönetici parolası üretildi"
  echo "   Kullanıcı : $(read_key ADMIN_USER "$ENV_FILE_PATH")"
  echo "   Parola    : $GEN_PASS"
  echo " Bu parola $ENV_FILE_PATH içinde saklanır; /settings ekranından"
  echo " değiştirdiğinizde bcrypt hash olarak yeniden yazılır."
  echo "==========================================================="
fi

chmod 600 "$ENV_FILE_PATH" 2>/dev/null || true
chown -R kh:kh /data 2>/dev/null || true

# Root ile başladıysak root olmayan kullanıcıya düş
if [ "$(id -u)" = "0" ]; then
  exec su-exec kh "$@"
fi
exec "$@"
