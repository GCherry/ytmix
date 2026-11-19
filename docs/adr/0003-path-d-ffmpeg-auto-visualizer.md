# 0003: Built-In FFmpeg Filtergraphs for Path D Auto Visualizer and Pure Blackout Mode

Status: accepted

When `--bg` is omitted (`Path D`), `ytmix` generates 1080p H.264 video directly via built-in FFmpeg audio visualization filtergraphs (`showfreqs`, `showwaves`, `showcqt`, `avectorscope`, `showspectrum`, `a3dscope`, `ahistogram`, `aphasemeter`, `showvolume`) and a `2 fps` `color=c=black` `-tune stillimage` blackout screen rather than adding CGo multimedia bindings, OpenGL shaders, a headless browser pipeline, or a custom pure-Go frame renderer.

## Considered Options

- **FFmpeg Built-In Filtergraphs vs. Pure-Go `-f rawvideo` / CGo Frame Renderer:** Native CGo graphics bindings (`SDL`/`OpenGL`) or headless browser canvases violate `ytmix`'s zero-dependency `go.mod` constraint (enforced by `depguard`) and complicate cross-platform Windows/macOS/Linux CI builds. Pure-Go custom 2D FFT rendering piped into `ffmpeg -f rawvideo` requires custom FFT math, typography rasterization, and high memory bandwidth at 1080p30; FFmpeg already ships 13 tested audio-reactive filters plus `color`, `gblur`, `colorchannelmixer`, and `fade` natively inside the required `ffmpeg` binary. A custom pure-Go rawvideo engine can still be added incrementally later behind `internal/render` if bespoke geometric scenes are needed.
- **Upfront Cached `ffmpeg -filters` Probe vs. Failing Mid-Render:** Optional filters such as `a3dscope` depend on FFmpeg build version (`>= 5.1`) and compile flags. Probing `ffmpeg -hide_banner -filters` once per CLI preflight or per `ui.Server` (`sync.Once`, skipped when `ffmpeg` is absent) lets `/api/health` grey out unsupported presets and lets CLI / `POST /api/render` fail fast before Step 2 audio processing begins.
- **`color=c=black` `-tune stillimage` (`2 fps` / `10 fps`) vs. Full `30 fps` Render for `black`:** For sleep and blackout mixes, `Path D` `black` mirrors `Path C`'s `2 fps` (or `10 fps` when `--overlay-titles` is enabled) `-tune stillimage` strategy, achieving literal `#000000` frames at minimal CPU cost and small output file size while preserving identical `-map 0:v:0 -map 1:a:0 -c:a copy -shortest` semantics.

## Consequences

- `internal/render` remains a zero-dependency leaf package owning all 14 presets (5 Dark/Sleep + 9 classic), all 7 Color Themes, `DetectFilters`, `VisualizerConfig` validation, and `BuildPathDFilterComplex` / `BuildPathDBlackArgs`.
- Headless Linux compatibility is enforced by sinking `aphasemeter`'s auxiliary audio pad (`[aout]anullsink`) and disabling font-dependent axis text (`axis=0:text=0` on `showcqt`; `t=0:v=0` on `showvolume`).
- Blurred dark presets (`ember`, `night-fog`) render at `960x540` -> `gblur` -> `scale=1920:1080` to keep software encoding fast and bloom smooth.
- `--fade-to-black` (`fade=t=out`) is placed after `subtitles=` so sleep blackouts fade both visuals and Title Overlays to `#000000`, clamping duration on short mixes and omitting cleanly when the fade start exceeds total Mix duration.
- Editorial Mix Studio (`ytmix ui`) pairs the 14 presets with an offline HTML5 `<canvas>` 60fps preview (and optional same-origin `/api/sessions/{sessionId}/tracks/{trackId}/audio` Web Audio `AnalyserNode` streaming) and permits `Path D` rendering on both loopback and LAN sessions without exposing server filesystem paths.
