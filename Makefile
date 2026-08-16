MODULE   := github.com/lkshrk/omni
BINARY   := omni
CMD_PATH := ./cmd/omni
DEMO_TAPE := demo/omni-demo.tape
DEMO_GIF  := docs/assets/omni-demo.gif
TMP_DIR     ?= $(CURDIR)/.tmp
TMP_MAX_MB  ?= 2048
DEV_DIR     ?= /private/tmp/omni-dev
DEV_HOST    ?= devhost
DEV_CONFIG  ?= $(DEV_DIR)/settings.json
DEV_CACHE   ?= $(DEV_DIR)/cache
DEV_GOCACHE ?= $(DEV_DIR)/go-build
TEST_SAFE   := bash scripts/run-test-safe.sh
TEST_UNIT_ROOT := $(TMP_DIR)/test-unit-root
TEST_PACKAGES ?= ./...
INTEGRATION_PACKAGES ?= ./integration_tests/... ./internal/provider/... ./internal/apm/... ./internal/app/...
ARGS        ?= --help
DOCKER      ?= docker

define run_pm_test
	GOCACHE="$(TMP_DIR)/go-build" CGO_ENABLED=0 GOOS=linux GOARCH=$(2) go test -tags=pmcontainer -c ./internal/provider/$(1) -o "$(TMP_DIR)/pm-tests/$(1).test"
	@set -eu; \
	container=$$($(DOCKER) create $(4) \
		-e OMNI_PMCONTAINER=1 \
		-e OMNI_PMCONTAINER_PROVIDER=$(1) \
		$(5) $(3) /$(1).test -test.v); \
	trap '$(DOCKER) rm -f "$$container" >/dev/null 2>&1 || true' EXIT HUP INT TERM; \
	$(DOCKER) cp "$(TMP_DIR)/pm-tests/$(1).test" "$$container:/$(1).test"; \
	$(DOCKER) start -a "$$container"
endef

GIT_VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GIT_COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "")
BUILD_DATE  := $(shell date -u +%Y-%m-%d)
LDFLAGS     := -X $(MODULE)/internal/buildinfo.Version=$(GIT_VERSION) \
               -X $(MODULE)/internal/buildinfo.Commit=$(GIT_COMMIT) \
               -X $(MODULE)/internal/buildinfo.Date=$(BUILD_DATE)

.PHONY: build run tui-live tui-dev cli cli-live cli-dev dev-bootstrap test test-unit test-scripts test-canary test-package-managers test-all test-integration-build test-integration docs-build lint clean clean-cache clean-docker prune-tmp install gen-schema demo-gif

## build: compile the binary to ./bin/omni
build:
	@mkdir -p bin
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(CMD_PATH)

## run: alias for tui-live
run: tui-live

## tui-live: build if needed, then run the TUI with live/default config and cache
tui-live: prune-tmp build
	./bin/$(BINARY)

## tui-dev: run the TUI with isolated dev config and cache
tui-dev: dev-bootstrap
	GOCACHE="$(DEV_GOCACHE)" OMNI_HOSTNAME="$(DEV_HOST)" go run -ldflags "$(LDFLAGS)" $(CMD_PATH) --config "$(DEV_CONFIG)" --cache-dir "$(DEV_CACHE)"

## cli: alias for cli-dev
cli: cli-dev

## cli-live: build if needed, then run the CLI with live/default config and cache; pass ARGS="..."
cli-live: prune-tmp build
	./bin/$(BINARY) $(ARGS)

## cli-dev: run the CLI with isolated dev config and cache; pass ARGS="..."
cli-dev: dev-bootstrap
	GOCACHE="$(DEV_GOCACHE)" OMNI_HOSTNAME="$(DEV_HOST)" go run -ldflags "$(LDFLAGS)" $(CMD_PATH) --config "$(DEV_CONFIG)" --cache-dir "$(DEV_CACHE)" $(ARGS)

dev-bootstrap:
	@mkdir -p "$(DEV_DIR)" "$(DEV_CACHE)" "$(DEV_GOCACHE)"
	@GOCACHE="$(DEV_GOCACHE)" OMNI_HOSTNAME="$(DEV_HOST)" go run -ldflags "$(LDFLAGS)" $(CMD_PATH) --config "$(DEV_CONFIG)" --cache-dir "$(DEV_CACHE)" hosts ensure "$(DEV_HOST)" >/dev/null

## install: install the binary to $GOPATH/bin
install:
	go install -ldflags "$(LDFLAGS)" $(CMD_PATH)

## gen-schema: regenerate versioned/current settings JSON schemas from config types
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

## test: run unit tests and script regressions
test: test-scripts test-unit

