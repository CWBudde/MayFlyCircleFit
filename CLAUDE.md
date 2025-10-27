# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

MayFlyCircleFit is a high-performance circle fitting optimization tool that approximates images with colored circles using evolutionary algorithms (Mayfly Algorithm and Differential Evolution). It features CPU/GPU backends, live web visualization, and SIMD-accelerated evaluation.

## Build and Development Commands

```bash
# Build the binary
just build

# Build and run
just run

# Run tests
just test

# Run tests with coverage
just test-coverage

# Format code
just fmt

# Run linters (go vet + formatting check)
just lint

# Clean build artifacts
just clean

# Generate templ files
just templ

# Watch templ files and regenerate on changes
just templ-watch

# Format templ files
just templ-fmt
```

The binary is output to `./bin/mayflycirclefit`.

**Note:** Templ files (`*.templ`) are automatically compiled to Go code (`*_templ.go`) which is gitignored.

## Directory Structure

- `assets/` - Example reference images bundled with the project
- `bin/` - Compiled binaries (gitignored)
- `cmd/` - CLI entry points and command definitions
- `data/` - Runtime data (checkpoints, jobs, traces) (gitignored)
- `docs/` - Documentation
- `internal/` - Go packages (fit, opt, server, store, ui)
- `refs/` - Reference images for testing (gitignored)

## Architecture

The codebase follows a modular, interface-driven design with clear separation of concerns:

### Core Domain Model (`internal/fit/`)
- **Circle representation**: 7-parameter encoding (X, Y, R, CR, CG, CB, Opacity)
- **ParamVector**: Flat float64 slice encoding K circles for optimizer consumption
- **Bounds**: Parameter validation and clamping with configurable ranges
- **MSECost**: Mean Squared Error cost function over RGB channels

### Rendering System (`internal/fit/`)
- **Renderer interface**: Defines contract for render backends (CPU/GPU)
  - `Render(params []float64) *image.NRGBA` - Renders circles to image
  - `Cost(params []float64) float64` - Computes MSE against reference
  - `Dim() int` - Returns parameter space dimensionality
  - `Bounds() (lower, upper []float64)` - Returns parameter bounds
  - `Reference() *image.NRGBA` - Returns reference image
- **CPURenderer**: Software rendering with Porter-Duff alpha compositing
  - Bounding-box optimized circle rasterization
  - Premultiplied alpha blending

### Optimization (`internal/opt/`)
- **Optimizer interface**: Pluggable optimization algorithms
  - `Run(eval func([]float64) float64, lower, upper []float64, dim int) ([]float64, float64)`
- **Mayfly Algorithm**: Evolutionary algorithm with male/female populations
  - Males: attracted to females and global best
  - Females: attracted to global best
  - Mating with crossover and mutation
- **Configuration**: Supports deterministic runs via seed parameter

### Optimization Pipelines (`internal/fit/`)
Three strategies for adding circles:
1. **Joint**: Optimize all K circles simultaneously (planned)
2. **Sequential**: Add circles one at a time greedily (planned)
3. **Batch**: Add batchK circles per pass (planned)

### HTTP Server (`internal/server/`)
- **Job Management**: Thread-safe job queue with state machine
  - Job states: pending, running, completed, failed, cancelled
  - In-memory storage with concurrent access via RWMutex
- **Background Worker**: Async optimization execution with context cancellation
- **REST API**: Endpoints for job creation, status, and image retrieval
  - POST /api/v1/jobs - Create and start job
  - GET /api/v1/jobs - List all jobs
  - GET /api/v1/jobs/:id/status - Get job status with metrics
  - GET /api/v1/jobs/:id/best.png - Current best rendered image
  - GET /api/v1/jobs/:id/diff.png - False-color difference visualization
  - GET /api/v1/jobs/:id/ref.png - Reference image
  - GET /api/v1/jobs/:id/stream - Server-Sent Events (SSE) for live progress updates
- **UI Routes**: Web interface for job management
  - GET / - Job list page showing all optimization jobs
  - GET /jobs/:id - Job detail page with metrics and image comparison
  - GET /create - Job creation form
  - POST /create - Submit new job (validates and redirects to detail page)
