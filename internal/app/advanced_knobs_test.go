package app_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/app"
)

const advancedRefPath = "reference.png"

func floatPtr(v float64) *float64 { return &v }

// The three advanced knobs share a range, so one table covers all of them.
func TestAdvancedKnobRanges(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name  string
		value *float64
		valid bool
	}{
		{"unset", nil, true},
		{"zero is a real setting", floatPtr(0), true},
		{"mid range", floatPtr(0.5), true},
		{"one", floatPtr(1), true},
		{"above one", floatPtr(1.5), false},
		{"negative", floatPtr(-0.1), false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			// danceDamp applies to every variant; the other two are checked
			// against aoblmoa so the variant guard does not mask the range.
			for field, config := range map[string]app.JobConfig{
				"danceDamp": {RefPath: advancedRefPath, DanceDamp: testCase.value},
				"aquilaWeight": {
					RefPath: advancedRefPath, Variant: app.VariantAOBLMOA,
					AquilaWeight: testCase.value,
				},
				"oppositionProbability": {
					RefPath: advancedRefPath, Variant: app.VariantAOBLMOA,
					OppositionProbability: testCase.value,
				},
			} {
				_, err := app.Normalize(config)

				if testCase.valid && err != nil {
					t.Fatalf("%s: Normalize() = %v, want nil", field, err)
				}

				if !testCase.valid && err == nil {
					t.Fatalf("%s: Normalize() = nil, want an error", field)
				}
			}
		})
	}
}

// A knob that silently does nothing is the failure this guard exists to
// prevent: without it the setting is accepted, persisted and reported back
// unchanged while never reaching the optimizer.
func TestAOBLMOAKnobsRejectedOnOtherVariants(t *testing.T) {
	t.Parallel()

	for _, variant := range []app.Variant{app.VariantStandard, app.VariantDESMA, app.VariantMPMA} {
		t.Run(string(variant), func(t *testing.T) {
			t.Parallel()

			_, err := app.Normalize(app.JobConfig{
				RefPath:      advancedRefPath,
				Variant:      variant,
				AquilaWeight: floatPtr(0.5),
			})
			if err == nil {
				t.Fatalf("Normalize() accepted aquilaWeight on %q, want an error", variant)
			}

			if !strings.Contains(err.Error(), string(app.VariantAOBLMOA)) {
				t.Fatalf("error %q does not name the variant that reads the knob", err)
			}
		})
	}
}

func TestAOBLMOAKnobsAcceptedOnAOBLMOA(t *testing.T) {
	t.Parallel()

	config, err := app.Normalize(app.JobConfig{
		RefPath:               advancedRefPath,
		Variant:               app.VariantAOBLMOA,
		AquilaWeight:          floatPtr(0.25),
		OppositionProbability: floatPtr(0),
	})
	if err != nil {
		t.Fatal(err)
	}

	if config.AquilaWeight == nil || *config.AquilaWeight != 0.25 {
		t.Fatalf("AquilaWeight = %v, want 0.25", config.AquilaWeight)
	}

	// The pointer exists precisely so this survives a round trip.
	if config.OppositionProbability == nil || *config.OppositionProbability != 0 {
		t.Fatalf("OppositionProbability = %v, want an explicit 0", config.OppositionProbability)
	}
}

func TestAdvancedKnobsDefaultToUnset(t *testing.T) {
	t.Parallel()

	config, err := app.Normalize(app.JobConfig{RefPath: advancedRefPath})
	if err != nil {
		t.Fatal(err)
	}

	if config.DanceDamp != nil || config.AquilaWeight != nil || config.OppositionProbability != nil {
		t.Fatalf("an unset config gained knobs: %v %v %v",
			config.DanceDamp, config.AquilaWeight, config.OppositionProbability)
	}
}

// The pointer types exist so an explicit zero survives persistence. A plain
// float64 with omitempty would drop it, and a checkpoint would come back
// carrying the library default instead of the setting the operator chose.
func TestAdvancedKnobZeroSurvivesJSONRoundTrip(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(app.JobConfig{
		RefPath:               advancedRefPath,
		Variant:               app.VariantAOBLMOA,
		DanceDamp:             floatPtr(0),
		AquilaWeight:          floatPtr(0),
		OppositionProbability: floatPtr(0),
	})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(encoded), `"danceDamp":0`) {
		t.Fatalf("an explicit zero was dropped from the encoding: %s", encoded)
	}

	var decoded app.JobConfig

	err = json.Unmarshal(encoded, &decoded)
	if err != nil {
		t.Fatal(err)
	}

	for field, value := range map[string]*float64{
		"danceDamp":             decoded.DanceDamp,
		"aquilaWeight":          decoded.AquilaWeight,
		"oppositionProbability": decoded.OppositionProbability,
	} {
		if value == nil {
			t.Fatalf("%s came back nil, so an explicit zero was indistinguishable from unset", field)
		}

		if *value != 0 {
			t.Fatalf("%s = %v, want 0", field, *value)
		}
	}
}

// A checkpoint written before these knobs existed carries no such fields, and
// must resume as it always did rather than picking up a zero.
func TestAdvancedKnobsAbsentFromLegacyJSON(t *testing.T) {
	t.Parallel()

	var decoded app.JobConfig

	err := json.Unmarshal([]byte(`{"refPath":"reference.png","popSize":64}`), &decoded)
	if err != nil {
		t.Fatal(err)
	}

	if decoded.DanceDamp != nil || decoded.AquilaWeight != nil || decoded.OppositionProbability != nil {
		t.Fatal("a legacy checkpoint gained advanced knobs")
	}
}
