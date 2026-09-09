# Build Stage
FROM golang:1-alpine AS builder

WORKDIR /app

COPY go.mod ./
RUN go mod download || true

COPY . .

# Compile optimized static binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/docker-traefik-dns ./cmd/docker-traefik-dns

FROM alpine:3

WORKDIR /app

RUN apk --no-cache add ca-certificates tzdata

COPY --from=builder /app/docker-traefik-dns /app/docker-traefik-dns

USER 1000:1000

ENTRYPOINT ["/app/docker-traefik-dns"]
