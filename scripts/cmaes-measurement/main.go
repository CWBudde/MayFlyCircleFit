// Command cmaes-measurement submits and collects the evaluation-matched
// campaign described by go-cma-es PLAN.md Phase 11.
//
//nolint:cyclop,embeddedstructfieldcheck,err113,forbidigo,goconst,lll,noinlineerr,wrapcheck // A standalone campaign driver reports contextual CLI errors and Markdown to stdout.
package main

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/cwbudde/circlefit/internal/opt"
	"github.com/cwbudde/circlefit/internal/store"
)

// The registered design names, so every place that has to name one -- the
// switch in campaignDesign, the artifact defaults, and the tests that
// enumerate them -- cannot drift apart.
const (
	designPhase21 = "phase21"
	designLambda  = "lambda"
	designPilot   = "stagnation-pilot"
	designStag    = "stagnation"
	designSplit   = "budget-split"
)

const (
	defaultBudget  = 6_502_400
	defaultProject = "cmaes-phase11"
	defaultPop     = 1024
	// campaignBlocks is the paired-block count the phase21 and lambda designs
	// registered. It is no longer universal: a design carries its own count, so
	// a descriptive pilot can buy three blocks and an inferential campaign
	// twelve, and df follows the design rather than this constant.
	campaignBlocks = 12
	// defaultCircles is the eight-circle batch every campaign before the
	// budget-split screen fitted, and the shape searchDimensions describes.
	defaultCircles = 8
	// searchDimensions is the dimensionality of that default fixture: eight
	// circles of seven parameters, fitted in one batch. It is only read by
	// hansenStagnationWindow, and both designs that call it run the default
	// fixture. A design on another circle count must not reuse it.
	searchDimensions = 56
	// resultColumns is the width of the header writeResults emits.
	resultColumns = 12
)

type arm struct {
	name              string
	optimizer         string
	covariance        string
	restartStrategy   string
	iters             int
	popSize           int
	optimizerRestarts int
	// stopStagnationIters ends an individual run after this many iterations
	// without sufficient progress, which is what lets a restart schedule
	// reclaim the budget of a run that has stopped improving. Zero is the
	// behaviour every campaign before this one measured: no criterion is
	// configured, Stop.enabled() is false, and a dead run holds its budget to
	// the cap.
	stopStagnationIters int
	// optimizerEpochs runs the engine several times in sequence, each epoch
	// re-initialized from the previous incumbent rather than cold. It is the
	// warm counterpart to optimizerRestarts, and it reaches CMA-ES as well as
	// MayFly: the server wraps WithRestarts(WithEpochs(...)) around whatever
	// newStageOptimizer built, and CMAESAdapter implements RunWithInitial.
	optimizerEpochs int
	// stopMinImprovement is the absolute cost reduction that counts as
	// progress. Zero accepts any improvement and is the only setting that can
	// become a shipped default, because a threshold in cost units does not
	// transfer between reference images whose costs differ in scale.
	stopMinImprovement float64
}

// plannedContrast is one paired comparison a design registers before it runs.
// Naming them is what bounds multiplicity: the lambda screen crossed two
// factors and thereby manufactured thirteen contrasts out of eight arms, and
// the resulting Holm threshold retained a p of 0.0056. A design that declares
// the comparisons it intends pays for those and no others.
type plannedContrast struct {
	control   string
	candidate string
	// primary marks the single comparison the campaign exists to make. A
	// design may register at most one.
	primary bool
}

// design is one registered campaign: a fixed arm set, the arm every paired
// comparison is taken against, and an optional second control reported
// underneath. Designs are named and enumerated rather than assembled from
// flags so a campaign cannot silently differ from the one that was registered.
type design struct {
	name             string
	baseline         string
	secondaryControl string
	// blocks is the design's paired-block count and the source of df. It is
	// per design rather than global so a descriptive pilot and an inferential
	// campaign can live in the same driver without either borrowing the
	// other's degrees of freedom.
	blocks int
	// seedBase is the design's first block seed prefix. It belongs to the
	// design, not to a flag, so two campaigns cannot silently share seeds and
	// a replication cannot silently fail to.
	seedBase int64
	arms     []arm
	// contrasts is the family every paired test is corrected over. phase21 and
	// lambda derive theirs from the two controls, which is how their committed
	// reports were produced and must stay reproducible; a design that fills
	// this in itself is corrected over exactly the comparisons it names.
	contrasts []plannedContrast
	// reference and circles are the fixture. They belong to the design because
	// a campaign run on a different image is not comparable to one run on
	// example/MayFly-512.png, and a flag that silently changed either would
	// make two campaigns' rows look poolable when they are not. A design that
	// leaves circles zero runs defaultCircles; one that leaves reference empty
	// takes whatever -ref supplies, which defaults to the image every earlier
	// campaign used but is not pinned to it.
	reference string
	circles   int
	// descriptive marks a design that buys mechanism rather than inference.
	// It has too few blocks for a paired test, so the report prints arm
	// summaries and refuses to print a statistic.
	descriptive bool
}

// fixture reports the reference image and circle count the design runs. A
// design that names neither falls back to defaultCircles and to the caller's
// -ref, so only a design that pins its reference is guaranteed the image it
// was registered on.
func (d design) fixture(fallback string) (string, int) {
	reference, circles := d.reference, d.circles
	if reference == "" {
		reference = fallback
	}

	if circles == 0 {
		circles = defaultCircles
	}

	return reference, circles
}

// primaryContrast returns the design's registered primary comparison.
func (d design) primaryContrast() (plannedContrast, bool) {
	for _, current := range d.contrasts {
		if current.primary {
			return current, true
		}
	}

	return plannedContrast{}, false
}

type manifestRow struct {
	Arm   string
	Block int
	Seed  int64
	JobID string
}

type jobStatus struct {
	ID          string  `json:"id"`
	State       string  `json:"state"`
	Termination string  `json:"termination"`
	BestCost    float64 `json:"bestCost"`
	Elapsed     float64 `json:"elapsed"`
	Iterations  int     `json:"iterations"`
	Evaluations int     `json:"evaluations"`
}

type resultRow struct {
	manifestRow
	State             string
	Termination       string
	OptimizerVersion  string
	Score             float64
	ScoredEvaluations int
	FinalEvaluations  int
	Iterations        int
	ElapsedSeconds    float64
	// Restarts is the collected checkpoint's per-run record. It is populated
	// only by collectJob, which reads the checkpoint; a row parsed back out of
	// the result CSV by readResults leaves it empty, because the result CSV is
	// one row per job and these are one row per run.
	Restarts []opt.RestartRun
}

type checkpoint struct {
	OptimizerVersion string           `json:"optimizerVersion"`
	Restarts         []opt.RestartRun `json:"restarts"`
}

type checkpointInfo struct {
	Termination      string `json:"termination"`
	OptimizerVersion string `json:"optimizerVersion"`
	Iteration        int    `json:"iteration"`
	Evaluations      int    `json:"evaluations"`
}

type settings struct {
	server       string
	dataRoot     string
	reference    string
	manifestPath string
	resultsPath  string
	trajectory   string
	restartsPath string
	project      string
	action       string
	design       string
	blocks       int
	budget       int
	workers      int
	seedBase     int64
}

