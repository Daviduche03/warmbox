# warmbox — self-hosted GUI desktop orchestrator (vfkit + noVNC)

WARMBOX_HOME ?= $(HOME)/.warmbox
BROWSER ?= chromium
BINARY ?= warmbox

.PHONY: build guest-image setup daemon run test vet clean install

## build: compile the warmbox binary
build:
	go build -o $(BINARY) ./cmd/warmbox

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
