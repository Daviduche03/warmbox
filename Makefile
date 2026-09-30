# warmbox — self-hosted GUI desktop orchestrator (vfkit + noVNC)

WARMBOX_HOME ?= $(HOME)/.warmbox
BROWSER ?= chromium
BINARY ?= warmbox
VERSION ?= v0.1.0

.PHONY: build build-all web web-dev guest-image setup daemon run test vet clean install image-pack release-snapshot

## build: compile the warmbox binary (embeds the committed web/dist).
## CGO off keeps it a static, portable binary (and avoids SDK/toolchain drift).
build:
	CGO_ENABLED=0 go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY) ./cmd/warmbox

## web: build the dashboard SPA into web/dist
web:
	cd web && npm install --no-audit --no-fund
	cd web && npm run build

## web-dev: run the dashboard dev server (proxies /api to :7070)
web-dev:
	cd web && npm run dev

## build-all: rebuild the SPA, then the binary that embeds it
build-all: web build

## install: build, install to ~/.local/bin, and re-sign (macOS kills an arm64
## binary whose ad-hoc signature a plain copy invalidated)
install: build
	mkdir -p $(HOME)/.local/bin
	install -m755 $(BINARY) $(HOME)/.local/bin/$(BINARY)
	-codesign --force --sign - $(HOME)/.local/bin/$(BINARY)

## guest-image: build the guest rootfs and extract vmlinux + initramfs.zst
guest-image:
	./deploy/guest/build.sh

## setup: fetch noVNC and check prerequisites (requires the guest image)
setup: build
	./$(BINARY) setup

## daemon: run the orchestrator (warm pool + REST API)
daemon: build
	./$(BINARY) daemon

## run: build everything and start the daemon
run: guest-image build
	./$(BINARY) daemon

## test: run the Go test suite
test:
	go test ./...

## vet: run go vet
vet:
	go vet ./...

## clean: remove local build artifacts (not the guest image)
clean:
	rm -f $(BINARY)

## image-pack: pack the built-in image for the "images" release (also writes .sha256)
image-pack: build
	./$(BINARY) image pack --builtin -o warmbox-image-$(shell go env GOARCH).tar.zst

## release-snapshot: dry-run a release locally (builds every target, publishes nothing)
release-snapshot:
	goreleaser release --snapshot --clean