## test-unit: run unit tests with race detector
test-unit:
	$(MAKE) --no-print-directory prune-tmp
	@mkdir -p "$(TEST_UNIT_ROOT)"
	@chmod -R u+w "$(TEST_UNIT_ROOT)" 2>/dev/null || true
	@find "$(TEST_UNIT_ROOT)" -mindepth 1 -delete
	OMNI_TEST_ROOT="$(TEST_UNIT_ROOT)" $(TEST_SAFE) go test -race -trimpath $(TEST_PACKAGES)

## test-unit-fast: run unit tests without the race detector, for quick local iteration
test-unit-fast:
	$(MAKE) --no-print-directory prune-tmp
	@mkdir -p "$(TEST_UNIT_ROOT)"
	@chmod -R u+w "$(TEST_UNIT_ROOT)" 2>/dev/null || true
	@find "$(TEST_UNIT_ROOT)" -mindepth 1 -delete
	OMNI_TEST_ROOT="$(TEST_UNIT_ROOT)" $(TEST_SAFE) go test -trimpath ./...

## test-fast: run unit tests (no race detector) and script regressions, for quick local iteration
test-fast: test-scripts test-unit-fast

## test-scripts: run shell-script regression tests
test-scripts:
	$(TEST_SAFE) bash scripts/test-release.sh

## test-canary: run the opt-in upstream contract canary against live endpoints
test-canary:
	go test -tags canary -count=1 -run 'TestCanary' ./internal/agent/... ./internal/app/...

## test-package-managers: run real package-manager provider tests in minimal distro containers
test-package-managers: prune-tmp
	@mkdir -p "$(TMP_DIR)/pm-tests"
	$(call run_pm_test,apt,$$(go env GOARCH),debian:bookworm-slim)
	$(call run_pm_test,apk,$$(go env GOARCH),alpine:3.20)
	$(call run_pm_test,brew,$$(go env GOARCH),homebrew/brew:latest,,-e HOMEBREW_NO_AUTOREMOVE=1 -e HOMEBREW_NO_AUTO_UPDATE=1)
	$(call run_pm_test,dnf,$$(go env GOARCH),fedora:42)
	$(call run_pm_test,pacman,amd64,archlinux/archlinux:base,--platform linux/amd64)
	$(call run_pm_test,zypper,$$(go env GOARCH),opensuse/leap:15.6)
	$(call run_pm_test,cargo,$$(go env GOARCH),rust:1.88-slim-bookworm)

## test-all: run unit tests locally and integration tests in Docker
test-all: test test-integration

## test-integration-build: run the isolated integration test Docker build stage
test-integration-build: clean-docker
	docker buildx build -f Dockerfile.test --target integration-test --build-arg "TEST_PACKAGES=$(INTEGRATION_PACKAGES)" $(DOCKER_TEST_CACHE) --output=type=cacheonly .

## test-integration: run integration-tagged tests inside the isolated Docker environment
test-integration: test-integration-build

## docs-build: build the documentation site in a minimal Docker image
docs-build:
	$(DOCKER) build -f Dockerfile.docs --target docs-build --output=type=cacheonly .

## lint: run golangci-lint
lint: prune-tmp
	@mkdir -p "$(TMP_DIR)/go-build" "$(TMP_DIR)/golangci-lint"
	@GOCACHE=$${GOCACHE:-$(TMP_DIR)/go-build} GOLANGCI_LINT_CACHE=$${GOLANGCI_LINT_CACHE:-$(TMP_DIR)/golangci-lint} golangci-lint run

## clean: remove build artifacts and caches
clean: clean-cache clean-docker
	rm -rf bin/ .tmp/

## clean-cache: prune Go build and test caches
clean-cache:
	go clean -cache -testcache
	rm -rf "$(TMP_DIR)/go-build" "$(TMP_DIR)/go-mod" "$(TMP_DIR)/golangci-lint" "$(TMP_DIR)/pm-tests" "$(TMP_DIR)/uv-cache" "$(TMP_DIR)/docs-venv"

## prune-tmp: remove repo-local caches when .tmp exceeds TMP_MAX_MB
prune-tmp:
	@if [ -d "$(TMP_DIR)" ]; then \
		size=$$(du -sm "$(TMP_DIR)" 2>/dev/null | awk '{print $$1}'); \
		if [ "$${size:-0}" -gt "$(TMP_MAX_MB)" ]; then \
			echo "pruning $(TMP_DIR) ($${size}MB > $(TMP_MAX_MB)MB)"; \
			rm -rf "$(TMP_DIR)/go-build" "$(TMP_DIR)/go-mod" "$(TMP_DIR)/golangci-lint" "$(TMP_DIR)/pm-tests" "$(TMP_DIR)/uv-cache"; \
		fi; \
	fi

## clean-docker: prune Docker BuildKit build cache
clean-docker:
	docker builder prune --keep-storage=2g -f

## help: print this help message
help:
	@grep -E '^## ' Makefile | sed 's/## /  /'
