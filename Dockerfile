FROM golang:1.26-alpine AS builder

WORKDIR /src

# Build dependencies
RUN apk add --no-cache ca-certificates tzdata

# Cache dependencies first
COPY go.mod go.sum ./
RUN go mod download

# Copy only required source folders
COPY cmd ./cmd
COPY pkg ./pkg

# Build a small, static binary
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/keywordhunter ./cmd/main.go

FROM alpine:3.22

WORKDIR /app

# wget: compose healthcheck. chromium + fontlar: Tor üzerinden .onion ekran görüntüsü
# (chromedp ile sürülür). nss/freetype/harfbuzz/ttf-freefont headless render için gerekli.
RUN apk add --no-cache \
		ca-certificates tzdata wget \
		chromium nss freetype harfbuzz ttf-freefont

# chromedp'nin chromium'u bulması için
ENV CHROME_BIN=/usr/bin/chromium-browser
ENV SCREENSHOT_DIR=/data/screenshots

COPY --from=builder /out/keywordhunter /app/keywordhunter
COPY .env.example /app/.env.example
COPY docker-entrypoint.sh /app/docker-entrypoint.sh

RUN sed -i 's/\r$//' /app/docker-entrypoint.sh \
	&& chmod +x /app/docker-entrypoint.sh \
	&& mkdir -p /data/logs /data/screenshots

EXPOSE 8080

ENTRYPOINT ["/app/docker-entrypoint.sh"]
CMD ["/app/keywordhunter"]
