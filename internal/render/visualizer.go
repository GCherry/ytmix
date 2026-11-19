package render

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultVisualizerPreset is the default Path D Auto Visualizer preset when --bg and --visualizer are omitted.
	DefaultVisualizerPreset = "bars"
	// DefaultVisualizerTheme is the default Path D Color Theme.
	DefaultVisualizerTheme = "studio-mint"
	// DefaultVisualizerBrightness is the default 100% full luminance multiplier.
	DefaultVisualizerBrightness = 100
	// MinVisualizerBrightness is the minimum permitted global brightness percentage.
	MinVisualizerBrightness = 10
	// MaxVisualizerBrightness is the maximum permitted global brightness percentage.
	MaxVisualizerBrightness = 100
)

// VisualizerSpec describes one built-in Path D Auto Visualizer preset.
type VisualizerSpec struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Filter      string   `json:"filter"`
	Required    []string `json:"-"`
	Category    string   `json:"category"`
	Description string   `json:"description"`
	IsDark      bool     `json:"isDark"`
}

// ThemeSpec describes a color palette for Path D Auto Visualizer presets.
type ThemeSpec struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	BG            string `json:"bg"`
	Primary       string `json:"primary"`
	Secondary     string `json:"secondary"`
	Glow          string `json:"glow"`
	primaryHex    string
	secondaryHex  string
	spectrumColor string
	r             int
	g             int
	b             int
}

// VisualizerConfig configures Path D Auto Visualizer generation and Darkness & Sleep controls.
type VisualizerConfig struct {
	Preset      string
	Theme       string
	Glow        bool
	Brightness  int
	FadeToBlack time.Duration
}

// DefaultVisualizerConfig returns the default Path D Auto Visualizer configuration (bars, studio-mint, glow on, 100% brightness).
func DefaultVisualizerConfig() VisualizerConfig {
	return VisualizerConfig{
		Preset:     DefaultVisualizerPreset,
		Theme:      DefaultVisualizerTheme,
		Glow:       true,
		Brightness: DefaultVisualizerBrightness,
	}
}

var visualizerCatalog = []VisualizerSpec{
	{
		ID:          "black",
		Name:        "Pure Blackout Screen",
		Filter:      "color=c=black",
		Required:    []string{"color"},
		Category:    "dark",
		Description: "True #000000 pitch-black screen (2 fps)",
		IsDark:      true,
	},
	{
		ID:          "ember",
		Name:        "Dim Ember Pulse",
		Filter:      "showwaves+gblur",
		Required:    []string{"showwaves", "gblur", "scale"},
		Category:    "dark",
		Description: "Soft warm fireplace ember breath",
		IsDark:      true,
	},
	{
		ID:          "night-line",
		Name:        "Midnight Whisper Line",
		Filter:      "showwaves+pad",
		Required:    []string{"showwaves", "pad"},
		Category:    "dark",
		Description: "Ultra-dim thin hairline on OLED black",
		IsDark:      true,
	},
	{
		ID:          "night-fog",
		Name:        "Sub-Bass Night Fog",
		Filter:      "showfreqs+gblur",
		Required:    []string{"showfreqs", "gblur", "scale"},
		Category:    "dark",
		Description: "Blurred low ambient horizon glow",
		IsDark:      true,
	},
	{
		ID:          "lunar-halo",
		Name:        "Dim Lunar Halo",
		Filter:      "avectorscope+gblur",
		Required:    []string{"avectorscope", "gblur", "pad"},
		Category:    "dark",
		Description: "Faint slow-breathing halo in pitch black",
		IsDark:      true,
	},
	{
		ID:          "bars",
		Name:        "Spectrum Bars",
		Filter:      "showfreqs",
		Required:    []string{"showfreqs"},
		Category:    "spectrum",
		Description: "Classic vertical EQ bars (log freq)",
	},
	{
		ID:          "waves",
		Name:        "Smooth Waveform",
		Filter:      "showwaves",
		Required:    []string{"showwaves"},
		Category:    "wave",
		Description: "Centered horizontal sound wave",
	},
	{
		ID:          "cqt",
		Name:        "Musical Piano Spectrum",
		Filter:      "showcqt",
		Required:    []string{"showcqt"},
		Category:    "spectrum",
		Description: "Pitch-spaced bars + harmonic ribbon",
	},
	{
		ID:          "radial",
		Name:        "Radial / Stereo Burst",
		Filter:      "avectorscope",
		Required:    []string{"avectorscope", "pad"},
		Category:    "wave",
		Description: "Circular polar stereo bloom",
	},
	{
		ID:          "spectrogram",
		Name:        "Scrolling Spectrogram",
		Filter:      "showspectrum",
		Required:    []string{"showspectrum"},
		Category:    "spectrum",
		Description: "Flowing frequency heatmap waterfall",
	},
	{
		ID:          "3dscope",
		Name:        "3D Depth Ribbon",
		Filter:      "a3dscope",
		Required:    []string{"a3dscope"},
		Category:    "wave",
		Description: "Receding 3D waveform corridor",
	},
	{
		ID:          "histogram",
		Name:        "Audio Histogram",
		Filter:      "ahistogram",
		Required:    []string{"ahistogram"},
		Category:    "meters",
		Description: "Stereo amplitude density + waterfall",
	},
	{
		ID:          "phase",
		Name:        "Stereo Phase Meter",
		Filter:      "aphasemeter",
		Required:    []string{"aphasemeter", "anullsink"},
		Category:    "meters",
		Description: "L/R correlation trace + median line",
	},
	{
		ID:          "vu",
		Name:        "Studio VU Meters",
		Filter:      "showvolume",
		Required:    []string{"showvolume", "pad"},
		Category:    "meters",
		Description: "Dual-channel L/R console peak bars",
	},
}

