package app

import "fmt"

// An advisory is the third answer a document can get. Validation gives two: the
// document runs, or it does not. Some documents run exactly as written and
// still spend their budget on something a measurement says is worthless, and
// that is neither a yes nor a no — so it is reported separately rather than
// folded into either.

// ScheduleAdvisory is a note about a document that is valid but measured
// wasteful. It is never an error: the campaign runs exactly as authored, and a
// document that wants the setting keeps it.
//
// The structured fields exist so a surface can count, group or filter without
// parsing prose. Message carries the whole note, because every surface that
// prints one — a CLI line, a slog record, a JSON warnings array — wants a
// single string.
type ScheduleAdvisory struct {
	// Field names the authoring site the setting was written at, not the stage
	// it was observed on: "base.popSize", "steps[1].popSize",
	// "steps[2].polishingPopSize". A step that overrides no population inherits
	// the base's, so its note points at the base rather than at a step that
	// says nothing about the field being complained about.
	//
	// A polish step authors its polishing population through the step's own
	// popSize key, which is what the format documents. The advisory names the
	// configuration field that key lands on instead, so a polish note cannot be
	// mistaken for an extend note about the job-wide population.
	Field string

	// Stages is how many realized stages carry the setting. A repeated step is
	// one advisory over all its repetitions, so a 63-extend climb reads as one
	// note about 63 stages instead of 63 identical lines.
	Stages int

	// Message is the whole note: the field, the values, the stage count, and
	// the measurement the advice rests on.
	Message string
}

// String is the printable form, and it is Message verbatim. An advisory is
// written to be read as one sentence; satisfying fmt.Stringer is what lets a
// surface hand it straight to %s or to slog without unpacking it first.
func (a ScheduleAdvisory) String() string {
	return a.Message
}

// Advisories reports the settings a document may keep but probably should not.
//
// It is deliberately not called from validate or Validate. Those return an
// error or nothing, and folding a non-fatal note into them would make every
// caller decide which errors are fatal. This is a separate pure query over the
// authored document, the same shape SummarizeSchedulePlan already is, so a
// surface asks for advisories when it has somewhere to print them and ignores
// them otherwise.
//
// A document that does not expand yields no advisories. Expansion failed for a
// reason the caller is already reporting as an error, and an advisory list is
// not the place to report it a second time.
//
// The result is ordered by the first realized stage that carries each setting,
// so a document setting more than one always reports the same one first — the
// same determinism advancedKnobs is written for.
func (d ScheduleDocument) Advisories() []ScheduleAdvisory {
	plan, err := d.Expand()
	if err != nil {
		return nil
	}

	defaults := DefaultConfig()

	var (
		advisories []ScheduleAdvisory
		budgets    []scheduleBudget
	)

	seen := make(map[scheduleBudget]int)

	for _, stage := range plan {
		budget, wasteful := d.wastefulBudget(stage, defaults)
		if !wasteful {
			continue
		}

		if at, repeated := seen[budget]; repeated {
			advisories[at].Stages++

			continue
		}

		seen[budget] = len(advisories)

		advisories = append(advisories, ScheduleAdvisory{Field: budget.field, Stages: 1})
		budgets = append(budgets, budget)
	}

	for i := range advisories {
		advisories[i].Message = budgets[i].message(advisories[i].Stages)
	}

	return advisories
}

// scheduleBudget is the population and epoch pair one realized stage spends,
// named as the document authored it. An extend stage and a polish stage spend
// different pairs through different fields, so the shared check is written once
// against this and resolved per kind. It is comparable, which is what lets two
// stages carrying the same setting collapse into one advisory.
type scheduleBudget struct {
	field      string
	epochField string
	population int
	epochs     int
	polishing  bool
}

