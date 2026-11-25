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
		cues := []TitleCue{
			{Title: "Dawn {Intro}", Start: 0.0},
			{Title: `Night \ Horizon`, Start: 120.5},
		}
		totalDuration := 200.25

		ass := BuildASS(cues, totalDuration, 0, "Inter, Bold\n")

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
		cues := []TitleCue{
			{Title: "Short Track", Start: 0.0},
			{Title: "Long Track", Start: 3.0},
			{Title: "Final Track", Start: 20.0},
		}
		totalDuration := 24.0

		// titleDuration=10:
		// Track 0: min(0+10, 3) = 3.0
		// Track 1: min(3+10, 20) = 13.0
		// Track 2: min(20+10, 24) = 24.0
		ass := BuildASS(cues, totalDuration, 10, "   ")

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
		err := WriteASS(outPath, []TitleCue{{Title: "Alpha", Start: 0.0}}, 10.0, 5, "Roboto")
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
		cues          []TitleCue
		totalDuration float64
		opts          ASSOptions
		wantStyle     string
		wantPrimary   string
		forbidPrimary string
		wantExtraSubs []string
		wantDialogues []string
	}{
		{
			name: "dim false keeps default bright style",
			cues: []TitleCue{
				{Title: "Dawn {Intro}", Start: 0.0},
				{Title: `Night \ Horizon`, Start: 120.5},
			},
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
			name: "dim true emits low-luminance style with font sanitization and escaping",
			cues: []TitleCue{
				{Title: "Sleep {Drift}\r\nPhase", Start: 0.0},
				{Title: `Deep \ Rain`, Start: 30.0},
			},
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
			name: "dim true with blank font falls back to Arial",
			cues: []TitleCue{
				{Title: "Quiet Meadow", Start: 0.0},
			},
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
			ass := BuildASSWithOptions(tc.cues, tc.totalDuration, tc.opts)

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
			if err := WriteASSWithOptions(outPath, tc.cues, tc.totalDuration, tc.opts); err != nil {
				t.Fatalf("WriteASSWithOptions failed: %v", err)
			}
			data, err := os.ReadFile(outPath)
			if err != nil {
				t.Fatalf("read written ASS: %v", err)
			}
			got := string(data)
			want := BuildASSWithOptions(tc.cues, tc.totalDuration, tc.opts)
			if got != want {
				t.Fatalf("written ASS differs from BuildASSWithOptions\ngot:\n%s\nwant:\n%s", got, want)
			}
		})
	}

	t.Run("BuildASS delegates identically to BuildASSWithOptions with DimTitles false", func(t *testing.T) {
		cues := []TitleCue{
			{Title: "First {Song}", Start: 0.0},
			{Title: `Second \ Song`, Start: 42.5},
		}
		got := BuildASS(cues, 100.0, 12, "Roboto")
		want := BuildASSWithOptions(cues, 100.0, ASSOptions{
			TitleDuration: 12,
			FontName:      "Roboto",
			DimTitles:     false,
		})
		if got != want {
			t.Fatalf("BuildASS != BuildASSWithOptions\ngot:\n%s\nwant:\n%s", got, want)
		}
	})
}

