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

func TestRunVariantFlagDefaultsToStandard(t *testing.T) {
	flag := runCmd.Flags().Lookup("variant")
	if flag == nil {
		t.Fatal("run command has no --variant flag")
	}
	if flag.DefValue != "standard" {
		t.Fatalf("--variant default = %q, want %q", flag.DefValue, "standard")
	}
}
