# Archived Plans

| Plan | Outcome | Purpose |
|------|---------|---------|
| [ytmix-cli-implementation.md](ytmix-cli-implementation.md) | Complete (Nov 16, 2026) | End-to-end implementation of the zero-dependency Go `ytmix` CLI across all 5 pipeline steps, 3 rendering paths, unit tests, and automated FFmpeg E2E verification. |
| [ci-security-workflows.md](ci-security-workflows.md) | Complete (Nov 16, 2026) | Codebase & security audit (`G204`, `G404`, `G306`, `G104`), `.golangci.yml` v2, `.github/workflows/{ci,security}.yml`, `.github/dependabot.yml`, and PR #6 verification for Issue #2. |
| [editorial-mix-studio-ui.md](editorial-mix-studio-ui.md) | Complete (Nov 16, 2026) | Zero-dependency Editorial Mix Studio v3 web & mobile UI (`ytmix ui`) via Go `net/http`, `embed.FS`, and SSE `[1/5]`–`[5/5]` telemetry for Issue #5. |
| [output-location-picker.md](output-location-picker.md) | Complete (Nov 16, 2026) | Output directory & filename picker across `ytmix` CLI (`--out-dir`, `--out`, `--timestamps`, auto `mkdir -p`) and Editorial Mix Studio (`allowedRoots`, `/api/fs/*`, shared Folder Browser modal, LAN `showDirectoryPicker()`) for Issue #9. |
| [structured-telemetry.md](structured-telemetry.md) | Complete (Nov 19, 2026) | Replaced `stdout` `[N/5]` scraping (`sseLogWriter`) with `mixer.TelemetrySink`, standardized `context.Context` runners across `probe`/`audio`/`render`/`mixer`, added `ExecStreamRunner` (`ffmpeg -progress pipe:1`), TTY `\r` status output, and throttled SSE `"progress"` events for Issue #10. |
