package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/fit/renderer"
	"github.com/cwbudde/circlefit/internal/store"
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

	err := json.Unmarshal(data, &named)
	if err != nil {
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

	fmt.Fprintf(output, "Stages: %d (%d base, %d extend, %d polish; %d conditional)\n",
		summary.Stages, summary.Base, summary.Extends, summary.Polishes, summary.Conditional)
	printScheduleBarriers(output, plan)
	fmt.Fprintln(output)
	// The advisories sit above the table rather than below it because a dry run
	// is where an author reads a document before submitting it, and a note about
	// a budget that cannot be spent is worth more before seventy stage rows than
	// after them.
	printScheduleAdvisories(output, document.Advisories())

	writer := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "#\tKIND\tCIRCLES\tITERATIONS\tPARAMETERS\tWHEN")

	for _, stage := range plan {
		fmt.Fprintf(writer, "%d\t%s\t%d\t%d\t%s\t%s\n",
			stage.Index, stage.Kind, stage.Circles, stage.PlannedIterations(),
			stagePlanParameters(stage), stagePlanCondition(stage))
	}

	err = writer.Flush()
	if err != nil {
		return err
	}

	fmt.Fprintf(output, "\nPlanned optimizer iterations (nominal): %d\n", summary.TotalIterations)
	fmt.Fprintf(output, "  unconditional: %d\n", summary.FirmIterations())
	fmt.Fprintf(output, "  conditional:   %d across %d %s, decided at run time\n",
		summary.ConditionalIterations, summary.Conditional, pluralStages(summary.Conditional))
	fmt.Fprintln(output, "\nThe figure is the nominal planned count rather than a prediction: early")
	fmt.Fprintln(output, "stopping, convergence detection and a polish sweep that stops improving all")
	fmt.Fprintln(output, "spend less than it. Nothing spends more. A batch stage that places nothing")
	fmt.Fprintf(output, "may still retry against the residual, up to %d times, but those retries are\n",
		renderer.MaxExtraBatchStages)
	fmt.Fprintln(output, "drawn from the stage's own iteration budget instead of being added to it,")
	fmt.Fprintln(output, "so they cannot take a campaign past this plan.")

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
		return fmt.Sprintf("+%d circles, batch %d, %d × %d iters, pop %d%s",
			stage.AdditionalCircles, config.BatchSize, config.OptimizerEpochs, config.Iters, config.PopSize,
			stagePlanRestarts(config))
	default:
		return fmt.Sprintf("%s, batch %d, %d × %d iters, pop %d%s",
			config.Mode, config.BatchSize, config.OptimizerEpochs, config.Iters, config.PopSize,
			stagePlanRestarts(config))
	}
}

// stagePlanRestarts names the stage's restart shape, and says nothing when the
// stage takes the historical single attempt — which is every document written
// so far, so their plans print exactly as before.
//
// It is worth naming because the ITERATIONS column cannot distinguish the two
// shapes: both bound the stage at abs(N) times iters, so a fixed count of 8 and
// a cap filled by 8 or more attempts print the same figure. What differs is how
// the budget is spent — a fixed count runs exactly N attempts whatever each one
// costs, while a cap keeps launching whole attempts until none fits.
func stagePlanRestarts(config app.JobConfig) string {
	switch restarts := config.OptimizerRestarts; {
	case restarts < 0:
		return fmt.Sprintf(", restarts filling %d × iters", -restarts)
	case restarts > 1:
		return fmt.Sprintf(", %d restarts", restarts)
	default:
		return ""
	}
}

// printScheduleBarriers says up front how far a campaign will actually get.
// The stage table carries the same fact, but a reader scanning a fourteen-stage
// plan should not have to find it there to learn that only five of them run.
func printScheduleBarriers(output io.Writer, plan []app.ScheduleStage) {
	for _, stage := range plan {
		if !stage.PauseBefore {
			continue
		}

		fmt.Fprintf(output,
			"Barrier: runs stages 0-%d, then pauses before stage %d (%s, %d circles).\n",
			stage.Index-1, stage.Index, stage.Kind, stage.Circles)
		fmt.Fprintln(output, "         Everything after it stays planned; `schedule resume` releases it.")

		return
	}
}

