# Task 9.10 CPU correctness validation

**Validated:** 2026-08-12  
**Historical baseline:** `3650d61`  
**Scope:** Phase 9 CPU rendering and cost optimizations

## Result

The optimized CPU renderer is byte-for-byte equivalent to the pre-Phase-9
renderer for the validated input matrix. Its production `FastMSECost` result is
also exactly equal to the scalar `MSECost` result for every rendered case.

Validation found and corrected one regression before Task 9.10 was closed. The
Task 9.3 opacity fast-reject skipped circles below `0.001`; those circles can
change an 8-bit channel on a non-white or custom canvas. The renderer now skips
only circles whose opacity is exactly zero. The opacity boundary remains in the
automated matrix to prevent recurrence.

## Baseline oracle and matrix

`internal/fit/renderer/renderer_correctness_test.go` contains a test-only
implementation of the renderer from revision `3650d61`. It deliberately keeps
the original bounding-box traversal, Porter-Duff arithmetic, and `math.Round`
conversion rather than sharing optimized production helpers.

Each case is rendered by the historical oracle and by the current renderer
with one worker and the default maximum worker count. The complete NRGBA pixel
buffers are compared without tolerance, repeated renders verify background
reset, and costs are compared exactly against scalar MSE.

The deterministic matrix covers:

- zero, one, several, 24, and 48 circles;
- 1×1, small, odd, rectangular, and 257×193 images;
- minimum-radius, oversized, edge-clipped, fully off-canvas, and fractional circles;
- transparent, sub-`0.001`, boundary-`0.001`, translucent, and opaque circles;
- heavily overlapping circles; and
- white and nonuniform custom canvases.

Existing focused tests additionally cover empty images, invalid parameter
lengths, independent image origins and strides, SIMD remainder widths,
repeated parallel rendering, thread counts greater than image height, and
pipeline replay consistency.

## Verification commands

Run the repository gates with:

```sh
just test
go test -race -short ./...
just lint
just build
```

The focused historical comparison can be run independently:

```sh
go test -count=1 -run '^TestCPURendererMatchesPreOptimizationBaseline$' \
  ./internal/fit/renderer
```

Task 9.9 supplies the matched profiles, allocation measurements, and
same-host before/after throughput evidence used by the remaining Phase 9
acceptance checks.

All four repository gates above passed on the validation host after the opacity
correction, including the complete race-enabled short suite.

## Preserved tradeoffs and limitations

- A `CPURenderer` reuses and returns one mutable output buffer. Callers that
  need to retain an image across another render must copy it.
- A renderer instance does not support simultaneous `Render` or `Cost` calls;
  independent instances and the renderer's internal row workers are safe.
- Multi-threading has scheduling and allocation overhead and can be slower for
  tiny workloads; `--threads 1` remains the explicit serial option.
- The objective compares RGB bytes and ignores alpha. It is not a perceptual
  color metric.
- Circle edges are not antialiased and are not expected to match vector
  rendering. The guarantee is parity with the project's historical rasterizer.