func main() {
	config := parseFlags()
	var err error

	switch config.action {
	case "submit":
		err = submit(config)
	case "collect":
		err = collect(config)
	case "preliminary":
		err = collectPreliminary(config)
	case "analyze":
		err = analyzeDesign(config)
	case "plan":
		err = printPlan(config)
	default:
		err = fmt.Errorf("unknown action %q (want plan, submit, collect, preliminary, or analyze)", config.action)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseFlags() settings {
	var config settings
	flag.StringVar(&config.action, "action", "collect", "plan, submit, collect, preliminary, or analyze")
	flag.StringVar(&config.design, "design", "phase21", "registered campaign design: phase21, lambda, stagnation-pilot, stagnation or budget-split")
	flag.StringVar(&config.server, "server", "http://localhost:8085", "serve base URL")
	flag.StringVar(&config.dataRoot, "data-root", "./data/cmaes-phase11", "serve data root")
	flag.StringVar(&config.reference, "ref", "example/MayFly-512.png", "reference image")
	flag.StringVar(&config.manifestPath, "manifest", "", "job manifest (default: the design's own)")
	flag.StringVar(&config.resultsPath, "results", "", "collected result CSV (default: the design's own)")
	flag.StringVar(&config.trajectory, "trajectories", "", "diagnostic trajectory CSV (default: the design's own)")
	flag.StringVar(&config.restartsPath, "restarts", "", "per-restart outcome CSV (default: the design's own)")
	flag.StringVar(&config.project, "project", defaultProject, "server project")
	flag.IntVar(&config.blocks, "blocks", 0, "assert the design's paired block count (0 uses it)")
	flag.IntVar(&config.budget, "budget", defaultBudget, "optimizer evaluation cap")
	flag.IntVar(&config.workers, "workers", 8, "parallel evaluation workers")
	flag.Int64Var(&config.seedBase, "seed-base", 0, "assert the design's first block seed prefix (0 uses it)")
	flag.Parse()

	return withDesignArtifacts(config)
}

// withDesignArtifacts fills every artifact path the caller left unset with one
// belonging to the selected design. Without it a second campaign collected
// with only -design would write over the first one's committed data: the
// Phase 21 paths are not a neutral default, they are one campaign's record.
// Submission already refuses to overwrite an existing manifest; this extends
// that refusal to the CSVs collection writes.
func withDesignArtifacts(config settings) settings {
	prefix := "docs/cmaes-"
	manifest := "manifest.csv"

	if config.design != "" && config.design != designPhase21 {
		prefix += config.design + "-"
		manifest = "manifest-" + config.design + ".csv"
	}

	if config.manifestPath == "" {
		config.manifestPath = filepath.Join(config.dataRoot, manifest)
	}

	if config.resultsPath == "" {
		config.resultsPath = prefix + "measurement.csv"
	}

	if config.trajectory == "" {
		config.trajectory = prefix + "trajectories.csv"
	}

	if config.restartsPath == "" {
		config.restartsPath = prefix + "restarts.csv"
	}

	return config
}

// lambdaLevels are the initial CMA-ES population sizes the lambda screen
// visits. 1024 is what every earlier measurement used; 20 is the smallest
// population app.MinPopulation permits and the closest this repository can get
// to Hansen's default of 4+floor(3*ln(n)) = 16 for the 56-dimension search; 64
// sits between them. Every level has to divide the budget exactly so all arms
// are evaluation-matched by construction rather than by post-hoc truncation.
func lambdaLevels() []int { return []int{1024, 64, 20} }

func campaignDesign(name string, budget int) (design, error) {
	arms, err := campaignArms(budget)
	if err != nil {
		return design{}, err
	}

	switch name {
	case designPhase21:
		return withDerivedContrasts(design{
			name: name, baseline: "mayfly-single", secondaryControl: "mayfly-r16",
			blocks: campaignBlocks, seedBase: 111_000, arms: arms,
		}), nil
	case designLambda:
		screen, screenErr := lambdaScreenArms(budget)
		if screenErr != nil {
			return design{}, screenErr
		}

		return withDerivedContrasts(design{
			name: name, baseline: "cmaes-single", secondaryControl: "cmaes-ipop",
			blocks: campaignBlocks, seedBase: 111_000, arms: screen,
		}), nil
	case designPilot:
		pilot, pilotErr := stagnationPilotArms(budget)
		if pilotErr != nil {
			return design{}, pilotErr
		}

		return design{
			name: name, baseline: "sep-ipop-l20",
			blocks: stagnationPilotBlocks, seedBase: 112_000, arms: pilot,
			descriptive: true,
		}, nil
	case designStag:
		campaign, campaignErr := stagnationArms(budget)
		if campaignErr != nil {
			return design{}, campaignErr
		}

		return design{
			name: name, baseline: "sep-ipop-l20", secondaryControl: "sep-ipop",
			blocks: campaignBlocks, seedBase: 111_012, arms: campaign,
			contrasts: []plannedContrast{
				{control: "sep-ipop-l20", candidate: "sep-ipop-l20-w102", primary: true},
				{control: "sep-ipop", candidate: "sep-ipop-w60"},
			},
		}, nil
	case designSplit:
		split, splitErr := budgetSplitArms(budget)
		if splitErr != nil {
			return design{}, splitErr
		}

		return design{
			name: name, baseline: "mayfly-r16", secondaryControl: "sep-r5",
			blocks: campaignBlocks, seedBase: 113_000, arms: split,
			reference: "example/Ref-512.png", circles: 12,
			contrasts: []plannedContrast{
				{control: "mayfly-r16", candidate: "sep-ipop", primary: true},
				{control: "sep-r5", candidate: "sep-e5"},
			},
		}, nil
	default:
		return design{}, fmt.Errorf(
			"unknown design %q (want phase21, lambda, stagnation-pilot, stagnation or budget-split)", name)
	}
}

// withDerivedContrasts fills in the family a design gets when it does not name
// one: every arm against each control, controls outer. It reproduces exactly
// what buildContrasts used to compute inline, so the phase21 and lambda
// reports stay reproducible from their committed CSVs.
func withDerivedContrasts(plan design) design {
	controls := []string{plan.baseline, plan.secondaryControl}

	plan.contrasts = make([]plannedContrast, 0, len(controls)*(len(plan.arms)-1))
	for _, control := range controls {
		for _, current := range plan.arms {
			if current.name == control || current.name == plan.baseline {
				continue
			}

			plan.contrasts = append(plan.contrasts, plannedContrast{control: control, candidate: current.name})
		}
	}

	return plan
}

// stagnationPilotBlocks is deliberately far too few blocks for a paired test.
// The pilot exists to measure a mechanism -- how many restarts a window buys
// and how much budget it reclaims -- and to select the window the registered
// campaign will then test on cost. Selecting it on cost and afterwards testing
// cost would be selecting on the outcome.
const stagnationPilotBlocks = 3

// hansenStagnationWindow is Hansen's stagnation length, 120 + 30*n/lambda, for
// the dimensionality this campaign searches. It is an a-priori anchor for the
// window and not a claim of fidelity: go-cma-es stops a run after N iterations
// without sufficient progress, while Hansen's criterion tests a median of
// fitness histories across that span.
func hansenStagnationWindow(lambda int) int { return 120 + 30*searchDimensions/lambda }

// stagnationPilotArms measures what a stagnation criterion does to a restart
// schedule, at the two population sizes the registered campaign will use. Both
// are separable IPOP, the shape the lambda screen left standing; lambda 20 is
// where the measured waste is worst (56.7%) and where an IPOP ladder stays
// inside the population range that screen found unremarkable, and lambda 1024
// is the shape Phase 21 actually ran.
//
// Each level contributes its own no-criterion baseline, so reclaimed budget is
// read within the pilot's own three blocks rather than against the lambda
// screen's different seeds. Windows are half, one and four times the Hansen
// anchor. One extra cell raises stopMinImprovement off zero at lambda 20's
// anchor: the committed lambda traces show 30.9% of that arm's recorded
// improvements are smaller than 0.1 cost units and the smallest is 2.7e-05, so
// whether trivial improvements keep resetting the counter is worth one arm --
// but it stays exploratory, because an absolute cost threshold cannot become a
// default that holds on a reference image of a different scale.
func stagnationPilotArms(budget int) ([]arm, error) {
	levels := []int{20, 1024}

	arms := make([]arm, 0, len(levels)*4+1)
	for _, lambda := range levels {
		if budget%lambda != 0 {
			return nil, fmt.Errorf("budget %d is not divisible by lambda %d; the arms would not be evaluation-matched", budget, lambda)
		}

		base := stagnationArm(stagnationArmName(lambda, 0), lambda, budget, 0, 0)
		arms = append(arms, base)

		anchor := hansenStagnationWindow(lambda)
		for _, window := range []int{anchor / 2, anchor, anchor * 4} {
			if err := checkStagnationWindow(lambda, budget, window); err != nil {
				return nil, err
			}

			arms = append(arms, stagnationArm(stagnationArmName(lambda, window), lambda, budget, window, 0))
		}

		if lambda == 20 {
			arms = append(arms, stagnationArm(
				stagnationArmName(lambda, anchor)+"-min01", lambda, budget, anchor, 0.1))
		}
	}

	return arms, nil
}

// checkStagnationWindow rejects a window a run at this level could never
// reach. app.JobConfig.Validate refuses a stopStagnationIters larger than
// iters, so a budget too small for a level's window fails at submit -- after
// the preceding arms are already queued, and the manifest is written with
// O_EXCL, so the campaign cannot simply be resubmitted. Catch it here, while
// the design is still being built and nothing has been queued.
func checkStagnationWindow(lambda, budget, window int) error {
	iters := budget / lambda
	if window > iters {
		return fmt.Errorf(
			"budget %d leaves lambda %d only %d iterations, shorter than its %d-generation stagnation window",
			budget, lambda, iters, window)
	}

	return nil
}

// stagnationArms is the registered campaign the pilot selected. Two pairs, one
// per population size: each level's no-criterion baseline against the same
// level under a stagnation window of half the Hansen anchor -- 102 generations
// at lambda 20 and 60 at lambda 1024.
//
// The window is not chosen here. The pilot's rule was fixed before its data
// existed -- take the window that reclaims the most budget while still
// completing at least two restarts, ties toward the anchor -- and the pilot
// answered it at both levels with the half-anchor: it reclaimed 19.7 and 25.6
// percentage points of budget spent after the last improvement, where the
// anchor itself reclaimed nothing at lambda 20 and four times the anchor never
// fired at all, returning its baseline's cost to the last digit in all three
// blocks. Cost did not select it and cost is what this campaign tests.
//
// Four arms and exactly two registered contrasts, so Holm corrects over two
// rather than over however many arms the design happens to carry. lambda 20 is
// primary: it is the level where the pilot's criterion bought another restart
// rather than merely a longer final run, the ladder at lambda 1024 being
// capped at three runs by the evaluation budget however it terminates.
func stagnationArms(budget int) ([]arm, error) {
	arms := make([]arm, 0, 4)

	for _, lambda := range []int{20, defaultPop} {
		if budget%lambda != 0 {
			return nil, fmt.Errorf(
				"budget %d is not divisible by lambda %d; the arms would not be evaluation-matched", budget, lambda)
		}

		window := hansenStagnationWindow(lambda) / 2
		if err := checkStagnationWindow(lambda, budget, window); err != nil {
			return nil, err
		}

		arms = append(arms,
			stagnationArm(stagnationArmName(lambda, 0), lambda, budget, 0, 0),
			stagnationArm(stagnationArmName(lambda, window), lambda, budget, window, 0))
	}

	return arms, nil
}

// stagnationArmName keeps the lambda level and the window in the arm name, so
// a result CSV row says what it ran without a lookup. Window zero is the
// no-criterion baseline.
func stagnationArmName(lambda, window int) string {
	name := "sep-ipop"
	if lambda != defaultPop {
		name = fmt.Sprintf("%s-l%d", name, lambda)
	}

	if window > 0 {
		name = fmt.Sprintf("%s-w%d", name, window)
	}

	return name
}

func stagnationArm(name string, lambda, budget, window int, minImprovement float64) arm {
	return arm{
		name: name, optimizer: "cmaes", covariance: "separable", restartStrategy: "ipop",
		iters: budget / lambda, popSize: lambda, optimizerRestarts: 1,
		stopStagnationIters: window, stopMinImprovement: minImprovement,
	}
}

// budgetSplitArms asks two questions on a fixture nothing has been measured on.
//
// The first is whether CMA-ES should be the default engine. Phase 21 found
// separable IPOP beating MayFly's long run in 12/12 blocks and its r16 arm in
// 11/12, and the three objections recorded against reading that as a default
// change have since been answered and none survived: lambda is a null at 20,
// 64 and 1024, separable covariance alone is a null, and arming a stagnation
// criterion is a null, so the wasted budget it was spending was never a
// recoverable gain. What remains is that every one of those numbers was taken
// on eight circles of example/MayFly-512.png. This design repeats the decisive
// contrast on a photographic reference at twelve circles, which is the open
// box of Task 10.
//
// The second is Task 3's, asked of CMA-ES rather than of MayFly. A stage's
// budget can be split three ways and every campaign so far varied only the
// third: warm epochs re-initialize from the incumbent, cold restarts are
// independent attempts scored best-of, and IPOP is the adapter's own ladder
// doubling lambda. sep-e5 and sep-r5 hold the budget fixed and spend it as five
// warm epochs and five cold attempts, and the registered contrast is one
// against the other -- that pair, not either against sep-ipop, is the epoch-
// versus-cold-restart question Task 3 asks. sep-ipop and the unsplit sep-single
// are run alongside as the reference shapes the two split arms are read
// against, but they carry no test of their own.
//
// Only two contrasts are registered, so Holm corrects over the two questions
// rather than the fifteen that six arms would otherwise produce.
func budgetSplitArms(budget int) ([]arm, error) {
	if budget <= 0 || budget%defaultPop != 0 {
		return nil, fmt.Errorf(
			"budget %d must be positive and divisible by lambda %d; the arms would not be evaluation-matched",
			budget, defaultPop)
	}
	// The two Mayfly controls keep Phase 21's fixed shape, whose cost is
	// defaultBudget evaluations, while only the CMA-ES arms derive their length
	// from the budget. A larger one would fund those past anything the Mayfly
	// arms reach and the collector would still print the engine comparison as
	// evaluation-matched. Reject that instead of reporting it.
	if budget > defaultBudget {
		return nil, fmt.Errorf(
			"budget %d exceeds the fixed Mayfly campaign budget %d; the arms would no longer be evaluation-matched",
			budget, defaultBudget)
	}

	iters := budget / defaultPop
	// The split count has to divide the generation count exactly or the split
	// arms would spend a different budget from the unsplit ones and the
	// comparison would be confounded by it. 6350 = 2 * 5^2 * 127, so four -- the
	// ladder's smallest interesting step in docs/restart-vs-budget-report.md --
	// does not divide it and five does.
	const splits = 5
	if iters%splits != 0 {
		return nil, fmt.Errorf(
			"%d generations do not divide into %d equal parts; the split arms would not be evaluation-matched",
			iters, splits)
	}

	cmaes := func(name string, epochs, restarts, perRun int) arm {
		return arm{
			name: name, optimizer: "cmaes", covariance: "separable", restartStrategy: "none",
			iters: perRun, popSize: defaultPop, optimizerRestarts: restarts, optimizerEpochs: epochs,
		}
	}

	return []arm{
		// The MayFly controls keep the shapes Phase 21 ran, so the engine
		// question is asked the same way on the new fixture.
		{name: "mayfly-single", optimizer: "mayfly", iters: 2048, popSize: defaultPop, optimizerRestarts: 1},
		{name: "mayfly-r16", optimizer: "mayfly", iters: 128, popSize: defaultPop, optimizerRestarts: 16},

		cmaes("sep-single", 1, 1, iters),
		// Five warm epochs and five cold attempts each get a fifth of the
		// generations, so all four CMA-ES arms spend the same 6,502,400.
		cmaes("sep-e5", splits, 1, iters/splits),
		cmaes("sep-r5", 1, splits, iters/splits),
		{
			name: "sep-ipop", optimizer: "cmaes", covariance: "separable", restartStrategy: "ipop",
			iters: iters, popSize: defaultPop, optimizerRestarts: 1,
		},
	}, nil
}

// lambdaScreenArms crosses the two covariance modes with four restart-and-
// population shapes. Three of its cells -- cmaes-single, cmaes-ipop and
// sep-cmaes-ipop -- repeat Phase 21 exactly, at the same seeds, so a
// cross-campaign difference shows up as a measured discrepancy instead of
// being assumed away; sep-cmaes-single is the cell Phase 21 never ran, and it
// is the one that separates covariance mode from restart strategy.
func lambdaScreenArms(budget int) ([]arm, error) {
	modes := []struct{ prefix, covariance string }{{"", "full"}, {"sep-", "separable"}}
	levels := lambdaLevels()

	arms := make([]arm, 0, len(modes)*(1+len(levels)))
	for _, mode := range modes {
		arms = append(arms, arm{
			name: mode.prefix + "cmaes-single", optimizer: "cmaes", covariance: mode.covariance,
			restartStrategy: "none", iters: budget / defaultPop, popSize: defaultPop, optimizerRestarts: 1,
		})
		for _, lambda := range levels {
			if budget%lambda != 0 {
				return nil, fmt.Errorf("budget %d is not divisible by lambda %d; the arms would not be evaluation-matched", budget, lambda)
			}

			name := mode.prefix + "cmaes-ipop"
			if lambda != defaultPop {
				name = fmt.Sprintf("%s-l%d", name, lambda)
			}

			arms = append(arms, arm{
				name: name, optimizer: "cmaes", covariance: mode.covariance,
				restartStrategy: "ipop", iters: budget / lambda, popSize: lambda, optimizerRestarts: 1,
			})
		}
	}

	return arms, nil
}

func campaignArms(budget int) ([]arm, error) {
	if budget <= 0 || budget%defaultPop != 0 {
		return nil, fmt.Errorf("budget %d must be positive and divisible by population %d", budget, defaultPop)
	}
	// The two Mayfly arms have a fixed campaign shape -- 2048 iterations,
	// however they are split across restarts -- whose cost is defaultBudget
	// evaluations. Only the CMA-ES arms derive their length from the budget,
	// so a larger one would fund them past anything the Mayfly arms ever
	// reach and the collector would still print the comparison as
	// evaluation-matched. Reject that instead of reporting it.
	if budget > defaultBudget {
		return nil, fmt.Errorf(
			"budget %d exceeds the fixed Mayfly campaign budget %d; the arms would no longer be evaluation-matched",
			budget, defaultBudget,
		)
	}

	return []arm{
		{name: "mayfly-single", optimizer: "mayfly", iters: 2048, popSize: defaultPop, optimizerRestarts: 1},
		{name: "mayfly-r16", optimizer: "mayfly", iters: 128, popSize: defaultPop, optimizerRestarts: 16},
		{name: "cmaes-single", optimizer: "cmaes", covariance: "full", restartStrategy: "none", iters: budget / defaultPop, popSize: defaultPop, optimizerRestarts: 1},
		{name: "cmaes-ipop", optimizer: "cmaes", covariance: "full", restartStrategy: "ipop", iters: budget / defaultPop, popSize: defaultPop, optimizerRestarts: 1},
		{name: "sep-cmaes-ipop", optimizer: "cmaes", covariance: "separable", restartStrategy: "ipop", iters: budget / defaultPop, popSize: defaultPop, optimizerRestarts: 1},
	}, nil
}

// assertDesignShape refuses a -blocks or -seed-base that contradicts the
// registered design. Both are the design's to choose, so the flags are an
// assertion a caller can make rather than a way to alter a campaign: a
// mistyped seed base would silently reuse another campaign's seeds, and a
// mistyped block count would change df after the fact.
func assertDesignShape(config settings, plan design) error {
	if config.blocks != 0 && config.blocks != plan.blocks {
		return fmt.Errorf("design %s registers %d paired blocks, got -blocks %d",
			plan.name, plan.blocks, config.blocks)
	}

	if config.seedBase != 0 && config.seedBase != plan.seedBase {
		return fmt.Errorf("design %s registers seed base %d, got -seed-base %d",
			plan.name, plan.seedBase, config.seedBase)
	}

	return nil
}

func submit(config settings) error {
	plan, err := campaignDesign(config.design, config.budget)
	if err != nil {
		return err
	}

	arms := plan.arms

	if err := assertDesignShape(config, plan); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(config.manifestPath), 0o755); err != nil {
		return fmt.Errorf("create manifest directory: %w", err)
	}

	file, err := os.OpenFile(config.manifestPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create fresh manifest (refusing to duplicate a campaign): %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	if err := writer.Write([]string{"arm", "block", "seed", "jobId"}); err != nil {
		return err
	}

	writer.Flush()

	client := &http.Client{Timeout: 30 * time.Second}

	for block := 1; block <= plan.blocks; block++ {
		seed := plan.seedBase + int64(block)
		for _, current := range arms {
			jobID, submitErr := submitJob(client, config, plan, current, seed)
			if submitErr != nil {
				return fmt.Errorf("submit %s block %d: %w", current.name, block, submitErr)
			}

			record := []string{current.name, strconv.Itoa(block), strconv.FormatInt(seed, 10), jobID}
			if err := writer.Write(record); err != nil {
				return fmt.Errorf("record submitted job: %w", err)
			}

			writer.Flush()

			if err := writer.Error(); err != nil {
				return fmt.Errorf("flush manifest: %w", err)
			}

			fmt.Printf("submitted block %02d %-16s %s\n", block, current.name, jobID)
		}
	}

	return nil
}

func submitJob(client *http.Client, config settings, plan design, current arm, seed int64) (string, error) {
	body, err := json.Marshal(jobPayload(config, plan, current, seed))
	if err != nil {
		return "", err
	}

	request, err := http.NewRequestWithContext(
		context.Background(), http.MethodPost, config.server+"/api/v1/jobs", bytes.NewReader(body),
	)
	if err != nil {
		return "", fmt.Errorf("build create request: %w", err)
	}

	request.Header.Set("Content-Type", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", err
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("server returned %s: %s", response.Status, responseBody)
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(responseBody, &created); err != nil {
		return "", err
	}

	if created.ID == "" {
		return "", errors.New("create response has no job id")
	}

	return created.ID, nil
}

// jobPayload is the create request one arm of one block submits. It is separate
// from the transport so the registered shape of a job -- fixture, budget, and
// the engine-specific fields -- can be read in one place.
func jobPayload(config settings, plan design, current arm, seed int64) map[string]any {
	reference, circles := plan.fixture(config.reference)
	epochs := max(current.optimizerEpochs, 1)

	payload := map[string]any{
		"project": config.project, "refPath": reference,
		"mode": "batch", "backend": "cpu", "optimizer": current.optimizer,
		"circles": circles, "batchSize": circles, "iters": current.iters, "popSize": current.popSize,
		"optimizerEpochs": epochs, "optimizerRestarts": current.optimizerRestarts,
		"seed": seed, "threads": 1, "parallelEvaluation": true,
		"evaluationWorkers": config.workers, "disableConvergence": true,
		"enableTrace": true, "enableOptimizerDiagnostics": true,
	}
	if current.optimizer == "mayfly" {
		payload["variant"] = "standard"
	} else {
		payload["covarianceMode"] = current.covariance
		payload["restartStrategy"] = current.restartStrategy
	}
	// Only send the stop fields when a window is configured. app.JobConfig
	// rejects stopMinImprovement without stopStagnationIters, and an arm that
	// sets neither has to reach the server as the untouched configuration every
	// earlier campaign ran.
	if current.stopStagnationIters > 0 {
		payload["stopStagnationIters"] = current.stopStagnationIters
		if current.stopMinImprovement > 0 {
			payload["stopMinImprovement"] = current.stopMinImprovement
		}
	}

	return payload
}

func collect(config settings) error {
	manifest, err := readManifest(config.manifestPath)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	results := make([]resultRow, 0, len(manifest))
	counts := make(map[string]int)

	for _, record := range manifest {
		status, statusErr := fetchStatus(client, config.server, record.JobID)
		if statusErr != nil {
			return fmt.Errorf("status %s: %w", record.JobID, statusErr)
		}

		counts[status.State]++
		if status.State != "completed" {
			continue
		}

		result, collectErr := collectJob(config, record, status)
		if collectErr != nil {
			return collectErr
		}

		results = append(results, result)
	}

	fmt.Printf("campaign status:")

	for _, state := range []string{"pending", "running", "completed", "failed", "cancelled"} {
		if counts[state] > 0 {
			fmt.Printf(" %s=%d", state, counts[state])
		}
	}

	fmt.Println()

	if len(results) != len(manifest) {
		return fmt.Errorf("campaign is not complete: collected %d of %d jobs", len(results), len(manifest))
	}

	if err := writeResults(config.resultsPath, results); err != nil {
		return err
	}

	if err := writeTrajectories(config, manifest); err != nil {
		return err
	}

	if err := writeRestarts(config, results); err != nil {
		return err
	}

	return analyzeDesign(config)
}

// printPlan reports exactly what submit would create, without creating it.
// A campaign is expensive and a manifest may only be written once, so the arm
// shapes are worth reading before 12 blocks of them are queued.
func printPlan(config settings) error {
	plan, err := campaignDesign(config.design, config.budget)
	if err != nil {
		return err
	}

	if err := assertDesignShape(config, plan); err != nil {
		return err
	}

	reference, circles := plan.fixture(config.reference)

	fmt.Printf("design %s: %d arms x %d blocks = %d jobs, budget %d evaluations, seeds %d..%d\n",
		plan.name, len(plan.arms), plan.blocks, len(plan.arms)*plan.blocks, config.budget,
		plan.seedBase+1, plan.seedBase+int64(plan.blocks))
	fmt.Printf("fixture %s, %d circles in one batch\n", reference, circles)
	fmt.Println("| arm | optimizer | covariance | restarts | epochs | popSize (lambda) | iters | stagnation | evaluations |")
	fmt.Println("| --- | --- | --- | --- | ---: | ---: | ---: | --- | ---: |")

	for _, current := range plan.arms {
		covariance, restarts := current.covariance, current.restartStrategy
		// Only CMA-ES spends exactly lambda evaluations per iteration, so only
		// there is iters*popSize the evaluation count. Mayfly evaluates its
		// population several times per iteration, and its arms are matched to
		// the budget by campaign shape rather than by that product; printing it
		// as an evaluation count would invite the comparison the campaign
		// deliberately makes post-hoc from the trace.
		// Epochs and cold restarts each multiply the generation count, so an
		// arm's budget is the product of all three. Printing one run's share
		// would make a split arm look like it spends a fifth of the cap.
		evaluations := strconv.FormatInt(
			int64(current.iters)*int64(current.popSize)*
				int64(max(current.optimizerEpochs, 1))*int64(max(current.optimizerRestarts, 1)), 10)
		if current.optimizer == "mayfly" {
			covariance = "-"
			restarts = fmt.Sprintf("%d cold run(s)", current.optimizerRestarts)
			evaluations = "scored from trace"
		} else if current.optimizerRestarts > 1 {
			// A CMA-ES arm can carry both the adapter's own ladder and the
			// generic cold-restart wrapper; naming only the strategy would hide
			// the second.
			restarts = fmt.Sprintf("%s + %d cold run(s)", restarts, current.optimizerRestarts)
		}

		fmt.Printf("| `%s` | %s | %s | %s | %d | %d | %d | %s | %s |\n",
			current.name, current.optimizer, covariance, restarts, max(current.optimizerEpochs, 1),
			current.popSize, current.iters, describeStagnation(current), evaluations)
	}

	if plan.secondaryControl != "" {
		fmt.Printf("\nbaseline `%s`, secondary control `%s`\n", plan.baseline, plan.secondaryControl)
	} else {
		fmt.Printf("\nbaseline `%s`\n", plan.baseline)
	}

	if plan.descriptive {
		fmt.Println("descriptive design: mechanism only, too few blocks for a paired test")
	}

	for _, current := range plan.contrasts {
		role := "registered"
		if current.primary {
			role = "PRIMARY"
		}

		fmt.Printf("%s contrast: `%s` against `%s`\n", role, current.candidate, current.control)
	}

	return nil
}

// describeStagnation renders an arm's stopping configuration for the plan
// table. "none" is the configuration every campaign before this one ran, and
// naming it explicitly is the point of the column.
func describeStagnation(current arm) string {
	if current.stopStagnationIters == 0 {
		return "none"
	}

	if current.stopMinImprovement > 0 {
		return fmt.Sprintf("%d iters, min %.3g", current.stopStagnationIters, current.stopMinImprovement)
	}

	return fmt.Sprintf("%d iters", current.stopStagnationIters)
}

// analyzeDesign resolves the named design before reporting, so the arm set,
// the controls and the expected job count all come from one place.
func analyzeDesign(config settings) error {
	plan, err := campaignDesign(config.design, config.budget)
	if err != nil {
		return err
	}

	return analyze(config.resultsPath, plan)
}

func collectPreliminary(config settings) error {
	manifest, err := readManifest(config.manifestPath)
	if err != nil {
		return err
	}

	results := make([]resultRow, 0, len(manifest))

	available := make([]manifestRow, 0, len(manifest))
	for _, record := range manifest {
		jobDir := filepath.Join(config.dataRoot, "projects", config.project, "jobs", record.JobID)

		body, readErr := os.ReadFile(filepath.Join(jobDir, "checkpoint-info.json"))
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}

		if readErr != nil {
			return fmt.Errorf("read %s checkpoint info: %w", record.JobID, readErr)
		}

		var saved checkpointInfo
		if err := json.Unmarshal(body, &saved); err != nil {
			return fmt.Errorf("decode %s checkpoint info: %w", record.JobID, err)
		}

		state := "interrupted"
		if saved.Termination == "completed" {
			state = "completed"
		}

		status := jobStatus{
			State: state, Termination: saved.Termination,
			Iterations: saved.Iteration, Evaluations: saved.Evaluations,
		}

		result, collectErr := collectJob(config, record, status)
		if collectErr != nil {
			return collectErr
		}

		result.OptimizerVersion = saved.OptimizerVersion
		results = append(results, result)
		available = append(available, record)
	}

	if len(results) == 0 {
		return errors.New("campaign has no persisted job results")
	}

	if err := writeResults(config.resultsPath, results); err != nil {
		return err
	}

	if err := writeTrajectories(config, available); err != nil {
		return err
	}

	fmt.Printf("wrote %d preliminary results from %d planned jobs; no inferential statistics were calculated\n", len(results), len(manifest))

	return nil
}

func fetchStatus(client *http.Client, server, jobID string) (jobStatus, error) {
	request, err := http.NewRequestWithContext(
		context.Background(), http.MethodGet, server+"/api/v1/jobs/"+jobID+"/status", nil,
	)
	if err != nil {
		return jobStatus{}, fmt.Errorf("build status request: %w", err)
	}

	response, err := client.Do(request)
	if err != nil {
		return jobStatus{}, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return jobStatus{}, fmt.Errorf("server returned %s: %s", response.Status, body)
	}

	var status jobStatus
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&status); err != nil {
		return jobStatus{}, err
	}

	return status, nil
}

func collectJob(config settings, record manifestRow, status jobStatus) (resultRow, error) {
	jobDir := filepath.Join(config.dataRoot, "projects", config.project, "jobs", record.JobID)

	trace, err := readTrace(filepath.Join(jobDir, "trace.jsonl"))
	if err != nil {
		return resultRow{}, fmt.Errorf("read %s trace: %w", record.JobID, err)
	}

	score := math.Inf(1)
	scoredEvaluations := 0

	for _, entry := range trace {
		if entry.Evaluations <= config.budget && entry.Cost < score {
			score = entry.Cost
			scoredEvaluations = entry.Evaluations
		}
	}

	if math.IsInf(score, 1) {
		return resultRow{}, fmt.Errorf("job %s has no trace sample within budget", record.JobID)
	}

	if status.Elapsed == 0 && len(trace) > 1 {
		status.Elapsed = trace[len(trace)-1].Timestamp.Sub(trace[0].Timestamp).Seconds()
	}

	var saved checkpoint

	checkpointBody, err := os.ReadFile(filepath.Join(jobDir, "checkpoint.json"))
	if err != nil {
		return resultRow{}, fmt.Errorf("read %s checkpoint: %w", record.JobID, err)
	}

	if err := json.Unmarshal(checkpointBody, &saved); err != nil {
		return resultRow{}, fmt.Errorf("decode %s checkpoint: %w", record.JobID, err)
	}

	return resultRow{
		manifestRow: record, State: status.State, Termination: status.Termination,
		OptimizerVersion: saved.OptimizerVersion, Score: score,
		ScoredEvaluations: scoredEvaluations, FinalEvaluations: status.Evaluations,
		Iterations: status.Iterations, ElapsedSeconds: status.Elapsed,
		Restarts: saved.Restarts,
	}, nil
}

func readTrace(path string) ([]store.TraceEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var entries []store.TraceEntry
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)

	for scanner.Scan() {
		var entry store.TraceEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			return nil, err
		}

		entries = append(entries, entry)
	}

	return entries, scanner.Err()
}

