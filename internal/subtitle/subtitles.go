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

// BuildASS builds an Advanced SubStation Alpha (.ass) subtitle script for bottom-left
// Track Title Overlays with 500ms fade-in/out tags.
func BuildASS(titles []string, starts []float64, totalDuration float64, titleDuration int, fontName string) string {
	font := sanitizeFontName(fontName)

	var b strings.Builder
	b.WriteString("[Script Info]\n")
	b.WriteString("ScriptType: v4.00+\n")
	b.WriteString("PlayResX: 1920\n")
	b.WriteString("PlayResY: 1080\n\n")

	b.WriteString("[V4+ Styles]\n")
	b.WriteString("Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\n")
	fmt.Fprintf(&b, "Style: Default,%s,42,&H00FFFFFF,&H000000FF,&H00000000,&H80000000,0,0,0,0,100,100,0,0,1,2,1,1,60,60,55,1\n\n", font)

	b.WriteString("[Events]\n")
	b.WriteString("Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n")

	n := len(titles)
	if len(starts) < n {
		n = len(starts)
	}
	for i := 0; i < n; i++ {
		start := starts[i]
		nextBoundary := totalDuration
		if i+1 < n {
			nextBoundary = starts[i+1]
		}
		end := nextBoundary
		if titleDuration > 0 {
			if candidate := start + float64(titleDuration); candidate < end {
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

// WriteASS generates an ASS subtitle script and writes it to path.
func WriteASS(path string, titles []string, starts []float64, totalDuration float64, titleDuration int, fontName string) error {
	content := BuildASS(titles, starts, totalDuration, titleDuration, fontName)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("write ASS subtitles to %q: %w", path, err)
	}
	return nil
}
