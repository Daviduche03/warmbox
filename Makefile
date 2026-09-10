# warmbox — self-hosted GUI desktop orchestrator (vfkit + noVNC)

WARMBOX_HOME ?= $(HOME)/.warmbox
BROWSER ?= epiphany
BINARY ?= warmbox

.PHONY: build guest-image setup daemon run test vet clean

## build: compile the warmbox binary
build:
	go build -o $(BINARY) ./cmd/warmbox

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
