#!/bin/bash
# profile-run.sh - Run optimization with CPU and memory profiling
#
# Usage:
#   ./scripts/profiling/profile-run.sh <ref-image> <circles> <iters> [mode]
#
# Examples:
#   ./scripts/profiling/profile-run.sh assets/test.png 50 100
#   ./scripts/profiling/profile-run.sh refs/Ref.png 100 200 sequential

set -e

# Check arguments
if [ "$#" -lt 3 ]; then
    echo "Usage: $0 <ref-image> <circles> <iters> [mode]"
    echo ""
    echo "Arguments:"
    echo "  ref-image  Path to reference image"
    echo "  circles    Number of circles"
    echo "  iters      Number of iterations"
    echo "  mode       Optimization mode (joint|sequential|batch) [default: joint]"
    echo ""
    echo "Examples:"
    echo "  $0 assets/test.png 50 100"
    echo "  $0 refs/Ref.png 100 200 sequential"
    exit 1
fi

REF_IMAGE="$1"
CIRCLES="$2"
ITERS="$3"
MODE="${4:-joint}"

# Validate reference image exists
if [ ! -f "$REF_IMAGE" ]; then
    echo "Error: Reference image not found: $REF_IMAGE"
    exit 1
fi

# Create profiles directory
PROFILE_DIR="profiles/$(date +%Y%m%d_%H%M%S)_${MODE}_c${CIRCLES}_i${ITERS}"
mkdir -p "$PROFILE_DIR"

echo "=== MayFlyCircleFit Profiling Run ==="
echo "Reference: $REF_IMAGE"
echo "Mode: $MODE"
echo "Circles: $CIRCLES"
echo "Iterations: $ITERS"
echo "Output directory: $PROFILE_DIR"
echo ""

# Build if needed
if [ ! -f ./bin/mayflycirclefit ]; then
    echo "Building binary..."
    just build
fi

# Run optimization with profiling
echo "Running optimization with profiling..."
./bin/mayflycirclefit run \
    --ref "$REF_IMAGE" \
    --mode "$MODE" \
    --circles "$CIRCLES" \
    --iters "$ITERS" \
    --out "$PROFILE_DIR/output.png" \
    --cpuprofile "$PROFILE_DIR/cpu.prof" \
    --memprofile "$PROFILE_DIR/mem.prof"

echo ""
echo "=== Profiling Complete ==="
echo ""
echo "Profile files:"
echo "  CPU:    $PROFILE_DIR/cpu.prof"
echo "  Memory: $PROFILE_DIR/mem.prof"
echo "  Output: $PROFILE_DIR/output.png"
echo ""
echo "Analyze profiles:"
echo "  go tool pprof -top $PROFILE_DIR/cpu.prof"
echo "  go tool pprof -http=:8081 $PROFILE_DIR/cpu.prof"
echo "  go tool pprof -alloc_space $PROFILE_DIR/mem.prof"
echo ""
