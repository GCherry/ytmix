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
// For PathD, bucket is normalized as a resolution preset (1080p/1440p/4k);
// for PathC, bucket preserves distinct fps tiers ("2fps" / "10fps").
func CalibrationKey(pathKind Path, encoderName, bucket string) string {
	enc := strings.TrimSpace(encoderName)
	if enc == "" {
		enc = "libx264"
	}
	b := strings.TrimSpace(bucket)
	switch pathKind {
	case PathD:
		b = normalizeResolution(b)
	case PathC:
		if b == "" {
			b = "2fps"
		}
	default:
		b = Resolution1080p
	}
	return fmt.Sprintf("%s|%s|%s", pathKind, enc, b)
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

// SegmentEstimateInput describes one Track segment's visual mode and duration for segmented Mix estimation.
type SegmentEstimateInput struct {
	SlotDurationSec float64
	LayerMode       TrackLayerMode
	MediaKind       BackgroundKind
	OverlayTitles   bool
	MediaFPS        float64
	MediaWidth      int
	MediaHeight     int
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
	Segmented      bool
	Layer0         Layer0Spec
	Segments       []SegmentEstimateInput
}

// StageWeights captures the normalized share of overall pipeline wall time across Steps 2 and 5.
type StageWeights struct {
	Audio        float64 `json:"audio"`
	Video        float64 `json:"video"`
	PathANormSub float64 `json:"pathANormSub,omitempty"`
}

// MixEstimate summarizes predicted wall-clock seconds, confidence bounds, stage weights, and loop context.
type MixEstimate struct {
	Path             Path         `json:"renderingPath"`
	EncoderName      string       `json:"encoder"`
	MixDurationSec   float64      `json:"mixDurationSec"`
	TotalAudioSec    float64      `json:"-"`
	AudioSec         float64      `json:"audioSec"`
	NormalizeSec     float64      `json:"normalizeSec"`
	MergeSec         float64      `json:"mergeSec"`
	VideoSec         float64      `json:"videoSec"`
	VideoWorkSec     float64      `json:"-"`
	PathANormSec     float64      `json:"pathANormSec,omitempty"`
	PathANormWorkSec float64      `json:"-"`
	PathAMuxSec      float64      `json:"pathAMuxSec,omitempty"`
	PointSec         float64      `json:"pointSec"`
	LowSec           float64      `json:"lowSec"`
	HighSec          float64      `json:"highSec"`
	LoopDurationSec  float64      `json:"loopDurationSec,omitempty"`
	LoopFPS          float64      `json:"loopFPS,omitempty"`
	LoopCycles       int          `json:"loopCycles,omitempty"`
	Calibrated       bool         `json:"calibrated"`
	Weights          StageWeights `json:"weights"`
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

func resolveSegmentedEstimateFPS(in EstimateInput) int {
	items := make([]segmentedFPSItem, len(in.Segments))
	for i, seg := range in.Segments {
		items[i] = segmentedFPSItem{
			OverlayTitles: seg.OverlayTitles,
			LayerMode:     seg.LayerMode,
			MediaKind:     seg.MediaKind,
		}
	}
	return resolveUniformSegmentedFPS(in.Layer0, in.Layer0.BGKind, in.OverlayTitles, items)
}

func estimateSegmentedVideo(in EstimateInput, enc string, fCPU, mixDur float64, cal map[string]CalibrationEntry) (videoSec, videoWorkSec float64, calibrated bool) {
	segments := in.Segments
	if len(segments) == 0 {
		segments = []SegmentEstimateInput{{
			SlotDurationSec: mixDur,
			LayerMode:       LayerBackdrop,
			OverlayTitles:   in.OverlayTitles,
		}}
	}

	uniformFPS := resolveSegmentedEstimateFPS(in)
	l0Mode := normalizeLayer0Mode(in.Layer0.Mode)
	res := Resolution1080p
	if l0Mode == Layer0Visualizer || l0Mode == Layer0Black {
		res = normalizeResolution(in.Layer0.Visualizer.Resolution)
	}
	vizCfg := in.Layer0.Visualizer
	if strings.TrimSpace(vizCfg.Preset) == "" {
		vizCfg.Preset = DefaultVisualizerPreset
	}
	l0VizPreset := strings.ToLower(strings.TrimSpace(vizCfg.Preset))
	l0IsVideo := l0Mode == Layer0Media && in.Layer0.BGKind == BackgroundKindVideo
	l0IsReactiveViz := l0Mode == Layer0Visualizer && l0VizPreset != "black"

	var totalSegDur float64
	allCalibrated := true
	for _, seg := range segments {
		dur := math.Max(0, seg.SlotDurationSec)
		if dur == 0 {
			continue
		}
		totalSegDur += dur

		mode, err := ValidateTrackLayerMode(seg.LayerMode)
		if err != nil {
			mode = LayerBackdrop
		}
		usesLayer0 := mode == LayerArtCard || mode == LayerBackdrop
		segUsesReactiveViz := usesLayer0 && l0IsReactiveViz
		segUsesVideo := ((mode == LayerFullscreen || mode == LayerArtCard) && seg.MediaKind == BackgroundKindVideo) || (usesLayer0 && l0IsVideo)
		cardCompositeMult := 1.0
		if mode == LayerArtCard {
			cardCompositeMult = 1.15
		}

		var segWork, segSpeed float64
		var segCal bool

		switch {
		case segUsesReactiveViz:
			priorD := clampSpeed(13.0*fCPU, 3.0, 35.0)
			if enc == "h264_nvenc" || enc == "h264_videotoolbox" {
				priorD = 15.0
			}
			segWork = dur * resolutionMultiplier(res) * visualizerComplexity(vizCfg) * cardCompositeMult
			segSpeed, segCal = lookupCalibratedSpeed(cal, CalibrationKey(PathD, enc, res), priorD)

		case segUsesVideo:
			priorB := clampSpeed(12.0*fCPU, 3.0, 32.0)
			if enc == "h264_nvenc" || enc == "h264_videotoolbox" {
				priorB = 14.0
			}
			fpsScale := float64(uniformFPS) / 30.0
			if seg.MediaFPS > 30.0 {
				fpsScale = seg.MediaFPS / 30.0
			}
			segWork = dur * fpsScale * cardCompositeMult
			segSpeed, segCal = lookupCalibratedSpeed(cal, CalibrationKey(PathB, enc, res), priorB)

		case uniformFPS == 30:
			// Static image or black encoded at uniform 30fps because another segment uses video/visualizer.
			prior30 := clampSpeed(30.0*fCPU, 8.0, 80.0)
			if enc == "h264_nvenc" || enc == "h264_videotoolbox" {
				prior30 = 32.0
			}
			segWork = dur * resolutionMultiplier(res) * cardCompositeMult
			segSpeed, segCal = lookupCalibratedSpeed(cal, CalibrationKey(PathB, enc, res), prior30)

		case uniformFPS == 10:
			priorC := clampSpeed(36.0*fCPU, 10.0, 90.0)
			segWork = dur * math.Sqrt(resolutionMultiplier(res)) * cardCompositeMult
			segSpeed, segCal = lookupCalibratedSpeed(cal, CalibrationKey(PathC, "libx264", "10fps"), priorC)

		default:
			priorC := clampSpeed(95.0*fCPU, 25.0, 220.0)
			segWork = dur * math.Sqrt(resolutionMultiplier(res)) * cardCompositeMult
			segSpeed, segCal = lookupCalibratedSpeed(cal, CalibrationKey(PathC, "libx264", "2fps"), priorC)
		}

		videoWorkSec += segWork
		videoSec += 0.08 + segWork/segSpeed
		if !segCal {
			allCalibrated = false
		}
	}

	if totalSegDur <= 0 {
		allCalibrated = false
	}
	// Stream-copy concat mux with merged_audio.m4a.
	concatSpeed, _ := lookupCalibratedSpeed(cal, CalibrationKey(PathA, "mux", Resolution1080p), 500.0)
	videoSec += 0.15 + max(mixDur, totalSegDur)/concatSpeed
	return videoSec, videoWorkSec, allCalibrated
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

	var loopFPS, loopDur float64
	var loopCycles int
	if !in.Segmented {
		loopFPS = in.BG.FPS
		if (in.Path == PathA || in.Path == PathB) && loopFPS <= 0 {
			loopFPS = 30.0
		}
		loopDur = in.BG.DurationSec
		if (in.Path == PathA || in.Path == PathB) && loopDur > 0 && mixDur > 0 {
			loopCycles = int(math.Ceil(mixDur / loopDur))
		}
	}

	var videoSec, videoWorkSec, pathANormSec, pathANormWorkSec, pathAMuxSec float64
	var videoCalibrated bool

	if in.Segmented {
		videoSec, videoWorkSec, videoCalibrated = estimateSegmentedVideo(in, enc, fCPU, mixDur, cal)
	} else {
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
			normWork := effectiveLoop * math.Pow(loopFPS/30.0, 0.5) * decodePenalty
			if !unknownLoopDur {
				pathANormWorkSec = normWork
			}
			priorNormLoop := clampSpeed(7.5*fCPU, 2.5, 20.0)
			normLoopSpeed, calNorm := lookupCalibratedSpeed(cal, CalibrationKey(PathA, "norm_loop", Resolution1080p), priorNormLoop)
			pathANormSec = 0.20 + normWork/normLoopSpeed

			priorMux := 500.0
			muxSpeed, calMux := lookupCalibratedSpeed(cal, CalibrationKey(PathA, "mux", Resolution1080p), priorMux)
			pathAMuxSec = 0.15 + mixDur/muxSpeed
			videoSec = pathANormSec + pathAMuxSec
			videoWorkSec = mixDur
			videoCalibrated = calNorm && calMux && !unknownLoopDur
		case PathB:
			priorB := clampSpeed(12.0*fCPU, 3.0, 32.0)
			if enc == "h264_nvenc" || enc == "h264_videotoolbox" {
				priorB = 14.0
			}
			videoWorkSec = mixDur * (loopFPS / 30.0)
			speedB, calB := lookupCalibratedSpeed(cal, CalibrationKey(PathB, enc, Resolution1080p), priorB)
			videoSec = 0.30 + videoWorkSec/speedB
			videoCalibrated = calB
		case PathC:
			priorC := clampSpeed(95.0*fCPU, 25.0, 220.0)
			resTag := "2fps"
			if in.OverlayTitles {
				priorC = clampSpeed(36.0*fCPU, 10.0, 90.0)
				resTag = "10fps"
			}
			videoWorkSec = mixDur
			speedC, calC := lookupCalibratedSpeed(cal, CalibrationKey(PathC, "libx264", resTag), priorC)
			videoSec = 0.25 + videoWorkSec/speedC
			videoCalibrated = calC
		case PathD:
			preset := strings.ToLower(strings.TrimSpace(in.Visualizer.Preset))
			if preset == "" {
				preset = DefaultVisualizerPreset
			}
			res := normalizeResolution(in.Visualizer.Resolution)
			if preset == "black" {
				priorBlack := clampSpeed(140.0*fCPU, 40.0, 300.0)
				blackEncKey := "black-2fps"
				if in.OverlayTitles {
					priorBlack = clampSpeed(45.0*fCPU, 15.0, 120.0)
					blackEncKey = "black-10fps"
				}
				videoWorkSec = mixDur * math.Sqrt(resolutionMultiplier(res))
				speedBlack, calBlack := lookupCalibratedSpeed(cal, CalibrationKey(PathD, blackEncKey, res), priorBlack)
				videoSec = 0.25 + videoWorkSec/speedBlack
				videoCalibrated = calBlack
			} else {
				priorD := clampSpeed(13.0*fCPU, 3.0, 35.0)
				if enc == "h264_nvenc" || enc == "h264_videotoolbox" {
					priorD = 15.0
				}
				videoWorkSec = mixDur * resolutionMultiplier(res) * visualizerComplexity(in.Visualizer)
				speedD, calD := lookupCalibratedSpeed(cal, CalibrationKey(PathD, enc, res), priorD)
				videoSec = 0.35 + videoWorkSec/speedD
				videoCalibrated = calD
			}
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
	if !in.Segmented && in.Path == PathA && videoSec > 0 {
		weights.PathANormSub = clampSpeed(pathANormSec/videoSec, 0.06, 0.94)
	}

	return MixEstimate{
		Path:             in.Path,
		EncoderName:      enc,
		MixDurationSec:   mixDur,
		TotalAudioSec:    rawAudio,
		AudioSec:         audioSec,
		NormalizeSec:     normSec,
		MergeSec:         mergeSec,
		VideoSec:         videoSec,
		VideoWorkSec:     videoWorkSec,
		PathANormSec:     pathANormSec,
		PathANormWorkSec: pathANormWorkSec,
		PathAMuxSec:      pathAMuxSec,
		PointSec:         pointSec,
		LowSec:           lowSec,
		HighSec:          highSec,
		LoopDurationSec:  loopDur,
		LoopFPS:          loopFPS,
		LoopCycles:       loopCycles,
		Calibrated:       videoCalibrated,
		Weights:          weights,
	}
}
