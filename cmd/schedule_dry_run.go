package cmd

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/store"
)

// A dry run is `schedule create --dry-run` rather than a subcommand of its own.
// It answers the question a caller has while holding a document and about to
// submit it, and hanging it off create keeps the document argument, the local
// validation, and the flag set identical to the real thing — a separate verb
// would be a second path to keep in step for no gain.
//
// It reaches nothing. Expansion is a pure function of the document, so the dry
// run never opens a socket and the server it would have posted to never learns
// the document exists: no schedule directory, no stage file, no job.

// printSchedulePlan writes the realized stage list and its optimizer budget.
func printSchedulePlan(output io.Writer, path string, document *app.ScheduleDocument) error {
	plan, err := document.Expand()
	if err != nil {
		return fmt.Errorf("expand schedule %q: %w", path, err)
	}
	summary := app.SummarizeSchedulePlan(plan)

	fmt.Fprintf(output, "Dry run of %s — nothing was submitted and no schedule was created.\n", path)
	if document.Name != "" {
		fmt.Fprintf(output, "Name: %s\n", document.Name)
	}
	fmt.Fprintf(output, "Seed: %d\n", document.Seed)
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

	fmt.Fprintf(output, "\nPlanned optimizer iterations: %d\n", summary.TotalIterations)
	fmt.Fprintf(output, "  unconditional: %d\n", summary.FirmIterations())
	fmt.Fprintf(output, "  conditional:   %d across %d %s, decided at run time\n",
		summary.ConditionalIterations, summary.Conditional, pluralStages(summary.Conditional))
	fmt.Fprintln(output, "\nThe figure is the budget the configuration authorizes, not a prediction:")
	fmt.Fprintln(output, "early stopping and convergence detection can only spend less. Bounded")
	fmt.Fprintln(output, "residual-refill batch stages are excluded because most stages never run them.")
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
			config.PolishingEpochs, config.PolishingIters, config.PopSize)
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
func printScheduleProjection(output io.Writer, detail scheduleDetailResponse, asOf time.Time) {
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
