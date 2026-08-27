package app_test

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/circlefit/internal/app"
)

// The fixture is the 512x512 growth campaign of 2026-08-19/20, sampled at its
// three thousand-circle milestones. Its numbers are the reason the forward
// projections read a trailing window instead of the campaign average, so the
// tests below assert against the campaign as measured rather than against
// figures chosen to be convenient.
const (
	costAt1000 = 96.199
	costAt2000 = 64.602
	costAt3000 = 46.905

	// Per-leg wall clock. The exact figures are plausible rather than recorded
	// — an extend gets slower as the canvas grows, so the second leg is the
	// longer one — but the second is the campaign's own 56 minutes, which is
	// what puts its per-hour rate at the 18.96 the CLI block reports.
	baseElapsed    = 30 * time.Minute
	leg1000To2000  = 42 * time.Minute
	leg2000To3000  = 56 * time.Minute
	measuredWindow = leg1000To2000 + leg2000To3000
)

// costTolerance is the slack every float comparison below is made with.
//
// The expected figures are differences of five-significant-digit costs divided
// by circle counts and hours, so double-precision error lands around 1e-15,
// while the smallest difference any assertion has to resolve is 0.007
// cost/circle. 1e-9 sits far above the one and far below the other, and is
// wide enough to let the per-hour expectations be written as ten-decimal
// literals instead of recomputing production's own arithmetic.
const costTolerance = 1e-9

func closeTo(t *testing.T, name string, got, want float64) {
	t.Helper()

	if math.Abs(got-want) > costTolerance {
		t.Errorf("%s = %.10f, want %.10f (tolerance %g)", name, got, want, costTolerance)
	}
}

// costPlanTo is the fixture's plan, optionally carrying one more extend so
// there are circles left for a projection to be made against.
func costPlanTo(ceiling int) []app.ScheduleStage {
	plan := []app.ScheduleStage{
		{Index: 0, Kind: app.ScheduleStageBase, Circles: 1000},
		{Index: 1, Kind: app.ScheduleStageExtend, Circles: 2000, AdditionalCircles: 1000},
		{Index: 2, Kind: app.ScheduleStageExtend, Circles: 3000, AdditionalCircles: 1000},
	}

	if ceiling > 3000 {
		plan = append(plan, app.ScheduleStage{
			Index: 3, Kind: app.ScheduleStageExtend, Circles: ceiling, AdditionalCircles: ceiling - 3000,
		})
	}

	return plan
}

func costMeasured(
	index int, kind app.ScheduleStageKind, circles int, cost float64, elapsed time.Duration,
) app.ScheduleStageTiming {
	return app.ScheduleStageTiming{
		Index: index, Kind: kind, State: app.ScheduleOutcomeCompleted, Elapsed: elapsed,
		Circles: circles, BestCost: cost, CostMeasured: true,
	}
}

// costTimings is the campaign as measured: base to 1000 circles, then two
// extends of 1000 each.
func costTimings() []app.ScheduleStageTiming {
	return []app.ScheduleStageTiming{
		costMeasured(0, app.ScheduleStageBase, 1000, costAt1000, baseElapsed),
		costMeasured(1, app.ScheduleStageExtend, 2000, costAt2000, leg1000To2000),
		costMeasured(2, app.ScheduleStageExtend, 3000, costAt3000, leg2000To3000),
	}
}

