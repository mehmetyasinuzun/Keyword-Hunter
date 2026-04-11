FROM golang:1.25-alpine AS builder

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

# wget is used by compose healthcheck
RUN apk add --no-cache ca-certificates tzdata wget

COPY --from=builder /out/keywordhunter /app/keywordhunter
COPY .env.example /app/.env.example

RUN mkdir -p /data/logs \
	&& ln -sf /data/.env /app/.env

EXPOSE 8080

CMD ["/app/keywordhunter"]
