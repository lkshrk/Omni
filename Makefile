MODULE   := github.com/lkshrk/omni
BINARY   := omni
CMD_PATH := ./cmd/omni
DEMO_TAPE := demo/omni-demo.tape
DEMO_GIF  := docs/assets/omni-demo.gif

# Embed version from git tags; fall back to "dev" on untagged repos.
GIT_VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -X $(MODULE)/internal/cli.Version=$(GIT_VERSION)

.PHONY: build run test test-package-managers test-all test-integration-build test-integration lint clean install gen-schema demo-gif

## build: compile the binary to ./bin/omni
build:
	@mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(CMD_PATH)

## run: build and run the TUI
run:
	go run -ldflags "$(LDFLAGS)" $(CMD_PATH)

## install: install the binary to $GOPATH/bin
install:
	go install -ldflags "$(LDFLAGS)" $(CMD_PATH)

## gen-schema: regenerate spec/omni.settings.schema.json from config types
gen-schema:
	go run ./scripts/gen-schema

## demo-gif: render the README demo GIF with VHS
demo-gif:
	@mkdir -p $$(dirname "$(DEMO_GIF)")
	@rm -f "$(DEMO_GIF)"
	@if command -v rtk >/dev/null 2>&1; then \
		rtk vhs "$(DEMO_TAPE)"; \
	else \
		vhs "$(DEMO_TAPE)"; \
	fi

## test: run unit tests with race detector
test:
	go test -race ./...

## test-package-managers: run real package-manager provider tests in minimal distro containers
test-package-managers:
	@mkdir -p .tmp/pm-tests
	GOCACHE=$$(pwd)/.tmp/go-build CGO_ENABLED=0 GOOS=linux GOARCH=$$(go env GOARCH) go test -tags=pmcontainer -c ./internal/provider/apt -o .tmp/pm-tests/apt.test
	docker run --rm -e OMNI_PMCONTAINER=1 -e OMNI_PMCONTAINER_PROVIDER=apt -v "$$(pwd)/.tmp/pm-tests/apt.test:/apt.test:ro" debian:bookworm-slim /apt.test -test.v
	GOCACHE=$$(pwd)/.tmp/go-build CGO_ENABLED=0 GOOS=linux GOARCH=$$(go env GOARCH) go test -tags=pmcontainer -c ./internal/provider/apk -o .tmp/pm-tests/apk.test
	docker run --rm -e OMNI_PMCONTAINER=1 -e OMNI_PMCONTAINER_PROVIDER=apk -v "$$(pwd)/.tmp/pm-tests/apk.test:/apk.test:ro" alpine:3.20 /apk.test -test.v
	GOCACHE=$$(pwd)/.tmp/go-build CGO_ENABLED=0 GOOS=linux GOARCH=$$(go env GOARCH) go test -tags=pmcontainer -c ./internal/provider/brew -o .tmp/pm-tests/brew.test
	docker run --rm -e OMNI_PMCONTAINER=1 -e OMNI_PMCONTAINER_PROVIDER=brew -v "$$(pwd)/.tmp/pm-tests/brew.test:/brew.test:ro" homebrew/brew:latest /brew.test -test.v
	GOCACHE=$$(pwd)/.tmp/go-build CGO_ENABLED=0 GOOS=linux GOARCH=$$(go env GOARCH) go test -tags=pmcontainer -c ./internal/provider/dnf -o .tmp/pm-tests/dnf.test
	docker run --rm -e OMNI_PMCONTAINER=1 -e OMNI_PMCONTAINER_PROVIDER=dnf -v "$$(pwd)/.tmp/pm-tests/dnf.test:/dnf.test:ro" fedora:42 /dnf.test -test.v
	GOCACHE=$$(pwd)/.tmp/go-build CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -tags=pmcontainer -c ./internal/provider/pacman -o .tmp/pm-tests/pacman.test
	docker run --rm --platform linux/amd64 -e OMNI_PMCONTAINER=1 -e OMNI_PMCONTAINER_PROVIDER=pacman -v "$$(pwd)/.tmp/pm-tests/pacman.test:/pacman.test:ro" archlinux/archlinux:base /pacman.test -test.v
	GOCACHE=$$(pwd)/.tmp/go-build CGO_ENABLED=0 GOOS=linux GOARCH=$$(go env GOARCH) go test -tags=pmcontainer -c ./internal/provider/zypper -o .tmp/pm-tests/zypper.test
	docker run --rm -e OMNI_PMCONTAINER=1 -e OMNI_PMCONTAINER_PROVIDER=zypper -v "$$(pwd)/.tmp/pm-tests/zypper.test:/zypper.test:ro" opensuse/leap:15.6 /zypper.test -test.v

## test-all: run unit tests locally and integration tests in Docker
test-all: test test-integration

## test-integration-build: run the isolated integration test Docker build stage
test-integration-build:
	docker build -f Dockerfile.test --target integration-test --output=type=cacheonly .

## test-integration: run all tests inside the isolated Docker environment
test-integration: test-integration-build

## lint: run golangci-lint
lint:
	golangci-lint run ./...

## clean: remove build artifacts
clean:
	rm -rf bin/

## help: print this help message
help:
	@grep -E '^## ' Makefile | sed 's/## /  /'