// TestProjectScheduleCostMeasuresTheSpanAndEachLeg pins the arithmetic against
// the campaign: the whole measured span, and each of its two legs read on its
// own.
func TestProjectScheduleCostMeasuresTheSpanAndEachLeg(t *testing.T) {
	t.Parallel()

	plan := costPlanTo(3000)
	timings := costTimings()

	tests := []struct {
		name             string
		timings          []app.ScheduleStageTiming
		wantSamples      int
		wantGain         float64
		wantAddedCircles int
		wantElapsed      time.Duration
		wantPerCircle    float64
		wantPerHour      float64
	}{
		{
			name:             "the whole measured span",
			timings:          timings,
			wantSamples:      3,
			wantGain:         costAt1000 - costAt3000,
			wantAddedCircles: 2000,
			// 98 minutes, not 128: the base stage's own half hour bought the
			// starting point and none of the gain measured after it.
			wantElapsed:   measuredWindow,
			wantPerCircle: 0.024647,
			wantPerHour:   30.18,
		},
		{
			name:             "the first leg alone",
			timings:          timings[:2],
			wantSamples:      2,
			wantGain:         costAt1000 - costAt2000,
			wantAddedCircles: 1000,
			wantElapsed:      leg1000To2000,
			wantPerCircle:    0.031597,
			wantPerHour:      45.1385714286,
		},
		{
			name:             "the second leg alone",
			timings:          timings[1:],
			wantSamples:      2,
			wantGain:         costAt2000 - costAt3000,
			wantAddedCircles: 1000,
			wantElapsed:      leg2000To3000,
			wantPerCircle:    0.017697,
			wantPerHour:      18.9610714286,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			projection := app.ProjectScheduleCost(plan, testCase.timings, 0)

			if !projection.Projected() {
				t.Fatalf("projection gated with %d samples: %q", projection.Samples, projection.Note)
			}

			if projection.Samples != testCase.wantSamples {
				t.Errorf("samples = %d, want %d", projection.Samples, testCase.wantSamples)
			}

			if projection.AddedCircles != testCase.wantAddedCircles {
				t.Errorf("added circles = %d, want %d", projection.AddedCircles, testCase.wantAddedCircles)
			}

			if projection.Elapsed != testCase.wantElapsed {
				t.Errorf("elapsed = %v, want %v", projection.Elapsed, testCase.wantElapsed)
			}

			closeTo(t, "gain", projection.Gain, testCase.wantGain)
			closeTo(t, "gain per circle", projection.GainPerCircle, testCase.wantPerCircle)
			closeTo(t, "gain per hour", projection.GainPerHour, testCase.wantPerHour)
		})
	}
}

// TestProjectScheduleCostProjectsFromTheTrailingWindow is the design rule
// stated as a test. A projection standing at 2000 circles spends the rate the
// campaign had just measured, and the campaign then shows why: that rate
// over-predicted the next thousand circles by 1.79x, which is the whole reason
// the forward projections read the trailing window and not the average.
func TestProjectScheduleCostProjectsFromTheTrailingWindow(t *testing.T) {
	t.Parallel()

	timings := costTimings()

	atTwoThousand := app.ProjectScheduleCost(costPlanTo(3000), timings[:2], 0)
	if !atTwoThousand.HasCircleCeiling {
		t.Fatalf("no circle ceiling projected at 2000 circles: %+v", atTwoThousand)
	}

	if atTwoThousand.RemainingCircles != 1000 {
		t.Errorf("remaining circles = %d, want 1000 to the plan's 3000", atTwoThousand.RemainingCircles)
	}

	closeTo(t, "trailing rate at 2000", atTwoThousand.RecentGainPerCircle, 0.031597)
	closeTo(t, "cost at plan end", atTwoThousand.CostAtPlanEnd, costAt2000-0.031597*1000)

	// What the campaign actually did with those thousand circles, against what
	// the rate promised. The projection was optimistic by the factor the
	// trailing window exists to keep from compounding.
	predictedGain := costAt2000 - atTwoThousand.CostAtPlanEnd
	actualGain := costAt2000 - costAt3000

	if ratio := predictedGain / actualGain; math.Abs(ratio-1.7854) > 1e-3 {
		t.Errorf("the rate over-predicted the next leg by %.4fx, want the campaign's 1.7854x", ratio)
	}

	// Standing at 3000 with another thousand planned, the two legs are averaged
	// by the whole-campaign rate and only the second is spent by the trailing
	// one. The projection must use the second.
	atThreeThousand := app.ProjectScheduleCost(costPlanTo(4000), timings, leg2000To3000)
	if atThreeThousand.RecentLegs != 1 {
		t.Fatalf("trailing legs = %d, want 1 — the trailing half of two legs", atThreeThousand.RecentLegs)
	}

	closeTo(t, "trailing rate at 3000", atThreeThousand.RecentGainPerCircle, 0.017697)
	closeTo(t, "campaign rate at 3000", atThreeThousand.GainPerCircle, 0.024647)

	if !atThreeThousand.HasCircleCeiling {
		t.Fatalf("no circle ceiling projected toward 4000: %+v", atThreeThousand)
	}

	closeTo(t, "cost at plan end", atThreeThousand.CostAtPlanEnd, costAt3000-0.017697*1000)

	if fromCampaignRate := costAt3000 - 0.024647*1000; math.Abs(atThreeThousand.CostAtPlanEnd-fromCampaignRate) < 1e-3 {
		t.Errorf("cost at plan end = %.6f, which is the whole-campaign extrapolation %.6f",
			atThreeThousand.CostAtPlanEnd, fromCampaignRate)
	}

	// The time answer spends the same trailing window against the finish
	// projection's clock: one more leg of 56 minutes buys one more leg's gain.
	if !atThreeThousand.HasTimeBudget {
		t.Fatalf("no time budget projected with %v remaining: %+v",
			atThreeThousand.RemainingElapsed, atThreeThousand)
	}

	closeTo(t, "trailing rate per hour", atThreeThousand.RecentGainPerHour, 18.9610714286)
	closeTo(t, "cost at finish", atThreeThousand.CostAtFinish, costAt3000-(costAt2000-costAt3000))
}