func readManifest(path string) ([]manifestRow, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)

	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	if len(records) < 2 || !slices.Equal(records[0], []string{"arm", "block", "seed", "jobId"}) {
		return nil, errors.New("manifest has an unexpected header or no jobs")
	}

	rows := make([]manifestRow, 0, len(records)-1)
	for _, record := range records[1:] {
		if len(record) != 4 {
			return nil, fmt.Errorf("invalid manifest row %q", record)
		}

		block, blockErr := strconv.Atoi(record[1])

		seed, seedErr := strconv.ParseInt(record[2], 10, 64)
		if blockErr != nil || seedErr != nil {
			return nil, fmt.Errorf("invalid manifest row %q", record)
		}

		rows = append(rows, manifestRow{Arm: record[0], Block: block, Seed: seed, JobID: record[3]})
	}

	return rows, nil
}

func writeResults(path string, results []resultRow) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)

	header := []string{"arm", "block", "seed", "jobId", "state", "termination", "optimizerVersion", "bestCost", "scoredEvaluations", "finalEvaluations", "iterations", "elapsedSeconds"}
	if err := writer.Write(header); err != nil {
		return err
	}

	for _, result := range results {
		record := []string{
			result.Arm, strconv.Itoa(result.Block), strconv.FormatInt(result.Seed, 10), result.JobID,
			result.State, result.Termination, result.OptimizerVersion,
			strconv.FormatFloat(result.Score, 'g', 17, 64), strconv.Itoa(result.ScoredEvaluations),
			strconv.Itoa(result.FinalEvaluations), strconv.Itoa(result.Iterations),
			strconv.FormatFloat(result.ElapsedSeconds, 'f', 6, 64),
		}
		if err := writer.Write(record); err != nil {
			return err
		}
	}

	writer.Flush()

	return writer.Error()
}

