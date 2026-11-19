package subtitle

import (
	"fmt"
	"math"
	"os"
	"strings"
)

var assTextEscaper = strings.NewReplacer(
	"\r\n", " ",
	"\r", " ",
	"\n", " ",
	`\`, `\\`,
	`{`, `\{`,
	`}`, `\}`,
)

var fontNameSanitizer = strings.NewReplacer(
	",", "",
	"\r", "",
	"\n", "",
)

// FormatASSTime formats seconds as H:MM:SS.cc (centiseconds) with clean carryover rounding.
func FormatASSTime(seconds float64) string {
	if math.IsNaN(seconds) || math.IsInf(seconds, -1) || seconds <= 0 {
		return "0:00:00.00"
	}
	totalCs := int64(math.Round(seconds * 100))
	if totalCs < 0 {
		totalCs = 0
	}
	cs := totalCs % 100
	totalSec := totalCs / 100
	sec := totalSec % 60
	min := (totalSec / 60) % 60
	hr := totalSec / 3600
	return fmt.Sprintf("%d:%02d:%02d.%02d", hr, min, sec, cs)
}

// EscapeASSText escapes ASS control characters (\, {, }) and normalizes line breaks
// so Display Titles cannot break an ASS Dialogue line or inject override tags.
func EscapeASSText(s string) string {
	return assTextEscaper.Replace(s)
}

func sanitizeFontName(fontName string) string {
	cleaned := strings.TrimSpace(fontNameSanitizer.Replace(fontName))
	if cleaned == "" {
		return "Arial"
	}
	return cleaned
}

// ASSOptions configures optional Advanced SubStation Alpha (.ass) styling and timing behavior.
type ASSOptions struct {
	TitleDuration int
	FontName      string
	DimTitles     bool
}

// BuildASSWithOptions builds an Advanced SubStation Alpha (.ass) subtitle script for bottom-left
// Track Title Overlays with 500ms fade-in/out tags using the provided ASSOptions.
func BuildASSWithOptions(titles []string, starts []float64, totalDuration float64, opts ASSOptions) string {
	font := sanitizeFontName(opts.FontName)
	primaryColour := "&H00FFFFFF"
	outlineColour := "&H00000000"
	backColour := "&H80000000"
	outline := 2
	shadow := 1
	if opts.DimTitles {
		primaryColour = "&H789EB2AF"
		outlineColour = "&H90000000"
		backColour = "&HB0000000"
		outline = 1
		shadow = 0
	}

	var b strings.Builder
	b.WriteString("[Script Info]\n")
	b.WriteString("ScriptType: v4.00+\n")
	b.WriteString("PlayResX: 1920\n")
	b.WriteString("PlayResY: 1080\n\n")

	b.WriteString("[V4+ Styles]\n")
	b.WriteString("Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\n")
	fmt.Fprintf(
		&b,
		"Style: Default,%s,42,%s,&H000000FF,%s,%s,0,0,0,0,100,100,0,0,1,%d,%d,1,60,60,55,1\n\n",
		font, primaryColour, outlineColour, backColour, outline, shadow,
	)

	b.WriteString("[Events]\n")
	b.WriteString("Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n")

	n := min(len(titles), len(starts))
	for i := 0; i < n; i++ {
		start := starts[i]
		nextBoundary := totalDuration
		if i+1 < len(starts) && i+1 < n {
			nextBoundary = starts[i+1]
		}
		end := nextBoundary
		if opts.TitleDuration > 0 {
			if candidate := start + float64(opts.TitleDuration); candidate < end {
				end = candidate
			}
		}
		if end < start {
			end = start
		}
		fmt.Fprintf(
			&b,
			"Dialogue: 0,%s,%s,Default,,0,0,0,,{\\fad(500,500)}♪ %s\n",
			FormatASSTime(start),
			FormatASSTime(end),
			EscapeASSText(titles[i]),
		)
	}

	return b.String()
}

// BuildASS builds an Advanced SubStation Alpha (.ass) subtitle script for bottom-left
// Track Title Overlays with 500ms fade-in/out tags.
func BuildASS(titles []string, starts []float64, totalDuration float64, titleDuration int, fontName string) string {
	return BuildASSWithOptions(titles, starts, totalDuration, ASSOptions{
		TitleDuration: titleDuration,
		FontName:      fontName,
	})
}

// WriteASSWithOptions generates an ASS subtitle script with opts and writes it to path.
func WriteASSWithOptions(path string, titles []string, starts []float64, totalDuration float64, opts ASSOptions) error {
	content := BuildASSWithOptions(titles, starts, totalDuration, opts)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return fmt.Errorf("write ASS subtitles to %q: %w", path, err)
	}
	return nil
}

// WriteASS generates an ASS subtitle script and writes it to path.
func WriteASS(path string, titles []string, starts []float64, totalDuration float64, titleDuration int, fontName string) error {
	return WriteASSWithOptions(path, titles, starts, totalDuration, ASSOptions{
		TitleDuration: titleDuration,
		FontName:      fontName,
	})
}
