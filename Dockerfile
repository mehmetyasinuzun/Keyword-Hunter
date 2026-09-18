# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS builder

WORKDIR /src

RUN apk add --no-cache ca-certificates tzdata

# Bağımlılıkları önce önbelleğe al
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

# Yalnızca gerekli kaynaklar
COPY cmd ./cmd
COPY pkg ./pkg

ARG VERSION=dev
# Küçük, statik ikili
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w -X keywordhunter-mvp/pkg/web.Version=${VERSION}" \
    -o /out/keywordhunter ./cmd/main.go

FROM alpine:3.22

WORKDIR /app

# wget: compose healthcheck. chromium + fontlar: Tor üzerinden .onion ekran görüntüsü
# (chromedp ile sürülür). nss/freetype/harfbuzz/ttf-freefont headless render için gerekli.
# su-exec: entrypoint root ile /data izinlerini düzeltip uygulamayı root DIŞI kullanıcıyla başlatır.
RUN apk add --no-cache \
        ca-certificates tzdata wget su-exec \
        chromium nss freetype harfbuzz ttf-freefont \
    && addgroup -S -g 10001 kh \
    && adduser -S -u 10001 -G kh -h /app -s /sbin/nologin kh

# chromedp'nin chromium'u bulması için
ENV CHROME_BIN=/usr/bin/chromium-browser
ENV SCREENSHOT_DIR=/data/screenshots

COPY --from=builder /out/keywordhunter /app/keywordhunter
COPY .env.example /app/.env.example
COPY docker-entrypoint.sh /app/docker-entrypoint.sh

RUN sed -i 's/\r$//' /app/docker-entrypoint.sh \
    && chmod +x /app/docker-entrypoint.sh \
    && mkdir -p /data/logs /data/screenshots \
    && chown -R kh:kh /data /app

EXPOSE 8080

ENTRYPOINT ["/app/docker-entrypoint.sh"]
CMD ["/app/keywordhunter"]