func writeTrajectories(config settings, manifest []manifestRow) error {
	if err := os.MkdirAll(filepath.Dir(config.trajectory), 0o755); err != nil {
		return err
	}

	file, err := os.Create(config.trajectory)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	if err := writer.Write([]string{"arm", "block", "seed", "iteration", "evaluations", "bestCost", "populationSpread", "sigma", "conditionNumber", "distributionExtent", "restart"}); err != nil {
		return err
	}

	early := map[int]bool{1: true, 5: true, 10: true, 20: true, 40: true, 80: true, 160: true, 255: true}

	for _, record := range manifest {
		jobDir := filepath.Join(config.dataRoot, "projects", config.project, "jobs", record.JobID)

		entries, readErr := readTrace(filepath.Join(jobDir, "trace.jsonl"))
		if readErr != nil {
			return readErr
		}

		lastEligible := -1

		for index, entry := range entries {
			if entry.OptimizerDiagnostics != nil && entry.Evaluations <= config.budget {
				lastEligible = index
			}
		}

		lastBucket := -1

		for index, entry := range entries {
			if entry.OptimizerDiagnostics == nil || entry.Evaluations > config.budget {
				continue
			}

			bucket := entry.Evaluations * 256 / config.budget

			isLast := index == lastEligible
			if !isLast && !early[entry.Iteration] && bucket == lastBucket {
				continue
			}

			lastBucket = bucket
			diagnostic := entry.OptimizerDiagnostics
			populationSpread, sigma, conditionNumber, extent := formatDiagnostics(diagnostic)

			record := []string{
				record.Arm, strconv.Itoa(record.Block), strconv.FormatInt(record.Seed, 10),
				strconv.Itoa(entry.Iteration), strconv.Itoa(entry.Evaluations),
				strconv.FormatFloat(entry.Cost, 'g', 17, 64),
				populationSpread, sigma, conditionNumber, extent,
				strconv.Itoa(diagnostic.Restart),
			}
			if err := writer.Write(record); err != nil {
				return err
			}
		}
	}

	writer.Flush()

	return writer.Error()
}

