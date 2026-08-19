package cmd

import (
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"math"
	"os"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/fit"
	"github.com/cwbudde/mayflycirclefit/internal/fit/renderer"
	"github.com/spf13/cobra"
)

var (
	scoreRefPath     string
	scoreCirclesPath string
	scoreOutPath     string
	scoreDiffPath    string
	scoreJSON        bool
)

var scoreCmd = &cobra.Command{
	Use:   "score",
	Short: "Score a hand-authored circle arrangement against a reference",
	Long: `Renders an explicit list of circles and reports its cost against a
reference image, without starting a run or a server.

The circle file is either a bare JSON array of circle specifications or a
schedule document, in which case base.initialCircles is scored. This is how a
hand-placed arrangement is checked before it becomes the seed of a campaign.`,
	Args: cobra.NoArgs,
	RunE: runScore,
}

func init() {
	scoreCmd.Flags().StringVar(&scoreRefPath, "ref", "", "Reference image path (required)")
	scoreCmd.Flags().StringVar(&scoreCirclesPath, "circles", "", "Circle list or schedule document (required)")
	scoreCmd.Flags().StringVar(&scoreOutPath, "out", "", "Write the rendered arrangement to this PNG")
	scoreCmd.Flags().StringVar(&scoreDiffPath, "diff", "", "Write the false-color residual to this PNG")
	scoreCmd.Flags().BoolVar(&scoreJSON, "json", false, "Print the result as JSON")
	_ = scoreCmd.MarkFlagRequired("ref")
	_ = scoreCmd.MarkFlagRequired("circles")
	rootCmd.AddCommand(scoreCmd)
}

// scoreResult is the machine-readable form of what the command prints.
type scoreResult struct {
	Circles      int     `json:"circles"`
	Cost         float64 `json:"cost"`
	PSNR         float64 `json:"psnr"`
	PSNRInfinite bool    `json:"psnrInfinite,omitempty"`
	BlankCost    float64 `json:"blankCost"`
	Width        int     `json:"width"`
	Height       int     `json:"height"`
}

func runScore(_ *cobra.Command, _ []string) error {
	specs, err := loadCircleSpecs(scoreCirclesPath)
	if err != nil {
		return err
	}
	if len(specs) == 0 {
		return fmt.Errorf("%s contains no circles", scoreCirclesPath)
	}
	specs.ApplyDefaults()
	if err := specs.Validate(); err != nil {
		return err
	}
	params, err := specs.ToParams()
	if err != nil {
		return err
	}

	ref, err := loadScoreReference(scoreRefPath)
	if err != nil {
		return err
	}
	width, height := ref.Bounds().Dx(), ref.Bounds().Dy()

	// The same refusal the worker applies, for the same reason: a circle the
	// canvas cannot hold would be silently pulled inside and the printed cost
	// would describe an arrangement nobody wrote.
	bounds := fit.NewBounds(len(specs), width, height)
	clamped := append([]float64(nil), params...)
	bounds.ClampVector(clamped)
	for i := range params {
		if params[i] != clamped[i] {
			return fmt.Errorf("circle %d is outside the bounds a %dx%d canvas allows", i/app.ParamsPerCircle, width, height)
		}
	}

	rend := renderer.NewCPURenderer(ref, len(specs))
	result := scoreResult{
		Circles:   len(specs),
		Cost:      rend.Cost(params),
		BlankCost: rend.Cost(make([]float64, len(params))),
		Width:     width,
		Height:    height,
	}
	psnr := fit.PSNR(result.Cost)
	if math.IsInf(psnr, 1) {
		result.PSNRInfinite = true
	} else {
		result.PSNR = psnr
	}

	if scoreOutPath != "" || scoreDiffPath != "" {
		rendered := rend.Render(params)
		if scoreOutPath != "" {
			if err := writeScorePNG(scoreOutPath, rendered); err != nil {
				return err
			}
		}
		// The residual is what makes the number actionable: it says where the
		// arrangement is wrong, which is the question anyone placing a circle by
		// hand is actually asking.
		if scoreDiffPath != "" {
			if err := writeScorePNG(scoreDiffPath, fit.DiffImage(ref, rendered, fit.ColormapTurbo)); err != nil {
				return err
			}
		}
	}

	if scoreJSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}
	fmt.Printf("circles:    %d\n", result.Circles)
	fmt.Printf("canvas:     %dx%d\n", result.Width, result.Height)
	fmt.Printf("cost:       %.4f\n", result.Cost)
	if result.PSNRInfinite {
		fmt.Printf("psnr:       inf dB\n")
	} else {
		fmt.Printf("psnr:       %.4f dB\n", result.PSNR)
	}
	fmt.Printf("blank cost: %.4f\n", result.BlankCost)
	if scoreOutPath != "" {
		fmt.Printf("wrote:      %s\n", scoreOutPath)
	}
	if scoreDiffPath != "" {
		fmt.Printf("residual:   %s\n", scoreDiffPath)
	}
	return nil
}

// loadCircleSpecs accepts either shape a circle list is written in: the bare
// array a scratch file holds, or the schedule document the arrangement ends up
// living in once it seeds a campaign. Scoring the campaign file directly is the
// point -- it means the number reported here describes the document that will
// actually run, not a copy of it that can drift.
func loadCircleSpecs(path string) (app.CircleSpecs, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read circles: %w", err)
	}
	var specs app.CircleSpecs
	if err := json.Unmarshal(data, &specs); err == nil {
		return specs, nil
	}
	var document struct {
		Base struct {
			InitialCircles app.CircleSpecs `json:"initialCircles"`
		} `json:"base"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("%s is neither a circle array nor a schedule document: %w", path, err)
	}
	return document.Base.InitialCircles, nil
}

func loadScoreReference(path string) (*image.NRGBA, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open reference: %w", err)
	}
	defer file.Close()
	decoded, _, err := image.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("decode reference: %w", err)
	}
	bounds := decoded.Bounds()
	if err := app.ValidateImageDimensions(bounds.Dx(), bounds.Dy()); err != nil {
		return nil, err
	}
	ref := image.NewNRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			ref.Set(x, y, decoded.At(x, y))
		}
	}
	return ref, nil
}

func writeScorePNG(path string, img *image.NRGBA) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		return fmt.Errorf("encode output: %w", err)
	}
	return nil
}