// TestProjectScheduleCostReportsDecayingReturn checks that the fixture's own
// decay is stated rather than left for the reader to notice, and that a
// campaign holding its rate is not qualified for no reason.
func TestProjectScheduleCostReportsDecayingReturn(t *testing.T) {
	t.Parallel()

	decaying := app.ProjectScheduleCost(costPlanTo(4000), costTimings(), 0)
	if !strings.Contains(decaying.Note, "decaying") {
		t.Fatalf("note = %q, want it to report the decaying per-circle return", decaying.Note)
	}

	for _, rate := range []string{"0.017697", "0.024647"} {
		if !strings.Contains(decaying.Note, rate) {
			t.Errorf("note = %q, want it to name the rate %s", decaying.Note, rate)
		}
	}

	// The same shape of campaign removing the same cost per circle in both
	// legs: the trailing rate equals the average, and there is nothing to say.
	steady := app.ProjectScheduleCost(costPlanTo(4000), []app.ScheduleStageTiming{
		costMeasured(0, app.ScheduleStageBase, 1000, 90, baseElapsed),
		costMeasured(1, app.ScheduleStageExtend, 2000, 70, leg1000To2000),
		costMeasured(2, app.ScheduleStageExtend, 3000, 50, leg2000To3000),
	}, 0)
	if steady.Note != "" {
		t.Errorf("note = %q on a campaign holding its rate, want none", steady.Note)
	}
}

// TestProjectScheduleCostRefusesToGuess covers the gate and the paths where a
// denominator was never measured. In every case the rates stay zero and the
// projection says why rather than filling the gap.
func TestProjectScheduleCostRefusesToGuess(t *testing.T) {
	t.Parallel()

	plan := costPlanTo(4000)

	tests := []struct {
		name    string
		timings []app.ScheduleStageTiming
	}{
		{name: "nothing has run"},
		{
			name:    "one stage is a starting point, not a gain",
			timings: costTimings()[:1],
		},
		{
			name: "completed stages that recorded no cost",
			timings: []app.ScheduleStageTiming{
				{Index: 0, Kind: app.ScheduleStageBase, State: app.ScheduleOutcomeCompleted, Elapsed: baseElapsed, Circles: 1000},
				{
					Index: 1, Kind: app.ScheduleStageExtend, State: app.ScheduleOutcomeCompleted,
					Elapsed: leg1000To2000, Circles: 2000,
				},
			},
		},
		{
			name: "a skipped stage is not a measurement",
			timings: []app.ScheduleStageTiming{
				costTimings()[0],
				{
					Index: 1, Kind: app.ScheduleStageExtend, State: app.ScheduleOutcomeSkipped,
					Circles: 2000, BestCost: costAt2000, CostMeasured: true,
				},
			},
		},
		{
			name: "measurements from outside the plan cannot move the estimate",
			timings: []app.ScheduleStageTiming{
				costTimings()[0],
				costMeasured(99, app.ScheduleStageExtend, 2000, costAt2000, leg1000To2000),
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			projection := app.ProjectScheduleCost(plan, testCase.timings, time.Hour)

			if projection.Projected() {
				t.Fatalf("projected from %d samples: %+v", projection.Samples, projection)
			}

			if !strings.Contains(projection.Note, "insufficient data") {
				t.Errorf("note = %q, want it to report insufficient data", projection.Note)
			}

			if projection.GainPerCircle != 0 || projection.RecentGainPerCircle != 0 ||
				projection.GainPerHour != 0 || projection.RecentGainPerHour != 0 {
				t.Errorf("a gated projection still carried rates: %+v", projection)
			}

			if projection.HasCircleCeiling || projection.HasTimeBudget {
				t.Errorf("a gated projection still claimed an answer: %+v", projection)
			}
		})
	}
}

