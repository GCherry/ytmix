# ytmix

`ytmix` is a high-performance, minimal-dependency Go (`1.24+`) CLI tool that wraps `ffmpeg` and `ffprobe` to compile a directory of `.mp3` audio files and either a Background Visual (static image or short looping video/GIF) or an auto-generated music-reactive visualizer into a 1080p `.mp4` Mix ready for YouTube upload, alongside a YouTube-formatted Chapter Timestamps file (`timestamps.txt`).

## Features

- **Minimal, vetted pure-Go dependencies:** Built on the Go standard library plus `golang.org/x/sync/errgroup` for bounded concurrent probing/normalization (zero CGo, zero external CDN assets in the embedded UI), wrapping system `ffmpeg` and `ffprobe`.
- **Smart 4-Path Video Rendering:**
  - **Path A (Stream Copy Loop):** Pre-normalizes a short `.mp4`/`.mov`/`.gif` loop once to 1080p and muxes it across the entire Mix with `-stream_loop -1 -c:v copy` (renders a 60-minute Mix in seconds with zero frame re-encoding).
  - **Path B (Hardware-Accelerated Overlay Loop):** Burns dynamic `.ass` Title Overlays over a looping visual using `h264_videotoolbox` (macOS), `h264_nvenc` (Windows/Linux), or `libx264` fallback.
  - **Path C (Low-FPS Still Image):** Encodes `.png`/`.jpg`/`.jpeg`/`.webp` artwork with `-loop 1 -tune stillimage` at `2 fps` (or `10 fps` with Title Overlays) to cut frame encoding work by up to 93%.
  - **Path D (Auto Visualizer):** When `--bg` is omitted, generates a deterministic 1080p audio-reactive visualizer directly from `merged_audio.m4a` across **14 built-in presets** (5 Dark/Sleep modes + 9 classic visualizers) and **7 Color Themes**, with optional **Darkness & Sleep Controls** (`--visualizer-brightness`, `--fade-to-black`, `--dim-titles`, `--visualizer-glow=false`).
- **Darkness & Sleep Modes (Path D):** Includes a true `#000000` still-image fast path (`black` at `2 fps`, or `10 fps` with titles), ultra-dim OLED-black presets (`ember`, `night-line`, `night-fog`, `lunar-halo`), global brightness scaling (`10%–100%`), exponential fade-to-black over time (`--fade-to-black`), and translucent warm-gray ASS titles (`--dim-titles`).
- **Sample-Accurate Chapter Timestamps:** Probes exact audio durations via `ffprobe`, accounts for `--crossfade` overlap and post-normalization lengths, and writes a YouTube-ready `timestamps.txt` starting at `00:00`.
- **Optional EBU R128 Loudness Normalization:** Normalizes each Track to YouTube's `-14 LUFS` target (`loudnorm=I=-14:TP=-1.5:LRA=11`).
- **Burned-In `.ass` Title Overlays:** Generates Advanced SubStation Alpha (`titles.ass`) subtitles with 500ms fade-in/out tags (`\fad(500,500)`) instead of fragile FFmpeg `drawtext` chains.
- **Per-Track Visuals & Segmented Composition (`--track-media`):** Optionally pairs individual `.mp3` Tracks with sidecar visuals (`<track_stem>.<ext>`), embedded ID3 `attached_pic` cover art, or a JSON manifest (`--track-media <manifest.json>`) composed against a Layer 0 Mix Backdrop (`Black #000000`, `--bg`, or Path D Auto Visualizer) in `Fullscreen`, `Art Card`, or `L0 Only` framing with optional `0.5s` `--track-transition dip` boundaries and bounded-parallel Engine Option A `concat -c:v copy` rendering.
- **Embedded Web & Mobile Studio (`ytmix ui`):** Serves the Dual-Layer Composer Editorial Mix Studio UI directly from the single Go binary via `net/http` + `embed.FS` + Server-Sent Events (`text/event-stream`), supporting drag-and-drop MP3/Background Visual uploads, drag-and-drop Track reordering, **Layer 0 Mix Backdrop** (`Upload`, `Local Path`, `Auto Visualizer`, `Black #000`), per-Track `Fullscreen`/`Art Card`/`L0 Only` toggles + inline `Media▾` picker, live composited **16:9 Stage Preview** (60fps `<canvas>` + Track Visual layer), **Audition Selected Track** via Web Audio `AnalyserNode`, **Download Manifest JSON** export, and real-time 5-step telemetry.
- **MCP Stdio Server (`ytmix mcp`):** Exposes a pure Go standard-library Model Context Protocol server over newline-delimited JSON-RPC 2.0 `stdio` (`2025-06-18` / `2025-03-26` / `2024-11-05`) so AI assistants (Antigravity, Claude Desktop, Cursor) can inspect host FFmpeg/FFprobe availability, detected H.264 encoders, Path D Auto Visualizer presets/themes/resolutions (`get_capabilities`), audio Track folders (`inspect_tracks`), Background Visual assets (`inspect_background`), dry-run Mix plans and Crossfade-adjusted Chapter Timestamps (`plan_mix`), single-frame Path D PNG previews (`preview_visualizer_frame`), asynchronous 5-step Mix rendering with Display Title overrides, live progress/cancellation, and output verification (`start_render`, `get_render_status`, `cancel_render`, `verify_mix`), static discovery + live job MCP resources (`ytmix://presets`, `ytmix://themes`, `ytmix://resolutions`, `ytmix://jobs/{id}`, `ytmix://jobs/{id}/timestamps`), and guided workflow MCP prompts (`create-sleep-mix`, `clean-youtube-release`).

