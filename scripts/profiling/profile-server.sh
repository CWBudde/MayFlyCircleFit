#!/bin/bash
# profile-server.sh - Capture live profiling data from running server
#
# Usage:
#   ./scripts/profiling/profile-server.sh [port] [duration]
#
# Examples:
#   ./scripts/profiling/profile-server.sh           # port 8080, 30s CPU profile
#   ./scripts/profiling/profile-server.sh 8080 60   # 60s CPU profile

set -e

PORT="${1:-8080}"
DURATION="${2:-30}"

# Create profiles directory
PROFILE_DIR="profiles/server_$(date +%Y%m%d_%H%M%S)"
mkdir -p "$PROFILE_DIR"

echo "=== MayFlyCircleFit Server Profiling ==="
echo "Server: http://localhost:$PORT"
echo "Profile duration: ${DURATION}s"
echo "Output directory: $PROFILE_DIR"
echo ""

# Check if server is running
if ! curl -s -f "http://localhost:$PORT/debug/pprof/" > /dev/null 2>&1; then
    echo "Error: Server not running on port $PORT"
    echo "Start server with: ./bin/mayflycirclefit serve --port $PORT"
    exit 1
fi

echo "Server is running. Capturing profiles..."
echo ""

# Capture CPU profile
echo "1. Capturing CPU profile (${DURATION}s)..."
curl -s "http://localhost:$PORT/debug/pprof/profile?seconds=$DURATION" > "$PROFILE_DIR/cpu.prof"
echo "   Saved: $PROFILE_DIR/cpu.prof"

# Capture heap profile
echo "2. Capturing heap profile..."
curl -s "http://localhost:$PORT/debug/pprof/heap" > "$PROFILE_DIR/heap.prof"
echo "   Saved: $PROFILE_DIR/heap.prof"

# Capture goroutine dump
echo "3. Capturing goroutine dump..."
curl -s "http://localhost:$PORT/debug/pprof/goroutine" > "$PROFILE_DIR/goroutines.txt"
GOROUTINE_COUNT=$(grep -c "^goroutine" "$PROFILE_DIR/goroutines.txt" || echo "0")
echo "   Saved: $PROFILE_DIR/goroutines.txt ($GOROUTINE_COUNT goroutines)"

# Capture allocs profile
echo "4. Capturing allocations profile..."
curl -s "http://localhost:$PORT/debug/pprof/allocs" > "$PROFILE_DIR/allocs.prof"
echo "   Saved: $PROFILE_DIR/allocs.prof"

# Capture mutex profile
echo "5. Capturing mutex profile..."
curl -s "http://localhost:$PORT/debug/pprof/mutex" > "$PROFILE_DIR/mutex.prof"
echo "   Saved: $PROFILE_DIR/mutex.prof"

echo ""
echo "=== Profiling Complete ==="
echo ""
echo "Profile files:"
echo "  CPU:        $PROFILE_DIR/cpu.prof"
echo "  Heap:       $PROFILE_DIR/heap.prof"
echo "  Allocs:     $PROFILE_DIR/allocs.prof"
echo "  Goroutines: $PROFILE_DIR/goroutines.txt"
echo "  Mutex:      $PROFILE_DIR/mutex.prof"
echo ""
echo "Analyze profiles:"
echo "  go tool pprof -top $PROFILE_DIR/cpu.prof"
echo "  go tool pprof -http=:8081 $PROFILE_DIR/cpu.prof"
echo "  go tool pprof -alloc_space $PROFILE_DIR/heap.prof"
echo "  cat $PROFILE_DIR/goroutines.txt | head -50"
echo ""