- **SSE Live Updates**: Real-time progress streaming (`internal/server/stream.go`)
  - **EventBroadcaster**: Central hub for managing SSE connections
    - Thread-safe client registry using `sync.RWMutex`
    - Per-job client channels (`map[string]map[chan ProgressEvent]bool`)
    - Buffered channels (size 10) to prevent blocking
    - Last event caching for reconnecting clients
  - **Progress Events**: JSON-encoded SSE messages
    - Fields: `jobId`, `state`, `iterations`, `bestCost`, `cps`, `timestamp`
    - Broadcast every 500ms during optimization
    - Sent on all state transitions (pending→running→completed/failed)
  - **Connection Management**:
    - Subscribe: Adds client channel, sends last cached event if available
    - Unsubscribe: Removes client channel, closes channel gracefully
    - Cleanup: Closes all channels and removes cached data for completed jobs
  - **SSE Protocol**:
    - Headers: `text/event-stream`, `no-cache`, `keep-alive`
    - Event format: `data: {json}\n\n`
    - Ping messages: `: ping\n\n` every 30 seconds to keep connection alive
    - Client disconnect detection via `r.Context().Done()`
  - **Reliability Features**:
    - Automatic client reconnection with last event replay
    - Non-blocking broadcast (skips slow clients)
    - Graceful cleanup on job completion
    - Error handling with slog debug/warn/error logging
  - **Client-Side Integration**:
    - JavaScript EventSource API for SSE connection
    - Automatic reconnection on error (with 5-second fallback to polling)
    - JSON parsing of event data
    - Updates metrics, images, and sparkline in real-time
    - Closes connection when job completes/fails/cancelled
- **Middleware**: CORS and logging support

### Web UI (`internal/ui/`)

**Technology Stack:**
- **templ-based**: Type-safe HTML templating with Go
  - Templates are defined in `*.templ` files
  - Run `just templ` to generate Go code (`*_templ.go`)
  - Generated files are gitignored
- **Styling**: Minimal custom CSS without heavy frameworks
  - CSS variables for consistent theming and easy customization
  - Responsive design with mobile-first breakpoints
  - System font stack for optimal performance
  - Smooth animations and transitions

**Component Architecture:**

#### Layout Component (`layout.templ`)
Base HTML structure and global styles shared across all pages.
- **Navigation header** with app branding (SVG logo) and links
- **Responsive navigation** collapses on mobile devices
- **CSS utilities**: Card, button, badge, spinner, fade animations
- **Color system**: Primary, success, error, warning, info colors
- **Consistent spacing and typography** using CSS variables

#### Index Component (`index.templ`)
Homepage with project overview and quick start guide.
- **Welcome message** with project description
- **Quick start instructions** (3-step guide)
- **Feature highlights** in responsive grid:
  - Evolutionary algorithms (Mayfly)
  - Multiple optimization strategies
  - Live progress monitoring
- **Call-to-action buttons** to create job or view API

#### Job List Component (`list.templ`)
Displays all optimization jobs in card format.
- **Job cards** with hover effects (lift on hover)
- **Job metadata**: ID (truncated), status badge, mode, circles, iterations
- **Cost display** for completed/running jobs with improvement percentage
- **State badges** with visual indicators:
  - Pending (blue, static)
  - Running (blue, pulsing animation)
  - Completed (green)
  - Failed (red)
  - Cancelled (orange)
- **Empty state** with illustration when no jobs exist
- **Relative timestamps** (e.g., "5 minutes ago", "yesterday")
- **Clickable cards** linking to job detail pages

#### Job Detail Component (`detail.templ`)
Comprehensive single job view with real-time updates.

**Metrics Panel:**
- **Best Cost** with improvement percentage vs initial cost
- **Iterations** with progress bar showing completion percentage
- **Throughput** (circles/sec) with K/M formatting
- **Elapsed Time** in human-readable format (ms/s/m/h)
- **Live updates** via SSE for running jobs

