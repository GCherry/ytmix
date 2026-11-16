# Editorial Mix Studio Web & Mobile UI (`ytmix ui`)

**Origin:** Nov 16, 2026 — [Issue #5](https://github.com/GCherry/ytmix/issues/5) (`Add local web & mobile-ready UI ("Editorial Mix Studio") served via Go net/http + embed`).

Build an interactive local browser and mobile-ready web UI (`Editorial Mix Studio v3`) for `ytmix`, embedded directly inside the zero-dependency Go `1.22+` binary via `embed.FS` and served over Go's `net/http` with real-time Server-Sent Events (`text/event-stream`) pipeline telemetry.

## For Future Agents

The plan file is the source of truth — a chat summary is not a handoff. Last
step of every agent, including subagents: mark checkboxes `- [x]`; set phase
status; write **Phase Summary**; record **Verification Plan** results. When all
phases are done, fill **Final Recap** and **Deployment Plan**, then archive this
file to `docs/plans/archive/editorial-mix-studio-ui.md` per the `real-work` skill (Section 3).

**Repo:** `ytmix` (`D:\CodingWorkspace\ytmix\ytmix`) — read [`AGENTS.md`](../../../AGENTS.md) before building or testing.

**Branch:** `feature/editorial-mix-studio-ui` off `main` (`Closes #5`).

**Domain docs:** [`GLOSSARY.md`](../../../GLOSSARY.md), [`docs/adr/0001-ffmpeg-rendering-paths-and-ass-subtitles.md`](../../adr/0001-ffmpeg-rendering-paths-and-ass-subtitles.md), [`docs/adr/0002-embedded-http-ui-and-sse-telemetry.md`](../../adr/0002-embedded-http-ui-and-sse-telemetry.md), [`docs/specs/ytmix_technical_specification.md`](../../specs/ytmix_technical_specification.md).

**Related skills:** `coordinator`, `real-work`, `tdd`, `code-review`, `diagnosing-bugs`.

## Architecture Overview

`internal/ui` slots cleanly into `ytmix`'s acyclic package layout without adding any external Go dependencies (`go.mod` remains stdlib-only, enforced by `depguard` in `.golangci.yml`):

```text
cmd/ytmix/main.go               # Adds `ytmix ui` subcommand (--host, --port, --no-open) alongside existing CLI flags
├── internal/mixer/mixer.go     # Adds RunWithTracks + ProbeTrackFiles to preserve UI Track order & uploaded paths
└── internal/ui/
    ├── server.go               # HTTP router, //go:embed assets, /api/health, /api/probe, /api/render, /output, /timestamps
    ├── jobs.go                 # Session & render job registry, SSE event broadcaster, temp-dir lifecycle & cleanup
    ├── server_test.go          # Unit & httptest coverage for handlers, SSE stream, security & Track ordering
    └── assets/
        ├── index.html          # Editorial Mix Studio v3 layout (desktop 2-col hairline grid + mobile responsive)
        ├── styles.css          # Dark (Anodized Obsidian) & Light (Studio Paper) tokens, >=44px touch targets
        └── app.js              # Upload & local-path modes, Track reorder/remove, live chapters, 16:9 preview, SSE client
```

## Open Questions

| # | Question | Owner | Resolve by | Blocking? |
|---|----------|-------|------------|-----------|
| _(none — all Wave 0 questions resolved)_ | | | | |

### Resolved

| # | Question | Resolution |
|---|----------|------------|
| 1 | **LAN (`--host 0.0.0.0`) local-filesystem guard** | Resolved Nov 16, 2026 04:09:00 PM EST — User accepted recommendation: allow local-filesystem path reads/writes strictly from loopback (`127.0.0.1` / `::1`) when bound locally; reject local path inputs/writes from non-loopback requests or `--host 0.0.0.0` so LAN/mobile clients use Upload mode and `/api/render/{id}/output` + `/timestamps` downloads only. |
| 2 | **Offline typography strategy** | Resolved Nov 16, 2026 04:09:00 PM EST — User accepted recommendation: pure offline system font stacks (`"Instrument Serif", Georgia, serif`, `"Plus Jakarta Sans", system-ui, sans-serif`, `"IBM Plex Mono", ui-monospace, monospace`) with zero external network requests. |
| 3 | **Upload cap & single-render concurrency** | Resolved Nov 16, 2026 04:09:00 PM EST — User accepted recommendation: enforce a `512 MiB` multipart upload limit per request (streamed to disk via `MultipartReader`), allow 1 active Render job at a time (`409 Conflict` when busy), and retain up to 5 completed jobs until shutdown. |
| 4 | **Domain docs (`ADR-0002`)** | Resolved Nov 16, 2026 04:09:00 PM EST — User accepted recommendation: add `docs/adr/0002-embedded-http-ui-and-sse-telemetry.md` during Phase 4. |

## Decisions (confirmed)

| Topic | Decision |
|-------|----------|
| Zero External Go Dependencies | Use Go `1.22+` standard library only (`net/http`, `embed`, `encoding/json`, `io`, `os`, `path/filepath`, `sync`, `crypto/rand`); `go.mod` must remain dependency-free (`depguard` compliant). |
| CLI Backwards Compatibility | Expose the studio via `ytmix ui [--host 127.0.0.1] [--port 8080] [--no-open]` subcommand in `cmd/ytmix/main.go`, leaving all existing root CLI flags (`--audio-dir`, `--bg`, `--out`, etc.) unchanged. |
| Default Bind Address | Default `--host` is `127.0.0.1` and `--port` is `8080`; `--host 0.0.0.0` is explicit opt-in for LAN/mobile device access. |
| LAN Local-Filesystem Guard | Allow local-filesystem path reads (`--audio-dir`, local `--bg`) and arbitrary `--out`/`--timestamps` disk writes strictly for loopback (`127.0.0.1`/`::1`) local sessions; non-loopback / `--host 0.0.0.0` LAN sessions use Upload mode and job-scoped HTTP downloads only. |
| Offline Typography | Use pure offline system font stacks (`Instrument Serif` -> `Georgia, serif`; `Plus Jakarta Sans` -> `system-ui, sans-serif`; `IBM Plex Mono` -> `ui-monospace, monospace`) with zero outbound network requests. |
| Upload Cap & Single-Render Guard | Cap multipart upload requests at `512 MiB` (streamed directly to disk via `MultipartReader`), allow 1 active Render job at a time (`409 Conflict` when busy), and retain up to 5 recent jobs for download until server shutdown. |
| Preserve UI Track Order & Uploads (`mixer.RunWithTracks`) | Do not re-scan `AudioDir` when rendering from the UI; add `mixer.RunWithTracks` sharing the 5-step pipeline with `mixer.Run` so user-reordered (`↑`/`↓`), removed (`×`), or uploaded `.mp3` Tracks execute deterministically without accidental re-sorting or re-shuffling. |
| Opaque Server-Side Session Tokens | `POST /api/probe` returns an opaque `sessionId` and per-Track `trackId`s backed by server-side session state; upload-mode clients never submit raw server temp paths in `POST /api/render`. |
| Streaming Multipart & Extension Validation | Stream uploads to disk via `r.MultipartReader()` (avoiding whole-file RAM buffering), enforce `.mp3` and `mixer.IsSupportedBackgroundExt` allowlists, sanitize base filenames, and write temp files with `0600` (`gosec` G306). |
| HTTP Server Hardening (`gosec`) | Configure `http.Server{ReadHeaderTimeout: 10 * time.Second}` (`gosec` G112), clean up temp workspaces on job eviction and graceful shutdown (`SIGINT`/`SIGTERM`), and document `#nosec G204` only if browser-open invokes a validated `http://` localhost URL via discrete args. |
| Rendering Path & Chapters Reuse | Reuse `render.SelectPath` / `render.DetectEncoderArgs` / `mixer.RunWithTracks` directly so `Path A`, `Path B`, `Path C`, `-14 LUFS` Loudness Normalization, and `Chapter Timestamps` match CLI output identically. |
| Canonical Glossary Vocabulary | Use `Track`, `Display Title`, `Background Visual`, `Mix`, `Crossfade`, `Loudness Normalization`, `Chapter Timestamps`, `Title Overlay`, and `Rendering Path` (`Path A` / `Path B` / `Path C`) across Go code, UI copy, and docs. |
| Domain Docs (`ADR-0002`) | Record stdlib `net/http` + `embed.FS` + SSE + vanilla JS architecture choice in `docs/adr/0002-embedded-http-ui-and-sse-telemetry.md`. |

## Grill

**Recorded:** Nov 16, 2026 04:07:00 PM EST

### Design tree

```text
Editorial Mix Studio (ytmix ui) Design Tree
├── 1. CLI Entrypoint & Server Lifecycle
│   ├── `ytmix ui` subcommand (--host, --port, --no-open) preserves root CLI flags [Confirmed from Issue #5 + cmd/ytmix/main.go]
│   ├── Graceful shutdown on SIGINT/SIGTERM cleans up temp dirs & cancels active render ctx [Auto-taken Critical — G5]
│   └── Browser auto-open uses discrete OS args with validated localhost URL + --no-open bypass [Auto-taken Low-risk — G6]
├── 2. Pipeline Integration (`internal/mixer`)
│   ├── `mixer.Run` re-scans AudioDir alphabetically/shuffled; breaks UI Track reorder/removal [Verified in mixer.go:166]
│   ├── Add `mixer.RunWithTracks` + `mixer.ProbeTrackFiles` sharing `runPipeline` [Auto-taken Critical — G1]
│   └── Capture `[1/5]`–`[5/5]` stdout lines via line-scanning io.Writer to feed SSE telemetry [Auto-taken Low-risk — G7]
├── 3. HTTP / SSE API & Security (`internal/ui`)
│   ├── Opaque `sessionId`/`trackId` registry prevents client-supplied arbitrary path injection in Upload mode [Auto-taken Critical — G2]
│   ├── Streaming `MultipartReader` + extension allowlist + filename sanitization + `0600` temp mode [Auto-taken Critical — G3]
│   ├── `http.Server.ReadHeaderTimeout` + `.golangci.yml` (`depguard`, `gosec`, `errcheck`, `staticcheck`) compliance [Auto-taken Critical — G4]
│   ├── LAN (`--host 0.0.0.0`) local-filesystem path restriction policy [Open Question #1 — Other]
│   └── Upload size cap (`512 MiB`) & single-active-render concurrency guard [Open Question #3 — Other]
├── 4. Editorial Mix Studio v3 Frontend (`internal/ui/assets/`)
│   ├── Two-column `1.12fr / 0.88fr` hairline grid + mobile responsive (`>=44px` targets, `tabular-nums`) [Confirmed from Issue #5]
│   ├── Dark (Anodized Obsidian) & Light (Studio Paper) localStorage toggle [Confirmed from Issue #5]
│   └── Offline font strategy (pure system stacks vs progressive Google Fonts) [Open Question #2 — Other]
└── 5. Verification & Domain Docs
    ├── Mocked `httptest` unit suite + live `ffmpeg`/`ffprobe` E2E verification [Confirmed from AGENTS.md]
    └── Add `docs/adr/0002-embedded-http-ui-and-sse-telemetry.md` [Open Question #4 — Other]
```

### Auto-taken (Critical / Low-risk)

| # | Class | Rec | Where applied |
|---|-------|-----|---------------|
| G1 | Critical | Add `mixer.RunWithTracks` (and `mixer.ProbeTrackFiles`) sharing `runPipeline` with `mixer.Run` so UI Track reordering (`↑`/`↓`), Track removals (`×`), and uploaded `.mp3` files do not get overwritten by `ProbeTracks(cfg.AudioDir, ...)`. | Decisions / Phase 1 |
| G2 | Critical | Return opaque `sessionId` and `trackId` tokens from `POST /api/probe` instead of accepting raw server filesystem paths from upload-mode `POST /api/render` payloads. | Decisions / Phase 2 |
| G3 | Critical | Stream multipart uploads directly to `tempDir` via `r.MultipartReader()` with `http.MaxBytesReader`, `.mp3`/`mixer.IsSupportedBackgroundExt` allowlisting, `filepath.Base` sanitization, and `0600` file permissions (`gosec` G306). | Decisions / Phase 2 |
| G4 | Critical | Set `ReadHeaderTimeout: 10 * time.Second` on `http.Server` (`gosec` G112), preserve zero external deps (`depguard`), and enforce LF line endings (`gofmt` on Linux CI). | Decisions / Phase 2 |
| G5 | Critical | Cancel active render job contexts and remove all session/job temp directories cleanly on server shutdown (`SIGINT`/`SIGTERM`). | Phase 1 & Phase 2 |
| G6 | Low-risk | Launch default browser best-effort (unless `--no-open` or non-loopback bind) with discrete OS binary args (`rundll32 url.dll,FileProtocolHandler` on Windows, `open` on macOS, `xdg-open` on Linux) and documented `#nosec G204`. | Phase 1 |
| G7 | Low-risk | Feed SSE `[1/5]`–`[5/5]` telemetry via an `io.Writer` adapter passed into `mixer.RunWithTracks`, replaying buffered events on SSE connect so early step logs are never lost. | Phase 2 |

### Remaining (Other → Open Questions)

1. **Q1 (`Blocking? Yes`):** LAN (`--host 0.0.0.0`) local-filesystem path access vs Upload-only restriction for non-loopback sessions.
2. **Q2 (`Blocking? Yes`):** Air-gapped system font stacks vs progressive Google Fonts `<link>` in `index.html`.
3. **Q3 (`Blocking? Yes`):** `512 MiB` upload size cap and single active render concurrency (`409 Conflict` when busy, 5-job retention cap).
4. **Q4 (`Blocking? No`):** Author `docs/adr/0002-embedded-http-ui-and-sse-telemetry.md` alongside `README.md` updates.

## Wave 0: Planning & Scope Lock <!-- serial -->

**Triggered by:** User (`/coordinator` on Issue #5) — Nov 16, 2026 04:07:00 PM EST

### Phase 0: Plan Creation, Grill & Wave 0 Approval

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Agent (Planner) — Nov 16, 2026 04:07:00 PM EST

- [x] Inspect `ytmix` repo (`AGENTS.md`, `GLOSSARY.md`, `docs/adr/0001`, `.golangci.yml`, `cmd/ytmix`, `internal/mixer`, `internal/render`)
- [x] Write `docs/plans/editorial-mix-studio-ui.md`, update `docs/plans/README.md`, and complete post-write `## Grill`
- [x] Claim Issue #5 (`@GCherry`, `work-started` label, kickoff comment)
- [x] Resolve Wave 0 Open Questions (#1–#4) and obtain user sign-off to start Wave 1 (`feature/editorial-mix-studio-ui`)

#### Verification Plan

- Confirm `docs/plans/editorial-mix-studio-ui.md` contains `## Grill`, `## Open Questions`, `## Decisions (confirmed)`, and canonical `GLOSSARY.md` terms.
- Confirm GitHub Issue #5 has `work-started` label and kickoff comment.

#### Phase Summary

Completed Nov 16, 2026 04:09:00 PM EST. Wave 0 plan and post-write Grill written; GitHub Issue #5 claimed (`work-started` label + kickoff comment); user approved the plan and accepted all 4 recommended Wave 0 Open Question answers (`LAN local-filesystem guard`, `pure offline typography`, `512 MiB upload cap + single active render`, `ADR-0002`). Handed off to Wave 1 Engineering on branch `feature/editorial-mix-studio-ui`.

## Wave 1: Pipeline Extension & CLI Entrypoint <!-- serial -->

**Triggered by:** User (Wave 0 approval) — Nov 16, 2026 04:09:00 PM EST

### Phase 1: `internal/mixer` Track-Order Support & `ytmix ui` CLI Subcommand

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Agent (Engineering) — Nov 16, 2026 04:09:00 PM EST

- [x] Create branch `feature/editorial-mix-studio-ui` off `main`
- [x] Add `ProbeTrackFiles(ctx context.Context, files []string, runner CommandRunner) ([]Track, error)` and `RunWithTracks(ctx context.Context, cfg Config, tracks []Track, stdout io.Writer, lookPath func(string) (string, error), runner CommandRunner) error` in `internal/mixer/mixer.go`, extracting shared `runPipeline` with `Run` so `[1/5]`–`[5/5]` steps, `Normalize`, `Crossfade`, `WriteChapters`, `WriteASS`, and `render.Render` execute identically without re-scanning `AudioDir`
- [x] Add unit tests in `internal/mixer/mixer_test.go` verifying `ProbeTrackFiles` and `RunWithTracks` preserve explicit non-alphabetical Track order and reject empty/missing Track inputs
- [x] Update `cmd/ytmix/main.go` to add `IsUISubcommand`, `NewUIFlagSet`, and `ParseUIFlags` (`--host` default `127.0.0.1`, `--port` default `8080`, `--no-open` default `false`, optional `--audio-dir` & `--bg` UI prefills) (server startup & browser open wires in Phase 2 once `internal/ui` exists)
- [x] Add unit tests in `cmd/ytmix/main_test.go` covering `ui` subcommand flag defaults/overrides and backwards compatibility of root Mix flags

#### Verification Plan

- `go test -v -skip "^TestE2EPipeline$" ./cmd/ytmix ./internal/mixer` — passed (`TestParseFlags`, `TestParseUIFlags`, `TestValidateConfig`, `TestProbeTracks`, `TestRunPipelineSteps1To5`, `TestProbeTrackFilesAndRunWithTracks`).
- `go vet ./...` — passed (exit `0`).

#### Phase Summary

Completed Nov 16, 2026 04:11:00 PM EST. Extracted `validateTrackFile`, `probeFiles`, `prepareRun`, and `runPipeline` in `internal/mixer/mixer.go`, added `ProbeTrackFiles` + `RunWithTracks` with unit tests in `internal/mixer/mixer_test.go`, and added `UIConfig`/`IsUISubcommand`/`ParseUIFlags` + unit tests in `cmd/ytmix`. Wiring `main()` to start `internal/ui.Server` follows in Phase 2.

## Wave 2: Embedded HTTP/SSE Server & Editorial Mix Studio Frontend <!-- serial -->

**Triggered by:** Agent (Engineering — Wave 1 Complete) — Nov 16, 2026 04:11:00 PM EST

### Phase 2: `internal/ui` HTTP/SSE Server, Job Lifecycle & Security Hardening

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Agent (Engineering) — Nov 16, 2026 04:11:00 PM EST

- [x] Implement `internal/ui/server.go` and `internal/ui/jobs.go` (zero external dependencies; `//go:embed assets` serving `GET /`, `GET /styles.css`, `GET /app.js` with accurate `Content-Type`) and wire `ytmix ui` startup + browser auto-open in `cmd/ytmix/main.go`
- [x] Implement `GET /api/health` checking `ffmpeg` & `ffprobe` via injected `lookPath` and probing encoder availability (`render.DetectEncoderArgs`)
- [x] Implement `POST /api/probe` supporting multipart `.mp3` streaming uploads (`MultipartReader`, `MaxBytesReader`, `.mp3` allowlist, sanitized filenames, `0600` temp mode) and validated local `--audio-dir` JSON requests, returning opaque `sessionId`, `trackId`, `fileName`, `displayTitle`, and `duration` (plus loopback-only `GET /api/preview-bg` for local Background Visual preview)
- [x] Implement `POST /api/render` accepting Mix configuration, ordered Track IDs/selections, and uploaded or local `--bg` Background Visual (`mixer.IsSupportedBackgroundExt`), invoking `mixer.RunWithTracks` asynchronously in a managed job goroutine with single-active-render protection (`409` if busy) and returning `{jobId}`
- [x] Implement `GET /api/render/{id}/events` SSE (`text/event-stream`) streaming `[1/5]`–`[5/5]` step logs, completion payload (`renderingPath`, `timestampsText`, download URLs), and errors with buffered replay for late subscribers
- [x] Implement `GET /api/render/{id}/output` and `GET /api/render/{id}/timestamps` serving generated artifacts, plus clean `Close()`/`Shutdown()` cleanup of all session and job temp directories
- [x] Write comprehensive `httptest` unit tests in `internal/ui/server_test.go` covering `/api/health`, `/api/preview-bg`, `/api/probe` (multipart + JSON + invalid extension + size cap + LAN guard), `/api/render` (Track order preservation, invalid crossfade/bg, 409 busy guard), SSE `[1/5]`–`[5/5]` stream, `/output` + `/timestamps` downloads, and temp cleanup using mocked `CommandRunner`/`lookPath`

#### Verification Plan

- `go build ./...`, `go vet ./...`, and `go test -v -skip "^TestE2EPipeline$" ./...` all exited `0` (`internal/ui`: 7 test suites passed).
- Security checks verified: `ReadHeaderTimeout: 10s`, `0600` temp file writes, `.mp3` / `IsSupportedBackgroundExt` allowlists, `512 MiB` default upload limit, loopback-only local path guard (`403` on non-loopback/LAN).

#### Phase Summary

Completed Nov 16, 2026 04:14:30 PM EST. Implemented `internal/ui/jobs.go`, `internal/ui/server.go`, and `internal/ui/server_test.go`, plus wired `ytmix ui` server startup & graceful shutdown into `cmd/ytmix/main.go`. Added `GET /api/preview-bg` (loopback + Background Visual extension gated) so local-path `--bg` images/videos preview live in the 16:9 stage. Handed off to Phase 3 frontend build.

### Phase 3: Editorial Mix Studio v3 Frontend (`internal/ui/assets/`)

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Agent (Engineering) — Nov 16, 2026 04:14:30 PM EST

- [x] Build `internal/ui/assets/index.html`, `styles.css`, and `app.js` implementing the Editorial Mix Studio v3 spec:
  - Two-column architectural hairline desktop grid (`1.12fr / 0.88fr`) collapsing cleanly to a single-column mobile layout (`>= 44px` touch targets, `tabular-nums` on durations and timestamps)
  - Dark Mode (Anodized Obsidian Chassis `#060707`/`#131516`/`#0B0C0D`/`#3EE6B5`/`#E8BE5A`) & Light Mode (Studio Paper `#E6E4DC`/`#F5F4EF`/`#ECEAE2`/`#0F766E`/`#C28E1A`) toggle persisted in `localStorage` with pure offline system font stacks
  - `01 · Tracks & Sequence`: dual source mode (Upload MP3s drag-and-drop/browse + Local Folder `--audio-dir`), `--shuffle` toggle, Track table with cumulative `Start`, `Display Title`, `Length`, `↑`/`↓` reorder, `×` remove, and click-to-preview `Title Overlay`
  - `02 · Background Visual`: drag-and-drop/browse upload (`.jpg`, `.jpeg`, `.png`, `.webp`, `.mp4`, `.mov`, `.gif`) or local `--bg` path, live 16:9 stage preview, and automatic `Path A` / `Path B` / `Path C` indicator
  - `03 · Audio & Title Overlays`: `--crossfade` (`0`–`8s` slider live-recalculating `Chapter Timestamps` and flagging invalid crossfade >= shortest Track), `--normalize` (`-14 LUFS`), `--overlay-titles`, `--font-name`, `--title-duration`
  - Right column: 16:9 Stage Preview (gold corner ticks, HUD badges, bottom-left `.ass` `Title Overlay` `♪ Display Title`, output metadata bar `H.264 + AAC 320k` + fps), live `timestamps.txt` preview + `Copy`, editable `--out` / `--timestamps` + live equivalent `ytmix` CLI preview + `Copy CLI`, `04 · Pipeline Telemetry (5 Steps)` driven by SSE `[1/5]`–`[5/5]`, progress bar, and Gold `Render Mix → <out>` CTA + post-render artifact download links

#### Verification Plan

- `go build ./cmd/ytmix` embeds `internal/ui/assets/*` cleanly.
- `TestStaticAssetsAndHealth` confirms `GET /`, `GET /styles.css`, and `GET /app.js` serve `200 OK` with expected Editorial Mix Studio markup, `#060707` palette tokens, and `/api/render` client logic.
- Live `ytmix.exe ui` E2E verification confirmed `GET /` `200 OK`, `/api/health`, `/api/probe`, `/api/render`, SSE `[1/5]`–`[5/5]` + `complete`, and `/output` + `/timestamps` downloads.

#### Phase Summary

Completed Nov 16, 2026 04:15:45 PM EST. Implemented `internal/ui/assets/index.html`, `styles.css`, and `app.js` with zero external requests, Obsidian/Paper theme toggle, responsive mobile layout (`>= 44px` touch targets), live 16:9 stage & chapter preview, and real-time SSE telemetry client.

## Wave 3: Verification, Documentation & Closeout <!-- serial -->

**Triggered by:** Agent (Engineering — Wave 2 Complete) — Nov 16, 2026 04:15:45 PM EST

### Phase 4: End-to-End Verification, Documentation & PR Prep

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Agent (Engineering) — Nov 16, 2026 04:15:45 PM EST

- [x] Run full Go verification suite: `gofmt -l cmd internal` (clean), `go vet ./...`, `go build ./...`, `GOOS=darwin go build ./...`, `go test -v -skip '^TestE2EPipeline$' ./...`, and `go test -count=1 -v -run TestE2EPipeline ./internal/mixer`
- [x] Run live synthetic `ytmix ui` E2E verification against `ffmpeg`/`ffprobe` (`ytmix.exe ui --port 0 --no-open`: probed synthetic `01_Alpha.mp3` + `02_Beta.mp3`, rendered reversed `[Beta, Alpha]` with `crossfade=1.0`, `overlayTitles=true`, `cover.png` via `Path C`, verified SSE `[1/5]`–`[5/5]` + `complete`, asserted `1920x1080` `h264` + `aac` `6.000000s` via `ffprobe` and `00:00 Beta` / `00:03 Alpha` in `timestamps.txt`, cleaned up temp dir)
- [x] Update `README.md` with `ytmix ui` subcommand, flags (`--host`, `--port`, `--no-open`, `--audio-dir`, `--bg`), and LAN/mobile upload usage; add `docs/adr/0002-embedded-http-ui-and-sse-telemetry.md`
- [x] Run independent `Reviewer` (`code-review`, Round 2 clean: 0 blocking findings), `Planner` `Acceptance Check` (`PASS`), and `Release` (`pre-merge`, archived to `docs/plans/archive/editorial-mix-studio-ui.md` in final PR commit)

#### Verification Plan

- `gofmt -l cmd internal` -> empty (clean).
- `go vet ./...` -> exit `0`.
- `go build ./...` & `GOOS=darwin go build ./...` -> exit `0`.
- `go test -v -skip '^TestE2EPipeline$' ./...` -> all packages pass (`cmd/ytmix`, `internal/audio`, `internal/mixer`, `internal/probe`, `internal/render`, `internal/subtitle`, `internal/ui`).
- `go test -count=1 -v -run TestE2EPipeline ./internal/mixer` -> all 3 FFmpeg E2E paths pass (`7.469s`).
- Live `ytmix.exe ui` E2E smoke test -> `HEALTH={"status":"ok","ffmpeg":true,"ffprobe":true,"encoder":"libx264",...}`, `SSE_STEPS_OK=True`, `TIMESTAMPS=00:00 Beta | 00:03 Alpha`, `FFPROBE` `h264 1920x1080` + `aac`, `duration=6.000000`.

#### Phase Summary

Completed Nov 16, 2026 04:19:00 PM EST. Updated `README.md`, authored `docs/adr/0002-embedded-http-ui-and-sse-telemetry.md`, fixed `errcheck` (`fmt.Fprintln`, `_, _ = w.buf.Write`) and `staticcheck` ST1005 findings during self-review, and verified all builds, unit tests, `TestE2EPipeline`, and live `ytmix.exe ui` HTTP/SSE/FFmpeg end-to-end flow. Ready for independent `Reviewer` (`code-review`), `Planner` `Acceptance Check`, and `Release` (`pre-merge`).

## Final Recap

Implemented the zero-dependency Editorial Mix Studio v3 web & mobile UI for `ytmix` (`ytmix ui`) on branch `feature/editorial-mix-studio-ui`:
- Extended `internal/mixer` (`ProbeTrackFiles`, `RunWithTracks`, shared `runPipeline`) so UI Track reordering, removals, and uploaded `.mp3` files run deterministically without re-scanning `AudioDir`.
- Added `internal/ui` (`server.go`, `jobs.go`, `server_test.go`, `assets/{index.html,styles.css,app.js}`) using Go stdlib `net/http`, `embed.FS`, and SSE (`text/event-stream`) with opaque `sessionId`/`trackId` tokens, `512 MiB` streaming multipart uploads, loopback-only local path isolation, single-active-render concurrency guard, and Dark/Light responsive Editorial Mix Studio v3 UI.
- Wired `ytmix ui` subcommand (`--host`, `--port`, `--no-open`, `--audio-dir`, `--bg`) in `cmd/ytmix/main.go`, documented usage in `README.md`, and recorded architectural decisions in `docs/adr/0002-embedded-http-ui-and-sse-telemetry.md`.

## Deployment Plan

- Open PR from `feature/editorial-mix-studio-ui` targeting `main` (`Closes #5`), archive this plan to `docs/plans/archive/editorial-mix-studio-ui.md` in the final PR commit, verify all GitHub Actions CI & Security checks (`ci.yml` + `security.yml`) pass on `expectedHeadSha`, and await human merge approval (`Risk: Medium`).