// formatDiagnostics splits one trace entry's optimizer diagnostics into the
// Mayfly column and the three CMA-ES columns, leaving the other engine's cells
// empty. distributionExtent stays empty for a trace written before the adapter
// recorded it, which is every trace behind docs/cmaes-report.md; an empty cell
// says "not measured" where a zero would say "the distribution had no width".
// writeRestarts records one row per independent run of every restart schedule
// in the campaign. It is the only place a restart arm's per-run outcome
// survives: the job-level termination a restart schedule reports is its
// budget-exhausted reason whenever the shared budget is spent, which for an
// arm sized to consume its budget is always, so every restart arm records
// "completed" however its individual runs actually ended.
//
// The file is written even when it holds nothing but a header. A campaign
// whose checkpoints predate the adapter recording these produces exactly that,
// and an empty file says "this campaign has no per-run record" where a missing
// one would be indistinguishable from a collection that failed.
func writeRestarts(config settings, results []resultRow) error {
	if err := os.MkdirAll(filepath.Dir(config.restartsPath), 0o755); err != nil {
		return err
	}

	file, err := os.Create(config.restartsPath)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)

	header := []string{
		"arm", "block", "seed", "stage", "restart", "regime",
		"population", "iterations", "evaluations", "bestCost", "termination",
	}
	if err := writer.Write(header); err != nil {
		return err
	}

	for _, result := range results {
		for _, run := range result.Restarts {
			record := []string{
				result.Arm, strconv.Itoa(result.Block), strconv.FormatInt(result.Seed, 10),
				strconv.Itoa(run.Stage), strconv.Itoa(run.Restart), run.Regime,
				strconv.Itoa(run.Population), strconv.Itoa(run.Iterations),
				strconv.Itoa(run.Evaluations),
				strconv.FormatFloat(run.BestCost, 'g', 17, 64),
				run.Termination,
			}
			if err := writer.Write(record); err != nil {
				return err
			}
		}
	}

	writer.Flush()

	return writer.Error()
}

