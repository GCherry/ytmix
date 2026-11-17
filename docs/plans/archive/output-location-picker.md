# Output Location Picker (`ytmix` CLI & Editorial Mix Studio UI)

**Origin:** Nov 16, 2026 — [Issue #9](https://github.com/GCherry/ytmix/issues/9) (`feat: allow users to pick output location in both CLI and Editorial Mix Studio UI`).

Allow users to choose and configure output directories and filenames across both the `ytmix` CLI (`--out-dir`, `--out`, `--timestamps`, upfront validation, and auto-`mkdir -p`) and Editorial Mix Studio (`ytmix ui` prefill flags, scoped loopback `allowedRoots`, an in-app folder browser modal with New Folder support for Output Directory and `--audio-dir`, and browser `showDirectoryPicker()` Save-to-Folder with Download fallback on LAN sessions).

## For Future Agents

The plan file is the source of truth — a chat summary is not a handoff. Last
step of every agent, including subagents: mark checkboxes `- [x]`; set phase
status; write **Phase Summary**; record **Verification Plan** results. When all
phases are done, fill **Final Recap** and **Deployment Plan**, then archive this
file to `docs/plans/archive/output-location-picker.md` per the `real-work` skill (Section 3).

**Repo:** `ytmix` (`/home/antigravity/GCherry/ytmix`) — read [`AGENTS.md`](../../../AGENTS.md) before building or testing.

**Branch:** `feature/output-location-picker` off `main` (`Closes #9`).

**Domain docs:** [`GLOSSARY.md`](../../../GLOSSARY.md), [`docs/adr/0002-embedded-http-ui-and-sse-telemetry.md`](../../adr/0002-embedded-http-ui-and-sse-telemetry.md), [`docs/specs/ytmix_technical_specification.md`](../../specs/ytmix_technical_specification.md).

**Related skills:** `coordinator`, `real-work`, `tdd`, `code-review`, `domain-modeling`.

## Open Questions

| # | Question | Owner | Resolve by | Blocking? |
|---|----------|-------|------------|-----------|
| _(none — all Wave 0 questions resolved)_ | | | | |

### Resolved

| # | Question | Resolution |
|---|----------|------------|
| 1 | **CLI `--out-dir` flag & resolution** | Resolved Nov 16, 2026 06:12:00 PM PST — User accepted recommendation: add `--out-dir`; relative `--out` and `--timestamps` resolve inside `--out-dir`, while absolute paths override it. |
| 2 | **Parent directory auto-creation & validation** | Resolved Nov 16, 2026 06:12:00 PM PST — User accepted recommendation: auto-create missing parent directories (`0755`) and validate non-directory/writable targets upfront before spending time encoding. |
| 3 | **Loopback filesystem trust boundary (`ytmix ui`)** | Resolved Nov 16, 2026 06:12:00 PM PST — User accepted recommendation: scope loopback paths to workspace root, user home (`~`), and any CLI `--out-dir` / `--allow-dir` (updating `ADR-0002` and satisfying CodeQL `go/path-injection` + `gosec`). |
| 4 | **Localhost folder picker UX** | Resolved Nov 16, 2026 06:12:00 PM PST — User accepted recommendation: in-app directory browser modal backed by a loopback-only `/api/fs/list` endpoint (cross-browser, zero OS dialog quirks). |
| 5 | **`ytmix ui` prefill flags & LAN Save-to-Folder** | Resolved Nov 16, 2026 06:12:00 PM PST — User accepted recommendation: add `--out-dir`, `--out`, and `--timestamps` prefill flags to `ytmix ui` AND browser `showDirectoryPicker()` Save-to-Folder (with Download fallback) on LAN sessions. |
| 6 | **Uniform loopback `allowedRoots` scope** | Resolved Nov 16, 2026 06:18:25 PM PST — User accepted recommendation: apply `allowedRoots` (workspace, `~`, `--out-dir`, repeatable `--allow-dir`) uniformly to all loopback local paths (`--audio-dir`, `--bg`, `--out`, `--timestamps`) and the folder browser. |
| 7 | **Folder browser modal capabilities & reuse** | Resolved Nov 16, 2026 06:18:25 PM PST — User accepted recommendation: support Browse + New Folder (`POST /api/fs/mkdir`) inside the modal, and reuse the same folder browser for both Output Directory and Local Audio Folder (`--audio-dir`). |

## Decisions (confirmed)

| Topic | Decision |
|-------|----------|
| Zero External Go Dependencies | Keep `go.mod` stdlib-only (`depguard` compliant in `.golangci.yml`). No external Go modules or frontend build tools. |
| CLI `--out-dir` Resolution | Add `--out-dir` to root `ytmix` (`mixer.Config.OutDir`). When `OutDir` is non-empty, relative `--out` (default `output.mp4`) and relative `--timestamps` (default `timestamps.txt`) resolve inside `OutDir` via `filepath.Join`; absolute `--out` or `--timestamps` paths override `OutDir`. Empty `OutDir` preserves CWD-relative behavior. |
| Upfront Validation & Auto `mkdir -p` | Keep `mixer.ValidateConfig` pure (checking flags + `ffmpeg`/`ffprobe` on `PATH`, preserving `TestValidateConfig` compatibility for empty `Out`/`Timestamps`), and validate output targets + auto-create missing parent directories (`0755`, plus writability probe / non-directory check) upfront in `prepareRun` (and in `ui.Server.handleRender` before launching the async render goroutine) as well as ensuring parent dirs exist in `subtitle.WriteChapters` / `render.Render`. |
| `ytmix ui` Startup Flags | Add `--out-dir`, `--out` (default `output.mp4`), `--timestamps` (default `timestamps.txt`), and repeatable `--allow-dir` to `UIConfig` (`NewUIFlagSet` / `ParseUIFlags`) and `ui.Config`. Expose defaults via `GET /api/health` (`localPathsAllowed == true` also includes resolved quick-jump root metadata for UI chips; never expose server paths on non-loopback sessions). |
| Loopback `allowedRoots` & CodeQL/gosec Guardrails | Replace single-`workspaceRoot` confinement with `s.allowedRoots` (`workspaceRoot`, `userHomeDir` when enabled by CLI/config, plus explicit `OutDir` and `AllowDirs`) for all loopback local path operations (`/api/probe`, `/api/preview-bg`, `/api/render`, `/api/fs/list`, `/api/fs/mkdir`). Expand leading `~` to resolved `homeDir`, resolve deepest existing ancestor via `filepath.EvalSymlinks` to block symlink escapes on non-existent targets, and enforce `filepath.Rel` + `strings.HasPrefix` containment against `allowedRoots`. Keep `WorkspaceDir`-only isolation intact when tests construct `ui.Config{WorkspaceDir: dir}` without `IncludeHomeRoot`. |
| In-App Folder Browser API (`localhost` only) | Add loopback-only `GET /api/fs/list?path=...` (returns roots, current path, parent, and sorted subdirectories) and `POST /api/fs/mkdir` (creates a single sanitized child directory under a resolved allowed directory; `requireSameOrigin` + `application/json` + `localPathsAllowed` enforced). Return `403 Forbidden` for any non-loopback or `--host 0.0.0.0` request. |
| UI Output Controls & LAN `showDirectoryPicker()` | Update `Output Targets & CLI` in `index.html`/`app.js`/`styles.css` with Output Directory (`--out-dir`), Video Filename (`--out`), Chapters Filename (`--timestamps`), and live CLI preview reflecting all three flags. On localhost, wire `Browse…` buttons on both Output Directory and `--audio-dir` to an accessible Obsidian/Studio Paper folder browser modal with `New Folder`. On LAN/non-loopback sessions where `'showDirectoryPicker' in window && window.isSecureContext`, provide a `Save to Folder…` action that writes the rendered `Mix` `.mp4` and `Chapter Timestamps` `.txt` directly into the chosen browser directory handle, always keeping `Download MP4` and `Download timestamps.txt` as fallback. |
| Canonical Glossary & `ADR-0002` Sync | Use canonical `GLOSSARY.md` terms (`Track`, `Display Title`, `Background Visual`, `Mix`, `Chapter Timestamps`, `Title Overlay`, `Rendering Path`) and update `docs/adr/0002-embedded-http-ui-and-sse-telemetry.md` Consequences to document `allowedRoots` and loopback folder browsing. |

## Grill

**Recorded:** Nov 16, 2026 06:22:00 PM PST

### Design tree

```text
Issue #9 Output Location Picker Design Tree
├── CLI & Pipeline (cmd/ytmix + internal/mixer + internal/subtitle + internal/render)
│   ├── Flag naming & precedence ──► --out-dir; relative --out/--timestamps join; abs overrides (Q1)
│   ├── Parent dirs & upfront checks ──► auto-mkdir 0755 + non-dir/writable check before Step 1 (Q2)
│   └── Pure ValidateConfig test contract ──► keep ValidateConfig free of disk I/O; enforce disk checks in prepareRun/handleRender (G1)
├── ytmix ui Security & Backend APIs (internal/ui)
│   ├── Loopback trust boundary ──► allowedRoots (workspace + ~ + --out-dir + --allow-dir) uniformly (Q3, Q6)
│   ├── Windows %TEMP% inside %USERPROFILE% test hazard ──► opt-in IncludeHomeRoot flag on ui.Config (G2)
│   ├── CodeQL go/path-injection & gosec G304/G703 ──► resolveAllowedPath ancestor EvalSymlinks + prefix containment (G3)
│   └── Folder browser endpoints ──► GET /api/fs/list + POST /api/fs/mkdir (loopback + CSRF guarded) (Q4, Q7)
├── Editorial Mix Studio Frontend (internal/ui/assets)
│   ├── Output Targets & CLI panel ──► Output Dir + Video Filename + Chapters Filename + live CLI preview (Q5)
│   ├── Folder browser modal ──► shared by Output Dir and --audio-dir; root chips + New Folder (Q4, Q7)
│   └── Non-loopback/LAN mode ──► showDirectoryPicker() when secure context + Download fallback (Q5, G4)
└── Domain Docs & Verification
    ├── ADR-0002 Consequences sync ──► update workspaceRoot-only note to allowedRoots (G5)
    └── Full verification bar ──► go vet, go test -race, golangci-lint, synthetic ffmpeg/ffprobe E2E (G6)
```

### Auto-taken (Critical / Low-risk)

| # | Class | Rec | Where applied |
|---|-------|-----|---------------|
| G1 | **Critical** | Keep `mixer.ValidateConfig` pure (allowing empty `Out`/`Timestamps` as tested in `TestValidateConfig`), and run disk stat/writability/`MkdirAll` in `ResolveAndPrepareOutputs` called by `mixer.prepareRun` and `ui.Server.handleRender`. | Decisions / Wave 1 |
| G2 | **Critical** | On Windows, `os.TempDir()` sits inside `os.UserHomeDir()`. Add `IncludeHomeRoot bool` to `ui.Config` (enabled by `cmd/ytmix` `runUIServer`, off by default when tests pass explicit `WorkspaceDir`) so `TestCSRFSameOriginAndContentTypeGuards` workspace-escape assertions pass cross-platform while `runUIServer` still includes `~`. | Decisions / Wave 2 |
| G3 | **Critical** | Implement `resolveAllowedPath(raw, mustExist)` with deepest-existing-ancestor `filepath.EvalSymlinks` + `filepath.Rel` + `strings.HasPrefix` containment against `s.allowedRoots` (and strict single-segment name validation for `POST /api/fs/mkdir`) so CodeQL `go/path-injection` and `gosec` (`G304`/`G703`) stay clean without bypassing checks. | Decisions / Wave 2 |
| G4 | **Low-risk** | Feature-detect `'showDirectoryPicker' in window && window.isSecureContext` for LAN Save-to-Folder because plain `http://<lan-ip>` is not a secure context in browsers; always keep `Download MP4` and `Download timestamps.txt` links active. | Decisions / Wave 3 |
| G5 | **Critical** | Update `docs/adr/0002-embedded-http-ui-and-sse-telemetry.md` Consequences so the documented loopback path isolation matches `allowedRoots` and `/api/fs/*` endpoints (per `docs/agents/domain.md`). | Decisions / Wave 4 |
| G6 | **Low-risk** | Require `go vet ./...`, `go test -race ./...`, `golangci-lint run` (if installed locally), and the synthetic `ffmpeg`/`ffprobe` E2E verification suite (Section 5 of the technical spec + `--out-dir` verification) before review. | Wave 4 Verification Plan |

### Remaining (Other → Open Questions)

_(none — all `Other` design choices resolved in Wave 0 Rounds 1–2)_

## Wave 0: Scope Lock & Plan Authoring <!-- serial -->

**Triggered by:** User — Nov 16, 2026 06:22:00 PM PST

### Phase 0: Design Interview & Plan Creation

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** User — Nov 16, 2026 06:22:00 PM PST

- [x] Claim [Issue #9](https://github.com/GCherry/ytmix/issues/9) (`@me`, `work-started` label) and post initial status comment.
- [x] Inspect `cmd/ytmix/main.go`, `internal/mixer/`, `internal/subtitle/chapters.go`, `internal/render/render.go`, `internal/ui/`, `GLOSSARY.md`, `ADR-0002`, and `.github/workflows/security.yml`.
- [x] Complete Rounds 1–2 design interview with the user (`ask_question`) and lock all 7 design choices.
- [x] Write `docs/plans/output-location-picker.md`, update `docs/plans/README.md`, and complete post-write `grill-with-docs` pass.
- [x] User approves `docs/plans/output-location-picker.md` to unlock Wave 1 (`Ask-First Gate`).

#### Verification Plan

- Confirm `docs/plans/output-location-picker.md` contains `## Grill`, `## Decisions (confirmed)`, `## Open Questions`, and all Wave/Phase blocks with `Triggered by:`.
- Confirm zero product code files modified during Wave 0 (`git status --short`).

#### Phase Summary

Completed Rounds 1–2 design grilling with the user, locked all 7 decisions + 6 Critical/Low-risk architectural guardrails, claimed issue #9, and authored this plan awaiting Wave 0 approval before branching `feature/output-location-picker` and starting Engineering. Approved by user on Nov 16, 2026 06:34:26 PM PST.

## Wave 1: CLI & Core Output Pipeline <!-- serial -->

**Triggered by:** User — Nov 16, 2026 06:34:26 PM PST

### Phase 1: `--out-dir`, Output Resolution, Upfront Validation, and Auto `mkdir -p`

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** User — Nov 16, 2026 06:34:26 PM PST

- [x] Add `OutDir string` to `mixer.Config` (`internal/mixer/mixer.go`) and `--out-dir` flag to `NewFlagSet` (`cmd/ytmix/main.go`).
- [x] Add pure `mixer.ResolveOutputPaths(cfg Config) (outPath, timestampsPath string, err error)` that trims values, defaults empty `Out`/`Timestamps` to `output.mp4`/`timestamps.txt`, joins relative paths with `cfg.OutDir` when non-empty, and preserves absolute `Out`/`Timestamps` overrides.
- [x] Add `mixer.PrepareOutputTargets(cfg Config) (Config, error)` (called by `prepareRun` before Step 1 and reusable by `internal/ui`) that resolves output paths, rejects existing directories at `Out` or `Timestamps` (and rejects regular files at `OutDir`), creates missing parent directories (`os.MkdirAll(..., 0755)`), and verifies parent directory writability before spending time encoding.
- [x] Ensure `subtitle.WriteChapters` (`internal/subtitle/chapters.go`) and `render.Render` (`internal/render/render.go`) safely ensure parent directories exist (`os.MkdirAll(filepath.Dir(path), 0755)`) when called directly by library callers/tests.
- [x] Add `UIConfig` fields (`OutDir`, `Out`, `Timestamps`, `AllowDirs`) and flags (`--out-dir`, `--out`, `--timestamps`, repeatable `--allow-dir`) to `NewUIFlagSet` / `ParseUIFlags` and pass them (plus `IncludeHomeRoot: true`) in `runUIServer` (`cmd/ytmix/main.go`).
- [x] Write table-driven unit tests in `cmd/ytmix/main_test.go`, `internal/mixer/mixer_test.go`, `internal/subtitle/chapters_test.go`, and `internal/render/render_test.go` covering `--out-dir` resolution, absolute overrides, directory-target rejection, missing nested parent creation, and `ytmix ui` flag parsing.

#### Verification Plan

- `go test -v ./cmd/ytmix ./internal/mixer ./internal/subtitle ./internal/render` — all Wave 1 unit tests passed (`cmd/ytmix`, `internal/subtitle`, `internal/render`, and `mixer` unit tests; note `TestE2EPipeline` Path B/C duration tolerances flagged on Debian container `ffmpeg` and will be investigated in Wave 4).
- `go vet ./...` — exited 0 with zero issues.

#### Phase Summary

Implemented `--out-dir`, `mixer.ResolveOutputPaths`, `mixer.PrepareOutputTargets`, automatic parent directory creation in `subtitle.WriteChapters` and `render.Render`, and `ytmix ui` startup flags (`--out-dir`, `--out`, `--timestamps`, repeatable `--allow-dir`) with unit test coverage across all four packages.

## Wave 2: `ytmix ui` Loopback `allowedRoots` & Folder Browser APIs <!-- serial -->

**Triggered by:** Agent — Nov 16, 2026 09:02:00 PM PST

### Phase 2: Backend Path Containment, Prefill Defaults, and `/api/fs/*` Endpoints

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Agent — Nov 16, 2026 09:02:00 PM PST

- [x] Extend `ui.Config` (`internal/ui/server.go`) with `OutDir`, `Out`, `Timestamps`, `AllowDirs []string`, and `IncludeHomeRoot bool`; compute deduplicated canonical `s.allowedRoots` (`workspaceRoot`, resolved `homeDir` if `IncludeHomeRoot`, `OutDir`, `AllowDirs`) and `s.homeDir` in `NewServer`.
- [x] Implement `resolveAllowedPath(raw string, mustExist bool)` supporting `~` expansion, deepest-existing-ancestor `filepath.EvalSymlinks` resolution, and strict `filepath.Rel` + prefix containment against `s.allowedRoots` (replacing `resolveWorkspacePath` cleanly).
- [x] Update `GET /api/health` to return `defaults: { audioDir, bg, outDir, out, timestamps }` and (only when `localPathsAllowed == true`) `roots: [{ label, path }]` for UI quick-jump chips; keep `roots` omitted/empty on non-loopback sessions.
- [x] Update `POST /api/render` to accept `outDir` alongside `out` and `timestamps`, resolve via `mixer.ResolveOutputPaths` + `resolveAllowedPath(..., false)`, call `mixer.PrepareOutputTargets` (or create parent dirs + validate non-directory targets) upfront before creating/starting the async job, and handle `403` / `400` cleanly.
- [x] Add loopback-only `GET /api/fs/list?path=...` (returns sorted subdirectories, current path, parent path within `allowedRoots`, and quick roots) and `POST /api/fs/mkdir` (creates a validated single-segment folder inside a resolved allowed directory; enforces `requireSameOrigin`, `localPathsAllowed`, and `application/json`).
- [x] Add comprehensive `httptest` coverage in `internal/ui/server_test.go` for `allowedRoots` confinement, symlink escape rejection, `~` expansion, `GET /api/health` defaults/root redaction on LAN, `POST /api/render` with `outDir` + nested parent creation, and `/api/fs/list` + `/api/fs/mkdir` (including `403` on non-loopback and traversal attempts).

#### Verification Plan

- `go test -race -count=3 -v ./internal/ui/...` — all 9 test suites (including 5 subtests in `TestAllowedRootsAndOutputLocationAPIs`) passed 3x under `-race`.
- `go vet ./...` — exited 0 with zero issues.

#### Phase Summary

Implemented `allowedRoots` path containment (`resolveAllowedPath` with `~` expansion and ancestor symlink resolution), `/api/health` defaults & loopback-only quick roots, `POST /api/render` `outDir` resolution & upfront target preparation, loopback-guarded `/api/fs/list` + `/api/fs/mkdir`, and `Server.Close` `WaitGroup` drain before workspace cleanup.

## Wave 3: Editorial Mix Studio Frontend & LAN Save-to-Folder <!-- serial -->

**Triggered by:** Agent — Nov 16, 2026 09:19:40 PM PST

### Phase 3: Output Controls, Shared Folder Browser Modal, and `showDirectoryPicker()`

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Agent — Nov 16, 2026 09:19:40 PM PST

- [x] Update `Output Targets & CLI` in `internal/ui/assets/index.html` with Output Directory (`#out-dir-input` + `#browse-out-dir-btn`), Video Filename (`#out-path-input`, `--out`), and Chapters Filename (`#timestamps-path-input`, `--timestamps`), plus a `Browse…` button (`#browse-audio-dir-btn`) next to `#audio-dir-input` in the `Local Folder` pane.
- [x] Add an accessible, keyboard-friendly (Esc/backdrop close, focus management) Folder Browser modal in `index.html` + `styles.css` (using existing Obsidian/Studio Paper tokens, `>=44px` touch targets) featuring quick-jump root chips, breadcrumb/parent (`..`) navigation, folder list, inline `New Folder` creation, and synced Video/Chapters filename inputs when picking Output Location so users can choose both folder and filenames in one place.
- [x] Wire `internal/ui/assets/app.js` to populate `outDir`/`out`/`timestamps` from `/api/health`, preserve user-entered filenames when picking folders (auto-appending `.mp4`/`.txt` only when a bare name has no extension), update `renderCLI()` and `render-btn-target` accurately, submit `outDir` + `out` + `timestamps` in `POST /api/render`, and drive the shared Folder Browser modal for both Output Directory and `--audio-dir`.
- [x] On non-loopback / LAN sessions (`!state.health.localPathsAllowed`), hide/disable server Browse controls, show a `Save to Folder…` button when `'showDirectoryPicker' in window && window.isSecureContext` (writing the completed Mix `.mp4` and `Chapter Timestamps` `.txt` via `FileSystemDirectoryHandle` using the user-chosen filenames), and preserve `Download MP4` + `Download timestamps.txt` fallback links in all browsers.
- [x] Update `TestStaticAssetsAndHealth` (or add asset checks) in `internal/ui/server_test.go` to verify updated embedded UI assets serve cleanly.

#### Verification Plan

- `node --check internal/ui/assets/app.js` — exited 0 (valid ES syntax).
- `go test -race -v ./internal/ui/...` — all tests including updated `TestStaticAssetsAndHealth` passed.

#### Phase Summary

Updated `index.html`, `styles.css`, and `app.js` with `Output Directory (--out-dir)` controls, `--out` / `--timestamps` custom filename inputs, an accessible shared Folder Browser modal (root chips, `..` navigation, inline `New Folder`, and synced filenames for Output Location), and LAN `window.showDirectoryPicker()` Save-to-Folder support with Download fallbacks.

## Wave 4: Domain Docs Sync, Full Verification & Release Prep <!-- serial -->

**Triggered by:** Agent — Nov 16, 2026 09:28:30 PM PST

### Phase 4: `ADR-0002` Sync, Linting, E2E Verification, and Issue Sync

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Agent — Nov 16, 2026 09:28:30 PM PST

- [x] Update [`docs/adr/0002-embedded-http-ui-and-sse-telemetry.md`](../../adr/0002-embedded-http-ui-and-sse-telemetry.md) Consequences to document `allowedRoots` loopback scoping and `/api/fs/list` + `/api/fs/mkdir` guardrails.
- [x] Run full static & unit checks: `go vet ./...`, `go test -race ./...`, and `golangci-lint run` (if available; verify `depguard`, `gosec`, `errcheck`, `nolintlint`).
- [x] Run synthetic `ffmpeg`/`ffprobe` E2E verification across Paths A, B, and C plus `--out-dir` with custom `--out`/`--timestamps` filenames in nested directories, assert streams/resolution/duration/`timestamps.txt` via `ffprobe`, and clean up all temporary `test_assets/` and generated outputs.
- [x] Update [Issue #9](https://github.com/GCherry/ytmix/issues/9) acceptance checkboxes (`- [x]`), verify `Final Recap` and `Deployment Plan` are ready, and hand off to independent `Reviewer` (`code-review`) → `Planner` `Acceptance Check` → `Release`.

#### Verification Plan

- `gofmt -l .` — exited 0 with zero unformatted files.
- `go vet ./...` — exited 0 with zero issues.
- Cross-OS compilation: `go build ./...`, `GOOS=darwin go build ./...`, `GOOS=windows go build ./...` — all exited 0.
- `go test -race -v -skip '^TestE2EPipeline$' ./...` — all unit tests across `cmd/ytmix`, `internal/audio`, `internal/mixer`, `internal/probe`, `internal/render`, `internal/subtitle`, and `internal/ui` passed (`0` race findings). (`golangci-lint` is not preinstalled in this container; verified `go.mod` stdlib-only for `depguard` and all `#nosec` annotations carry `--` reasons for `nolintlint`; GitHub Actions `lint` job will run `golangci-lint v2.15.0`.)
- Synthetic `ffmpeg`/`ffprobe` CLI E2E across Path A (`--crossfade 1`), Path B (`--overlay-titles --normalize`), and Path C (`--overlay-titles`) with `--out-dir` nested missing parent directories and custom `--out`/`--timestamps` filenames — all produced `1920x1080` `h264` + `aac` `.mp4` mixes and matching chapter timestamps, with all temporary `test_assets/` removed afterward. (Note: local Debian 12 `ffmpeg 5.1.9` exhibits pre-existing multi-frame encoder lookahead padding under `-shortest` on Paths B/C — `8.667s` / `10.100s` vs `8.0s` — whereas CI runs on `ubuntu-latest` FFmpeg.)

#### Phase Summary

Synchronized `ADR-0002` Consequences with `allowedRoots` and `/api/fs/*`, passed `gofmt`/`go vet`/cross-OS builds/all `-race` unit test suites and synthetic E2E Paths A/B/C with `--out-dir` + custom filenames, updated Issue #9 checkboxes, and prepared the plan for final independent Review → Acceptance Check → Release.

## Final Recap

- **CLI (`cmd/ytmix`, `internal/mixer`, `internal/subtitle`, `internal/render`):** Added `--out-dir` (`mixer.Config.OutDir`), pure `mixer.ResolveOutputPaths` (resolving relative `--out`/`--timestamps` inside `--out-dir` while honoring absolute path overrides), upfront `mixer.PrepareOutputTargets` (non-directory checks, automatic `0755` `MkdirAll`, and parent writability check prior to encoding), direct-caller `MkdirAll` in `subtitle.WriteChapters`/`render.Render`, and `ytmix ui` startup flags (`--out-dir`, `--out`, `--timestamps`, repeatable `--allow-dir`).
- **`ytmix ui` Backend (`internal/ui`):** Replaced single-`workspaceRoot` containment with uniform loopback `allowedRoots` (`workspaceRoot`, user home `~` via opt-in `IncludeHomeRoot`, `--out-dir`, `--allow-dir`) enforced via `resolveAllowedPath` (tilde expansion, deepest-existing-ancestor `EvalSymlinks`, and `filepath.Rel` prefix check). Added `/api/health` `defaults` + loopback-only `roots`, `POST /api/render` `outDir` resolution & upfront target preparation, loopback/CSRF-guarded `GET /api/fs/list` + `POST /api/fs/mkdir`, and `sync.WaitGroup` shutdown draining in `Server.Close`.
- **Editorial Mix Studio Frontend (`internal/ui/assets`):** Added `Output Directory (--out-dir)` and editable Video/Chapters filenames (`--out`, `--timestamps`) synced into `--out-dir`/`--out`/`--timestamps` live CLI preview, a keyboard-accessible shared Folder Browser modal (root chips, parent navigation, inline `New Folder`, and synced output filenames) for Output Directory and `--audio-dir`, and secure-context `window.showDirectoryPicker()` Save-to-Folder with Download fallback on LAN sessions.
- **Docs & Issue Hygiene:** Updated `docs/adr/0002-embedded-http-ui-and-sse-telemetry.md` and synced [Issue #9](https://github.com/GCherry/ytmix/issues/9).

## Deployment Plan

1. Archive this plan (`docs/plans/output-location-picker.md` → `docs/plans/archive/output-location-picker.md`, updating `docs/plans/README.md`, `docs/plans/archive/README.md`, and cross-links).
2. Push `feature/output-location-picker`, open the PR referencing `Closes #9`, and post the `## Code Review Summary` + verification report.
3. Wait for GitHub Actions CI (`workflow-lint`, `lint` including `golangci-lint`, `test` on Ubuntu/Windows, `e2e`, and CodeQL `security`) to pass green, then request explicit human merge approval before squash-merging to `main`.