// wastefulBudget reports the stage's budget pair when the pair is the measured
// waste, and false otherwise.
//
// The thresholds are read from DefaultConfig rather than from literals of their
// own. The point of the check is "raised above the default without raising the
// epochs to use it", so a default that moves has to move the check with it.
func (d ScheduleDocument) wastefulBudget(stage ScheduleStage, defaults JobConfig) (scheduleBudget, bool) {
	config := stage.Config

	switch stage.Kind {
	case ScheduleStageBase, ScheduleStageExtend:
		// The measurement behind this note is MayFly's, but popSize is not.
		// The same key reaches CMA-ES as lambda and Dragonfly as NPop, and a
		// population above the default is an ordinary setting there rather than
		// a measured waste — raising lambda on a multimodal landscape is
		// exactly what the IPOP and BIPOP restart strategies this repository
		// exposes do on purpose. An advisory that quotes a figure may only fire
		// where the figure was taken, so the engine is checked before the
		// budget is. It is read through ResolvedOptimizer rather than off the
		// field, because a document that names no engine runs MayFly and still
		// has to be warned.
		if config.ResolvedOptimizer() != OptimizerMayfly {
			return scheduleBudget{}, false
		}

		if config.PopSize <= defaults.PopSize || config.OptimizerEpochs != 1 {
			return scheduleBudget{}, false
		}

		return scheduleBudget{
			field:      d.authoringSite(stage, "popSize"),
			epochField: "optimizerEpochs",
			population: config.PopSize,
			epochs:     config.OptimizerEpochs,
		}, true
	case ScheduleStagePolish:
		// Deliberately ungated, and not a symmetry the file forgot: a polishing
		// sweep runs its own MayFly population whatever engine the document
		// names, so the measurement's engine is this stage's engine by
		// construction. A document that names another engine and asks for a
		// polish step never reaches here anyway — validation refuses the
		// polishing fields under a non-MayFly engine before the plan expands.
		if config.PolishingPopSize <= defaults.PolishingPopSize || config.PolishingEpochs != 1 {
			return scheduleBudget{}, false
		}

		return scheduleBudget{
			field:      d.authoringSite(stage, "polishingPopSize"),
			epochField: "polishingEpochs",
			population: config.PolishingPopSize,
			epochs:     config.PolishingEpochs,
			polishing:  true,
		}, true
	}

	return scheduleBudget{}, false
}

// authoringSite names the field the stage's population was written at.
//
// A step that sets no popSize runs the base's population, so pointing the note
// at the step would send a reader to a stanza that never mentions the field.
// Every step override is a pointer for exactly this reason: an omitted override
// and an explicit value do not look alike.
func (d ScheduleDocument) authoringSite(stage ScheduleStage, name string) string {
	if stage.StepIndex >= 0 && stage.StepIndex < len(d.Steps) && d.Steps[stage.StepIndex].PopSize != nil {
		return qualifiedField(fmt.Sprintf("steps[%d]", stage.StepIndex), name)
	}

	return qualifiedField("base", name)
}

// message renders the note for a budget carried by the given number of stages.
//
// Both kinds name the same measurement, because the mechanism behind it is the
// same: an epoch reseeds from the best candidate so far, so a population with
// one epoch has nowhere to spend itself. Only the extend arm was measured,
// though, so the polishing note says whose figure it is borrowing instead of
// claiming evidence that does not exist.
func (b scheduleBudget) message(stages int) string {
	header := fmt.Sprintf("%s %d with %s %d, on %s",
		b.field, b.population, b.epochField, b.epochs, countedStages(stages))

	if b.polishing {
		return header + ": an epoch reseeds from the best candidate so far, so a population with " +
			"one epoch has nowhere to spend itself. The figure behind that was taken on extend " +
			"stages, not on polishing sweeps: " + growthCampaignMeasurement +
			" The mechanism carries over to a sweep; the figure has not been measured there. " +
			"Raise both or neither. " + scheduleFormatReference
	}

	return header + ": " + growthCampaignMeasurement + " An epoch reseeds from the best candidate " +
		"so far, so a population with one epoch has nowhere to spend itself. Raise both or neither. " +
		scheduleFormatReference
}

// growthCampaignMeasurement is the evidence both advisories rest on, quoted as
// the run measured it. Its figures are a historical record of one campaign, so
// they stay literal here while the check that fires reads today's defaults.
const growthCampaignMeasurement = "measured on a 512x512 growth campaign, raising popSize 30 to 100 " +
	"at optimizerEpochs 1 moved cost by 0.026 for 2.2x the wall clock, while the same change at " +
	"optimizerEpochs 3 was worth 1.94."

// scheduleFormatReference points at the page carrying the recipe, so the note
// can stay one paragraph long without dropping the reader.
const scheduleFormatReference = "See docs/schedule-format.md."

// countedStages counts stages in words a sentence can contain, because a note
// reading "on 1 stages" would undercut the figures it is quoting.
func countedStages(stages int) string {
	if stages == 1 {
		return "1 stage"
	}

	return fmt.Sprintf("%d stages", stages)
}
