package server

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/store"
)

// This file holds the Task 16.6 parity check: driving a campaign through the
// schedule executor must produce the same cost sequence as driving the same
// stages by hand.
//
// The original acceptance check named the throwaway Python orchestrator as the
// reference. That orchestrator no longer exists — it lived only on a remote
// compute box whose directory was deleted on 2026-08-17 — and it was never more
// than an HTTP client that called POST /api/v1/jobs, then /extend, then
// /polish. This test is that client, in Go, in process. Comparing the executor
// against the endpoint sequence it wraps is a strictly narrower comparison than
// comparing it against a whole external program, because the endpoints are the
// only thing the two paths share and the executor is the only thing that
// differs.
//
// Determinism is a precondition, not a hope. The campaign fixes the seed, and
// parityConfig pins every knob that would otherwise let two runs of the same
// configuration disagree:
//
//   - parallelEvaluation stays off (its zero value), so population members are
//     evaluated in a fixed order and the optimizer's RNG draws land on the same
//     candidates every time.
//   - threads is 1, so a cost is one summation over the rows in one order
//     rather than a reduction over however many shards the host happens to
//     have. Floating-point addition is not associative; a different shard count
//     is a different number.
//   - fastCompositing stays off, so both paths use the exact compositor.
//
// With those pinned the comparison is exact equality. Nothing here is compared
// with a tolerance.

// parityCircles is the campaign's shape: base 8 circles, then three +8 extends,
// then one polish — the short campaign PLAN.md Task 16.6 names.
const (
	parityBaseCircles  = 8
	parityExtendWidth  = 8
	parityExtendCount  = 3
	parityIters        = 40
	parityPopSize      = 20
	parityPolishIters  = 40
	parityPolishSweeps = 1
	parityPolishStagn  = 20
	paritySeed         = 4242
	parityJobTimeout   = 5 * time.Minute
)

// parityConfig is the one configuration both paths run. It is a JSON fragment
// rather than a struct so the hand-driven job body and the schedule's base
// stanza are literally the same text: a field that appeared in one and not the
// other would make the comparison meaningless.
func parityConfig(imagePath string) string {
	return fmt.Sprintf(`"refPath": %q,
    "mode": "batch",
    "circles": %d,
    "batchSize": %d,
    "iters": %d,
    "popSize": %d,
    "threads": 1,
    "polishingMaxSweeps": %d,
    "polishingIters": %d,
    "polishingStagnationIters": %d,
    "seed": %d`,
		imagePath, parityBaseCircles, parityExtendWidth, parityIters, parityPopSize,
		parityPolishSweeps, parityPolishIters, parityPolishStagn, paritySeed)
}

func TestScheduleReproducesTheHandDrivenCampaign(t *testing.T) {
	// The campaign runs ten real optimizer stages, five per path. The budgets
	// above are sized so that costs about a second and a half, which is why this
	// is an ordinary test and not one gated behind -short: a parity check nobody
	// runs protects nothing.
	fixture := newScheduleFixture(t, 1)
	fixture.imagePath = createParityTestImage(t, filepath.Join(fixture.root, "parity.png"))

	byHand := handDrivenCampaign(t, fixture)
	bySchedule := scheduledCampaign(t, fixture)

	t.Logf("hand-driven cost sequence:  %v", byHand)
	t.Logf("scheduled cost sequence:    %v", bySchedule)

	if len(byHand) != len(bySchedule) {
		t.Fatalf("hand-driven chain ran %d stages, the schedule ran %d", len(byHand), len(bySchedule))
	}
	for index := range byHand {
		if byHand[index] != bySchedule[index] {
			t.Fatalf("stage %d cost: hand-driven %.17g, scheduled %.17g (difference %g)",
				index, byHand[index], bySchedule[index], bySchedule[index]-byHand[index])
		}
	}
	// A sequence of identical zeroes would match trivially. The costs must be
	// real, and the chain must actually improve, or the comparison proves
	// nothing about the optimizer having run at all.
	if byHand[0] <= 0 {
		t.Fatalf("base stage cost = %g, want a positive cost", byHand[0])
	}
	if byHand[len(byHand)-1] >= byHand[0] {
		t.Fatalf("cost did not improve across the campaign: %v", byHand)
	}
}

// createParityTestImage writes the campaign's reference. It is deliberately not
// createSimpleTestImage: a 50×50 white square with one red block is fitted
// almost exactly by eight circles, after which every appended circle earns
// nothing, the batch pruner retains none of them, and the run ends on
// `refill_limit` with an incomplete batch checkpoint that /extend rightly
// refuses. A campaign needs a reference with enough structure left over that
// each extend still has work to do.
//
// The content is a fixed analytic pattern rather than anything random, so the
// fixture is the same image on every host and the costs below are reproducible.
func createParityTestImage(t *testing.T, path string) string {
	t.Helper()
	const size = 64
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			img.Set(x, y, color.NRGBA{
				R: uint8(x * 4),
				G: uint8(y * 4),
				B: uint8((x ^ y) * 4),
				A: 255,
			})
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create parity image: %v", err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		t.Fatalf("encode parity image: %v", err)
	}
	return path
}

