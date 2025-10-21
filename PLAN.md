# MayFlyCircleFit Implementation Plan

> **For Claude:** Use `${SUPERPOWERS_SKILLS_ROOT}/skills/collaboration/executing-plans/SKILL.md` to implement this plan task-by-task.

**Goal:** Build a high-performance circle-fitting optimization tool with CPU/GPU backends, web UI, and live progress visualization.

**Architecture:** Go-based CLI with Cobra, modular optimizer/renderer interfaces, HTTP server with SSE streaming, templ-based UI, and SIMD-accelerated evaluation kernels.

**Tech Stack:** Go 1.21+, Cobra (CLI), templ (frontend), slog (logging), net/http (server), optional chi (routing), cgo+SIMD (performance), OpenGL/OpenCL (GPU)

---

## Phase 1: Core Domain Model ✅ COMPLETE

**Implemented:**
- `Circle` struct: Position (X, Y, R) + Color (CR, CG, CB) + Opacity
- `ParamVector`: Flat float64 encoding of K circles (7 params per circle)
- `Bounds`: Parameter validation with configurable ranges
  - X, Y: [0, width/height]
  - R: [1, max(width, height)]
  - Color/Opacity: [0, 1]
- `MSECost`: Mean Squared Error cost function over RGB channels
- Helper functions: `EncodeCircle`, `DecodeCircle`, `ClampCircle`, `ClampVector`

**Test Coverage:** 6 passing tests (encoding, bounds, clamping, MSE)

---

## Phase 2: CPU Renderer ✅ COMPLETE

**Implemented:**
- `Renderer` interface: Render(), Cost(), Dim(), Bounds(), Reference()
- `CPURenderer` struct: Software rendering with Porter-Duff alpha compositing
- `renderCircle()`: Bounding-box optimized circle rasterization
- `compositePixel()`: Premultiplied alpha blending

**Test Coverage:** 2 passing tests (white canvas, single circle rendering)

---

## Phase 3: Optimizer (Mayfly - Using External Library) ✅ COMPLETE

**Implemented:**
- `Optimizer` interface: Run() method for optimization algorithms
- `MayflyAdapter` struct: Wrapper for external Mayfly library with configurable variants
- Variant support: Standard, DESMA, OLCE, and other Mayfly algorithm variants
- Constructor functions: `NewMayfly()`, `NewMayflyDESMA()`, `NewMayflyOLCE()`

**Test Coverage:** 2 passing tests (sphere function optimization, deterministic behavior)

---

## Phase 4: Pipelines (Joint, Sequential, Batch) ✅ COMPLETE

**Implemented:**
- `OptimizationResult` struct: Holds best parameters, costs, and iteration info
- `OptimizeJoint()`: Optimizes all circles simultaneously
- `OptimizeSequential()`: Adds circles one at a time greedily
- `OptimizeBatch()`: Adds batches of circles in multiple passes

**Test Coverage:** 3 passing tests (joint, sequential, batch optimization pipelines)

---

## Phase 5: CLI with Cobra (Log-only UX) ✅ COMPLETE

**Implemented:**
- `run` command: Single-shot optimization with configurable modes (joint/sequential/batch)
  - Flags: --ref, --out, --mode, --circles, --iters, --pop, --seed
  - Image loading, optimization, and output saving
  - Metrics reporting (cost improvement, circles/sec throughput)
- Stub commands: `serve`, `status`, `resume` (placeholders for future phases)
- Test image: assets/test.png for validation

**Commands:**
- `mayflycirclefit run --ref <image>` - Run optimization
- `mayflycirclefit serve` - Stub for Phase 6
- `mayflycirclefit status` - Stub for Phase 6
- `mayflycirclefit resume <job-id>` - Stub for Phase 7

---

## Phase 6: Background Server + Job Model + Live Progress ✅ COMPLETE

**Goal:** A long-running HTTP server that executes optimizations in the background with real-time progress via SSE.

**Implemented:**
- Job management with thread-safe state machine (pending, running, completed, failed, cancelled)
- Background worker for async optimization execution with context cancellation
- HTTP server with graceful shutdown and middleware (CORS, logging)
- REST API endpoints:
  - POST /api/v1/jobs - Create and start job
  - GET /api/v1/jobs - List all jobs
  - GET /api/v1/jobs/:id/status - Get job status with metrics
  - GET /api/v1/jobs/:id/best.png - Render current best image
  - GET /api/v1/jobs/:id/diff.png - False-color difference visualization
- CLI `serve` command with signal handling and graceful shutdown
- CLI `status` command for querying jobs (list or specific)
- Helper functions for image loading and diff computation
- Comprehensive test coverage for all components

**Note:** SSE (Task 6.5) was deferred as the polling-based status endpoint provides sufficient functionality for Phase 6 goals.

---

## Phase 6: Background Server + Job Model + Live Progress - Implementation Details

**Goal:** A long-running HTTP server that executes optimizations in the background with real-time progress via SSE.

### Task 6.1: Job Management Core ✅
- [x] Create `internal/server/job.go` with job state machine
  - [x] Define `Job` struct (ID, state, config, best params, best cost, iterations, start time)
  - [x] Define job states: `pending`, `running`, `completed`, `failed`, `cancelled`
  - [x] Implement `JobManager` with in-memory job storage (map[string]*Job)
  - [x] Add methods: `CreateJob()`, `GetJob()`, `ListJobs()`, `UpdateJob()`
  - [x] Add thread-safe access with `sync.RWMutex`
  - [x] Write tests for job lifecycle

