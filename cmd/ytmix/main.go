package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/GCherry/ytmix/internal/mcp"
	"github.com/GCherry/ytmix/internal/mixer"
	"github.com/GCherry/ytmix/internal/render"
	"github.com/GCherry/ytmix/internal/ui"
)

var version = "0.1.0"

type stringSliceFlag []string

func (s *stringSliceFlag) String() string {
	return strings.Join(*s, ",")
}

func (s *stringSliceFlag) Set(val string) error {
	trimmed := strings.TrimSpace(val)
	if trimmed != "" {
		*s = append(*s, trimmed)
	}
	return nil
}

// UIConfig holds the CLI configuration for the `ytmix ui` local web server subcommand.
type UIConfig struct {
	Host       string
	Port       int
	NoOpen     bool
	AudioDir   string
	BG         string
	OutDir     string
	Out        string
	Timestamps string
	AllowDirs  []string
}

// IsUISubcommand reports whether args invoke the `ui` subcommand.
func IsUISubcommand(args []string) bool {
	return len(args) > 0 && args[0] == "ui"
}

// NewUIFlagSet configures the CLI flag definitions for `ytmix ui`.
func NewUIFlagSet(cfg *UIConfig, output io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("ytmix ui", flag.ContinueOnError)
	fs.SetOutput(output)

	fs.StringVar(&cfg.Host, "host", "127.0.0.1", "Bind host address (set 0.0.0.0 for LAN/mobile access).")
	fs.IntVar(&cfg.Port, "port", 8080, "HTTP listen port (0 selects an ephemeral port).")
	fs.BoolVar(&cfg.NoOpen, "no-open", false, "Do not automatically open the browser on startup.")
	fs.StringVar(&cfg.AudioDir, "audio-dir", "./tracks", "Default local folder path prefilled in the UI.")
	fs.StringVar(&cfg.BG, "bg", "", "Default local Background Visual path prefilled in the UI.")
	fs.StringVar(&cfg.OutDir, "out-dir", "", "Default output directory prefilled in the UI and allowed for local reads/writes.")
	fs.StringVar(&cfg.Out, "out", "output.mp4", "Default output .mp4 filename prefilled in the UI.")
	fs.StringVar(&cfg.Timestamps, "timestamps", "timestamps.txt", "Default Chapter Timestamps filename prefilled in the UI.")
	fs.Var((*stringSliceFlag)(&cfg.AllowDirs), "allow-dir", "Additional local directory allowed for loopback UI browsing and output (repeatable).")
	return fs
}

// ParseUIFlags parses `ytmix ui` subcommand arguments into a UIConfig.
func ParseUIFlags(args []string) (UIConfig, error) {
	var cfg UIConfig
	fs := NewUIFlagSet(&cfg, io.Discard)
	if err := fs.Parse(args); err != nil {
		return UIConfig{}, err
	}
	cfg.Host = strings.TrimSpace(cfg.Host)
	if cfg.Host == "" {
		return UIConfig{}, errors.New("--host must not be empty")
	}
	if cfg.Port < 0 || cfg.Port > 65535 {
		return UIConfig{}, fmt.Errorf("--port must be between 0 and 65535 (got %d)", cfg.Port)
	}
	return cfg, nil
}

// IsMCPSubcommand reports whether args invoke the `mcp` subcommand.
func IsMCPSubcommand(args []string) bool {
	return len(args) > 0 && args[0] == "mcp"
}

// NewMCPFlagSet configures the CLI flag definitions for `ytmix mcp`.
func NewMCPFlagSet(output io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("ytmix mcp", flag.ContinueOnError)
	fs.SetOutput(output)
	fs.Usage = func() {
		fmt.Fprintln(output, "Usage: ytmix mcp")
		fmt.Fprintln(output, "Run ytmix as a stdio Model Context Protocol (MCP) JSON-RPC 2.0 server.")
		fs.PrintDefaults()
	}
	return fs
}

// ParseMCPFlags parses `ytmix mcp` subcommand arguments.
func ParseMCPFlags(args []string) error {
	fs := NewMCPFlagSet(io.Discard)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	return nil
}

