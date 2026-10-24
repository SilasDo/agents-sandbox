.PHONY: build build-release build-release-all bootstrap test fuzz test-race test-integration test-msb compile-all vulncheck deadcode deadcode-test mutation coverage coverage-junit lint fmt validate-fmt run check verify all clean install-bash-completion install-head install-head-and-bash-completion upgrade-deps docs-diagrams docs-linkcheck docs-linkcheck-test docs-serve

VERSION ?= dev

GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
ZIG_TARGET ?=
PLANTUML ?= plantuml
# keep in sync with versions in .agents-sandbox/Dockerfile
GO_VERSION = $(shell cat .go-version)
GOLANGCI_LINT_VERSION = 2.14.0
ZIG_VERSION ?= 0.17.0
GOTESTSUM_VERSION = 1.13.0
GOVULNCHECK_VERSION = v1.9.0
GOMUTANTS_VERSION = v0.7.1
DEADCODE_VERSION = v0.52.0
# Per-binary cap for the unit suite: a wedged test panics with a stack dump
# instead of blocking for go test's default 10m. Override for a slow machine.
TEST_TIMEOUT ?= 5m
# The SDK's microsandbox_ffi_path tag swaps its //go:embed for reading the FFI
# library from $MICROSANDBOX_FFI_PATH at runtime. The unit and integration
# suites reach the SDK only through seams, so they never read that path and need
# no env var — the ~36 MB library simply leaves the test binary. The msb-tagged
# tests keep the embed: they are the tests that load it.
NO_EMBEDDED_FFI_TAG = microsandbox_ffi_path

ARTIFACT = agents-sandbox-$(GOOS)-$(GOARCH)

RELEASE_TARGETS = \
	linux/amd64/x86_64-linux-gnu \
	linux/arm64/aarch64-linux-gnu \
	darwin/arm64/aarch64-macos.11.0-none

build:
	CGO_ENABLED=1 go build -ldflags "-X main.version=$(VERSION)" -o agents-sandbox ./cmd/agents-sandbox

build-release: export CC=zig cc -target $(ZIG_TARGET)
build-release: export CXX=zig c++ -target $(ZIG_TARGET)

build-release:
	@test -n "$(ZIG_TARGET)" || { echo "ZIG_TARGET is required (e.g. x86_64-linux-gnu)"; exit 1; }
	@echo "building $(ARTIFACT) (zig target: $(ZIG_TARGET))"
ifeq ($(GOOS),darwin)
	CGO_ENABLED=1 GOOS=$(GOOS) GOARCH=$(GOARCH) \
	    CGO_CFLAGS="-isystem $(shell dirname $(shell which zig))/lib/libc/include/any-darwin-any" \
	    CGO_LDFLAGS="-F $(shell dirname $(shell which zig))/lib/libc/darwin/System/Library/Frameworks -L $(shell dirname $(shell which zig))/lib/libc/darwin/usr/lib -Wl,-undefined,dynamic_lookup" \
	    go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=$(VERSION)" -o $(ARTIFACT) ./cmd/agents-sandbox
else
	CGO_ENABLED=1 GOOS=$(GOOS) GOARCH=$(GOARCH) \
	    go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=$(VERSION)" -o $(ARTIFACT) ./cmd/agents-sandbox
endif

build-release-all:
	@for t in $(RELEASE_TARGETS); do \
	    goos=$${t%/*/*}; rest=$${t#*/}; goarch=$${rest%/*}; zig=$${t##*/}; \
	    $(MAKE) build-release GOOS=$$goos GOARCH=$$goarch ZIG_TARGET=$$zig; \
	done