**Cost Sparkline Visualization:**
- **Toggle button** to show/hide chart
- **SVG-based sparkline** (no external dependencies)
- **Bounded history**: Stores last 100 cost samples
- **Auto-scaling**: Y-axis adapts to data range
- **Statistics display**: Start cost, current cost, minimum cost, sample count
- **Real-time updates** as SSE events arrive
- **Smooth polyline rendering** with endpoint dot indicator

**Configuration Panel:**
- **Mode**: Joint, Sequential, or Batch
- **Circles**: Number of circles (K)
- **Population Size**: Optimizer population
- **Reference Image Path**: Full file path

**Image Display (Three-Pane Layout):**
1. **Reference Image**: Original target image
2. **Current Best**: Optimized result (auto-refreshing)
3. **Difference Visualization**: False-color heatmap showing pixel errors

**Image Loading Features:**
- **Checkered background** for transparent images
- **Loading spinners** during image fetch
- **Smart preloading**: Loads new image before swapping
- **Smooth opacity transitions** during updates
- **Error handling**: Graceful fallback if image unavailable
- **Cache busting**: Timestamp-based query parameters
- **Pending state**: Shows "Not started yet" for pending jobs

**Real-Time Updates:**
- **Automatic SSE connection** for running/pending jobs
- **Metric updates** without page reload
- **Image refreshes** triggered by SSE events
- **Cost history collection** for sparkline
- **Automatic page reload** when job completes/fails
- **Fallback to polling** after 5 seconds if SSE fails
- **Connection error handling** with user-friendly messages

**Error States:**
- **Job not found**: 404 page with link back to job list
- **Error banner**: Displays job errors prominently
- **Graceful degradation**: Manual refresh button always available

#### Job Creation Form Component (`create.templ`)
User-friendly job creation interface with validation.

**Form Fields:**
- **Reference Image Path** (text input, required)
  - Placeholder example: `assets/test.png`
  - Helper text explaining server-side path
- **Mode** (dropdown select, required)
  - Options: Joint, Sequential, Batch
  - Helper text explaining strategy
- **Circles** (number input, required)
  - Range: 1-1000
  - Default: 10
- **Iterations** (number input, required)
  - Range: 1-10000
  - Default: 100
- **Population Size** (number input, required)
  - Range: 2-200
  - Default: 30
- **Random Seed** (number input, optional)
  - Default: 0 (random)
  - Set for reproducibility

**Form Features:**
- **HTML5 validation**: Min/max constraints on inputs
- **Server-side validation**: Returns helpful error messages
- **Error display**: Red banner showing validation errors
- **Form preservation**: Values retained on validation failure
- **Tips section**: Blue info box explaining optimization modes and parameters
- **Cancel button**: Returns to job list
- **Submit button**: Creates job and redirects to detail page

**Validation Messages:**
- Missing required fields
- Out-of-range values
- Invalid reference image path
- File not found errors

**templ Development Workflow:**
1. Edit `*.templ` files in `internal/ui/`
2. Run `just templ` to generate Go code (`*_templ.go`)
3. Build and run server: `just build && ./bin/mayflycirclefit serve`
4. Visit http://localhost:8080 to view UI
5. For live development, use `just templ-watch` in a separate terminal

**File Naming Conventions:**
- `layout.templ` - Base layout and global styles
- `index.templ` - Homepage
- `list.templ` - Job list and job cards
- `detail.templ` - Job detail with metrics and images
- `create.templ` - Job creation form
- `*_templ.go` - Generated Go code (gitignored)

### Persistence & Checkpoints (`internal/store/`)

**Storage Interface Design:**
- **Store interface**: Defines contract for checkpoint persistence
  - `SaveCheckpoint(jobID, checkpoint)` - Atomically save checkpoint
  - `LoadCheckpoint(jobID)` - Retrieve checkpoint by ID
  - `ListCheckpoints()` - Get metadata for all checkpoints
  - `DeleteCheckpoint(jobID)` - Remove checkpoint and artifacts
- **Thread-safe**: All implementations must handle concurrent access
- **Atomic writes**: Use temp file + rename pattern to prevent corruption
- **Error handling**: Returns `ErrNotFound` for missing checkpoints

