# Technical Specification: `ytmix` — Automated YouTube Mix & Compilation Builder CLI

## 1. Overview & Objective
`ytmix` is a high-performance, minimal-dependency Go CLI tool (with an embedded stdlib web studio `ytmix ui`) that wraps `ffmpeg` and `ffprobe` to compile a directory of `.mp3` audio files and either a Background Visual (static image or looping video/GIF) or an Auto Visualizer (`Path D`) into a 1080p `.mp4` video ready for YouTube upload. Alongside the video, it calculates exact Track start times and outputs a YouTube-formatted chapter file (`timestamps.txt`).

---

## 2. System & Architectural Constraints
- **Language:** Go (`1.27+`), utilizing the Go standard library plus minimal, vetted pure-Go dependencies (`golang.org/x/sync/errgroup`, explicitly allowlisted in `.golangci.yml` `depguard`, zero CGo).
- **External Dependencies:** `ffmpeg` and `ffprobe` must be available on the system `PATH`. Do not use heavy CGo bindings or in-memory audio/video processing libraries; shell out directly via `os/exec`.
- **Hardware Acceleration Detection (`render.DetectEncoder` / `render.DetectPathDEncoder`):** Inspect `runtime.GOOS` and probe via a 1-frame null encode to select the optimal H.264 encoder when video re-encoding is required:
  - `darwin`: `h264_videotoolbox` (`-b:v 6000k` for Path B; Path D probes `-q:v 65 -profile:v high` for Apple Silicon and falls back to resolution-scaled `-b:v` `12000k`/`18000k`/`28000k` `-profile:v high` for Intel Macs)
  - `windows` / `linux`: Probe `h264_nvenc` (`-preset p4 -cq 20` / Path D `-cq 18`); fall back cleanly to `libx264 -preset veryfast -crf 20` (Path D `-preset fast -crf 18 -profile:v high`).
- **Filter Availability Detection (`render.DetectFilters`):** Execute `ffmpeg -hide_banner -filters` once during Path D preflight (and cache per `ui.Server` via `sync.Once`) so unsupported filters fail fast before audio processing begins.
- **Temporary Workspace:** Create a deterministic temporary working directory (`os.MkdirTemp("", "ytmix-*")`) for intermediate normalized audio Tracks, concat lists, pre-normalized video loops, and `.ass` subtitle scripts. Clean up this directory via `defer os.RemoveAll(tempDir)` on exit.

---

## 3. CLI Flags & Interface

| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--audio-dir` | `string` | `./tracks` | Directory containing input `.mp3` files. |
| `--bg` | `string` | `""` *(Optional)* | Path to Background Visual (`.jpg`, `.jpeg`, `.png`, `.webp`, `.mp4`, `.mov`, `.gif`). Omit to enter **Path D (Auto Visualizer)**; mutually exclusive with `--visualizer`. |
| `--out-dir` | `string` | `""` | Directory where relative `--out` and `--timestamps` paths resolve (missing folders created automatically). |
| `--out` | `string` | `output.mp4` | Output `.mp4` video path. |
| `--timestamps` | `string` | `timestamps.txt` | Output file path for YouTube Chapter Timestamps. |
| `--crossfade` | `float64` | `0` | Crossfade duration in seconds between Tracks (`0` uses fast concat demuxer; `>0` uses `acrossfade`). |
| `--normalize` | `bool` | `false` | Normalize each Track to YouTube's `-14 LUFS` target (`loudnorm=I=-14:TP=-1.5:LRA=11`). |
| `--overlay-titles` | `bool` | `false` | Burn each Track Display Title into the bottom-left corner via `.ass` subtitles. |
| `--title-duration` | `int` | `0` | Seconds to display each Display Title (`0` = full Track; `>0` = fades in/out for first $N$ seconds). |
| `--font-name` | `string` | `Arial` | Font family name used inside the generated `.ass` subtitle script. |
| `--shuffle` | `bool` | `false` | Randomize Track order instead of sorting alphabetically. |
| `--visualizer` | `string` | `bars` *(when `--bg` omitted)* | Path D Auto Visualizer preset ID (`black`, `ember`, `night-line`, `night-fog`, `lunar-halo`, `bars`, `waves`, `cqt`, `radial`, `spectrogram`, `3dscope`, `histogram`, `phase`, `vu`). |
| `--visualizer-theme` | `string` | `studio-mint` | Path D Color Theme (`studio-mint`, `midnight-cyan`, `sunset-amber`, `neon-violet`, `monochrome`, `oled-ember`, `night-indigo`). |
| `--visualizer-resolution` | `string` | `1080p` | Path D output resolution (`1080p` = `1920x1080`, `1440p` = `2560x1440`, `4k` = `3840x2160`). |
| `--visualizer-glow` | `bool` | `true` | Apply subtle `gblur` glow post-pass / blur stage on Path D audio-reactive presets. |
| `--visualizer-brightness` | `int` | `100` | Global linear luminance scale percentage (`10..100`) applied via `colorchannelmixer` while preserving `#000000` black. |
| `--fade-to-black` | `duration` | `0` | Fade video and burned Title Overlays to `#000000` black after this offset (e.g. `15m`; `0` disables). |
| `--dim-titles` | `bool` | `false` | Render `.ass` Title Overlays in low-luminance translucent warm-gray (`&H789EB2AF`) for sleep/dark mixes. |
| `--list-visualizers` | `bool` | `false` | Print all 14 Path D Auto Visualizer presets (with live FFmpeg filter availability), 7 Color Themes, and 3 Output Resolutions and exit `0`. |
| `--track-media` | `string` | `off` | Per-Track visual attachment mode: `off` (Paths A–D), `auto` (discover sidecar media and embedded ID3 `attached_pic`), or `<manifest.json>` path. |
| `--track-media-layout` | `string` | `auto` | Default framing for per-Track visuals: `auto` (`Art Card` for ID3/near-square `aspect <= 1.25`, `Fullscreen` for widescreen), `fullscreen`, or `card`. |
| `--track-transition` | `string` | `cut` | Visual transition at Track segment boundaries when segmented mode is active: `cut` (direct cut) or `dip` (`0.5s` fade-to-black dip). |

---

## 4. Step-by-Step Pipeline Architecture

### Step 1: Track Discovery & Metadata Extraction
1. Scan `--audio-dir` for case-insensitive `.mp3` files. Exit with a clear error if fewer than 1 Track is found.
2. Sort Tracks alphabetically by filename, or shuffle if `--shuffle` is set.
3. For each Track:
   - Run `ffprobe -v error -show_entries format=duration -show_entries format_tags=title -of json <file>` to extract exact duration in seconds and any embedded ID3 `title` tag (or `-show_entries format=duration:format_tags=title:stream=index,codec_type,codec_name,width,height:stream_disposition=attached_pic` when `--track-media` is active to also detect embedded ID3 `attached_pic` artwork).
   - If the ID3 `title` tag is empty, derive Display Title from the filename (strip `.mp3` extension and leading track numbering `^\d+[\s._-]+`).

### Step 2: Audio Processing & Normalization
1. **Optional Loudness Normalization (`--normalize`):** Transcode each Track with `-af "loudnorm=I=-14:TP=-1.5:LRA=11"` and re-probe duration.
2. **Audio Merging (`merged_audio.m4a`):** Use FFmpeg concat demuxer (`--crossfade == 0`) or sequential `acrossfade=d=<crossfade>:c1=tri:c2=tri` (`--crossfade > 0`) at `320k` AAC `48000` Hz.

### Step 3: Timestamp Calculation & `timestamps.txt` Generation
1. Calculate $T_0 = 0.0$ and $T_i = T_{i-1} + \text{Duration}_{i-1} - \text{Crossfade}$.
2. Format timestamps as `MM:SS Display Title` (or `HH:MM:SS Display Title` when total Mix duration $\ge 3600$s).
3. Write the schedule to `--timestamps` starting at `00:00`.

### Step 4: Subtitle-Based Track Overlays (`--overlay-titles`, `--dim-titles`)
Generate `tempDir/titles.ass` (`PlayResX: 1920`, `PlayResY: 1080`, bottom-left `Alignment=1`, `MarginL=60`, `MarginV=55`, `Fontsize=42`) with `{\fad(500,500)}` tags. When `--dim-titles` is enabled, emit low-luminance translucent warm-gray primary (`&H789EB2AF`) and subtle outline (`&H90000000`) instead of bright white (`&H00FFFFFF`). When segmented per-Track rendering is active, generate segment-local `tempDir/seg_<idx>.ass` scripts shifted to `[0, slotDur]` so each segment burns its own overlay prior to `-c:v copy` concatenation.