var themeCatalog = []ThemeSpec{
	{
		ID:            "studio-mint",
		Name:          "Studio Mint & Gold",
		BG:            "#0B0C0D",
		Primary:       "#3EE6B5",
		Secondary:     "#E8BE5A",
		Glow:          "rgba(62, 230, 181, 0.45)",
		primaryHex:    "0x3EE6B5",
		secondaryHex:  "0xE8BE5A",
		spectrumColor: "cool",
		r:             62,
		g:             230,
		b:             181,
	},
	{
		ID:            "midnight-cyan",
		Name:          "Midnight Cyan",
		BG:            "#070B14",
		Primary:       "#38BDF8",
		Secondary:     "#818CF8",
		Glow:          "rgba(56, 189, 248, 0.45)",
		primaryHex:    "0x38BDF8",
		secondaryHex:  "0x818CF8",
		spectrumColor: "viridis",
		r:             56,
		g:             189,
		b:             248,
	},
	{
		ID:            "sunset-amber",
		Name:          "Sunset Amber",
		BG:            "#120A09",
		Primary:       "#FB923C",
		Secondary:     "#F43F5E",
		Glow:          "rgba(251, 146, 60, 0.45)",
		primaryHex:    "0xFB923C",
		secondaryHex:  "0xF43F5E",
		spectrumColor: "fiery",
		r:             251,
		g:             146,
		b:             60,
	},
	{
		ID:            "neon-violet",
		Name:          "Neon Violet",
		BG:            "#0D0814",
		Primary:       "#C084FC",
		Secondary:     "#2DD4BF",
		Glow:          "rgba(192, 132, 252, 0.45)",
		primaryHex:    "0xC084FC",
		secondaryHex:  "0x2DD4BF",
		spectrumColor: "magma",
		r:             192,
		g:             132,
		b:             252,
	},
	{
		ID:            "monochrome",
		Name:          "Monochrome Paper",
		BG:            "#0A0A0A",
		Primary:       "#F3F2EC",
		Secondary:     "#949A95",
		Glow:          "rgba(243, 242, 236, 0.35)",
		primaryHex:    "0xF3F2EC",
		secondaryHex:  "0x949A95",
		spectrumColor: "intensity",
		r:             243,
		g:             242,
		b:             236,
	},
	{
		ID:            "oled-ember",
		Name:          "OLED Ember (Zero Blue)",
		BG:            "#000000",
		Primary:       "#9A3412",
		Secondary:     "#5C1E09",
		Glow:          "rgba(154, 52, 18, 0.35)",
		primaryHex:    "0x9A3412",
		secondaryHex:  "0x5C1E09",
		spectrumColor: "fire",
		r:             154,
		g:             52,
		b:             18,
	},
	{
		ID:            "night-indigo",
		Name:          "Deep Night Indigo",
		BG:            "#000000",
		Primary:       "#3B4F73",
		Secondary:     "#1E293B",
		Glow:          "rgba(59, 79, 115, 0.35)",
		primaryHex:    "0x3B4F73",
		secondaryHex:  "0x1E293B",
		spectrumColor: "cividis",
		r:             59,
		g:             79,
		b:             115,
	},
}