**Data Structures:**
- **Checkpoint**: Complete optimization state for resume
  - `JobID` - Unique job identifier
  - `BestParams` - Best circle parameters (7 × K floats)
  - `BestCost` - Cost achieved by best parameters
  - `InitialCost` - Starting cost for improvement tracking
  - `Iteration` - Current iteration count
  - `Timestamp` - When checkpoint was created
  - `Config` - Job configuration for validation on resume
- **CheckpointInfo**: Metadata-only view for efficient listing
  - Excludes large `BestParams` array
  - Used for checkpoint management UI

**Filesystem Layout:**
```
./data/
  └── jobs/
      └── <job-uuid>/
          ├── checkpoint.json    # Checkpoint struct as JSON
          ├── best.png          # Rendered best image
          ├── diff.png          # False-color difference heatmap
          └── trace.jsonl       # Optional: cost history (one JSON per line)
```

**Checkpoint JSON Format:**
```json
{
  "jobId": "550e8400-e29b-41d4-a716-446655440000",
  "bestParams": [100.5, 50.2, 25.0, 0.8, 0.2, 0.1, 0.9, ...],
  "bestCost": 0.0234,
  "initialCost": 0.5621,
  "iteration": 500,
  "timestamp": "2025-10-23T10:30:00Z",
  "config": {
    "refPath": "assets/test.png",
    "mode": "joint",
    "circles": 10,
    "iters": 1000,
    "popSize": 30,
    "seed": 42
  }
}
```

**Design Rationale:**
- **Filesystem-based**: Simple, no external dependencies, easy to inspect/debug
- **JSON format**: Human-readable, standard library support, easy to version
- **One directory per job**: Clean isolation, easy cleanup, supports multiple artifacts
- **Atomic writes**: Prevents corruption during server crashes or kills
- **Optional trace**: Trade-off between disk usage and post-hoc analysis capability

**Checkpoint Lifecycle:**
1. **Create**: Worker periodically saves checkpoint during optimization
2. **Load**: Resume command loads checkpoint to continue from saved state
3. **List**: CLI/UI displays available checkpoints with metadata
4. **Delete**: Cleanup removes checkpoint and all associated artifacts

**Concurrency Considerations:**
- Checkpoints written by background worker goroutines
- Multiple jobs may checkpoint simultaneously
- Store implementations use atomic file operations (no locks needed)
- Checkpoint reads are read-only and don't interfere with writes

### CLI (`cmd/`)
- **Cobra-based**: Structured command-line interface
- **Logging**: Structured logging via `slog` with configurable levels (debug, info, warn, error)
- **Commands**:
  - `run` - Single-shot optimization (writes output to file)
  - `serve` - Start HTTP server with graceful shutdown
  - `status` - Query server for job information
  - `resume` - Resume from checkpoint (Phase 8)

## Development Guidelines

### Testing
- All domain logic in `internal/` packages has corresponding `*_test.go` files
- Tests use table-driven patterns where appropriate
- Run single test: `go test ./internal/fit -v -run TestName`

### Code Organization
- `cmd/`: CLI entry points and command definitions
- `internal/fit/`: Core domain (circles, rendering, cost, pipelines)
- `internal/opt/`: Optimization algorithms
- `internal/server/`: HTTP server and job management
- `internal/ui/`: templ components for web UI (Phase 7)
- `internal/store/`: Persistence and checkpoints (Phase 8)
- `assets/`: Example reference images
- `docs/`: Documentation

### Parameter Encoding
Circles use 7 parameters in this order:
1. X - horizontal position [0, width]
2. Y - vertical position [0, height]
3. R - radius [1, max(width, height)]
4. CR - red channel [0, 1]
5. CG - green channel [0, 1]
6. CB - blue channel [0, 1]
7. Opacity - alpha [0, 1]

For K circles, the parameter vector has length K * 7.

### Renderer Interface Contract
When implementing new renderers:
- Render empty vector (all zeros) should produce white canvas
- Cost function must return MSE over all RGB channels
- Bounds must match the dimension returned by Dim()
- Reference image must be in NRGBA format