### Step 5: Smart Video Rendering (4 Execution Paths + Segmented Option A)
All paths stream FFmpeg `-progress pipe:1` telemetry and map merged audio strictly from `merged_audio.m4a`.

- **Path A (Stream Copy Loop):** `--bg` is video/GIF and `--overlay-titles == false`. Pre-normalizes the loop to 1080p30 H.264 and muxes via `-stream_loop -1 -c:v copy -c:a copy -shortest`.
- **Path B (Hardware-Accelerated Overlay Loop):** `--bg` is video/GIF and `--overlay-titles == true`. Loops input, burns `titles.ass`, and encodes via `DetectEncoder`.
- **Path C (Low-FPS Still Image):** `--bg` is static image. Encodes `-loop 1 -tune stillimage` at `2 fps` (no overlays) or `10 fps` (with overlays).
- **Path D (Auto Visualizer):** `--bg` is omitted (`--visualizer-resolution` selects `1080p`, `1440p`, or `4k`):
  - **`black` preset:** Encodes `color=c=black:s=<W>x<H>` at `2 fps` (or `10 fps` with overlays) using `-tune stillimage -preset veryfast -crf 18`, BT.709 color tags, and `-movflags +faststart` (`-map 0:v:0 -map 1:a:0 -c:a copy -shortest`).
  - **13 Audio-Reactive Presets:** Renders at `<W>x<H>`, `30 fps` from `merged_audio.m4a` using `DetectPathDEncoder`, BT.709 color tags, and `-movflags +faststart`:
    - *Dark / Sleep:* `ember` (`showwaves` `scale=cbrt:draw=full` -> `gblur`), `night-line` (`showwaves` hairline `scale=cbrt:draw=full` + `pad`), `night-fog` (`showfreqs` `win_size=8192` `ascale=cbrt` -> `gblur`), `lunar-halo` (`avectorscope` `0.72*H` + `gblur` + `pad`). (Blurred `ember`/`night-fog` cap pre-blur canvas at `1920x1080` and `lunar-halo` at `1036x1036` when `4k` is selected, then upscale with `bicubic`.)
    - *Spectrum:* `bars` (`showfreqs` `ascale=log:win_size=8192:averaging=2`), `cqt` (`showcqt axis=0:text=0:csp=bt709`), `spectrogram` (`showspectrum fps=30`).
    - *Wave & 3D:* `waves` (`showwaves` `scale=cbrt:draw=full`), `radial` (`avectorscope` `H x H` `rf=10:gf=10:bf=10` + `pad`), `3dscope` (`a3dscope`).
    - *Meters:* `histogram` (`ahistogram`), `phase` (`aphasemeter` + `[aout]anullsink`), `vu` (`showvolume` `0.85*W x 0.16*H` `t=0:v=0` + `pad`).
  - **Post-Processing Order (Planar RGB `gbrp`):** All presets emit `format=gbrp` before the glow stage (`split`/`gblur`/`blend=screen` on non-dark presets when `--visualizer-glow` is true) -> `colorchannelmixer=rr=B:gg=B:bb=B` (when `--visualizer-brightness < 100`) -> `subtitles=` (when `--overlay-titles` is true) -> `fade=t=out:st=<st>:d=<d>:color=black` (when `--fade-to-black > 0` and `< totalDuration`) -> `format=yuv420p`. Running `gblur`/`blend` in `gbrp` prevents FFmpeg from auto-inserting `yuv420p` mid-graph and shifting `#000000` black to muddy purple.