func formatDiagnostics(diagnostic *opt.SearchDiagnostics) (string, string, string, string) {
	if diagnostic == nil {
		return "", "", "", ""
	}

	if diagnostic.Sigma == 0 && diagnostic.ConditionNumber == 0 {
		return strconv.FormatFloat(diagnostic.PopulationSpread, 'g', 17, 64), "", "", ""
	}

	extent := ""
	if diagnostic.DistributionExtent != 0 {
		extent = strconv.FormatFloat(diagnostic.DistributionExtent, 'g', 17, 64)
	}

	return "",
		strconv.FormatFloat(diagnostic.Sigma, 'g', 17, 64),
		strconv.FormatFloat(diagnostic.ConditionNumber, 'g', 17, 64),
		extent
}

// familyAlpha is the family-wise error rate a design's paired contrasts are
// held to together. Holm's step-down procedure spends it across
// the whole family, so a contrast that clears the uncorrected 0.05 can still
// retain its null here.
const familyAlpha = 0.05

// contrast is one paired comparison of a candidate arm against a control arm,
// carried far enough to be corrected for multiplicity before it is printed.
type contrast struct {
	control   string
	candidate string
	gain      float64
	statistic float64
	pValue    float64
	wins      int
	rejected  bool
}

func analyze(path string, plan design) error {
	rows, err := readResults(path, len(plan.arms)*plan.blocks)
	if err != nil {
		return err
	}

	byArm := make(map[string][]resultRow)
	for _, row := range rows {
		byArm[row.Arm] = append(byArm[row.Arm], row)
	}

	names := make([]string, 0, len(plan.arms))
	for _, current := range plan.arms {
		rows := byArm[current.name]
		if len(rows) != plan.blocks {
			return fmt.Errorf("arm %s has %d blocks, want %d", current.name, len(rows), plan.blocks)
		}

		slices.SortFunc(rows, func(a, b resultRow) int { return a.Block - b.Block })

		names = append(names, current.name)
	}

	if plan.descriptive {
		return reportDescriptive(plan, names, byArm)
	}

	contrasts := buildContrasts(plan, byArm)
	holmReject(contrasts, familyAlpha)

	fmt.Printf("| arm | mean | sd | median | best | gain vs %s | t (df=%d) | p | Holm | blocks won |\n",
		plan.baseline, plan.blocks-1)
	fmt.Println("| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- | ---: |")

	for _, name := range names {
		values := costs(byArm[name])
		mean, sd := meanSD(values)

		summary := "control | control | control | control | control"
		if name != plan.baseline {
			summary = summarize(contrasts, plan.baseline, name, plan.blocks)
		}

		fmt.Printf("| `%s` | %.2f | %.2f | %.2f | %.2f | %s |\n",
			name, mean, sd, median(values), slices.Min(values), summary)
	}

	fmt.Printf("\nAgainst %s:\n", plan.secondaryControl)
	fmt.Printf("| arm | gain vs %s | t (df=%d) | p | Holm | blocks won |\n",
		plan.secondaryControl, plan.blocks-1)
	fmt.Println("| --- | ---: | ---: | ---: | --- | ---: |")

	for _, name := range names {
		if name == plan.baseline || name == plan.secondaryControl {
			continue
		}

		fmt.Printf("| `%s` | %s |\n", name, summarize(contrasts, plan.secondaryControl, name, plan.blocks))
	}

	fmt.Printf("\nHolm step-down over all %d paired contrasts at a family-wise alpha of %.2f;\n",
		len(contrasts), familyAlpha)
	fmt.Printf("the uncorrected two-sided threshold at df=%d is t=%.2f and the Bonferroni one is t=%.2f.\n",
		plan.blocks-1, studentTCritical(familyAlpha, plan.blocks-1),
		studentTCritical(familyAlpha/float64(len(contrasts)), plan.blocks-1))

	if primary, ok := plan.primaryContrast(); ok {
		fmt.Printf("the registered primary contrast is `%s` against `%s`.\n",
			primary.candidate, primary.control)
	}

	return nil
}

