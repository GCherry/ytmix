package subtitle

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatASSTime(t *testing.T) {
	cases := []struct {
		name    string
		seconds float64
		want    string
	}{
		{"zero", 0, "0:00:00.00"},
		{"negative clamped", -3.5, "0:00:00.00"},
		{"nan clamped", math.NaN(), "0:00:00.00"},
		{"fractional centiseconds", 165.1, "0:02:45.10"},
		{"rounds centiseconds cleanly", 1.236, "0:00:01.24"},
		{"centisecond rollover to next second", 4.996, "0:00:05.00"},
		{"centisecond rollover to next minute", 59.999, "0:01:00.00"},
		{"centisecond rollover to next hour", 3599.995, "1:00:00.00"},
		{"multi-hour", 3661.05, "1:01:01.05"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatASSTime(tc.seconds); got != tc.want {
				t.Fatalf("FormatASSTime(%v) = %q, want %q", tc.seconds, got, tc.want)
			}
		})
	}
}

func TestEscapeASSText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain text", "Midnight Breeze", "Midnight Breeze"},
		{"braces and backslash", `Track {1} \ Remix`, `Track \{1\} \\ Remix`},
		{"normalizes newlines and crlf", "Line1\r\nLine2\nLine3\rEnd", "Line1 Line2 Line3 End"},
		{"unicode and apostrophes", "L'Été — Café ♪", "L'Été — Café ♪"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EscapeASSText(tc.in); got != tc.want {
				t.Fatalf("EscapeASSText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBuildAndWriteASS(t *testing.T) {
	t.Run("header, style fields, font sanitization, and full-track title-duration=0", func(t *testing.T) {
		titles := []string{"Dawn {Intro}", `Night \ Horizon`}
		starts := []float64{0.0, 120.5}
		totalDuration := 200.25

		ass := BuildASS(titles, starts, totalDuration, 0, "Inter, Bold\n")

		for _, wantSub := range []string{
			"[Script Info]",
			"ScriptType: v4.00+",
			"PlayResX: 1920",
			"PlayResY: 1080",
			"[V4+ Styles]",
			"Style: Default,Inter Bold,42,&H00FFFFFF,&H000000FF,&H00000000,&H80000000,0,0,0,0,100,100,0,0,1,2,1,1,60,60,55,1",
			"[Events]",
			`Dialogue: 0,0:00:00.00,0:02:00.50,Default,,0,0,0,,{\fad(500,500)}♪ Dawn \{Intro\}`,
			`Dialogue: 0,0:02:00.50,0:03:20.25,Default,,0,0,0,,{\fad(500,500)}♪ Night \\ Horizon`,
		} {
			if !strings.Contains(ass, wantSub) {
				t.Errorf("ASS missing %q\nfull ASS:\n%s", wantSub, ass)
			}
		}
	})

	t.Run("blank font falls back to Arial and title-duration>0 clamps to next boundary", func(t *testing.T) {
		titles := []string{"Short Track", "Long Track", "Final Track"}
		starts := []float64{0.0, 3.0, 20.0}
		totalDuration := 24.0

		// titleDuration=10:
		// Track 0: min(0+10, 3) = 3.0
		// Track 1: min(3+10, 20) = 13.0
		// Track 2: min(20+10, 24) = 24.0
		ass := BuildASS(titles, starts, totalDuration, 10, "   ")

		if !strings.Contains(ass, "Style: Default,Arial,42,") {
			t.Errorf("expected default Arial font, got:\n%s", ass)
		}
		for _, wantLine := range []string{
			`Dialogue: 0,0:00:00.00,0:00:03.00,Default,,0,0,0,,{\fad(500,500)}♪ Short Track`,
			`Dialogue: 0,0:00:03.00,0:00:13.00,Default,,0,0,0,,{\fad(500,500)}♪ Long Track`,
			`Dialogue: 0,0:00:20.00,0:00:24.00,Default,,0,0,0,,{\fad(500,500)}♪ Final Track`,
		} {
			if !strings.Contains(ass, wantLine) {
				t.Errorf("missing clamped line %q\nfull ASS:\n%s", wantLine, ass)
			}
		}
	})

	t.Run("WriteASS writes script to disk", func(t *testing.T) {
		outPath := filepath.Join(t.TempDir(), "titles.ass")
		err := WriteASS(outPath, []string{"Alpha"}, []float64{0.0}, 10.0, 5, "Roboto")
		if err != nil {
			t.Fatalf("WriteASS failed: %v", err)
		}
		data, err := os.ReadFile(outPath)
		if err != nil {
			t.Fatalf("read written ASS: %v", err)
		}
		if !strings.Contains(string(data), `Dialogue: 0,0:00:00.00,0:00:05.00,Default,,0,0,0,,{\fad(500,500)}♪ Alpha`) {
			t.Fatalf("unexpected written ASS:\n%s", string(data))
		}
	})
}
