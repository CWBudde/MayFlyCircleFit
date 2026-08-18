package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/fit/renderer"
	"github.com/cwbudde/mayflycirclefit/internal/store"
)

// A dry run is `schedule create --dry-run` rather than a subcommand of its own.
// It answers the question a caller has while holding a document and about to
// submit it, and hanging it off create keeps the document argument, the local
// validation, and the flag set identical to the real thing — a separate verb
// would be a second path to keep in step for no gain.
//
// It reaches nothing. Expansion needs no runtime state, so the dry run never
// opens a socket and the server it would have posted to never learns the
// document exists: no schedule directory, no stage file, no job.
//
// Expansion is deterministic for a document that names its seed. A document
// that omits one is resolved a seed by ParseSchedule, but that seed belongs to
// this parse: the CLI posts the document as authored, so the server parses it
// again and resolves a different one. Printing the local value would name a
// seed the campaign will never run with, so an omitted seed is reported as
// automatic. Whether the document named one is therefore read from the
// document text, not from the resolved value, which is never zero.

// scheduleDocumentNamesSeed reports whether the document text pinned the
// campaign seed, at either of the two places that pin it. The document is
// re-decoded loosely rather than inspected after parsing, because parsing fills
// the seed in and the answer would then always be yes.
func scheduleDocumentNamesSeed(data []byte) bool {
	var named struct {
		Seed *int64 `json:"seed"`
		Base *struct {
			Seed *int64 `json:"seed"`
		} `json:"base"`
	}
	if err := json.Unmarshal(data, &named); err != nil {
		// An undecodable document never reaches here; treating it as unnamed
		// keeps the failure to the parse error rather than a wrong seed line.
		return false
	}
	if named.Seed != nil && *named.Seed != 0 {
		return true
	}
	return named.Base != nil && named.Base.Seed != nil && *named.Base.Seed != 0
}

// printSchedulePlan writes the realized stage list and its nominal optimizer
// iteration count. seedNamed says whether the document itself pinned the seed.
func printSchedulePlan(output io.Writer, path string, document *app.ScheduleDocument, seedNamed bool) error {
	plan, err := document.Expand()
	if err != nil {
		return fmt.Errorf("expand schedule %q: %w", path, err)
	}
	summary := app.SummarizeSchedulePlan(plan)

	fmt.Fprintf(output, "Dry run of %s — nothing was submitted and no schedule was created.\n", path)
	if document.Name != "" {
		fmt.Fprintf(output, "Name: %s\n", document.Name)
	}
	if seedNamed {
		fmt.Fprintf(output, "Seed: %d\n", document.Seed)
	} else {
		fmt.Fprintln(output, "Seed: automatic — resolved at submission, so this plan is not reproducible;")
		fmt.Fprintln(output, "      set \"seed\" in the document to pin it.")
	}
	fmt.Fprintf(output, "Stages: %d (%d base, %d extend, %d polish; %d conditional)\n\n",
		summary.Stages, summary.Base, summary.Extends, summary.Polishes, summary.Conditional)

	writer := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "#\tKIND\tCIRCLES\tITERATIONS\tPARAMETERS\tWHEN")
	for _, stage := range plan {
		fmt.Fprintf(writer, "%d\t%s\t%d\t%d\t%s\t%s\n",
			stage.Index, stage.Kind, stage.Circles, stage.PlannedIterations(),
			stagePlanParameters(stage), stagePlanCondition(stage))
	}
	if err := writer.Flush(); err != nil {
		return err
	}

	fmt.Fprintf(output, "\nPlanned optimizer iterations (nominal): %d\n", summary.TotalIterations)
	fmt.Fprintf(output, "  unconditional: %d\n", summary.FirmIterations())
	fmt.Fprintf(output, "  conditional:   %d across %d %s, decided at run time\n",
		summary.ConditionalIterations, summary.Conditional, pluralStages(summary.Conditional))
	fmt.Fprintln(output, "\nThe figure is the nominal planned count, not a prediction, and not a hard")
	fmt.Fprintln(output, "ceiling either. Early stopping and convergence detection spend less than it;")
	fmt.Fprintf(output, "a batch stage that leaves circles unplaced may run up to %d residual-refill\n",
		renderer.MaxExtraBatchStages)
	fmt.Fprintln(output, "stages beyond its plan and spend more. Those refills are excluded here")
	fmt.Fprintln(output, "because most stages never run them.")
	return nil
}

func pluralStages(count int) string {
	if count == 1 {
		return "stage"
	}
	return "stages"
}

// stagePlanParameters states what the stage was realized with, in the terms the
// document was written in.
func stagePlanParameters(stage app.ScheduleStage) string {
	config := stage.Config
	switch stage.Kind {
	case app.ScheduleStagePolish:
		return fmt.Sprintf("%s, active set %d, %d sweeps × %d × %d iters, pop %d",
			config.PolishingStrategy, config.PolishingActiveSetSize, config.PolishingMaxSweeps,
			config.PolishingEpochs, config.PolishingIters, config.PolishingPopSize)
	case app.ScheduleStageExtend:
		return fmt.Sprintf("+%d circles, batch %d, %d × %d iters, pop %d",
			stage.AdditionalCircles, config.BatchSize, config.OptimizerEpochs, config.Iters, config.PopSize)
	default:
		return fmt.Sprintf("%s, batch %d, %d × %d iters, pop %d",
			config.Mode, config.BatchSize, config.OptimizerEpochs, config.Iters, config.PopSize)
	}
}