### Optimizer Interface Contract
When implementing new optimizers:
- Must respect provided bounds (lower/upper)
- Evaluation function is provided by caller
- Should support deterministic runs via configuration (seed)
- Returns best parameters and best cost

## Profiling and Performance Analysis

MayFlyCircleFit includes comprehensive profiling support via Go's pprof tooling for identifying performance bottlenecks and optimizing CPU/memory usage.

### CLI Profiling Flags

Both `run` and `serve` commands support CPU and memory profiling:

```bash
# CPU profiling during single-shot optimization
./bin/mayflycirclefit run --ref assets/test.png --circles 50 --iters 100 \
  --cpuprofile cpu.prof

# Memory profiling (written at end of optimization)
./bin/mayflycirclefit run --ref assets/test.png --circles 50 --iters 100 \
  --memprofile mem.prof

# Both CPU and memory profiling
./bin/mayflycirclefit run --ref assets/test.png --circles 50 --iters 100 \
  --cpuprofile cpu.prof --memprofile mem.prof

# Profiling the server (profiles until Ctrl+C shutdown)
./bin/mayflycirclefit serve --port 8080 \
  --cpuprofile server-cpu.prof --memprofile server-mem.prof
```

### HTTP Profiling Endpoints

When running the server, live profiling data is available via `/debug/pprof/` endpoints:

```bash
# Start server
./bin/mayflycirclefit serve --port 8080

# View profiling index (browser)
open http://localhost:8080/debug/pprof/

# Capture 30-second CPU profile
curl http://localhost:8080/debug/pprof/profile?seconds=30 > cpu.prof

# Capture heap profile
curl http://localhost:8080/debug/pprof/heap > heap.prof

# Capture goroutine dump
curl http://localhost:8080/debug/pprof/goroutine > goroutines.txt

# Capture 5-second execution trace
curl http://localhost:8080/debug/pprof/trace?seconds=5 > trace.out
```

### Analyzing Profile Data

#### CPU Profile Analysis

```bash
# Interactive analysis (top, list, web commands)
go tool pprof cpu.prof

# Generate flamegraph (requires graphviz)
go tool pprof -http=:8081 cpu.prof

# Text report of top functions by CPU time
go tool pprof -top cpu.prof

# List specific function's CPU usage
go tool pprof -list=Render cpu.prof
```

#### Memory Profile Analysis

```bash
# Interactive heap analysis
go tool pprof mem.prof

# Web-based visualization
go tool pprof -http=:8081 mem.prof

# Show memory allocations
go tool pprof -alloc_space mem.prof

# Show in-use memory
go tool pprof -inuse_space mem.prof
```

#### Execution Trace Analysis

```bash
# View trace in browser (detailed goroutine/timing info)
go tool trace trace.out
```

### Common Profiling Workflows

#### Finding CPU Hotspots

```bash
# 1. Run optimization with CPU profiling
./bin/mayflycirclefit run --ref assets/test.png --circles 100 --iters 200 \
  --cpuprofile cpu.prof

# 2. Analyze top functions
go tool pprof -top cpu.prof

# 3. Generate flamegraph for visual analysis
go tool pprof -http=:8081 cpu.prof
```

#### Identifying Memory Leaks

```bash
# 1. Start server with long-running jobs
./bin/mayflycirclefit serve --memprofile mem.prof

# 2. Create several jobs via API
# ... let jobs run ...

# 3. Shutdown server (Ctrl+C) to write memory profile

# 4. Analyze heap allocations
go tool pprof -alloc_space mem.prof
go tool pprof -inuse_space mem.prof
```

#### Profiling Specific Workloads

```bash
# Small image (64x64, 10 circles)
./bin/mayflycirclefit run --ref small.png --circles 10 --iters 100 \
  --cpuprofile small-cpu.prof --memprofile small-mem.prof

# Large image (512x512, 100 circles)
./bin/mayflycirclefit run --ref large.png --circles 100 --iters 200 \
  --cpuprofile large-cpu.prof --memprofile large-mem.prof

# Compare profiles
go tool pprof -top small-cpu.prof > small-report.txt
go tool pprof -top large-cpu.prof > large-report.txt
diff small-report.txt large-report.txt
```