// CLIOptions holds auxiliary CLI mode flags outside mixer.Config.
type CLIOptions struct {
	ListVisualizers bool
	Estimate        bool
}

// NewFlagSet configures the CLI flag definitions for ytmix.
func NewFlagSet(cfg *mixer.Config, output io.Writer) *flag.FlagSet {
	return NewCLIFlagSetWithOptions(cfg, nil, output)
}

// NewCLIFlagSet configures the CLI flag definitions for ytmix, including --list-visualizers.
func NewCLIFlagSet(cfg *mixer.Config, listVisualizers *bool, output io.Writer) *flag.FlagSet {
	return newCLIFlagSet(cfg, listVisualizers, nil, output)
}

// NewCLIFlagSetWithOptions configures the CLI flag definitions for ytmix, including --list-visualizers and --estimate.
func NewCLIFlagSetWithOptions(cfg *mixer.Config, opts *CLIOptions, output io.Writer) *flag.FlagSet {
	var dummy CLIOptions
	target := &dummy
	if opts != nil {
		target = opts
	}
	return newCLIFlagSet(cfg, &target.ListVisualizers, &target.Estimate, output)
}

func newCLIFlagSet(cfg *mixer.Config, listVisualizers *bool, estimate *bool, output io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("ytmix", flag.ContinueOnError)
	fs.SetOutput(output)

	fs.StringVar(&cfg.AudioDir, "audio-dir", "./tracks", "Directory containing input .mp3 files.")
	fs.StringVar(&cfg.BG, "bg", "", "Optional path to Background Visual (.jpg, .jpeg, .png, .webp, .mp4, .mov, .gif). Omit to use Path D Auto Visualizer.")
	fs.StringVar(&cfg.OutDir, "out-dir", "", "Directory where relative --out and --timestamps paths are written.")
	fs.StringVar(&cfg.Out, "out", "output.mp4", "Output .mp4 video path (relative paths resolve inside --out-dir when set).")
	fs.StringVar(&cfg.Timestamps, "timestamps", "timestamps.txt", "Output file path for YouTube Chapter Timestamps (relative paths resolve inside --out-dir when set).")
	fs.Float64Var(&cfg.Crossfade, "crossfade", 0, "Crossfade duration in seconds between Tracks.")
	fs.BoolVar(&cfg.Normalize, "normalize", false, "Normalize each Track to -14 LUFS.")
	fs.BoolVar(&cfg.OverlayTitles, "overlay-titles", false, "Burn Display Titles into the bottom-left corner via .ass subtitles.")
	fs.IntVar(&cfg.TitleDuration, "title-duration", 0, "Seconds to display each Display Title (0 = full Track).")
	fs.StringVar(&cfg.FontName, "font-name", "Arial", "Font family name used in the .ass subtitle script.")
	fs.BoolVar(&cfg.Shuffle, "shuffle", false, "Randomize Track order instead of sorting alphabetically.")
	fs.StringVar(&cfg.Visualizer, "visualizer", "", "Path D Auto Visualizer preset when --bg is omitted (default bars; see --list-visualizers).")
	fs.StringVar(&cfg.VisualizerTheme, "visualizer-theme", render.DefaultVisualizerTheme, "Path D Color Theme (studio-mint, midnight-cyan, sunset-amber, neon-violet, monochrome, oled-ember, night-indigo).")
	fs.StringVar(&cfg.VisualizerResolution, "visualizer-resolution", render.DefaultVisualizerResolution, "Path D output resolution (1080p, 1440p, 4k).")
	fs.BoolVar(&cfg.VisualizerGlow, "visualizer-glow", true, "Apply subtle glow blur on Path D Auto Visualizer presets.")
	fs.IntVar(&cfg.VisualizerBrightness, "visualizer-brightness", render.DefaultVisualizerBrightness, "Global Path D visualizer brightness percentage in 10..100.")
	fs.DurationVar(&cfg.FadeToBlack, "fade-to-black", 0, "Fade Path D video and burned Title Overlays to #000000 black after this duration (e.g. 15m; 0 disables).")
	fs.BoolVar(&cfg.DimTitles, "dim-titles", false, "Use low-luminance translucent warm-gray ASS styling for Title Overlays.")
	fs.StringVar(&cfg.TrackMedia, "track-media", string(mixer.TrackMediaOff), "Per-Track visual attachment mode: off, auto (sidecar/ID3), or <manifest.json>.")
	fs.StringVar(&cfg.TrackMediaLayout, "track-media-layout", string(mixer.TrackMediaLayoutAuto), "Default layout for per-Track visuals: auto, fullscreen, or card.")
	fs.StringVar(&cfg.TrackTransition, "track-transition", string(render.TransitionCut), "Visual transition across Track segment boundaries: cut or dip.")

	var dummyList bool
	listTarget := &dummyList
	if listVisualizers != nil {
		listTarget = listVisualizers
	}
	fs.BoolVar(listTarget, "list-visualizers", false, "List all built-in Path D Auto Visualizer presets and Color Themes and exit.")

	var dummyEstimate bool
	estimateTarget := &dummyEstimate
	if estimate != nil {
		estimateTarget = estimate
	}
	fs.BoolVar(estimateTarget, "estimate", false, "Probe Tracks and Background Visual, print the estimated render duration breakdown, and exit without rendering.")
	return fs
}

