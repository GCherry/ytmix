package subtitle

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTimestampsCalculationAndFormatting(t *testing.T) {
	t.Run("cumulative start times without and with crossfade", func(t *testing.T) {
		titles := []string{"Alpha", "Beta", "Gamma"}
		durations := []float64{125.0, 90.0, 60.0}

		noFade := BuildChapterSchedule(titles, durations, 0)
		wantStartsNoFade := []float64{0, 125, 215}
		if !reflect.DeepEqual(noFade.Starts, wantStartsNoFade) || noFade.TotalDuration != 275.0 {
			t.Fatalf("noFade = %+v", noFade)
		}
		wantTextNoFade := "00:00 Alpha\n02:05 Beta\n03:35 Gamma\n"
		if noFade.Content != wantTextNoFade {
			t.Fatalf("noFade.Content =\n%s\nwant:\n%s", noFade.Content, wantTextNoFade)
		}

		withFade := BuildChapterSchedule(titles, durations, 2.0)
		wantStartsFade := []float64{0, 123, 211}
		if !reflect.DeepEqual(withFade.Starts, wantStartsFade) || withFade.TotalDuration != 271.0 {
			t.Fatalf("withFade = %+v", withFade)
		}
		wantTextFade := "00:00 Alpha\n02:03 Beta\n03:31 Gamma\n"
		if withFade.Content != wantTextFade {
			t.Fatalf("withFade.Content =\n%s\nwant:\n%s", withFade.Content, wantTextFade)
		}
	})

	t.Run("switches from MM:SS to HH:MM:SS at exact 3600s boundary", func(t *testing.T) {
		titles := []string{"Part One", "Part Two"}

		schedUnder := BuildChapterSchedule(titles, []float64{3540.0, 59.0}, 0)
		if schedUnder.TotalDuration != 3599.0 {
			t.Fatalf("expected 3599.0, got %v", schedUnder.TotalDuration)
		}
		wantUnder := "00:00 Part One\n59:00 Part Two\n"
		if schedUnder.Content != wantUnder {
			t.Fatalf("underHour =\n%s\nwant:\n%s", schedUnder.Content, wantUnder)
		}

		schedAt := BuildChapterSchedule(titles, []float64{3540.0, 60.0}, 0)
		if schedAt.TotalDuration != 3600.0 {
			t.Fatalf("expected 3600.0, got %v", schedAt.TotalDuration)
		}
		wantAt := "00:00:00 Part One\n00:59:00 Part Two\n"
		if schedAt.Content != wantAt {
			t.Fatalf("atHour =\n%s\nwant:\n%s", schedAt.Content, wantAt)
		}
	})

	t.Run("writes formatted chapters to --timestamps file", func(t *testing.T) {
		dir := t.TempDir()
		outPath := filepath.Join(dir, "custom_timestamps.txt")
		titles := []string{"Intro", "Outro"}
		durations := []float64{4.0, 4.0}
		sched, err := WriteChapters(outPath, titles, durations, 1.0)
		if err != nil {
			t.Fatalf("WriteChapters error: %v", err)
		}
		if sched.TotalDuration != 7.0 {
			t.Fatalf("TotalDuration = %v, want 7.0", sched.TotalDuration)
		}
		data, err := os.ReadFile(outPath)
		if err != nil {
			t.Fatalf("read written timestamps: %v", err)
		}
		want := "00:00 Intro\n00:03 Outro\n"
		if string(data) != want {
			t.Fatalf("written timestamps =\n%s\nwant:\n%s", string(data), want)
		}
	})
}