// ListVisualizers returns a fresh copy of all 14 built-in Path D Auto Visualizer presets.
func ListVisualizers() []VisualizerSpec {
	out := make([]VisualizerSpec, len(visualizerCatalog))
	for i, spec := range visualizerCatalog {
		spec.Required = append([]string(nil), spec.Required...)
		out[i] = spec
	}
	return out
}

// LookupVisualizer looks up a visualizer preset by ID (case-insensitive).
func LookupVisualizer(id string) (VisualizerSpec, bool) {
	key := strings.ToLower(strings.TrimSpace(id))
	for _, spec := range visualizerCatalog {
		if spec.ID == key {
			spec.Required = append([]string(nil), spec.Required...)
			return spec, true
		}
	}
	return VisualizerSpec{}, false
}

// ListThemes returns a fresh copy of all 7 built-in Path D Color Themes.
func ListThemes() []ThemeSpec {
	out := make([]ThemeSpec, len(themeCatalog))
	copy(out, themeCatalog)
	return out
}

// LookupTheme looks up a Path D Color Theme by ID (case-insensitive).
func LookupTheme(id string) (ThemeSpec, bool) {
	key := strings.ToLower(strings.TrimSpace(id))
	for _, th := range themeCatalog {
		if th.ID == key {
			return th, true
		}
	}
	return ThemeSpec{}, false
}

func validVisualizerIDs() string {
	ids := make([]string, len(visualizerCatalog))
	for i, spec := range visualizerCatalog {
		ids[i] = spec.ID
	}
	return strings.Join(ids, ", ")
}

func validThemeIDs() string {
	ids := make([]string, len(themeCatalog))
	for i, th := range themeCatalog {
		ids[i] = th.ID
	}
	return strings.Join(ids, ", ")
}

// ValidateVisualizerConfig normalizes defaults and validates a VisualizerConfig.
func ValidateVisualizerConfig(cfg VisualizerConfig) (VisualizerConfig, error) {
	preset := strings.ToLower(strings.TrimSpace(cfg.Preset))
	if preset == "" {
		preset = DefaultVisualizerPreset
	}
	if _, ok := LookupVisualizer(preset); !ok {
		return VisualizerConfig{}, fmt.Errorf("unknown --visualizer %q (supported: %s)", cfg.Preset, validVisualizerIDs())
	}

	theme := strings.ToLower(strings.TrimSpace(cfg.Theme))
	if theme == "" {
		theme = DefaultVisualizerTheme
	}
	if _, ok := LookupTheme(theme); !ok {
		return VisualizerConfig{}, fmt.Errorf("unknown --visualizer-theme %q (supported: %s)", cfg.Theme, validThemeIDs())
	}

	brightness := cfg.Brightness
	if brightness == 0 {
		brightness = DefaultVisualizerBrightness
	}
	if brightness < MinVisualizerBrightness || brightness > MaxVisualizerBrightness {
		return VisualizerConfig{}, fmt.Errorf("--visualizer-brightness must be between %d and %d (got %d)", MinVisualizerBrightness, MaxVisualizerBrightness, cfg.Brightness)
	}

	if cfg.FadeToBlack < 0 {
		return VisualizerConfig{}, fmt.Errorf("--fade-to-black must be >= 0 (got %s)", cfg.FadeToBlack)
	}

	return VisualizerConfig{
		Preset:      preset,
		Theme:       theme,
		Glow:        cfg.Glow,
		Brightness:  brightness,
		FadeToBlack: cfg.FadeToBlack,
	}, nil
}

