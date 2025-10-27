# Baseline Performance Report - Phase 9 Task 9.2

**Date:** 2025-10-27
**Tool Version:** MayFlyCircleFit (commit 3650d61)
**Environment:** WSL2 (Linux 6.6.87.2)
**Compiler:** Go 1.23

## Executive Summary

This report establishes baseline performance metrics for MayFlyCircleFit before implementing optimizations in Phase 9. Profiling was conducted across three workload sizes to identify CPU and memory bottlenecks.

### Key Findings

1. **Critical Bottleneck:** `compositePixel` function dominates CPU time (54-61% depending on workload)
2. **Memory Pressure:** `image.NewNRGBA` accounts for 88-98% of all allocations
3. **Performance Scaling:** Performance degrades superlinearly with image size (643 circles/sec for 256x256 vs 9343 circles/sec for 64x64)
4. **Math Operations:** `math.Round` consumes 10-16% CPU time - candidate for integer arithmetic

### Optimization Priorities

**High Priority (>10% impact):**
1. Optimize `compositePixel` - alpha compositing inner loop
2. Implement buffer pooling to reduce `NewNRGBA` allocations
3. Replace `math.Round` with integer arithmetic

**Medium Priority (5-10% impact):**
4. Optimize circle rasterization bounds checking
5. Reduce `image.Set` overhead

## Test Methodology

### Test Images

Three synthetic test images were generated with random colored ellipses:
- **Small:** 64x64 pixels (4,096 pixels)
- **Medium:** 256x256 pixels (65,536 pixels)
- **Large:** 512x512 pixels (262,144 pixels)

### Test Parameters

| Workload | Image Size | Circles (K) | Iterations | Population | Total Evals |
|----------|------------|-------------|------------|------------|-------------|
| Small    | 64x64      | 10          | 100        | 30         | ~3,000      |
| Medium   | 256x256    | 30          | 100        | 30         | ~3,000      |
| Large    | 512x512    | 64          | 100        | 30         | ~3,000      |

All tests used **joint optimization mode** with fixed seed for reproducibility.

### Profiling Method

- CPU profiling: Go pprof with default sampling rate
- Memory profiling: Heap allocation tracking
- Command: `./scripts/profiling/profile-run.sh <image> <K> <iters> joint`

## Baseline Performance Metrics

### Small Workload (64x64, K=10)

**Runtime Metrics:**
- **Total Time:** 3.21 seconds
- **CPU Time:** 3.11 seconds (90.5% CPU utilization)
- **Circles/Second:** 9,343
- **Initial Cost:** 1311.01
- **Final Cost:** 611.45
- **Improvement:** 53.3%

**Memory Metrics:**
- **Total Allocations:** 139.29 MB
- **Peak Heap:** ~23 MB (estimated)
- **Alloc Rate:** 43.4 MB/s

### Medium Workload (256x256, K=30)

**Runtime Metrics:**
- **Total Time:** 140.02 seconds (2 min 20s)
- **CPU Time:** 134.86 seconds (96.2% CPU utilization)
- **Circles/Second:** 643
- **Initial Cost:** 1970.26
- **Final Cost:** 1566.18
- **Improvement:** 20.5%

**Memory Metrics:**
- **Total Allocations:** 1,889.20 MB (1.84 GB)
- **Peak Heap:** ~320 MB (estimated)
- **Alloc Rate:** 13.5 MB/s

### Large Workload (512x512, K=64)

**Status:** Profiling in progress (expected completion: ~10-15 minutes)

### Performance Scaling Analysis

| Metric | Small (64x64) | Medium (256x256) | Ratio |
|--------|---------------|------------------|-------|
| Pixels | 4,096 | 65,536 | 16.0x |
| Circles | 10 | 30 | 3.0x |
| Time | 3.2s | 140.0s | 43.8x |
| Circles/sec | 9,343 | 643 | 0.069x (14.5x slower) |
| Memory | 139 MB | 1,889 MB | 13.6x |

**Observations:**
- Performance scales **superlinearly** with image size (43.8x time for 16x pixels)
- This is expected due to O(pixels × circles) rendering complexity
- Memory scales linearly with optimization evaluations (~3000 image buffers allocated)

## CPU Profiling Analysis

