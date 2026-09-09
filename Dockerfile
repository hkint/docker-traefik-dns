ARG BUILDPLATFORM
ARG TARGETOS
ARG TARGETARCH

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS builder

WORKDIR /src

ENV CGO_ENABLED=0

RUN apk add --no-cache tzdata ca-certificates

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download -x

COPY . .

ARG TARGETOS
ARG TARGETARCH

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
      -a \
      -trimpath \
      -buildvcs=false \
      -ldflags="-s -w" \
      -o /out/docker-traefik-dns \
      ./cmd/docker-traefik-dns

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /out/docker-traefik-dns /docker-traefik-dns

USER 1000:1000

ENTRYPOINT ["/docker-traefik-dns"]