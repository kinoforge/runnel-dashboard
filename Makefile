VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LIGHT = GOMAXPROCS=1 nice -n 19

GOLANGCI_LINT = go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0

.PHONY: build test vet lint docker hash

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o bin/runnel-dashboard ./cmd/runnel-dashboard

test:
	$(LIGHT) go test -count=1 ./internal/config ./internal/auth ./internal/runnel ./internal/web

vet:
	go vet ./...

lint:
	$(LIGHT) $(GOLANGCI_LINT) run ./...

docker:
	docker build --build-arg VERSION=$(VERSION) -t runnel-dashboard:$(VERSION) .

hash:
	@go run ./cmd/runnel-dashboard hash