### Top 5 Hotspots - Small Workload (64x64)

| Function | Flat Time | Flat % | Cum Time | Cum % | Analysis |
|----------|-----------|--------|----------|-------|----------|
| `compositePixel` | 1.68s | 54.0% | 2.31s | 74.3% | **Critical:** Alpha compositing inner loop |
| `math.Round` | 0.34s | 10.9% | 0.61s | 19.6% | **High:** Circle coordinate rounding |
| `math.Float64bits` | 0.25s | 8.0% | 0.25s | 8.0% | **Medium:** Called by Round |
| `renderCircle` | 0.21s | 6.8% | 2.52s | 81.0% | **Low:** Orchestration function |
| `MSECost` | 0.20s | 6.4% | 0.20s | 6.4% | **Medium:** Cost computation |

### Top 5 Hotspots - Medium Workload (256x256)

| Function | Flat Time | Flat % | Cum Time | Cum % | Analysis |
|----------|-----------|--------|----------|-------|----------|
| `compositePixel` | 82.81s | 61.4% | 119.30s | 88.5% | **Critical:** Dominates even more at scale |
| `math.Round` | 21.47s | 15.9% | 34.10s | 25.3% | **High:** Increases with larger images |
| `math.Float64bits` | 12.08s | 9.0% | 12.20s | 9.1% | **Medium:** Same as small |
| `renderCircle` | 9.02s | 6.7% | 128.39s | 95.2% | **Low:** Orchestration overhead |
| `image.PixOffset` | 2.05s | 1.5% | 2.08s | 1.5% | **Low:** Array indexing |

### Hotspot Analysis

#### 1. `compositePixel` - CRITICAL (54-61% CPU)

**What it does:**
Porter-Duff alpha compositing for blending circles onto canvas.

**Why it's slow:**
- Called for every pixel in every circle's bounding box
- For 256x256 with 30 circles: ~30 million calls per optimization evaluation
- Floating-point arithmetic for alpha blending
- No SIMD vectorization

**Optimization opportunities (Task 9.3, 9.6):**
- Precompute circle AABBs to skip out-of-bounds pixels
- Early-exit for transparent circles (opacity < 0.001)
- Replace division with multiplication by reciprocal
- Use integer arithmetic where possible
- Consider SIMD for batch pixel processing

**Code location:** `internal/fit/renderer.go` (approx line 100-120)

#### 2. `math.Round` - HIGH (11-16% CPU)

**What it does:**
Rounds circle coordinates to integer pixel positions.

**Why it's slow:**
- Called 4 times per pixel (x, y, minX, maxX calculations)
- Uses floating-point rounding which is expensive
- Inline expansion still shows up in profile

**Optimization opportunities (Task 9.6):**
- Replace with `int(x + 0.5)` for positive numbers
- Use integer-only circle rasterization
- Precompute bounding boxes once per circle

**Code location:** `internal/fit/renderer.go` (circle rasterization loop)

#### 3. `math.Float64bits` - MEDIUM (8-9% CPU)

**What it does:**
Helper for `math.Round`, converts float to bits for manipulation.

**Why it shows up:**
- Called by `math.Round` for every rounding operation
- Intrinsic function but still has overhead

**Optimization:**
- Will be eliminated when `math.Round` is replaced

#### 4. `renderCircle` - LOW (7% CPU flat, 81-95% cumulative)

**What it does:**
Main rendering loop that calls `compositePixel` for each pixel.

**Analysis:**
- Low flat time means overhead is minimal
- High cumulative time just reflects calling expensive children
- Not a optimization target itself

#### 5. `image.Set` / `image.PixOffset` - MEDIUM (1-3% CPU)

**What it does:**
- `Set`: Sets pixel value via color model conversion
- `PixOffset`: Computes array index for pixel

**Optimization opportunities (Task 9.6):**
- Direct pixel buffer manipulation instead of `Set`
- Inline `PixOffset` calculation
- Batch pixel updates

## Memory Profiling Analysis

### Top 5 Memory Allocators - Small Workload