// ParseFiltersOutput parses `ffmpeg -hide_banner -filters` output into a set of available filter names.
func ParseFiltersOutput(out []byte) map[string]bool {
	filters := make(map[string]bool)
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "Filters:") || strings.HasPrefix(line, "T") && strings.Contains(line, "=") || strings.HasPrefix(line, "--") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 || !strings.Contains(fields[2], "->") {
			continue
		}
		if !isFilterFlagsField(fields[0]) {
			continue
		}
		filters[fields[1]] = true
	}
	return filters
}

func isFilterFlagsField(s string) bool {
	if len(s) < 2 || len(s) > 4 {
		return false
	}
	for _, r := range s {
		switch r {
		case '.', 'T', 'S', 'C':
		default:
			return false
		}
	}
	return true
}

// DetectFilters executes `ffmpeg -hide_banner -filters` once and returns available filter names.
func DetectFilters(ctx context.Context, runner func(ctx context.Context, name string, args ...string) ([]byte, error)) (map[string]bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if runner == nil {
		return nil, errors.New("filter detection runner is nil")
	}
	out, err := runner(ctx, "ffmpeg", "-hide_banner", "-filters")
	if err != nil {
		return nil, fmt.Errorf("detect ffmpeg filters: %w", err)
	}
	return ParseFiltersOutput(out), nil
}

// PresetAvailable reports whether all FFmpeg filters required by presetID exist in available.
func PresetAvailable(presetID string, available map[string]bool) (bool, string) {
	spec, ok := LookupVisualizer(presetID)
	if !ok {
		return false, ""
	}
	for _, req := range spec.Required {
		if !available[req] {
			return false, req
		}
	}
	return true, ""
}

func formatFFmpegSeconds(sec float64) string {
	rounded := math.Round(sec*100) / 100
	return strconv.FormatFloat(rounded, 'f', -1, 64)
}

// BuildPathDFadeFilter constructs the sleep fade-to-black video filter string, or "" when disabled or out of range.
func BuildPathDFadeFilter(fadeToBlack time.Duration, totalDuration float64) string {
	if fadeToBlack <= 0 {
		return ""
	}
	st := fadeToBlack.Seconds()
	if totalDuration > 0 && st >= totalDuration {
		return ""
	}
	d := 15.0
	if totalDuration > 0 {
		if rem := totalDuration - st; rem < d {
			d = rem
		}
	}
	if d < 1.0 {
		d = 1.0
	}
	return fmt.Sprintf("fade=t=out:st=%s:d=%s:color=black", formatFFmpegSeconds(st), formatFFmpegSeconds(d))
}

// BuildPathDBlackArgs constructs the FFmpeg arguments for Path D's pure #000000 blackout screen preset.
func BuildPathDBlackArgs(mergedAudioPath, assPath, outPath string, overlayTitles bool, totalDuration float64, cfg VisualizerConfig) []string {
	fps := "2"
	if overlayTitles {
		fps = "10"
	}
	var vfParts []string
	if overlayTitles {
		vfParts = append(vfParts, fmt.Sprintf("subtitles='%s'", EscapeFFmpegFilterPath(assPath)))
		if fade := BuildPathDFadeFilter(cfg.FadeToBlack, totalDuration); fade != "" {
			vfParts = append(vfParts, fade)
		}
	}
	vfParts = append(vfParts, "format=yuv420p")

	return []string{
		"-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi",
		"-i", fmt.Sprintf("color=c=black:s=1920x1080:r=%s", fps),
		"-i", mergedAudioPath,
		"-vf", strings.Join(vfParts, ","),
		"-map", "0:v:0",
		"-map", "1:a:0",
		"-c:v", "libx264", "-tune", "stillimage", "-preset", "veryfast", "-crf", "18",
		"-c:a", "copy",
		"-shortest",
		outPath,
	}
}