## Prerequisites

- **Go** `1.24+`
- **FFmpeg** and **FFprobe** installed and available on your system `PATH`

Verify prerequisites:

```powershell
go version
ffmpeg -version
ffprobe -version
```

## Build

```powershell
go build -o ytmix.exe ./cmd/ytmix
```

*(On macOS/Linux: `go build -o ytmix ./cmd/ytmix`)*

## Usage Examples

```powershell
# List all 14 built-in Path D Auto Visualizer presets, FFmpeg filter availability, and 7 Color Themes
./ytmix --list-visualizers

# Path D (Default Auto Visualizer): Omit --bg to generate a 1080p bars visualizer
./ytmix --audio-dir ./tracks --out mix.mp4

# Path D (Darkness & Sleep): Dim Ember Pulse at 45% brightness, fading to #000000 after 15m with dimmed titles
./ytmix --audio-dir ./tracks --visualizer ember --visualizer-theme oled-ember --visualizer-brightness 45 --fade-to-black 15m --overlay-titles --dim-titles --out sleep_mix.mp4

# Path D (Pure Blackout Screen): True #000000 2fps still-image screen for blackout sleep mixes
./ytmix --audio-dir ./tracks --visualizer black --out blackout_sleep.mp4

# Path A: Fast stream-copy video loop with 2-second audio crossfades
./ytmix --audio-dir ./tracks --bg loop_visual.mp4 --crossfade 2 --out mix.mp4

# Path B: Looping video with -14 LUFS loudness normalization and burned-in track titles (shown for 10s each)
./ytmix --audio-dir ./tracks --bg loop_visual.mp4 --normalize --overlay-titles --title-duration 10 --out mix.mp4

# Path C: Static cover art with shuffled tracks and full-track title overlays
./ytmix --audio-dir ./tracks --bg cover_art.png --shuffle --overlay-titles --out mix.mp4

# Per-Track Visuals (Auto): Discover sidecars & ID3 cover art over Path D bars with 0.5s dip transitions
./ytmix --audio-dir ./tracks --track-media auto --track-media-layout auto --track-transition dip --overlay-titles --out mix.mp4

# Per-Track Visuals (Manifest): Explicit dual-layer configuration via JSON manifest
./ytmix --audio-dir ./tracks --track-media ./track_media_manifest.json --out mix.mp4

# Launch the Editorial Mix Studio web UI (default: http://127.0.0.1:8080)
./ytmix ui

# Expose the studio on your LAN for mobile uploads & Path D Auto Visualizer renders (restricts local filesystem paths)
./ytmix ui --host 0.0.0.0 --port 8080 --no-open

# Run ytmix as a stdio Model Context Protocol (MCP) JSON-RPC 2.0 server
./ytmix mcp
```

## Path D Visualizer Presets & Themes

Run `./ytmix --list-visualizers` to inspect live FFmpeg filter availability on your machine:

