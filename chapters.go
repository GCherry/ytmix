package main

import (
	"fmt"
	"math"
	"os"
	"strings"
)

// ChapterSchedule holds cumulative Track start times (seconds), total Mix duration,
// and the formatted YouTube Chapter Timestamps text.
type ChapterSchedule struct {
	Starts        []float64
	TotalDuration float64
	Content       string
}

func formatChapterTime(seconds float64, includeHours bool) string {
	totalSec := int(math.Round(seconds))
	if totalSec < 0 {
		totalSec = 0
	}
	if includeHours {
		hours := totalSec / 3600
		mins := (totalSec % 3600) / 60
		secs := totalSec % 60
		return fmt.Sprintf("%02d:%02d:%02d", hours, mins, secs)
	}
	mins := totalSec / 60
	secs := totalSec % 60
	return fmt.Sprintf("%02d:%02d", mins, secs)
}

func buildChapterSchedule(tracks []Track, crossfade float64) ChapterSchedule {
	if len(tracks) == 0 {
		return ChapterSchedule{}
	}

	starts := make([]float64, len(tracks))
	starts[0] = 0.0
	for i := 1; i < len(tracks); i++ {
		nextStart := starts[i-1] + tracks[i-1].Duration - crossfade
		if nextStart < 0 {
			nextStart = 0
		}
		starts[i] = nextStart
	}

	totalDuration := starts[len(tracks)-1] + tracks[len(tracks)-1].Duration
	includeHours := totalDuration >= 3600.0

	var b strings.Builder
	for i, tr := range tracks {
		b.WriteString(formatChapterTime(starts[i], includeHours))
		b.WriteByte(' ')
		b.WriteString(tr.DisplayTitle)
		b.WriteByte('\n')
	}

	return ChapterSchedule{
		Starts:        starts,
		TotalDuration: totalDuration,
		Content:       b.String(),
	}
}

func writeChapters(path string, tracks []Track, crossfade float64) (ChapterSchedule, error) {
	sched := buildChapterSchedule(tracks, crossfade)
	if err := os.WriteFile(path, []byte(sched.Content), 0644); err != nil {
		return ChapterSchedule{}, fmt.Errorf("write Chapter Timestamps to %q: %w", path, err)
	}
	return sched, nil
}
