package cmd

import (
	"runtime"
	"strconv"
	"testing"
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
		"polishing-max-sweeps":       "3",
		"polishing-epochs":           "2",
		"polishing-iters":            "1000",
		"polishing-stagnation-iters": "500",
		"polishing-min-improvement":  "0.001",
	}
	for name, want := range tests {
		flag := runCmd.Flags().Lookup(name)
		if flag == nil || flag.DefValue != want {
			t.Errorf("--%s = %#v, want default %q", name, flag, want)
		}
	}
}
