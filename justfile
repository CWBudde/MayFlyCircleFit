BINARY_NAME := "mayflycirclefit"
BUILD_DIR := "./bin"

# Show this help message
help:
	@just --list

# Build the binary
build: templ
	go build -o {{BUILD_DIR}}/{{BINARY_NAME}} .

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

# Run tests with coverage
test-coverage: templ
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# Run linters
lint: templ-check
	go vet ./...
	@echo "Checking formatting..."
	@test -z "$(gofmt -s -l .)" || (gofmt -s -l . && echo "Code not formatted" && exit 1)
	@echo "All checks passed!"

# Run the complete local/CI verification suite
check: templ-check
	go test ./...
	go vet ./...
	@echo "Checking formatting..."
	@test -z "$(gofmt -s -l .)" || (gofmt -s -l . && echo "Code not formatted" && exit 1)
	go build ./...

# Clean build artifacts
clean:
	rm -rf {{BUILD_DIR}}
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