- **Segmented Per-Track Rendering (Engine Option A — `--track-media auto|<manifest.json>` or UI Overrides):**
  - **Dual-Layer Composition:** Layer 0 (**Mix Backdrop**) renders continuously as `black` (`#000000`), `media` (`--bg` static image or looping video/GIF), or `visualizer` (Path D preset + theme/glow/brightness/fade). Layer 1 (**Track Visual**) resolves per Track with precedence: (1) explicit UI override or `--track-media <manifest.json>` entry → (2) sidecar `<track_stem>.<ext>` in `--audio-dir` (`.mp4`, `.mov`, `.gif`, `.png`, `.jpg`, `.jpeg`, `.webp`) → (3) extracted ID3 `attached_pic` → (4) `L0 Only` (`backdrop`).
  - **Framing & Auto Layout (`--track-media-layout`):** `fullscreen` scales and pads the Track Visual across the 16:9 canvas; `card` (**Art Card**) centers a visual-only framed card (~62% stage width/height with subtle dark border) over Layer 0; `backdrop` (**L0 Only**) shows Layer 0 uninterrupted. Under `auto`, embedded ID3 artwork and near-square visuals (`width/height <= 1.25`) select `Art Card`, while widescreen visuals select `Fullscreen`.
  - **Uniform Stream Parameters & Concat:** `ResolveSegmentedFPS` selects `30 fps` if any segment renders video or an audio-reactive Path D visualizer, `10 fps` if all segments are still/black with Title Overlays, or `2 fps` otherwise. `ResolveSegmentedDimensions` locks uniform even dimensions (`1920x1080`, or the Path D visualizer resolution). Bounded-parallel `errgroup` workers render each Track slot (`Duration_i - Crossfade`, last Track keeps `Duration_n`) with audio-synced Path D slices (`-ss <StartSec> -t <SlotDuration> -i merged_audio.m4a`), segment-local `seg_<idx>.ass`, optional `0.5s` `--track-transition dip` fades, segment-clamped `--fade-to-black`, BT.709 color tags, and `-video_track_timescale 90000`, then stitch via `ffmpeg -f concat -safe 0 -i seg_concat.txt -i merged_audio.m4a -map 0:v:0 -map 1:a:0 -c:v copy -c:a copy -movflags +faststart`.
  - **Manifest JSON Schema (`--track-media <manifest.json>`):** Supports top-level `layer0` (`mode`, `bgPath`, `visualizer`, `visualizerTheme`, `visualizerResolution`, `visualizerGlow`, `visualizerBrightness`, `fadeToBlack`), `auto` (bool), `layout` (`auto|fullscreen|card`), `transition` (`cut|dip`), and `tracks` (`[{ "file", "media", "layerMode" }]`, accepting `"mode"` as an alias for `"layerMode"`). Relative `bgPath` and `media` paths resolve against the manifest file's directory.

---

## 5. Automated End-to-End Verification Plan
`internal/mixer/e2e_test.go` synthesizes test assets in `t.TempDir()` and executes real `ffmpeg`/`ffprobe` pipelines across two suites:
- **`TestE2EPipeline` (Paths A–D):**
  - **Test 1 (Path A):** Video loop, no overlays, `--crossfade 1` (`7.0s`).
  - **Test 2 (Path B):** Video loop, `--overlay-titles`, `--normalize` (`8.0s`).
  - **Test 3 (Path C):** Static image, `--overlay-titles` (`8.0s`).
  - **Test 4 (Path D Default):** Omitted `--bg`, `bars`, `--visualizer-glow` (`8.0s`), verifying `bt709` color signaling and neutral true-black background chroma via `signalstats` (`YAVG <= 17.0`, `126 <= UAVG/VAVG <= 130`).
  - **Test 5 (Path D Blackout):** Omitted `--bg`, `black` (`2 fps` `-tune stillimage`), verifying `1920x1080` H.264/AAC and near-zero pixel luminance (`YAVG <= 17.0`).
  - **Test 6 (Path D Dark/Sleep Controls):** Omitted `--bg`, `ember`, `--visualizer-brightness 40`, `--fade-to-black 1m` (short test Mix checking fade omission when `st >= totalDuration`), `--overlay-titles`, `--dim-titles` (`8.0s`).
  - **Test 7 (Path D 1440p QHD):** Omitted `--bg`, `waves`, `--visualizer-resolution 1440p`, `--visualizer-glow`, `--overlay-titles` (`8.0s`), verifying `2560x1440` `h264`/`aac` output and BT.709 tags.
