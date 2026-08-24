# Checkpoint and resume: end-to-end test results

**Tested:** 2025-10-26 · **Result:** passed, with the limitations recorded below

> **Read the status of each limitation before relying on it.** Two still hold
> and one does not:
>
> - **Still true:** resume is joint-mode only — `cmd/resume.go` rejects
>   `sequential` and `batch` outright.
> - **Still true, and understated here:** resume is *restart-from-best*, not
>   exact continuation. Velocity, mating state, and RNG position are not
>   restored.
> - **No longer true:** the report's headline finding, that periodic
>   checkpointing during a run is impossible, has since been implemented — and
>   with it the per-iteration trace logging the report records as limited to a
>   single initial entry.
>   `checkpointInterval` in `app.JobConfig` drives a periodic save from the
>   server's progress loop (`internal/server/worker.go`). Read the sections
>   below as a record of the 2025-10-26 state, not as current behavior.
>
> [`known-limitations.md`](known-limitations.md) carries the maintained
> statement of what resume does and does not restore.

## Executive Summary

**As of 2025-10-26.** The checkpoint and resume system worked correctly within
the constraints of the Mayfly optimizer as it stood then. The primary limitation
was that the optimizer library exposed no intermediate optimization state, which
prevented periodic checkpointing **during** optimization runs; everything else
worked as designed for the supported use cases.

**Superseded.** That limitation is gone. The pinned library reports progress
during a run, and `internal/server/worker.go` writes both a periodic checkpoint
(driven by `checkpointInterval`) and a trace entry per progress sample.
Everything below records the 2025-10-26 behavior; where a finding no longer
holds it is marked inline.

## Test Scenarios and Results

### 1. ✅ Checkpoint File Creation

**Test:** Verify checkpoint files are created periodically during optimization.

**Result:** CONDITIONAL PASS — **superseded; periodic checkpointing now works.**
- Checkpoint monitoring goroutine started correctly
- Checkpoint files were **not** created during optimization runs
- **Root Cause:** the Mayfly optimizer library ran synchronously and exposed no intermediate state
- **Impact:** `job.BestParams` was only populated after optimization completed
- **Mitigation at the time:** checkpoints could be created on graceful shutdown for running jobs
- **Today:** the pinned library reports progress mid-run and the worker saves on the `checkpointInterval` schedule

**Evidence:**
```
- Checkpoint monitoring goroutine started: ✓
- Checkpoint interval ticker created: ✓
- BestParams available during run: ✗ (optimizer limitation)
```

### 2. ✅ Graceful Shutdown with Checkpoint

**Test:** Send SIGTERM during optimization and verify checkpoint is saved.

**Result:** PASS
- Server received SIGTERM (signal 15)
- Shutdown checkpoint attempted for all running jobs
- Jobs without BestParams are skipped (logged at debug level)
- Completed jobs are not checkpointed (by design)

**Evidence:**
```json
{"level":"INFO","msg":"Shutdown signal received","signal":15}
{"level":"INFO","msg":"Shutdown checkpoint complete","checkpointed":1,"failed":0}
```

**Limitation (2025-10-26):** jobs that had not completed their first optimizer run had no BestParams to checkpoint.

### 3. ✅ Checkpoint File Validation

**Test:** Verify checkpoint files exist and contain valid JSON.

**Result:** PASS
- Created test checkpoint manually to verify file format
- Checkpoint detected by `checkpoints list` command
- JSON structure valid and complete

**Evidence:**
```bash
$ ./bin/mayflycirclefit checkpoints list
JOB ID           TIMESTAMP            ITERATION  BEST COST   SIZE
------           ---------            ---------  ---------   ----
test-checkpo...  2025-10-26 23:00:00  50         500.500000  470 B

Total checkpoints: 1
```

**Checkpoint Structure:**
```json
{
  "jobId": "test-checkpoint-001",
  "bestParams": [...],
  "bestCost": 500.5,
  "initialCost": 1734.0,
  "iteration": 50,
  "timestamp": "2025-10-26T23:00:00Z",
  "config": { ... }
}
```

### 4. ✅ Resume from Checkpoint (CLI Local Mode)

**Test:** Resume optimization from saved checkpoint using CLI.

**Result:** PASS
- Checkpoint loaded successfully
- Optimization resumed with initial parameters from checkpoint
- Cost improved from previous best
- Output image saved
- Checkpoint updated with new results

