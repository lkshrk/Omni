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
TEST_PACKAGES ?= ./...
TEST_FLAGS  ?= -race -trimpath
INTEGRATION_PACKAGES ?= ./integration_tests/... ./internal/provider/... ./internal/apm/... ./internal/app/... ./internal/cli/...
INTEGRATION_IMAGE ?= omni-integration-test:local
INTEGRATION_LANE ?= full
ARGS        ?= --help
DOCKER      ?= docker
DOCKER_SAFE := bash scripts/run-docker-safe.sh "$(DOCKER)"

define run_pm_test
	@set -eu; \
	test_binary=$$(mktemp /tmp/omni-pm-test.XXXXXX); \
	trap 'rm -f "$$test_binary"' EXIT HUP INT TERM; \
	CGO_ENABLED=0 GOOS=linux GOARCH=$(2) $(TEST_SAFE) go test -tags=pmcontainer -c ./internal/provider/$(1) -o "$$test_binary"; \
	container=$$($(DOCKER_SAFE) create $(4) \
		-e OMNI_PMCONTAINER=1 \
		-e OMNI_PMCONTAINER_PROVIDER=$(1) \
		$(5) $(3) /$(1).test -test.v); \
	trap '$(DOCKER_SAFE) rm -f "$$container" >/dev/null 2>&1 || true; rm -f "$$test_binary"' EXIT HUP INT TERM; \
	$(DOCKER_SAFE) cp "$$test_binary" "$$container:/$(1).test"; \
	$(DOCKER_SAFE) start -a "$$container"
endef

GIT_VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GIT_COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "")
BUILD_DATE  := $(shell date -u +%Y-%m-%d)
LDFLAGS     := -X $(MODULE)/internal/buildinfo.Version=$(GIT_VERSION) \
               -X $(MODULE)/internal/buildinfo.Commit=$(GIT_COMMIT) \
               -X $(MODULE)/internal/buildinfo.Date=$(BUILD_DATE)

.PHONY: build run tui-live tui-dev cli cli-live cli-dev dev-bootstrap test test-unit test-scripts test-canary test-package-managers test-all test-integration-build test-integration docs-build lint clean clean-cache clean-docker prune-tmp install gen-schema check-flows gen-flows demo-gif

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

## check-flows: validate the flow catalog and generated test matrix
check-flows:
	go run ./scripts/flow-catalog

## gen-flows: regenerate catalog-backed tables in the test matrix
gen-flows:
	go run ./scripts/flow-catalog -write

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
	$(TEST_SAFE) go test $(TEST_FLAGS) $(TEST_PACKAGES)

## test-unit-fast: run unit tests without the race detector, for quick local iteration
test-unit-fast:
	$(TEST_SAFE) go test -trimpath ./...

## test-fast: run unit tests (no race detector) and script regressions, for quick local iteration
test-fast: test-scripts test-unit-fast

## test-scripts: run shell-script regression tests
test-scripts:
	$(TEST_SAFE) bash scripts/test-release.sh

## test-canary: run the opt-in upstream contract canary against live endpoints
test-canary:
	go test -tags canary -count=1 -run 'TestCanary' ./internal/agent/... ./internal/app/...

## test-package-managers: run real package-manager provider tests in minimal distro containers
test-package-managers:
	$(call run_pm_test,apt,$$(go env GOARCH),debian:bookworm-slim)
	$(call run_pm_test,apk,$$(go env GOARCH),alpine:3.20)
	$(call run_pm_test,brew,$$(go env GOARCH),homebrew/brew:latest,,-e HOMEBREW_NO_AUTOREMOVE=1 -e HOMEBREW_NO_AUTO_UPDATE=1)
	$(call run_pm_test,dnf,$$(go env GOARCH),fedora:42)
	$(call run_pm_test,pacman,amd64,archlinux/archlinux:base,--platform linux/amd64)
	$(call run_pm_test,zypper,$$(go env GOARCH),opensuse/leap:15.6)
	$(call run_pm_test,cargo,$$(go env GOARCH),rust:1.88-slim-bookworm)

## test-all: run unit tests locally and integration tests in Docker
test-all: test test-integration