- **`TestE2ESegmentedPerTrackMedia` (Engine Option A):**
  - **Test A (Mixed-Media Segmented Dual-Layer Mix):** 4 synthesized `.mp3` Tracks + widescreen 16:9 PNG (`Fullscreen`), looping `.mp4` (`Fullscreen`), square 1:1 PNG (`Art Card` over Path D `bars` Layer 0), and fallback (`L0 Only`) with `--track-media auto`, `--crossfade 1`, `--overlay-titles`, and `--track-transition dip`, verifying `1920x1080` `h264`/`bt709` + `aac`, duration, and `timestamps.txt`.
  - **Test B (Embedded ID3 `attached_pic` + Sidecar Precedence):** Synthesizes an `.mp3` with embedded MJPEG `attached_pic` and a second `.mp3` with both embedded art and a widescreen `.png` sidecar, verifying embedded art extraction, sidecar precedence, and valid `1920x1080` H.264/AAC output.

---

## 6. Embedded Web & Mobile Studio (`ytmix ui`)

`ytmix ui` embeds static HTML/CSS/JS assets (`internal/ui/assets` via `embed.FS`, zero external CDN dependencies) inside a standard-library `net/http` server (`internal/ui/server.go`).

- **Subcommand Flags:** `--host` (default `127.0.0.1`), `--port` (default `8080`), `--no-open`, `--audio-dir`, `--bg`, `--out-dir`, `--out`, `--timestamps`, and repeatable `--allow-dir`.
- **Host / Path Security Boundary:** Loopback sessions (`127.0.0.1` / `::1`) allow reading local `--audio-dir` / `--bg` / `--allow-dir` paths (`localPathsAllowed: true`). Binding non-loopback addresses (`0.0.0.0` for LAN/mobile access) restricts local filesystem browsing/paths (`403`) while continuing to permit browser uploads, per-Track media uploads, and **`bgMode: "visualizer"` / `"black"`** renders.
- **Health & Discovery (`GET /api/health`):** Reports `ffmpeg`/`ffprobe` availability, cached hardware `encoder`, sorted `visualizers` (`14` specs with per-host `available` capability booleans from cached `sync.Once` `DetectFilters`), `visualizerThemes` (`7` palettes), and `visualizerResolutions` (`3` specs).
- **Dual-Layer Composer & Auditioning:**
  - `01 Tracks & Sequence` enables **Per-Track Visuals (`--track-media`)** with global Layout (`Auto`, `Fullscreen`, `Art Card`) and Transition (`Cut`, `0.5s Black Dip`) selectors, HTML5 drag-and-drop Track reordering (with `▲`/`▼` accessible buttons), per-Track `Fullscreen` / `Art Card` / `L0 Only` toggles, and an inline `Media▾` picker for uploading custom per-Track visuals or reverting to auto-paired sidecar/ID3 art.
  - `02 Background Visual (Layer 0 · Mix Backdrop)` provides **Upload File**, **Local Path**, **Auto Visualizer** (category chips, 6-card `3×2` paged preset grid, 7 Color Themes, Subtle Glow, Output Resolution, and Darkness & Sleep controls with `Test 3s Fade`), and **Black (#000)** tabs.
  - Session-scoped endpoints (`POST /api/sessions/{sessionId}/tracks/{trackId}/media`, `GET /api/sessions/{sessionId}/tracks/{trackId}/media`, `DELETE /api/sessions/{sessionId}/tracks/{trackId}/media`) store uploaded/extracted Track Visuals in `sessionState.tempDir` with `X-Content-Type-Options: nosniff`, `Cache-Control: no-store`, and non-loopback `403` local-path guards.
  - `16:9 Stage Preview` composites Layer 0 (black / image / video / 60fps `<canvas>` visualizer) underneath Layer 1 (`Fullscreen`, `Art Card`, or `L0 Only`) + `Title Overlay`, connects **Audition Selected Track** to `GET /api/sessions/{sessionId}/tracks/{trackId}/audio` via Web Audio API `AnalyserNode`, and offers **Download Manifest JSON** for CLI `--track-media` export.
- **SSE Telemetry:** Active jobs stream structured `mixer.TelemetrySink` progress events (`ffmpeg -progress pipe:1`) over `GET /api/render/{id}/events` (`text/event-stream`).