# One-shot dev environment bootstrap: requires goenv; installs the pinned Go
# version, golangci-lint, and Zig (for cross-compilation). Idempotent.
bootstrap:
	@command -v goenv >/dev/null 2>&1 || { echo "goenv is required; see CONTRIBUTING.md"; exit 1; }
	@if goenv versions 2>/dev/null | grep -qw "$(GO_VERSION)"; then \
	    echo "go $(GO_VERSION) already installed"; \
	else \
	    goenv install "$(GO_VERSION)"; \
	fi
	goenv tools install "golangci-lint@v$(GOLANGCI_LINT_VERSION)"
	@if command -v zig >/dev/null 2>&1 && [ "$$(zig version)" = "$(ZIG_VERSION)" ]; then \
	    echo "zig $(ZIG_VERSION) already installed"; \
	else \
	    ZIG_ARCH=$$(if [ "$$(uname -m)" = aarch64 ]; then echo aarch64; else echo x86_64; fi); \
	    if [ "$$(uname -s)" = "Darwin" ]; then \
	        ZIG_OS=macos; ZIG_PREFIX="$${HOME}/.local"; \
	    else \
	        ZIG_OS=linux; ZIG_PREFIX=/usr/local; \
	    fi; \
	    ZIG_TARBALL=zig-$${ZIG_ARCH}-$${ZIG_OS}-$(ZIG_VERSION).tar.xz; \
	    ZIG_DIR=zig-$${ZIG_ARCH}-$${ZIG_OS}-$(ZIG_VERSION); \
	    mkdir -p "$$ZIG_PREFIX"; \
	    curl -fsSL "https://ziglang.org/download/$(ZIG_VERSION)/$${ZIG_TARBALL}" -o /tmp/zig.tar.xz \
	 && tar -xf /tmp/zig.tar.xz -C "$$ZIG_PREFIX" \
	 && rm /tmp/zig.tar.xz \
	 && ln -sfn "$$ZIG_PREFIX/$${ZIG_DIR}" "$$ZIG_PREFIX/zig"; \
	fi

test:
	CGO_ENABLED=1 go test -tags $(NO_EMBEDDED_FFI_TAG) -timeout $(TEST_TIMEOUT) ./...

# Race-detector run across the module. The launcher runs concurrent state
# transactions, legacy-volume copy campaigns, daemon health polling, and
# watchers, so a data race is a real bug; kept out of `test`/`check` because
# it is substantially slower.
test-race:
	CGO_ENABLED=1 go test -tags $(NO_EMBEDDED_FFI_TAG) -race ./...

# Drive the fuzz targets with generated input for a bounded time per target.
# The seed corpus (and any committed crasher) already runs as part of `make
# test`, so this only explores new inputs; it needs no network and no VM.
# Targets are discovered from the test sources because `go test -fuzz` refuses
# to run against more than one package at once and accepts exactly one target
# per run, so a package's targets are enumerated and each runs on its own. A
# failing target does not stop the others. FUZZPKG=<package path> narrows the
# run to one package's targets, which is how CI splits the work.
FUZZTIME ?= 30s
FUZZPKG ?=
fuzz:
	@pkgs=$$(grep -rl '^func Fuzz' --include='*_test.go' internal cmd test | xargs -n1 dirname | sort -u); \
	if [ -n "$(FUZZPKG)" ]; then pkgs=$$(printf '%s\n' "$$pkgs" | grep -x -F "$(FUZZPKG)" || true); fi; \
	if [ -z "$$pkgs" ]; then echo "no fuzz targets found for FUZZPKG='$(FUZZPKG)'"; exit 1; fi; \
	status=0; \
	for pkg in $$pkgs; do \
		targets=$$(grep -rhoE '^func Fuzz[A-Za-z0-9_]+' --include='*_test.go' "$$pkg" | awk '{print $$2}' | sort -u); \
		for target in $$targets; do \
			echo "== fuzz $$pkg $$target"; \
			CGO_ENABLED=1 go test -run='^$$' -fuzz="^$$target$$" -fuzztime=$(FUZZTIME) "./$$pkg" || status=1; \
		done; \
	done; \
	exit $$status

# Integration tests across the module: the RenderDockerfile docker-build tests
# in internal/sandbox/image and the real Docker-daemon delegation tests in
# internal/sandbox/docker. Excluded from `test`/`check` via the `integration`
# build tag. Requires a working docker daemon and network access (base image
# pulls, agent installs, registry round-trips).
test-integration:
	CGO_ENABLED=1 go test -tags 'integration $(NO_EMBEDDED_FFI_TAG)' -timeout 60m -count=1 ./...

# Harness smoke tests plus the real-client msb tests: start every registered
# harness to its TUI in a real microsandbox VM, and drive the real SDK client
# against the host runtime. Host-only (needs /dev/kvm, a docker daemon, and
# network); excluded from test/check via the `msb` build tag. These keep the
# embedded native library: they are the tests that actually load it.
test-msb:
	CGO_ENABLED=1 go test -tags msb -timeout 60m -count=1 ./test/harness/... ./internal/sandbox/msb/...

# Compile every test binary including build-tagged files (`//go:build
# integration`, `//go:build msb`) so a regression that only breaks tagged code
# is caught without a docker daemon or VM. `-run=^$` matches no test.
compile-all:
	CGO_ENABLED=1 go test -tags 'integration msb' -run='^$$' ./...

# Scan the module for reachable vulnerabilities against the Go vulnerability
# database. Needs network access on first run (and to refresh the DB); exits
# non-zero when a reachable vulnerability is found.
vulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