| Function | Alloc Space | % | Analysis |
|----------|-------------|---|----------|
| `image.NewNRGBA` | 122.90 MB | 88.2% | **Critical:** Creates image buffer per render |
| `mayfly.newMayfly` | 5.50 MB | 4.0% | **Low:** Optimizer state |
| `MayflyAdapter.func1` | 5.00 MB | 3.6% | **Low:** Eval wrapper |
| `mayfly.unifrndVec` | 4.00 MB | 2.9% | **Low:** Random vectors |
| `flate.NewWriter` | 0.88 MB | 0.6% | **Low:** PNG compression |

### Top 5 Memory Allocators - Medium Workload

| Function | Alloc Space | % | Analysis |
|----------|-------------|---|----------|
| `image.NewNRGBA` | 1,846.12 MB | 97.7% | **Critical:** Even more dominant |
| `MayflyAdapter.func1` | 14.02 MB | 0.7% | **Low:** Proportional to evals |
| `mayfly.unifrndVec` | 12.52 MB | 0.7% | **Low:** Random state |
| `mayfly.newMayfly` | 11.52 MB | 0.6% | **Low:** Optimizer overhead |
| `runtime.main` | 0.50 MB | 0.03% | **Low:** Runtime overhead |

### Memory Analysis

#### `image.NewNRGBA` - CRITICAL (88-98% of allocations)

**Problem:**
- Every `Render()` and `Cost()` call allocates a new 4× width × height byte array
- For 256x256 image: 262,144 bytes per allocation
- With ~3000 evaluations: 1.8 GB total allocated
- Causes GC pressure and allocation overhead

**Why this happens:**
```go
func (r *CPURenderer) Render(params []float64) *image.NRGBA {
    canvas := image.NewNRGBA(r.bounds)  // ← New allocation every call
    // ... render circles ...
    return canvas
}
```

**Optimization (Task 9.4):**
1. **Buffer Pooling:** Reuse image buffers via sync.Pool
2. **Single Buffer:** Renderer keeps one buffer, clears between renders
3. **In-Place Reset:** `copy()` white background instead of allocating

**Expected Impact:**
- Reduce allocations by ~97% (only optimizer state remains)
- Eliminate GC pressure during optimization
- Potential 10-20% speedup from reduced allocation overhead

## Rendering Pipeline Breakdown

Based on cumulative times in CPU profile:

### Time Distribution

| Pipeline Stage | Time (256x256) | % of Total | Notes |
|----------------|----------------|------------|-------|
| Circle Rendering | 128.39s | 95.2% | `renderCircle` cumulative |
| - Alpha Compositing | 119.30s | 88.5% | `compositePixel` within rendering |
| - Coordinate Rounding | 34.10s | 25.3% | `math.Round` (overlaps with compositing) |
| - Pixel Indexing | 3.10s | 2.3% | `image.Set` overhead |
| Cost Computation | 1.80s | 1.3% | MSE calculation |
| Optimizer Overhead | <1.0s | <1% | Mayfly algorithm |

**Observation:** 95% of time is spent in rendering, with 88% in the innermost compositing loop.

## Cost Computation Hotspots

| Function | Time | % | Analysis |
|----------|------|---|----------|
| `MSECost` | 1.80s | 1.3% | **Primary cost function** |
| `image.At` | <0.1s | <0.1% | Pixel access (inlined) |
| `color.RGBA` | <0.1s | <0.1% | Color conversion |

**Analysis:**
- Cost computation is **not a bottleneck** (<2% of runtime)
- Already efficient pixel-wise MSE calculation
- No optimization needed at this time

## Optimization Roadmap

Based on profiling data, recommended optimization order:

### Phase 9 Task Priority

| Task | Target | Expected Speedup | Difficulty |
|------|--------|------------------|------------|
| **9.3** | AABB precomputation, early exits | 15-25% | Low |
| **9.4** | Buffer pooling | 10-20% | Low |
| **9.6** | Replace `math.Round`, optimize compositing | 20-40% | Medium |
| **9.5** | Data layout (cache efficiency) | 5-10% | Medium |
| **9.7** | Multi-threading | 50-300% (multi-core) | Medium |

**Cumulative Speedup Estimate:** 2-4x single-threaded, 4-12x multi-threaded

### Task 9.3 - AABB Precomputation (Immediate Priority)

**Current Situation:**
- Every pixel checks if it's inside circle radius
- Loops over entire image even if circle is small
- No early-exit for transparent circles