### Profiling Best Practices

1. **Use representative workloads**: Profile with realistic image sizes and circle counts
2. **Run multiple iterations**: Longer runs provide more accurate profiling data
3. **Profile before optimizing**: Establish baseline performance metrics
4. **Profile after optimizing**: Verify improvements and identify new bottlenecks
5. **Focus on hot paths**: Optimize functions consuming >10% CPU time first
6. **Check memory allocations**: Look for unnecessary allocations in tight loops
7. **Compare profiles**: Use `-base` flag to compare before/after profiles:
   ```bash
   go tool pprof -base=baseline.prof optimized.prof
   ```

### Interpreting pprof Output

**CPU Profile Metrics:**
- `flat`: Time spent in function itself (excluding callees)
- `cum`: Cumulative time (including callees)
- Focus on high `flat` values in hot loops

**Memory Profile Metrics:**
- `alloc_space`: Total bytes allocated (even if later freed)
- `inuse_space`: Bytes currently in use
- High `alloc_space` indicates GC pressure

**Common Bottlenecks:**
- Circle rasterization (pixel loops)
- Alpha compositing calculations
- Cost function evaluation (MSE computation)
- Image buffer allocations

### Performance Targets

Based on profiling analysis, optimization goals:
- **Rendering**: >1000 circles/second on modern CPU
- **Memory**: <100MB heap for typical workloads (256x256, 50 circles)
- **Allocations**: Minimize allocations in rendering hot path
- **Goroutines**: Keep goroutine count proportional to GOMAXPROCS

## Project Status

Currently completed **Phase 8** (Persistence & Checkpoints) according to PLAN.md. Phases 1-8 are complete:
- Phase 1: Core domain model (Circle, ParamVector, Bounds, MSECost) - ✅ COMPLETE
- Phase 2: CPU Renderer with alpha compositing - ✅ COMPLETE
- Phase 3: Mayfly Algorithm - ✅ COMPLETE
- Phase 4: Optimization Pipelines (Joint, Sequential, Batch) - ✅ COMPLETE
- Phase 5: CLI with Cobra - ✅ COMPLETE
- Phase 6: HTTP Server + Job Management + REST API - ✅ COMPLETE
- Phase 7: Frontend with templ - ✅ COMPLETE (All tasks 7.1-7.10 complete)
  - templ infrastructure and base layout
  - Job list page with state indicators
  - Job detail page with real-time updates
  - SSE live progress streaming with EventBroadcaster
  - Auto-refreshing images with loading states
  - Optional cost sparkline visualization
  - Job creation form with validation
  - End-to-end UI testing
  - Comprehensive documentation
- Phase 8: Persistence & Checkpoints - ✅ COMPLETE (All tasks 8.1-8.11 complete)
  - Filesystem-based checkpoint storage with atomic writes
  - Checkpoint data structures with validation
  - Periodic checkpointing infrastructure (limited by optimizer library)
  - Trace logging (JSONL format)
  - Resume capability for joint mode optimization
  - CLI resume command (local and server modes)
  - Server resume endpoint
  - Graceful shutdown with checkpoint save
  - Checkpoint management utilities (list, clean)
  - End-to-end testing with comprehensive test report
  - **Known limitation:** Periodic checkpointing during optimization not possible due to Mayfly optimizer library constraints (no iteration callbacks). Checkpoints saved on graceful shutdown.

**Next Phase:** Phase 9 (Performance Profiling & Fast Paths) - See PLAN.md for detailed implementation roadmap through Phase 13.

## API Usage Examples

### Start Server
```bash
mayflycirclefit serve --port 8080
```

### Create Optimization Job
```bash
curl -X POST http://localhost:8080/api/v1/jobs \
  -H "Content-Type: application/json" \
  -d '{
    "refPath": "assets/test.png",
    "mode": "joint",
    "circles": 10,
    "iters": 100,
    "popSize": 30,
    "seed": 42
  }'
```

### Check Job Status
```bash
# Via CLI
mayflycirclefit status <job-id>

# Via API
curl http://localhost:8080/api/v1/jobs/<job-id>/status
```