# Fail when production code is unreachable from any main package and carries no
# reason-bearing //deadcode:ignore marker. deadcode always exits 0, so
# ci/check-deadcode.sh decides the verdict; a failed analysis is a hard error,
# never a silent pass. Analyses the host's own GOOS, once per build-tag
# configuration. See the specs/dead-code-gate contract in .operator-shared/.
#   make deadcode
#   make deadcode PKG=./cmd/...         # inspection only; stale markers not checked
#   make deadcode TAGS=integration      # one configuration instead of the matrix
deadcode:
	DEADCODE_VERSION='$(DEADCODE_VERSION)' PKG='$(PKG)' $(if $(TAGS),TAGS='$(TAGS)',) ci/check-deadcode.sh

# Test the dead-code gate script itself (fixtures only: never runs deadcode).
deadcode-test:
	ci/test-check-deadcode.sh

# Mutation testing: mutate the code and check whether the tests notice. Run on
# demand (at release time), never as a gate — a full run takes minutes per
# package, so it stays out of check/verify/CI. By default it mutates only lines
# changed since the last release tag; FULL=1 runs every mutant and PKG picks
# the package(s).
#   make mutation                        # whole module, lines changed since the last tag
#   make mutation PKG=./internal/foo     # one package, changed lines
#   make mutation FULL=1                 # whole module, all lines (slow)
PKG   ?= ./...
TAGS  ?=
SINCE ?= $(shell git describe --tags --abbrev=0)
FULL  ?=
mutation:
	@test -n "$(FULL)" || test -n "$(SINCE)" || { \
		echo "make mutation: cannot resolve a release tag; pass SINCE=<ref> or FULL=1"; exit 1; }
	CGO_ENABLED=1 go run github.com/gomutants/gomutants@$(GOMUTANTS_VERSION) \
		--detect-equivalent \
		$(if $(FULL),,--changed-since $(SINCE)) \
		$(PKG)

coverage:
	CGO_ENABLED=1 go test -tags $(NO_EMBEDDED_FFI_TAG) -timeout $(TEST_TIMEOUT) -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

# Single test run that also emits JUnit XML (junit.xml) for Codecov Test Analytics.
coverage-junit:
	CGO_ENABLED=1 go run gotest.tools/gotestsum@v$(GOTESTSUM_VERSION) --junitfile junit.xml -- -tags $(NO_EMBEDDED_FFI_TAG) -timeout $(TEST_TIMEOUT) -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

lint:
	golangci-lint run ./...

fmt:
	golangci-lint fmt ./...

validate-fmt:
	golangci-lint fmt -d ./...

run:
	go run ./cmd/agents-sandbox

check: fmt lint test compile-all docs-linkcheck docs-linkcheck-test deadcode-test

# Read-only check for CI parity: fmt-check + lint + test + race + vulncheck (does not modify files).
verify: validate-fmt lint test test-race compile-all vulncheck deadcode docs-linkcheck docs-linkcheck-test deadcode-test

all: fmt lint test docs-linkcheck docs-linkcheck-test build

clean:
	rm -f agents-sandbox
	rm -f agents-sandbox-linux-* agents-sandbox-darwin-*
	rm -f coverage.out junit.xml

install-bash-completion:
	mkdir -p ~/.local/share/bash-completion/completions
	go run ./cmd/agents-sandbox completion bash > ~/.local/share/bash-completion/completions/agents-sandbox

# Render PlantUML diagrams in docs/diagrams/ to SVG (local preview; CI re-renders on release).
# Excludes the vendored C4-PlantUML library files (C4*.puml), which are not standalone diagrams.
docs-diagrams:
	cd docs/diagrams && for f in *.puml; do case "$$f" in C4*) ;; *) $(PLANTUML) -DRELATIVE_INCLUDE -tsvg -o . "$$f" || exit 1;; esac; done

# Validate documentation links and linking style (run by `make check` and CI).
docs-linkcheck:
	ci/check-docs.sh

# Test the documentation link checker itself.
docs-linkcheck-test:
	ci/test-check-docs.sh

# Serve the docs locally the same way GitHub Pages does (Jekyll build + live reload) at http://localhost:4000/.
docs-serve:
	cd docs && bundle config set --local path vendor/bundle && bundle install && bundle exec jekyll serve --livereload

install-head: build
	mkdir -p ~/.local/bin
	cp agents-sandbox ~/.local/bin/agents-sandbox.tmp
	mv -f ~/.local/bin/agents-sandbox.tmp ~/.local/bin/agents-sandbox

install-head-and-bash-completion: install-head install-bash-completion

upgrade-deps:
	go get -u ./...
	go mod tidy
