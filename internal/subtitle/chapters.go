package subtitle

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

// FormatChapterTime formats seconds as MM:SS or HH:MM:SS.
func FormatChapterTime(seconds float64, includeHours bool) string {
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

// BuildChapterSchedule computes cumulative Track start times and formats YouTube Chapter Timestamps.
func BuildChapterSchedule(titles []string, durations []float64, crossfade float64) ChapterSchedule {
	if len(titles) == 0 || len(durations) == 0 {
		return ChapterSchedule{}
	}
	n := len(titles)
	if len(durations) < n {
		n = len(durations)
	}

	starts := make([]float64, n)
	starts[0] = 0.0
	for i := 1; i < n; i++ {
		nextStart := starts[i-1] + durations[i-1] - crossfade
		if nextStart < 0 {
			nextStart = 0
		}
		starts[i] = nextStart
	}

	totalDuration := starts[n-1] + durations[n-1]
	includeHours := totalDuration >= 3600.0

	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(FormatChapterTime(starts[i], includeHours))
		b.WriteByte(' ')
		b.WriteString(titles[i])
		b.WriteByte('\n')
	}

	return ChapterSchedule{
		Starts:        starts,
		TotalDuration: totalDuration,
		Content:       b.String(),
	}
}

// WriteChapters calculates the ChapterSchedule and writes it to path.
func WriteChapters(path string, titles []string, durations []float64, crossfade float64) (ChapterSchedule, error) {
	sched := BuildChapterSchedule(titles, durations, crossfade)
	if err := os.WriteFile(path, []byte(sched.Content), 0644); err != nil {
		return ChapterSchedule{}, fmt.Errorf("write Chapter Timestamps to %q: %w", path, err)
	}
	return sched, nil
}