func TestBuildAndWriteSegmentASSWithOptions(t *testing.T) {
	cases := []struct {
		name          string
		title         string
		slotDuration  float64
		opts          ASSOptions
		wantStyle     string
		wantDialogue  string
		forbidPrimary string
	}{
		{
			name:         "full slot duration when TitleDuration is 0",
			title:        "Midnight Drift",
			slotDuration: 125.5,
			opts: ASSOptions{
				TitleDuration: 0,
				FontName:      "Inter",
				DimTitles:     false,
			},
			wantStyle:     "Style: Default,Inter,42,&H00FFFFFF,&H000000FF,&H00000000,&H80000000,0,0,0,0,100,100,0,0,1,2,1,1,60,60,55,1",
			wantDialogue:  `Dialogue: 0,0:00:00.00,0:02:05.50,Default,,0,0,0,,{\fad(500,500)}♪ Midnight Drift`,
			forbidPrimary: "&H789EB2AF",
		},
		{
			name:         "clamped by TitleDuration when shorter than slotDuration",
			title:        "Sunrise",
			slotDuration: 180.0,
			opts: ASSOptions{
				TitleDuration: 10,
				FontName:      "Roboto",
			},
			wantStyle:    "Style: Default,Roboto,42,&H00FFFFFF,",
			wantDialogue: `Dialogue: 0,0:00:00.00,0:00:10.00,Default,,0,0,0,,{\fad(500,500)}♪ Sunrise`,
		},
		{
			name:         "clamped to slotDuration when TitleDuration exceeds slotDuration",
			title:        "Short Interlude",
			slotDuration: 6.5,
			opts: ASSOptions{
				TitleDuration: 15,
				FontName:      "Roboto",
			},
			wantStyle:    "Style: Default,Roboto,42,&H00FFFFFF,",
			wantDialogue: `Dialogue: 0,0:00:00.00,0:00:06.50,Default,,0,0,0,,{\fad(500,500)}♪ Short Interlude`,
		},
		{
			name:         "DimTitles true with blank font and ASS special character escaping",
			title:        "Dream {Mix} \\ Line1\r\nLine2",
			slotDuration: 45.25,
			opts: ASSOptions{
				TitleDuration: 12,
				FontName:      "  \n\t ",
				DimTitles:     true,
			},
			wantStyle:     "Style: Default,Arial,42,&H789EB2AF,&H000000FF,&H90000000,&HB0000000,0,0,0,0,100,100,0,0,1,1,0,1,60,60,55,1",
			wantDialogue:  `Dialogue: 0,0:00:00.00,0:00:12.00,Default,,0,0,0,,{\fad(500,500)}♪ Dream \{Mix\} \\ Line1 Line2`,
			forbidPrimary: "&H00FFFFFF",
		},
		{
			name:         "sanitizes commas and newlines in FontName",
			title:        "Forest",
			slotDuration: 20.0,
			opts: ASSOptions{
				FontName: "Inter, Bold\r\n",
			},
			wantStyle:    "Style: Default,Inter Bold,42,",
			wantDialogue: `Dialogue: 0,0:00:00.00,0:00:20.00,Default,,0,0,0,,{\fad(500,500)}♪ Forest`,
		},
		{
			name:         "clamps negative slotDuration cleanly to zero",
			title:        "Zeroed",
			slotDuration: -5.0,
			opts: ASSOptions{
				TitleDuration: 10,
			},
			wantStyle:    "Style: Default,Arial,42,",
			wantDialogue: `Dialogue: 0,0:00:00.00,0:00:00.00,Default,,0,0,0,,{\fad(500,500)}♪ Zeroed`,
		},
	}

	for _, tc := range cases {
		t.Run("BuildSegmentASSWithOptions/"+tc.name, func(t *testing.T) {
			ass := BuildSegmentASSWithOptions(tc.title, tc.slotDuration, tc.opts)
			if !strings.Contains(ass, tc.wantStyle) {
				t.Errorf("ASS missing style %q\nfull ASS:\n%s", tc.wantStyle, ass)
			}
			if !strings.Contains(ass, tc.wantDialogue) {
				t.Errorf("ASS missing dialogue %q\nfull ASS:\n%s", tc.wantDialogue, ass)
			}
			if tc.forbidPrimary != "" && strings.Contains(ass, tc.forbidPrimary) {
				t.Errorf("ASS unexpectedly contained %q\nfull ASS:\n%s", tc.forbidPrimary, ass)
			}
			if gotCount := strings.Count(ass, "Dialogue: "); gotCount != 1 {
				t.Errorf("expected exactly 1 Dialogue line, got %d:\n%s", gotCount, ass)
			}
		})

		t.Run("WriteSegmentASSWithOptions/"+tc.name, func(t *testing.T) {
			outPath := filepath.Join(t.TempDir(), "segment.ass")
			if err := WriteSegmentASSWithOptions(outPath, tc.title, tc.slotDuration, tc.opts); err != nil {
				t.Fatalf("WriteSegmentASSWithOptions failed: %v", err)
			}
			data, err := os.ReadFile(outPath)
			if err != nil {
				t.Fatalf("read written segment ASS: %v", err)
			}
			want := BuildSegmentASSWithOptions(tc.title, tc.slotDuration, tc.opts)
			if string(data) != want {
				t.Fatalf("written segment ASS differs from BuildSegmentASSWithOptions\ngot:\n%s\nwant:\n%s", string(data), want)
			}
		})
	}

	t.Run("WriteSegmentASSWithOptions returns wrapped error on write failure", func(t *testing.T) {
		missingParent := filepath.Join(t.TempDir(), "missing", "segment.ass")
		if err := WriteSegmentASSWithOptions(missingParent, "Alpha", 10.0, ASSOptions{}); err == nil {
			t.Fatal("expected error writing to non-existent directory")
		}
	})
}
