package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/GCherry/ytmix/internal/mixer"
)

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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := mixer.Run(ctx, cfg, os.Stdout, exec.LookPath, mixer.ExecCommandRunner); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
