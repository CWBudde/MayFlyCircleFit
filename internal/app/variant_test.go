package app

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// TestValidateAcceptsEveryVariant pins that every exported variant constant is
// reachable through validation. The two sets were allowed to drift once
// already: internal/opt could construct seven variants while Validate accepted
// three, so four were unreachable from the CLI, the server, and schedule
// documents.
func TestValidateAcceptsEveryVariant(t *testing.T) {
	for _, variant := range SupportedVariants() {
		t.Run(string(variant), func(t *testing.T) {
			config := DefaultConfig()
			config.RefPath = "reference.png"
			config.Variant = variant

			normalized, err := Normalize(config)
			if err != nil {
				t.Fatalf("Normalize rejected variant %q: %v", variant, err)
			}

			if normalized.Variant != variant {
				t.Fatalf("variant = %q, want %q", normalized.Variant, variant)
			}
		})
	}
}

func TestSupportedVariantsListsEveryConstantOnce(t *testing.T) {
	want := []Variant{
		VariantStandard,
		VariantDESMA,
		VariantOLCE,
		VariantEOBBMA,
		VariantGSASMA,
		VariantMPMA,
		VariantAOBLMOA,
	}
	if got := SupportedVariants(); !slices.Equal(got, want) {
		t.Fatalf("SupportedVariants() = %v, want %v", got, want)
	}
}

func TestValidateRejectsUnknownVariantAndNamesTheAcceptedOnes(t *testing.T) {
	config := DefaultConfig()
	config.RefPath = "reference.png"
	config.Variant = Variant("desna")

	err := config.Validate()
	if err == nil {
		t.Fatal("Validate accepted an unknown variant")
	}

	var validation *ValidationError
	if !errors.As(err, &validation) || validation.Field != "variant" {
		t.Fatalf("Validate() error = %v, want a ValidationError on field variant", err)
	}

	for _, variant := range SupportedVariants() {
		if !strings.Contains(validation.Reason, string(variant)) {
			t.Fatalf("error reason %q does not name the accepted variant %q", validation.Reason, variant)
		}
	}
}
