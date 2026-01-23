FROM golang:1.24-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache gcc musl-dev sqlite-dev

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o keywordhunter ./cmd/main.go

# Final stage
FROM alpine:latest

WORKDIR /app

# Copy the binary from the builder stage
COPY --from=builder /app/keywordhunter .
# Copy templates and static files (they are embedded in the binary in server.go, but just in case)
# Wait, looking at server.go, they are embedded: //go:embed templates/*
# So we don't need to copy them if they are in the binary.
# But logs and db should be in volumes.

EXPOSE 8080

CMD ["./keywordhunter"]
