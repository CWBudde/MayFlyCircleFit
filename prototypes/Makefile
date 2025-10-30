# Makefile for AVX2 SSD prototype
#
# Compiles and tests the AVX2 implementation before transpiling to Go assembly

CC = gcc
CFLAGS = -O3 -mavx2 -Wall -Wextra -std=c11
LDFLAGS = -lm

# Target binary
TARGET = ssd_avx2_test

# Source files
SRCS = ssd_avx2.c

# Build
all: $(TARGET)

$(TARGET): $(SRCS)
	@echo "Compiling AVX2 SSD prototype..."
	$(CC) $(CFLAGS) -o $(TARGET) $(SRCS) $(LDFLAGS)
	@echo "Build complete: $(TARGET)"

# Run tests
test: $(TARGET)
	@echo "Running AVX2 SSD tests..."
	./$(TARGET)

# Check for AVX2 support
check-avx2:
	@echo "Checking CPU features..."
	@if grep -q avx2 /proc/cpuinfo; then \
		echo "✓ AVX2 is supported on this CPU"; \
	else \
		echo "✗ AVX2 is NOT supported on this CPU"; \
		exit 1; \
	fi

# Clean
clean:
	rm -f $(TARGET)

.PHONY: all test check-avx2 clean
