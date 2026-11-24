# Deepen Track Intake, Eliminate Parallel Slice Unpacking, and Parallelize Probe/Loudnorm with `errgroup`

**Origin:** Nov 24, 2026 — [Issue #11](https://github.com/GCherry/ytmix/issues/11) (`refactor(probe,audio,mixer): deepen Track intake, remove 4-slice unpacking, and parallelize probe/loudnorm with errgroup`)

Consolidate Track discovery, `.mp3` validation, `ffprobe` metadata probing, and `Display Title` resolution into `internal/probe`, fix the `RunWithTracks` blank `Display Title` fallback bug on uploaded Tracks, replace parallel primitive slices (`paths`, `fileNames`, `durations`, `titles`) with cohesive Track structs across the `mixer` → `audio` and `mixer` → `subtitle` seams, and bound-parallelize `ffprobe` and `--normalize` (`Loudness Normalization`) subprocesses with `golang.org/x/sync/errgroup`.

## For Future Agents

The plan file is the source of truth — a chat summary is not a handoff. Last
step of every agent, including subagents: mark checkboxes `- [x]`; set phase
status; write **Phase Summary**; record **Verification Plan** results. When all
phases are done, fill **Final Recap** and **Deployment Plan**, then archive this
file to `docs/plans/archive/` per the `real-work` skill (Section 3).

**Repo:** `GCherry/ytmix` (`/home/antigravity/GCherry/ytmix`) — read [AGENTS.md](../../AGENTS.md) before building or testing.

**Branch:** `refactor/track-intake-errgroup` off `main`.

**Domain docs:** [GLOSSARY.md](../../GLOSSARY.md), [ADR 0002](../adr/0002-embedded-http-ui-and-sse-telemetry.md), [ADR 0003](../adr/0003-path-d-ffmpeg-auto-visualizer.md).

**Related skills:** `architect` / `codebase-design` (Stage 2 seam check), `tdd` (red-green verification), `code-review` (Standards + Spec), `real-work` (write-back & archival).

## Open Questions

| # | Question | Owner | Resolve by | Blocking? |
|---|----------|-------|------------|-----------|
| 1 | Does CI `golangci-lint` (`v2.15.0`) pass cleanly once `.golangci.yml` `depguard` allowlists `golang.org/x/sync/errgroup`? | Engineering / Release | Phase 1C / CI | No |

### Resolved

_(None yet — all pre-build triage and architecture choices were confirmed by the user in Wave 0.)_

## Decisions (confirmed)

| Topic | Decision |
|---|---|
| Triage & workflow | Transition [Issue #11](https://github.com/GCherry/ytmix/issues/11) from `needs-triage` to `ready-for-agent`, claim with `work-started` + `@me`, post `Agent Brief` + kickoff comment, and obtain Wave 0 plan approval before `Engineering` starts. |
| Canonical `Track` & intake boundary | Move `Track`, `NamedTrackFile`, `.mp3` validation, and bounded-parallel probing (`ProbeTracks`, `ProbeTrackFiles`, `ProbeNamedTrackFiles`) into `internal/probe` (keeping `type Track = probe.Track`, `type NamedTrackFile = probe.NamedTrackFile`, and thin delegators in `internal/mixer` as needed) so `internal/audio` can take `[]probe.Track` directly without breaking acyclic package boundaries. |
| `internal/subtitle` seam | Keep `internal/subtitle` as a zero-internal-import leaf package with cohesive structs (`ChapterTrack` / `TitleCue`) so `mixer.runPipeline` avoids parallel primitive slice unpacking and slice-length mismatch checks without adding an internal import. |
| Concurrency limits (`errgroup`) | Use fail-fast `errgroup.WithContext` with `SetLimit(min(max(runtime.GOMAXPROCS(0), 4), 8))` for lightweight `ffprobe` Track probing and `SetLimit(max(1, min(runtime.GOMAXPROCS(0), 4)))` for CPU-heavy `ffmpeg` `loudnorm` + re-probe, preserving deterministic input Track ordering. |
| External dependency policy | Allowlist `golang.org/x/sync/errgroup` specifically in [`.golangci.yml`](../../.golangci.yml) `depguard` (`list-mode: strict`) and update [AGENTS.md](../../AGENTS.md), [ADR 0002](../adr/0002-embedded-http-ui-and-sse-telemetry.md), [README.md](../../README.md), and spec docs from "zero external deps" to "minimal, vetted pure-Go dependencies (`golang.org/x/sync/errgroup`, zero CGo)". |

## Grill

**Recorded:** Nov 24, 2026 11:18:37 AM UTC

### Design tree

```text
Issue #11 (Deepen Track Intake + Remove Slice Unpacking + Parallel errgroup)
├── Triage & Issue Hygiene [Confirmed: ready-for-agent + work-started + Agent Brief]
├── Track Intake & `Track` Ownership [Confirmed: consolidate in `internal/probe`, alias in `internal/mixer`]
│   ├── Delete `ProbeTrackFiles`/`probeFiles` loop duplication (G1)
│   └── Fix `RunWithTracks` blank `Display Title` fallback to use `tr.FileName` (G3)
├── Audio & Subtitle Seams [Confirmed: `[]probe.Track` for `audio`; leaf structs for `subtitle`]
│   ├── `audio.ProcessAndMerge*` & `NormalizeTracks*` take/return `[]probe.Track`
│   ├── Serialize concurrent `onProgress` callback + `atomic.Int64` counter for `-race` safety (G2)
│   └── `subtitle.ChapterTrack` / `subtitle.TitleCue` replace parallel `titles`/`durations`/`starts` slices
├── Concurrency & Limits [Confirmed: errgroup.WithContext, probe 4..8, loudnorm 1..4]
│   └── Regression tests proving order preservation, SetLimit concurrency bound, and fail-fast cancel (G5)
└── Governance & Docs [Confirmed: strict `depguard` allowlist for `golang.org/x/sync/errgroup`]
    └── Update `AGENTS.md`, `ADR 0002`, `README.md`, `ytmix_technical_specification.md`, blueprint HTML (G4)
```

### Auto-taken (Critical / Low-risk)

| # | Class | Rec | Where applied |
|---|-------|-----|---------------|
| G1 | **Critical** | Keep `type Track = probe.Track` and `type NamedTrackFile = probe.NamedTrackFile` aliases in `internal/mixer` (with thin 1-line delegators if external `cmd`/`ui` callers retain `mixer.Probe*`) while deleting `validateTrackFile`, `probeFiles`, `probeNamedFiles`, and the duplicated `ProbeTrackFiles` loop from `mixer.go`. | Phase 1A / Decisions |
| G2 | **Critical** | Guard concurrent `onProgress` callbacks in `audio.NormalizeTracksWithProgress` with an `atomic.Int64` completion counter and a mutex around invoking `onProgress` so `completed` arrives strictly sequentially (`1..total`), `normDoneAt` has zero data races under `go test -race`, and telemetry sinks (`NewCLITelemetry` / SSE) remain race-free. | Phase 1B |
| G3 | **Critical** | In `mixer.RunWithTracks`, resolve `tr.FileName = filepath.Base(tr.Path)` when empty first, then resolve blank `tr.DisplayTitle` via `probe.ResolveDisplayTitle("", tr.FileName)` (never `tr.Path`) so uploaded `ytmix ui` Tracks (`track_0001.mp3`) use their sanitized upload name. | Phase 1B |
| G4 | **Low-risk** | Update all zero-dependency policy text across [AGENTS.md](../../AGENTS.md), [ADR 0002](../adr/0002-embedded-http-ui-and-sse-telemetry.md), [README.md](../../README.md), [ytmix_technical_specification.md](../specs/ytmix_technical_specification.md), and [ytmix_architecture_pipeline_blueprint.html](../specs/ytmix_architecture_pipeline_blueprint.html), while keeping [ADR 0003](../adr/0003-path-d-ffmpeg-auto-visualizer.md)'s note that `internal/render` stays a zero-dependency leaf package intact. | Phase 1C |
| G5 | **Low-risk** | Add deterministic concurrency tests under `-race` verifying Track order preservation when later Tracks finish faster, bounded worker counts honoring `SetLimit`, and fail-fast cancellation on the first failing Track. | Phase 1A / Phase 1B |

### Remaining (Other → Open Questions)

_(None — all triage, boundary, concurrency, and governance choices were confirmed by the user in Wave 0.)_

## Wave 0: Triage, Alignment & Durable Plan <!-- serial -->

**Triggered by:** User — Nov 24, 2026 11:18:37 AM UTC

### Phase 0: Issue Triage, Claim, Grill & Plan Authoring

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Agent — Nov 24, 2026 11:18:37 AM UTC

- [x] Verify all 4 problem statements, stale line references (`mixer.go:1095-1097`), `.out-of-scope/` absence, and open PR/worktree status for [Issue #11](https://github.com/GCherry/ytmix/issues/11).
- [x] Confirm triage outcome, `internal/probe` / `internal/subtitle` package boundaries, `errgroup` concurrency limits, and `depguard`/ADR-0002 updates with the user.
- [x] Create branch `refactor/track-intake-errgroup` off `main`, author `docs/plans/track-intake-errgroup.md` with `## Grill`, and register it in [docs/plans/README.md](README.md).
- [x] Transition [Issue #11](https://github.com/GCherry/ytmix/issues/11) (`needs-triage` → `ready-for-agent` + `work-started`, assign `@me`, post `Agent Brief` and kickoff comment).

#### Verification Plan

- Plan file exists at `docs/plans/track-intake-errgroup.md` with `## Decisions (confirmed)`, `## Grill`, and `0` blocking `Open Questions` — **Passed**.
- `gh issue view 11` shows assignee `GCherry`, labels `enhancement`, `priority: soon`, `ready-for-agent`, `work-started`, `Agent Brief`, and kickoff comment linking `docs/plans/track-intake-errgroup.md` and `refactor/track-intake-errgroup` — **Passed**.

#### Phase Summary

Wave 0 triage and planning completed on Nov 24, 2026 11:18:37 AM UTC. All claims in Issue #11 were verified against `main@88c4fa2`, user design decisions were locked, Issue #11 was transitioned to `ready-for-agent` + `work-started`, and this plan was created. Awaiting user Wave 0 approval before `Engineering` starts Wave 1.

## Wave 1: Implementation, Docs Alignment & Verification <!-- serial -->

**Triggered by:** User (Wave 0 approved) — Nov 24, 2026 11:20:24 AM UTC

### Phase 1A: `depguard` / `errgroup` Enablement & `internal/probe` Track Intake Consolidation

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Agent — Nov 24, 2026 11:20:24 AM UTC

- [x] Add `golang.org/x/sync/errgroup` (`v0.19.0`) to `go.mod` / `go.sum` and update [`.golangci.yml`](../../.golangci.yml) `depguard` allowlist to permit `golang.org/x/sync/errgroup` specifically under `list-mode: strict`.
- [x] Move `Track`, `NamedTrackFile`, `.mp3` Track validation (`ValidateTrackFile`), and cohesive Track intake (`ProbeTracks`, `ProbeTrackFiles`, `ProbeNamedTrackFiles`) into [internal/probe/ffprobe.go](../../internal/probe/ffprobe.go), removing `ProbeTrackFiles`/`probeFiles` loop duplication.
- [x] Implement bounded-parallel Track probing in `internal/probe` using `errgroup.WithContext` with `SetLimit(min(max(runtime.GOMAXPROCS(0), 4), 8))`, writing each result into its pre-allocated slice index so input/sorted/shuffled Track order is preserved deterministically.
- [x] Update [internal/mixer/mixer.go](../../internal/mixer/mixer.go) to alias `Track = probe.Track` and `NamedTrackFile = probe.NamedTrackFile` and delegate cleanly to `internal/probe` (removing `validateTrackFile`, `probeFiles`, `probeNamedFiles` from `mixer.go`).
- [x] Add unit tests in [internal/probe/ffprobe_test.go](../../internal/probe/ffprobe_test.go) covering Track validation, `FileName` vs `Path` Display Title fallback, out-of-order completion preserving Track order, `SetLimit` concurrency cap, and fail-fast context cancellation.

#### Verification Plan

- `gofmt -w . && gofmt -l .` — **Passed** (`exit 0`, empty output).
- `go vet ./...` — **Passed** (`exit 0`).
- `go test -race -skip '^TestE2EPipeline$' ./internal/probe ./internal/mixer ./internal/ui ./cmd/ytmix` — **Passed** (`exit 0`: `probe` 1.103s, `mixer` 2.092s, `ui` 1.154s, `cmd/ytmix` 1.027s).

#### Phase Summary

Completed Nov 24, 2026 11:24:34 AM UTC. Added `golang.org/x/sync@v0.19.0` (compatible with Go `1.24`), updated `.golangci.yml` `depguard` allowlist, consolidated `Track`/`NamedTrackFile`/`ValidateTrackFile`/`ProbeTracks`/`ProbeTrackFiles`/`ProbeNamedTrackFiles` into `internal/probe/ffprobe.go` with bounded `errgroup` concurrency, delegated from `internal/mixer`, updated race-sensitive `mixer_test.go` stubs to use `sync.Mutex`/`atomic.Bool`, and added concurrency/order/fail-fast tests in `internal/probe/ffprobe_test.go`.

### Phase 1B: `[]probe.Track` Audio Seam, Concurrent `loudnorm`, `internal/subtitle` Leaf Seam & `RunWithTracks` Fix

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Agent — Nov 24, 2026 11:24:34 AM UTC

- [x] Fix `RunWithTracks` in [internal/mixer/mixer.go](../../internal/mixer/mixer.go) so blank `DisplayTitle` resolves via `probe.ResolveDisplayTitle("", tr.FileName)` (with `tr.FileName` defaulted from `filepath.Base(tr.Path)` when empty) instead of `tr.Path`, and add a regression test with a temp path (`track_0001.mp3`) and `FileName: "03 - Midnight Breeze.mp3"` (verified Red before fix, Green after).
- [x] Refactor [internal/audio/audio.go](../../internal/audio/audio.go) (`ValidateCrossfade`, `NormalizeTracks`, `NormalizeTracksWithProgress`, `ProcessAndMerge`, `ProcessAndMergeWithProgress`) to accept `[]probe.Track` and return `(string, []probe.Track, error)`, eliminating `(paths, fileNames, durations)` parallel slices and slice-length mismatch errors.
- [x] Parallelize `NormalizeTracksWithProgress` with `errgroup.WithContext` and `SetLimit(max(1, min(runtime.GOMAXPROCS(0), 4)))`, writing normalized `.wav` paths and re-probed durations into a cloned `[]probe.Track` by index and invoking `onProgress` sequentially under `progressMu` with a strictly monotonic `completed` counter.
- [x] Introduce cohesive leaf structs in `internal/subtitle` (`ChapterTrack` for [chapters.go](../../internal/subtitle/chapters.go) and `TitleCue` + `ChapterSchedule.Cues` for [subtitles.go](../../internal/subtitle/subtitles.go)) and update `mixer.runPipeline`, `mixer.buildEstimateContext`, and `internal/ui/handlers_probe.go` (`buildUIEstimate`) to pass cohesive structs without unpacking parallel primitive slices.
- [x] Update and expand tests in [internal/audio/audio_test.go](../../internal/audio/audio_test.go), [internal/subtitle/chapters_test.go](../../internal/subtitle/chapters_test.go), [internal/subtitle/subtitles_test.go](../../internal/subtitle/subtitles_test.go), [internal/mixer/mixer_test.go](../../internal/mixer/mixer_test.go), and stabilize SSE subscriber synchronization in [internal/ui/server_test.go](../../internal/ui/server_test.go) (`TestRenderUploadFlowSSEAndDownloads`).

#### Verification Plan

- `gofmt -w . && gofmt -l .` — **Passed** (`exit 0`, empty output).
- `go vet ./...` — **Passed** (`exit 0`).
- `go test -race -count=10 -run TestRenderUploadFlowSSEAndDownloads ./internal/ui` — **Passed** (`exit 0`, 1.083s).
- `go test -race -skip '^TestE2EPipeline$' ./...` — **Passed** (`exit 0`, all 7 packages: `cmd/ytmix` 1.025s, `audio` 1.056s, `mixer` 2.114s, `probe` 1.094s, `render` 13.500s, `subtitle` 1.043s, `ui` 1.175s).

#### Phase Summary

Completed Nov 24, 2026 11:31:06 AM UTC. Fixed the `RunWithTracks` blank `DisplayTitle` fallback bug (`tr.FileName`), refactored `internal/audio` to operate on `[]probe.Track` with bounded concurrent `loudnorm` + re-probe, replaced parallel slices in `internal/subtitle` with `ChapterTrack` and `TitleCue` leaf structs, removed all 4-slice unpacking from `mixer.runPipeline`/`buildEstimateContext`/`handlers_probe.go`, and synchronized a pre-existing goroutine scheduling race in `TestRenderUploadFlowSSEAndDownloads`.

### Phase 1C: Governance & Documentation Alignment + Full Verification

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Agent — Nov 24, 2026 11:27:00 AM UTC

- [x] Update [AGENTS.md](../../AGENTS.md), [ADR 0002](../adr/0002-embedded-http-ui-and-sse-telemetry.md), [README.md](../../README.md), [ytmix_technical_specification.md](../specs/ytmix_technical_specification.md), and [ytmix_architecture_pipeline_blueprint.html](../specs/ytmix_architecture_pipeline_blueprint.html) to document minimal vetted pure-Go dependencies (`golang.org/x/sync/errgroup`, zero CGo) and `internal/probe`'s consolidated Track intake role (preserving [ADR 0003](../adr/0003-path-d-ffmpeg-auto-visualizer.md)'s zero-dep `internal/render` invariant).
- [x] Run full verification suite: `gofmt -l .`, `go vet ./...`, `go build ./...`, `GOOS=darwin go build ./...`, `GOOS=windows go build ./...`, `go test -race -skip '^TestE2EPipeline$' ./...`, `guard.py --check-probes`, and `go test -race -count=1 -v -run TestE2EPipeline ./internal/mixer`.
- [x] Sync [Issue #11](https://github.com/GCherry/ytmix/issues/11) acceptance criteria checkboxes (`- [x]`).

#### Verification Plan

- `gofmt -l .` — **Passed** (`exit 0`, empty output).
- `go vet ./...` — **Passed** (`exit 0`).
- `go build ./...`, `GOOS=darwin go build ./...`, `GOOS=windows go build ./...` — **Passed** (`exit 0`).
- `go test -race -skip '^TestE2EPipeline$' ./...` — **Passed** (`exit 0`, all 7 packages).
- `python3 /home/antigravity/GCherry/chill-hq/bin/guard.py --check-probes --root /home/antigravity/GCherry/ytmix` — **Passed** (`exit 0`, `[PASS] Zero [DEBUG-...] probes detected.`).
- `go test -race -count=1 -v -run TestE2EPipeline ./internal/mixer` — **Passed** (`exit 0`, 134.600s, all 7 E2E subtests across Paths A, B with `--normalize`, C, and D).
- `golangci-lint` (`v2.15.0`) — **CI-deferred** (not preinstalled in local VM; verified in `.github/workflows/ci.yml` on PR push).

#### Phase Summary

Completed Nov 24, 2026 11:31:16 AM UTC. Aligned `AGENTS.md`, `ADR 0002`, `README.md`, `ytmix_technical_specification.md`, and `ytmix_architecture_pipeline_blueprint.html`, verified cross-OS builds (`linux`, `darwin`, `windows`), `-race` unit tests across all packages, probe cleanup, and the full FFmpeg `TestE2EPipeline` suite (all 7 subtests passing).

## Final Recap

- Consolidated `Track`, `NamedTrackFile`, `ValidateTrackFile`, and bounded-parallel `ProbeTracks`/`ProbeTrackFiles`/`ProbeNamedTrackFiles` into [internal/probe/ffprobe.go](../../internal/probe/ffprobe.go), removing duplicate probing helpers from [internal/mixer/mixer.go](../../internal/mixer/mixer.go).
- Fixed `RunWithTracks` blank `DisplayTitle` fallback to resolve from `tr.FileName` rather than opaque temp `tr.Path` values (`track_0001.mp3`), guarded by a regression test.
- Eliminated parallel slice unpacking (`paths`, `fileNames`, `durations`, `titles`, `starts`) across `mixer`/`ui` → `audio` (`[]probe.Track`) and `mixer`/`ui` → `subtitle` (`ChapterTrack`, `TitleCue`, `ChapterSchedule.Cues`).
- Parallelized `ffprobe` Track probing (`SetLimit(min(max(GOMAXPROCS, 4), 8))`) and `--normalize` (`Loudness Normalization` + re-probe, `SetLimit(max(1, min(GOMAXPROCS, 4)))`) with fail-fast `golang.org/x/sync/errgroup` (`v0.19.0`) while preserving deterministic Track ordering and race-free sequential progress callbacks.
- Updated `.golangci.yml` (`depguard`), `AGENTS.md`, `docs/adr/0002-embedded-http-ui-and-sse-telemetry.md`, `README.md`, and specs; all unit tests (`-race`), cross-OS builds, and all 7 FFmpeg `TestE2EPipeline` subtests passed.

## Deployment Plan

Two-way-door internal Go refactoring with zero CLI/API schema or runtime deployment changes; ready for `Reviewer` (`code-review`), `Planner` `Acceptance Check`, and `Release` (`pre-merge` PR against `main` with `Closes #11` and CI `golangci-lint` verification).

