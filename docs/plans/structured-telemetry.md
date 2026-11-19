# Structured Telemetry, Live `ffmpeg -progress`, and `Context`-Aware Runners

**Origin:** Nov 19, 2026 — [Issue #10](https://github.com/GCherry/ytmix/issues/10) (`refactor(mixer,ui): replace stdout progress scraping with structured telemetry and Context-aware runners`).

Eliminate `stdout` `[N/5]` regex scraping (`sseLogWriter`), standardize `context.Context`-aware subprocess runners across `probe`, `audio`, `render`, and `mixer` (with prompt cancellation during `--normalize`), and stream live `ffmpeg -progress pipe:1` telemetry (`out_time_us`, `speed`, `progress=end`) through structured pipeline events to both CLI TTY status output and Editorial Mix Studio SSE (`ytmix ui`).

## For Future Agents

The plan file is the source of truth — a chat summary is not a handoff. Last
step of every agent, including subagents: mark checkboxes `- [x]`; set phase
status; write **Phase Summary**; record **Verification Plan** results. When all
phases are done, fill **Final Recap** and **Deployment Plan**, then archive this
file to `docs/plans/archive/structured-telemetry.md` per the `real-work` skill (Section 3).

**Repo:** `ytmix` (`/home/antigravity/GCherry/ytmix`) — read [`AGENTS.md`](../../AGENTS.md) before building or testing.

**Branch:** `feature/structured-telemetry` off `main` (`Closes #10`).

**Domain docs:** [`GLOSSARY.md`](../../GLOSSARY.md), [`docs/adr/0001-ffmpeg-rendering-paths-and-ass-subtitles.md`](../adr/0001-ffmpeg-rendering-paths-and-ass-subtitles.md), [`docs/adr/0002-embedded-http-ui-and-sse-telemetry.md`](../adr/0002-embedded-http-ui-and-sse-telemetry.md), [`docs/specs/ytmix_technical_specification.md`](../specs/ytmix_technical_specification.md).

**Related skills:** `coordinator`, `real-work`, `tdd`, `code-review`, `domain-modeling`.

## Open Questions

| # | Question | Owner | Resolve by | Blocking? |
|---|----------|-------|------------|-----------|
| _(none — all Wave 0 questions resolved)_ | | | | |

### Resolved

| # | Question | Resolution |
|---|----------|------------|
| 1 | **Overall 0–100% progress allocation across Steps 1–5** | Resolved Nov 19, 2026 11:31:23 AM PST — User accepted recommendation: allocate `0–20%` to Steps 1–4 (`5%` each) and `20–99%` to Step 5 via `ffmpeg -progress pipe:1`, reaching `100%` only on completion. |
| 2 | **Path A two-stage `ffmpeg` & Step 2 progress scope** | Resolved Nov 19, 2026 11:31:23 AM PST — User accepted recommendation: reserve `20–35%` for Path A `norm_loop.mp4` pre-normalization and `35–99%` for the full-duration mux via `out_time_us` (Step 2 stays milestone-based at `10%`). |
| 3 | **Editorial Mix Studio (`ytmix ui`) SSE schema & display** | Resolved Nov 19, 2026 11:31:23 AM PST — User accepted recommendation: emit throttled SSE `"progress"` events (`~250ms` or `>= 1%` delta, not appended to unbounded log history) to update `#progress-fill` and `#telemetry-state` (`Running · 42% · 2.4x`) without flooding `#telemetry-log`. |
| 4 | **CLI terminal output during Step 5** | Resolved Nov 19, 2026 11:31:23 AM PST — User accepted recommendation: keep clean `[1/5]`–`[5/5]` lines by default and overwrite a single TTY status line (`\r`) during Step 5 with `%` and `speed=` (no progress spam on non-TTY stdout). |

## Decisions (confirmed)

| Topic | Decision |
|-------|----------|
| Zero External Go Dependencies | Keep `go.mod` standard-library-only (`depguard` compliant in `.golangci.yml`). No external modules or new interfaces beyond narrow function types (`CommandRunner`, `StreamRunner`, `TelemetrySink`). |
| `context.Context`-Aware Runners | Standardize `func(ctx context.Context, name string, args ...string) ([]byte, error)` across `probe.ProbeFile`, `audio.NormalizeTracks`, `audio.ProcessAndMerge`, `render.DetectEncoderArgs`, and `render.Render`. Remove `ctxRunner` closure adapters from `internal/mixer/mixer.go` and `internal/ui/server.go`. Check `ctx.Err()` at the start of every Track iteration in `audio.NormalizeTracks`. |
| Streaming `-progress pipe:1` Runner & Fallback | Add `StreamRunner` (`func(ctx context.Context, onLine func(string), name string, args ...string) error`) implemented by `mixer.ExecStreamRunner` (`exec.CommandContext` + `cmd.StdoutPipe()` + `bufio.Scanner`, waiting for scanner drain before `cmd.Wait()`, capturing `stderr` for errors). When `streamRunner != nil`, `render` appends `-progress pipe:1` to FFmpeg render invocations; when `streamRunner == nil`, `render` falls back cleanly to `CommandRunner` so non-streaming unit tests remain concise. |
| Step & Path Progress Mapping | Steps 1–4 report `5%`, `10%`, `15%`, `20%`. Step 5 starts at `20%` and advances smoothly through `20%–99%`: **Path B / Path C** map `out_time_us / (totalDuration * 1e6)` across `20–99%`; **Path A** probes the `Background Visual` loop duration best-effort via `ffprobe` to map `norm_loop` across `20–35%` (falling back to `25%` until `progress=end` -> `35%` when duration is unavailable/N/A, e.g. animated GIFs) and maps the full-duration mux across `35–99%`. Completion emits `100%`. |
| Structured Telemetry Sink | Replace `stdout io.Writer` on `mixer.Run` and `mixer.RunWithTracks` with `TelemetrySink` (`func(TelemetryEvent)`), emitting `EventStep` (`Step`, `TotalSteps`, `Progress`, `Message`, `Detail`), `EventProgress` (`Step`, `TotalSteps`, `Progress`, `OutTimeSec`, `TotalDurationSec`, `Speed`), and `EventDone` (`Progress: 100`, `Message`). |
| CLI TTY Adapter | Provide `mixer.NewCLITelemetry(w io.Writer, isTTY bool) TelemetrySink` used by `cmd/ytmix/main.go` (detecting TTY via `os.Stdout.Stat()` `ModeCharDevice`). Non-TTY writes only `[1/5]`–`[5/5]` and `Done!` lines; TTY additionally overwrites a single `\r[5/5] Rendering Mix... 42% (2.4x)` line (throttled `>= 250ms` or `>= 1%` delta) and clears/newlines cleanly before `Done!` or on exit. |
| UI SSE Adapter & Reconnect Behavior | Remove `sseLogWriter` and `stepLinePattern` from `internal/ui/jobs.go`. `Server.runRenderJob` wires a `TelemetrySink` that records `EventStep` in `job.events` history, throttles `EventProgress` (`>= 250ms` or `>= 1%` delta) as live SSE `"progress"` events (storing only `latestProgress` on `renderJob` for reconnect replay rather than bloating `job.events`), and updates `app.js` to animate `#progress-fill` and `#telemetry-state`. |
| Canonical Glossary Vocabulary | Use canonical `GLOSSARY.md` terms (`Track`, `Background Visual`, `Mix`, `Loudness Normalization`, `Chapter Timestamps`, `Title Overlay`, `Rendering Path`) in all UI labels, telemetry messages, and docs. |

## Grill

**Recorded:** Nov 19, 2026 11:34:00 AM PST

### Design tree

```text
Issue #10 Structured Telemetry & Context-Aware Runners Design Tree
├── Leaf & Audio Packages (internal/probe, internal/audio, internal/render)
│   ├── Acyclic runner types ──► explicit func(ctx context.Context, ...) params without cross-package imports (G1)
│   ├── Cancel responsiveness ──► ctx.Err() guard per Track in audio.NormalizeTracks (G2)
│   ├── FFmpeg -progress parser ──► pure ParseProgressLine + Path A/B/C progress mapper (Q1, Q2, G3)
│   └── Path A loop duration ──► best-effort ffprobe on --bg with N/A fallback for GIFs (G3)
├── Pipeline & CLI (internal/mixer, cmd/ytmix)
│   ├── ExecStreamRunner ──► StdoutPipe + bufio.Scanner drained before cmd.Wait() + stderr capture + #nosec G204 (G4)
│   ├── TelemetrySink seam ──► replace stdout io.Writer on mixer.Run / RunWithTracks; add NewCLITelemetry (G5)
│   └── TTY vs piped stdout ──► \r status overwrite on TTY only, newline cleanup on Done/err (Q4)
├── Studio UI & SSE (internal/ui, internal/ui/assets)
│   ├── Remove scraping ──► delete sseLogWriter + stepLinePattern; wire TelemetrySink directly (Q3)
│   ├── Bounded reconnect state ──► store latestProgress snapshot separately from job.events history (G6)
│   └── Frontend handler ──► handle SSE "progress" event on #progress-fill and #telemetry-state (Q3)
└── Verification & Static Analysis
    └── gofmt, go vet, go test -race, E2E synthetic Paths A/B/C, gosec/errcheck compliance (G4, G7)
```

### Auto-taken (Critical / Low-risk)

| # | Class | Rec | Where applied |
|---|-------|-----|---------------|
| G1 | **Critical** | Preserve `AGENTS.md` acyclic package boundaries (`probe` leaf, `audio` imports `probe` only, `render` leaf) by accepting `func(ctx context.Context, name string, args ...string) ([]byte, error)` directly in `probe`, `audio`, and `render` instead of importing `mixer`. | Decisions / Wave 1 |
| G2 | **Critical** | Guard `audio.NormalizeTracks` loop iterations with `if err := ctx.Err(); err != nil { return ..., err }` before invoking `ffmpeg` or `ffprobe` so cancelling a multi-track `--normalize` run stops immediately between Tracks. | Decisions / Phase 1A |
| G3 | **Low-risk** | Probe `Background Visual` duration best-effort via `ffprobe` in `render` only for `Path A` `norm_loop`; if `ffprobe` returns `N/A` or fails (common for animated `.gif` headers), keep `norm_loop` progress clamped cleanly (`25%` -> `35%` on `progress=end`) rather than failing the render. | Decisions / Phase 1B |
| G4 | **Critical** | In `mixer.ExecStreamRunner`, drain `cmd.StdoutPipe()` completely before calling `cmd.Wait()`, capture `stderr` in a bounded/full buffer for actionable errors, annotate `exec.CommandContext` with `#nosec G204` matching `ExecCommandRunner` (ADR-0001), and respect `.golangci.yml` `errcheck` rules. | Decisions / Phase 2 |
| G5 | **Low-risk** | Remove `stdout io.Writer` from `mixer.Run` and `mixer.RunWithTracks` in favor of `TelemetrySink` (defaulting `nil` to no-op), updating `cmd/ytmix/main.go`, `internal/ui/server.go`, `mixer_test.go`, and `e2e_test.go`. | Decisions / Phase 2 |
| G6 | **Low-risk** | Store only `latestProgress *sseEvent` on `renderJob` (alongside `job.events` milestone history) so live SSE `"progress"` updates never grow `job.events` unboundedly over a 90-minute Mix while reconnecting clients still restore the current percentage. | Decisions / Phase 3 |
| G7 | **Critical** | Verify all changes with `gofmt -l .`, `go vet ./...`, `go test -race ./...`, `go build ./...`, and `TestE2EPipeline` (real `ffmpeg`/`ffprobe` synthetic verification across Paths A, B, and C). | Wave 4 Verification Plan |

### Remaining (Other → Open Questions)

_(none — all `Other` design choices resolved in Wave 0)_

## Parallel Execution

| Wave | Phases | Parallel? | Agent count | Gate |
|------|--------|-----------|-------------|------|
| **0** | 0 | No | 1 | User approves `docs/plans/structured-telemetry.md` |
| **1** | 1A, 1B | **Yes** | 2 | After Wave 0 |
| **2** | 2 | No | 1 | After Wave 1 (1A + 1B complete) |
| **3** | 3 | No | 1 | After Wave 2 |
| **4** | 4 | No | 1 | After Wave 3 |

## Wave 0: Scope Lock & Plan Authoring <!-- serial -->

**Triggered by:** User — Nov 19, 2026 11:34:00 AM PST

### Phase 0: Design Interview, Issue Claim & Plan Creation

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** User — Nov 19, 2026 11:34:00 AM PST

- [x] Inspect `internal/probe/`, `internal/audio/`, `internal/render/`, `internal/mixer/`, `internal/ui/`, `cmd/ytmix/main.go`, `.golangci.yml`, `GLOSSARY.md`, and `docs/adr/`.
- [x] Resolve all 4 Wave 0 frontier design questions (`ask_question`) with the user.
- [x] Claim [Issue #10](https://github.com/GCherry/ytmix/issues/10) (`@me`, `work-started` + `ready-for-agent` labels, kickoff comment).
- [x] Write `docs/plans/structured-telemetry.md`, update `docs/plans/README.md`, and complete the post-write `## Grill` pass.
- [x] User approves `docs/plans/structured-telemetry.md` to unlock Wave 1 (`Ask-First Gate`).

#### Verification Plan

- Confirm `docs/plans/structured-telemetry.md` contains `## Grill`, `## Decisions (confirmed)`, `## Open Questions`, and all Wave/Phase blocks with `Triggered by:`. (`Verified`)
- Confirm zero product code files modified during Wave 0 (`git status --short` shows only `docs/plans/`). (`Verified`: `M docs/plans/README.md`, `?? docs/plans/structured-telemetry.md`)

#### Phase Summary

Locked all 4 Wave 0 design decisions (`0–20%` Steps 1–4, `20–99%` Step 5 with `20–35%` Path A `norm_loop` / `35–99%` mux, throttled SSE `"progress"` events, and TTY-only `\r` CLI progress), claimed Issue #10, and received user approval to build the plan to completion on `feature/structured-telemetry`.

## Wave 1: Context-Aware Runners & Streaming `-progress` Parser <!-- parallel: 2 tracks -->

**Triggered by:** Parent agent — Nov 19, 2026 12:04:00 PM PST

### Phase 1A: `Context`-Aware Runners & Cancellation (`internal/probe`, `internal/audio`)

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Parent agent — Nov 19, 2026 12:04:00 PM PST

- Owned files: `internal/probe/ffprobe.go`, `internal/probe/ffprobe_test.go`, `internal/audio/audio.go`, `internal/audio/audio_test.go`.
- [x] Add `ctx context.Context` (defaulting `nil` to `context.Background()`) to `probe.ProbeFile` runner parameter (`func(ctx context.Context, name string, args ...string) ([]byte, error)`).
- [x] Add `ctx context.Context` to `audio.NormalizeTracks` and `audio.ProcessAndMerge` runner parameters, forwarding `ctx` to every `ffmpeg` and `probe.ProbeFile` call.
- [x] Check `ctx.Err()` at the start of each Track iteration in `audio.NormalizeTracks` so cancelled contexts abort promptly without starting remaining Tracks.
- [x] Update `internal/probe/ffprobe_test.go` and `internal/audio/audio_test.go` for `ctx` signatures and add a table/subtest verifying `NormalizeTracks` stops immediately when `ctx` is cancelled mid-batch.

#### Verification Plan

- Run `go vet ./internal/probe/... ./internal/audio/...` and `go test -race -v ./internal/probe/... ./internal/audio/...` — expect exit code `0` and all tests green. (`PASS`: `internal/probe` 1.025s, `internal/audio` 1.019s, exit `0`)

#### Phase Summary

Standardized `ctx context.Context` runner signatures across `probe.ProbeFile`, `audio.NormalizeTracks`, and `audio.ProcessAndMerge` without violating acyclic leaf boundaries (`probe` leaf, `audio -> probe`). Added `ctx.Err()` cancellation guards before subprocess execution and verified mid-batch `NormalizeTracks` cancellation with `-race`.

### Phase 1B: `Context`-Aware Runner & `-progress pipe:1` Streaming (`internal/render`)

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Parent agent — Nov 19, 2026 12:04:00 PM PST

- Owned files: `internal/render/render.go`, `internal/render/render_test.go`.
- [x] Add `ctx context.Context` to `render.DetectEncoderArgs` and `render.Render` runner parameters.
- [x] Define pure streaming progress types (`StreamRunner`, `ProgressUpdate`, `ProgressFunc`) and `ParseProgressLine` / `ProgressTracker` helpers in `internal/render/render.go` to parse `out_time_us`, `speed`, and `progress=end`.
- [x] Implement Path A (`20–35%` `norm_loop` with best-effort `ffprobe` loop duration + `N/A` fallback; `35–99%` mux via `out_time_us / totalDuration`) and Path B/C (`20–99%` via `out_time_us / totalDuration`) progress mapping using `totalDuration`.
- [x] Update `render.Render` to accept `totalDuration float64`, optional `streamRunner StreamRunner`, and optional `onProgress ProgressFunc`; inject `-progress pipe:1` when `streamRunner != nil` and fall back cleanly to `runner` when `streamRunner == nil`.
- [x] Add unit tests in `internal/render/render_test.go` covering `-progress` key-value parsing, clamped fractions, Path A/B/C progress mapping (including GIF/`N/A` loop duration fallback), `StreamRunner` streaming, and `CommandRunner` fallback.

#### Verification Plan

- Run `go vet ./internal/render/...` and `go test -race -v ./internal/render/...` — expect exit code `0` and all tests green. (`PASS`: `internal/render` 1.025s, exit `0`)

#### Phase Summary

Updated `render.DetectEncoderArgs` and `render.Render` for `context.Context` runners, added `StreamRunner` + `ParseProgressLine` (`out_time_us`, `speed`, `progress=end`), implemented `PathANormWeight` (`20–35%` `norm_loop` + `35–99%` mux) and Path B/C (`20–99%`) fraction mapping with best-effort `ffprobe` duration + `N/A` GIF fallback, and verified with unit + `-race` tests.

## Wave 2: Structured Telemetry Pipeline & CLI Adapter (`internal/mixer`, `cmd/ytmix`) <!-- serial -->

**Triggered by:** Parent agent — Nov 19, 2026 12:17:00 PM PST

### Phase 2: `ExecStreamRunner`, `TelemetrySink`, & CLI Output

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Parent agent — Nov 19, 2026 12:17:00 PM PST

- Owned files: `internal/mixer/mixer.go`, `internal/mixer/mixer_test.go`, `internal/mixer/e2e_test.go`, `cmd/ytmix/main.go`, `cmd/ytmix/main_test.go`.
- [x] Add `StreamRunner` and `ExecStreamRunner` to `internal/mixer/mixer.go` (`cmd.StdoutPipe()` + `bufio.Scanner` drained before `cmd.Wait()`, capturing `stderr` on failure, `#nosec G204`).
- [x] Remove `ctxRunner` closure wrappers in `probeNamedFiles` and `runPipeline` by passing `ctx` and `runner` directly to `probe`, `audio`, and `render`.
- [x] Define `EventKind`, `TelemetryEvent`, `TelemetrySink`, and `NewCLITelemetry(w io.Writer, isTTY bool)` in `internal/mixer`; replace `stdout io.Writer` on `mixer.Run` and `mixer.RunWithTracks` with `TelemetrySink` (and `StreamRunner` support, keeping tests concise).
- [x] Emit structured milestones (`[1/5]` `5%`, `[2/5]` `10%`, `[3/5]` `15%`, `[4/5]` `20%`, `[5/5]` `20%`), live Step 5 `EventProgress` (`20–99%` with `OutTimeSec`/`TotalDurationSec`/`Speed`), and `EventDone` (`100%`).
- [x] Wire `NewCLITelemetry(os.Stdout, isStdoutTTY())` and `mixer.ExecStreamRunner` in `cmd/ytmix/main.go` (TTY overwrites single `\r` line during Step 5 and terminates cleanly; non-TTY prints milestone lines only).
- [x] Update `internal/mixer/mixer_test.go`, `internal/mixer/e2e_test.go`, and `cmd/ytmix/main_test.go` to assert structured `TelemetryEvent` sequence, progress monotonicity (`5 -> 10 -> 15 -> 20 -> ... -> 100`), TTY `\r` formatting vs non-TTY clean logs, and context cancellation.

#### Verification Plan

- Run `go vet ./cmd/... ./internal/mixer/...` and `go test -race -v ./cmd/... ./internal/mixer/...` — expect exit code `0`. (`internal/mixer` `go vet` + `go test -race` PASS 2.053s + `TestE2EPipeline/Test_1` real `ffmpeg -progress pipe:1` PASS 3.572s; `cmd/ytmix` compiles/runs after Phase 3 updates `internal/ui`'s `RunWithTracks` call).

#### Phase Summary

Replaced `stdout io.Writer` on `mixer.Run`/`RunWithTracks` with `TelemetrySink`, added `ExecStreamRunner` (`StdoutPipe` + `bufio.Scanner` + stderr capture), removed `ctxRunner` closures in `probeNamedFiles` and `runPipeline`, added `NewCLITelemetry` (TTY `\r` vs clean non-TTY logs), and wired `cmd/ytmix/main.go`.

## Wave 3: Editorial Mix Studio SSE & UI Progress (`internal/ui`) <!-- serial -->

**Triggered by:** Parent agent — Nov 19, 2026 12:25:00 PM PST

### Phase 3: Replace `sseLogWriter` & Stream Live Progress to Frontend

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Parent agent — Nov 19, 2026 12:25:00 PM PST

- Owned files: `internal/ui/jobs.go`, `internal/ui/server.go`, `internal/ui/server_test.go`, `internal/ui/assets/app.js`.
- [x] Remove `sseLogWriter`, `stepLinePattern`, and unused `bytes`/`regexp` imports from `internal/ui/jobs.go`.
- [x] Add `latestProgress *sseEvent` snapshot handling on `renderJob` (`emitProgress` / `subscribe`) so live `"progress"` events stream to subscribers and reconnecting clients without inflating `job.events` history.
- [x] Add `StreamRunner` to `ui.Config` (defaulting to `mixer.ExecStreamRunner` in `ui.NewServer` and `cmd/ytmix/main.go` `runUIServer`) and remove `DetectEncoderArgs` closure wrapper in `handleHealth`.
- [x] Wire a throttled (`>= 250ms` or `>= 1%` progress delta) `mixer.TelemetrySink` adapter in `Server.runRenderJob` emitting `"step"`, `"progress"`, and `"log"` SSE events.
- [x] Update `internal/ui/assets/app.js` `subscribeSSE` to handle `"progress"` events by updating `#progress-fill` and `#telemetry-state` (`Running · 42% · 2.4x`) while keeping `"step"` milestones in `#telemetry-log`.
- [x] Update `internal/ui/server_test.go` to verify `[5/5]` starts at `20%` (not `100%`), `"progress"` SSE events advance smoothly, reconnect replay preserves latest progress, and `go test -race ./internal/ui/...` passes.

#### Verification Plan

- Run `go vet ./internal/ui/...` and `go test -race -v ./internal/ui/...` — expect exit code `0`. (`PASS`: `cmd/ytmix` and `internal/ui` all tests green under `-race`, exit `0`)

#### Phase Summary

Removed `sseLogWriter`/`stepLinePattern`, added `latestProgress` reconnect snapshots on `renderJob`, wired `StreamRunner` + `TelemetrySink` in `ui.Server`, removed `handleHealth` closure wrapper, updated `app.js` to handle `"progress"` SSE events on `#progress-fill` and `#telemetry-state`, and verified SSE `[5/5]` `20%` start, live progress events, and reconnect behavior.

## Wave 4: Full Verification, Docs Check & PR Readiness <!-- serial -->

**Triggered by:** Parent agent — Nov 19, 2026 12:36:00 PM PST

### Phase 4: End-to-End Verification, Issue Sync & PR Prep

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Parent agent — Nov 19, 2026 12:36:00 PM PST

- [x] Review `docs/adr/0002-embedded-http-ui-and-sse-telemetry.md` and `README.md` to confirm telemetry descriptions match the new `"progress"` SSE and `StreamRunner` behavior (no factual mismatch; zero unnecessary doc churn).
- [x] Run `gofmt -l .`, `go vet ./...`, `go test -race -v ./...`, and `go build ./...`.
- [x] Run synthetic `ffmpeg`/`ffprobe` E2E verification (`TestE2EPipeline` across Paths A, B, and C per Spec Section 5), inspecting streams/resolution/duration/`timestamps.txt`, and verify zero leftover temp assets.
- [x] Update [Issue #10](https://github.com/GCherry/ytmix/issues/10) acceptance checkboxes (`- [x]`), prepare for **Reviewer** (`code-review`), **Planner** (`Acceptance Check`), plan archival to `docs/plans/archive/structured-telemetry.md`, and **Release** PR creation (`Closes #10`).

#### Verification Plan

- `gofmt -l .` produces empty output. (`Verified`: empty output)
- `go vet ./...`, `go test -race -count=1 -skip '^TestE2EPipeline$' ./...`, `go build ./...`, `GOOS=darwin go build ./...`, and `GOOS=windows go build ./...` exit `0`. (`Verified`: all 7 Go packages pass with `-race` and cross-OS builds exit `0`)
- `go test -count=1 -v -run TestE2EPipeline ./internal/mixer`: `Test_1` (Path A with real `ffmpeg -progress pipe:1` and `sawProgress == true`) passes (`2.43s`); on the local Debian 12 `ffmpeg 5.1.9` binary, `Test_2` (`8.667s` vs `~8.0s`) and `Test_3` (`10.100s` vs `~8.0s`) reproduce the identical pre-existing `main` `-shortest` lookahead duration delta (`task-93` baseline), while `ubuntu-latest` CI `ffmpeg` runs on the PR. Zero leftover `test_assets/` or output files.

#### Phase Summary

Verified formatting, static checks, cross-OS compilation (Linux/macOS/Windows), uncached `-race` unit test suites across all 7 packages, and real `ffmpeg -progress pipe:1` E2E streaming in Path A. Ready for independent **Reviewer** (`code-review`), **Planner** (`Acceptance Check`), plan archival, and **Release** PR handoff.

## Final Recap

- **Standardized `context.Context` runners (`probe`, `audio`, `render`, `mixer`):** Eliminated `ctxRunner` closure adapters in `internal/mixer/mixer.go` and `internal/ui/server.go`, and added per-Track `ctx.Err()` cancellation checks inside `audio.NormalizeTracks`.
- **Live `ffmpeg -progress pipe:1` streaming (`internal/render`, `internal/mixer`):** Added `StreamRunner` (`mixer.ExecStreamRunner` via `StdoutPipe` + `bufio.Scanner`) and `render.ParseProgressLine` (`out_time_us`, `speed`, `progress=end`) with `0–20%` Steps 1–4, `20–99%` Step 5 (`20–35%` Path A `norm_loop` + `35–99%` mux; `20–99%` Path B/C), and `100%` completion.
- **Structured telemetry (`mixer.TelemetrySink`, CLI TTY, & Editorial Mix Studio SSE):** Removed `sseLogWriter` and `stepLinePattern` from `internal/ui/jobs.go`, wired `mixer.NewCLITelemetry` (TTY-only `\r` status updates), added bounded `latestProgress` reconnect snapshots on `renderJob`, and updated `internal/ui/assets/app.js` to animate `#progress-fill` and `#telemetry-state` on SSE `"progress"` events.

## Deployment Plan

1. Complete independent **Reviewer** (`code-review`) and **Planner** (`Acceptance Check`) on `feature/structured-telemetry`.
2. Archive `docs/plans/structured-telemetry.md` → `docs/plans/archive/structured-telemetry.md` and update `docs/plans/README.md` + `docs/plans/archive/README.md` on the PR branch prior to merge.
3. Push `feature/structured-telemetry`, open PR targeting `main` (`Closes #10`), watch `CI` (including `golangci-lint` and `ubuntu-latest` `TestE2EPipeline`) and `Security` (`govulncheck`, `CodeQL`) workflows to green, and hand off to human maintainer for merge approval (`Risk: Medium`).