// reportDescriptive is the output for a design that buys mechanism instead of
// inference. It prints what each arm cost and what its budget did, and no
// statistic at all: three blocks cannot support a paired test, and printing a
// t on them would invite exactly the reading the design was built to avoid.
func reportDescriptive(plan design, names []string, byArm map[string][]resultRow) error {
	fmt.Printf("design %s: descriptive only, %d blocks, no inferential statistics\n\n",
		plan.name, plan.blocks)
	fmt.Println("| arm | mean | best | worst | mean evaluations | % of cap | % spent after last improvement |")
	fmt.Println("| --- | ---: | ---: | ---: | ---: | ---: | ---: |")

	capacity := plan.evaluationCap()

	for _, name := range names {
		rows := byArm[name]
		values := costs(rows)
		mean, _ := meanSD(values)

		// Both shares are ratios of totals rather than means of per-job
		// ratios: the jobs of one arm do not all spend the same number of
		// evaluations, so averaging their fractions would weight a short run
		// exactly like a long one and misstate the budget share the pilot
		// exists to measure.
		var final, wasted float64
		for _, row := range rows {
			final += float64(row.FinalEvaluations)
			wasted += float64(row.FinalEvaluations - row.ScoredEvaluations)
		}

		var share float64
		if final > 0 {
			share = wasted / final * 100
		}

		final /= float64(len(rows))

		fmt.Printf("| `%s` | %.2f | %.2f | %.2f | %.0f | %.1f%% | %.1f%% |\n",
			name, mean, slices.Min(values), slices.Max(values),
			final, final/float64(capacity)*100, share)
	}

	fmt.Println("\nRestart counts and per-run termination reasons are in the -restarts CSV;")
	fmt.Println("they, not the costs above, are what selects the window for the registered campaign.")

	return nil
}

// evaluationCap is the per-job evaluation budget this design's arms were built
// from, read back off the arms rather than assumed from defaultBudget: a
// campaign submitted with a smaller -budget would otherwise report its spend
// as a share of a cap it never had. Only CMA-ES spends exactly lambda
// evaluations per iteration, so only there is iters*popSize the cap; a Mayfly
// arm is matched to the fixed campaign budget by shape instead.
func (d design) evaluationCap() int {
	highest := 0

	for _, current := range d.arms {
		spend := defaultBudget
		if current.optimizer != "mayfly" {
			spend = current.iters * current.popSize
		}

		if spend > highest {
			highest = spend
		}
	}

	if highest == 0 {
		return defaultBudget
	}

	return highest
}

// buildContrasts evaluates the family the design registered. All of them are
// one family and are corrected together, so the number of contrasts a design
// declares is the multiplicity it pays for -- which is why declaring them is
// now the design's job rather than a consequence of how many arms it happens
// to run.
func buildContrasts(plan design, byArm map[string][]resultRow) []contrast {
	contrasts := make([]contrast, 0, len(plan.contrasts))
	for _, planned := range plan.contrasts {
		gain, statistic, wins := pairedImprovement(byArm[planned.control], byArm[planned.candidate])
		contrasts = append(contrasts, contrast{
			control:   planned.control,
			candidate: planned.candidate,
			gain:      gain,
			statistic: statistic,
			pValue:    studentTTwoSided(statistic, plan.blocks-1),
			wins:      wins,
		})
	}

	return contrasts
}