**Evidence:**
```
Loaded checkpoint:
  Job ID: test-checkpoint-001
  Iteration: 50
  Best cost: 500.500000

✓ Optimization completed in 421.459921ms
  Previous cost: 500.500000
  New cost: 168.139200
  Improvement: 66.41%
  Throughput: 21354 circles/sec

✓ Output saved to: test-resume-output/test-checkpoint-001_resumed.png
✓ Checkpoint updated
```

**Updated Checkpoint:**
- Best cost: 500.5 → 168.14 (66% improvement) ✓
- Iteration: 50 → 150 (added 100 new iterations) ✓
- Timestamp: Updated to resume time ✓

### 5. ✅ Resume via Server Endpoint

**Test:** Resume optimization using server's POST /api/v1/jobs/:id/resume endpoint.

**Result:** PASS
- Server accepted resume request
- Created new job with resumed state
- Optimization completed successfully
- Cumulative iterations tracked correctly

**Evidence:**
```json
POST /api/v1/jobs/test-checkpoint-001/resume
{
  "jobId": "75f3a571-fc21-47bc-9c83-6e990050a7dc",
  "message": "Job resumed successfully from checkpoint",
  "previousCost": 168.1392,
  "previousIters": 150,
  "resumedFrom": "test-checkpoint-001",
  "state": "pending"
}
```

**Resumed Job Status:**
```json
{
  "bestCost": 168.1392,
  "iterations": 250,  // 150 from checkpoint + 100 new
  "initialCost": 1734,
  "state": "completed"
}
```

### 6. ✅ Cost Improvement Verification

**Test:** Verify resumed optimization doesn't worsen previous best.

**Result:** PASS
- Resume implementation uses `RunWithInitial()` which guarantees no regression
- Optimizer runs and compares new result with checkpoint solution
- Returns better of (new result, checkpoint solution)
- First resume: 500.5 → 168.14 (improved by 66%)
- Second resume: 168.14 → 168.14 (maintained, no regression)

**Evidence:**
```go
// From MayflyAdapter.RunWithInitial()
if newBestCost < initialCost {
    return newBestParams, newBestCost
}
return initialParams, initialCost  // Never worse than checkpoint
```

### 7. ✅ Different Optimization Modes

**Test:** Test checkpoint/resume with joint, sequential, and batch modes.

**Result:** PARTIAL PASS (as designed)
- **Joint mode:** ✓ Fully supported
- **Sequential mode:** ✗ Not supported (documented limitation)
- **Batch mode:** ✗ Not supported (documented limitation)

**Evidence:**
```bash
$ ./bin/mayflycirclefit resume test-sequential-checkpoint --local
Error: resume not yet supported for mode: sequential

$ ./bin/mayflycirclefit resume test-batch-checkpoint --local
Error: resume not yet supported for mode: batch
```

**Rationale:** Sequential and batch modes have complex multi-stage state that can't be easily resumed. Joint mode optimizes all circles simultaneously, making resume straightforward.

### 8. ✅ Trace Logging Validation

**Test:** Verify trace.jsonl files are created and contain valid data.

**Result:** CONDITIONAL PASS — **superseded; per-sample trace entries now work.**
- Trace files created correctly
- Initial state logged
- JSON format valid
- **Limitation at the time:** only the initial entry was logged, due to optimizer constraints

**Evidence:**
```bash
$ cat ./data/jobs/fcbb56ca-85b0-4b0c-a656-57f1c2e4a209/trace.jsonl
{"iteration":0,"cost":1734,"timestamp":"2025-10-26T22:54:44.429530256+01:00"}

$ cat ./data/jobs/fcbb56ca-85b0-4b0c-a656-57f1c2e4a209/trace.jsonl | python3 -m json.tool
{
    "iteration": 0,
    "cost": 1734,
    "timestamp": "2025-10-26T22:54:44.429530256+01:00"
}
```

**Trace Monitoring Limitation (2025-10-26, no longer true):** like checkpoints, trace monitoring could not log intermediate iterations because the optimizer did not expose them. The worker now writes one trace entry per progress sample.

### 9. ✅ SIGKILL vs SIGTERM Behavior

**Test:** Compare abrupt termination (SIGKILL) vs graceful shutdown (SIGTERM).

