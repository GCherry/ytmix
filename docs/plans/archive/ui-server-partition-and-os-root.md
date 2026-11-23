# Partition `internal/ui/server.go`, Adopt Go 1.24 `os.Root`, and Fix Local Output Overwrites

**Origin:** Nov 23, 2026 — [Issue #13](https://github.com/GCherry/ytmix/issues/13) (`refactor(ui): partition server.go along internal seams, adopt Go 1.24 os.Root, and fix local output overwrites`)

Refactor the 2,111-line [server.go](../../../internal/ui/server.go) monolith into cohesive package-private files inside `package ui`, bump [go.mod](../../../go.mod) to Go `1.24` to adopt stdlib `os.Root` (`os.OpenRoot`) for kernel-enforced filesystem confinement across `allowedRoots`, prevent consecutive loopback Mix renders from silently overwriting prior retained jobs' download artifacts, and fix SSE client-disconnect handling plus `>= 3600s` Track start formatting in [app.js](../../../internal/ui/assets/app.js).

## For Future Agents

The plan file is the source of truth — a chat summary is not a handoff. Last
step of every agent, including subagents: mark checkboxes `- [x]`; set phase
status; write **Phase Summary**; record **Verification Plan** results. When all
phases are done, fill **Final Recap** and **Deployment Plan**, then archive this
file to `docs/plans/archive/` per the `real-work` skill (Section 3).

**Repo:** `GCherry/ytmix` (`/home/antigravity/GCherry/ytmix`) — read [AGENTS.md](../../../AGENTS.md) before building or testing.

**Branch:** `refactor/ui-server-os-root` off `main`.

**Domain docs:** [GLOSSARY.md](../../../GLOSSARY.md), [ADR 0002](../../../docs/adr/0002-embedded-http-ui-and-sse-telemetry.md).

**Related skills:** `architect` / `codebase-design` (Stage 2 seam check), `tdd` (red-green verification), `code-review` (Standards + Spec), `real-work` (write-back & archival).

## Open Questions

| # | Question | Owner | Resolve by | Blocking? |
|---|----------|-------|------------|-----------|
| 1 | Does CI `golangci-lint` (`v2.15.0`) or `gosec` flag any `os.OpenRoot` / `root.FS()` call patterns when `go.mod` is bumped to `1.24`? | Engineering / Release | Phase 1A / CI | No |

### Resolved

_(None yet — all pre-build design choices were confirmed in Wave 0 rounds 1–2.)_

## Decisions (confirmed)

| Topic | Decision |
|---|---|
| Go minimum version | Bump [go.mod](../../../go.mod) from `go 1.22` to `go 1.24` and update `1.22+` references in [AGENTS.md](../../../AGENTS.md), [README.md](../../../README.md), [ytmix_technical_specification.md](../../specs/ytmix_technical_specification.md), and [ytmix_architecture_pipeline_blueprint.html](../../specs/ytmix_architecture_pipeline_blueprint.html) to `1.24+`. |
| `os.Root` (`os.OpenRoot`) scope | Adopt `os.Root` in `internal/ui/security.go` for all server-side file reads (`handlePreviewBG`, `handleSessionTrackAudio`, `serveJobArtifact`), directory listings (`handleFSList`), `mkdir` (`handleFSMkdir`), and uploaded part writes (`saveUploadedPart`) within `allowedRoots` / `jobDir` / `session.tempDir`, while keeping canonicalized paths for `ffmpeg`/`mixer` subprocesses and updating [ADR 0002](../../../docs/adr/0002-embedded-http-ui-and-sse-telemetry.md). |
| Local-mode consecutive overwrite protection | Retain per-job artifact snapshots in `jobDir` (an `os.OpenRoot`-confined independent file copy right before a colliding render overwrites a retained job's `outPath` / `tsPath`, avoiding shared-inode `O_TRUNC` corruption from `ffmpeg -y`) so prior jobs in the 5-job ring always serve their exact rendered files while the user's `--out` / `--timestamps` target on disk receives the latest Mix. |
| `internal/ui` file partition | Split [server.go](../../../internal/ui/server.go) (currently 2,111 lines) into cohesive package-private files in `package ui`: `server.go` (lifecycle, config, routing, embedded assets, capability cache, `handleHealth`), `handlers_probe.go`, `handlers_render.go`, `handlers_fs.go`, and `security.go` alongside existing [jobs.go](../../../internal/ui/jobs.go) and [picker.go](../../../internal/ui/picker.go). |
| SSE disconnect & `app.js` `>= 1h` Track table | Update `writeSSEEvent` to return write errors and `handleRenderEvents` to check `r.Context().Done()` and write errors during both history replay and live streaming; update `renderTracksTable` in [app.js](../../../internal/ui/assets/app.js) so Track `Start` times use `includeHours = sched.totalDuration >= 3600` consistently with Chapter Timestamps (`timestamps.txt`). |

## Grill

**Recorded:** Nov 23, 2026 10:10:17 PM UTC

### Design tree

```text
Issue #13 (UI Refactor + Go 1.24 os.Root + Overwrite + SSE + >=1h Start)
├── Go Toolchain (`go.mod` 1.22 -> 1.24) [Confirmed: bump to go 1.24 + docs]
│   └── CI `test`/`e2e` follow `go-version-file: go.mod`; `lint` & `codeql-go` use `stable`
├── `os.Root` Confinement (`internal/ui/security.go`) [Confirmed: all server-side reads/list/mkdir/uploads]
│   ├── Ephemeral per-call `os.OpenRoot` + `defer root.Close()` (G1: avoids Windows directory handle locks & FD leaks)
│   ├── Relative path + `filepath.ToSlash` `"."` normalization for `root.FS()` (G2)
│   └── Canonicalized paths preserved for `ffmpeg`/`mixer` CLI subprocesses + ADR 0002 updated (G5)
├── Consecutive Loopback Overwrite Fix [Confirmed: per-job snapshot in `jobDir` before colliding overwrite]
│   └── Independent `os.OpenRoot`-confined copy under `jobStore.mu` before overwriting (G3)
├── File Partition (`package ui` only, no sub-packages per AGENTS.md) [Confirmed: 5-file split + jobs.go/picker.go]
│   └── Preserve all exported APIs, HTTP routes/status codes, and `gosec`/`depguard`/CodeQL cleanliness (G4)
└── SSE & `app.js` Fixes
    └── Context/write-error early exit in `handleRenderEvents` + `includeHours` in `renderTracksTable` + unit tests (G6)
```

### Auto-taken (Critical / Low-risk)

| # | Class | Rec | Where applied |
|---|-------|-----|---------------|
| G1 | **Critical** | Open `os.OpenRoot(root)` per operation with `defer root.Close()` inside `security.go` helpers rather than caching long-lived `*os.Root` handles in `Server`, preventing FD leaks across dynamic `allowedRoots` and Windows directory locks. | Decisions / Phase 1A |
| G2 | **Critical** | Normalize `filepath.Rel(root, target)` (`""` -> `"."`, `filepath.ToSlash` for `fs.ReadDir(root.FS(), ...)`) and ensure non-existent output paths still resolve safely via deepest-existing-ancestor canonicalization + `os.Root` parent verification before `PrepareOutputTargets`. | Phase 1A |
| G3 | **Critical** | Snapshot prior retained jobs' `outPath` / `tsPath` into their `jobDir` via an `os.OpenRoot`-confined independent file copy atomically under `jobStore.mu` / `job.mu` right before a new local render claims/overwrites the same path, and serve `jobDir`-confined artifacts via `os.OpenRoot(job.dir)`. | Phase 1B |
| G4 | **Critical** | Keep all new files strictly in `package ui` (honoring [AGENTS.md](../../../AGENTS.md) anti-bloat rules and `depguard` zero-dependency constraints), preserving every public symbol (`Config`, `Server`, `NewServer`, `Handler`, `HTTPServer`, `Close`, `PickPathFunc`) and HTTP status contract. | Phase 1A / Phase 1B |
| G5 | **Low-risk** | Update [ADR 0002](../../../docs/adr/0002-embedded-http-ui-and-sse-telemetry.md) and `Go 1.22+` references in [AGENTS.md](../../../AGENTS.md), [README.md](../../../README.md), [ytmix_technical_specification.md](../../specs/ytmix_technical_specification.md), and [ytmix_architecture_pipeline_blueprint.html](../../specs/ytmix_architecture_pipeline_blueprint.html). | Phase 1C |
| G6 | **Low-risk** | Add deterministic regression tests for `os.Root` confinement (including symlink escape & `mkdir`), consecutive local render artifact preservation, `handleRenderEvents` disconnect/write-error termination, and `app.js` `renderTracksTable` `includeHours` consistency. | Phase 1A / Phase 1B |

### Remaining (Other → Open Questions)

_(None — all product and structure trade-offs were answered by the user in Wave 0 Rounds 1–2.)_

## Wave 0: Planning & Alignment <!-- serial -->

**Triggered by:** User — Nov 23, 2026 10:10:17 PM UTC

### Phase 0: Issue Claim, Grill & Durable Plan

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Agent — Nov 23, 2026 10:10:17 PM UTC

- [x] Verify [internal/ui/server.go](../../../internal/ui/server.go) (2,111 lines), [internal/ui/jobs.go](../../../internal/ui/jobs.go), [internal/ui/assets/app.js](../../../internal/ui/assets/app.js), [go.mod](../../../go.mod), and [ADR 0002](../../../docs/adr/0002-embedded-http-ui-and-sse-telemetry.md) on `main`.
- [x] Confirm Go `1.24` bump, `os.Root` scope, consecutive overwrite strategy, and `internal/ui` file partition with the user.
- [x] Write `docs/plans/ui-server-partition-and-os-root.md` with post-write `## Grill` section and register it in [docs/plans/README.md](../README.md).
- [x] Claim [Issue #13](https://github.com/GCherry/ytmix/issues/13) (`@me`, `work-started` + `ready-for-agent`, remove `needs-triage`, post kickoff comment).

#### Verification Plan

- Plan file exists at `docs/plans/ui-server-partition-and-os-root.md` with `## Decisions (confirmed)`, `## Grill`, and `0` blocking `Open Questions`.
- `gh issue view 13` shows assignee `GCherry`, `work-started` label, and kickoff comment linking this plan and branch `refactor/ui-server-os-root`.

#### Phase Summary

Wave 0 planning completed after two rounds of user alignment on Go `1.24`, `os.Root` adoption scope, per-job `jobDir` snapshot isolation for consecutive loopback renders, and the 5-file package-private split of `internal/ui/server.go`. Awaiting user approval of Wave 0 before `Engineering` starts Wave 1.

## Wave 1: Implementation & Documentation Alignment <!-- serial -->

**Triggered by:** User (Wave 0 approved) — Nov 23, 2026 10:12:55 PM UTC

### Phase 1A: Go 1.24 Upgrade, `os.Root` Security Module & `internal/ui` File Partition

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Agent — Nov 23, 2026 10:12:55 PM UTC

- [x] Bump [go.mod](../../../go.mod) from `go 1.22` to `go 1.24`.
- [x] Create `internal/ui/security.go` implementing `os.Root` (`os.OpenRoot`) confined operations (`openAllowedFile`, `openFileInDir`, `readAllowedDir`, `mkdirAllowedDir`, `saveUploadedPartInDir`, plus `resolveAllowedPath`, `resolveRenderOutputTarget`, `requireSameOrigin`, `localPathsAllowed`, and filename sanitizers).
- [x] Partition [internal/ui/server.go](../../../internal/ui/server.go) cleanly into `server.go` (lifecycle, `Config`, `Server`, routing, embedded assets, encoder/filter caches, `handleHealth`), `handlers_probe.go` (`/api/probe`, `/api/estimate`, `/api/sessions/{sessionId}/tracks/{trackId}/audio`, `/api/preview-bg`), `handlers_render.go` (`/api/render`, `runRenderJob`, `/api/render/{id}/events`, `/api/render/{id}/output`, `/api/render/{id}/timestamps`), and `handlers_fs.go` (`/api/fs/list`, `/api/fs/pick`, `/api/fs/mkdir`) with zero public API changes.
- [x] Add focused unit tests in `internal/ui/security_test.go` covering `os.Root` traversal/symlink rejection, `allowedRoots` boundaries, `mkdirAllowedDir`, and `resolveRenderOutputTarget`.

#### Verification Plan

- `gofmt -l .` returns empty output — **Passed** (`exit 0`).
- `go vet ./...` exits `0` — **Passed** (`exit 0`).
- `go test -race ./internal/ui/...` exits `0` — **Passed** (`ok github.com/GCherry/ytmix/internal/ui 1.131s`).
- `GOOS=darwin go build ./...` and `GOOS=windows go build ./...` exit `0` — **Passed** (`exit 0`).

#### Phase Summary

Bumped [go.mod](../../../go.mod) to `go 1.24`, extracted [internal/ui/security.go](../../../internal/ui/security.go) using ephemeral `os.OpenRoot` handles (`openAllowedFile`, `openFileInDir`, `readAllowedDir`, `mkdirAllowedDir`, `saveUploadedPartInDir`), partitioned the 2,111-line [internal/ui/server.go](../../../internal/ui/server.go) into `server.go` (388 lines), `handlers_probe.go` (574 lines), `handlers_render.go` (600 lines), `handlers_fs.go` (253 lines), and `security.go` (482 lines), and added [internal/ui/security_test.go](../../../internal/ui/security_test.go).

### Phase 1B: Consecutive Output Isolation, SSE Disconnect Fix & `app.js` `>= 1h` Formatting

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Agent — Nov 23, 2026 10:18:00 PM UTC

- [x] Update [internal/ui/jobs.go](../../../internal/ui/jobs.go) and [internal/ui/handlers_render.go](../../../internal/ui/handlers_render.go) so before a new loopback render overwrites an `outPath` or `tsPath` referenced by a prior retained job in `jobStore`, the prior job's existing artifact is snapshotted via an `os.OpenRoot`-confined independent file copy into its `jobDir` (avoiding shared-inode `O_TRUNC` corruption from `ffmpeg -y` / `os.WriteFile`) and `job.outPath` / `job.tsPath` update under lock; serve artifacts through `os.Root` (`os.OpenRoot`) cleanly.
- [x] Update `writeSSEEvent` to return `error` and `handleRenderEvents` to check `r.Context().Done()` and write errors during both history replay and live streaming, exiting cleanly without hanging or panicking.
- [x] Update `renderTracksTable` in [internal/ui/assets/app.js](../../../internal/ui/assets/app.js) so individual Track `Start` cells format with `includeHours = sched.totalDuration >= 3600`, matching `computeSchedule()` and `subtitle.BuildChapterSchedule`.
- [x] Add regression tests in [internal/ui/handlers_render_test.go](../../../internal/ui/handlers_render_test.go) for consecutive loopback renders preserving earlier job downloads, `copyFileToDir` via `os.Root`, `handleRenderEvents` early abort on canceled context / failing `http.ResponseWriter` during replay & live stream, and `app.js` `includeHours` behavior.

#### Verification Plan

- `go test -race -count=1 -skip '^TestE2EPipeline$' ./...` — **Passed** (`exit 0`; all 7 packages `cmd/ytmix`, `internal/audio`, `internal/mixer`, `internal/probe`, `internal/render`, `internal/subtitle`, `internal/ui` passed at Nov 23, 2026 10:19:53 PM UTC).
- `node --check internal/ui/assets/app.js` — **Passed** (`exit 0`).
- `go test -race -count=1 -run TestE2EPipeline ./internal/mixer` — **CI-deferred** (`ffmpeg` / `ffprobe` not installed in local sandbox VM; runs in GitHub Actions `e2e` job on PR).

#### Phase Summary

Implemented `preserveCollidingArtifacts`, `snapshotArtifactToDir`, and `copyFileToDir` (via `os.OpenRoot`) in [internal/ui/jobs.go](../../../internal/ui/jobs.go) and wired them into `handleRender` / `runRenderJob` in [internal/ui/handlers_render.go](../../../internal/ui/handlers_render.go) (TDD caught that `os.Link` would share the same inode truncated by `ffmpeg -y`, so independent copy snapshots are used only on actual target collisions). Updated `writeSSEEvent` and `handleRenderEvents` to abort immediately on `r.Context().Done()` or write errors during both replay and live streaming, updated `renderTracksTable` in [internal/ui/assets/app.js](../../../internal/ui/assets/app.js) to use `includeHours = sched.totalDuration >= 3600`, and verified all unit/integration tests in [internal/ui/handlers_render_test.go](../../../internal/ui/handlers_render_test.go).

### Phase 1C: Docs & ADR 0002 Alignment + Full Verification Suite

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Agent — Nov 23, 2026 10:19:00 PM UTC

- [x] Update [ADR 0002](../../../docs/adr/0002-embedded-http-ui-and-sse-telemetry.md) to document Go 1.24 `os.Root` confinement, per-job artifact snapshots, and the partitioned `internal/ui` file layout.
- [x] Update `Go 1.22+` references to `Go 1.24+` in [AGENTS.md](../../../AGENTS.md), [README.md](../../../README.md), [ytmix_technical_specification.md](../../specs/ytmix_technical_specification.md), and [ytmix_architecture_pipeline_blueprint.html](../../specs/ytmix_architecture_pipeline_blueprint.html).
- [x] Run full verification suite (`node --check internal/ui/assets/app.js`, `test -z "$(gofmt -l .)"`, `go vet ./...`, `go build ./...`, `GOOS=darwin go build ./...`, `GOOS=windows go build ./...`, `go test -race -count=1 -skip '^TestE2EPipeline$' ./...`; `golangci-lint` and `TestE2EPipeline` explicitly deferred to GitHub Actions CI where `golangci-lint` v2.15.0 and `ffmpeg` are installed).
- [x] Sync first 5 implemented/verified acceptance checkboxes (`- [x]`) in [Issue #13](https://github.com/GCherry/ytmix/issues/13) (keeping the `golangci-lint` + full CI suite checkbox pending until PR CI completes) and fill `## Final Recap` + `## Deployment Plan`.

#### Verification Plan

- `node --check internal/ui/assets/app.js && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && GOOS=darwin go build ./... && GOOS=windows go build ./... && go test -race -count=1 -skip '^TestE2EPipeline$' ./...` — **Passed** (`exit 0`, 7/7 packages `ok` at Nov 23, 2026 10:19:53 PM UTC).
- `rg -n "\[DEBUG-" .` — **Passed** (`0` matches).
- `golangci-lint` v2.15.0 & `TestE2EPipeline` (`ffmpeg`) — **CI-deferred** (executed by `.github/workflows/ci.yml` and `.github/workflows/security.yml` on PR).

#### Phase Summary

Updated [ADR 0002](../../../docs/adr/0002-embedded-http-ui-and-sse-telemetry.md) and bumped `Go 1.22+` documentation references to `Go 1.24+` across [AGENTS.md](../../../AGENTS.md), [README.md](../../../README.md), [ytmix_technical_specification.md](../../specs/ytmix_technical_specification.md), and [ytmix_architecture_pipeline_blueprint.html](../../specs/ytmix_architecture_pipeline_blueprint.html), verified clean cross-platform builds (`linux`, `darwin`, `windows`), `gofmt`, `go vet`, `node --check`, and `go test -race`, and synced [Issue #13](https://github.com/GCherry/ytmix/issues/13).

## Final Recap

- **Go 1.24 & `os.Root` Confinement:** Upgraded [go.mod](../../../go.mod) to `go 1.24` (and doc references to `1.24+`), implemented ephemeral per-operation `os.OpenRoot` helpers in [internal/ui/security.go](../../../internal/ui/security.go) (`openAllowedFile`, `openFileInDir`, `readAllowedDir`, `mkdirAllowedDir`, `saveUploadedPartInDir`) paired with `resolveAllowedPath` and documented in [ADR 0002](../../../docs/adr/0002-embedded-http-ui-and-sse-telemetry.md), backed by [internal/ui/security_test.go](../../../internal/ui/security_test.go).
- **`internal/ui` Cohesion Split:** Partitioned the 2,111-line [internal/ui/server.go](../../../internal/ui/server.go) into [server.go](../../../internal/ui/server.go), [handlers_probe.go](../../../internal/ui/handlers_probe.go), [handlers_render.go](../../../internal/ui/handlers_render.go), [handlers_fs.go](../../../internal/ui/handlers_fs.go), and [security.go](../../../internal/ui/security.go) alongside [jobs.go](../../../internal/ui/jobs.go) and [picker.go](../../../internal/ui/picker.go) with zero exported API changes.
- **Consecutive Local Render Isolation, SSE & UI Fixes:** Preserved prior retained jobs' colliding `outPath` / `tsPath` artifacts via `os.OpenRoot`-confined independent copies in [jobs.go](../../../internal/ui/jobs.go), added write-error and `r.Context().Done()` early exits in `handleRenderEvents` / `writeSSEEvent`, fixed `>= 3600s` Track `Start` formatting in [app.js](../../../internal/ui/assets/app.js), and added regression coverage in [internal/ui/handlers_render_test.go](../../../internal/ui/handlers_render_test.go).

## Deployment Plan

1. Complete independent `Reviewer` (`code-review` Standards + Spec) and `Planner` `Acceptance Check` on branch `refactor/ui-server-os-root`.
2. Open PR targeting `main` with `Closes #13` and `## Code Review Summary`, archive this plan to `docs/plans/archive/ui-server-partition-and-os-root.md` (updating `docs/plans/README.md` and `docs/plans/archive/README.md`), and watch `.github/workflows/ci.yml` (`golangci-lint`, Linux/Windows `test`, `e2e`) and `.github/workflows/security.yml` (`govulncheck`, `CodeQL`).
3. Tick the final CI checkbox on Issue #13 once CI is green and await human merge approval (`Risk: High`, protected `main` branch).