**Proposed Changes:**
```go
type CircleAABB struct {
    minX, minY, maxX, maxY int
    opacity float64
}

func precomputeAABB(circle Circle, imgBounds image.Rectangle) (CircleAABB, bool) {
    // Early reject: circle fully outside image
    if circleOutsideBounds(...) {
        return CircleAABB{}, false
    }

    // Early reject: circle is fully transparent
    if circle.Opacity < 0.001 {
        return CircleAABB{}, false
    }

    // Compute tight AABB
    aabb := CircleAABB{
        minX: max(0, int(circle.X - circle.R)),
        minY: max(0, int(circle.Y - circle.R)),
        maxX: min(width, int(circle.X + circle.R + 1)),
        maxY: min(height, int(circle.Y + circle.R + 1)),
        opacity: circle.Opacity,
    }
    return aabb, true
}
```

**Expected Impact:**
- 15-25% speedup from reduced pixel iterations
- Eliminates wasted work on out-of-bounds circles
- Enables further optimizations (SIMD, tiling)

## Appendix: Raw Profile Data

### Small Workload CPU Profile (Top 15)

```
File: mayflycirclefit
Type: cpu
Duration: 3.44s, Total samples = 3.11s (90.49%)

      flat  flat%   sum%        cum   cum%
     1.68s 54.02% 54.02%      2.31s 74.28%  compositePixel
     0.34s 10.93% 64.95%      0.61s 19.61%  math.Round (inline)
     0.25s  8.04% 72.99%      0.25s  8.04%  math.Float64bits (inline)
     0.21s  6.75% 79.74%      2.52s 81.03%  renderCircle
     0.20s  6.43% 86.17%      0.20s  6.43%  MSECost
     0.14s  4.50% 90.68%      0.28s  9.00%  image.Set
     0.06s  1.93% 92.60%      0.12s  3.86%  color.modelFunc.Convert
     0.05s  1.61% 94.21%      0.05s  1.61%  color.nrgbaModel
```

### Medium Workload CPU Profile (Top 15)

```
File: mayflycirclefit
Type: cpu
Duration: 140.18s, Total samples = 134.86s (96.21%)

      flat  flat%   sum%        cum   cum%
    82.81s 61.40% 61.40%    119.30s 88.46%  compositePixel
    21.47s 15.92% 77.32%     34.10s 25.29%  math.Round (inline)
    12.08s  8.96% 86.28%     12.20s  9.05%  math.Float64bits (inline)
     9.02s  6.69% 92.97%    128.39s 95.20%  renderCircle
     2.05s  1.52% 94.49%      2.08s  1.54%  image.PixOffset (inline)
     1.78s  1.32% 95.81%      1.80s  1.33%  MSECost
     1.28s  0.95% 96.76%      3.10s  2.30%  image.Set
```

### Memory Profile (256x256)

```
Type: alloc_space
Total: 1889.20MB

      flat  flat%   sum%        cum   cum%
 1846.12MB 97.72% 97.72%  1846.12MB 97.72%  image.NewNRGBA
   14.02MB  0.74% 98.46%    14.02MB  0.74%  MayflyAdapter.func1
   12.52MB  0.66% 99.13%    12.52MB  0.66%  mayfly.unifrndVec
   11.52MB  0.61% 99.73%    11.52MB  0.61%  mayfly.newMayfly
```

## Conclusion

Baseline profiling reveals clear optimization targets:

1. **Rendering dominates** (95% of runtime) with `compositePixel` as the critical inner loop
2. **Memory allocations** are excessive (1.8 GB for medium workload) due to per-render buffer creation
3. **Performance scales poorly** with image size (14.5x slower for 4x larger images)

The profiling infrastructure (Task 9.1) has successfully identified actionable bottlenecks. Proceeding with Task 9.3 (AABB precomputation) is recommended as the first optimization step, as it:
- Has high impact potential (15-25% speedup)
- Is relatively simple to implement
- Enables further optimizations
- Can be validated with existing tests

**Next Steps:**
1. Complete profiling of 512x512 workload
2. Implement Task 9.3 (AABB optimization)
3. Re-profile to measure improvement
4. Proceed to Task 9.4 (buffer pooling)
