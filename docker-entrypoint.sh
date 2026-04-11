#!/bin/sh
set -eu

ENV_FILE_PATH="${ENV_FILE:-/data/.env}"

mkdir -p "$(dirname "$ENV_FILE_PATH")"
mkdir -p /data/logs

if [ ! -f "$ENV_FILE_PATH" ]; then
  cp /app/.env.example "$ENV_FILE_PATH"
fi

set_key() {
  key="$1"
  value="$2"
  file="$3"

  if grep -q "^${key}=" "$file"; then
    sed -i "s|^${key}=.*|${key}=${value}|" "$file"
  else
    printf "%s=%s\n" "$key" "$value" >> "$file"
  fi
}

read_key() {
  key="$1"
  file="$2"
  grep "^${key}=" "$file" | head -n 1 | cut -d'=' -f2- | tr -d '\r' || true
}

# Docker icinde /data/.env kullaniliyorsa, local default degerleri docker degerlerine migrate et.
if [ "${ENV_FILE_PATH#"/data/"}" != "$ENV_FILE_PATH" ]; then
  current_tor="$(read_key "TOR_PROXY" "$ENV_FILE_PATH")"
  current_db="$(read_key "DB_PATH" "$ENV_FILE_PATH")"
  current_log="$(read_key "LOG_DIR" "$ENV_FILE_PATH")"

  if [ -z "$current_tor" ] || [ "$current_tor" = "127.0.0.1:9150" ]; then
    set_key "TOR_PROXY" "tor:9050" "$ENV_FILE_PATH"
  fi

  if [ -z "$current_db" ] || [ "$current_db" = "keywordhunter.db" ]; then
    set_key "DB_PATH" "/data/keywordhunter.db" "$ENV_FILE_PATH"
  fi

  if [ -z "$current_log" ] || [ "$current_log" = "logs" ]; then
    set_key "LOG_DIR" "/data/logs" "$ENV_FILE_PATH"
  fi
fi

current_user="$(read_key "ADMIN_USER" "$ENV_FILE_PATH")"
current_pass="$(read_key "ADMIN_PASS" "$ENV_FILE_PATH")"
current_level="$(read_key "LOG_LEVEL" "$ENV_FILE_PATH")"

if [ -z "$current_user" ]; then
  set_key "ADMIN_USER" "admin" "$ENV_FILE_PATH"
fi

if [ -z "$current_pass" ]; then
  set_key "ADMIN_PASS" "admin123" "$ENV_FILE_PATH"
fi

if [ -z "$current_level" ]; then
  set_key "LOG_LEVEL" "info" "$ENV_FILE_PATH"
fi

ln -sf "$ENV_FILE_PATH" /app/.env

exec "$@"
