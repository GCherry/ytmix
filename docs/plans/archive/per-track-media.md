# Per-Track Media Attachment & Dual-Layer Composer

**Origin:** Nov 25, 2026 — User request via `/coordinator`, tracked in [`GCherry/ytmix#47`](https://github.com/GCherry/ytmix/issues/47)

Allow each **Track** (`.mp3`) in a compiled `ytmix` **Mix** to attach its own static image or looping video/GIF (or embedded ID3 `attached_pic`), rendered via **Engine Option A** (bounded-parallel per-Track segment rendering + FFmpeg `concat` demuxer) and configured through the **Dual-Layer Composer** UI in `ytmix ui` and opt-in CLI flags (`--track-media`, `--track-media-layout`, `--track-transition`).

## For Future Agents

The plan file is the source of truth — a chat summary is not a handoff. Last step of every agent, including subagents: mark checkboxes `- [x]`; set phase status; write **Phase Summary**; record **Verification Plan** results. When all phases are done, fill **Final Recap** and **Deployment Plan**, then archive this file to `docs/plans/archive/per-track-media.md` per the `real-work` skill (Section 3).

**Repo:** `ytmix` (`D:\CodingWorkspace\ytmix\ytmix`) — read [`AGENTS.md`](../../../AGENTS.md) before building or testing.

**Branch:** `feature/per-track-media` off `main` (worktree: `.worktrees/feature-per-track-media` if parallel work is active).

**Domain docs:** [`GLOSSARY.md`](../../../GLOSSARY.md), [`docs/adr/0001-ffmpeg-rendering-paths-and-ass-subtitles.md`](../../adr/0001-ffmpeg-rendering-paths-and-ass-subtitles.md), [`docs/adr/0002-embedded-http-ui-and-sse-telemetry.md`](../../adr/0002-embedded-http-ui-and-sse-telemetry.md), [`docs/adr/0003-path-d-ffmpeg-auto-visualizer.md`](../../adr/0003-path-d-ffmpeg-auto-visualizer.md), [`docs/specs/ytmix_technical_specification.md`](../../specs/ytmix_technical_specification.md).

**Related skills:** `tdd` (unit & synthetic FFmpeg E2E tests), `code-review` (pre-merge 2-axis review), `domain-modeling` (`GLOSSARY.md` + `docs/adr/0004-...`).

## Open Questions

| # | Question | Owner | Resolve by | Blocking? |
|---|----------|-------|------------|-----------|

### Resolved

| # | Question | Resolution |
|---|----------|------------|
| 1 | Mixed-segment `concat -c:v copy` PTS drift | Resolved Nov 25, 2026 02:55:31 PM EST — Initial `TestE2ESegmentedPerTrackMedia` Test A caught `-0.2333s` VFR drift from `showfreqs` when `fps=30` was skipped (`internal/render/segments.go:352`). Making `fps=%d` unconditional alongside `-video_track_timescale 90000` produced exact `30/1` CFR and `0.0000s` drift (`sched=13.0000s`, `video=13.0000s`, `audio=13.0000s`). |
| 2 | Multi-stream `attached_pic` MP3 probing | Resolved Nov 25, 2026 02:21:13 PM EST — `probe.ProbeEmbeddedArt` / `ProbeFileWithArt` deterministically selects the first stream with `codec_type == "video"` and `disposition.attached_pic == 1`; verified in `internal/probe/ffprobe_test.go` (Phase 1A). |

| # | Question | Resolution |
|---|----------|------------|
| Q1 | CLI default for `--track-media` | Resolved Nov 25, 2026 02:10:55 PM EST — User confirmed opt-in default `--track-media off`, with `--track-media auto` and `--track-media <manifest.json>`. |
| Q2 | `Art Card` composition & media types | Resolved Nov 25, 2026 02:10:55 PM EST — User confirmed visual-only centered framed card (~55–60% stage height) supporting both static images and looping videos/GIFs, keeping bottom-left `.ass` `Title Overlays` as the single text source. |
| Q3 | Transitions between Track visuals | Resolved Nov 25, 2026 02:10:55 PM EST — User confirmed default `cut` with optional `0.5s` fade-to-black `dip` (`--track-transition cut\|dip`). |
| Q4 | Default layer mode for auto-discovered media | Resolved Nov 25, 2026 02:10:55 PM EST — User confirmed `--track-media-layout auto\|fullscreen\|card` (`auto` picks `Art Card` for square/ID3 covers and `Fullscreen` for widescreen media). |
| Q5 | Estimator & MCP server scope for `#47` | Resolved Nov 25, 2026 02:10:55 PM EST — User confirmed per-Track estimation is in scope for `#47`; `internal/mcp/` tool parity deferred to a follow-up issue. |

## Decisions (confirmed)

| Topic | Decision |
|-------|----------|
| Rendering Engine | **Option A (Per-Track Segment Render + FFmpeg `concat` Demuxer)**: bounded-parallel `errgroup` workers render uniform H.264 segments sized to each Track slot (`Duration_i - Crossfade`, last Track keeps `Duration_n`), then stitch via `ffmpeg -f concat -safe 0 -i video_concat.txt -i merged_audio.m4a -map 0:v:0 -map 1:a:0 -c:v copy -c:a copy -movflags +faststart`. |
| Uniform Stream Parameters | All segments in a segmented Mix share identical resolution (`--visualizer-resolution` or `1920x1080`), `setsar=1`, `format=yuv420p`, `bt709`, H.264 profile, and **uniform FPS** (`30 fps` if any segment uses video or Auto Visualizer; `2`/`10 fps` `-tune stillimage` only if every segment is static image or `black`). |
| Layer 0 (`Mix Backdrop`) | Continuous Mix-level canvas supporting `Black (#000000)`, `Image / Video` (`--bg`), or `Auto Visualizer` (`--visualizer` + theme/glow/brightness). Existing `--bg` vs `--visualizer` mutual exclusion remains intact. |
| Audio-Synced Visualizer Slices | Segments using an Auto Visualizer Layer 0 slice `merged_audio.m4a` at `-ss <T_i> -t <slotDur>` so visuals stay synchronized with normalized and crossfaded Mix audio. |
| Per-Segment Overlays & Fades | Slice `.ass` `Title Overlay` cues to segment-local `[0, slotDur]` and apply `--fade-to-black` / `--track-transition dip` per segment so final `concat` stays pure `-c:v copy` with zero double re-encode. |
| Media Resolution Precedence | (1) Explicit UI override or CLI manifest → (2) Sidecar file in `--audio-dir` (`<track_stem>.<ext>`) → (3) Embedded ID3 `attached_pic` → (4) `L0 Only` (Layer 0 Mix Backdrop). |
| Per-Track Layer Modes | `Fullscreen` (fills 16:9 canvas via `scale` + `pad`), `Art Card` (centered visual-only framed card over Layer 0, supporting images and looping videos/GIFs), or `L0 Only` (uninterrupted Layer 0). |
| Auto Layout Heuristic | `--track-media-layout auto` (default when `--track-media` is active) selects `Art Card` for embedded ID3 art or near-square media (`aspect <= 1.25`), and `Fullscreen` for widescreen media. |
| CLI Flags (Opt-In) | `--track-media off\|auto\|<manifest.json>` (default `off`), `--track-media-layout auto\|fullscreen\|card` (default `auto`), `--track-transition cut\|dip` (default `cut`). Existing `--bg` and `--visualizer` runs are 100% unchanged when `--track-media off`. |
| UI Architecture (`ytmix ui`) | **Dual-Layer Composer**: Layer 0 Backdrop Bar (`Black`, `Image / Video`, `Auto Visualizer`) + drag-and-drop Track sequence (`▲`/`▼` accessible fallback) + per-Track `Fullscreen`/`Art Card`/`L0 Only` toggles + inline `Media▾` picker + live 16:9 composited Stage Preview. |
| UI Session Media Endpoints | Session-scoped `POST /api/sessions/{sessionId}/tracks/{trackId}/media`, `GET /api/sessions/{sessionId}/tracks/{trackId}/media`, and `DELETE /api/sessions/{sessionId}/tracks/{trackId}/media` backed by `sessionState.tempDir`, preserving `defaultMaxSessions` cleanup and non-loopback `403` local-path security. |
| Estimator & MCP Parity | Update `internal/render/estimate.go` and `POST /api/estimate` to sum per-Track segment render times. Defer `internal/mcp/` schema parity to a separate follow-up issue. |

## Grill

**Recorded:** Nov 25, 2026 02:11:00 PM EST

### Design tree

```text
Per-Track Media (#47)
├── Engine: Option A (Segment + concat -c:v copy)
│   ├── Stream uniformity -> uniform FPS (30 vs 2/10), 1920x1080/target res, yuv420p, bt709 [Auto-taken: Critical]
│   ├── Overlays & sleep fade -> per-segment .ass cue shift + per-segment fade=t=out [Auto-taken: Critical]
│   ├── L0 Auto Visualizer sync -> slice merged_audio.m4a at -ss T_i -t slotDur [Auto-taken: Critical]
│   └── Transitions -> cut (default) | dip (0.5s fade in/out) [Confirmed Q3]
├── Media Resolution & CLI
│   ├── Opt-in flag -> --track-media off|auto|<manifest.json> [Confirmed Q1]
│   ├── Precedence -> Override/Manifest > Sidecar > ID3 attached_pic > L0 Only [Confirmed #47]
│   ├── Default layout -> --track-media-layout auto|fullscreen|card (ID3/square -> Card) [Confirmed Q4]
│   └── Art Card shape -> centered visual-only framed card, image + looping video/GIF [Confirmed Q2]
├── Studio UI (Dual-Layer Composer)
│   ├── Layer 0 bar -> Black | Image/Video | Auto Visualizer + Theme [Confirmed #47]
│   ├── Track sequence -> HTML5 drag-and-drop + ▲/▼ fallback [Confirmed #47]
│   └── Upload security -> session-scoped media endpoints, non-loopback 403 preserved [Auto-taken: Critical]
└── Scope Boundaries
    ├── Estimator -> sum per-Track segment estimates [Confirmed Q5]
    ├── MCP parity -> deferred to follow-up issue [Confirmed Q5]
    └── Domain docs -> GLOSSARY.md terms + ADR-0004 (Option A dual-layer segment concat) [Auto-taken: Low-risk]
```

### Auto-taken (Critical / Low-risk)

| # | Class | Rec | Where applied |
|---|-------|-----|---------------|
| G1 | Critical | Enforce uniform FPS (`30 fps` if any segment is video/visualizer; `2`/`10 fps` only if all segments are still/black), identical resolution, SAR, and pixel format across Option A segments so `concat -c:v copy` never produces VFR/PTS glitches. | Decisions / Phase 1B |
| G2 | Critical | Slice `titles.ass` cues to segment-local `[0, slotDur]` and apply `--fade-to-black` per segment so final `concat` stays pure `-c:v copy -c:a copy` without a second full-Mix re-encode. | Decisions / Phase 1A, 1B |
| G3 | Critical | Feed Layer 0 Auto Visualizer segments from `merged_audio.m4a` sliced at `-ss <T_i> -t <slotDur>` rather than raw `.mp3` files so visuals match normalized/crossfaded Mix audio. | Decisions / Phase 1B |
| G4 | Critical | Store per-Track UI media uploads inside `sessionState.tempDir` via session-scoped endpoints (`POST/GET/DELETE /api/sessions/{sessionId}/tracks/{trackId}/media`) and enforce existing `requireSameOrigin` + `localPathsAllowed` (`403` on non-loopback local paths). | Decisions / Phase 2B |
| G5 | Low-risk | Preserve `AGENTS.md` acyclic package boundaries (`internal/probe` and `internal/subtitle` and `internal/render` stay leaf packages; `internal/mixer` coordinates) and zero-CGo `go.mod` (`depguard`). | Decisions / All Waves |
| G6 | Low-risk | Record ADR `docs/adr/0004-per-track-media-segment-concat.md` and canonical glossary terms (`Mix Backdrop`, `Track Visual`, `Art Card`) in `GLOSSARY.md` during docs closure (meets 3-criteria ADR bar: hard to reverse, surprising uniform-FPS/segment-slice design, real trade-off vs. single `filter_complex`). | Phase 3 |

### Remaining (Other → Open Questions)

_(None — all 5 product/UX `Other` recommendations Q1–Q5 were confirmed by the user on Nov 25, 2026.)_

## Parallel Execution

| Wave | Phases | Parallel? | Agent count | Gate |
|------|--------|-----------|-------------|------|
| **0** | 0 | No | 1 | User approves `docs/plans/per-track-media.md` |
| **1** | 1A, 1B | **Yes** | 2 | After Wave 0 (disjoint packages: `probe`+`subtitle` vs `render`) |
| **2** | 2A, 2B | **Yes** | 2 | After Wave 1 (`mixer`+`cmd/ytmix` vs `internal/ui` backend + frontend once contract is frozen) |
| **3** | 3 | No | 1 | After Wave 2 (E2E verification, docs/ADR/glossary, `go vet`, `go test`) |

---

## Wave 0: Planning & Issue Claim <!-- serial -->

**Triggered by:** User — Nov 25, 2026 02:11:00 PM EST

### Phase 0: Plan Authoring, Grill & Issue Kickoff

Status: Complete
**Triggered by:** User — Nov 25, 2026 02:11:00 PM EST

- [x] Audit `internal/{probe,render,mixer,ui}` and `docs/specs/ytmix_technical_specification.md` for technical and UX gaps.
- [x] Resolve Round 1 grilling frontier (Q1–Q5) with the user.
- [x] Author `docs/plans/per-track-media.md` with post-write `## Grill` record and add row to `docs/plans/README.md`.
- [x] Claim `GCherry/ytmix#47` (assign `GCherry`, apply `work-started`, post kickoff comment linking plan and target branch).

#### Verification Plan

- Confirm `docs/plans/per-track-media.md` and `docs/plans/README.md` are the only modified files in `D:\CodingWorkspace\ytmix\ytmix` (`git status -sb`).
- Confirm `#47` shows assignee `GCherry`, label `work-started`, and kickoff comment.

#### Phase Summary

Wave 0 completed Nov 25, 2026: locked Engine Option A + Dual-Layer Composer + Q1–Q5, recorded `## Grill` with 6 auto-taken Critical/Low-risk guardrails, and claimed `GCherry/ytmix#47`. Awaiting user approval to begin Wave 1.

---

## Wave 1: Core Leaf Packages (`probe`, `subtitle`, `render`) <!-- parallel: up to 2 tracks -->

**Triggered by:** Agent (pending Wave 0 sign-off) — Nov 25, 2026 02:11:00 PM EST

### Phase 1A: Media Discovery, ID3 `attached_pic` & Segment-Local `.ass` Slicing (`internal/probe`, `internal/subtitle`)

Status: Complete
**Triggered by:** Subagent (`Phase 1A`) — Nov 25, 2026 02:21:13 PM EST

**Owned files:** `internal/probe/ffprobe.go`, `internal/probe/ffprobe_test.go`, `internal/subtitle/subtitles.go` (or existing file in `internal/subtitle/`), `internal/subtitle/*_test.go`.

- [x] Extend `probe.ProbeFile` / `probe.Track` to detect embedded ID3 artwork (`-show_entries streams=codec_type,disposition:width:height`) and expose `HasEmbeddedArt bool` (plus art dimensions when available) without adding non-stdlib dependencies.
- [x] Add `probe.DiscoverSidecarMedia(trackPath string)` to find matching `<track_stem>.<ext>` files (`.jpg`, `.jpeg`, `.png`, `.webp`, `.mp4`, `.mov`, `.gif`, case-insensitive, deterministic extension priority) in the Track's directory.
- [x] Add `probe.ExtractEmbeddedArt(ctx, trackPath, outPath, runner)` helper using `ffmpeg -y -i <mp3> -map 0:v:0 -frames:v 1 <out.jpg>` (or `.png`).
- [x] Add segment-local `.ass` generation in `internal/subtitle` (e.g., `GenerateSegmentASS`) that shifts a Track's cue timing into `[0, slotDur]` while preserving `--title-duration`, `--font-name`, and `--dim-titles`.
- [x] Add table-driven unit tests for sidecar discovery, ID3 `attached_pic` JSON parsing, and segment-local `.ass` timing.

#### Verification Plan

- `go vet ./internal/probe/... ./internal/subtitle/...` -> exit code `0`.
- `go test -v ./internal/probe/... ./internal/subtitle/...` -> exit code `0` (`12` top-level tests, `101` `--- PASS:` lines across `internal/probe` (`6` tests / `55` subtests) and `internal/subtitle` (`6` tests / `46` subtests)).
- Regression check: `go vet ./...` (`0`), `go test ./internal/mixer -run TestProbeTracks` (`0`), `go test ./internal/audio/... ./internal/ui/... ./internal/mcp/... ./cmd/ytmix/...` (`0`).

#### Phase Summary

Added `EmbeddedArt`, `ProbeEmbeddedArt`, `ProbeFileWithArt`, `ProbeTracksWithArt`, `ProbeTrackFilesWithArt`, `ProbeNamedTrackFilesWithArt`, `DiscoverSidecarMedia`, and `ExtractEmbeddedArt` in `internal/probe/ffprobe.go` while keeping existing `ProbeFile`/`ProbeTracks` signatures/args untouched for backward compatibility. Added `BuildSegmentASSWithOptions` and `WriteSegmentASSWithOptions` in `internal/subtitle/subtitles.go` with unit tests in `internal/probe/ffprobe_test.go` and `internal/subtitle/subtitles_test.go`.

### Phase 1B: Option A Segment Renderers, `Art Card` Filtergraph & Estimator (`internal/render`)

Status: Complete
**Triggered by:** Subagent (`Phase 1B`) — Nov 25, 2026 02:21:28 PM EST

**Owned files:** `internal/render/render.go`, `internal/render/segments.go` (new), `internal/render/estimate.go`, `internal/render/*_test.go`.

- [x] Define cohesive pure types in `internal/render`: `TrackLayerMode` (`fullscreen`, `card`, `backdrop`), `TrackTransition` (`cut`, `dip`), `Layer0Spec` (`black`, `media`, `visualizer`), and `TrackSegmentSpec` (index, start, slotDur, mediaPath, mediaKind, layerMode, segmentASSPath, fadeToBlack).
- [x] Implement uniform-FPS resolution (`ResolveSegmentedFPS`: `30` if any segment uses video or Auto Visualizer; `2`/`10` if all segments are still/black) and pure FFmpeg arg builders for:
  - `Fullscreen` static image & looping video/GIF segments (`scale`+`pad`, `setsar=1`, uniform `fps`, `bt709`, optional `dip` fade, optional segment `subtitles=`, optional `--fade-to-black`).
  - `Art Card` composite filtergraph (Layer 0 base: `black`, image/video, or audio-sliced Path D visualizer from `merged_audio.m4a` at `-ss <T_i> -t <slotDur>`, plus centered visual-only framed Track image/video `overlay`, followed by `subtitles=` and `fade=t=out` in planar `gbrp` before final `format=yuv420p`).
  - `L0 Only` segment builder.
  - Final stream-copy concat builder (`BuildSegmentConcatArgs`: `-f concat -safe 0 -i segs.txt -i merged_audio.m4a -map 0:v:0 -map 1:a:0 -c:v copy -c:a copy -movflags +faststart`).
- [x] Implement bounded-parallel `RenderSegmented` execution using `golang.org/x/sync/errgroup` with aggregated `ProgressFunc` reporting across segments + final concat substage.
- [x] Update `internal/render/estimate.go` to sum per-Track segment render estimates when segmented mode is active.
- [x] Add table-driven unit tests covering filtergraph strings, uniform FPS selection, `cut` vs `dip` boundaries, `--fade-to-black` segment clamping, and estimator sums.

#### Verification Plan

- `go list -f '{{.Imports}}' ./internal/render` -> exit code `0` (zero internal imports).
- `go vet ./internal/render/... && go test -v ./internal/render/...` -> exit code `0` (`22` top-level tests, `109` subtests passed, `0` failed, `0` skipped, including real FFmpeg smoke test `TestRenderSegmentedFFmpegSmoke` verified via `ffprobe` as `h264,video,1920,1080,bt709` + `aac,audio`).

#### Phase Summary

Created `internal/render/segments.go` and `internal/render/segments_test.go` with `TrackLayerMode`, `TrackTransition`, `Layer0Spec`, `TrackSegmentSpec`, `SegmentedRenderOptions`, `ResolveSegmentedFPS`, `ResolveSegmentedDimensions`, `BuildSegmentArgs`, `BuildSegmentConcatArgs`, and bounded-parallel `RenderSegmented`. Updated `internal/render/estimate.go` and `internal/render/estimate_test.go` with `SegmentEstimateInput` and per-segment summation in `EstimateMix`. Real FFmpeg smoke test confirmed `concat -c:v copy` produces valid 1080p BT.709 H.264 + AAC output.

---

## Wave 2: Orchestration, CLI & Dual-Layer Studio UI <!-- parallel: up to 2 tracks -->

**Triggered by:** Agent (pending Wave 1 gate) — Nov 25, 2026 02:11:00 PM EST

### Phase 2A: Mixer Orchestration, Manifest Schema & CLI Flags (`internal/mixer`, `cmd/ytmix`)

Status: Complete
**Triggered by:** Subagent (`Phase 2A`) — Nov 25, 2026 02:35:20 PM EST

**Owned files:** `internal/mixer/mixer.go`, `internal/mixer/mixer_test.go`, `cmd/ytmix/main.go`, `cmd/ytmix/*_test.go`.

- [x] Extend `mixer.Config` with `TrackMedia` (`off` | `auto` | `<manifest.json>`), `TrackMediaLayout` (`auto` | `fullscreen` | `card`), `TrackTransition` (`cut` | `dip`), and structured per-Track overrides (`[]TrackMediaOverride` for UI/manifest callers).
- [x] Implement JSON manifest parser & validation (strict path checks, valid layer modes, unknown-track error handling) plus media resolution precedence (Override/Manifest → Sidecar → Embedded ID3 extracted to `tempDir` → `L0 Only`) and `auto` aspect heuristic (`Art Card` for ID3/square `aspect <= 1.25`, `Fullscreen` for widescreen).
- [x] Wire `mixer.RunWithTracks` and `mixer.EstimateConfig` to invoke `render.RenderSegmented` when per-Track media is active while keeping `--track-media off` routed identically through Paths A–D.
- [x] Add CLI flags `--track-media`, `--track-media-layout`, and `--track-transition` in `cmd/ytmix/main.go` (preserving `--bg` / `--visualizer` mutual exclusion for Layer 0).
- [x] Add unit tests in `internal/mixer/mixer_test.go` for precedence resolution, manifest parsing, `auto` layout heuristic, and backward-compatible `off` path.

#### Verification Plan

- `go vet ./internal/mixer/... ./cmd/ytmix/...` -> exit code `0`.
- `go test -count=1 -v ./internal/mixer` -> exit code `0` (`62` `--- PASS:` lines including all 7 `TestE2EPipeline` subtests, `TestTrackMediaValidationAndPreflight`, `TestParseTrackMediaManifest`, `TestResolveTrackSegments`, `TestSegmentedEstimateAndRunWithTracks`; `0` failed, `0` skipped).
- `go test -count=1 -v ./cmd/ytmix/...` -> exit code `0` (`7` top-level tests + `7` subtests passed).
- Regression: `go vet ./...` (`0`), `go test ./internal/probe/... ./internal/subtitle/... ./internal/render/... ./internal/audio/... ./internal/mcp/... ./internal/ui/...` (`0`).

#### Phase Summary

Added `TrackMediaMode`, `TrackMediaLayout`, `TrackMediaOverride`, `ManifestLayer0`, `ManifestTrackEntry`, `TrackMediaManifest`, `ValidateTrackMedia`, `ValidateTrackMediaLayout`, `ParseTrackMediaManifest`, `ResolveTrackSegments`, and `Probe*WithArt` wrappers in `internal/mixer/mixer.go`, plus `--track-media`, `--track-media-layout`, and `--track-transition` in `cmd/ytmix/main.go`. Preserved original `.mp3` path/art metadata across `--normalize`, enforced precedence and `auto` aspect heuristic, and wired `RunWithTracks` and `EstimateConfig` to `render.RenderSegmented`.

### Phase 2B: Dual-Layer Composer Backend & Studio UI (`internal/ui`)

Status: Complete
**Triggered by:** Subagent (`Phase 2B`) — Nov 25, 2026 02:45:55 PM EST

**Owned files:** `internal/ui/jobs.go`, `internal/ui/handlers_probe.go`, `internal/ui/handlers_render.go`, `internal/ui/server.go`, `internal/ui/assets/index.html`, `internal/ui/assets/styles.css`, `internal/ui/assets/app.js`, `internal/ui/*_test.go`.

- [x] Extend `sessionState` in `internal/ui/jobs.go` to store per-Track media attachments (source, file path inside `tempDir` or allowed local path, kind, layer mode) cleaned up automatically on session eviction.
- [x] Add session-scoped endpoints in `internal/ui`:
  - `POST /api/sessions/{sessionId}/tracks/{trackId}/media` (upload image/video/GIF or bind allowed local path; enforce `requireSameOrigin`, `CanonicalBackgroundExt`, upload size cap, and `403` for local paths on non-loopback sessions).
  - `GET /api/sessions/{sessionId}/tracks/{trackId}/media` (serve preview image/video/extracted ID3 cover with `X-Content-Type-Options: nosniff`, `Cache-Control: no-store`).
  - `DELETE /api/sessions/{sessionId}/tracks/{trackId}/media` (revert Track to `L0 Only` / sidecar / ID3).
- [x] Extend `trackDTO`, `estimateRequest`, and `renderConfigRequest` to carry per-Track visual metadata, `layerMode`, `trackTransition`, and `layer0` mode (`black` | `upload` | `local` | `visualizer`).
- [x] Implement the **Dual-Layer Composer** in `internal/ui/assets/{index.html,styles.css,app.js}` matching `ytmix`'s dark/Studio Paper design system:
  - Dedicated **Layer 0 — Mix Backdrop** bar (`Black (#000)`, `Image / Video` upload/local, `Auto Visualizer` preset + theme/glow/darkness controls).
  - **Drag-and-drop Track sequence** (HTML5 drag-and-drop + `▲`/`▼` accessible buttons) updating `Start` timestamps and Crossfade live.
  - Per-Track `Fullscreen` / `Art Card` / `L0 Only` mode pills + inline `Media▾` picker (upload, auto-paired sidecar/ID3 badge, revert to `L0 Only`) + `Export Manifest` / CLI preview update.
  - Live **16:9 Stage Preview** compositing Layer 0 (black / image / video / 60fps visualizer `<canvas>`) + Layer 1 (`Fullscreen`, `Art Card`, or `L0 Only`) + `Title Overlay` / `Dim Titles`.
- [x] Add unit & security tests in `internal/ui/server_test.go` and `internal/ui/handlers_render_test.go` covering media upload/preview/delete, path-traversal/non-loopback `403` guards, and segmented render dispatch.

#### Verification Plan

- `go vet ./internal/ui/...` -> exit code `0`.
- `go test -count=1 -v ./internal/ui/...` -> exit code `0` (`23` top-level tests passed, `34` subtests passed, `0` failed; `1` pre-existing Windows symlink privilege skip in `TestAllowedRootsAndOutputLocationAPIs`).

#### Phase Summary

Extended `sessionState` in `internal/ui/jobs.go`, added session-scoped `POST/GET/DELETE /api/sessions/{sessionId}/tracks/{trackId}/media` routes in `internal/ui/server.go` and `handlers_probe.go` (preserving `403` non-loopback local-path restrictions and `nosniff`), wired `estimateRequest`/`renderConfigRequest` to `mixer.Config.TrackOverrides`/`Segmented` + `bgMode: "black"` in `handlers_render.go`, and implemented the Dual-Layer Composer UI in `internal/ui/assets/{index.html,styles.css,app.js}` with Layer 0 `Black (#000)` mode, drag-and-drop Track reordering, inline `Media▾` drawers, live 16:9 dual-layer Stage compositing, and manifest JSON export.

---

## Wave 3: End-to-End FFmpeg Verification & Domain Docs <!-- serial -->

**Triggered by:** Agent (pending Wave 2 gate) — Nov 25, 2026 02:11:00 PM EST

### Phase 3: Synthetic FFmpeg E2E Tests, Spec/Glossary/ADR & Final Checks

Status: Complete
**Triggered by:** Subagent (`Phase 3`) — Nov 25, 2026 02:55:31 PM EST

**Owned files:** `internal/mixer/e2e_test.go`, `docs/specs/ytmix_technical_specification.md`, `GLOSSARY.md`, `docs/adr/0004-per-track-media-segment-concat.md`, `README.md`.

- [x] Add synthetic `ffmpeg`/`ffprobe` E2E tests in `internal/mixer/e2e_test.go` covering:
  - Mixed-media segmented Mix (Track 1: `Fullscreen` static image, Track 2: `Art Card` over Path D `bars` Layer 0, Track 3: `L0 Only` fallback) with `--crossfade 1`, `--overlay-titles`, and `--track-transition dip`.
  - Embedded ID3 `attached_pic` auto-extraction + sidecar precedence test.
  - Verify output stream count, `1920x1080` resolution, `h264`/`aac` codecs, `bt709` color metadata, exact Mix duration, and `timestamps.txt` alignment via `ffprobe`.
- [x] Add `docs/adr/0004-per-track-media-segment-concat.md` documenting Engine Option A + uniform FPS + per-segment overlay/fade slicing.
- [x] Update `GLOSSARY.md` with canonical definitions for **Mix Backdrop (Layer 0)**, **Track Visual**, and **Art Card** (no implementation details).
- [x] Update `docs/specs/ytmix_technical_specification.md` and `README.md` with new flags, Dual-Layer Composer UI section, and E2E test entries.
- [x] Run full repository verification (`go build -o ytmix.exe ./cmd/ytmix`, `go vet ./...`, `go test ./...`) and confirm zero untracked test assets remain.
- [x] File follow-up GitHub issue on `GCherry/ytmix` for `internal/mcp/` per-Track media tool parity (per Q5 — filed as `GCherry/ytmix#48`).

#### Verification Plan

- `go test -count=1 -v ./internal/mixer -run TestE2ESegmentedPerTrackMedia` -> exit code `0` (`21.819s`; Test A `11.40s` PASS with `0.0000s` drift at `30/1` CFR; Test B `9.37s` PASS).
- `go vet ./...` -> exit code `0`.
- `go test -count=1 -v ./...` -> exit code `0` (`443` `--- PASS:` lines across all 8 packages, `0` failed, `1` pre-existing Windows symlink privilege skip in `internal/ui`).
- `go build -o ytmix.exe ./cmd/ytmix` -> exit code `0`.

#### Phase Summary

Added `TestE2ESegmentedPerTrackMedia` (Test A mixed-media dual-layer segmented Mix + Test B ID3 `attached_pic` and sidecar precedence) in `internal/mixer/e2e_test.go`, patched `internal/render/segments.go` (`fps=%d` unconditional CFR) and `internal/render/segments_test.go` to eliminate `showfreqs` VFR drift, authored `docs/adr/0004-per-track-media-segment-concat.md`, updated `GLOSSARY.md`, `docs/specs/ytmix_technical_specification.md`, and `README.md`, filed follow-up `GCherry/ytmix#48`, and verified full `go vet`, `go test -count=1 ./...`, and `go build`.

---

## Final Recap

Implemented per-Track media attachment for `GCherry/ytmix#47` on `feature/per-track-media` using **Engine Option A** (bounded-parallel uniform-FPS segment rendering + FFmpeg `concat` demuxer `-c:v copy -c:a copy`) and the **Dual-Layer Composer** UI:
- `internal/probe` & `internal/subtitle`: `EmbeddedArt` / `ProbeFileWithArt` / `Probe*WithArt`, `DiscoverSidecarMedia`, `ExtractEmbeddedArt`, and segment-local `BuildSegmentASSWithOptions` / `WriteSegmentASSWithOptions`.
- `internal/render`: `internal/render/segments.go` (`TrackLayerMode`, `TrackTransition`, `Layer0Spec`, `TrackSegmentSpec`, `ResolveSegmentedFPS`, `BuildSegmentArgs`, `BuildSegmentConcatArgs`, `RenderSegmented`) + per-segment duration estimation in `internal/render/estimate.go`.
- `internal/mixer` & `cmd/ytmix`: `--track-media`, `--track-media-layout`, `--track-transition`, JSON manifest parser, precedence & aspect-ratio layout heuristic, and segmented `RunWithTracks` / `EstimateConfig`.
- `internal/ui`: Session-scoped `POST/GET/DELETE /api/sessions/{sessionId}/tracks/{trackId}/media`, `bgMode: "black"`, drag-and-drop Track sequence, Layer 0 Backdrop selector, per-Track `Fullscreen`/`Art Card`/`L0 Only` modes, live 16:9 composite Stage, and manifest JSON export.
- Domain docs & E2E: `TestE2ESegmentedPerTrackMedia`, `ADR-0004`, `GLOSSARY.md`, technical spec, `README.md`, and follow-up MCP parity issue `GCherry/ytmix#48`.

## Deployment Plan

1. Completed post-build `Reviewer` (`code-review`, `0` blocking findings) and `Planner` `Acceptance Check` (`PASS`, `0` gaps) on `feature/per-track-media`.
2. Archived `docs/plans/per-track-media.md` → `docs/plans/archive/per-track-media.md` (and updated `docs/plans/README.md` + `docs/plans/archive/README.md`) in the final PR commit.
3. Open PR targeting `main` (`Closes #47`), watch CI checks, and pause for human merge approval per `Coordinator` Merge Bar rules.