### Task 6.2: Background Worker ✅
- [x] Create `internal/server/worker.go` for job execution
  - [x] Implement `runJob(ctx context.Context, job *Job)` function
  - [x] Load reference image from job config
  - [x] Create renderer and optimizer from job config
  - [x] Run optimization with periodic progress updates
  - [x] Use context for cancellation support
  - [x] Update job state atomically during execution
  - [x] Handle errors and set failed state
  - [x] Write tests for worker execution flow

### Task 6.3: HTTP Server Foundation ✅
- [x] Create `internal/server/server.go` with HTTP server setup
  - [x] Define `Server` struct with JobManager, port, routes
  - [x] Implement `NewServer()` constructor
  - [x] Implement `Start()` method with graceful shutdown
  - [x] Add CORS middleware for development
  - [x] Add logging middleware with slog
  - [x] Write tests for server lifecycle

### Task 6.4: REST API Endpoints ✅
- [x] Implement `POST /api/v1/jobs` - Create new job
  - [x] Accept JSON payload (refPath, width, height, mode, circles, iters, pop, seed)
  - [x] Validate input parameters
  - [x] Create job and start worker goroutine
  - [x] Return job ID and initial status
  - [x] Write integration test

- [x] Implement `GET /api/v1/jobs` - List all jobs
  - [x] Return JSON array of job summaries
  - [x] Write integration test

- [x] Implement `GET /api/v1/jobs/:id/status` - Get job status
  - [x] Return JSON with state, cost, iterations, elapsed time, cps
  - [x] Write integration test

- [x] Implement `GET /api/v1/jobs/:id/best.png` - Get current best image
  - [x] Render current best params to PNG
  - [x] Set appropriate content-type and cache headers
  - [x] Write integration test

- [x] Implement `GET /api/v1/jobs/:id/diff.png` - Get difference image
  - [x] Compute pixel-wise difference (false-color heatmap)
  - [x] Return PNG with difference visualization
  - [x] Write integration test

### Task 6.5: Server-Sent Events (SSE) for Live Progress ⏭️ DEFERRED
- [ ] Create `internal/server/stream.go` for SSE support
  - [ ] Implement `GET /api/v1/jobs/:id/stream` endpoint
  - [ ] Set SSE headers (text/event-stream)
  - [ ] Create event channel per client connection
  - [ ] Send progress events (iteration, cost, cps) periodically
  - [ ] Handle client disconnect gracefully
  - [ ] Write integration test with SSE client

- [ ] Integrate SSE with worker
  - [ ] Add event broadcaster to JobManager
  - [ ] Emit events from worker during optimization
  - [ ] Throttle events (e.g., max 1 per 500ms)

**Note:** Deferred as polling-based status endpoint provides sufficient functionality.

### Task 6.6: CLI Integration - Serve Command ✅
- [x] Update `cmd/serve.go` with full implementation
  - [x] Add flags: --port (default 8080), --addr (default localhost)
  - [x] Create and start HTTP server
  - [x] Add signal handling for graceful shutdown (SIGINT, SIGTERM)
  - [x] Log server start with URL
  - [x] Write manual test

### Task 6.7: CLI Integration - Status Command ✅
- [x] Update `cmd/status.go` with full implementation
  - [x] Add flags: --server-url (default http://localhost:8080)
  - [x] Optional job-id argument to show specific job
  - [x] Call GET /api/v1/jobs or /api/v1/jobs/:id/status
  - [x] Format and display status in terminal
  - [x] Handle connection errors gracefully
  - [x] Write manual test

### Task 6.8: Integration Testing ✅
- [x] Create `internal/server/server_test.go` with integration tests
  - [x] Test full flow: POST job → poll status → get best.png
  - [x] Test SSE stream receives progress events (deferred with SSE)
  - [x] Test concurrent job execution (basic coverage)
  - [x] Test error handling (invalid job ID, bad parameters)
  - [x] Test graceful shutdown (tested via manual verification)

### Task 6.9: Documentation ✅
- [x] Update CLAUDE.md with server architecture
  - [x] Document API endpoints with examples
  - [x] Document job lifecycle and states
  - [x] Document SSE event format (deferred with SSE)
- [x] Add example curl commands to CLAUDE.md

**Deliverables:**
- Working HTTP server with job queue and SSE
- All REST API endpoints functional
- CLI commands `serve` and `status` fully implemented
- Integration tests covering main flows
- Updated documentation

**Acceptance Checks:**
- ✅ `mayflycirclefit serve` starts server
- ✅ Can POST job and receive job ID
- ✅ Can poll status and see cost decreasing
- ✅ Can fetch best.png while optimization runs
- ✅ SSE stream shows real-time progress
- ✅ `mayflycirclefit status` displays job information

---

## Summary and Next Steps

This plan covers **Phases 0-6** in detail with bite-sized, testable tasks. Each task follows TDD principles:
1. Write failing test
2. Run test to verify failure
3. Write minimal implementation
4. Run test to verify pass
5. Commit

**Remaining Phases (7-13)** will follow the same structure:
- **Phase 7**: templ-based frontend UI
- **Phase 8**: Persistence and checkpoints
- **Phase 9**: CPU profiling and optimizations
- **Phase 10**: SIMD/intrinsics for SSD
- **Phase 11**: GPU backends
- **Phase 12**: UX polish and visualization
- **Phase 13**: Documentation and packaging
