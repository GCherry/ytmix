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

func TestBuildAndWriteASSWithOptions(t *testing.T) {
	cases := []struct {
		name          string
		titles        []string
		starts        []float64
		totalDuration float64
		opts          ASSOptions
		wantStyle     string
		wantPrimary   string
		forbidPrimary string
		wantExtraSubs []string
		wantDialogues []string
	}{
		{
			name:          "dim false keeps default bright style",
			titles:        []string{"Dawn {Intro}", `Night \ Horizon`},
			starts:        []float64{0.0, 120.5},
			totalDuration: 200.25,
			opts: ASSOptions{
				TitleDuration: 0,
				FontName:      "Inter, Bold\n",
				DimTitles:     false,
			},
			wantStyle:     "Style: Default,Inter Bold,42,&H00FFFFFF,&H000000FF,&H00000000,&H80000000,0,0,0,0,100,100,0,0,1,2,1,1,60,60,55,1",
			wantPrimary:   "&H00FFFFFF",
			forbidPrimary: "&H789EB2AF",
			wantDialogues: []string{
				`Dialogue: 0,0:00:00.00,0:02:00.50,Default,,0,0,0,,{\fad(500,500)}♪ Dawn \{Intro\}`,
				`Dialogue: 0,0:02:00.50,0:03:20.25,Default,,0,0,0,,{\fad(500,500)}♪ Night \\ Horizon`,
			},
		},
		{
			name:          "dim true emits low-luminance style with font sanitization and escaping",
			titles:        []string{"Sleep {Drift}\r\nPhase", `Deep \ Rain`},
			starts:        []float64{0.0, 30.0},
			totalDuration: 90.0,
			opts: ASSOptions{
				TitleDuration: 15,
				FontName:      "Inter, Light\r\n",
				DimTitles:     true,
			},
			wantStyle:     "Style: Default,Inter Light,42,&H789EB2AF,&H000000FF,&H90000000,&HB0000000,0,0,0,0,100,100,0,0,1,1,0,1,60,60,55,1",
			wantPrimary:   "&H789EB2AF",
			forbidPrimary: "&H00FFFFFF",
			wantExtraSubs: []string{"&H90000000", "&HB0000000", `{\fad(500,500)}`},
			wantDialogues: []string{
				`Dialogue: 0,0:00:00.00,0:00:15.00,Default,,0,0,0,,{\fad(500,500)}♪ Sleep \{Drift\} Phase`,
				`Dialogue: 0,0:00:30.00,0:00:45.00,Default,,0,0,0,,{\fad(500,500)}♪ Deep \\ Rain`,
			},
		},
		{
			name:          "dim true with blank font falls back to Arial",
			titles:        []string{"Quiet Meadow"},
			starts:        []float64{0.0},
			totalDuration: 12.0,
			opts: ASSOptions{
				TitleDuration: 8,
				FontName:      "  \n\t ",
				DimTitles:     true,
			},
			wantStyle:     "Style: Default,Arial,42,&H789EB2AF,&H000000FF,&H90000000,&HB0000000,0,0,0,0,100,100,0,0,1,1,0,1,60,60,55,1",
			wantPrimary:   "&H789EB2AF",
			forbidPrimary: "&H00FFFFFF",
			wantExtraSubs: []string{"&H90000000", `{\fad(500,500)}`},
			wantDialogues: []string{
				`Dialogue: 0,0:00:00.00,0:00:08.00,Default,,0,0,0,,{\fad(500,500)}♪ Quiet Meadow`,
			},
		},
	}

	for _, tc := range cases {
		t.Run("BuildASSWithOptions/"+tc.name, func(t *testing.T) {
			ass := BuildASSWithOptions(tc.titles, tc.starts, tc.totalDuration, tc.opts)

			if !strings.Contains(ass, tc.wantStyle) {
				t.Errorf("ASS missing expected style %q\nfull ASS:\n%s", tc.wantStyle, ass)
			}
			if !strings.Contains(ass, tc.wantPrimary) {
				t.Errorf("ASS missing primary colour %q\nfull ASS:\n%s", tc.wantPrimary, ass)
			}
			if strings.Contains(ass, tc.forbidPrimary) {
				t.Errorf("ASS unexpectedly contained %q\nfull ASS:\n%s", tc.forbidPrimary, ass)
			}
			for _, sub := range tc.wantExtraSubs {
				if !strings.Contains(ass, sub) {
					t.Errorf("ASS missing %q\nfull ASS:\n%s", sub, ass)
				}
			}
			for _, dlg := range tc.wantDialogues {
				if !strings.Contains(ass, dlg) {
					t.Errorf("ASS missing dialogue %q\nfull ASS:\n%s", dlg, ass)
				}
			}
		})

		t.Run("WriteASSWithOptions/"+tc.name, func(t *testing.T) {
			outPath := filepath.Join(t.TempDir(), "titles.ass")
			if err := WriteASSWithOptions(outPath, tc.titles, tc.starts, tc.totalDuration, tc.opts); err != nil {
				t.Fatalf("WriteASSWithOptions failed: %v", err)
			}
			data, err := os.ReadFile(outPath)
			if err != nil {
				t.Fatalf("read written ASS: %v", err)
			}
			got := string(data)
			want := BuildASSWithOptions(tc.titles, tc.starts, tc.totalDuration, tc.opts)
			if got != want {
				t.Fatalf("written ASS differs from BuildASSWithOptions\ngot:\n%s\nwant:\n%s", got, want)
			}
		})
	}

	t.Run("BuildASS delegates identically to BuildASSWithOptions with DimTitles false", func(t *testing.T) {
		titles := []string{"First {Song}", `Second \ Song`}
		starts := []float64{0.0, 42.5}
		got := BuildASS(titles, starts, 100.0, 12, "Roboto")
		want := BuildASSWithOptions(titles, starts, 100.0, ASSOptions{
			TitleDuration: 12,
			FontName:      "Roboto",
			DimTitles:     false,
		})
		if got != want {
			t.Fatalf("BuildASS != BuildASSWithOptions\ngot:\n%s\nwant:\n%s", got, want)
		}
	})
}