// stagePlanCondition names a conditional stage as conditional.
//
// A dry run has no outcomes, so it cannot know whether a conditional stage will
// run — and a plan that quietly included or excluded it would be a claim it has
// no evidence for. The stage is listed either way, marked, and the condition
// printed in full.
func stagePlanCondition(stage app.ScheduleStage) string {
	if stage.When == nil {
		return "always"
	}
	return "conditional: " + stage.When.Describe()
}

// printScheduleProjection reports a finish estimate for a running campaign,
// derived only from the stages that have already completed.
//
// Only a running campaign gets a finish timestamp. The projection anchors at
// asOf, which is a claim about the clock and therefore only true while the
// server is advancing the campaign: a failed or cancelled one never will, and a
// paused one resumes at a moment nothing here can know. Their remaining stages
// still map to pending work, so the arithmetic would happily print a future
// finish time for a campaign that is already over.
func printScheduleProjection(output io.Writer, detail scheduleDetailResponse, asOf time.Time) {
	switch store.ScheduleState(detail.State) {
	case store.ScheduleStateCompleted, store.ScheduleStateFailed, store.ScheduleStateCancelled:
		fmt.Fprintf(output, "\nNo projection: the campaign is %s and will not advance.\n", detail.State)
		return
	}

	plan, err := detail.Document.Expand()
	if err != nil {
		// A stored document that no longer expands is a real problem, but it is
		// the stage table's problem to report; a projection simply has no plan.
		return
	}
	projection := app.ProjectScheduleFinish(plan, stageTimings(detail.Stages), asOf)
	if projection.RemainingStages == 0 {
		fmt.Fprintln(output, "\nNo projection: every planned stage has completed or been skipped.")
		return
	}

	fmt.Fprintln(output, "\nProjection (from measured stage wall clock only):")
	writer := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "KIND\tMEASURED\tPER STAGE\tREMAINING\tESTIMATE\tNOTE")
	for _, kind := range projection.Kinds {
		perStage, estimate := "-", "-"
		if kind.Projected() {
			perStage = kind.PerStage.Round(time.Second).String()
			estimate = kind.Remaining.Round(time.Second).String()
		}
		fmt.Fprintf(writer, "%s\t%d\t%s\t%d\t%s\t%s\n",
			kind.Kind, kind.Samples, perStage, kind.RemainingStages, estimate, kind.Note)
	}
	_ = writer.Flush()

	if !projection.Complete {
		fmt.Fprintln(output, "No finish time: not every remaining stage kind has been measured yet.")
		return
	}
	if store.ScheduleState(detail.State) != store.ScheduleStateRunning {
		// A campaign that is not advancing has a remaining workload but no
		// finish time: that would require knowing when it starts again.
		fmt.Fprintf(output, "Remaining once the campaign runs again: %s (no finish time while it is %s)\n",
			projection.Remaining.Round(time.Second), detail.State)
		if projection.Firm != projection.Remaining {
			fmt.Fprintf(output, "Earliest if every conditional stage is skipped: %s\n",
				projection.Firm.Round(time.Second))
		}
		return
	}
	fmt.Fprintf(output, "Remaining: %s, finishing around %s\n",
		projection.Remaining.Round(time.Second), projection.FinishBy.Format(time.RFC3339))
	if projection.Firm != projection.Remaining {
		fmt.Fprintf(output, "Earliest if every conditional stage is skipped: %s (%s)\n",
			projection.Firm.Round(time.Second), projection.EarliestFinish.Format(time.RFC3339))
	}
}

// stageTimings reduces the stage records to the wall clock the projection
// reads. A stage without both timestamps measured nothing.
func stageTimings(stages []store.ScheduleStageRecord) []app.ScheduleStageTiming {
	timings := make([]app.ScheduleStageTiming, 0, len(stages))
	for _, stage := range stages {
		timing := app.ScheduleStageTiming{
			Index: stage.Index,
			Kind:  stage.Kind,
			State: scheduleOutcomeState(stage.State),
		}
		if stage.StartedAt != nil && stage.CompletedAt != nil {
			timing.Elapsed = stage.CompletedAt.Sub(*stage.StartedAt)
		}
		timings = append(timings, timing)
	}
	return timings
}

// scheduleOutcomeState narrows a stage's lifecycle state to what the projection
// distinguishes. A failed or cancelled stage is neither a measurement nor a
// settled one, so it stays remaining work.
func scheduleOutcomeState(state store.ScheduleState) app.ScheduleOutcomeState {
	switch state {
	case store.ScheduleStateCompleted:
		return app.ScheduleOutcomeCompleted
	case store.ScheduleStateSkipped:
		return app.ScheduleOutcomeSkipped
	default:
		return app.ScheduleOutcomePending
	}
}