- **Dark / Sleep Presets (`isDark`):** `black` (`Pure Blackout Screen`, 2 fps `#000000`), `ember` (`Dim Ember Pulse`), `night-line` (`Midnight Whisper Line`), `night-fog` (`Sub-Bass Night Fog`), `lunar-halo` (`Dim Lunar Halo`).
- **Classic Presets:** `bars` (default), `waves`, `cqt`, `radial`, `spectrogram`, `3dscope`, `histogram`, `phase`, `vu`.
- **Color Themes:** `studio-mint` (default), `midnight-cyan`, `sunset-amber`, `neon-violet`, `monochrome`, `oled-ember`, `night-indigo`.

## Web UI Subcommand (`ytmix ui`)

| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--host` | `string` | `127.0.0.1` | HTTP bind host (`0.0.0.0` enables LAN/mobile uploads and Path D renders with local filesystem paths disabled). |
| `--port` | `int` | `8080` | HTTP listen port (`0` selects an ephemeral port). |
| `--no-open` | `bool` | `false` | Do not automatically launch the default browser on startup. |
| `--audio-dir` | `string` | `./tracks` | Default local folder path prefilled in the UI (loopback sessions only). |
| `--bg` | `string` | `""` | Default local Background Visual path prefilled in the UI (loopback sessions only). |
| `--out-dir` | `string` | `""` | Default output directory prefilled in the UI and allowed for local reads/writes. |
| `--out` | `string` | `output.mp4` | Default output `.mp4` filename prefilled in the UI. |
| `--timestamps` | `string` | `timestamps.txt` | Default Chapter Timestamps filename prefilled in the UI. |
| `--allow-dir` | `[]string` | `""` | Additional local directory allowed for loopback UI browsing and output (repeatable). |

## MCP Server Subcommand (`ytmix mcp`)

`ytmix mcp` runs a newline-delimited JSON-RPC 2.0 MCP server over `stdin`/`stdout` (with all diagnostics strictly on `stderr` so `stdout` never contains non-JSON output).

- **Supported MCP Methods:** `initialize` (`2025-06-18`, `2025-03-26`, `2024-11-05`), `notifications/initialized`, `ping`, `tools/list`, `tools/call`, `resources/list`, `resources/read`, `prompts/list`, and `prompts/get`.
- **Registered Tools:**
  - `get_capabilities` *(read-only, idempotent)* — probes `ffmpeg` and `ffprobe` availability, detected H.264 encoder (`h264_videotoolbox`, `h264_nvenc`, or `libx264` fallback) and Path D encoder info, all 14 Path D Auto Visualizer presets with live filter availability, 7 Color Themes, 3 output resolutions (`1080p`, `1440p`, `4k`), supported Background Visual extensions, and host `GOOS`.
  - `inspect_tracks` *(read-only, idempotent)* — audits an `audioDir` of `.mp3` Tracks in default sorted order via `ffprobe`, returning durations, `rawId3Title` vs. resolved `displayTitle`, total and Crossfade-adjusted `estimatedMixDurationSec`, and pre-flight warnings (Tracks `<= crossfade` or `<= 2 * crossfade`, single-track Crossfade skips, and messy filenames lacking ID3 title tags).
  - `inspect_background` *(read-only, idempotent)* — probes a Background Visual (`bgPath`) via `ffprobe`, returning media `kind` (`image` or `video`), dimensions, reduced `aspectRatio`, `is16x9`, duration, `fps`, `codec`, `pixFmt`, `nbFrames`, `hasAudio`, and pre-flight warnings (non-16:9 aspect ratios, sub-1080p resolution, missing dimensions, or looping videos containing stray audio streams).
  - `plan_mix` *(read-only, idempotent)* — simulates a Mix dry-run from `audioDir` or an explicit `tracks` list with zero disk writes, returning the selected `renderingPath` (`Path A`/`B`/`C`/`D`), `tradeOffNotes` (including `Path A` stream-copy vs. `Path B` full re-encode when `overlayTitles` is toggled), `render.MixEstimate` band and summary, Crossfade-adjusted `chapters` and `timestampsText`, and `titleOverlayPreview`.
  - `preview_visualizer_frame` *(read-only, idempotent)* — renders a single PNG preview frame for any available Path D Auto Visualizer `preset`, `theme`, `resolution`, `glow`, `brightness`, `fadeToBlack`, and optional `.ass` Title Overlay (`overlayTitles`, `dimTitles`, `titleText`) from an optional `.mp3` `audioPath` or synthetic stereo test tone in a cleaned-up temporary workspace, returning an MCP base64 PNG `image` block and metadata.
  - `start_render` *(background job, single active render)* — pre-flights a Mix configuration (`audioDir` or explicit ordered `tracks` with optional `displayTitle` overrides that leave `.mp3` files on disk untouched), spawns the 5-step pipeline asynchronously, and returns `{ jobId, status, renderingPath, outPath, timestampsPath, trackCount, estimate, resourceUri, timestampsResourceUri }` immediately.
  - `get_render_status` *(read-only, idempotent)* — polls live step (`1–5`), `progress` %, FFmpeg `speed`, `outTimeSec`, `remainingSec`, `estimate`, error details, and output paths for a `jobId`.
  - `cancel_render` *(idempotent)* — cancels a running `jobId` via `context.Context` and triggers temporary workspace cleanup.
  - `verify_mix` *(read-only, idempotent)* — runs `ffprobe` on `outPath` (confirming 1 H.264 video stream, 1 AAC audio stream, no stray audio streams, positive duration, and 16:9 / 1080p warnings) and optionally validates `timestampsPath` (`00:00` / `00:00:00` start, monotonic timestamps, and consistency with video duration).
- **Registered Resources:**
  - `ytmix://presets` (`application/json`) — JSON catalog of all 14 Path D Auto Visualizer presets (`id`, `name`, `filter`, `category`, `isDark`, `description`, `available`, `missingFilter`) with cached live FFmpeg filter availability.
  - `ytmix://themes` (`application/json`) — JSON catalog of all 7 Path D Color Themes (`id`, `name`, `bg`, `primary`, `secondary`, `glow`).
  - `ytmix://resolutions` (`application/json`) — JSON catalog of supported Path D output resolutions (`1080p`, `1440p`, `4k` with `width`, `height`, `description`).
  - `ytmix://jobs/{id}` (`application/json`) — live JSON status and telemetry snapshot for each tracked render job (bounded ring of 5 jobs).
  - `ytmix://jobs/{id}/timestamps` (`text/plain`) — generated YouTube Chapter Timestamps (`timestamps.txt`) once the job reaches `complete`.