// summarize renders one contrast's cells for the Markdown tables above.
func summarize(contrasts []contrast, control, candidate string, blocks int) string {
	for _, current := range contrasts {
		if current.control != control || current.candidate != candidate {
			continue
		}

		decision := "retain"
		if current.rejected {
			decision = "reject"
		}

		return fmt.Sprintf("%+.2f | %+.2f | %.5f | %s | %d/%d",
			current.gain, current.statistic, current.pValue, decision, current.wins, blocks)
	}

	return "n/a | n/a | n/a | n/a | n/a"
}

// holmReject marks the contrasts whose null hypotheses Holm's step-down
// procedure rejects at the given family-wise alpha. It stops at the first
// contrast that fails its threshold, so every larger p-value retains too.
func holmReject(contrasts []contrast, alpha float64) {
	order := make([]int, len(contrasts))
	for index := range order {
		order[index] = index
	}

	slices.SortFunc(order, func(a, b int) int {
		return cmp.Compare(contrasts[a].pValue, contrasts[b].pValue)
	})

	for rank, index := range order {
		if contrasts[index].pValue >= alpha/float64(len(contrasts)-rank) {
			return
		}

		contrasts[index].rejected = true
	}
}

// studentTTwoSided returns the two-sided p-value of a t statistic on the given
// degrees of freedom. An infinite statistic comes from a zero-variance paired
// difference and is reported as p=0.
func studentTTwoSided(statistic float64, degrees int) float64 {
	if math.IsInf(statistic, 0) {
		return 0
	}

	freedom := float64(degrees)

	return regularizedIncompleteBeta(freedom/(freedom+statistic*statistic), freedom/2, 0.5)
}

// studentTCritical inverts studentTTwoSided by bisection, returning the
// two-sided critical t for the given alpha and degrees of freedom.
func studentTCritical(alpha float64, degrees int) float64 {
	low, high := 0.0, 1e3
	for range 200 {
		middle := (low + high) / 2
		if studentTTwoSided(middle, degrees) > alpha {
			low = middle
		} else {
			high = middle
		}
	}

	return (low + high) / 2
}

// regularizedIncompleteBeta evaluates I_x(a, b) by the continued fraction of
// Numerical Recipes section 6.4, switching branches where it converges fastest.
func regularizedIncompleteBeta(x, a, b float64) float64 {
	switch {
	case x <= 0:
		return 0
	case x >= 1:
		return 1
	}

	logA, _ := math.Lgamma(a)
	logB, _ := math.Lgamma(b)
	logSum, _ := math.Lgamma(a + b)

	front := math.Exp(logSum - logA - logB + a*math.Log(x) + b*math.Log1p(-x))
	if x < (a+1)/(a+b+2) {
		return front * betaContinuedFraction(x, a, b) / a
	}

	return 1 - front*betaContinuedFraction(1-x, b, a)/b
}

// betaContinuedFraction evaluates the continued fraction of the incomplete beta
// function by the modified Lentz algorithm.
func betaContinuedFraction(x, a, b float64) float64 {
	const (
		maxIterations = 300
		epsilon       = 1e-15
		tiny          = 1e-300
	)

	guard := func(value float64) float64 {
		if math.Abs(value) < tiny {
			return tiny
		}

		return value
	}

	sum, plus, minus := a+b, a+1, a-1
	numerator := 1.0
	denominator := 1 / guard(1-sum*x/plus)
	fraction := denominator

	for step := 1; step <= maxIterations; step++ {
		index := float64(step)
		doubled := 2 * index

		term := index * (b - index) * x / ((minus + doubled) * (a + doubled))
		denominator = 1 / guard(1+term*denominator)
		numerator = guard(1 + term/numerator)
		fraction *= denominator * numerator

		term = -(a + index) * (sum + index) * x / ((a + doubled) * (plus + doubled))
		denominator = 1 / guard(1+term*denominator)
		numerator = guard(1 + term/numerator)
		delta := denominator * numerator
		fraction *= delta

		if math.Abs(delta-1) < epsilon {
			break
		}
	}

	return fraction
}

func readResults(path string, wantJobs int) ([]resultRow, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)

	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	if len(records) != wantJobs+1 {
		return nil, fmt.Errorf("results contain %d jobs, want %d", len(records)-1, wantJobs)
	}

	rows := make([]resultRow, 0, wantJobs)

	for index, record := range records[1:] {
		line := index + 2
		if len(record) != resultColumns {
			return nil, fmt.Errorf("results line %d has %d columns, want %d", line, len(record), resultColumns)
		}

		parser := rowParser{record: record, line: line}

		row := resultRow{
			manifestRow: manifestRow{
				Arm: record[0], Block: parser.integer(1), Seed: parser.integer64(2), JobID: record[3],
			},
			State: record[4], Termination: record[5], OptimizerVersion: record[6],
			Score:             parser.float(7),
			ScoredEvaluations: parser.integer(8),
			FinalEvaluations:  parser.integer(9),
			Iterations:        parser.integer(10),
			ElapsedSeconds:    parser.float(11),
		}
		if parser.err != nil {
			return nil, parser.err
		}

		rows = append(rows, row)
	}

	return rows, nil
}

// rowParser reads the numeric columns of one result record and keeps the first
// failure. Discarding the strconv errors instead would let a hand-edited or
// truncated CSV parse as zeros and be reported as a statistic, which is the
// one failure mode a measurement collector must not have.
type rowParser struct {
	record []string
	err    error
	line   int
}

func (p *rowParser) fail(column int, err error) {
	if p.err == nil {
		p.err = fmt.Errorf("results line %d column %d (%q): %w", p.line, column+1, p.record[column], err)
	}
}

func (p *rowParser) integer(column int) int {
	value, err := strconv.Atoi(p.record[column])
	if err != nil {
		p.fail(column, err)
	}

	return value
}

func (p *rowParser) integer64(column int) int64 {
	value, err := strconv.ParseInt(p.record[column], 10, 64)
	if err != nil {
		p.fail(column, err)
	}

	return value
}

func (p *rowParser) float(column int) float64 {
	value, err := strconv.ParseFloat(p.record[column], 64)
	if err != nil {
		p.fail(column, err)
	}

	return value
}

func costs(rows []resultRow) []float64 {
	values := make([]float64, len(rows))
	for index, row := range rows {
		values[index] = row.Score
	}

	return values
}

func meanSD(values []float64) (float64, float64) {
	mean := 0.0
	for _, value := range values {
		mean += value
	}

	mean /= float64(len(values))

	variance := 0.0

	for _, value := range values {
		delta := value - mean
		variance += delta * delta
	}

	variance /= float64(len(values) - 1)

	return mean, math.Sqrt(variance)
}

func median(values []float64) float64 {
	ordered := append([]float64(nil), values...)
	slices.Sort(ordered)
	middle := len(ordered) / 2

	return (ordered[middle-1] + ordered[middle]) / 2
}

func pairedImprovement(control, candidate []resultRow) (float64, float64, int) {
	controlByBlock := make(map[int]float64, len(control))
	for _, row := range control {
		controlByBlock[row.Block] = row.Score
	}

	differences := make([]float64, 0, len(candidate))
	wins := 0

	for _, row := range candidate {
		difference := controlByBlock[row.Block] - row.Score

		differences = append(differences, difference)
		if difference > 0 {
			wins++
		}
	}

	mean, sd := meanSD(differences)
	if sd == 0 {
		return mean, math.Copysign(math.Inf(1), mean), wins
	}

	return mean, mean / (sd / math.Sqrt(float64(len(differences)))), wins
}
