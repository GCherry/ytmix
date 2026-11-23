package mixer

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GCherry/ytmix/internal/render"
)

const (
	calibrationEMAAlpha   = 0.35
	minCalibratedMixSec   = 10.0
	minObservedSpeed      = 0.1
	maxObservedSpeed      = 5000.0
	calibrationFileSchema = 1
)

type calibrationDisk struct {
	Version int                                `json:"version"`
	Entries map[string]render.CalibrationEntry `json:"entries"`
}

// DefaultCalibrationPath returns the OS-standard cache path for ytmix throughput calibration.
func DefaultCalibrationPath() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(base) == "" {
		return "", err
	}
	return filepath.Join(base, "ytmix", "calibration.json"), nil
}

// LoadCalibration reads cached calibration entries from path, returning an empty map if missing or invalid.
func LoadCalibration(path string) map[string]render.CalibrationEntry {
	entries := make(map[string]render.CalibrationEntry)
	if strings.TrimSpace(path) == "" {
		return entries
	}
	// #nosec G304 -- path is the fixed OS user cache file from DefaultCalibrationPath or explicit test path.
	raw, err := os.ReadFile(path)
	if err != nil {
		return entries
	}
	var disk calibrationDisk
	if err := json.Unmarshal(raw, &disk); err != nil {
		return entries
	}
	for k, v := range disk.Entries {
		if v.Speed >= minObservedSpeed && v.Speed <= maxObservedSpeed && !math.IsNaN(v.Speed) && !math.IsInf(v.Speed, 0) && v.Samples > 0 {
			entries[k] = v
		}
	}
	return entries
}

// SaveCalibration atomically writes calibration entries to path via temp file and os.Rename.
func SaveCalibration(path string, entries map[string]render.CalibrationEntry) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	dir := filepath.Dir(path)
	// #nosec G301 -- local user cache directory uses standard 0755 permissions.
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(calibrationDisk{
		Version: calibrationFileSchema,
		Entries: entries,
	}, "", "  ")
	if err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(dir, ".calibration-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmpFile.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmpFile.Write(payload); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanupTmp = false
	return nil
}

// RecordObservedSpeed updates cal[key] via EMA when observedSpeed and mixDurationSec pass sanity bounds.
func RecordObservedSpeed(cal map[string]render.CalibrationEntry, key string, observedSpeed, mixDurationSec float64, now time.Time) bool {
	if cal == nil || strings.TrimSpace(key) == "" {
		return false
	}
	if mixDurationSec < minCalibratedMixSec {
		return false
	}
	if math.IsNaN(observedSpeed) || math.IsInf(observedSpeed, 0) || observedSpeed < minObservedSpeed || observedSpeed > maxObservedSpeed {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	prev, ok := cal[key]
	if !ok || prev.Samples <= 0 || prev.Speed < minObservedSpeed || prev.Speed > maxObservedSpeed {
		cal[key] = render.CalibrationEntry{
			Speed:     observedSpeed,
			Samples:   1,
			UpdatedAt: now,
		}
		return true
	}

	cal[key] = render.CalibrationEntry{
		Speed:     calibrationEMAAlpha*observedSpeed + (1.0-calibrationEMAAlpha)*prev.Speed,
		Samples:   prev.Samples + 1,
		UpdatedAt: now,
	}
	return true
}
