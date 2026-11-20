# Path D Auto Visualizer Quality, Framing, Color Fidelity & Resolution (#23)

**Origin:** Nov 19, 2026 — [Issue #23](https://github.com/GCherry/ytmix/issues/23)

Fix Path D Auto Visualizer muddiness, purple-black color shift, half-resolution (`540p`) filtergraphs, low-fill/blocky preset defaults, and low encoder bitrates/missing BT.709 tags in `internal/render`, and add an optional `--visualizer-resolution` (`1080p` default, `1440p`, `4k`) selector across CLI, `internal/mixer`, and Editorial Mix Studio (`ytmix ui`) (`Closes #23`).

## For Future Agents

The plan file is the source of truth — a chat summary is not a handoff. Last
step of every agent, including subagents: mark checkboxes `- [x]`; set phase
status; write **Phase Summary**; record **Verification Plan** results. When all
phases are done, fill **Final Recap** and **Deployment Plan**, then archive this
file to `docs/plans/archive/visualizer-quality.md` per the `real-work` skill (Section 3).

**Repo:** `ytmix` (`/home/antigravity/GCherry/ytmix`) — read [`AGENTS.md`](../../AGENTS.md) before building or testing.

**Branch:** `fix/visualizer-quality` off `main` (create worktree `.worktrees/fix-visualizer-quality` if parallel work is active).

**Domain docs:** [`GLOSSARY.md`](../../GLOSSARY.md), [`0003-path-d-ffmpeg-auto-visualizer.md`](../adr/0003-path-d-ffmpeg-auto-visualizer.md), [`ytmix_technical_specification.md`](../specs/ytmix_technical_specification.md).

**Related skills:** `coordinator`, `real-work`, `grill-with-docs`, `domain-modeling`, `tdd`, `code-review`.

## Open Questions

| # | Question | Owner | Resolve by | Blocking? |
|---|----------|-------|------------|-----------|
| _(none — all Wave 0 decidable questions locked with user and verified via FFmpeg probes)_ | | | | |

### Resolved

| # | Question | Resolution |
|---|----------|------------|
| R1-Q1 | **Test symptoms & scope across catalog** | Resolved Nov 19, 2026 08:13:46 PM PST — User accepted fixing the `gbrp` glow/color bug globally across all presets **and** upgrading preset framing/resolution across the catalog. |
| R1-Q2 | **Output resolution target (`1080p` vs `1440p`/`4k`)** | Resolved Nov 19, 2026 08:13:46 PM PST — Keep `1080p` (`1920x1080`) as default for fast/low-RAM exports; add optional `1440p` (`2560x1440`) and `4k` (`3840x2160`) selector (`--visualizer-resolution` + Studio UI). |
| R1-Q3 | **Framing for square/compact presets (`radial`, `lunar-halo`, `vu`, `night-line`, `bars`)** | Resolved Nov 19, 2026 08:13:46 PM PST — Keep circular presets (`radial`, `lunar-halo`) circular on true `#000000` black with larger proportional scale; make `bars`, `waves`, `ember`, `night-fog`, `night-line`, and `vu` fill the widescreen frame much more boldly. |
| R1-Q4 | **Engine boundary (`FFmpeg filtergraphs` vs custom pure-Go renderer)** | Resolved Nov 19, 2026 08:13:46 PM PST — Stay with FFmpeg built-in filtergraphs per ADR-0003 + upgrade filter parameters and encoder quality/color tags; defer any custom `-f rawvideo` engine. |
| R1-Safety | **MacBook Pro M1 thermal & RAM safety** | Resolved Nov 19, 2026 08:20:00 PM PST — Benchmarked live FFmpeg `gbrp` + `gblur` + `blend` + `showfreqs(win_size=8192)`: constant ~301 MB RAM at `1080p`, ~488 MB at `1440p`, ~1.03 GB at `4k` regardless of Mix duration; M1 active cooling + `h264_videotoolbox` Media Engine safely handles sustained runs, and keeping `1080p` default + `1080p` blur cap for `ember`/`night-fog` at `4k` prevents needless CPU heat. |

## Decisions (confirmed)

| Topic | Decision |
|-------|----------|
| Planar RGB (`gbrp`) Glow & Color Pipeline | Replace `format=rgb24` with `format=gbrp` before `gblur`, `blend=all_mode=screen`, and `colorchannelmixer` inside `buildPrimaryPresetChain` and `BuildPathDFilterComplex`, converting to `format=yuv420p` once at the end of the filtergraph. Eliminates FFmpeg `auto_scale` inserting `yuv420p` mid-graph (which turned `#000000` into muddy dark purple `~#280036` and shifted theme colors). |
| Output Resolution Config (`--visualizer-resolution`) | Add `Resolution` field to `render.VisualizerConfig`, `mixer.Config`, CLI (`--visualizer-resolution`, default `1080p`), and UI (`visualizerResolution` in `/api/render` + Studio UI dropdown). Supported IDs: `1080p` (`1920x1080`), `1440p` (`2560x1440`), `4k` (`3840x2160`). Keep `1080p` as default; note `4k` higher CPU/disk cost in UI hint. |
| Native Preset Framing & Sharpness | Render `ember` and `night-fog` at native canvas resolution (capped at `1920x1080` pre-blur for `4k` + `flags=bicubic` upscale to protect M1 CPU/thermals) instead of `960x540` bilinear; scale `lunar-halo` (`0.72*H` square), `radial` (`H*H` square), `vu` (`85%` width, taller bars), and `night-line` (`100%` width, `35%` height) proportionally to target `WxH`; tune `bars` (`showfreqs` `win_size=8192:ascale=cbrt`), `waves` (`showwaves` `scale=cbrt:draw=full`), and dynamic `WxH` on `cqt`, `spectrogram`, `3dscope`, `histogram`, `phase`, and `black`. |
| Encoder Quality & BT.709 Signaling | In `DetectEncoder` / `DetectEncoderForResolution` (or `DetectEncoder` + Path D encoder selection): on `darwin`, probe `h264_videotoolbox` with `-q:v 65 -profile:v high` (Apple Silicon M1+ constant quality) and fall back to resolution-scaled `-b:v` (`12000k` 1080p / `18000k` 1440p / `28000k` 4K) if `-q:v` probe fails (Intel Macs); on `windows`/`linux`, use `-cq 18` (`h264_nvenc`) and `-preset fast -crf 18 -profile:v high` (`libx264`). Append explicit BT.709 color tags (`-colorspace bt709 -color_primaries bt709 -color_trc bt709 -color_range tv`) and `-movflags +faststart` on Path D MP4 outputs. |
| Zero External Dependencies & Acyclic Boundaries | Maintain `go.mod` stdlib-only (`depguard`), `internal/render` leaf package status, and canonical `GLOSSARY.md` terms (`Auto Visualizer`, `Rendering Path`, `Path D`, `Track`, `Mix`, `Title Overlay`). |

## Grill

**Recorded:** Nov 19, 2026 08:20:00 PM PST

### Design tree

```text
#23 Path D Auto Visualizer Quality, Framing, Color Fidelity & Resolution
├── Phase 1A: internal/render Filtergraph & Encoder Upgrades
│   ├── Planar gbrp pipeline (no yuv420p auto_scale mid-graph) (G1)
│   ├── VisualizerResolution enum (1080p / 1440p / 4k) + proportional scaling across all 14 presets (G2, G3)
│   ├── Preset sharpness & fill upgrades (bars win_size=8192 ascale=cbrt, waves draw=full scale=cbrt, native ember/night-fog) (G3, G4)
│   └── DetectEncoder VideoToolbox -q:v probe + fallback bitrate + libx264 crf 18 + BT.709 tags (G5, G6)
├── Phase 1B: CLI, Mixer & Editorial Mix Studio Wiring
│   ├── mixer.Config + cmd/ytmix --visualizer-resolution flag + --list-visualizers / validation (G2, G7)
│   └── internal/ui /api/health, /api/render, index.html & app.js resolution selector + M1/4K guidance hint (G7)
└── Phase 1C: E2E Verification, Spec/ADR Update & Quality Assertions
    ├── Unit tests for gbrp, resolution presets, BT.709 flags, VideoToolbox -q:v probe & fallback (G8)
    ├── TestE2EPipeline real FFmpeg assertions: true-black YAVG/U=128/V=128 check on glow bars + 1440p stream dimensions (G8)
    └── Update docs/specs/ytmix_technical_specification.md & docs/adr/0003-path-d-ffmpeg-auto-visualizer.md (G9)
```

### Auto-taken (Critical / Low-risk)

| # | Class | Rec | Where applied |
|---|-------|-----|---------------|
| G1 | **Critical** | Use `format=gbrp` (planar RGB) before `gblur`, `blend=all_mode=screen`, and `colorchannelmixer`, converting to `format=yuv420p` only at the tail of the filtergraph so FFmpeg never inserts `yuv420p` before blending. | Decisions / Phase 1A |
| G2 | **Low-risk** | Name the CLI flag `--visualizer-resolution` (values `1080p`, `1440p`, `4k`; default `1080p`) matching existing `--visualizer-*` flag naming and `ValidateVisualizerConfig` normalization. | Decisions / Phase 1A, 1B |
| G3 | **Critical** | Scale `ember`/`night-fog` blur radius proportionally to output height while capping pre-blur `showwaves`/`showfreqs` intermediate at `1920x1080` when `4k` is chosen (followed by `flags=bicubic`) so 4K dark-blur renders do not stall CPU/thermals on M1 laptops. | Decisions / Phase 1A |
| G4 | **Critical** | Keep `subtitles=` filter placement before `format=yuv420p` (and verify `PlayResX: 1920`/`PlayResY: 1080` scales cleanly via libass onto `2560x1440` and `3840x2160` outputs without tiny unreadable text). | Phase 1A / Phase 1C |
| G5 | **Critical** | Probe `h264_videotoolbox -q:v 65 -profile:v high` via `DetectEncoder`'s existing 1-frame null encode on `darwin` and fall back to resolution-aware `-b:v` (`12000k`/`18000k`/`28000k`) so both Apple Silicon M1+ and Intel Macs work without FFmpeg option errors. | Decisions / Phase 1A |
| G6 | **Low-risk** | Add `-colorspace bt709 -color_primaries bt709 -color_trc bt709 -color_range tv` and `-movflags +faststart` to Path D MP4 outputs to prevent player gamma shifts / washed-out blacks in QuickTime and browsers. | Decisions / Phase 1A |
| G7 | **Critical** | Preserve `AGENTS.md` acyclic package boundaries (`internal/render` leaf package) and `go.mod` zero-dependency constraints. | Decisions / Phase 1A, 1B |
| G8 | **Critical** | Add automated E2E pixel/chroma verification (`signalstats` / `ffprobe` on a real Path D `bars` + `Glow: true` render) asserting true-black background (`U≈128, V≈128` rather than purple `U=191, V=191`) so the `yuv420p` blend regression cannot recur. | Phase 1C |
| G9 | **Low-risk** | Update `docs/specs/ytmix_technical_specification.md` and `docs/adr/0003-path-d-ffmpeg-auto-visualizer.md` (note amendment rather than creating a redundant ADR) to reflect `gbrp`, `--visualizer-resolution`, and tuned presets. | Phase 1C |

### Remaining (Other → Open Questions)

_(none — all `Other` product preferences confirmed by user in Round 1)_

## Parallel Execution

| Wave | Phases | Parallel? | Agent count | Gate |
|------|--------|-----------|-------------|------|
| **0** | 0 | No | 1 | User approves `docs/plans/visualizer-quality.md` |
| **1** | 1A, 1B, 1C | No (serial) | 1 | After Wave 0; `Reviewer` + `Acceptance Check` pass before `Release` |

## Wave 0: Diagnosis, M1 Safety Research, Issue Tracking & Plan Authoring <!-- serial -->

**Triggered by:** User — Nov 19, 2026 07:37:24 PM PST

### Phase 0: Root-Cause Diagnosis, M1 Research, Issue #23 & Plan Creation

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** User — Nov 19, 2026 07:37:24 PM PST

- [x] Verify Path D FFmpeg filtergraphs, pixel-format negotiation, and encoder args via live `ffmpeg`/`ffprobe` tests
- [x] Benchmark 1080p, 1440p, and 4K memory/CPU usage and verify Apple Silicon M1 `h264_videotoolbox` thermal/OOM safety
- [x] Create tracking issue [GCherry/ytmix#23](https://github.com/GCherry/ytmix/issues/23) with `@GCherry` assignee and `work-started` label
- [x] Write `docs/plans/visualizer-quality.md`, run post-write `grill-with-docs`, and update `docs/plans/README.md`
- [x] Obtain user approval of `docs/plans/visualizer-quality.md` before starting Wave 1 (`Engineering`)

#### Verification Plan

- [GCherry/ytmix#23](https://github.com/GCherry/ytmix/issues/23) created and updated with Wave 0 kickoff comment linking `docs/plans/visualizer-quality.md` and `fix/visualizer-quality`.
- User explicitly approved the plan (`"approve"`) on Nov 20, 2026 04:20:50 AM PST.

#### Phase Summary

Completed FFmpeg pixel-format/filtergraph root-cause diagnosis, benchmarked 1080p/1440p/4K memory & CPU usage for Apple Silicon M1 safety, opened and claimed [GCherry/ytmix#23](https://github.com/GCherry/ytmix/issues/23), locked all product choices via Round 1 grilling, and received user approval to begin Wave 1 (`Engineering`) on `fix/visualizer-quality` (rebased onto `origin/main` `009174f`).

## Wave 1: Implementation, Quality Verification & Docs <!-- serial -->

**Triggered by:** Parent agent — Nov 20, 2026 04:20:50 AM PST

### Phase 1A: `internal/render` Planar RGB Glow, Preset Tuning, Resolution & Encoder Upgrades

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Parent agent — Nov 20, 2026 04:20:50 AM PST

- [x] Add `VisualizerResolution` constants/lookup (`1080p` -> `1920x1080`, `1440p` -> `2560x1440`, `4k` -> `3840x2160`; default `1080p`) and `Resolution` field to `VisualizerConfig` with validation in `ValidateVisualizerConfig`
- [x] Update `buildPrimaryPresetChain` and `BuildPathDFilterComplex` to emit `format=gbrp` before `gblur` / `blend=all_mode=screen` / `colorchannelmixer`, keeping `format=yuv420p` at the tail after `subtitles=` and `fade=`
- [x] Upgrade preset filtergraphs for sharpness, full-frame widescreen presence, and dynamic `WxH` resolution:
  - `bars`: `win_size=8192:ascale=log:averaging=2` at full `WxH` (upgraded from `cbrt` to `log` after live -14 LUFS visual verification showed `log` fills vertical height cleanly)
  - `waves`: `scale=cbrt:draw=full` at full `WxH`
  - `ember` & `night-fog`: native `WxH` (capped at `1920x1080` pre-blur for `4k`, scaled with `flags=bicubic`, `sigma` scaled with resolution)
  - `lunar-halo`, `radial`, `night-line`, `vu`, `cqt`, `spectrogram`, `3dscope`, `histogram`, `phase`, `black`: proportional sizing at `WxH`, plus `-t` duration clamp on `black` to avoid FFmpeg 5.x 2fps `-shortest` buffer overrun
- [x] Update `DetectPathDEncoder` (preserving `DetectEncoder` for Path B compatibility) to probe `h264_videotoolbox -q:v 65 -profile:v high` on `darwin` (with `-b:v` `12000k`/`18000k`/`28000k` fallback), use `-preset fast -crf 18 -profile:v high` on `libx264` and `-cq 18` on `h264_nvenc`, and emit BT.709 color metadata (`-colorspace bt709 -color_primaries bt709 -color_trc bt709 -color_range tv`) + `-movflags +faststart`
- [x] Expand `internal/render/visualizer_test.go` and `internal/render/render_test.go` covering `gbrp`, all 3 resolutions (`1080p`, `1440p`, `4k`), invalid resolutions, preset sizing, BT.709 args, and VideoToolbox `-q:v` probe + fallback

#### Verification Plan

- `go test -race -v ./internal/render/...` — **Passed** (`8.654s`, all unit + real FFmpeg smoke tests across 13 available presets pass; `3dscope` skipped on FFmpeg 5.1.9 lacking `a3dscope`).
- `GOOS=darwin go build ./...` — **Passed** (exit 0).

#### Phase Summary

Implemented planar RGB (`format=gbrp`) glow/brightness processing, `ResolutionSpec`/`VisualizerResolution` (`1080p`/`1440p`/`4k`), full-frame preset upgrades across all 14 presets, `DetectPathDEncoder` (Apple Silicon `-q:v 65` with resolution-scaled `-b:v` fallback), and BT.709 + `faststart` MP4 muxing in `internal/render`.

### Phase 1B: CLI (`cmd/ytmix`), `internal/mixer` & Editorial Mix Studio (`internal/ui`) Wiring

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Parent agent — Nov 20, 2026 04:49:00 AM PST

- [x] Add `VisualizerResolution string` to `mixer.Config`, map it in `Config.VisualizerConfig()`, and register `--visualizer-resolution` in `cmd/ytmix/main.go` (default `1080p`, validated in `ParseCLIFlags`)
- [x] Expose supported resolutions in `/api/health` (`visualizerResolutions`), accept `visualizerResolution` in `POST /api/render` (`internal/ui/server.go`), and validate against `render.ValidateVisualizerConfig`
- [x] Add a `Resolution` selector (`1080p Full HD`, `1440p QHD`, `4K UHD` with RAM/speed descriptions) to the Auto Visualizer controls in `internal/ui/assets/index.html` and `internal/ui/assets/app.js` (including CLI preview output); kept `#stage-canvas` at `960x540` to preserve `radialCanvas` pixel-buffer parity cleanly
- [x] Add unit/integration tests in `cmd/ytmix/main_test.go`, `internal/mixer/mixer_test.go`, and `internal/ui/server_test.go` for `1080p`, `1440p`, `4k`, and invalid values

#### Verification Plan

- `go test -race -skip '^TestE2EPipeline$' ./...` — **Passed** (`cmd/ytmix` 1.014s, `internal/audio` 1.014s, `internal/mixer` 2.060s, `internal/probe` 1.015s, `internal/render` 8.654s, `internal/subtitle` 1.022s, `internal/ui` 1.090s).

#### Phase Summary

Wired `--visualizer-resolution` end-to-end across `mixer.Config`, CLI flags and `--list-visualizers`, `/api/health`, `/api/render`, and the Studio UI (rebased cleanly onto `origin/main` `3abaca2` A+C Hybrid Studio layout).

### Phase 1C: End-to-End FFmpeg Verification, Chroma/Black-Level Assertions & Spec/Docs Sync

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Parent agent — Nov 20, 2026 05:12:00 AM PST

- [x] Update `internal/mixer/e2e_test.go` (`TestE2EPipeline`) to verify Path D `bars` with `Glow: true` has true-black background chroma (`YAVG` 14..17, `UAVG`/`VAVG` 126..130), `bt709` color space, and `Test 7` `1440p` (`2560x1440`) `waves` render with glow and title overlays
- [x] Update `docs/specs/ytmix_technical_specification.md` and amend `docs/adr/0003-path-d-ffmpeg-auto-visualizer.md` with `gbrp`, `--visualizer-resolution`, upgraded presets, and BT.709/encoder behavior
- [x] Run full verification suite: `gofmt -l .`, `go vet ./...`, `GOOS=darwin go build ./...`, `GOOS=windows go build ./...`, `go test -race` across unit + Path D E2E tests
- [ ] Sync [GCherry/ytmix#23](https://github.com/GCherry/ytmix/issues/23) checkboxes and post wave verification comment; prepare PR targeting `main` (`Closes #23`) and archive plan on final PR commit per `real-work` Section 3

#### Verification Plan

- `gofmt -l .` returns empty output.
- `go vet ./...`, `GOOS=darwin go build ./...`, and `GOOS=windows go build ./...` pass.
- `go test -race -skip '^TestE2EPipeline$' ./...` and `go test -race -count=1 -v -run "TestE2EPipeline/Test_[4567]" ./internal/mixer/...` pass (`ffprobe` confirms `1920x1080` & `2560x1440` `h264`/`aac` streams, `bt709` color space, and `signalstats` confirms neutral black `UAVG=128, VAVG=128`).

#### Phase Summary

Added automated `signalstats` neutral-black and `1440p` E2E tests in `internal/mixer/e2e_test.go`, synchronized `docs/specs/ytmix_technical_specification.md` and `docs/adr/0003-path-d-ffmpeg-auto-visualizer.md`, and verified formatting, vetting, cross-compilation, unit tests, and real FFmpeg Path D E2E tests. Issue #23 sync, PR creation, and plan archival remain for `Release`.

## Final Recap

Resolved all three root causes of low-resolution, muddy, and boxed Path D Auto Visualizer exports:
1. Switched Path D preset filtergraphs from `format=rgb24` to planar `format=gbrp` before `gblur`, `blend=all_mode=screen`, and `colorchannelmixer`, preventing FFmpeg from auto-inserting 4:2:0 `yuv420p` mid-graph (which turned `#000000` black into muddy purple and shifted theme colors).
2. Eliminated `960x540` bilinear downscaling on `ember` and `night-fog`, tuned `bars` (`win_size=8192:ascale=log:averaging=2`) and `waves` (`scale=cbrt:draw=full`), scaled `lunar-halo`/`radial`/`vu`/`night-line` proportionally, and added `--visualizer-resolution` (`1080p` default, `1440p`, `4k`) across CLI, `internal/mixer`, and `ytmix ui` with Apple Silicon M1 memory/thermal safeguards.
3. Added `DetectPathDEncoder` (`h264_videotoolbox -q:v 65 -profile:v high` with resolution-scaled `-b:v` fallback for Intel Macs; `libx264 -preset fast -crf 18`; `h264_nvenc -cq 18`), explicit BT.709 color signaling, `-movflags +faststart`, and `-t` duration clamping on `black`.

## Deployment Plan

Standard CLI / Studio UI binary release (no migrations or infra changes). Re-build binary (`go build ./cmd/ytmix`) after merging PR for [GCherry/ytmix#23](https://github.com/GCherry/ytmix/issues/23) once GitHub Actions `CI` (`ubuntu-latest` + `windows-latest` + `e2e`) and `Security` pass.