// handDrivenCampaign is the orchestrator's job, done by hand: create, extend,
// extend, extend, polish, reading the cost off each job as it settles.
func handDrivenCampaign(t *testing.T, fixture *scheduleFixture) []float64 {
	t.Helper()
	costs := make([]float64, 0, parityExtendCount+2)

	body := fmt.Sprintf("{%s}", parityConfig(fixture.imagePath))
	jobID := postJob(t, fixture, "/api/v1/jobs", body)
	costs = append(costs, awaitParityJob(t, fixture, jobID))
	requireCompleteBatchCheckpoint(t, fixture, jobID, parityBaseCircles)

	for range parityExtendCount {
		extend := fmt.Sprintf(`{"additionalCircles": %d}`, parityExtendWidth)
		jobID = postJob(t, fixture, "/api/v1/jobs/"+jobID+"/extend", extend)
		costs = append(costs, awaitParityJob(t, fixture, jobID))
	}

	jobID = postJob(t, fixture, "/api/v1/jobs/"+jobID+"/polish", `{}`)
	costs = append(costs, awaitParityJob(t, fixture, jobID))
	return costs
}

// scheduledCampaign states the same campaign once and lets the executor drive
// it, then reads the cost off the stage records.
func scheduledCampaign(t *testing.T, fixture *scheduleFixture) []float64 {
	t.Helper()
	document := fmt.Sprintf(`{
  "name": "parity campaign",
  "base": {
    %s
  },
  "steps": [
    {"type": "extend", "repeat": %d, "additionalCircles": %d},
    {"type": "polish"}
  ]
}`, parityConfig(fixture.imagePath), parityExtendCount, parityExtendWidth)

	scheduleID := fixture.createScheduleWithStages(t, document, parityExtendCount+2)
	fixture.waitForScheduleState(t, scheduleID, store.ScheduleStateCompleted, parityJobTimeout)

	stages := fixture.stages(t, scheduleID)
	if len(stages) > 0 && stages[0].JobID != "" {
		requireCompleteBatchCheckpoint(t, fixture, stages[0].JobID, parityBaseCircles)
	}
	costs := make([]float64, 0, len(stages))
	for _, stage := range stages {
		if stage.State != store.ScheduleStateCompleted {
			t.Fatalf("stage %d state = %q, want completed: %s", stage.Index, stage.State, stage.Error)
		}
		costs = append(costs, stage.BestCost)
	}
	return costs
}

// requireCompleteBatchCheckpoint states the precondition every later stage
// depends on: the stage that just completed left behind a complete batch
// checkpoint holding wantCircles circles.
//
// It is asserted directly rather than left to surface downstream because the
// two ways it can break look nothing like each other three calls later. A base
// stage whose circles were pruned — the reference is fitted so well that the
// batch pruner retains fewer than it was asked for — reaches /extend as a 400
// "extension requires a complete batch checkpoint"; a checkpoint that has not
// been written yet reaches it as a 404. Both are far easier to read here, as
// "base produced 6 of 8 circles" or "no checkpoint", than as an HTTP status on
// an unrelated request.
func requireCompleteBatchCheckpoint(t *testing.T, fixture *scheduleFixture, jobID string, wantCircles int) {
	t.Helper()
	jobStore, err := fixture.server.storeForJob(jobID)
	if err != nil {
		t.Fatalf("resolve store for job %s: %v", jobID, err)
	}
	checkpoint, err := jobStore.LoadCheckpoint(jobID)
	if err != nil {
		t.Fatalf("job %s reported completed but has no readable checkpoint: %v", jobID, err)
	}
	if got := len(checkpoint.BestParams) / 7; got != wantCircles || len(checkpoint.BestParams)%7 != 0 {
		t.Fatalf("base produced %d of %d circles (%d parameters, termination %q); "+
			"the campaign cannot continue from an incomplete batch checkpoint",
			got, wantCircles, len(checkpoint.BestParams), checkpoint.Termination)
	}
}

// postJob issues one of the three orchestrator calls and returns the identifier
// of the job it created. The extend and polish responses name the new job
// `jobId`; POST /api/v1/jobs returns the whole job, whose identifier is `id`.
func postJob(t *testing.T, fixture *scheduleFixture, path, body string) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	fixture.server.Handler().ServeHTTP(recorder,
		httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("POST %s status = %d, body %s", path, recorder.Code, recorder.Body.String())
	}
	var response struct {
		ID    string `json:"id"`
		JobID string `json:"jobId"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode POST %s response: %v", path, err)
	}
	if response.JobID != "" {
		return response.JobID
	}
	if response.ID == "" {
		t.Fatalf("POST %s named no job: %s", path, recorder.Body.String())
	}
	return response.ID
}

// awaitParityJob blocks until a hand-driven stage settles and returns its cost,
// read back over the API rather than out of the manager so both paths report a
// cost that survived serialization.
//
// `completed` is the only signal it waits for, and that is enough: the worker
// persists a job's final checkpoint before it publishes that state, so a stage
// observed completed can always be continued. It did not always do so, and the
// resulting window turned CPU contention into a 404 from the next /extend.
func awaitParityJob(t *testing.T, fixture *scheduleFixture, jobID string) float64 {
	t.Helper()
	deadline := time.Now().Add(parityJobTimeout)
	for time.Now().Before(deadline) {
		recorder := httptest.NewRecorder()
		fixture.server.Handler().ServeHTTP(recorder,
			httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET job %s status = %d, body %s", jobID, recorder.Code, recorder.Body.String())
		}
		var job struct {
			State    JobState `json:"state"`
			BestCost float64  `json:"bestCost"`
			Error    string   `json:"error"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &job); err != nil {
			t.Fatalf("decode job %s: %v", jobID, err)
		}
		switch job.State {
		case StateCompleted:
			return job.BestCost
		case StateFailed, StateCancelled:
			t.Fatalf("job %s ended %s: %s", jobID, job.State, job.Error)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s did not settle within %s", jobID, parityJobTimeout)
	return 0
}