### Get Best Image
```bash
curl http://localhost:8080/api/v1/jobs/<job-id>/best.png -o best.png
```

### List All Jobs
```bash
# Via CLI
mayflycirclefit status

# Via API
curl http://localhost:8080/api/v1/jobs
```

### Stream Job Progress (SSE)
```bash
# Using curl to stream SSE events
curl -N http://localhost:8080/api/v1/jobs/<job-id>/stream

# Example event output:
# data: {"jobId":"abc123...", "state":"running", "iterations":50, "bestCost":0.123, "cps":1500.5, "timestamp":"2025-10-22T10:30:00Z"}
```

## Troubleshooting

### Web UI Issues

**Problem: SSE connection fails or live updates don't work**
- Check browser console for errors
- Verify job is in "running" or "pending" state (SSE only connects for these states)
- Ensure server is accessible at the correct URL
- Check CORS settings if accessing from different origin
- SSE automatically falls back to polling after 5 seconds if connection fails
- Use manual refresh button as fallback

**Problem: Images not refreshing during optimization**
- Check browser network tab for failed image requests
- Verify job has actually started (check status endpoint)
- Images auto-refresh only when SSE events arrive
- Check that SSE connection is established (see browser console)
- Try manual refresh button to force image reload
- Check server logs for image rendering errors

**Problem: Sparkline chart not displaying**
- Click "Show Chart" toggle button (hidden by default)
- Chart requires at least 2 cost samples to render
- Verify SSE connection is working and sending cost updates
- Check browser console for JavaScript errors

**Problem: Job creation form validation errors**
- **"Image not found"**: Check that reference image path exists on server
- **"Invalid parameter"**: Verify values are within allowed ranges:
  - Circles: 1-1000
  - Iterations: 1-10000
  - Population Size: 2-200
- Check server logs for detailed validation error messages

### templ Development Issues

**Problem: Changes to .templ files not reflected in UI**
- Run `just templ` to regenerate Go code
- Rebuild the binary: `just build`
- Restart the server
- Clear browser cache or do hard refresh (Ctrl+F5)

**Problem: `templ generate` command not found**
- Install templ: `go install github.com/a-h/templ/cmd/templ@latest`
- Ensure `~/go/bin` (or `$GOPATH/bin`) is in your PATH
- Update justfile if templ is installed in different location

**Problem: Generated *_templ.go files showing errors**
- Run `just templ` to regenerate all templates
- Check for syntax errors in .templ files
- Verify Go imports are correct in templ components

### Server Issues

**Problem: Server fails to start**
- Check if port 8080 is already in use: `netstat -an | grep 8080` (Unix) or `netstat -an | findstr 8080` (Windows)
- Use `--port` flag to specify different port: `./bin/mayflycirclefit serve --port 8081`
- Check server logs for detailed error messages

**Problem: Job stuck in "pending" state**
- Check server logs for worker errors
- Verify reference image path is accessible
- Check system resources (CPU, memory)
- Try restarting the server

**Problem: High memory usage during optimization**
- Large images require more memory for rendering
- Multiple concurrent jobs consume memory proportionally
- Consider reducing image size or circles count
- Restart server to clear memory if needed

### API Issues

**Problem: POST /api/v1/jobs returns 400 Bad Request**
- Verify JSON payload is valid
- Check all required fields are present: `refPath`, `mode`, `circles`, `iters`, `popSize`
- Ensure reference image file exists on server
- Check Content-Type header is `application/json`

**Problem: GET /api/v1/jobs/:id/status returns 404**
- Verify job ID is correct (full UUID, not truncated)
- Job may have been deleted or server restarted (in-memory storage)
- Use `GET /api/v1/jobs` to list all available jobs

### Browser Compatibility

**Tested Browsers:**
- Chrome/Edge 90+ (full support)
- Firefox 88+ (full support)
- Safari 14+ (full support)

**Known Issues:**
- Older browsers may not support CSS variables or EventSource API
- Mobile browsers work but UI is optimized for desktop
- Safari may have stricter CORS policies for local development
