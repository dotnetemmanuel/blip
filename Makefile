BINARY  := blip
PKG     := github.com/dotnetemmanuel/blip
PREFIX  ?= $(HOME)/.local/bin

VERSION ?= $(shell git describe --tags --dirty 2>/dev/null || echo 0.0.0-dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X $(PKG)/internal/cli.version=$(VERSION) \
	-X $(PKG)/internal/cli.commit=$(COMMIT) \
	-X $(PKG)/internal/cli.date=$(DATE)

PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: all build install uninstall test vet fmt lint tidy golden dist clean

all: lint test build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) .

install: build
	mkdir -p $(PREFIX)
	install -m 0755 $(BINARY) $(PREFIX)/$(BINARY)
	@echo "installed $(PREFIX)/$(BINARY)"

uninstall:
	rm -f $(PREFIX)/$(BINARY)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

lint: vet
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed:"; echo "$$unformatted"; exit 1; \
	fi

tidy:
	go mod tidy

# Rewrites the pinned describe --compact and --dry-run output. Deliberate act:
# both are contracts a caller parses.
golden:
	go test ./internal/build ./internal/cli -update

dist:
	@rm -rf dist && mkdir -p dist
	@for platform in $(PLATFORMS); do \
		goos=$${platform%/*}; goarch=$${platform#*/}; \
		out=dist/$(BINARY)-$$goos-$$goarch; \
		echo "building $$out"; \
		CGO_ENABLED=0 GOOS=$$goos GOARCH=$$goarch \
			go build -trimpath -ldflags '$(LDFLAGS)' -o $$out . || exit 1; \
	done
	@cd dist && sha256sum * > SHA256SUMS
	@ls -1 dist

clean:
	rm -f $(BINARY)
	rm -rf dist
	go clean -testcache
