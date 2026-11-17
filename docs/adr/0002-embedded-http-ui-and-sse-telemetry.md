# 0002: Embedded Stdlib HTTP UI, SSE Telemetry, and Loopback Path Isolation

Status: accepted

`ytmix ui` embeds the Editorial Mix Studio frontend directly inside the Go binary via `embed.FS` and serves it over Go's standard library `net/http` with Server-Sent Events (`text/event-stream`) for real-time `[1/5]`–`[5/5]` pipeline telemetry, rather than introducing a Node/React build pipeline, desktop CGo/Wails wrapper, or WebSocket dependency.

## Considered Options

- **Zero-dependency stdlib `net/http` + `embed.FS` + vanilla HTML/CSS/JS vs. SPA bundler (Vite/React) or Wails/CGo:** An external SPA toolchain or Wails/WebView wrapper adds Node/npm or native GUI build dependencies, breaking `ytmix`'s single-binary zero-dependency constraint (`go.mod` enforced by `depguard`) and complicating cross-platform CI on Linux/macOS/Windows.
- **Server-Sent Events (`text/event-stream`) vs. WebSockets:** `ytmix` only needs unidirectional server-to-browser pipeline progress (`[1/5]`–`[5/5]` steps, completion metadata, and errors). SSE runs over standard `net/http` (`http.Flusher`) with zero third-party packages and native browser `EventSource` support.
- **`mixer.RunWithTracks` + opaque `sessionId`/`trackId` tokens vs. re-scanning `AudioDir` or trusting client paths:** `mixer.Run` re-discovers and alphabetically sorts (or shuffles) `--audio-dir`, which would discard browser Track reordering (`↑`/`↓`) and Track removals (`×`). Adding `mixer.RunWithTracks` preserves the caller's exact Track slice while sharing the 5-step pipeline, and opaque server-side tokens prevent upload clients from submitting arbitrary server filesystem paths.

## Consequences

- `ytmix ui` binds to `127.0.0.1:8080` by default; passing `--host 0.0.0.0` (or receiving any non-loopback `RemoteAddr`) automatically disables local-filesystem path inputs/outputs (`--audio-dir`, `--bg`, `/api/preview-bg`, `/api/fs/*`, arbitrary `--out-dir`/`--out`/`--timestamps` disk writes, and omits `roots` from `/api/health`) so LAN/mobile clients can only upload media (`512 MiB` cap), download job-scoped artifacts, or use secure-context `window.showDirectoryPicker()` to save outputs into a browser-selected client folder.
- When running on loopback (`localhost`/`127.0.0.1`/`::1`), all local reads, directory browsing (`GET /api/fs/list`), folder creation (`POST /api/fs/mkdir`), and output targets (`--out-dir`, `--out`, `--timestamps`) are uniformly confined by `resolveAllowedPath` (with `~` expansion, deepest-existing-ancestor `filepath.EvalSymlinks` resolution against symlink escapes, and `filepath.Rel` containment) to `allowedRoots`: the workspace directory, user home (`~`), optional `--out-dir`, and repeatable `--allow-dir` roots. Uploaded temp files continue to use fixed non-tainted names (`track_0001.mp3`, `bg_upload.<ext>`), and API endpoints enforce same-origin (`Sec-Fetch-Site`/`Origin`) and strict `application/json` `Content-Type` checks.
- Frontend typography uses pure offline system font stacks (`Georgia`/`system-ui`/`ui-monospace` fallbacks) with zero external network requests so local execution works air-gapped.
- Only one active Mix render executes at a time (`409 Conflict` if busy), retaining up to 5 completed jobs for download and draining active render goroutines (`sync.WaitGroup`) before cleaning up all temporary session/job workspaces on shutdown.
