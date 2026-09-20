package cmd

import (
	"encoding/json"
	"fmt"
	"image"
	"image/draw"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cwbudde/circlefit/internal/anim"
	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/fit"
	"github.com/cwbudde/circlefit/internal/store"
	"github.com/spf13/cobra"
)

var (
	animateRefPath        string
	animateCirclesPath    string
	animateCheckpointPath string
	animateCanvasPath     string
	animateOutDir         string
	animateStyle          string
	animateScale          float64
	animateMargin         float64
	animateHalfLife       int
	animateMaxActive      int
	animateFrames         int
	animateOutro          int
	animateReverse        bool
	animateBackground     string
	animateMP4Path        string
	animateFPS            int
)

// frameNamePattern is both the file name and what ffmpeg is handed as its input
// pattern, so the two cannot drift. The padding is a deliberate departure from
// the original, which wrote Frame7.png and left the sequence unsortable; the
// same mistake is in this project's own snapshots/canvas-%02d.png.
const frameNamePattern = "frame-%06d.png"

// progressThreshold is how many frames a sequence needs before it reports
// progress while writing.
const progressThreshold = 50

var animateCmd = &cobra.Command{
	Use:   "animate",
	Short: "Replay a finished circle arrangement as a sequence of PNG frames",
	Long: `Renders an arrangement as an animation, one PNG per frame, and
optionally encodes it to MP4 with ffmpeg.

This is a port of the animation export in this project's Pascal predecessor,
CircledPictureDrawing. Like the original it replays a finished arrangement
rather than recording the search, so it works on any circle list: a checkpoint
a run left behind, a hand-authored file, or the initialCircles of a schedule.

Four styles are available. static adds one circle per frame. grow expands each
circle in turn from a point, fading it in as it goes. cascade grows up to
--max-active circles at once in a sliding window. inflate expands the whole
arrangement together.`,
	Args: cobra.NoArgs,
	RunE: runAnimate,
}

func init() {
	styles := make([]string, 0, len(anim.Styles()))
	for _, style := range anim.Styles() {
		styles = append(styles, string(style))
	}

	defaults := anim.DefaultOptions()

	flags := animateCmd.Flags()
	flags.StringVar(&animateRefPath, "ref", "", "Reference image, which sets the frame size (required)")
	flags.StringVar(&animateCirclesPath, "circles", "", "Circle list or schedule document")
	flags.StringVar(&animateCheckpointPath, "checkpoint", "", "Checkpoint file whose best solution to animate")
	flags.StringVar(&animateCanvasPath, "canvas", "", "Base canvas the arrangement was fitted over")
	flags.StringVar(&animateOutDir, "out-dir", "", "Directory to write the frames into (required)")
	flags.StringVar(&animateStyle, "style", string(defaults.Style), "Animation style: "+strings.Join(styles, ", "))
	flags.Float64Var(&animateScale, "scale", defaults.Scale, "Multiply every coordinate and radius by this")
	flags.Float64Var(&animateMargin, "margin", 0,
		"Empty space on each side, as a fraction of the scaled size; 0.5 reproduces the original framing")
	flags.IntVar(&animateHalfLife, "half-life", defaults.HalfLife,
		"How aggressively grow drops frames as it goes; 0 keeps every frame")
	flags.IntVar(&animateMaxActive, "max-active", defaults.MaxActive, "How many circles cascade grows at once")
	flags.IntVar(&animateFrames, "frames", 0, "Frame count for inflate; 0 derives it from the largest radius")
	flags.IntVar(&animateOutro, "outro", 0, "Append this many closing vignette frames; 41 reproduces the original")
	flags.BoolVar(&animateReverse, "reverse", false, "Play inflate backwards, deflating to nothing")
	flags.StringVar(&animateBackground, "background", "#FFFFFF", "Background colour behind the arrangement")
	flags.StringVar(&animateMP4Path, "mp4", "", "Also encode the frames to this MP4, using ffmpeg")
	flags.IntVar(&animateFPS, "fps", 30, "Frame rate for --mp4")

	_ = animateCmd.MarkFlagRequired("ref")
	_ = animateCmd.MarkFlagRequired("out-dir")
	animateCmd.MarkFlagsMutuallyExclusive("circles", "checkpoint")
	animateCmd.MarkFlagsOneRequired("circles", "checkpoint")

	rootCmd.AddCommand(animateCmd)
}

