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
	totalCs := max(int64(math.Round(seconds*100)), 0)
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
func BuildASSWithOptions(cues []TitleCue, totalDuration float64, opts ASSOptions) string {
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

	for i, cue := range cues {
		start := cue.Start
		nextBoundary := totalDuration
		if i+1 < len(cues) {
			nextBoundary = cues[i+1].Start
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
			EscapeASSText(cue.Title),
		)
	}

	return b.String()
}

// BuildASS builds an Advanced SubStation Alpha (.ass) subtitle script for bottom-left
// Track Title Overlays with 500ms fade-in/out tags.
func BuildASS(cues []TitleCue, totalDuration float64, titleDuration int, fontName string) string {
	return BuildASSWithOptions(cues, totalDuration, ASSOptions{
		TitleDuration: titleDuration,
		FontName:      fontName,
	})
}

// WriteASSWithOptions generates an ASS subtitle script with opts and writes it to path.
func WriteASSWithOptions(path string, cues []TitleCue, totalDuration float64, opts ASSOptions) error {
	content := BuildASSWithOptions(cues, totalDuration, opts)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return fmt.Errorf("write ASS subtitles to %q: %w", path, err)
	}
	return nil
}

// WriteASS generates an ASS subtitle script and writes it to path.
func WriteASS(path string, cues []TitleCue, totalDuration float64, titleDuration int, fontName string) error {
	return WriteASSWithOptions(path, cues, totalDuration, ASSOptions{
		TitleDuration: titleDuration,
		FontName:      fontName,
	})
}

// BuildSegmentASSWithOptions builds an ASS subtitle script for a single per-Track segment
// whose cue starts at 0.0 and ends at slotDuration (clamped by opts.TitleDuration when > 0).
func BuildSegmentASSWithOptions(title string, slotDuration float64, opts ASSOptions) string {
	if math.IsNaN(slotDuration) || math.IsInf(slotDuration, 0) || slotDuration < 0 {
		slotDuration = 0
	}
	return BuildASSWithOptions([]TitleCue{{Title: title, Start: 0.0}}, slotDuration, opts)
}

// WriteSegmentASSWithOptions generates a segment-local ASS subtitle script with opts and writes it to path.
func WriteSegmentASSWithOptions(path string, title string, slotDuration float64, opts ASSOptions) error {
	content := BuildSegmentASSWithOptions(title, slotDuration, opts)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return fmt.Errorf("write segment ASS subtitles to %q: %w", path, err)
	}
	return nil
}
