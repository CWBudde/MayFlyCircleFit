BINARY_NAME := "mayflycirclefit"
BUILD_DIR := "./bin"

# Show this help message
help:
	@just --list

# Build the binary
build:
	go build -o {{BUILD_DIR}}/{{BINARY_NAME}} ./cmd

# Build and run the application
run: build
	{{BUILD_DIR}}/{{BINARY_NAME}}

# Format Go code
fmt:
	go fmt ./...
	gofmt -s -w .

# Run tests
test:
	go test -v ./...

# Run tests with coverage
test-coverage:
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# Run linters
lint:
	go vet ./...
	@echo "Checking formatting..."
	@gofmt -s -l . || (echo "Code not formatted" && exit 1)
	@echo "All checks passed!"

# Clean build artifacts
clean:
	rm -rf {{BUILD_DIR}}
	rm -f coverage.out coverage.html
	rm -f *.prof *.pprof

# Install the binary
install:
	go install ./cmd