func runAnimate(cmd *cobra.Command, _ []string) error {
	ref, err := loadScoreReference(animateRefPath)
	if err != nil {
		return err
	}

	circles, canvasPath, err := animateArrangement(ref)
	if err != nil {
		return err
	}

	sequence, err := anim.Plan(circles, ref.Bounds().Dx(), ref.Bounds().Dy(), animateOptions())
	if err != nil {
		return err
	}

	background, err := animateCanvas(sequence, ref, canvasPath)
	if err != nil {
		return err
	}

	err = os.MkdirAll(animateOutDir, 0o750)
	if err != nil {
		return fmt.Errorf("create %s: %w", animateOutDir, err)
	}

	cmd.Printf("style:      %s\n", animateStyle)
	cmd.Printf("circles:    %d\n", len(circles))
	cmd.Printf("frames:     %d at %dx%d\n", len(sequence.Frames), sequence.Width, sequence.Height)

	err = writeFrames(cmd, sequence, background)
	if err != nil {
		return err
	}

	cmd.Printf("wrote:      %s\n", filepath.Join(animateOutDir, fmt.Sprintf(frameNamePattern, 0)))

	return encodeVideo(cmd, len(sequence.Frames))
}

func animateOptions() anim.Options {
	opts := anim.DefaultOptions()
	opts.Style = anim.Style(animateStyle)
	opts.Scale = animateScale
	opts.Margin = animateMargin
	opts.HalfLife = animateHalfLife
	opts.MaxActive = animateMaxActive
	opts.Frames = animateFrames
	opts.Outro = animateOutro
	opts.Reverse = animateReverse

	return opts
}

// animateArrangement reads the circles from whichever source was named, and
// reports the base canvas a schedule document asks for. A checkpoint names no
// canvas of its own, so --canvas is the only way to supply one for it.
func animateArrangement(ref *image.NRGBA) ([]fit.Circle, string, error) {
	if animateCheckpointPath != "" {
		circles, err := checkpointCircles(animateCheckpointPath)

		return circles, "", err
	}

	specs, canvasPath, err := loadCircleSpecs(animateCirclesPath)
	if err != nil {
		return nil, "", err
	}

	if len(specs) == 0 {
		return nil, "", fmt.Errorf("%s contains no circles", animateCirclesPath)
	}

	specs.ApplyDefaults()

	err = specs.Validate()
	if err != nil {
		return nil, "", err
	}

	params, err := specs.ToParams()
	if err != nil {
		return nil, "", err
	}

	// An arrangement the canvas cannot hold would be quietly pulled inside, and
	// the animation would then show something nobody authored. score refuses it
	// for the same reason.
	bounds := fit.NewBounds(len(specs), ref.Bounds().Dx(), ref.Bounds().Dy())

	clamped := append([]float64(nil), params...)
	bounds.ClampVector(clamped)

	for i := range params {
		if params[i] != clamped[i] {
			return nil, "", fmt.Errorf("circle %d is outside the bounds a %dx%d canvas allows",
				i/app.ParamsPerCircle, ref.Bounds().Dx(), ref.Bounds().Dy())
		}
	}

	return decodeCircles(params), canvasPath, nil
}

func checkpointCircles(path string) ([]fit.Circle, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read checkpoint: %w", err)
	}

	var checkpoint store.Checkpoint

	err = json.Unmarshal(data, &checkpoint)
	if err != nil {
		return nil, fmt.Errorf("%s is not a checkpoint: %w", path, err)
	}

	// Deliberately not Checkpoint.Validate: that checks the whole job
	// configuration -- reference path, seed, optimizer settings -- and this
	// command reads one field. A checkpoint copied off the machine that ran it
	// names a reference path that does not exist here, and refusing to draw its
	// circles over that would be a rule with no purpose. What has to be sound
	// is the parameter vector.
	if len(checkpoint.BestParams) == 0 {
		return nil, fmt.Errorf("checkpoint %s records no solution", path)
	}

	if len(checkpoint.BestParams)%app.ParamsPerCircle != 0 {
		return nil, fmt.Errorf("checkpoint %s holds %d parameters, which is not a whole number of circles",
			path, len(checkpoint.BestParams))
	}

	return decodeCircles(checkpoint.BestParams), nil
}

func decodeCircles(params []float64) []fit.Circle {
	vector := fit.ParamVector{Data: params, K: len(params) / app.ParamsPerCircle}

	circles := make([]fit.Circle, vector.K)
	for i := range circles {
		circles[i] = vector.DecodeCircle(i)
	}

	return circles
}

