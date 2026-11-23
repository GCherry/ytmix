# Render Duration Estimation & Live ETA Plan

**Origin:** Nov 22, 2026 — [Issue #7](https://github.com/GCherry/ytmix/issues/7) research report ([`docs/research/render-duration-estimation.md`](../research/render-duration-estimation.md)), broken into scoped follow-up issues [#26](https://github.com/GCherry/ytmix/issues/26), [#27](https://github.com/GCherry/ytmix/issues/27), and [#28](https://github.com/GCherry/ytmix/issues/28).

Deliver accurate pre-render Mix build duration estimates (using Background Visual metadata, Track lengths, Rendering Path, active encoder, and a local observed-speed calibration cache), estimate-weighted Step 2–5 progress, and live ETA countdowns across both the `ytmix` CLI (`--estimate` + TTY status) and Editorial Mix Studio (`ytmix ui`), while fixing `DetectEncoder` / `DetectPathDEncoder`'s `64x64` NVENC probe bug.

## For Future Agents

The plan file is the source of truth — a chat summary is not a handoff. Last
step of every agent, including subagents: mark checkboxes `- [x]`; set phase
status; write **Phase Summary**; record **Verification Plan** results. When all
phases are done, fill **Final Recap** and **Deployment Plan**, then archive this
file to `docs/plans/archive/render-duration-estimation.md` per the `real-work` skill (Section 3).

**Repo:** `ytmix` (`D:\CodingWorkspace\ytmix\ytmix`) — read [`AGENTS.md`](../../AGENTS.md) before building or testing.

**Branch:** `feature/render-duration-estimation` off `main` (per-issue branches/PRs optional if implementing [#26](https://github.com/GCherry/ytmix/issues/26) → [#27](https://github.com/GCherry/ytmix/issues/27) / [#28](https://github.com/GCherry/ytmix/issues/28) independently).

**Domain docs:** [`GLOSSARY.md`](../../GLOSSARY.md), [`docs/adr/0001-ffmpeg-rendering-paths-and-ass-subtitles.md`](../adr/0001-ffmpeg-rendering-paths-and-ass-subtitles.md), [`docs/adr/0002-embedded-http-ui-and-sse-telemetry.md`](../adr/0002-embedded-http-ui-and-sse-telemetry.md), [`docs/adr/0003-path-d-ffmpeg-auto-visualizer.md`](../adr/0003-path-d-ffmpeg-auto-visualizer.md), [`docs/specs/ytmix_technical_specification.md`](../specs/ytmix_technical_specification.md), [`docs/research/render-duration-estimation.md`](../research/render-duration-estimation.md).

**Related skills:** `coordinator`, `real-work`, `tdd`, `code-review`, `domain-modeling`.

## Open Questions

| # | Question | Owner | Resolve by | Blocking? |
|---|---|---|---|---|
| _(none — all Wave 0 design choices confirmed by user)_ | | | | |

### Resolved

| # | Question | Resolution |
|---|---|---|
| 1 | **Scope of Issue #7 vs. follow-up work** | Resolved Nov 22, 2026 09:18:58 PM EST — User chose research report + `real-work` plan only for #7, closing #7 and opening scoped implementation issues ([#26](https://github.com/GCherry/ytmix/issues/26), [#27](https://github.com/GCherry/ytmix/issues/27), [#28](https://github.com/GCherry/ytmix/issues/28)). |
| 2 | **Pre-render calibration strategy** | Resolved Nov 22, 2026 09:18:58 PM EST — User approved analytical path/FPS/duration formula + local calibration cache (`os.UserCacheDir()/ytmix/calibration.json`) storing EMA observed speeds (zero startup delay). |
| 3 | **CLI flags & `DetectEncoder` 64x64 NVENC fix** | Resolved Nov 22, 2026 09:18:58 PM EST — User approved pre-render readout + TTY live ETA + `--estimate` dry-run flag, and folding the `256x256` NVENC probe fix into Wave 1 ([#26](https://github.com/GCherry/ytmix/issues/26)). |
| 4 | **Unstaged `docs/adr/0002` diff in local checkout** | Resolved Nov 22, 2026 09:20:26 PM EST — Verified git log `009174f..c5d6a37`: unstaged diff was an accidental stale revert of PR #22's `POST /api/fs/pick` consequences. Discarded per user instruction and synced `main` to `c5d6a37`. |

## Decisions (confirmed)

| Topic | Decision |
|---|---|
| Zero External Go Dependencies | Keep `go.mod` standard-library-only (`depguard` compliant in `.golangci.yml`). No external modules; preserve `AGENTS.md` acyclic package boundaries (`probe` leaf, `audio -> probe`, `render` leaf, `mixer -> {probe, audio, render, subtitle}`). |
| `BackgroundMeta` Probing | `internal/probe.ProbeBackgroundVisual` extracts `DurationSec`, `FPS` (`avg_frame_rate` -> `r_frame_rate` fallback), `Width`, `Height`, `CodecName`, `PixFmt`, `NBFrames`, gracefully handling GIFs / static images with `N/A` duration. |
| NVENC Probe Fix | Update `DetectEncoder` (`internal/render/render.go:206`) and `DetectPathDEncoder` (`internal/render/render.go:266`) probe resolution from `s=64x64` to `s=256x256`. |
| Analytical Estimator + Calibration Cache | Model Step 2 (`--normalize` + `concat`/`acrossfade`) and Step 5 (Paths A, B, C, D across `1080p`/`1440p`/`4k`) with confidence range `[LowSec, HighSec]`, refined via EMA (`alpha=0.35`, `T_mix >= 10s`) in `os.UserCacheDir()/ytmix/calibration.json`. |
| Estimate-Weighted Progress & Live ETA Handoff | Replace static `5/10/15/20` + `PathANormWeight = 15/79` progress weights with estimate-proportional weights; blend pre-render remaining estimate smoothly into EMA-smoothed `-progress pipe:1` `speed` to emit `RemainingSec`. |
| CLI Interface | Print pre-render estimate after `[1/5]`, show live remaining ETA in `NewCLITelemetry` TTY `\r` updates, and support `--estimate` dry-run flag exiting `0` without writing outputs. |
| Editorial Mix Studio (`ytmix ui`) | Add `POST /api/estimate` (loopback-confined for local `--bg`, metadata-safe for uploaded BG), segmented Audio/Video cost bar + estimate pill + loop cycle indicator, live weighted telemetry ETA, and `Estimated vs Actual` completion summary. |
| Canonical Glossary Vocabulary | Use `Track`, `Display Title`, `Background Visual`, `Auto Visualizer`, `Mix`, `Crossfade`, `Loudness Normalization`, `Chapter Timestamps`, `Title Overlay`, and `Rendering Path` everywhere (`GLOSSARY.md`). |

## Grill

**Recorded:** Nov 22, 2026 09:22:00 PM EST

### Design tree

```text
Render Duration Estimation & Live ETA Design Tree
├── Probe & Encoder Foundation (Wave 1 / #26)
│   ├── probe.ProbeBackgroundVisual ──► single ffprobe JSON pass; N/A GIF fallback; leaf pkg (G1)
│   ├── DetectEncoder / DetectPathDEncoder ──► 256x256 probe fixes NVENC invalid param (Q3, G2)
│   ├── Estimator & Calibration Cache ──► analytical Step 2+5 formulas + UserCacheDir EMA JSON (Q2, G3)
│   └── Weighted Progress & Live ETA ──► dynamic Step 2-5 weights + smooth speed handoff (G4)
├── CLI Surface (Wave 2A / #27)
│   ├── --estimate dry-run ──► zero side-effects; no PrepareOutputTargets / temp dirs (G5)
│   └── Pre-render summary & TTY ETA ──► \r status line only when isTTY (Q3)
├── Editorial Mix Studio UI (Wave 2B / #28)
│   ├── POST /api/estimate + BG probe ──► requireSameOrigin + localPathsAllowed + resolveAllowedPath (G6)
│   ├── Upload BG zero-reupload UX ──► browser extracts video/image dims/duration locally or caches once (G7)
│   └── Studio v3 UI components ──► segmented bar, estimate pill, loop cycles, ETA, accuracy readout
└── Verification & E2E (Wave 3)
    └── gofmt, go vet, go test -race, TestE2EPipeline, depguard/gosec/errcheck clean (G8)
```

### Auto-taken (Critical / Low-risk)

| # | Class | Rec | Where applied |
|---|---|---|---|
| G1 | **Critical** | Preserve `AGENTS.md` acyclic package boundaries: keep `internal/probe` and `internal/render` as leaf packages; place orchestration/calibration I/O in `internal/mixer` (or pure estimator structs in `render`/`mixer` without circular imports). | Decisions / Wave 1 |
| G2 | **Critical** | Fix both `DetectEncoder` and `DetectPathDEncoder` (`internal/render/render.go:206,266`) from `s=64x64` to `s=256x256` and update mock test expectations in `render_test.go`. | Phase 1A |
| G3 | **Critical** | Enforce atomic temp-file + `os.Rename` writes and strict value clamping (`0.1 <= speed <= 5000`, `T_mix >= 10s`) on `calibration.json` so corrupt/partial writes or tiny unit-test clips never poison future estimates. | Phase 1B |
| G4 | **Low-risk** | For Path A `norm_loop`, add `MuxSec` to live `RemainingSec` until `mux` starts so the ETA countdown decreases monotonically to Mix completion instead of hitting `0s` mid-pipeline. | Phase 1B |
| G5 | **Critical** | Ensure `ytmix --estimate` runs `PreflightConfig(..., prepareOutputs=false)` so dry-run checks never create `--out-dir` directories or `.ytmix-write-check-*` temp files on disk. | Phase 2A |
| G6 | **Critical** | Enforce `requireSameOrigin`, `localPathsAllowed`, `CanonicalBackgroundExt`, and `resolveAllowedPath` on `POST /api/estimate` (and any BG probe route), keeping LAN/`--host 0.0.0.0` sessions strictly isolated from server filesystem paths (`ADR-0002`). | Phase 2B |
| G7 | **Low-risk** | Avoid re-uploading large `--bg` files on every UI toggle: extract client `<video>`/`<img>` `duration`, `videoWidth`, `videoHeight` in-browser for uploaded files and pass them in `POST /api/estimate` JSON rather than sending multipart uploads on every slider input. | Phase 2B |
| G8 | **Critical** | Verify every phase with `gofmt -l .`, `go vet ./...`, `go test -race ./...`, and `TestE2EPipeline` across Paths A–D; keep zero external Go dependencies (`go.mod` untouched). | Wave 3 |

### Remaining (Other → Open Questions)

_(none — all `Other` design choices resolved in Wave 0)_

## Parallel Execution

| Wave | Phases | Parallel? | Agent count | Gate |
|---|---|---|---|---|
| **0** | 0 | No | 1 | Research & plan merged (#7 closed; #26/#27/#28 open) |
| **1** | 1A, 1B | Serial / 1 track | 1 | Issue [#26](https://github.com/GCherry/ytmix/issues/26) complete & reviewed |
| **2** | 2A, 2B | **Yes** | 2 | After Wave 1 (`#26` merged; `#27` & `#28` touch disjoint CLI vs UI files) |
| **3** | 3 | No | 1 | After Wave 2 (`#27` + `#28` complete) |

## Wave 0: Research & Scope Lock <!-- serial -->

**Triggered by:** User — Nov 22, 2026 08:42:00 PM EST

### Phase 0: Empirical Research, Plan Authoring & Issue Splitting

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** User — Nov 22, 2026 08:42:00 PM EST

- [x] Inspect `internal/{probe,audio,render,mixer,ui}` and `cmd/ytmix/main.go` against #10, #17/#20, #24, and #25 on `main` (`c5d6a37`).
- [x] Run empirical `ffmpeg 9.0.2` / `ffprobe 9.0.2` benchmarks across Audio (`loudnorm`, `concat`, `acrossfade`) and Video Paths A, B, C, and D (`libx264` + `h264_nvenc`).
- [x] Investigate and clean accidental unstaged `docs/adr/0002` diff; sync `main` to `c5d6a37`.
- [x] Write [`docs/research/render-duration-estimation.md`](../research/render-duration-estimation.md) and `docs/plans/render-duration-estimation.md` with `## Grill`.
- [x] Create scoped follow-up issues [#26](https://github.com/GCherry/ytmix/issues/26), [#27](https://github.com/GCherry/ytmix/issues/27), and [#28](https://github.com/GCherry/ytmix/issues/28) with native `blocked-by` edges, update & close [#7](https://github.com/GCherry/ytmix/issues/7).

#### Verification Plan

- Confirm `git status` shows only research/plan doc additions on `docs/render-duration-eta-research` and zero Go code edits.
- Confirm `#26`, `#27` (blocked by `#26`), and `#28` (blocked by `#26`) exist with `ready-for-agent` labels and `#7` is closed with research summary links.

#### Phase Summary

Completed empirical FFmpeg benchmarking and codebase audit on `c5d6a37`, documented the `64x64` NVENC probe bug and full estimation/UI architecture in [`docs/research/render-duration-estimation.md`](../research/render-duration-estimation.md), locked all Wave 0 decisions with the user, and split implementation into [#26](https://github.com/GCherry/ytmix/issues/26) (Wave 1), [#27](https://github.com/GCherry/ytmix/issues/27) (Wave 2A), and [#28](https://github.com/GCherry/ytmix/issues/28) (Wave 2B).

## Wave 1: Core Probe, NVENC Fix & Estimator Foundation (`#26`) <!-- serial -->

**Triggered by:** User — Nov 22, 2026 09:36:32 PM EST

### Phase 1A: Background Visual Probing & `256x256` Encoder Probe Fix

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Issue [#26](https://github.com/GCherry/ytmix/issues/26) — Nov 22, 2026 09:36:32 PM EST

- [x] Add `BackgroundMeta` struct and `ProbeBackgroundVisual(ctx, path, runner)` in `internal/probe/ffprobe.go` extracting duration, FPS (`avg_frame_rate` / `r_frame_rate`), dimensions, codec, pixel format, and frame count with GIF/`N/A` fallback.
- [x] Add table-driven unit tests in `internal/probe/ffprobe_test.go` covering `.mp4`, VFR/GIF `N/A` duration, fractional frame rates (`30000/1001`), and static images.
- [x] Update `DetectEncoder` and `DetectPathDEncoder` (`internal/render/render.go:206,266`) from `s=64x64` to `s=256x256` and update `internal/render/render_test.go`.

### Phase 1B: Analytical Estimator, Calibration Cache & Live Weighted ETA

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Issue [#26](https://github.com/GCherry/ytmix/issues/26) — Nov 22, 2026 09:36:32 PM EST

- [x] Implement pure analytical duration estimator covering Step 2 (`--normalize`, `concat`, `acrossfade`) and Step 5 (Paths A, B, C, D across `1080p`/`1440p`/`4k`, `black` vs reactive presets, `glow` modifier) returning `LowSec`, `HighSec`, `PointSec`, stage breakdown, and loop cycles.
- [x] Implement local calibration store (`os.UserCacheDir()/ytmix/calibration.json`) with EMA updates (`T_mix >= 10s`, clamped speeds, atomic write).
- [x] Replace static Step 2–5 progress weights in `internal/mixer/mixer.go` with estimate-proportional weights (including per-track `--normalize` progress) and add `RemainingSec` / `EstimatedTotalSec` to `TelemetryEvent` via EMA-smoothed `-progress pipe:1` speed handoff.
- [x] Add unit tests in `internal/mixer/mixer_test.go` and `internal/render/render_test.go` covering all 4 paths, calibration bounds, monotonic Path A ETA, and weighted progress.

#### Verification Plan

- `gofmt -l .` -> empty output (`exit 0`).
- `go vet ./...` -> `exit 0`.
- `go build ./...` (Windows) and `GOOS=darwin go build ./...` -> `exit 0`.
- `go test -count=1 ./...` -> `exit 0` across all 7 packages (`cmd/ytmix` `0.387s`, `internal/audio` `0.371s`, `internal/mixer` `23.013s` including `TestE2EPipeline`, `internal/probe` `0.376s`, `internal/render` `4.884s`, `internal/subtitle` `0.389s`, `internal/ui` `0.578s`). Note: `-race` requires CGO on Windows and runs on Linux CI.

#### Phase Summary

Implemented `probe.ProbeBackgroundVisual` + `ParseFrameRate`, refactored `DetectEncoder` and `DetectPathDEncoder` onto a shared `256x256` `probeEncoderArgs` helper, added the zero-dep analytical estimator (`internal/render/estimate.go`) + OS-cache EMA persistence (`internal/mixer/calibration.go`), and wired `EstimateConfig`, weighted Step 2–5 progress (including per-track `--normalize`), and EMA-smoothed live `RemainingSec` ETA into `internal/mixer/mixer.go` on branch `feature/render-duration-estimator`. Addressed Round 1 and Round 2 Reviewer findings: recorded `PathA norm_loop`, separated `PathC` `2fps`/`10fps` and `PathD black` calibration keys, normalized calibration speeds against work-unit numerators (`VideoWorkSec`, `PathANormWorkSec`, `TotalAudioSec`), fixed headless (`sink == nil`) `--normalize` elapsed split, added `NaN`/`Inf` guards and `NBFrames` inference in `internal/probe`, isolated `mixer` test calibration paths via `calibrationPathFn`, and anchored `etaTracker` substage timers to `renderStart` / `norm_loop` completion (`muxStart`) so fast `<0.5s` mux and initial `-progress` startup lag are measured accurately.

## Wave 2: CLI & Editorial Mix Studio Surfaces (`#27`, `#28`) <!-- parallel: up to 2 tracks -->

**Triggered by:** Parent plan — Nov 22, 2026 09:22:00 PM EST

### Phase 2A: CLI `--estimate` Dry-Run & TTY Live ETA (`#27`)

Status: Not Started <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Issue [#27](https://github.com/GCherry/ytmix/issues/27)

- [ ] Add `Estimate` pre-render event/summary printed right after Step 1 (`[1/5]`) during `mixer.Run`.
- [ ] Update `mixer.NewCLITelemetry` TTY `\r` status line to render remaining ETA (`[5/5] Rendering Mix... 42% (2.4x · ~1m 12s left)`), keeping non-TTY stdout clean.
- [ ] Add `--estimate` flag in `cmd/ytmix/main.go` calling `PreflightConfig(..., false)`, probing Tracks + Background Visual, printing breakdown + range, and exiting `0` without writing outputs.
- [ ] Add unit tests in `cmd/ytmix/main_test.go` and `internal/mixer/mixer_test.go`.

#### Verification Plan

- `go test -race ./cmd/ytmix ./internal/mixer` passes; `ytmix --estimate` verified to leave destination directory untouched.

#### Phase Summary

_(write when phase completes)_

### Phase 2B: Editorial Mix Studio UI Breakdown, Live ETA & Accuracy Readout (`#28`)

Status: Not Started <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Issue [#28](https://github.com/GCherry/ytmix/issues/28)

- [ ] Add `POST /api/estimate` in `internal/ui/server.go` enforcing `requireSameOrigin`, `localPathsAllowed`, and `resolveAllowedPath` for local `--bg`, while accepting client-probed dimensions/duration/FPS for uploaded Background Visuals.
- [ ] Include `remainingSec` / `estimatedTotalSec` in SSE `"progress"` events and `estimatedSec` / `actualSec` in SSE `"complete"` events (`internal/ui/server.go`, `internal/ui/jobs.go`).
- [ ] Update `internal/ui/assets/{index.html,styles.css,app.js}` with the segmented Audio/Video cost bar, monospace estimate & speed pill, loop cycle indicator, live ETA telemetry countdown, and `Estimated vs Actual` completion summary (`Anodized Obsidian` / `Studio Paper`, `tabular-nums`, mobile-safe).
- [ ] Add unit/integration tests in `internal/ui/server_test.go` covering `POST /api/estimate` security boundaries and SSE ETA/completion payloads.

#### Verification Plan

- `go vet ./...` and `go test -race ./internal/ui/...` pass.
- Manual/DOM verification in `ytmix ui` confirms zero horizontal overflow on mobile viewport and zero external font/script requests.

#### Phase Summary

_(write when phase completes)_

## Wave 3: Full End-to-End Verification & Closure

**Triggered by:** Parent plan — Nov 22, 2026 09:22:00 PM EST

### Phase 3: Full Suite & Archive

Status: Not Started <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Wave 2 completion

- [ ] Run full verification (`gofmt -l .`, `go vet ./...`, `go test -race ./...`, `go build -o ytmix.exe ./cmd/ytmix`, and `TestE2EPipeline` across Paths A, B, C, and D).
- [ ] Update `Final Recap` and `Deployment Plan`, archive this plan to `docs/plans/archive/render-duration-estimation.md`, update `docs/plans/README.md` and `docs/plans/archive/README.md`, and close `#26`–`#28`.

#### Verification Plan

- All Go checks and E2E tests pass with zero temp artifact leftovers.

#### Phase Summary

_(write when phase completes)_

## Final Recap

_(write when all phases complete)_

## Deployment Plan

_(write when all phases complete)_
