FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS builder

WORKDIR /src

ARG TARGETOS
ARG TARGETARCH

ENV CGO_ENABLED=0 \
    GOOS=$TARGETOS \
    GOARCH=$TARGETARCH

COPY go.mod go.sum ./

RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build \
      -trimpath \
      -buildvcs=false \
      -ldflags="-s -w" \
      -o /out/docker-traefik-dns \
      ./cmd/docker-traefik-dns

FROM scratch

COPY --from=builder /out/docker-traefik-dns \
    /docker-traefik-dns

USER 1000:1000

ENTRYPOINT ["/docker-traefik-dns"]
