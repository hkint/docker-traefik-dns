BINARY_NAME = docker-traefik-dns
PKG = ./...

.PHONY: all build test fmt vet lint docker-build clean

all: build

build:
	go build -v -o bin/$(BINARY_NAME) ./cmd/docker-traefik-dns

test:
	go test ./... -v

fmt:
	gofmt -s -w .

vet:
	go vet ./...

lint:
	@golangci-lint run || echo "Install golangci-lint locally to run lint checks"

docker-build:
	docker build -t $(BINARY_NAME):local .

clean:
	rm -rf bin