func buildPrimaryPresetChain(spec VisualizerSpec, theme ThemeSpec, glow bool) (chain string, hasBuiltInBlur bool) {
	switch spec.ID {
	case "ember":
		blurPart := ""
		if glow {
			blurPart = ",gblur=sigma=18"
		}
		return fmt.Sprintf("[0:a]showwaves=s=960x540:r=30:mode=cline:colors=0x9A3412|0x5C1E09:scale=sqrt,format=rgb24%s,scale=1920:1080:flags=bilinear,setsar=1[vbase]", blurPart), true
	case "night-line":
		lineColor := "0x476082"
		if theme.ID == "oled-ember" {
			lineColor = "0x9A3412"
		}
		return fmt.Sprintf("[0:a]showwaves=s=1920x220:r=30:mode=line:colors=%s:scale=sqrt,pad=1920:1080:0:640:black,setsar=1,format=rgb24[vbase]", lineColor), false
	case "night-fog":
		blurPart := ""
		if glow {
			blurPart = ",gblur=sigma=28"
		}
		return fmt.Sprintf("[0:a]showfreqs=s=960x540:r=30:mode=bar:fscale=log:ascale=sqrt:colors=0x7C2D12|0x312E81,format=rgb24%s,scale=1920:1080:flags=bilinear,setsar=1[vbase]", blurPart), true
	case "lunar-halo":
		rc, gc, bc := 120, 90, 55
		if theme.ID == "night-indigo" {
			rc, gc, bc = 65, 85, 125
		}
		blurPart := ""
		if glow {
			blurPart = ",gblur=sigma=10"
		}
		return fmt.Sprintf("[0:a]avectorscope=s=540x540:r=30:mode=polar:draw=line:scale=sqrt:rc=%d:gc=%d:bc=%d:rf=12:gf=10:bf=8,format=rgb24%s,pad=1920:1080:(ow-iw)/2:(oh-ih)/2:black,setsar=1[vbase]", rc, gc, bc, blurPart), true
	case "bars":
		return fmt.Sprintf("[0:a]showfreqs=s=1920x1080:r=30:mode=bar:fscale=log:ascale=sqrt:colors=%s|%s,setsar=1,format=rgb24[vbase]", theme.primaryHex, theme.secondaryHex), false
	case "waves":
		return fmt.Sprintf("[0:a]showwaves=s=1920x1080:r=30:mode=cline:scale=sqrt:colors=%s|%s,setsar=1,format=rgb24[vbase]", theme.primaryHex, theme.secondaryHex), false
	case "cqt":
		return "[0:a]showcqt=s=1920x1080:r=30:axis=0:text=0:bar_g=2:sono_g=4:bar_v=9:sono_v=17,setsar=1,format=rgb24[vbase]", false
	case "radial":
		return fmt.Sprintf("[0:a]avectorscope=s=1080x1080:r=30:mode=polar:draw=line:scale=sqrt:rc=%d:gc=%d:bc=%d:rf=18:gf=18:bf=18,pad=1920:1080:(ow-iw)/2:(oh-ih)/2:black,setsar=1,format=rgb24[vbase]", theme.r, theme.g, theme.b), false
	case "spectrogram":
		return fmt.Sprintf("[0:a]showspectrum=s=1920x1080:mode=combined:color=%s:scale=log:slide=scroll:fps=30,setsar=1,format=rgb24[vbase]", theme.spectrumColor), false
	case "3dscope":
		return "[0:a]a3dscope=s=1920x1080:r=30,setsar=1,format=rgb24[vbase]", false
	case "histogram":
		return "[0:a]ahistogram=s=1920x1080:r=30:dmode=separate:scale=log,setsar=1,format=rgb24[vbase]", false
	case "phase":
		return fmt.Sprintf("[0:a]aphasemeter=s=1920x1080:r=30:video=1:mpc=%s[aout][vraw];[aout]anullsink;[vraw]setsar=1,format=rgb24[vbase]", theme.secondaryHex), false
	case "vu":
		return fmt.Sprintf("[0:a]showvolume=w=1600:h=140:r=30:t=0:v=0:b=4:dm=1.5:dmc=%s,pad=1920:1080:(ow-iw)/2:(oh-ih)/2:black,setsar=1,format=rgb24[vbase]", theme.secondaryHex), false
	default:
		return "", false
	}
}