// TestProjectScheduleCostSeparatesTheTwoQuestions is the point of the type: a
// campaign that adds no circles still has a per-hour answer, and one whose
// finish cannot be estimated still has a per-circle answer. Neither absence
// takes the other down with it.
func TestProjectScheduleCostSeparatesTheTwoQuestions(t *testing.T) {
	t.Parallel()

	// A polish-only span: three measured stages, all at the same circle count.
	polishPlan := []app.ScheduleStage{
		{Index: 0, Kind: app.ScheduleStagePolish, Circles: 3000},
		{Index: 1, Kind: app.ScheduleStagePolish, Circles: 3000},
		{Index: 2, Kind: app.ScheduleStagePolish, Circles: 3000},
	}
	polish := app.ProjectScheduleCost(polishPlan, []app.ScheduleStageTiming{
		costMeasured(0, app.ScheduleStagePolish, 3000, 50, 10*time.Minute),
		costMeasured(1, app.ScheduleStagePolish, 3000, 48, 10*time.Minute),
		costMeasured(2, app.ScheduleStagePolish, 3000, 47, 10*time.Minute),
	}, 30*time.Minute)

	if polish.HasCircleCeiling {
		t.Errorf("a polish-only span projected a per-circle answer: %+v", polish)
	}

	if !polish.HasTimeBudget {
		t.Fatalf("a polish-only span projected no per-hour answer: %+v", polish)
	}
	// The trailing half of two legs is the last one: 1 cost unit in ten
	// minutes, so 6 per hour, and half an hour buys 3.
	closeTo(t, "trailing rate per hour", polish.RecentGainPerHour, 6)
	closeTo(t, "cost at finish", polish.CostAtFinish, 44)

	// The same measurements with no finish estimate behind them.
	noClock := app.ProjectScheduleCost(costPlanTo(4000), costTimings(), 0)
	if noClock.HasTimeBudget {
		t.Errorf("a projection with no time budget still answered the clock: %+v", noClock)
	}

	if !noClock.HasCircleCeiling {
		t.Errorf("a missing finish estimate suppressed the circle answer: %+v", noClock)
	}
}

// TestProjectScheduleCostIsPure checks the property the whole file rests on:
// the answer depends on the plan and the measurements, not on the order they
// arrive in or on how often it is asked.
func TestProjectScheduleCostIsPure(t *testing.T) {
	t.Parallel()

	plan := costPlanTo(4000)
	ordered := costTimings()
	shuffled := []app.ScheduleStageTiming{ordered[2], ordered[0], ordered[1]}

	first := app.ProjectScheduleCost(plan, ordered, leg2000To3000)
	if second := app.ProjectScheduleCost(plan, ordered, leg2000To3000); first != second {
		t.Fatalf("two calls with the same inputs differed:\n%+v\n%+v", first, second)
	}

	if outOfOrder := app.ProjectScheduleCost(plan, shuffled, leg2000To3000); first != outOfOrder {
		t.Fatalf("the timing order moved the estimate:\n%+v\n%+v", first, outOfOrder)
	}
}

// TestProjectScheduleFinishCarriesTheCostProjection checks the single entry
// point: the finish projection fills the cost projection, and hands it its own
// Remaining as the time budget so the two cannot disagree.
func TestProjectScheduleFinishCarriesTheCostProjection(t *testing.T) {
	t.Parallel()

	plan := costPlanTo(4000)
	asOf := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)

	projection := app.ProjectScheduleFinish(plan, costTimings(), asOf)
	if !projection.Complete {
		t.Fatalf("projection incomplete: %+v", projection.Kinds)
	}
	// Two extends at 42 and 56 minutes average 49, and one extend is left.
	if want := 49 * time.Minute; projection.Remaining != want {
		t.Fatalf("remaining = %v, want %v", projection.Remaining, want)
	}

	if projection.Cost.RemainingElapsed != projection.Remaining {
		t.Errorf("cost projection's time budget = %v, want the finish projection's %v",
			projection.Cost.RemainingElapsed, projection.Remaining)
	}

	if !projection.Cost.HasTimeBudget || !projection.Cost.HasCircleCeiling {
		t.Fatalf("the finish projection carried an empty cost projection: %+v", projection.Cost)
	}
	// 49 minutes at the trailing 18.9610714286 cost/hour removes 15.484875.
	closeTo(t, "cost at finish", projection.Cost.CostAtFinish, costAt3000-15.484875)
	closeTo(t, "cost at plan end", projection.Cost.CostAtPlanEnd, costAt3000-0.017697*1000)
}