// animateCanvas builds the background the frames are drawn over: the chosen
// colour everywhere, with the base canvas placed on top of it when one is
// named.
//
// A base canvas cannot be scaled. Resampling it would invent pixels the fit
// never saw, and the frames would stop matching the image the run produced, so
// the combination is refused rather than approximated.
func animateCanvas(sequence *anim.Sequence, ref *image.NRGBA, canvasPath string) (*image.NRGBA, error) {
	if animateCanvasPath != "" {
		canvasPath = animateCanvasPath
	}

	fill, err := app.ParseHexColor(animateBackground)
	if err != nil {
		return nil, fmt.Errorf("background: %w", err)
	}

	background := image.NewNRGBA(image.Rect(0, 0, sequence.Width, sequence.Height))
	for i := 0; i < len(background.Pix); i += 4 {
		background.Pix[i+0] = uint8(fill[0]*255 + 0.5)
		background.Pix[i+1] = uint8(fill[1]*255 + 0.5)
		background.Pix[i+2] = uint8(fill[2]*255 + 0.5)
		background.Pix[i+3] = 0xFF
	}

	if canvasPath == "" {
		return background, nil
	}

	if animateScale != 1 {
		return nil, fmt.Errorf("--canvas cannot be combined with --scale %g: "+
			"scaling the base image would invent pixels the fit never saw", animateScale)
	}

	canvas, err := loadScoreReference(canvasPath)
	if err != nil {
		return nil, fmt.Errorf("canvas %s: %w", canvasPath, err)
	}

	if canvas.Bounds().Dx() != ref.Bounds().Dx() || canvas.Bounds().Dy() != ref.Bounds().Dy() {
		return nil, fmt.Errorf("canvas %s is %dx%d but the reference is %dx%d",
			canvasPath, canvas.Bounds().Dx(), canvas.Bounds().Dy(), ref.Bounds().Dx(), ref.Bounds().Dy())
	}

	placeCanvas(background, canvas, sequence.OffsetX, sequence.OffsetY)

	return background, nil
}

func placeCanvas(background, canvas *image.NRGBA, offsetX, offsetY int) {
	target := image.Rect(offsetX, offsetY,
		offsetX+canvas.Bounds().Dx(), offsetY+canvas.Bounds().Dy())

	draw.Draw(background, target, canvas, canvas.Bounds().Min, draw.Src)
}

func writeFrames(cmd *cobra.Command, sequence *anim.Sequence, background *image.NRGBA) error {
	total := len(sequence.Frames)

	// Progress is for sequences long enough to wait on. A short one finishes
	// before the first line would be read, and printing ten of them for five
	// frames is noise.
	step := total / 10
	if total < progressThreshold {
		step = 0
	}

	return anim.Render(sequence, background, func(index int, img *image.NRGBA) error {
		path := filepath.Join(animateOutDir, fmt.Sprintf(frameNamePattern, index))

		err := writePNG(path, img)
		if err != nil {
			return fmt.Errorf("write frame %d: %w", index, err)
		}

		if step > 0 && index > 0 && index%step == 0 {
			cmd.Printf("            %d/%d frames\n", index, total)
		}

		return nil
	})
}

// encodeVideo runs ffmpeg over the frames just written.
//
// An absent ffmpeg is an error rather than a warning: --mp4 was asked for
// explicitly, and a command that quietly produces nothing it was told to
// produce is worse than one that says why. The message carries the command, so
// it can be run by hand once ffmpeg is installed.
func encodeVideo(cmd *cobra.Command, frames int) error {
	if animateMP4Path == "" {
		return nil
	}

	args := ffmpegArgs()

	binary, err := exec.LookPath("ffmpeg")
	if err != nil {
		return fmt.Errorf("--mp4 needs ffmpeg, which is not on PATH; "+
			"the frames are written, so run this once it is installed:\n  ffmpeg %s",
			strings.Join(args, " "))
	}

	cmd.Printf("encoding:   %d frames at %d fps\n", frames, animateFPS)

	// No shell is involved and the argument vector is fixed; the only variable
	// parts are paths the operator named on the command line. The context is the
	// command's own, so interrupting the run stops the encoder with it.
	command := exec.CommandContext(cmd.Context(), binary, args...)

	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg: %w\n%s", err, output)
	}

	cmd.Printf("video:      %s\n", animateMP4Path)

	return nil
}

// ffmpegArgs is separate so a test can assert the command without running it.
//
// The scale filter is not cosmetic: yuv420p needs even dimensions, and --scale
// and --margin can both produce odd ones.
func ffmpegArgs() []string {
	return []string{
		"-y",
		"-framerate", strconv.Itoa(animateFPS),
		"-i", filepath.Join(animateOutDir, frameNamePattern),
		"-c:v", "libx264",
		"-pix_fmt", "yuv420p",
		"-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2",
		animateMP4Path,
	}
}
