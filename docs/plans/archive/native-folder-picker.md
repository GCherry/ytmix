# Native OS Folder & File Picker (`ytmix ui`)

**Origin:** Nov 19, 2026 — [Issue #21](https://github.com/GCherry/ytmix/issues/21) (`bug(ui): folder picker restricts browsing to Workspace and Home instead of native OS picker or full filesystem`), following user approval of Recommended Option 1.

Replace the restricted Workspace/Home-only browse flow in Editorial Mix Studio (`ytmix ui`) with a zero-dependency native OS folder/file picker (`POST /api/fs/pick`) on loopback sessions (`127.0.0.1` / `localhost`) for `--audio-dir`, `--out-dir`, and local Background Visual (`--bg`), dynamically authorizing picked directories for the session while keeping the existing in-app folder browser modal as an automatic fallback when no desktop GUI is available.

## For Future Agents

The plan file is the source of truth — a chat summary is not a handoff. Last
step of every agent, including subagents: mark checkboxes `- [x]`; set phase
status; write **Phase Summary**; record **Verification Plan** results. When all
phases are done, fill **Final Recap** and **Deployment Plan**, then archive this
file to `docs/plans/archive/native-folder-picker.md` per the `real-work` skill (Section 3).

**Repo:** `ytmix` (`D:\CodingWorkspace\ytmix\ytmix`) — read [`AGENTS.md`](../../../AGENTS.md) before building or testing.

**Branch:** `fix/native-folder-picker` off `main` (`Closes #21`).

**Domain docs:** [`GLOSSARY.md`](../../../GLOSSARY.md), [`docs/adr/0002-embedded-http-ui-and-sse-telemetry.md`](../../adr/0002-embedded-http-ui-and-sse-telemetry.md), [`docs/specs/ytmix_technical_specification.md`](../../specs/ytmix_technical_specification.md).

**Related skills:** `coordinator`, `real-work`, `tdd`, `code-review`, `domain-modeling`.

## Open Questions

| # | Question | Owner | Resolve by | Blocking? |
|---|----------|-------|------------|-----------|
| _(none — user approved Recommended Option 1 on Nov 19, 2026)_ | | | | |

### Resolved

| # | Question | Resolution |
|---|----------|------------|
| 1 | **Native OS picker vs. third-party `ncruces/zenity` vs. web modal only** | Resolved Nov 19, 2026 10:28:40 PM EST — User approved Option 1: zero-dependency native OS picker via OS commands (`POST /api/fs/pick`) with automatic fallback to the in-app web modal when headless/unavailable. Keeps `go.mod` and `.golangci.yml` `depguard` `$gostd`-only (leaving #15 untouched). |
| 2 | **Loopback security boundary for picked paths** | Resolved Nov 19, 2026 10:28:40 PM EST — User approved Option 1: canonicalize the picked directory (or `filepath.Dir(pickedFile)` for `--bg`) and register it into `s.allowedRoots` / `s.quickRoots` for that session under a `sync.RWMutex`, preserving `resolveAllowedPath` containment and LAN (`--host 0.0.0.0`) isolation. |
| 3 | **Background Visual (`--bg`) local path browse button** | Resolved Nov 19, 2026 10:28:40 PM EST — User approved Option 1: add a `Browse…` button (`#browse-bg-path-btn`) in `#bg-local-pane` wired to `kind: "bgFile"` filtered to `.jpg;.jpeg;.png;.webp;.mp4;.mov;.gif`. |

## Decisions (confirmed)

| Topic | Decision |
|-------|----------|
| Zero External Go Dependencies | Keep `go.mod` stdlib-only (`$gostd` in `.golangci.yml` `depguard`). Invoke OS-native dialogs via `os/exec` behind an injectable `PickPathFunc` on `ui.Config` for deterministic unit testing. |
| Loopback `POST /api/fs/pick` Contract | Accept `{ "kind": "folder" \| "bgFile", "initialPath": string }`. Enforce `requireSameOrigin(r)`, `s.localPathsAllowed(r)` (`403` on LAN), strict `application/json`, and a single active dialog guard (`409 Conflict` if a picker dialog is already open). Return `200 { "cancelled": false, "path": "<canonical>" }` on selection, `200 { "cancelled": true }` on user cancel, or `503 { "error": "...", "fallback": true }` when native GUI tooling is unavailable/headless. |
| Cross-Platform OS Dialog Adapters | **Windows:** `powershell.exe -NoProfile -NonInteractive -STA` using modern Explorer `IFileOpenDialog` (`FOS_PICKFOLDERS` for `folder`) and `System.Windows.Forms.OpenFileDialog` (for `bgFile`). **macOS (`darwin`):** `osascript` (`choose folder` / `choose file`). **Linux/other:** `zenity` (`--file-selection --directory` / `--file-filter`), falling back to `kdialog`, or `ErrPickerUnavailable` (`fallback: true`) if headless (`DISPLAY`/`WAYLAND_DISPLAY` unset) or missing. Pass `initialPath` strictly via `YTMIX_PICK_INIT` env var (never interpolated into script text) to eliminate command-injection risks. |
| Thread-Safe Dynamic `allowedRoots` | Protect `s.allowedRoots` and `s.quickRoots` with `s.rootsMu sync.RWMutex`. On successful `POST /api/fs/pick`, canonicalize the selected folder (or `filepath.Dir` of a selected `--bg` file) and register it via `addAllowedRoot` so subsequent `/api/probe`, `/api/preview-bg`, `/api/render`, `/api/fs/list`, and `/api/fs/mkdir` requests succeed without relaxing existing `resolveAllowedPath` containment or breaking `server_test.go` escape tests. |
| Frontend Browse UX & Fallback | Wire `#browse-audio-dir-btn` (`kind: "folder"`), `#browse-out-dir-btn` (`kind: "folder"`), and new `#browse-bg-path-btn` (`kind: "bgFile"`) to call `POST /api/fs/pick` first (disabling browse buttons while open). If the server responds with `fallback: true` (HTTP `503`), automatically fall back to `openFSModal` for `audioDir` and `outDir` (and show a clear hint for `bgFile`). Keep LAN mode unchanged (browse buttons hidden/disabled, `window.showDirectoryPicker()` + Download links active). |
| Canonical Glossary & `ADR-0002` Sync | Use canonical `GLOSSARY.md` terms (`Track`, `Background Visual`, `Mix`, `Chapter Timestamps`) and update `docs/adr/0002-embedded-http-ui-and-sse-telemetry.md` Consequences to document `POST /api/fs/pick` and dynamic session authorization of OS-picked paths. |

## Grill

**Recorded:** Nov 19, 2026 10:30:00 PM EST

### Design tree

```text
Issue #21 Native OS Folder & File Picker Design Tree
├── Backend Native Picker & Security (`internal/ui`)
│   ├── Zero-dep OS dialog adapters ──► PowerShell STA IFileOpenDialog/OpenFileDialog (Win), osascript (macOS), zenity/kdialog (Linux) (G1)
│   ├── Command injection & gosec G204 ──► fixed script constants + YTMIX_PICK_INIT env var; zero user-input interpolation (G2)
│   ├── Concurrency & lifecycle ──► single-dialog mutex/409 guard + exec.CommandContext bound to r.Context() / s.baseCtx (G3)
│   └── Dynamic allowedRoots thread-safety ──► sync.RWMutex around s.allowedRoots / s.quickRoots; existing escape tests stay intact (G4)
├── Editorial Mix Studio Frontend (`internal/ui/assets`)
│   ├── Local Folder & Output Dir Browse ──► call POST /api/fs/pick; auto-probe audioDir after pick; fallback to openFSModal on 503 fallback (G5)
│   ├── Background Visual (--bg) Browse ──► add #browse-bg-path-btn in #bg-local-pane; auto-preview on 16:9 stage after pick
│   └── LAN isolation ──► keep #browse-bg-path-btn hidden/disabled alongside #browse-audio-dir-btn and #browse-out-dir-btn (G6)
└── Domain Docs & Verification
    ├── ADR-0002 Consequences sync ──► document POST /api/fs/pick + session-scoped OS-picked path authorization (G7)
    └── Full verification bar ──► go vet, go test -race, cross-OS builds (windows/darwin/linux), node --check app.js, synthetic ffmpeg E2E
```

### Auto-taken (Critical / Low-risk)

| # | Class | Rec | Where applied |
|---|-------|-----|---------------|
| G1 | **Critical** | Use modern Win32 `IFileOpenDialog` (`FOS_PICKFOLDERS`) via PowerShell STA on Windows rather than legacy `.NET 4.8` `FolderBrowserDialog` (`SHBrowseForFolder`) so users get the standard Windows Explorer dialog with address bar, Quick Access, search, and all drives. | Decisions / Phase 1 |
| G2 | **Critical** | Never interpolate `initialPath` or user strings into PowerShell/AppleScript source; pass validated existing directory paths solely via `YTMIX_PICK_INIT` environment variable and use fixed command arguments so `gosec` G204 / CodeQL stay clean. | Decisions / Phase 1 |
| G3 | **Critical** | Guard concurrent `POST /api/fs/pick` calls with an active-picker flag (`409 Conflict` if already open) and pass a context tied to `r.Context()` + `s.baseCtx` to `exec.CommandContext` so closing the server or cancelling the request never leaks hanging dialog processes. | Decisions / Phase 1 |
| G4 | **Critical** | Protect `s.allowedRoots` and `s.quickRoots` with `sync.RWMutex` across `addAllowedRoot`, `resolveAllowedPath`, `isWithinAnyAllowedRoot`, and `handleFSList` so dynamic root registration from `POST /api/fs/pick` is race-free under `go test -race`. | Decisions / Phase 1 |
| G5 | **Low-risk** | Automatically trigger `probeLocalFolder()` after a successful `audioDir` pick and `renderAll()` (updating stage Background Visual preview + CLI preview) after a successful `bgFile` / `outDir` pick. | Phase 2 |
| G6 | **Critical** | Hide and disable `#browse-bg-path-btn` on non-loopback / LAN sessions (`!state.health.localPathsAllowed`) identically to `#browse-audio-dir-btn` and `#browse-out-dir-btn`. | Phase 2 |
| G7 | **Critical** | Update `docs/adr/0002-embedded-http-ui-and-sse-telemetry.md` Consequences to document `POST /api/fs/pick` and session-scoped `allowedRoots` expansion on loopback. | Phase 3 |

### Remaining (Other → Open Questions)

_(none — all product & architectural choices approved by the user on Nov 19, 2026)_

## Wave 0: Scope Lock & Plan Authoring <!-- serial -->

**Triggered by:** User — Nov 19, 2026 10:28:40 PM EST

### Phase 0: Triage, Issue Claim & Plan Creation

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** User — Nov 19, 2026 10:28:40 PM EST

- [x] Verify reported behavior in `internal/ui/server.go` (`NewServer`, `handleFSList`, `resolveAllowedPath`) and `internal/ui/assets/app.js` (`openFSModal`, `loadFSList`).
- [x] File [Issue #21](https://github.com/GCherry/ytmix/issues/21) in `GCherry/ytmix` with triage verification and open design questions.
- [x] Obtain user approval on Recommended Option 1 (zero-dependency native OS picker + dynamic session `allowedRoots` + web modal fallback + `--bg` file browse).
- [x] Author `docs/plans/native-folder-picker.md`, register it in `docs/plans/README.md`, and complete the post-write `grill-with-docs` pass.
- [x] Update [Issue #21](https://github.com/GCherry/ytmix/issues/21) (`ready-for-agent`, `work-started`, acceptance checkboxes) and post the Wave 0 kickoff comment.

#### Verification Plan

- Confirm `docs/plans/native-folder-picker.md` contains `## Grill`, `## Decisions (confirmed)`, `## Open Questions`, and all Wave/Phase blocks with `Triggered by:`.
- Confirm zero product code files modified during Wave 0 (`git status --short`).

#### Phase Summary

Confirmed the `Workspace`/`Home` root restriction in `internal/ui/server.go` and `internal/ui/assets/app.js`, logged Issue #21, obtained user confirmation for Recommended Option 1, wrote this plan with `grill-with-docs` guardrails G1–G7, and claimed Issue #21 with `work-started`.

## Wave 1: Backend Native OS Picker & Dynamic Session Roots <!-- serial -->

**Triggered by:** Agent — Nov 19, 2026 10:30:00 PM EST

### Phase 1: `POST /api/fs/pick`, OS Dialog Adapters, and Thread-Safe `allowedRoots`

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Agent — Nov 19, 2026 10:30:00 PM EST

- [x] Add `PickPathFunc` (`func(ctx context.Context, kind, initialDir string) (string, error)`) to `ui.Config` (`internal/ui/server.go`), defaulting to the zero-dep native OS picker implementation in `internal/ui/picker.go`.
- [x] Implement `internal/ui/picker.go` with `pickKindFolder` (`"folder"`) and `pickKindBGFile` (`"bgFile"`), sentinel errors `ErrPickerCancelled` and `ErrPickerUnavailable`, and OS-specific zero-dep adapters:
  - Windows (`windows`): `powershell.exe -NoProfile -NonInteractive -STA` invoking modern Win32 `IFileOpenDialog` (`FOS_PICKFOLDERS`) for folders and `System.Windows.Forms.OpenFileDialog` filtered to `.jpg;.jpeg;.png;.webp;.mp4;.mov;.gif` for Background Visuals, reading optional `YTMIX_PICK_INIT` from env.
  - macOS (`darwin`): `osascript` (`choose folder` / `choose file`) reading optional `YTMIX_PICK_INIT` from env.
  - Linux/other: `zenity` (`--file-selection --directory` / `--file-filter`) or `kdialog` when GUI display (`DISPLAY` or `WAYLAND_DISPLAY`) is present, otherwise returning `ErrPickerUnavailable`.
- [x] Protect `s.allowedRoots` and `s.quickRoots` with `s.rootsMu sync.RWMutex` across `addAllowedRoot`, `isWithinAnyAllowedRoot`, `resolveAllowedPath`, and `handleFSList`.
- [x] Register `POST /api/fs/pick` on `Server` (`internal/ui/server.go`): enforce `requireSameOrigin`, `localPathsAllowed`, `Content-Type: application/json`, valid `kind` (`"folder"` or `"bgFile"`), and single-active-dialog lock (`409 Conflict` when busy); resolve optional `initialPath` best-effort to an existing directory; validate returned file extension for `"bgFile"` via `render.CanonicalBackgroundExt`; canonicalize and register the picked directory (or `filepath.Dir(pickedFile)`) into `s.allowedRoots` / `s.quickRoots`; return `200 { cancelled, path }` or `503 { error, fallback: true }` on `ErrPickerUnavailable`.
- [x] Add thorough unit tests in `internal/ui/picker_test.go` and `internal/ui/server_test.go` covering: `POST /api/fs/pick` folder + `bgFile` selection dynamically unlocking `/api/probe`, `/api/preview-bg`, `/api/render`, and `/api/fs/list`; cancellation (`{ cancelled: true }`); `503 fallback: true` when unavailable; `403` on non-loopback / cross-origin; `400` on invalid `kind` or unsupported `--bg` extension; `409` on concurrent pickers; and preservation of existing `allowedRoots` / symlink-escape rejection tests.

#### Verification Plan

- `go test -v ./internal/ui/...` — all picker (`TestPickWithEnvCrossPlatform`) and server (`TestNativePickerEndpointAndSessionRoots` + existing `internal/ui` tests) passed (`0.542s`). (Note: `-race` requires CGo/`gcc` which is absent on this Windows host; verified thread-safe `s.rootsMu` + `s.pickActive` locking and will run full cross-OS compile checks in Phase 3.)
- `go vet ./...` — exited 0.

#### Phase Summary

Implemented `internal/ui/picker.go` (zero-dep Windows `IFileOpenDialog`/`OpenFileDialog`, macOS `osascript`, and Linux `zenity`/`kdialog` adapters with env-passed `YTMIX_PICK_INIT`), added thread-safe `rootsMu`, `pickActive`, and `POST /api/fs/pick` to `internal/ui/server.go`, and verified all unit and integration tests pass.

## Wave 2: Editorial Mix Studio Frontend Wiring & Fallback <!-- serial -->

**Triggered by:** Agent — Nov 19, 2026 10:35:00 PM EST

### Phase 2: `Browse…` Native Picker Calls, `--bg` File Browse, and Web Modal Fallback

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Agent — Nov 19, 2026 10:30:00 PM EST

- [x] Add `#browse-bg-path-btn` (`Browse…`) beside `#bg-path-input` inside `#bg-local-pane` in `internal/ui/assets/index.html` (and ensure `.inline-field` alignment in `styles.css` if needed).
- [x] Update `internal/ui/assets/app.js` `applyHealth` / LAN mode handling so `#browse-bg-path-btn` is hidden and disabled when `!state.health.localPathsAllowed`, matching `#browse-audio-dir-btn` and `#browse-out-dir-btn`.
- [x] Implement `pickNativePath(kind, initialPath, triggerBtn)` in `internal/ui/assets/app.js` calling `POST /api/fs/pick`:
  - Disables browse trigger while the OS dialog is open (`409` safe) and restores focus/enabled state in `finally`.
  - On `{ cancelled: false, path }`: updates `#audio-dir-input` (+ auto-triggers `probeLocalFolder()`), `#out-dir-input` (+ `renderAll()`), or `#bg-path-input` (+ `renderAll()` to update 16:9 stage preview & CLI preview).
  - On `503` with `fallback: true`: automatically opens `openFSModal` for `audioDir` / `outDir`, or displays a concise inline hint for `bgFile`.
- [x] Update `TestStaticAssetsAndHealth` in `internal/ui/server_test.go` to verify `/api/fs/pick` and `#browse-bg-path-btn` presence in embedded assets.

#### Verification Plan

- `node --check D:\CodingWorkspace\ytmix\ytmix\internal\ui\assets\app.js` — exited 0.
- `go test ./internal/ui/...` — passed (`0.550s`).

#### Phase Summary

Added `#browse-bg-path-btn` to `internal/ui/assets/index.html`, implemented `pickNativePath` and LAN hiding/disabling in `internal/ui/assets/app.js` (wiring `--audio-dir`, `--out-dir`, and `--bg` `Browse…` buttons with automatic `openFSModal` fallback on `503 fallback: true`), and updated `TestStaticAssetsAndHealth` + `TestCSRFSameOriginAndContentTypeGuards`.

## Wave 3: Docs Sync, Full Verification & Release Prep <!-- serial -->

**Triggered by:** Agent — Nov 19, 2026 10:30:00 PM EST

### Phase 3: `ADR-0002` Sync, Static Analysis, E2E Verification, and Issue Checkboxes

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Agent — Nov 19, 2026 10:30:00 PM EST

- [x] Update [`docs/adr/0002-embedded-http-ui-and-sse-telemetry.md`](../../adr/0002-embedded-http-ui-and-sse-telemetry.md) Consequences to document loopback `POST /api/fs/pick` and session-scoped `allowedRoots` authorization for user-picked OS paths.
- [x] Run full verification suite: `gofmt -l .`, `go vet ./...`, `go test ./...` (including `internal/mixer` E2E), cross-OS builds (`windows`/`darwin`/`linux`), and `node --check internal/ui/assets/app.js`. (`golangci-lint` is not installed locally; CI runs it on PR.)
- [x] Verify Windows `IFileOpenDialog` (`COM`) and `System.Windows.Forms.OpenFileDialog` filter/type initialization via `powershell.exe -NoProfile -NonInteractive -STA`. (Note: live socket bind to `127.0.0.1` is blocked inside the agent sandbox; HTTP routes are verified via `httptest` in `server_test.go`.)
- [x] Update [Issue #21](https://github.com/GCherry/ytmix/issues/21) checkboxes (`- [x]`), complete `Final Recap` + `Deployment Plan`, and hand off to independent `Reviewer` (`code-review`) → `Planner` `Acceptance Check` → `Release`.

#### Verification Plan

- `gofmt -l D:\CodingWorkspace\ytmix\ytmix` — empty output (all files formatted).
- `go vet ./...` — exited 0.
- `go test ./...` — all 7 packages passed (`cmd/ytmix` 0.406s, `internal/audio` 0.384s, `internal/mixer` 33.311s including E2E, `internal/probe` 0.388s, `internal/render` 6.821s, `internal/subtitle` 0.385s, `internal/ui` uncached `-count=1` 0.533s).
- Cross-OS `go build ./...` (`windows`, `darwin`, `linux`) — all exited 0.
- `node --check internal/ui/assets/app.js` — exited 0.

#### Phase Summary

Updated `ADR-0002` Consequences, passed `gofmt`, `go vet`, full `go test ./...` (including `internal/mixer` E2E), cross-OS builds, `node --check`, and PowerShell COM/`OpenFileDialog` verification. Ready for independent `Reviewer` (`code-review`), `Planner` `Acceptance Check`, and `Release` archival + PR creation.

## Final Recap

- **Backend (`internal/ui/picker.go`, `internal/ui/server.go`):** Added zero-dependency cross-platform native OS dialog adapters (`PickPathFunc`: Windows `IFileOpenDialog` / `OpenFileDialog`, macOS `osascript`, Linux `zenity`/`kdialog` with `ErrPickerUnavailable` fallback), loopback/CSRF/Content-Type-guarded `POST /api/fs/pick` with single-dialog `pickActive` concurrency lock, and `sync.RWMutex`-protected dynamic session registration in `s.allowedRoots` / `s.quickRoots`.
- **Frontend (`internal/ui/assets/index.html`, `internal/ui/assets/app.js`):** Wired `--audio-dir`, `--out-dir`, and new Background Visual (`--bg`) `#browse-bg-path-btn` buttons to `pickNativePath`, auto-probing `--audio-dir` and refreshing stage/CLI previews after selection, automatically falling back to `openFSModal` when `503 fallback: true` is returned, and keeping LAN sessions locked down.
- **Docs & Tests (`docs/adr/0002-embedded-http-ui-and-sse-telemetry.md`, `internal/ui/picker_test.go`, `internal/ui/server_test.go`):** Synced `ADR-0002` Consequences and added unit/integration coverage for cross-platform picker adapters, dynamic session authorization, cancellation, `503` fallback, `409` concurrency, CSRF/`403` guards, and embedded UI asset checks.

## Deployment Plan

1. Commit implementation and run independent `Reviewer` (`code-review`) + `Planner` `Acceptance Check`.
2. Archive this plan (`docs/plans/native-folder-picker.md` → `docs/plans/archive/native-folder-picker.md`, updating `docs/plans/README.md`, `docs/plans/archive/README.md`, and cross-references) in the final PR commit per `real-work` Section 3.
3. Push `fix/native-folder-picker`, open the PR referencing `Closes #21` with `## Code Review Summary`, wait for GitHub Actions CI (`lint`, `test`, `e2e`, `security`) to pass green, and pause for human merge approval.
