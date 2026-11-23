# Render Duration Estimation & Live ETA Research

**Issue:** [#7](https://github.com/GCherry/ytmix/issues/7)  
**Date:** Nov 22, 2026  
**Baseline Commit:** `c5d6a37` (`main`)

---

## 1. Executive Summary & Current-State Delta

Since [Issue #7](https://github.com/GCherry/ytmix/issues/7) was filed (Nov 16, 2026), three major changes landed on `main`:

1. **Live `ffmpeg -progress pipe:1` streaming is already implemented (#10 / PR #18):**
   - `mixer.ExecStreamRunner` (`internal/mixer/mixer.go`) streams `stdout` lines to `render.ParseProgressLine` (`internal/render/render.go`), emitting `TelemetryEvent` (`EventStep`, `EventProgress`, `EventDone`) with `OutTimeSec`, `TotalDurationSec`, and `Speed`.
   - `internal/ui/jobs.go` + `internal/ui/server.go` push throttled SSE `"progress"` events (`~250ms` or `>= 5%` delta) and `internal/mixer/mixer.go` provides `NewCLITelemetry` for TTY `\r` status updates.
   - **What remains missing:** pre-render duration estimation, Background Visual loop probing, estimate-weighted Step 2–5 progress (currently hardcoded to `5%/10%/15%/20%` + `PathANormWeight = 15/79`), live ETA countdown (`RemainingSec`), CLI `--estimate` dry-run output, and UI breakdown/accuracy widgets.
2. **Path D (`Auto Visualizer`) and resolution scaling landed (#17 / PR #20, #23 / PR #25):**
   - `ytmix` now has **four Rendering Paths** (`Path A`, `Path B`, `Path C`, and `Path D`) plus `1080p`, `1440p`, and `4k` resolutions (`internal/render/visualizer.go`) and `black` (`2 fps` / `10 fps` `-tune stillimage`) vs 13 audio-reactive `30 fps` presets.
3. **`DetectEncoder` / `DetectPathDEncoder` `64x64` NVENC probe bug discovered:**
   - Both `DetectEncoder` (`internal/render/render.go:206`) and `DetectPathDEncoder` (`internal/render/render.go:266`) probe `h264_nvenc` using `color=c=black:s=64x64:d=0.1`.
   - On NVIDIA hardware tested on Windows (`ffmpeg 9.0.2`), `64x64` fails immediately with `InitializeEncoder failed: invalid param (8): Frame Dimension less than the minimum supported value`, causing silent fallback to `libx264` even when NVENC is available. Changing the probe size to `256x256` succeeds (`rc=0`).

---

## 2. Empirical `ffmpeg` / `ffprobe` Benchmarks

**Test Environment:** Windows, 20 logical CPUs, `ffmpeg 9.0.2-full_build-www.gyan.dev`, `ffprobe 9.0.2`, NVIDIA GPU (`h264_nvenc` verified at `256x256`). Synthetic inputs: `3 × 20s` `.mp3` Tracks (`60s` Mix) and `180s` audio, `1920x1080` `.png`, synthetic `.mp4`/`.gif` loops (`1080p30`, `1080p60`, `4K30`, `720p15` GIF), and `.ass` Title Overlays.

### 2.1 Encoder Probe Bug Verification

| Probe Command | Exit Code | Wall Time | Result |
|---|---:|---:|---|
| `h264_nvenc` at `s=64x64` (current `render.go`) | `-22` | `0.178s` | Fails: `Frame Dimension less than the minimum supported value` |
| `h264_nvenc` at `s=256x256` | `0` | `0.187s` | Succeeds |

### 2.2 Step 2 — Audio Processing (`internal/audio/audio.go`)

| Operation (3 Tracks, 60s total) | Wall Time | Throughput | Notes |
|---|---:|---:|---|
| `--normalize` (`loudnorm=I=-14:TP=-1.5:LRA=11` to `.wav` + `ffprobe`) | `0.859s` | `~69.8x` realtime | Per-track `ffmpeg` + `ffprobe` spawn overhead (~`0.28s`/track on short clips) |
| Merge via `concat` demuxer (`320k` AAC `48kHz`) | `1.737s` | `34.5x` realtime | Dominates total wall time on long **Path A** Mixes |
| Merge via `acrossfade` (`3s` tri-curve, `320k` AAC `48kHz`) | `1.578s` | `38.0x` realtime | Scales linearly with Mix duration + filter graph input count |

### 2.3 Step 5 — Video Rendering Across Paths A, B, C, and D (`60s` Mix unless noted)

| Rendering Path & Variant | Encoder | Wall Time | FFmpeg `speed` | Realtime Factor | Primary Scaling Drivers |
|---|---|---:|---:|---:|---|
| **Path A** `norm_loop` (`1080p30`, `5s` `.mp4`) | `libx264 fast` | `0.640s` | `8.88x` | `7.8x` loop | Loop duration × loop FPS × decode res |
| **Path A** `norm_loop` (`1080p60`, `5s` `.mp4`) | `libx264 fast` | `0.706s` | `7.85x` | `7.1x` loop | Source frame count (`2×` frames vs 30fps) |
| **Path A** `norm_loop` (`1080p30`, `15s` `.mp4`) | `libx264 fast` | `1.514s` | `10.4x` | `9.9x` loop | Linear in loop duration (`~3×` `5s` loop) |
| **Path A** `norm_loop` (`4K30`, `5s` `.mp4`) | `libx264 fast` | `0.854s` | `6.51x` | `5.9x` loop | 4K decode + `scale=1920:1080` downscale |
| **Path A** `norm_loop` (`720p15`, `5s` `.gif`) | `libx264 fast` | `0.602s` | `9.41x` | `8.3x` loop | GIF decode + `fps=30` frame duplication |
| **Path A** `mux` (`-c:v copy -c:a copy`, `60s`) | stream copy | `0.094s` | `1440x` | `639.8x` | Container mux / sequential disk I/O |
| **Path A** `mux` (`-c:v copy -c:a copy`, `180s`) | stream copy | `0.147s` | `1720x` | `1225.4x` | Sub-5s even for 60-min Mixes |
| **Path B** (`1080p30` loop + `.ass` burn) | `libx264 veryfast` | `3.558s` | `17.3x` | `16.9x` | Mix duration × source loop FPS |
| **Path B** (`1080p30` loop + `.ass` burn) | `h264_nvenc p4` | `4.762s` | `12.9x` | `12.6x` | PCIe frame upload + CPU `libass` bottleneck |
| **Path B** (`1080p60` loop + `.ass` burn) | `libx264 veryfast` | `6.386s` | `9.52x` | `9.4x` | Almost exact `2×` cost of `1080p30` |
| **Path B** (`1080p60` loop + `.ass` burn) | `h264_nvenc p4` | `8.979s` | `6.76x` | `6.7x` | Scales linearly with source loop FPS |
| **Path C** (`1080p` `.png`, `--overlay-titles=false`, `2 fps`) | `libx264 stillimage` | `0.504s` | `142x` | `119.1x` | Mix duration × `2 fps` |
| **Path C** (`1080p` `.png`, `--overlay-titles=true`, `10 fps`) | `libx264 stillimage` | `1.363s` | `47x` | `44.0x` | Mix duration × `10 fps` + `.ass` burn |
| **Path D** (`black` `1080p`, `2 fps`) | `libx264 stillimage` | `0.318s` | `246x` | `188.6x` | Mix duration × `2 fps` (near zero filter cost) |
| **Path D** (`bars`, `1080p30`) | `libx264` | `3.037s` | `20.2x` | `19.8x` | Filtergraph + `30 fps` encode |
| **Path D** (`bars`, `1080p30`) | `h264_nvenc p4` | `4.431s` | `13.8x` | `13.5x` | Single-threaded filter + GPU upload |
| **Path D** (`ember` `960x540` + `gblur` + `scale`) | `libx264` | `3.758s` | `16.3x` | `16.0x` | `gblur` + upscale cost |
| **Path D** (`cqt` `1080p30`) | `libx264` | `2.348s` | `26.3x` | `25.6x` | `showcqt` filter cost |
| **Path D** (`spectrogram` `1080p30`) | `libx264` | `2.719s` | — | `22.1x` | `showspectrum` slide scroll |
| **Path D** (`3dscope` `1080p30`) | `libx264` | `2.829s` | — | `21.2x` | `a3dscope` |
| **Path D** (`radial` `1080p30`) | `libx264` | `2.945s` | — | `20.4x` | `avectorscope` + `pad` |

### 2.4 Key Empirical Takeaways

1. **Step 2 vs. Step 5 inversion on Path A / Path C / `Path D black`:** On a 60-minute Mix (`3600s`) using Path A (`mux` ~`3s`), Step 2 (`concat`/`acrossfade` at `~35x` = `~103s`, plus `--normalize` at `~60–70x` = `~55s`) accounts for **>95%** of total wall time. Hardcoding Step 2 to `10%` progress causes the progress bar to sit at `10%` for minutes before jumping from `20%` to `100%` in 4 seconds.
2. **Background Visual FPS linearly controls Path B speed:** `1080p60` (`9.4x`) takes almost exactly `2×` the wall time of `1080p30` (`16.9x`) because `BuildPathBArgs` loops at native input FPS without an `fps=30` filter. Probing Background Visual FPS is mandatory for accurate Path B estimates.
3. **Encoder speed is non-monotonic on high-core CPUs:** On a 20-thread CPU, `libx264 -preset veryfast` outperformed `h264_nvenc -preset p4` when paired with CPU-side `libass` / `showfreqs` filtergraphs. Static per-encoder constants alone will mispredict; **recording actual observed `speed` per `(Path, Encoder, Resolution)` in a local calibration cache** solves hardware variance without startup latency.

---

## 3. Background Visual Metadata to Probe (`internal/probe`)

Currently `probe.ProbeFile` (`internal/probe/ffprobe.go:82`) inspects only `format=duration` and `format_tags=title` for `.mp3` Tracks.

Add `BackgroundMeta` and `ProbeBackgroundVisual(ctx, path, runner)` in `internal/probe` using a single `ffprobe` call:

```text
ffprobe -v error -select_streams v:0 \
  -show_entries format=duration:stream=codec_name,width,height,r_frame_rate,avg_frame_rate,nb_frames,pix_fmt \
  -of json <bgPath>
```

| Field | Extraction / Fallback Rule | Used For |
|---|---|---|
| `Width`, `Height` | Integer stream dimensions (`stream.width`, `stream.height`) | 4K/QHD decode penalty in Path A `norm_loop` & Path B |
| `FPS` | Parse `avg_frame_rate` (prefer over `r_frame_rate` for VFR/GIFs if valid `num/den > 0` and `<= 240`), fallback to `r_frame_rate`, fallback `30.0` (video) or `0` (image) | Path B linear scaling (`FPS / 30.0`), Path A `norm_loop` |
| `DurationSec` | Parse `format.duration` when `> 0`; `0` if `N/A` (common for `.gif` / static images) | Path A `norm_loop` target duration & UI loop cycle count (`MixDuration / LoopDuration`) |
| `CodecName`, `PixFmt` | Stream metadata (`gif`, `hevc`, `vp9`, `yuva420p`, etc.) | Decode complexity multiplier & UI metadata badge |
| `NBFrames` | Parse `stream.nb_frames` when present, else `DurationSec * FPS` | Path A `norm_loop` when GIF lacks `format.duration` |

**Invariant:** `internal/probe` remains a leaf package with zero internal imports (`AGENTS.md`).

---

## 4. Pre-Render Estimation Model & Calibration Strategy

### 4.1 Evaluation of Approaches

| Approach | Startup Latency | Accuracy (Run 1) | Accuracy (Run 2+) | Verdict |
|---|---|---|---|---|
| **Static formula only** | `~0ms` (just `ffprobe`) | Medium (`±35%` across CPU/GPU tiers) | Same | Wide bounds forever |
| **Short (~1s) micro-benchmark probe** | `+0.8s–2.0s` per config change | High | High | Too sluggish for live UI slider/toggle updates |
| **Analytical model + local calibration cache (Recommended)** | `~0ms` (cached `ffprobe` metadata) | Good (`±30%` via CPU-core & encoder priors) | High (`±10%–15%` from local EMA speeds) | **Zero latency, self-calibrating** |

### 4.2 Analytical Phase Formulas

Let:
- $N$ = Track count, $T_{\text{raw}} = \sum_{i=1}^N d_i$, $T_{\text{mix}} = T_{\text{raw}} - \max(0, N-1)\cdot \text{Crossfade}$
- $C = \text{clamp}(\text{runtime.NumCPU}(), 1, 32)$, CPU factor $f_{\text{cpu}} = (C / 8)^{0.65}$ (sub-linear scaling for FFmpeg filters/threads)
- Calibrated speed $S_k$ = EMA lookup from `~/.cache/ytmix/calibration.json` (via `os.UserCacheDir()`), or hardware prior when uncalibrated:

1. **Step 1 (Track probing — when running CLI `--audio-dir`):** $t_1 \approx N \cdot 0.04\text{s}$ (`0s` in `ytmix ui` where Tracks are already probed).
2. **Step 2A (`--normalize`):** $t_{\text{norm}} = N \cdot 0.12\text{s} + T_{\text{raw}} / S_{\text{norm}}$ (prior $S_{\text{norm}} \approx 45 \cdot f_{\text{cpu}}$, clamped `[15, 120]`).
3. **Step 2B (Merge to `merged_audio.m4a`):** $t_{\text{merge}} = 0.25\text{s} + T_{\text{mix}} / S_{\text{merge}}$ (prior $S_{\text{merge}} \approx 28 \cdot f_{\text{cpu}}$ for `concat` or `24 \cdot f_{\text{cpu}}` for `acrossfade` with slight penalty for large $N$).
4. **Step 5 (Video Render by Rendering Path):**
   - **Path A:**
     - `norm_loop`: $t_{\text{norm\_loop}} = 0.2\text{s} + \frac{T_{\text{loop}} \cdot (\text{FPS}_{\text{in}}/30)^{0.5} \cdot r_{\text{decode}}}{S_{\text{pathA\_norm}}}$ (prior `~8x` loop realtime; if $T_{\text{loop}} = 0$ e.g. GIF without duration, assume `10s` with wider uncertainty).
     - `mux`: $t_{\text{mux}} = 0.15\text{s} + T_{\text{mix}} / S_{\text{mux}}$ (prior `~500x` realtime).
   - **Path B:**
     - $t_{\text{video}} = 0.3\text{s} + \frac{T_{\text{mix}} \cdot (\text{FPS}_{\text{in}} / 30)}{S_{\text{pathB}}(E)}$ (prior `~12x * f_cpu` for `libx264`, `~14x` for `h264_nvenc` / `h264_videotoolbox`, adjusted by calibrated EMA).
   - **Path C:**
     - $\text{fps} = 10$ if `--overlay-titles` else $2$: $t_{\text{video}} = 0.25\text{s} + \frac{T_{\text{mix}}}{S_{\text{pathC}}(\text{fps})}$ (priors `~100x` at `2 fps`, `~38x` at `10 fps`).
   - **Path D:**
     - `black`: mirrors Path C (`2 fps` / `10 fps`, priors `~160x` / `~50x`, scaled slightly by resolution).
     - 13 audio-reactive presets: $t_{\text{video}} = 0.35\text{s} + \frac{T_{\text{mix}} \cdot m_{\text{res}} \cdot m_{\text{preset}}}{S_{\text{pathD}}(E)}$, where resolution multiplier $m_{\text{res}} \in \{1.0\ (\text{1080p}), 1.65\ (\text{1440p}), 3.4\ (\text{4k})\}$ and preset complexity factor $m_{\text{preset}}$ accounts for `gblur` (`ember`, `night-fog`, `lunar-halo`, or `--visualizer-glow=true`).

### 4.3 Confidence Bounds & Calibration Cache

- **Uncalibrated confidence band:** `LowSec = PointSec * 0.70`, `HighSec = PointSec * 1.45` (minimum `1s` span).
- **Calibrated confidence band (after $\ge 2$ runs on that `(Path, Encoder, Resolution)` key):** tightens to `LowSec = PointSec * 0.85`, `HighSec = PointSec * 1.20`.
- **Cache schema (`os.UserCacheDir()/ytmix/calibration.json`):**
  - Map keyed by `"<Path>|<Encoder>|<ResolutionBucket>"` storing `EMA_Speed float64`, `SampleCount int`, `UpdatedAt time.Time`.
  - Audio stage keys: `"audio|loudnorm"`, `"audio|concat"`, `"audio|acrossfade"`.
  - EMA update ($\alpha = 0.35$) runs only for completed renders with $T_{\text{mix}} \ge 10\text{s}$ and sane measured speed (`0.1 <= speed <= 5000`) to avoid short-test noise skewing long-Mix estimates; atomic write via temp file + `os.Rename`.

---

## 5. Live `ffmpeg -progress` ETA & Weighted Progress Design

Building directly on `#10`'s `StreamRunner` + `ParseProgressLine` (`internal/render/render.go:64`):

1. **Dynamic Step 2–5 Progress Weights:**
   - Replace hardcoded `5% / 10% / 15% / 20%` + `PathANormWeight = 15/79` with weights derived from the pre-render `Estimate`:
     - Step 1: `0% → 4%`
     - Step 2 (`--normalize` + merge): `4% → 4% + W_audio` (e.g. `~88%` of progress bar on a 60-min Path A Mix, `~35%` on a 60-min Path B Mix; during `--normalize`, advance smoothly per completed Track `i/N`!)
     - Steps 3–4 (chapters + `.ass`): `2%`
     - Step 5: remaining `W_video` up to `99%`, split for Path A by `norm_loop / (norm_loop + mux)`.
2. **Smooth ETA Handoff (`RemainingSec`):**
   - During Steps 1–4, `RemainingSec = max(0, Estimate.PointSec - elapsed)` (or stage-remaining sum).
   - Once Step 5 receives `out_time_us` and `speed` from `-progress pipe:1`:
     - Maintain an EMA of live `speed` ($\alpha = 0.25$) in `runFFmpegRender` / `mixer` so transient CPU spikes don't make the ETA jump wildly.
     - Blend weight $w = \min(1.0, \text{outTimeSec} / \max(5.0, 0.05 \cdot \text{targetDuration}))$:
       $$\text{liveRem} = \frac{\text{targetDuration} - \text{outTimeSec}}{\text{emaSpeed}}$$
       $$\text{RemainingSec} = (1 - w)\cdot \text{preRenderStep5Rem} + w \cdot \text{liveRem}$$
   - For Path A `norm_loop`, add `Estimate.MuxSec` to `RemainingSec` until `mux` begins so ETA counts down monotonically toward Mix completion rather than hitting `0s` after loop normalization.

---

## 6. CLI & Editorial Mix Studio (`ytmix ui`) Surface Design

### 6.1 CLI Surface (`cmd/ytmix/main.go`, `internal/mixer/mixer.go`)

- **Pre-render summary** emitted right after Step 1 (`[1/5]`):
  `Estimated render time: ~1m 25s – 2m 05s (Path B · libx264 · Audio ~35s, Video ~50s)`
- **TTY live ETA** (`mixer.NewCLITelemetry`):
  `\r[5/5] Rendering Mix... 42% (2.4x · ~1m 12s left)` (suppressed on non-TTY piped stdout).
- **`--estimate` flag:** Runs `PreflightConfig`, Track discovery/probing, Background Visual probing, encoder detection, and prints the structured breakdown (Tracks, Mix duration, Loop duration/FPS/cycles, Rendering Path, Encoder, Audio/Video estimates, Total range) and exits `0` without touching output targets or invoking FFmpeg render.

### 6.2 Editorial Mix Studio (`ytmix ui`) Wireframe & API

1. **API Changes (`internal/ui/server.go`):**
   - **`POST /api/estimate`:** Enforces `requireSameOrigin`, `localPathsAllowed`, and `resolveAllowedPath` on `bgPath`. Accepts uploaded BG probe token or local `bgPath` (or caches probed BG metadata on `/api/probe-bg` / multipart upload if needed so repeated slider tweaks don't re-upload a 100MB BG video!).
     - *Important optimization:* When a user uploads a Background Visual via browser `<input type="file">`, read basic video/image metadata (`HTMLVideoElement.videoWidth/videoHeight/duration` or `Image.naturalWidth/naturalHeight`) client-side for instant zero-network updates, and/or upload once to a session BG probe endpoint (`POST /api/sessions/{id}/bg`) so `POST /api/estimate` stays `<5ms` on every slider drag!
   - **SSE `"progress"` payload:** Add `remainingSec` (rounded integer seconds, `-1` if unavailable) and `estimatedTotalSec`.
   - **SSE `"complete"` payload:** Add `estimatedSec` and `actualSec` for post-render comparison.

2. **UI Layout (`Anodized Obsidian` / `Studio Paper`, `1px` hairlines, `IBM Plex Mono` `tabular-nums`):**

```text
┌─ Stage Meta Bar (.stage-meta-bar) ───────────────────────────────────────────┐
│ H.264 + AAC 320k · 48 kHz          12.0s loop · 30 fps · ×300 cycles (Path B)│
└──────────────────────────────────────────────────────────────────────────────┘

┌─ Pinned Render Dock (.render-dock) ──────────────────────────────────────────┐
│ Est. ~1m 25s – 2m 05s · ~14.2x rt      [████ Audio 38% ████|██████ Video 62%]│
│ [               Render Mix → output.mp4 (Est. ~1m 45s)                     ] │
└──────────────────────────────────────────────────────────────────────────────┘

┌─ 04 Pipeline Telemetry ──────────────────────────────────────────────────────┐
│ [04] Pipeline Telemetry (5 Steps)        [Running · 42% · 2.4x · ~1m 12s left]│
│ █████████████░░░░░░░░░░░░░░░░░░░░░░░░░                                       │
│ ...                                                                          │
│ On complete: Estimated ~1m 45s · Actual 1m 38s (-7%)                         │
└──────────────────────────────────────────────────────────────────────────────┘
```