// ParseFlags parses CLI arguments into a mixer.Config.
func ParseFlags(args []string) (mixer.Config, error) {
	cfg, _, err := ParseCLIOptions(args)
	return cfg, err
}

// ParseCLIFlags parses CLI arguments into a mixer.Config and reports whether --list-visualizers was requested.
func ParseCLIFlags(args []string) (mixer.Config, bool, error) {
	cfg, opts, err := ParseCLIOptions(args)
	return cfg, opts.ListVisualizers, err
}

// ParseCLIOptions parses CLI arguments into a mixer.Config and auxiliary CLIOptions.
func ParseCLIOptions(args []string) (mixer.Config, CLIOptions, error) {
	var cfg mixer.Config
	var opts CLIOptions
	fs := NewCLIFlagSetWithOptions(&cfg, &opts, io.Discard)
	if err := fs.Parse(args); err != nil {
		return mixer.Config{}, CLIOptions{}, err
	}
	if _, ok := render.LookupResolution(cfg.VisualizerResolution); !ok {
		return mixer.Config{}, CLIOptions{}, fmt.Errorf("unknown --visualizer-resolution %q (supported: 1080p, 1440p, 4k)", cfg.VisualizerResolution)
	}
	if cfg.VisualizerBrightness < render.MinVisualizerBrightness || cfg.VisualizerBrightness > render.MaxVisualizerBrightness {
		return mixer.Config{}, CLIOptions{}, fmt.Errorf("--visualizer-brightness must be between %d and %d (got %d)", render.MinVisualizerBrightness, render.MaxVisualizerBrightness, cfg.VisualizerBrightness)
	}
	if cfg.FadeToBlack < 0 {
		return mixer.Config{}, CLIOptions{}, fmt.Errorf("--fade-to-black must be >= 0 (got %s)", cfg.FadeToBlack)
	}
	if err := mixer.ValidateTrackMedia(cfg.TrackMedia); err != nil {
		return mixer.Config{}, CLIOptions{}, err
	}
	if _, err := mixer.ValidateTrackMediaLayout(cfg.TrackMediaLayout); err != nil {
		return mixer.Config{}, CLIOptions{}, err
	}
	if _, err := render.ValidateTrackTransition(render.TrackTransition(cfg.TrackTransition)); err != nil {
		return mixer.Config{}, CLIOptions{}, err
	}
	tm := strings.TrimSpace(cfg.TrackMedia)
	if tm != "" && !strings.EqualFold(tm, string(mixer.TrackMediaOff)) {
		cfg.Segmented = true
	}
	return cfg, opts, nil
}

