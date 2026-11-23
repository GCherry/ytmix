# Technical Specification: `ytmix` — Automated YouTube Mix & Compilation Builder CLI

## 1. Overview & Objective
`ytmix` is a high-performance, zero-dependency Go CLI tool (with an embedded stdlib web studio `ytmix ui`) that wraps `ffmpeg` and `ffprobe` to compile a directory of `.mp3` audio files and either a Background Visual (static image or looping video/GIF) or an Auto Visualizer (`Path D`) into a 1080p `.mp4` video ready for YouTube upload. Alongside the video, it calculates exact Track start times and outputs a YouTube-formatted chapter file (`timestamps.txt`).

---

## 2. System & Architectural Constraints
- **Language:** Go (`1.24+`), utilizing standard library packages only (`go.mod` enforced by `depguard`).
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

---

## 4. Step-by-Step Pipeline Architecture

### Step 1: Track Discovery & Metadata Extraction
1. Scan `--audio-dir` for case-insensitive `.mp3` files. Exit with a clear error if fewer than 1 Track is found.
2. Sort Tracks alphabetically by filename, or shuffle if `--shuffle` is set.
3. For each Track:
   - Run `ffprobe -v error -show_entries format=duration -show_entries format_tags=title -of json <file>` to extract exact duration in seconds and any embedded ID3 `title` tag.
   - If the ID3 `title` tag is empty, derive Display Title from the filename (strip `.mp3` extension and leading track numbering `^\d+[\s._-]+`).

### Step 2: Audio Processing & Normalization
1. **Optional Loudness Normalization (`--normalize`):** Transcode each Track with `-af "loudnorm=I=-14:TP=-1.5:LRA=11"` and re-probe duration.
2. **Audio Merging (`merged_audio.m4a`):** Use FFmpeg concat demuxer (`--crossfade == 0`) or sequential `acrossfade=d=<crossfade>:c1=tri:c2=tri` (`--crossfade > 0`) at `320k` AAC `48000` Hz.

### Step 3: Timestamp Calculation & `timestamps.txt` Generation
1. Calculate $T_0 = 0.0$ and $T_i = T_{i-1} + \text{Duration}_{i-1} - \text{Crossfade}$.
2. Format timestamps as `MM:SS Display Title` (or `HH:MM:SS Display Title` when total Mix duration $\ge 3600$s).
3. Write the schedule to `--timestamps` starting at `00:00`.

### Step 4: Subtitle-Based Track Overlays (`--overlay-titles`, `--dim-titles`)
Generate `tempDir/titles.ass` (`PlayResX: 1920`, `PlayResY: 1080`, bottom-left `Alignment=1`, `MarginL=60`, `MarginV=55`, `Fontsize=42`) with `{\fad(500,500)}` tags. When `--dim-titles` is enabled, emit low-luminance translucent warm-gray primary (`&H789EB2AF`) and subtle outline (`&H90000000`) instead of bright white (`&H00FFFFFF`).

### Step 5: Smart Video Rendering (4 Execution Paths)
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

---

## 5. Automated End-to-End Verification Plan
`internal/mixer/e2e_test.go` (`TestE2EPipeline`) synthesizes test `.mp3` Tracks, a looping `.mp4`, and a static `.png` in `t.TempDir()`, executes real `ffmpeg`/`ffprobe` pipelines across:
- **Test 1 (Path A):** Video loop, no overlays, `--crossfade 1` (`7.0s`).
- **Test 2 (Path B):** Video loop, `--overlay-titles`, `--normalize` (`8.0s`).
- **Test 3 (Path C):** Static image, `--overlay-titles` (`8.0s`).
- **Test 4 (Path D Default):** Omitted `--bg`, `bars`, `--visualizer-glow` (`8.0s`), verifying `bt709` color signaling and neutral true-black background chroma via `signalstats` (`YAVG <= 17.0`, `126 <= UAVG/VAVG <= 130`).
- **Test 5 (Path D Blackout):** Omitted `--bg`, `black` (`2 fps` `-tune stillimage`), verifying `1920x1080` H.264/AAC and near-zero pixel luminance (`YAVG <= 17.0`).
- **Test 6 (Path D Dark/Sleep Controls):** Omitted `--bg`, `ember`, `--visualizer-brightness 40`, `--fade-to-black 1m` (short test Mix checking fade omission when `st >= totalDuration`), `--overlay-titles`, `--dim-titles` (`8.0s`).
- **Test 7 (Path D 1440p QHD):** Omitted `--bg`, `waves`, `--visualizer-resolution 1440p`, `--visualizer-glow`, `--overlay-titles` (`8.0s`), verifying `2560x1440` `h264`/`aac` output and BT.709 tags.

---

## 6. Embedded Web & Mobile Studio (`ytmix ui`)

`ytmix ui` embeds static HTML/CSS/JS assets (`internal/ui/assets` via `embed.FS`, zero external CDN dependencies) inside a standard-library `net/http` server (`internal/ui/server.go`).

- **Subcommand Flags:** `--host` (default `127.0.0.1`), `--port` (default `8080`), `--no-open`, `--audio-dir`, `--bg`, `--out-dir`, `--out`, `--timestamps`, and repeatable `--allow-dir`.
- **Host / Path Security Boundary:** Loopback sessions (`127.0.0.1` / `::1`) allow reading local `--audio-dir` / `--bg` / `--allow-dir` paths (`localPathsAllowed: true`). Binding non-loopback addresses (`0.0.0.0` for LAN/mobile access) restricts local filesystem browsing/paths (`403`) while continuing to permit browser uploads and **`bgMode: "visualizer"` (Path D)** renders.
- **Health & Discovery (`GET /api/health`):** Reports `ffmpeg`/`ffprobe` availability, cached hardware `encoder`, sorted `visualizers` (`14` specs with per-host `available` capability booleans from cached `sync.Once` `DetectFilters`), and `visualizerThemes` (`7` palettes).
- **Variant A Studio & Auditioning:**
  - `02 Background Visual` adds an **Auto Visualizer** tab alongside **Upload File** and **Local Path**, displaying category filter chips (`All`, `Dark / Sleep`, `Spectrum`, `Wave & 3D`, `Meters`), a 6-card `3×2` paged preset grid (`◀`/`▶` + pagination dots), 7 Color Themes, Subtle Glow toggle, Darkness & Sleep controls, and a `Test 3s Fade` preview toggle.
  - `16:9 Stage Preview` renders 60fps `<canvas>` simulations for all 14 presets and connects **Audition Selected Track** to `GET /api/sessions/{sessionId}/tracks/{trackId}/audio` (`Content-Type: audio/mpeg`, `X-Content-Type-Options: nosniff`, `Cache-Control: no-store`, `http.ServeContent`) via Web Audio API `AnalyserNode`.
- **SSE Telemetry:** Active jobs stream structured `mixer.TelemetrySink` progress events (`ffmpeg -progress pipe:1`) over `GET /api/events` (`text/event-stream`).
