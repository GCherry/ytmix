package mixer

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/GCherry/ytmix/internal/render"
)

func TestCalibrationPersistenceAndEMA(t *testing.T) {
	t.Run("LoadCalibration returns empty map when file is missing or corrupt", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "does-not-exist.json")
		if got := LoadCalibration(missing); len(got) != 0 {
			t.Fatalf("expected empty map for missing file, got %+v", got)
		}
	})

	t.Run("RecordObservedSpeed rejects short mixes and out-of-range speeds", func(t *testing.T) {
		cal := make(map[string]render.CalibrationEntry)
		now := time.Now().UTC()
		if RecordObservedSpeed(cal, "Path B|libx264|1080p", 12.0, 5.0, now) {
			t.Fatal("expected short mix (<10s) to be ignored")
		}
		if RecordObservedSpeed(cal, "Path B|libx264|1080p", 0.01, 120.0, now) {
			t.Fatal("expected tiny speed (<0.1) to be rejected")
		}
		if RecordObservedSpeed(cal, "Path B|libx264|1080p", 9000.0, 120.0, now) {
			t.Fatal("expected extreme speed (>5000) to be rejected")
		}
		if len(cal) != 0 {
			t.Fatalf("expected empty map after rejections, got %+v", cal)
		}
	})

	t.Run("EMA updates and atomic SaveCalibration round-trips cleanly", func(t *testing.T) {
		cal := make(map[string]render.CalibrationEntry)
		now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
		key := render.CalibrationKey(render.PathB, "libx264", render.Resolution1080p)

		if !RecordObservedSpeed(cal, key, 10.0, 60.0, now) {
			t.Fatal("first RecordObservedSpeed failed")
		}
		if !RecordObservedSpeed(cal, key, 20.0, 60.0, now.Add(time.Minute)) {
			t.Fatal("second RecordObservedSpeed failed")
		}
		wantEMA := 0.35*20.0 + 0.65*10.0
		if math.Abs(cal[key].Speed-wantEMA) > 1e-6 || cal[key].Samples != 2 {
			t.Fatalf("cal[%q] = %+v, want Speed=%v Samples=2", key, cal[key], wantEMA)
		}

		path := filepath.Join(t.TempDir(), "ytmix", "calibration.json")
		if err := SaveCalibration(path, cal); err != nil {
			t.Fatalf("SaveCalibration error: %v", err)
		}
		loaded := LoadCalibration(path)
		if loaded[key].Samples != 2 || math.Abs(loaded[key].Speed-wantEMA) > 1e-6 {
			t.Fatalf("reloaded cal = %+v, want Speed=%v", loaded[key], wantEMA)
		}
	})
}