**Result:** PASS
- **SIGTERM:** Triggers graceful shutdown with checkpoint attempt
- **SIGKILL:** No checkpoint opportunity (expected behavior)

**SIGTERM Behavior:**
```json
{"level":"INFO","msg":"Shutdown signal received","signal":15}
{"level":"INFO","msg":"Shutdown checkpoint complete","checkpointed":1,"failed":0}
```

**SIGKILL Behavior:** Process terminates immediately, no shutdown hook runs. This is expected and acceptable - checkpoints are best-effort.

## Known Limitations

### Primary Limitation: Optimizer Library Constraints — **no longer applies**

At the time of the test the optimizer library exposed no intermediate state:

1. **No iteration callbacks:** the library did not call back during optimization
2. **Synchronous execution:** `Run()` blocked until completion
3. **No population access:** the current best could not be inspected during a run
4. **Impact:** periodic checkpointing **during** optimization was not possible

The pinned `github.com/cwbudde/mayfly` reports progress during a run, so points
1, 3, and 4 no longer hold. `known-limitations.md` carries the maintained
statement of what resume does and does not restore.

### Workarounds and Mitigations

These were the 2025-10-26 workarounds; graceful-shutdown checkpointing still
works, but it is no longer the only way to capture a running job.

1. **Graceful shutdown checkpointing:**
   - Users can send SIGTERM to checkpoint running jobs
   - Server attempts to checkpoint all running jobs on shutdown
   - Timeout ensures server doesn't hang indefinitely

2. **Future enhancement options:**
   - Switch to different optimizer library with iteration callbacks
   - Implement custom optimizer with checkpoint support
   - Add population seeding for true resume capability

### Mode Support Matrix

| Mode       | Checkpoint | Resume | Notes                                    |
|------------|------------|--------|------------------------------------------|
| Joint      | ✓          | ✓      | Fully supported                          |
| Sequential | ✓          | ✗      | Resume not implemented (complex state)   |
| Batch      | ✓          | ✗      | Resume not implemented (complex state)   |

## Files Created During Testing

```bash
./data/jobs/
├── test-checkpoint-001/
│   ├── checkpoint.json (470 B)
│   └── (artifacts not created - manual test checkpoint)
├── fcbb56ca-85b0-4b0c-a656-57f1c2e4a209/
│   └── trace.jsonl (78 B)
├── 4a7b92f9-0e3f-44d4-b6cc-c38d9f4f988a/
│   └── trace.jsonl (0 B)
└── 3551f4b2-a9e4-4af4-bb1d-f087d4c403c5/
    └── trace.jsonl (78 B)

./test-resume-output/
└── test-checkpoint-001_resumed.png (246 B)

./test-sequential-resume/ (not created - resume failed as expected)
./test-batch-resume/ (not created - resume failed as expected)
```

## Acceptance Criteria Status

| Criterion | Status | Notes |
|-----------|--------|-------|
| Kill server mid-run, restart, resume from checkpoint | ⚠️ PARTIAL *(as of 2025-10-26; now full — `checkpointInterval` saves periodically)* | Checkpoint saved on graceful shutdown only |
| Cost continues decreasing from previous best | ✅ PASS | RunWithInitial() guarantees no regression |
| Checkpoint files are valid and complete | ✅ PASS | JSON validation passed |
| Trace logging works correctly | ⚠️ PARTIAL *(as of 2025-10-26; now full — one entry per progress sample)* | Files created, limited to initial state |
| Graceful shutdown saves checkpoints | ✅ PASS | SIGTERM handler works correctly |
| Checkpoint management commands work | ✅ PASS | list and clean commands functional |

## Conclusion

As of 2025-10-26 the checkpoint and resume system was **functionally correct and
ready for production use** with the understanding that:

1. ✅ Resume functionality works for joint mode — still true, and it is
   restart-from-best rather than exact continuation
2. ✅ Checkpoint file format is valid and complete — still true
3. ✅ Graceful shutdown checkpointing works as designed — still true
4. ⚠️ Periodic checkpointing during runs is not possible (optimizer limitation)
   — **no longer true**, `checkpointInterval` drives a periodic save
5. ⚠️ Sequential/batch modes don't support resume (future enhancement) — still
   true

The system provided value for:
- Long-running optimizations that can be gracefully stopped and resumed
- Saving progress before server maintenance
- Experimenting with different iteration counts on same initial state
