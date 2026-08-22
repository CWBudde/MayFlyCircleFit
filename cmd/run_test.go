package cmd

import (
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/app"
)

func TestRunThreadsFlagDefaultsToGOMAXPROCS(t *testing.T) {
	flag := runCmd.Flags().Lookup("threads")
	if flag == nil {
		t.Fatal("run command has no --threads flag")
	}

	if want := strconv.Itoa(runtime.GOMAXPROCS(0)); flag.DefValue != want {
		t.Fatalf("--threads default = %q, want %q", flag.DefValue, want)
	}
}

// TestRunEarlyStopFlagsDefaultToDisabled pins that optimizer-level stopping is
// opt-in, and that the stage-level flags keep their own distinct defaults. The
// two mechanisms are separate on purpose and must not be unified.
func TestRunEarlyStopFlagsDefaultToDisabled(t *testing.T) {
	tests := map[string]string{
		"stop-target-cost":      "0",
		"stop-min-improvement":  "0",
		"stop-stagnation-iters": "0",
		"stop-min-iters":        "0",
		"patience":              "3",
		"threshold":             "0.001",
	}

	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			flag := runCmd.Flags().Lookup(name)
			if flag == nil {
				t.Fatalf("run command has no --%s flag", name)
			}

			if flag.DefValue != want {
				t.Fatalf("--%s default = %q, want %q", name, flag.DefValue, want)
			}
		})
	}
}

func TestRunVariantFlagDefaultsToStandard(t *testing.T) {
	flag := runCmd.Flags().Lookup("variant")
	if flag == nil {
		t.Fatal("run command has no --variant flag")
	}

	if flag.DefValue != "standard" {
		t.Fatalf("--variant default = %q, want %q", flag.DefValue, "standard")
	}
}

func TestRunBackendFlagDefaultsToCPU(t *testing.T) {
	flag := runCmd.Flags().Lookup("backend")
	if flag == nil {
		t.Fatal("run command has no --backend flag")
	}

	if flag.DefValue != "cpu" {
		t.Fatalf("--backend default = %q, want cpu", flag.DefValue)
	}

	if !strings.Contains(flag.Usage, "gpu") {
		t.Fatalf("--backend usage = %q, want alias help", flag.Usage)
	}
}

func TestRunSSIMFlagDefaultsToDisabled(t *testing.T) {
	flag := runCmd.Flags().Lookup("enable-ssim")
	if flag == nil {
		t.Fatal("run command has no --enable-ssim flag")
	}

	if flag.DefValue != "false" {
		t.Fatalf("--enable-ssim default = %q, want false", flag.DefValue)
	}
}

func TestRunBatchSizeFlagDefaultsToAutomatic(t *testing.T) {
	flag := runCmd.Flags().Lookup("batch-size")
	if flag == nil || flag.DefValue != "0" {
		t.Fatalf("batch-size flag = %#v, want default 0", flag)
	}
}

func TestRunPolishingFlagDefaults(t *testing.T) {
	tests := map[string]string{
		"polishing":                  "false",
		"polishing-strategy":         "replacement",
		"polishing-active-set-size":  "5",
		"polishing-max-sweeps":       strconv.Itoa(app.DefaultPolishingMaxSweeps),
		"polishing-epochs":           strconv.Itoa(app.DefaultPolishingEpochs),
		"polishing-iters":            strconv.Itoa(app.DefaultPolishingIters),
		"polishing-pop":              strconv.Itoa(app.DefaultPolishingPopSize),
		"polishing-stagnation-iters": strconv.Itoa(app.DefaultPolishingStagnationIters),
		"polishing-min-improvement":  "0.001",
	}
	for name, want := range tests {
		flag := runCmd.Flags().Lookup(name)
		if flag == nil || flag.DefValue != want {
			t.Errorf("--%s = %#v, want default %q", name, flag, want)
		}
	}
}

// TestRunPolishingPopulationFlagMatchesConfigDefault keeps the flag and the
// configuration default from drifting apart. --polishing-pop is the only way to
// see the population a polish runs at from the command line, so a flag default
// that disagreed with DefaultConfig would misreport every unconfigured run.
func TestRunPolishingPopulationFlagMatchesConfigDefault(t *testing.T) {
	flag := runCmd.Flags().Lookup("polishing-pop")
	if flag == nil {
		t.Fatal("--polishing-pop is not registered")
	}

	if want := strconv.Itoa(app.DefaultConfig().PolishingPopSize); flag.DefValue != want {
		t.Errorf("--polishing-pop default = %q, want %q", flag.DefValue, want)
	}

	if app.DefaultConfig().PolishingPopSize == app.DefaultConfig().PopSize {
		return
	}
	// The two differ deliberately: polishing sizes an active set, not a vector.
	if flag.DefValue == strconv.Itoa(app.DefaultConfig().PopSize) {
		t.Errorf("--polishing-pop default = %q, which is --pop rather than the polishing default", flag.DefValue)
	}
}
