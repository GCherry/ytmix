package render

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// CalibrationEntry stores an exponentially smoothed observed FFmpeg speed multiplier
// for a single pipeline stage or Rendering Path bucket.
type CalibrationEntry struct {
	Speed     float64   `json:"speed"`
	Samples   int       `json:"samples"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func normalizeResolution(res string) string {
	if spec, ok := LookupResolution(res); ok {
		return spec.ID
	}
	return Resolution1080p
}

// CalibrationKey builds a canonical lookup key for Step 5 video render calibration.
func CalibrationKey(pathKind Path, encoderName, resolution string) string {
	enc := strings.TrimSpace(encoderName)
	if enc == "" {
		enc = "libx264"
	}
	res := normalizeResolution(resolution)
	if pathKind != PathD {
		res = Resolution1080p
	}
	return fmt.Sprintf("%s|%s|%s", pathKind, enc, res)
}

// AudioCalibrationKey returns the canonical lookup key for Step 2 audio substages.
func AudioCalibrationKey(substage string) string {
	return "audio|" + strings.TrimSpace(substage)
}

// BackgroundEstimateInput carries probed Background Visual metrics needed for estimation.
type BackgroundEstimateInput struct {
	DurationSec float64
	FPS         float64
	Width       int
	Height      int
	CodecName   string
	PixFmt      string
}

// EstimateInput describes the full Mix configuration for pre-render duration estimation.
type EstimateInput struct {
	Path           Path
	EncoderName    string
	MixDurationSec float64
	TotalAudioSec  float64
	TrackCount     int
	Normalize      bool
	Crossfade      float64
	OverlayTitles  bool
	Visualizer     VisualizerConfig
	CPUCores       int
	BG             BackgroundEstimateInput
}

// StageWeights captures the normalized share of overall pipeline wall time across Steps 2 and 5.
type StageWeights struct {
	Audio        float64 `json:"audio"`
	Video        float64 `json:"video"`
	PathANormSub float64 `json:"pathANormSub,omitempty"`
}

// MixEstimate summarizes predicted wall-clock seconds, confidence bounds, stage weights, and loop context.
type MixEstimate struct {
	Path            Path         `json:"renderingPath"`
	EncoderName     string       `json:"encoder"`
	MixDurationSec  float64      `json:"mixDurationSec"`
	AudioSec        float64      `json:"audioSec"`
	NormalizeSec    float64      `json:"normalizeSec"`
	MergeSec        float64      `json:"mergeSec"`
	VideoSec        float64      `json:"videoSec"`
	PathANormSec    float64      `json:"pathANormSec,omitempty"`
	PathAMuxSec     float64      `json:"pathAMuxSec,omitempty"`
	PointSec        float64      `json:"pointSec"`
	LowSec          float64      `json:"lowSec"`
	HighSec         float64      `json:"highSec"`
	LoopDurationSec float64      `json:"loopDurationSec,omitempty"`
	LoopFPS         float64      `json:"loopFPS,omitempty"`
	LoopCycles      int          `json:"loopCycles,omitempty"`
	Calibrated      bool         `json:"calibrated"`
	Weights         StageWeights `json:"weights"`
}

func cpuScaleFactor(cores int) float64 {
	if cores <= 0 {
		cores = 4
	}
	if cores > 32 {
		cores = 32
	}
	return math.Pow(float64(cores)/8.0, 0.65)
}

func lookupCalibratedSpeed(cal map[string]CalibrationEntry, key string, fallback float64) (speed float64, calibrated bool) {
	if cal != nil {
		if entry, ok := cal[key]; ok && entry.Speed >= 0.1 && entry.Speed <= 5000 && entry.Samples > 0 {
			if entry.Samples >= 2 {
				return entry.Speed, true
			}
			// Blend single observation 50/50 with hardware prior to avoid noisy first-run swings.
			return 0.5*entry.Speed + 0.5*fallback, false
		}
	}
	return fallback, false
}

func clampSpeed(v, minV, maxV float64) float64 {
	if v < minV {
		return minV
	}
	if v > maxV {
		return maxV
	}
	return v
}

func resolutionMultiplier(res string) float64 {
	switch normalizeResolution(res) {
	case Resolution4K:
		return 3.4
	case Resolution1440p:
		return 1.65
	default:
		return 1.0
	}
}

func visualizerComplexity(cfg VisualizerConfig) float64 {
	preset := strings.ToLower(strings.TrimSpace(cfg.Preset))
	if preset == "" {
		preset = DefaultVisualizerPreset
	}
	mult := 1.0
	switch preset {
	case "ember", "night-fog", "lunar-halo":
		mult = 1.25
	case "cqt":
		mult = 0.85
	case "spectrogram", "3dscope", "radial":
		mult = 0.95
	}
	if cfg.Glow && preset != "black" && preset != "ember" && preset != "night-fog" && preset != "lunar-halo" && preset != "night-line" {
		mult *= 1.15
	}
	return mult
}

// EstimateMix computes a pre-render wall-time breakdown and confidence range across Step 2 and Step 5.
func EstimateMix(in EstimateInput, cal map[string]CalibrationEntry) MixEstimate {
	mixDur := math.Max(0, in.MixDurationSec)
	rawAudio := math.Max(mixDur, in.TotalAudioSec)
	trackCount := max(1, in.TrackCount)
	fCPU := cpuScaleFactor(in.CPUCores)

	enc := strings.TrimSpace(in.EncoderName)
	if enc == "" {
		enc = "libx264"
	}

	var normSec float64
	if in.Normalize {
		priorNorm := clampSpeed(45.0*fCPU, 15.0, 120.0)
		normSpeed, _ := lookupCalibratedSpeed(cal, AudioCalibrationKey("loudnorm"), priorNorm)
		normSec = float64(trackCount)*0.12 + rawAudio/normSpeed
	}

	mergeKey := "concat"
	priorMerge := clampSpeed(28.0*fCPU, 10.0, 80.0)
	if in.Crossfade > 0 && trackCount > 1 {
		mergeKey = "acrossfade"
		priorMerge = clampSpeed(24.0*fCPU, 8.0, 75.0)
		if trackCount > 12 {
			priorMerge *= math.Max(0.65, 1.0-float64(trackCount-12)*0.01)
		}
	}
	mergeSpeed, _ := lookupCalibratedSpeed(cal, AudioCalibrationKey(mergeKey), priorMerge)
	mergeSec := 0.25 + mixDur/mergeSpeed
	audioSec := normSec + mergeSec

	loopFPS := in.BG.FPS
	if (in.Path == PathA || in.Path == PathB) && loopFPS <= 0 {
		loopFPS = 30.0
	}
	loopDur := in.BG.DurationSec
	var loopCycles int
	if (in.Path == PathA || in.Path == PathB) && loopDur > 0 && mixDur > 0 {
		loopCycles = int(math.Ceil(mixDur / loopDur))
	}

	var videoSec, pathANormSec, pathAMuxSec float64
	var videoCalibrated bool

	switch in.Path {
	case PathA:
		effectiveLoop := loopDur
		unknownLoopDur := effectiveLoop <= 0
		if unknownLoopDur {
			effectiveLoop = 10.0
		}
		decodePenalty := 1.0
		if in.BG.Width*in.BG.Height >= 3840*2160 {
			decodePenalty = 1.45
		}
		priorNormLoop := clampSpeed(7.5*fCPU, 2.5, 20.0)
		normLoopSpeed, calNorm := lookupCalibratedSpeed(cal, CalibrationKey(PathA, "norm_loop", Resolution1080p), priorNormLoop)
		pathANormSec = 0.20 + (effectiveLoop*math.Pow(loopFPS/30.0, 0.5)*decodePenalty)/normLoopSpeed

		priorMux := 500.0
		muxSpeed, calMux := lookupCalibratedSpeed(cal, CalibrationKey(PathA, "mux", Resolution1080p), priorMux)
		pathAMuxSec = 0.15 + mixDur/muxSpeed
		videoSec = pathANormSec + pathAMuxSec
		videoCalibrated = calNorm && calMux && !unknownLoopDur
	case PathB:
		priorB := clampSpeed(12.0*fCPU, 3.0, 32.0)
		if enc == "h264_nvenc" || enc == "h264_videotoolbox" {
			priorB = 14.0
		}
		speedB, calB := lookupCalibratedSpeed(cal, CalibrationKey(PathB, enc, Resolution1080p), priorB)
		videoSec = 0.30 + (mixDur*(loopFPS/30.0))/speedB
		videoCalibrated = calB
	case PathC:
		priorC := clampSpeed(95.0*fCPU, 25.0, 220.0)
		resTag := "2fps"
		if in.OverlayTitles {
			priorC = clampSpeed(36.0*fCPU, 10.0, 90.0)
			resTag = "10fps"
		}
		speedC, calC := lookupCalibratedSpeed(cal, CalibrationKey(PathC, "libx264", resTag), priorC)
		videoSec = 0.25 + mixDur/speedC
		videoCalibrated = calC
	case PathD:
		preset := strings.ToLower(strings.TrimSpace(in.Visualizer.Preset))
		if preset == "" {
			preset = DefaultVisualizerPreset
		}
		res := normalizeResolution(in.Visualizer.Resolution)
		if preset == "black" {
			priorBlack := clampSpeed(140.0*fCPU, 40.0, 300.0)
			if in.OverlayTitles {
				priorBlack = clampSpeed(45.0*fCPU, 15.0, 120.0)
			}
			speedBlack, calBlack := lookupCalibratedSpeed(cal, CalibrationKey(PathD, "black", res), priorBlack)
			videoSec = 0.25 + (mixDur*math.Sqrt(resolutionMultiplier(res)))/speedBlack
			videoCalibrated = calBlack
		} else {
			priorD := clampSpeed(13.0*fCPU, 3.0, 35.0)
			if enc == "h264_nvenc" || enc == "h264_videotoolbox" {
				priorD = 15.0
			}
			speedD, calD := lookupCalibratedSpeed(cal, CalibrationKey(PathD, enc, res), priorD)
			videoSec = 0.35 + (mixDur*resolutionMultiplier(res)*visualizerComplexity(in.Visualizer))/speedD
			videoCalibrated = calD
		}
	}

	overheadSec := 0.20 + float64(trackCount)*0.02
	pointSec := audioSec + videoSec + overheadSec

	lowMult, highMult := 0.70, 1.45
	if videoCalibrated {
		lowMult, highMult = 0.85, 1.20
	}
	lowSec := math.Max(0.5, pointSec*lowMult)
	highSec := math.Max(lowSec+1.0, pointSec*highMult)

	denom := audioSec + videoSec
	weights := StageWeights{Audio: 0.20, Video: 0.80}
	if denom > 0 {
		weights.Audio = audioSec / denom
		weights.Video = videoSec / denom
	}
	if in.Path == PathA && videoSec > 0 {
		weights.PathANormSub = clampSpeed(pathANormSec/videoSec, 0.06, 0.94)
	}

	return MixEstimate{
		Path:            in.Path,
		EncoderName:     enc,
		MixDurationSec:  mixDur,
		AudioSec:        audioSec,
		NormalizeSec:    normSec,
		MergeSec:        mergeSec,
		VideoSec:        videoSec,
		PathANormSec:    pathANormSec,
		PathAMuxSec:     pathAMuxSec,
		PointSec:        pointSec,
		LowSec:          lowSec,
		HighSec:         highSec,
		LoopDurationSec: loopDur,
		LoopFPS:         loopFPS,
		LoopCycles:      loopCycles,
		Calibrated:      videoCalibrated,
		Weights:         weights,
	}
}