// BuildPathDFilterComplex builds the FFmpeg -filter_complex graph for an audio-reactive Path D preset.
func BuildPathDFilterComplex(assPath string, overlayTitles bool, totalDuration float64, cfg VisualizerConfig) (string, error) {
	normCfg, err := ValidateVisualizerConfig(cfg)
	if err != nil {
		return "", err
	}
	if normCfg.Preset == "black" {
		return "", errors.New("preset \"black\" uses BuildPathDBlackArgs instead of -filter_complex")
	}
	spec, _ := LookupVisualizer(normCfg.Preset)
	theme, _ := LookupTheme(normCfg.Theme)

	primary, hasBuiltInBlur := buildPrimaryPresetChain(spec, theme, normCfg.Glow)
	parts := []string{primary}
	curLabel := "[vbase]"

	if normCfg.Glow && !hasBuiltInBlur {
		parts = append(parts, fmt.Sprintf("%ssplit[vmain][vglow];[vglow]gblur=sigma=12[vblur];[vmain][vblur]blend=all_mode=screen:all_opacity=0.45[vpost]", curLabel))
		curLabel = "[vpost]"
	}

	var postFilters []string
	if normCfg.Brightness < MaxVisualizerBrightness {
		factor := float64(normCfg.Brightness) / 100.0
		postFilters = append(postFilters, fmt.Sprintf("colorchannelmixer=rr=%.2f:gg=%.2f:bb=%.2f", factor, factor, factor))
	}
	if overlayTitles {
		postFilters = append(postFilters, fmt.Sprintf("subtitles='%s'", EscapeFFmpegFilterPath(assPath)))
	}
	if fade := BuildPathDFadeFilter(normCfg.FadeToBlack, totalDuration); fade != "" {
		postFilters = append(postFilters, fade)
	}
	postFilters = append(postFilters, "format=yuv420p")

	parts = append(parts, fmt.Sprintf("%s%s[vout]", curLabel, strings.Join(postFilters, ",")))
	return strings.Join(parts, ";"), nil
}

// BuildPathDArgs constructs the complete FFmpeg CLI arguments for Rendering Path D.
func BuildPathDArgs(mergedAudioPath, assPath, outPath string, overlayTitles bool, totalDuration float64, cfg VisualizerConfig, encoderArgs []string) ([]string, error) {
	normCfg, err := ValidateVisualizerConfig(cfg)
	if err != nil {
		return nil, err
	}
	if normCfg.Preset == "black" {
		return BuildPathDBlackArgs(mergedAudioPath, assPath, outPath, overlayTitles, totalDuration, normCfg), nil
	}
	fc, err := BuildPathDFilterComplex(assPath, overlayTitles, totalDuration, normCfg)
	if err != nil {
		return nil, err
	}
	if len(encoderArgs) == 0 {
		encoderArgs = []string{"-c:v", "libx264", "-preset", "veryfast", "-crf", "20"}
	}

	args := []string{
		"-y", "-hide_banner", "-loglevel", "error",
		"-i", mergedAudioPath,
		"-filter_complex", fc,
		"-map", "[vout]",
		"-map", "0:a:0",
	}
	args = append(args, encoderArgs...)
	args = append(args, "-c:a", "copy", "-shortest", outPath)
	return args, nil
}
