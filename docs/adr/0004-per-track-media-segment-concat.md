# 0004: Per-Track Segmented Rendering, Uniform Stream Concat, and Dual-Layer Composition

Status: accepted

When per-Track visual attachments are enabled (`--track-media auto`, `--track-media <manifest.json>`, or per-Track overrides in `ytmix ui`), `ytmix` renders each Track slot as an independent, uniform H.264 video segment in bounded-parallel workers (`Engine Option A`) and stitches the segments with `merged_audio.m4a` via FFmpeg's stream-copy `concat` demuxer (`-c:v copy -c:a copy`), rather than building a single monolithic `filter_complex` graph across all Tracks or piping raw frames from Go (`-f rawvideo`).

## Considered Options

- **Engine Option A (Per-Track Segment Render + `concat -c:v copy`) vs. Monolithic `filter_complex` vs. Pure-Go `-f rawvideo` Pipe:**
  - A single `filter_complex` chaining `N` Track media inputs, `scale`/`pad`/`overlay`, `subtitles=`, and video `concat` keeps every Track decoder open simultaneously, hits Windows/shell command-line limits on large playlists, and runs as one serial encode without per-Track parallelism.
  - Piping raw frames from Go via `ffmpeg -f rawvideo` would require pure-Go image/video decoding, scaling, alpha compositing, and subtitle rasterization—violating `ytmix`'s minimal-dependency policy (`go.mod` strict `depguard` allowlist, zero CGo) and saturating memory bandwidth at 1080p30+.
  - **Engine Option A** (`internal/render/segments.go`) slices the Mix into each Track's exact slot (`Duration_i - Crossfade`, with the final Track keeping `Duration_n`), renders segments concurrently via bounded `golang.org/x/sync/errgroup` workers (`min(max(GOMAXPROCS/2, 1), 4)`), writes `seg_concat.txt`, and executes `ffmpeg -f concat -safe 0 -i seg_concat.txt -i merged_audio.m4a -map 0:v:0 -map 1:a:0 -c:v copy -c:a copy -movflags +faststart`.
- **Uniform FPS & Stream Parameters vs. Mixed Frame Rates Across Segments:**
  - Concatenating `2 fps`/`10 fps` `-tune stillimage` segments alongside `30 fps` looping video or Auto Visualizer segments with `-c:v copy` creates variable-frame-rate (VFR) timebase mismatches and PTS drift across MP4 containers and YouTube ingestion.
  - `ResolveSegmentedFPS` selects one uniform frame rate for the entire Mix (`30 fps` whenever any segment effectively renders a video loop or audio-reactive Path D visualizer; `10 fps` when all segments are static/black and any has Title Overlays; `2 fps` otherwise). Every segment enforces identical even dimensions (`ResolveSegmentedDimensions`), `setsar=1`, `format=yuv420p`, BT.709 color signaling, and `-video_track_timescale 90000`.
- **Segment-Local `.ass` & Fade Slicing vs. Full-Mix Post-Concat Re-Encode:**
  - Applying `titles.ass`, `--track-transition dip` (`0.5s` black dip), or `--fade-to-black` after concatenation would force a second full-duration video encode and negate `-c:v copy`.
  - `subtitle.WriteSegmentASSWithOptions` shifts each Track's title cue into segment-local `[0, slotDur]`, while `buildSegmentPostFilters` injects `subtitles=`, boundary `dip` fades, and segment-clamped `--fade-to-black` filters inside each segment's encode (using planar `gbrp` before the final `format=yuv420p` conversion).
- **Audio-Synced Layer 0 Visualizer Slicing vs. Raw `.mp3` Visualizer Inputs:**
  - Generating Path D Layer 0 segments directly from raw `.mp3` files would desynchronize visualizer motion from `--normalize` loudness adjustments and `--crossfade` overlaps. Segments using an audio-reactive Layer 0 slice `merged_audio.m4a` at `-ss <StartSec> -t <SlotDuration>` so visuals match the final Mix audio.

## Consequences

- **Leaf-Package Boundaries Preserved:** `internal/probe` (sidecar discovery and embedded ID3 `attached_pic` probing/extraction), `internal/subtitle` (segment-local `.ass` scripts), and `internal/render` (`Layer0Spec`, `TrackSegmentSpec`, `RenderSegmented`, per-segment `EstimateMix`) remain zero-internal-import leaf packages under `internal/mixer` orchestration.
- **Dual-Layer Composition & Precedence:** Every segment composes Layer 1 (**Track Visual** in `fullscreen`, `card` (**Art Card**), or `backdrop` (**L0 Only**)) against Layer 0 (**Mix Backdrop**: `black`, `media`, or `visualizer`). Resolution follows strict precedence: (1) explicit UI override or `--track-media <manifest.json>` entry → (2) sidecar `<track_stem>.<ext>` in `--audio-dir` → (3) extracted ID3 `attached_pic` → (4) `L0 Only` (`LayerBackdrop`). With `--track-media-layout auto`, embedded ID3 art and near-square visuals (`width/height <= 1.25`) default to `Art Card` while widescreen visuals default to `Fullscreen`.
- **Zero Regression on Opt-Out:** `--track-media` defaults to `off`, routing standard `--bg` and `--visualizer` runs unchanged through Paths A–D.
- **Session-Scoped Studio Integration & MCP Scope:** `ytmix ui` stores per-Track media uploads inside `sessionState.tempDir` via `POST`/`GET`/`DELETE /api/sessions/{sessionId}/tracks/{trackId}/media` while preserving non-loopback `403` local-path guards. `internal/mcp/` schema parity for per-Track media is deferred to a follow-up issue.
