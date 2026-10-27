.PHONY: help build build-darwin run test test-race vet lint fmt fmt-check tidy-check scan check install clean

help:
	@echo "Targets:"
	@echo "  help         show this list"
	@echo "  build        build bin/muxreveal"
	@echo "  build-darwin cross-compile everything for darwin"
	@echo "  run          run the app; pass arguments with ARGS=..."
	@echo "  test         run the tests"
	@echo "  test-race    run the tests with the race detector"
	@echo "  vet          go vet, with and without the bench tag"
	@echo "  lint         golangci-lint run"
	@echo "  fmt          format the code with golangci-lint fmt"
	@echo "  fmt-check    fail and show the diff if formatting is needed"
	@echo "  tidy-check   fail if go mod tidy would change go.mod or go.sum"
	@echo "  scan         fail on private-use glyphs in Go source and em-dashes in Go and Markdown files"
	@echo "  check        the full commit gate: all of the checks above, in order"
	@echo "  install      go install the binary into GOBIN, else GOPATH/bin"
	@echo "  clean        remove bin/"

build:
	go build -trimpath -o bin/muxreveal ./cmd/muxreveal

build-darwin:
	GOOS=darwin go build ./...

run:
	go run ./cmd/muxreveal $(ARGS)

test:
	go test ./...

test-race:
	CGO_ENABLED=1 go test -race ./...

vet:
	go vet ./...
	go vet -tags bench ./...

lint:
	golangci-lint run

fmt:
	golangci-lint fmt

fmt-check:
	@out=$$(golangci-lint fmt --diff 2>&1) && rc=0 || rc=$$?; \
	if [ $$rc -ne 0 ] || [ -n "$$out" ]; then \
		printf '%s\n' "$$out"; \
		echo "fmt-check: formatting needed; run 'make fmt'"; \
		exit 1; \
	fi

tidy-check:
	go mod tidy -diff

# Files come from git so a new file is caught before it is added; the list goes
# through a temp file so a failing git fails the scan. LC_ALL=C byte patterns
# because macOS grep has no -P. The canary proves grep can match before an empty
# result is trusted; /dev/null keeps grep off stdin on an empty list. Recipes
# stay correct under sh -e (BSD make).
scan:
	@export LC_ALL=C; \
	list=$$(mktemp "$${TMPDIR:-/tmp}/scan.XXXXXX") || exit 1; \
	trap 'rm -f "$$list"' EXIT; \
	glyph=$$(printf '\356[\200-\277][\200-\277]|\357[\200-\243][\200-\277]|\363[\260-\277][\200-\277][\200-\277]'); \
	dash=$$(printf '\342\200\224'); \
	printf '\357\200\200\n' | grep -qE "$$glyph" || { echo "scan: grep cannot match the glyph pattern"; exit 1; }; \
	printf '\342\200\224\n' | grep -qF "$$dash" || { echo "scan: grep cannot match an em-dash"; exit 1; }; \
	git ls-files -z --cached --others --exclude-standard -- '*.go' > "$$list" || exit 1; \
	bad=$$(xargs -0 grep -IlE "$$glyph" /dev/null < "$$list") || :; \
	if [ -n "$$bad" ]; then \
		echo "scan: private-use glyphs in:"; \
		printf '%s\n' "$$bad"; \
		fail=1; \
	fi; \
	git ls-files -z --cached --others --exclude-standard -- '*.go' '*.md' ':(exclude)docs/superpowers' > "$$list" || exit 1; \
	bad=$$(xargs -0 grep -IlF "$$dash" /dev/null < "$$list") || :; \
	if [ -n "$$bad" ]; then \
		echo "scan: em-dashes (use hyphens) in:"; \
		printf '%s\n' "$$bad"; \
		fail=1; \
	fi; \
	[ -z "$$fail" ]

check: build build-darwin fmt-check vet lint test-race tidy-check scan

install:
	go install -trimpath ./cmd/muxreveal

clean:
	rm -rf bin
