BINARY_NAME := "circlefit"
BUILD_DIR := "./bin"

# Show this help message
help:
	@just --list

# Build the binary
build: templ
	go build -buildvcs=false -o {{BUILD_DIR}}/{{BINARY_NAME}} .

# Build portable release archives for a semantic version (without a leading v)
release version:
	bash scripts/build-release.sh "{{version}}"

# Cross-build all supported CPU targets and verify SIMD build constraints
cross-build:
	bash scripts/check-cross-build.sh

# Build and run the application
run: build
	{{BUILD_DIR}}/{{BINARY_NAME}}

# Format Go code
fmt:
	go fmt ./...
	gofmt -s -w .

# Run tests
test: templ
	go test -v ./...

# Run the canonical CPU performance suite with statistically useful samples
benchmark:
	go test -run '^$' -bench '^BenchmarkFit$' -benchmem -count=6 ./internal/fit

# Compare two saved canonical benchmark result files
benchmark-compare baseline candidate:
	go tool benchstat "{{baseline}}" "{{candidate}}"

# Run the opt-in release lifecycle end-to-end test
test-e2e:
	CIRCLEFIT_RUN_E2E=1 go test -count=1 -timeout=3m ./tests/e2e

# Needs cgo and OpenCL headers, so it is deliberately not what `just build`
# produces; the name differs so a GPU binary is never mistaken for the portable
# one.
# Build the experimental OpenCL binary
build-gpu: templ
	CGO_ENABLED=1 go build -tags gpu -buildvcs=false -o {{BUILD_DIR}}/{{BINARY_NAME}}-gpu .

# CIRCLEFIT_REQUIRE_OPENCL turns the "no device" skip into a failure, so an
# unavailable ICD reports instead of passing vacuously.
# Run the focused OpenCL suite against a real device
test-gpu:
	CIRCLEFIT_REQUIRE_OPENCL=1 CGO_ENABLED=1 go test -tags gpu -count=1 \
		./internal/fit/renderer/... -run '^TestOpenCL|^TestPackReferenceNRGBA'

# No -count here on purpose: a -count=6 attempt produced 50-320% spreads and
# orderings that are impossible for this workload, so the method is several
# separate passes compared by hand. See docs/gpu-backends.md.
# Run one GPU benchmark pass
bench-gpu bench='^BenchmarkOpenCL':
	CIRCLEFIT_REQUIRE_OPENCL=1 CIRCLEFIT_REQUIRE_GPU_DEVICE=1 CGO_ENABLED=1 \
		go test -tags gpu -run '^$' -bench '{{bench}}' -benchmem \
		./internal/fit/renderer/...

# Run tests with coverage
test-coverage: templ
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# golangci-lint version kept in step with .github/workflows/ci-lint.yml
GOLANGCI_VERSION := "v2.13.1"

# Run golangci-lint over the whole tree
golangci:
	@command -v golangci-lint >/dev/null || (echo "golangci-lint not found; run: just golangci-install" && exit 1)
	golangci-lint run --config ./.golangci.toml --timeout 5m

# Apply every fix golangci-lint and its formatters can apply automatically
fix:
	@command -v golangci-lint >/dev/null || (echo "golangci-lint not found; run: just golangci-install" && exit 1)
	golangci-lint fmt --config ./.golangci.toml
	golangci-lint run --config ./.golangci.toml --timeout 5m --fix

# Alias for `just fix`
lint-fix: fix

# Install the pinned golangci-lint into $GOBIN
golangci-install:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@{{GOLANGCI_VERSION}}

# Run linters
lint: templ-check
	go vet ./...
	@echo "Checking formatting..."
	@test -z "$(gofmt -s -l .)" || (gofmt -s -l . && echo "Code not formatted" && exit 1)
	@echo "All checks passed!"

# Run the complete local/CI verification suite
check: templ-check bundle-check web-typecheck web-unit
	go test ./...
	go vet ./...
	@echo "Checking formatting..."
	@test -z "$(gofmt -s -l .)" || (gofmt -s -l . && echo "Code not formatted" && exit 1)
	go build -buildvcs=false ./...

# Clean build artifacts
clean:
	rm -rf {{BUILD_DIR}}
	rm -rf ./dist
	rm -f coverage.out coverage.html
	rm -f *.prof *.pprof

# Install the binary
install:
	go install .

# Generate templ files
templ:
	go tool templ generate

# Verify committed templ output is current
templ-check:
	go tool templ generate
	git diff --exit-code -- 'internal/ui/*_templ.go'
	@test -z "$(git ls-files --others --exclude-standard -- 'internal/ui/*_templ.go')" || (git ls-files --others --exclude-standard -- 'internal/ui/*_templ.go' && echo "Generated templ files are untracked" && exit 1)

# Watch templ files and regenerate on changes
templ-watch:
	go tool templ generate --watch

# Format templ files
templ-fmt:
	go tool templ fmt .

# Install the frontend dependencies esbuild bundles (npm fetches, it does not build)
web-deps:
	cd web && npm ci

# Type-check the React islands without emitting a second build artifact
web-typecheck:
	cd web && npm run typecheck

# Run the island unit tests over the formatters shared with the templ views
web-unit:
	cd web && npm run test:unit

# Exercise stream disconnect/reconciliation behavior in a real browser
web-test:
	cd web && npm run test:e2e

web-check: web-typecheck web-unit web-test

# Bundle the React islands into the committed internal/ui/static output
bundle:
	bash scripts/bundle-web.sh

# Install the browser engines the Playwright matrix drives
web-browsers:
	cd web && npx playwright install --with-deps chromium webkit

# Run only the accessibility sweep, on one engine: the tight loop while
# working violations down. The full matrix is `just web-test`.
web-a11y:
	cd web && npx playwright test --project=chromium e2e/accessibility.a11y.spec.ts

# Verify the committed island bundle is current
bundle-check:
	bash scripts/bundle-web.sh
	git diff --exit-code -- 'internal/ui/static/*'
	@test -z "$(git ls-files --others --exclude-standard -- 'internal/ui/static/*')" || (git ls-files --others --exclude-standard -- 'internal/ui/static/*' && echo "Bundled assets are untracked" && exit 1)