// stagePlanCondition names a conditional stage as conditional.
//
// A dry run has no outcomes, so it cannot know whether a conditional stage will
// run — and a plan that quietly included or excluded it would be a claim it has
// no evidence for. The stage is listed either way, marked, and the condition
// printed in full.
func stagePlanCondition(stage app.ScheduleStage) string {
	condition := "always"
	if stage.When != nil {
		condition = "conditional: " + stage.When.Describe()
	}
	// A barrier is the most consequential thing a plan can say about a stage —
	// the campaign stops here — so it leads, and the condition follows it.
	if stage.PauseBefore {
		return "BARRIER: pauses here; " + condition
	}

	return condition
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

	printScheduleFinish(output, projection, detail.State)
	printScheduleCostProjection(output, projection.Cost)
}

// printScheduleFinish writes the wall-clock half of the projection: what is
// left, and when it lands.
//
// It is split out of printScheduleProjection so that the cost half is printed
// whatever this one concludes. An incomplete finish projection is precisely the
// case where the cost projection still has something to say — it keeps its
// circle answer when it has no time answer — so returning early from the whole
// report here would withhold the estimate the reader still has.
func printScheduleFinish(output io.Writer, projection app.ScheduleProjection, state string) {
	if !projection.Complete {
		fmt.Fprintln(output, "No finish time: not every remaining stage kind has been measured yet.")
		return
	}

	if store.ScheduleState(state) != store.ScheduleStateRunning {
		// A campaign that is not advancing has a remaining workload but no
		// finish time: that would require knowing when it starts again.
		fmt.Fprintf(output, "Remaining once the campaign runs again: %s (no finish time while it is %s)\n",
			projection.Remaining.Round(time.Second), state)

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

// printScheduleCostProjection writes the other question a campaign can be
// asking: not when it finishes, but what it will cost when it does.
//
// The two blocks are printed separately and are meant to disagree. Which one an
// operator should read is decided by whichever resource is scarce — a fixed
// circle ceiling is answered by gain per circle, an open budget with a deadline
// by gain per hour — and collapsing them into one number would pick that answer
// on the reader's behalf. Both are stated, and the closing line says why.
func printScheduleCostProjection(output io.Writer, cost app.ScheduleCostProjection) {
	if !cost.Projected() {
		// Below the sample gate there are no rates to print, only the reason
		// there are none, which the projection has already worded.
		if cost.Note != "" {
			fmt.Fprintf(output, "\nNo cost projection: %s\n", cost.Note)
		}

		return
	}

	fmt.Fprintf(output, "\nCost, from %s at %d circles and %.3f (PSNR %s):\n",
		countedCostSamples(cost.Samples), cost.LatestCircles, cost.LatestCost, formatCLIPSNR(cost.LatestCost))

	printCostCeilingBlock(output, cost)
	printCostBudgetBlock(output, cost)

	if cost.Note != "" {
		fmt.Fprintf(output, "\nNote: %s\n", cost.Note)
	}

	fmt.Fprintln(output, "\nThe two answers differ because the objective does: see docs/schedule-format.md.")
}

// printCostCeilingBlock answers the campaign that is short of circles.
func printCostCeilingBlock(output io.Writer, cost app.ScheduleCostProjection) {
	fmt.Fprintln(output, "\nAgainst a circle ceiling (gain per circle):")

	if !cost.HasCircleCeiling {
		// Naming the missing denominator rather than dropping the block: a
		// reader who has been told there are two answers should not be left
		// deciding whether the second one was withheld or simply forgotten.
		if cost.RemainingCircles == 0 {
			fmt.Fprintln(output, "  none: the plan ends at the circle count the campaign already holds.")
		} else {
			fmt.Fprintln(output, "  none: the trailing window added no circles, so there is no per-circle rate.")
		}

		return
	}

	// A per-circle gain is a fraction of a cost unit, so it is printed at the
	// same six places the projection's own decay note uses.
	fmt.Fprintf(output, "  measured   %.6f cost/circle over the last %d circles (%s)\n",
		cost.RecentGainPerCircle, cost.RecentCircles, countedLegs(cost.RecentLegs))
	fmt.Fprintf(output, "  remaining  %d circles to %d\n",
		cost.RemainingCircles, cost.LatestCircles+cost.RemainingCircles)
	fmt.Fprintf(output, "  projected  %.3f (PSNR %s)\n", cost.CostAtPlanEnd, formatCLIPSNR(cost.CostAtPlanEnd))
}

// printCostBudgetBlock answers the campaign that is short of time.
func printCostBudgetBlock(output io.Writer, cost app.ScheduleCostProjection) {
	fmt.Fprintln(output, "\nAgainst a time budget (gain per hour):")

	if !cost.HasTimeBudget {
		// The usual cause is the finish projection above having declined to
		// complete, which the reader was told two lines ago; saying so again
		// here is what connects the two.
		if cost.RemainingElapsed == 0 {
			fmt.Fprintln(output, "  none: there is no remaining wall clock to spend, so the finish projection above")
			fmt.Fprintln(output, "        is the reason — an unprojected finish leaves this one nothing to carry.")
		} else {
			fmt.Fprintln(output, "  none: the trailing window measured no wall clock, so there is no per-hour rate.")
		}

		return
	}

	// A per-hour gain is a whole cost unit, so it is printed at the three places
	// the stage table prints a cost at.
	fmt.Fprintf(output, "  measured   %.3f cost/hour over the last %s (%s)\n",
		cost.RecentGainPerHour, cost.RecentElapsed.Round(time.Second), countedLegs(cost.RecentLegs))
	fmt.Fprintf(output, "  remaining  %s\n", cost.RemainingElapsed.Round(time.Second))
	fmt.Fprintf(output, "  projected  %.3f (PSNR %s)\n", cost.CostAtFinish, formatCLIPSNR(cost.CostAtFinish))
}

// countedLegs and countedCostSamples count in words a sentence can contain, the
// way the advisory's own stage count does.
//
// Both "measured" lines report the trailing rate, because that is the rate the
// projection below them was computed from — the per-circle return decays, and
// the campaign average over-predicts the next leg by 1.79x on the campaign this
// was measured on. Each is therefore labelled with the trailing window's own
// denominator, RecentCircles or RecentElapsed, never the whole campaign's:
// quoting a trailing rate against a whole-campaign span would be the same
// misstatement in reverse. The leg count follows in parentheses, because it is
// what says how many measurements the rate rests on.
func countedLegs(legs int) string {
	if legs == 1 {
		return "1 leg"
	}

	return fmt.Sprintf("%d legs", legs)
}

func countedCostSamples(samples int) string {
	if samples == 1 {
		return "1 measured stage"
	}

	return fmt.Sprintf("%d measured stages", samples)
}

// stageTimings reduces the stage listing to what the projections read: the wall
// clock a stage took, and the point on the cost curve it left behind. A stage
// the listing gives no elapsed for measured nothing.
//
// CostMeasured is set for a completed stage and for no other, which is the rule
// the stage table already applies to its cost and PSNR columns. A stage that is
// still running carries the zero cost its record was created with, and zero is
// not a missing cost — it is a perfect fit. Reading it as one would put the
// campaign at infinite PSNR and project every remaining stage from a gain that
// never happened, so an unmeasured stage contributes nothing instead.
func stageTimings(stages []scheduleStageSummaryResponse) []app.ScheduleStageTiming {
	timings := make([]app.ScheduleStageTiming, 0, len(stages))
	for _, stage := range stages {
		// The cost projection charges a gain to the circles that bought it, so
		// it reads what the stage really built. A stage that has not settled,
		// and any stage read from a server that predates the field, sends no
		// count and falls back to the planned one.
		circles := stage.Circles
		if stage.ActualCircles > 0 {
			circles = stage.ActualCircles
		}

		timing := app.ScheduleStageTiming{
			Index:        stage.Index,
			Kind:         stage.Kind,
			State:        scheduleOutcomeState(stage.State),
			Circles:      circles,
			BestCost:     stage.BestCost,
			CostMeasured: stage.State == store.ScheduleStateCompleted,
		}
		if stage.ElapsedNanos != nil {
			timing.Elapsed = time.Duration(*stage.ElapsedNanos)
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