## test-integration-build: build the isolated integration test environment image
test-integration-build:
	@set -eu; \
	apm_ref=$$(git ls-remote https://github.com/microsoft/apm.git refs/heads/main | awk 'NR == 1 {print $$1}'); \
	[ -n "$$apm_ref" ] || { echo "failed to resolve microsoft/apm main" >&2; exit 1; }; \
	$(DOCKER_SAFE) buildx build -f Dockerfile.test --target integration-test --build-arg "APM_REF=$$apm_ref" $(DOCKER_TEST_CACHE) --load --tag "$(INTEGRATION_IMAGE)" .

## test-integration: run integration-tagged tests inside the isolated Docker environment
test-integration: test-integration-build
	@set -eu; \
	lane="$(INTEGRATION_LANE)"; \
	case "$$lane" in ''|*[!a-z0-9-]*) echo "invalid integration lane: $$lane" >&2; exit 2 ;; esac; \
	repo_root=$$(cd "$(CURDIR)" && pwd -P); \
	tmp_root="$$repo_root/.tmp"; \
	if [ -e "$$tmp_root" ]; then [ -d "$$tmp_root" ] && [ ! -L "$$tmp_root" ] || { echo "refusing unsafe repo .tmp" >&2; exit 2; }; fi; \
	mkdir -p "$$tmp_root"; \
	safe_root=$$(cd "$$tmp_root" && pwd -P); \
	[ "$$safe_root" = "$$repo_root/.tmp" ] || { echo "repo .tmp escaped checkout" >&2; exit 2; }; \
	evidence_root="$$safe_root/test-evidence"; \
	if [ -e "$$evidence_root" ]; then [ -d "$$evidence_root" ] && [ ! -L "$$evidence_root" ] || { echo "refusing unsafe test-evidence directory" >&2; exit 2; }; fi; \
	mkdir -p "$$evidence_root"; \
	evidence_root=$$(cd "$$evidence_root" && pwd -P); \
	[ "$$evidence_root" = "$$safe_root/test-evidence" ] || { echo "test-evidence escaped repo .tmp" >&2; exit 2; }; \
	lane_lock="$$evidence_root/.docker-$$lane.lock"; \
	mkdir "$$lane_lock" 2>/dev/null || { echo "integration lane already running: $$lane" >&2; exit 2; }; \
	trap 'rmdir "$$lane_lock" >/dev/null 2>&1 || true' EXIT HUP INT TERM; \
	evidence="$$evidence_root/docker-$$lane"; \
	if [ -e "$$evidence" ]; then [ -d "$$evidence" ] && [ ! -L "$$evidence" ] || { echo "refusing unsafe lane evidence directory" >&2; exit 2; }; fi; \
	mkdir -p "$$evidence"; \
	evidence=$$(cd "$$evidence" && pwd -P); \
	[ "$$evidence" = "$$evidence_root/docker-$$lane" ] || { echo "lane evidence escaped test-evidence root" >&2; exit 2; }; \
	jsonl="$$evidence/go-test.jsonl"; \
	meta="$$evidence/meta.json"; \
	gate="$$evidence/gate.json"; \
	find "$$evidence" -maxdepth 1 -type f \( -name go-test.jsonl -o -name meta.json -o -name gate.json \) -delete; \
	image_id=$$($(DOCKER_SAFE) image inspect --format '{{.Id}}' "$(INTEGRATION_IMAGE)"); \
	container=$$($(DOCKER_SAFE) create \
		--network none \
		--env "TEST_PACKAGES=$(INTEGRATION_PACKAGES)" \
		--env "TEST_LANE=$(INTEGRATION_LANE)" \
		--env "TEST_IMAGE_REF=$(INTEGRATION_IMAGE)" \
		--env "TEST_IMAGE_ID=$$image_id" \
		--env "OMNI_TEST_APPROVED_TOOLS=apm,claude,codex,grok,cowsay" \
		"$(INTEGRATION_IMAGE)" \
		sh -c 'set +e; go_bin=$$(command -v go); binary_sha256=$$(sha256sum "$$go_bin" | cut -d " " -f 1); bash scripts/run-test-safe.sh go test -count=1 -tags=integration -race -trimpath -json $$TEST_PACKAGES > /tmp/test-results.jsonl 2>&1; status=$$?; [ "$$status" -eq 0 ] && result=pass || result=fail; printf '\''{"schema_version":1,"lane":"docker-%s","goos":"linux","tags":["integration"],"count":1}\n'\'' "$$TEST_LANE" > /tmp/test-meta.json; printf '\''{"schema_version":1,"kind":"container_gate","lane":"docker-%s","goos":"linux","image_ref":"%s","image_id":"%s","binary_sha256":"%s","command_id":"go-test-integration","exit_code":%s,"status":"%s","events":"go-test.jsonl"}\n'\'' "$$TEST_LANE" "$$TEST_IMAGE_REF" "$$TEST_IMAGE_ID" "$$binary_sha256" "$$status" "$$result" > /tmp/test-gate.json; exit "$$status"'); \
	trap '$(DOCKER_SAFE) rm -f "$$container" >/dev/null 2>&1 || true; rmdir "$$lane_lock" >/dev/null 2>&1 || true' EXIT HUP INT TERM; \
	if $(DOCKER_SAFE) start -a "$$container"; then status=0; else status=$$?; fi; \
	if ! $(DOCKER_SAFE) cp "$$container:/tmp/test-results.jsonl" "$$jsonl"; then [ "$$status" -ne 0 ] || status=1; fi; \
	if ! $(DOCKER_SAFE) cp "$$container:/tmp/test-meta.json" "$$meta"; then [ "$$status" -ne 0 ] || status=1; fi; \
	if ! $(DOCKER_SAFE) cp "$$container:/tmp/test-gate.json" "$$gate"; then [ "$$status" -ne 0 ] || status=1; fi; \
	exit "$$status"

## docs-build: build the documentation site in a minimal Docker image
docs-build:
	$(DOCKER_SAFE) build -f Dockerfile.docs --target docs-build --output=type=cacheonly .

## lint: run golangci-lint
lint:
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