// RunEstimate executes a zero-side-effect dry-run estimate without creating output directories or running FFmpeg render.
func RunEstimate(ctx context.Context, cfg mixer.Config, w io.Writer, lookPath func(string) (string, error), runner mixer.CommandRunner) error {
	preparedCfg, pathKind, err := mixer.PreflightConfig(cfg, lookPath, false)
	if err != nil {
		return err
	}
	if pathKind == render.PathD && runner != nil {
		if err := mixer.ValidateVisualizerFilters(ctx, preparedCfg, runner, nil); err != nil {
			return err
		}
	}
	var tracks []mixer.Track
	if preparedCfg.Segmented {
		tracks, err = mixer.ProbeTracksWithArt(ctx, preparedCfg.AudioDir, preparedCfg.Shuffle, nil, runner)
	} else {
		tracks, err = mixer.ProbeTracks(ctx, preparedCfg.AudioDir, preparedCfg.Shuffle, nil, runner)
	}
	if err != nil {
		return err
	}
	est, err := mixer.EstimateConfig(ctx, preparedCfg, tracks, nil, lookPath, runner)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, mixer.FormatEstimateReport(preparedCfg, len(tracks), est))
	return err
}

// PrintVisualizers prints all built-in Path D Auto Visualizer presets (with live FFmpeg filter availability) and Color Themes.
func PrintVisualizers(ctx context.Context, w io.Writer, lookPath func(string) (string, error), runner mixer.CommandRunner) error {
	var avail map[string]bool
	hasFFmpeg := false
	if lookPath != nil && runner != nil {
		if _, err := lookPath("ffmpeg"); err == nil {
			if detected, err := render.DetectFilters(ctx, runner); err == nil {
				avail = detected
				hasFFmpeg = true
			}
		}
	}

	if _, err := fmt.Fprintln(w, "Path D Auto Visualizer Presets (--visualizer):"); err != nil {
		return err
	}
	for _, spec := range render.ListVisualizers() {
		status := "unknown (ffmpeg not on PATH)"
		if hasFFmpeg {
			if ok, missing := render.PresetAvailable(spec.ID, avail); ok {
				status = "ready"
			} else {
				status = fmt.Sprintf("unavailable (missing %s)", missing)
			}
		}
		if _, err := fmt.Fprintf(w, "  %-12s  %-8s  %-18s  %-14s  %s\n", spec.ID, spec.Category, spec.Filter, status, spec.Description); err != nil {
			return err
		}
	}

	if _, err := fmt.Fprintln(w, "\nPath D Color Themes (--visualizer-theme):"); err != nil {
		return err
	}
	for _, th := range render.ListThemes() {
		if _, err := fmt.Fprintf(w, "  %-14s  %-24s  primary=%s secondary=%s bg=%s\n", th.ID, th.Name, th.Primary, th.Secondary, th.BG); err != nil {
			return err
		}
	}

	if _, err := fmt.Fprintln(w, "\nPath D Output Resolutions (--visualizer-resolution):"); err != nil {
		return err
	}
	for _, res := range render.ListResolutions() {
		if _, err := fmt.Fprintf(w, "  %-8s  %-14s  %dx%d  %s\n", res.ID, res.Name, res.Width, res.Height, res.Description); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if IsMCPSubcommand(os.Args[1:]) {
		if err := ParseMCPFlags(os.Args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				fs := NewMCPFlagSet(os.Stderr)
				fs.Usage()
				os.Exit(0)
			}
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(2)
		}
		if err := runMCPServer(ctx, os.Stdin, os.Stdout, os.Stderr, exec.LookPath, mixer.ExecCommandRunner, runtime.GOOS); err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if IsUISubcommand(os.Args[1:]) {
		uiCfg, err := ParseUIFlags(os.Args[2:])
		if err != nil {
			if errors.Is(err, flag.ErrHelp) {
				fs := NewUIFlagSet(&UIConfig{}, os.Stderr)
				fs.Usage()
				os.Exit(0)
			}
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(2)
		}
		if err := runUIServer(ctx, uiCfg, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	cfg, opts, err := ParseCLIOptions(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fs := NewCLIFlagSetWithOptions(&mixer.Config{}, &CLIOptions{}, os.Stderr)
			fs.Usage()
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(2)
	}
	if opts.ListVisualizers {
		if err := PrintVisualizers(ctx, os.Stdout, exec.LookPath, mixer.ExecCommandRunner); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if opts.Estimate {
		if err := RunEstimate(ctx, cfg, os.Stdout, exec.LookPath, mixer.ExecCommandRunner); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	isTTY := isCharDevice(os.Stdout)
	if err := mixer.Run(ctx, cfg, mixer.NewCLITelemetry(os.Stdout, isTTY), exec.LookPath, mixer.ExecCommandRunner, mixer.ExecStreamRunner); err != nil {
		if isTTY {
			fmt.Fprintln(os.Stdout)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func isCharDevice(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

func runUIServer(ctx context.Context, uiCfg UIConfig, out io.Writer) error {
	srv, err := ui.NewServer(ui.Config{
		Host:            uiCfg.Host,
		Port:            uiCfg.Port,
		AudioDir:        uiCfg.AudioDir,
		BG:              uiCfg.BG,
		OutDir:          uiCfg.OutDir,
		Out:             uiCfg.Out,
		Timestamps:      uiCfg.Timestamps,
		AllowDirs:       uiCfg.AllowDirs,
		IncludeHomeRoot: true,
		LookPath:        exec.LookPath,
		Runner:          mixer.ExecCommandRunner,
		StreamRunner:    mixer.ExecStreamRunner,
		GOOS:            runtime.GOOS,
	})
	if err != nil {
		return err
	}
	defer srv.Close()

	listenAddr := net.JoinHostPort(uiCfg.Host, strconv.Itoa(uiCfg.Port))
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", listenAddr, err)
	}
	defer func() { _ = ln.Close() }()

	actualPort := uiCfg.Port
	if tcpAddr, ok := ln.Addr().(*net.TCPAddr); ok {
		actualPort = tcpAddr.Port
	}
	displayHost := uiCfg.Host
	isWildcardBind := displayHost == "0.0.0.0" || displayHost == "::"
	if isWildcardBind {
		displayHost = "127.0.0.1"
	}
	url := fmt.Sprintf("http://%s", net.JoinHostPort(displayHost, strconv.Itoa(actualPort)))
	fmt.Fprintf(out, "Editorial Mix Studio listening on %s (bound: %s)\n", url, ln.Addr().String())
	if !uiCfg.NoOpen && !isWildcardBind {
		openBrowserBestEffort(ctx, url)
	}

	httpSrv := srv.HTTPServer(ln.Addr().String())
	errCh := make(chan error, 1)
	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		srv.Close()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
		return <-errCh
	}
}

func openBrowserBestEffort(ctx context.Context, url string) {
	if !strings.HasPrefix(url, "http://") {
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// #nosec G204 -- opens validated local http:// URL in system browser via discrete args.
		cmd = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		// #nosec G204 -- opens validated local http:// URL in system browser via discrete args.
		cmd = exec.CommandContext(ctx, "open", url)
	default:
		// #nosec G204 -- opens validated local http:// URL in system browser via discrete args.
		cmd = exec.CommandContext(ctx, "xdg-open", url)
	}
	_ = cmd.Start()
}

func runMCPServer(
	ctx context.Context,
	in io.Reader,
	out io.Writer,
	errOut io.Writer,
	lookPath func(string) (string, error),
	runner mcp.CommandRunner,
	goos string,
) error {
	srv := mcp.NewServer(mcp.ServerConfig{
		In:     in,
		Out:    out,
		ErrOut: errOut,
		Info: mcp.ServerInfo{
			Name:    "ytmix",
			Version: version,
		},
	})
	mcp.RegisterCapabilitiesTool(srv.Registry, mcp.CapabilitiesDeps{
		LookPath: lookPath,
		Runner:   runner,
		GOOS:     goos,
	})
	mcp.RegisterInspectTools(srv.Registry, mcp.InspectDeps{
		Runner: runner,
	})
	mcp.RegisterPlanTools(srv.Registry, mcp.PlanDeps{
		LookPath: lookPath,
		Runner:   runner,
	})
	mcp.RegisterResourcesAndPrompts(srv.Registry, mcp.ResourcesPromptsDeps{
		LookPath: lookPath,
		Runner:   runner,
	})
	mgr := mcp.RegisterRenderTools(srv.Registry, mcp.RenderDeps{
		LookPath:     lookPath,
		Runner:       runner,
		StreamRunner: mixer.ExecStreamRunner,
	})
	defer mgr.Close()
	return srv.Serve(ctx)
}
