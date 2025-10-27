#!/bin/bash
# compare-profiles.sh - Compare two CPU profiles to measure optimization impact
#
# Usage:
#   ./scripts/profiling/compare-profiles.sh <baseline.prof> <optimized.prof>
#
# Example:
#   ./scripts/profiling/compare-profiles.sh profiles/baseline/cpu.prof profiles/optimized/cpu.prof

set -e

if [ "$#" -ne 2 ]; then
    echo "Usage: $0 <baseline.prof> <optimized.prof>"
    echo ""
    echo "Example:"
    echo "  $0 profiles/baseline/cpu.prof profiles/optimized/cpu.prof"
    exit 1
fi

BASELINE="$1"
OPTIMIZED="$2"

# Validate files exist
if [ ! -f "$BASELINE" ]; then
    echo "Error: Baseline profile not found: $BASELINE"
    exit 1
fi

if [ ! -f "$OPTIMIZED" ]; then
    echo "Error: Optimized profile not found: $OPTIMIZED"
    exit 1
fi

echo "=== Profile Comparison ==="
echo "Baseline:  $BASELINE"
echo "Optimized: $OPTIMIZED"
echo ""

# Create comparison directory
COMPARE_DIR="profiles/comparison_$(date +%Y%m%d_%H%M%S)"
mkdir -p "$COMPARE_DIR"

echo "Generating comparison reports..."
echo ""

# Generate top functions report for baseline
echo "1. Baseline top functions..."
go tool pprof -top -nodecount=20 "$BASELINE" > "$COMPARE_DIR/baseline-top.txt"

# Generate top functions report for optimized
echo "2. Optimized top functions..."
go tool pprof -top -nodecount=20 "$OPTIMIZED" > "$COMPARE_DIR/optimized-top.txt"

# Generate diff report (optimized vs baseline)
echo "3. Generating diff report..."
go tool pprof -top -nodecount=20 -base="$BASELINE" "$OPTIMIZED" > "$COMPARE_DIR/diff-top.txt"

echo ""
echo "=== Comparison Complete ==="
echo ""
echo "Reports saved to: $COMPARE_DIR"
echo "  Baseline:  $COMPARE_DIR/baseline-top.txt"
echo "  Optimized: $COMPARE_DIR/optimized-top.txt"
echo "  Diff:      $COMPARE_DIR/diff-top.txt"
echo ""
echo "View diff report:"
echo "  cat $COMPARE_DIR/diff-top.txt"
echo ""
echo "Negative values in diff = performance improvement"
echo "Positive values in diff = performance regression"
echo ""

# Show summary of diff
echo "=== Diff Summary ==="
head -20 "$COMPARE_DIR/diff-top.txt"
echo ""

# Interactive comparison in browser
read -p "Open interactive comparison in browser? (y/N) " -n 1 -r
echo ""
if [[ $REPLY =~ ^[Yy]$ ]]; then
    echo "Opening browser on http://localhost:8082"
    echo "Compare mode: shows differences between profiles"
    go tool pprof -http=:8082 -base="$BASELINE" "$OPTIMIZED"
fi