- **Registered Prompts:**
  - `create-sleep-mix` *(optional `audioDir`, `targetDuration`)* — step-by-step workflow to inspect Tracks, curate a calm Track sequence, configure Dark/Sleep `Path D` Auto Visualizer presets (`black`, `ember`, `night-line`, `night-fog`, `lunar-halo`), dark Color Themes (`oled-ember`, `night-indigo`, `monochrome`), `--fade-to-black`, and `--dim-titles` (with optional `preview_visualizer_frame`), run `plan_mix` -> `start_render` -> `get_render_status` -> `verify_mix`, and draft a sleep-Mix YouTube description with Chapter Timestamps.
  - `clean-youtube-release` *(optional `audioDir`, `bgPath`)* — step-by-step workflow to inspect Tracks and Background Visuals (or suggest `Path D` Auto Visualizer when `bgPath` is omitted), audit Display Titles for messy source file-stem artifacts, confirm Display Title overrides and Track ordering without modifying `.mp3` files on disk, preview `Rendering Path` trade-offs (`Path A` vs. `Path B`) via `plan_mix`, render and verify via `start_render` -> `get_render_status` -> `verify_mix`, and draft YouTube release copy with Chapter Timestamps.

Example MCP client configuration (`mcpServers`):

```json
{
  "mcpServers": {
    "ytmix": {
      "command": "ytmix",
      "args": ["mcp"]
    }
  }
}
```

## CLI Flags

| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--audio-dir` | `string` | `./tracks` | Directory containing input `.mp3` files. |
| `--bg` | `string` | `""` | Optional path to Background Visual (`.jpg`, `.jpeg`, `.png`, `.webp`, `.mp4`, `.mov`, `.gif`). Omit to use **Path D** Auto Visualizer. |
| `--out-dir` | `string` | `""` | Directory where relative `--out` and `--timestamps` paths are written. |
| `--out` | `string` | `output.mp4` | Output `.mp4` video path (relative paths resolve inside `--out-dir` when set). |
| `--timestamps` | `string` | `timestamps.txt` | Output file path for YouTube Chapter Timestamps (relative paths resolve inside `--out-dir` when set). |
| `--crossfade` | `float64` | `0` | Crossfade duration in seconds between Tracks (`0` uses fast concat demuxer; `>0` uses `acrossfade`). |
| `--normalize` | `bool` | `false` | Normalize each Track to YouTube's `-14 LUFS` target (`loudnorm=I=-14:TP=-1.5:LRA=11`). |
| `--overlay-titles` | `bool` | `false` | Burn the current Display Title into the bottom-left corner of the video via dynamic `.ass` subtitles. |
| `--title-duration` | `int` | `0` | Seconds to display each Display Title (`0` = stays visible for the entire Track; `>0` = fades in/out for the first $N$ seconds). |
| `--font-name` | `string` | `Arial` | Font family name used inside the generated `.ass` subtitle script. |
| `--shuffle` | `bool` | `false` | Randomize Track order instead of sorting alphabetically by filename. |
| `--visualizer` | `string` | `""` | Path D Auto Visualizer preset when `--bg` is omitted (defaults to `bars`; see `--list-visualizers`). Mutually exclusive with `--bg`. |
| `--visualizer-theme` | `string` | `studio-mint` | Path D Color Theme (`studio-mint`, `midnight-cyan`, `sunset-amber`, `neon-violet`, `monochrome`, `oled-ember`, `night-indigo`). |
| `--visualizer-glow` | `bool` | `true` | Apply subtle glow blur on Path D Auto Visualizer presets (`--visualizer-glow=false` disables). |
| `--visualizer-brightness` | `int` | `100` | Global Path D visualizer brightness percentage (`10`–`100`). |
| `--fade-to-black` | `duration` | `0s` | Fade Path D video and burned Title Overlays to `#000000` black after this duration (e.g., `15m`; `0` disables). |
| `--dim-titles` | `bool` | `false` | Use low-luminance translucent warm-gray ASS styling (`&H789EB2AF`) for Title Overlays. |
| `--list-visualizers` | `bool` | `false` | Print all 14 built-in Path D presets (with live FFmpeg filter availability) and 7 Color Themes and exit. |
| `--track-media` | `string` | `off` | Per-Track visual mode: `off` (Paths A–D), `auto` (sidecar + ID3 `attached_pic`), or `<manifest.json>` path. |
| `--track-media-layout` | `string` | `auto` | Default framing for per-Track visuals: `auto` (`Art Card` for ID3/square `aspect <= 1.25`, `Fullscreen` for widescreen), `fullscreen`, or `card`. |
| `--track-transition` | `string` | `cut` | Visual transition at Track segment boundaries: `cut` (direct cut) or `dip` (`0.5s` fade-to-black dip). |

## Per-Track Media Manifest (`--track-media <manifest.json>`)

When `--track-media` points to a `.json` file, `ytmix` resolves relative `bgPath` and `media` paths against the manifest's directory and applies precedence **Manifest Entry → Sidecar (`<track_stem>.<ext>`) → Embedded ID3 `attached_pic` → `L0 Only`**:

```json
{
  "layer0": {
    "mode": "visualizer",
    "visualizer": "bars",
    "visualizerTheme": "studio-mint",
    "visualizerResolution": "1080p",
    "visualizerGlow": true,
    "visualizerBrightness": 100
  },
  "auto": true,
  "layout": "auto",
  "transition": "dip",
  "tracks": [
    { "file": "01_Opening.mp3", "media": "./art/opening_wide.png", "layerMode": "fullscreen" },
    { "file": "02_Midnight.mp3", "media": "./art/cover_sq.png", "layerMode": "card" },
    { "file": "03_Outro.mp3", "layerMode": "backdrop" }
  ]
}
```

## Documentation & Architecture

- [Technical Specification](docs/specs/ytmix_technical_specification.md) — full pipeline rules, FFmpeg filter chains, and end-to-end verification suite.
- [Interactive Architecture & Pipeline Blueprint](docs/specs/ytmix_architecture_pipeline_blueprint.html) — interactive HTML simulator of CLI flags and Rendering Paths.
- [Domain Glossary](GLOSSARY.md), [ADR 0001](docs/adr/0001-ffmpeg-rendering-paths-and-ass-subtitles.md), [ADR 0002](docs/adr/0002-embedded-http-ui-and-sse-telemetry.md), [ADR 0003](docs/adr/0003-path-d-ffmpeg-auto-visualizer.md), and [ADR 0004](docs/adr/0004-per-track-media-segment-concat.md).
