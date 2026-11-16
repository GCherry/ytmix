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

	"github.com/GCherry/ytmix/internal/mixer"
	"github.com/GCherry/ytmix/internal/ui"
)

// UIConfig holds the CLI configuration for the `ytmix ui` local web server subcommand.
type UIConfig struct {
	Host     string
	Port     int
	NoOpen   bool
	AudioDir string
	BG       string
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

// NewFlagSet configures the CLI flag definitions for ytmix.
func NewFlagSet(cfg *mixer.Config, output io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("ytmix", flag.ContinueOnError)
	fs.SetOutput(output)

	fs.StringVar(&cfg.AudioDir, "audio-dir", "./tracks", "Directory containing input .mp3 files.")
	fs.StringVar(&cfg.BG, "bg", "", "Path to Background Visual (.jpg, .jpeg, .png, .webp, .mp4, .mov, .gif).")
	fs.StringVar(&cfg.Out, "out", "output.mp4", "Output .mp4 video path.")
	fs.StringVar(&cfg.Timestamps, "timestamps", "timestamps.txt", "Output file path for YouTube Chapter Timestamps.")
	fs.Float64Var(&cfg.Crossfade, "crossfade", 0, "Crossfade duration in seconds between Tracks.")
	fs.BoolVar(&cfg.Normalize, "normalize", false, "Normalize each Track to -14 LUFS.")
	fs.BoolVar(&cfg.OverlayTitles, "overlay-titles", false, "Burn Display Titles into the bottom-left corner via .ass subtitles.")
	fs.IntVar(&cfg.TitleDuration, "title-duration", 0, "Seconds to display each Display Title (0 = full Track).")
	fs.StringVar(&cfg.FontName, "font-name", "Arial", "Font family name used in the .ass subtitle script.")
	fs.BoolVar(&cfg.Shuffle, "shuffle", false, "Randomize Track order instead of sorting alphabetically.")
	return fs
}

// ParseFlags parses CLI arguments into a mixer.Config.
func ParseFlags(args []string) (mixer.Config, error) {
	var cfg mixer.Config
	fs := NewFlagSet(&cfg, io.Discard)
	if err := fs.Parse(args); err != nil {
		return mixer.Config{}, err
	}
	return cfg, nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

	cfg, err := ParseFlags(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fs := NewFlagSet(&mixer.Config{}, os.Stderr)
			fs.Usage()
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(2)
	}

	if err := mixer.Run(ctx, cfg, os.Stdout, exec.LookPath, mixer.ExecCommandRunner); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runUIServer(ctx context.Context, uiCfg UIConfig, out io.Writer) error {
	srv, err := ui.NewServer(ui.Config{
		Host:     uiCfg.Host,
		Port:     uiCfg.Port,
		AudioDir: uiCfg.AudioDir,
		BG:       uiCfg.BG,
		LookPath: exec.LookPath,
		Runner:   mixer.ExecCommandRunner,
		GOOS:     runtime.GOOS,
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
