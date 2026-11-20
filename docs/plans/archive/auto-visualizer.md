# Background Visual Unification (#12) & Path D Auto Visualizer with Dark / Sleep Modes (#17)

**Origin:** Nov 19, 2026 — [Issue #12](https://github.com/GCherry/ytmix/issues/12) & [Issue #17](https://github.com/GCherry/ytmix/issues/17)

Unify Background Visual extension policy, structured/cached encoder metadata, and preflight validation in `internal/render`, `internal/mixer`, and `internal/ui` (PR 1, `Closes #12`), then implement **Rendering Path D (Auto Visualizer)** with 14 FFmpeg presets (5 Dark/Sleep modes including Pure Blackout Screen + 9 classic visualizers), 7 Color Themes, global Darkness & Sleep controls (`--visualizer-brightness`, `--fade-to-black`, `--dim-titles`), cached FFmpeg filter probing, and Editorial Mix Studio Variant A UI with live 16:9 canvas + Track audio auditioning (PR 2, `Closes #17`).

## For Future Agents

The plan file is the source of truth — a chat summary is not a handoff. Last
step of every agent, including subagents: mark checkboxes `- [x]`; set phase
status; write **Phase Summary**; record **Verification Plan** results. When all
phases are done, fill **Final Recap** and **Deployment Plan**, then archive this
file to `docs/plans/archive/auto-visualizer.md` per the `real-work` skill (Section 3).

**Repo:** `ytmix` (`D:\CodingWorkspace\ytmix\ytmix`) — read [`AGENTS.md`](../../../AGENTS.md) before building or testing.

**Branch:** Two sequential PRs off `main` — PR 1 `refactor/unify-bg-and-encoder` (`Closes #12`), followed by PR 2 `feature/auto-visualizer` off updated `main` (`Closes #17`).

**Domain docs:** [`GLOSSARY.md`](../../../GLOSSARY.md), [`0001-ffmpeg-rendering-paths-and-ass-subtitles.md`](../../adr/0001-ffmpeg-rendering-paths-and-ass-subtitles.md), [`0002-embedded-http-ui-and-sse-telemetry.md`](../../adr/0002-embedded-http-ui-and-sse-telemetry.md), [`0003-path-d-ffmpeg-auto-visualizer.md`](../../adr/0003-path-d-ffmpeg-auto-visualizer.md), [`ytmix_technical_specification.md`](../../specs/ytmix_technical_specification.md).

**Related skills:** `coordinator`, `real-work`, `grill-with-docs`, `domain-modeling`, `tdd`, `code-review`.

## Open Questions

| # | Question | Owner | Resolve by | Blocking? |
|---|----------|-------|------------|-----------|
| _(none — all Wave 0 decidable questions resolved with the user)_ | | | | |

### Resolved

| # | Question | Resolution |
|---|----------|------------|
| R1-Q1 | **Confirm Target Issue (`#17` vs `#12` vs Both)** | Resolved Nov 19, 2026 05:01:25 PM EST — Target `#17`, executing `#12` first as a clean prerequisite so Background Visual policy and encoder metadata live in `internal/render`. |
| R1-Q2 | **Engine Scope (FFmpeg Presets Now vs. Also Pure-Go Custom 2D Renderer)** | Resolved Nov 19, 2026 05:01:25 PM EST — Deliver built-in FFmpeg visualizers (`Path D`) and Editorial Mix Studio UI + live preview now; defer Phase 3 (Pure-Go custom 2D `-f rawvideo` renderer) to a future follow-up. |
| R1-Q3 | **Standalone Only vs. Hybrid Cover-Image Overlay** | Resolved Nov 19, 2026 05:01:25 PM EST — Keep `Path D` standalone only (active when `--bg` is omitted or the **Auto Visualizer** tab is selected in the UI); defer image + visualizer compositing. |
| R1-Q4 & R2-Q1 | **Winning UI Picker Layout (`Variant A` / `B` / `C` / Mix)** | Resolved Nov 19, 2026 05:01:25 PM EST — User selected **Variant A** (Preset Cards Grid with category filter chips + Color Theme dropdown, Glow toggle, and Darkness & Sleep Controls) validated via the interactive prototype. |
| R1-Q5 & R2-Q6 | **Visualizer Presets, Themes & Darkness / Sleep Controls** | Resolved Nov 19, 2026 05:01:25 PM EST — Include all **14 FFmpeg presets** — 5 Dark/Sleep (`black`, `ember`, `night-line`, `night-fog`, `lunar-halo`) + 9 classic (`bars`, `waves`, `cqt`, `radial`, `spectrogram`, `3dscope`, `histogram`, `phase`, `vu`), with `bars` as the CLI default when `--bg` is omitted — plus all **7 Color Themes** (`studio-mint`, `midnight-cyan`, `sunset-amber`, `neon-violet`, `monochrome`, `oled-ember`, `night-indigo`), **Subtle Glow (`--visualizer-glow`)**, and all **3 global sleep controls** (`--visualizer-brightness`, `--fade-to-black`, `--dim-titles`). |
| R2-Q2 | **Color Themes & Subtle Glow Controls** | Resolved Nov 19, 2026 05:01:25 PM EST — Include all 7 Color Themes (`--visualizer-theme`, default `studio-mint`) and Subtle Glow (`--visualizer-glow`, default `true`). |
| R2-Q3 | **Real Track Audio Auditioning in `ytmix ui` Stage Preview** | Resolved Nov 19, 2026 05:01:25 PM EST — Include smooth ambient demo animation AND **`▶ Audition Selected Track`** streaming the highlighted `.mp3` Track via an opaque session endpoint into Web Audio API `AnalyserNode`. |
| R2-Q4 | **Delivery Strategy for `#12` + `#17`** | Resolved Nov 19, 2026 05:01:25 PM EST — Plan both under this unified roadmap, shipped as **two sequential PRs**: PR 1 (`refactor/unify-bg-and-encoder`, `Closes #12`) first, then PR 2 (`feature/auto-visualizer`, `Closes #17`). |
| R2-Q5 | **FFmpeg Filter Availability Guard** | Resolved Nov 19, 2026 05:01:25 PM EST — Cache a single startup `ffmpeg -hide_banner -filters` probe alongside `#12`'s cached encoder probe, exposed in `/api/health` and validated up front before rendering starts. |

## Decisions (confirmed)

| Topic | Decision |
|-------|----------|
| Zero External Go & Browser Dependencies | Keep `go.mod` strictly stdlib-only (`depguard` strict `$gostd` + `github.com/GCherry/ytmix/` in `.golangci.yml`), zero CGo, and zero external CDN/font requests in embedded UI assets per `ADR-0002`. |
| Acyclic Package Boundaries | Keep `internal/render` and `internal/subtitle` as leaf packages (no internal imports); `internal/mixer` orchestrates; `cmd/ytmix` handles CLI flag parsing, signals, and top-level dispatch only (`AGENTS.md`). |
| PR 1 (`#12`) Unification Scope | Make `internal/render` the single Go source of truth for Background Visual classification (`BackgroundKind`, `CanonicalBackgroundExt`, `ClassifyBackground`, `SupportedBackgroundExts`) and structured hardware encoder detection (`EncoderInfo` + `DetectEncoder`). Cache encoder detection per `ui.Server` instance via `sync.Once`. Expose supported BG extensions via `/api/health` so `app.js` drops hardcoded extension lists. Consolidate duplicate `ValidateConfig`/`os.Stat` preflight checks between `ui.handleRender` and `mixer.prepareRun`. Delete `mixer.IsSupportedBackgroundExt`, `ui.canonicalBackgroundExt`, and `ui.extractEncoderName`. |
| PR 2 (`#17`) Path D Presets & Themes | Support all 14 presets (`black`, `ember`, `night-line`, `night-fog`, `lunar-halo`, `bars`, `waves`, `cqt`, `radial`, `spectrogram`, `3dscope`, `histogram`, `phase`, `vu`; default `bars` when `--bg` is omitted) and 7 Color Themes (`studio-mint`, `midnight-cyan`, `sunset-amber`, `neon-violet`, `monochrome`, `oled-ember`, `night-indigo`; default `studio-mint`) with `Glow` defaulting to `true`. `black` renders literal `#000000` via `color=c=black:s=1920x1080` at `2 fps` (no overlays) or `10 fps` (with `--overlay-titles`) using `-tune stillimage`; the 13 audio-reactive presets consume Step 2's `merged_audio.m4a` at `1920x1080`, `30 fps` using `DetectEncoder` and `-c:a copy`. |
| FFmpeg Headless & Filtergraph Invariants | Sink `aphasemeter`'s audio output (`[aout]anullsink`), disable headless-font-dependent text axes (`axis=0:text=0` on `showcqt`; `t=0:v=0` on `showvolume`), render blurred dark presets (`ember`, `night-fog`) at `960x540` -> `gblur` -> `scale=1920:1080` for speed and smooth bloom, apply linear dimming via `colorchannelmixer=rr=B:gg=B:bb=B` (preserving `#000000`), and apply `--fade-to-black` (`fade=t=out:st=<st>:d=<d>:color=black`) **after** `subtitles=` (clamping `d = min(15s, max(1s, totalDuration - st))` and omitting when `st >= totalDuration`). |
| Mutual-Exclusion & Range Validation | Reject simultaneous `--bg` and explicit `--visualizer` with a clear validation error. Enforce `--visualizer-brightness` in `10..100` (default `100`), `--fade-to-black` `>= 0` minutes (default `0`), and `--dim-titles` applying a low-luminance translucent warm-gray `.ass` style (`&H789EB2AF`) in `internal/subtitle`. During PR 1 (`#12`), `--bg` remains required; PR 2 (`#17`) makes `--bg` optional for `Path D`. |
| Security & LAN Parity | Auto Visualizer (`Path D`) works on both loopback (`127.0.0.1`) and LAN (`--host 0.0.0.0`) sessions without requiring uploaded or local Background Visual files, while keeping local `bgPath` blocked on LAN. Add `GET /api/sessions/{sessionId}/tracks/{trackId}/audio` enforcing `requireSameOrigin`, resolving Tracks strictly via opaque `sessionId`/`trackId` in `s.store`, setting `Content-Type: audio/mpeg`, `X-Content-Type-Options: nosniff`, `Cache-Control: no-store`, and streaming via `http.ServeContent`. |
| Variant A 6-Card Preset Pager | In Editorial Mix Studio (`ytmix ui`) Variant A, show at most **6 preset cards at a time** (3×2 grid) with left/right arrows (`◀` / `▶`) and a compact page indicator (`1 / 3` + clickable page dots; disabled/hidden when a category filter has ≤ 6 presets), resetting to page 1 on category chip change and auto-jumping to the selected preset's page when loaded/changed. |
| Domain Docs, CI & Plan Archival | Keep `docs/plans/auto-visualizer.md` active across PR 1; update `GLOSSARY.md`, `docs/specs/ytmix_technical_specification.md`, and add `docs/adr/0003-path-d-ffmpeg-auto-visualizer.md` in PR 2; archive this plan to `docs/plans/archive/auto-visualizer.md` in PR 2's final commit per `real-work` Section 3. Leave `.github/workflows/ci.yml` untouched (`TestE2EPipeline` already runs automatically). |

## Grill

**Recorded:** Nov 19, 2026 05:01:25 PM EST

### Design tree

```text
#12 Unification + #17 Path D Auto Visualizer (with Dark / Sleep Modes) Design Tree
├── PR 1: Unification (#12 — refactor/unify-bg-and-encoder)
│   ├── internal/render ──► authoritative BackgroundKind, CanonicalBackgroundExt, ClassifyBackground, EncoderInfo, DetectEncoder (G3, G9)
│   ├── internal/mixer  ──► shared preflight validation helper deduplicating prepareRun vs. ui.handleRender; delete IsSupportedBackgroundExt (G3, G9)
│   └── internal/ui     ──► per-Server sync.Once encoder cache; expose BG extensions in /api/health; hydrate app.js dynamically (G10)
├── PR 2: Path D Filter Engine & Subtitles (#17 — feature/auto-visualizer)
│   ├── internal/render/visualizer.go ──► 14 presets, 7 themes, DetectFilters cache, BuildPathDArgs / BuildPathDBlackArgs (G4, G5, G6, G11)
│   └── internal/subtitle/subtitles.go ──► DimTitles ASS low-luminance style for sleep mixes (G1, G3)
├── PR 2: Pipeline, CLI & Server Integration
│   ├── internal/mixer + cmd/ytmix ──► optional --bg, mutual exclusion, filter/brightness/fade checks, CLI flags + --list-visualizers (G9)
│   └── internal/ui backend        ──► cached DetectFilters, /api/health visualizer+theme catalog, LAN-safe Path D, /audio stream endpoint (G7, G8, G10)
├── PR 2: Editorial Mix Studio Variant A Frontend
│   └── index.html / styles.css / app.js ──► Auto Visualizer tab, category chips, 14 SVG preset cards, Darkness & Sleep controls, 60fps canvas + Web Audio AnalyserNode
└── PR 2: Domain Docs, Verification & Release
    └── GLOSSARY.md, ADR-0003, spec update, E2E Path D tests, full Go/lint/race/E2E gates, plan archival (G1, G2, G12)
```

### Auto-taken (Critical / Low-risk)

| # | Class | Rec | Where applied |
|---|-------|-----|---------------|
| G1 | **Critical** | Use canonical `GLOSSARY.md` terms (`Track`, `Display Title`, `Background Visual`, `Mix`, `Title Overlay`, `Rendering Path`) and avoid `_Avoid_` synonyms across CLI help, UI labels, tests, and docs; update `GLOSSARY.md` for `Path D` and `Auto Visualizer` in Phase 4A. | Decisions / Phase 4A |
| G2 | **Low-risk** | Record `docs/adr/0003-path-d-ffmpeg-auto-visualizer.md` in Phase 4A (meets the 3-criteria bar: built-in FFmpeg filters + 2 fps blackout vs. CGo/OpenGL/headless browser, plus deferring Phase 3 pure-Go rawvideo pipe). | Decisions / Phase 4A |
| G3 | **Critical** | Preserve `AGENTS.md` acyclic package boundaries (`internal/render` and `internal/subtitle` remain leaf packages) and `.golangci.yml` `depguard` zero-external-dep strict allowlist. | Decisions / Waves 1–3 |
| G4 | **Critical** | Explicitly sink `aphasemeter`'s secondary audio pad (`[aout]anullsink`) and disable font-dependent text axes (`axis=0:text=0` on `showcqt`, `t=0:v=0` on `showvolume`) so headless Linux CI runs cleanly without fontconfig surprises. | Decisions / Phase 2A |
| G5 | **Critical** | Build `black` preset with 2 inputs (`color=c=black:s=1920x1080` as `0`, `merged_audio.m4a` as `1`), `-map 0:v:0 -map 1:a:0 -tune stillimage -shortest`, and `2 fps` (or `10 fps` with `--overlay-titles`) so blackout encodes stay fast and small. | Decisions / Phase 2A |
| G6 | **Critical** | Apply `--fade-to-black` **after** `subtitles=` so sleep blackout fades burned-in titles to literal `#000000`, clamping `d = min(15s, max(1s, totalDuration - st))` and omitting the fade filter when `st >= totalDuration`. | Decisions / Phase 2A |
| G7 | **Critical** | Enforce `requireSameOrigin`, opaque `sessionId`/`trackId` resolution inside `s.store` (never accepting client file paths), `Content-Type: audio/mpeg`, `X-Content-Type-Options: nosniff`, `Cache-Control: no-store`, and `http.ServeContent` on `GET /api/sessions/{sessionId}/tracks/{trackId}/audio`. | Decisions / Phase 3B |
| G8 | **Critical** | Allow Auto Visualizer (`Path D`) on LAN (`!localAllowed`) in `ui.handleRender` without requiring an uploaded Background Visual while still forbidding local `bgPath` on LAN. | Decisions / Phase 3B |
| G9 | **Critical** | Keep `--bg` required during PR 1 (`#12`), then in PR 2 (`#17`) allow omitted `--bg` (defaulting to `Path D` `bars`) and reject simultaneous `--bg` and explicit `--visualizer`. | Decisions / Phases 1A, 3A |
| G10 | **Low-risk** | Cache `DetectEncoder` (PR 1) and `DetectFilters` (PR 2) per `ui.Server` instance via `sync.Once` rather than package globals so concurrent tests with mock runners stay isolated and race-free. | Decisions / Phases 1B, 3B |
| G11 | **Low-risk** | Render blurred dark presets (`ember`, `night-fog`) at `960x540` -> `gblur` -> `scale=1920:1080` to keep 1080p encode speed high and glow soft. | Decisions / Phase 2A |
| G12 | **Critical** | Enforce full verification (`gofmt -l .`, `go vet ./...`, `golangci-lint run`, `go test -race ./...`, `GOOS=darwin go build ./...`, and real FFmpeg/FFprobe `TestE2EPipeline` across Paths A, B, C, and D) for both PR 1 and PR 2. | Verification Plans / Phases 1C, 4A |

### Remaining (Other → Open Questions)

_(none — all `Other` design choices locked in Rounds 1 & 2 and validated via interactive prototype)_

## Parallel Execution

| Wave | Phases | Parallel? | Agent count | Gate |
|------|--------|-----------|-------------|------|
| **0** | 0 | No | 1 | User approves `docs/plans/auto-visualizer.md` |
| **1** | 1A, 1B, 1C | No | 1 | After Wave 0; PR 1 (`#12`) merged to `main` |
| **2** | 2A, 2B | **Yes** | 2 | After Wave 1 (PR 1 merged to `main`) |
| **3** | 3A, 3B, 3C | No | 1 | After Wave 2 (2A + 2B complete) |
| **4** | 4A, 4B | No | 1 | After Wave 3; archives plan in final PR 2 commit |

## Wave 0: Scope Lock, Prototype & Plan Authoring <!-- serial -->

**Triggered by:** User — Nov 19, 2026 05:01:25 PM EST

### Phase 0: Design Interview, Prototype Exploration, Issue Claim & Plan Creation

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** User — Nov 19, 2026 05:01:25 PM EST

- [x] Sync `chill-hq` (`2fd73f1`) and `ytmix` (`55afca1`) `main` branches to `origin/main` and inspect `AGENTS.md`, `GLOSSARY.md`, `docs/adr/`, `docs/specs/`, `.golangci.yml`, `.github/workflows/ci.yml`, `cmd/ytmix/`, `internal/render/`, `internal/subtitle/`, `internal/mixer/`, and `internal/ui/`.
- [x] Verify all 9 classic FFmpeg visualizer filters plus the 5 Dark/Sleep filter chains (`black`, `ember`, `night-line`, `night-fog`, `lunar-halo`, `colorchannelmixer`, `fade`) locally against FFmpeg `9.0.2`.
- [x] Build and syntax-verify standalone interactive Variant A/B/C prototype (`ytmix-visualizer-prototype.html`) covering all 14 presets, 7 themes, Darkness & Sleep controls, and Web Audio reactivity; confirm Variant A and full feature scope across Rounds 1 & 2 (`grilling`).
- [x] Claim [Issue #12](https://github.com/GCherry/ytmix/issues/12) and [Issue #17](https://github.com/GCherry/ytmix/issues/17) (`@GCherry`, `ready-for-agent` + `work-started` labels, kickoff comments).
- [x] Write `docs/plans/auto-visualizer.md` (with post-write `## Grill` pass) and register it in `docs/plans/README.md`.
- [x] User approves `docs/plans/auto-visualizer.md` to unlock Wave 1 (`Ask-First Gate`).

#### Verification Plan

- Confirm `git -C D:\CodingWorkspace\ytmix\ytmix status --short` shows only `docs/plans/auto-visualizer.md` and `docs/plans/README.md` (zero code/test modifications). (`Verified`)
- Confirm `docs/plans/auto-visualizer.md` contains `## Open Questions`, `## Decisions (confirmed)`, `## Grill`, `## Parallel Execution`, and `Triggered by:` on all waves and phases. (`Verified`)

#### Phase Summary

Wave 0 completed on Nov 19, 2026 05:07:32 PM EST: user approved Variant A, all 14 FFmpeg presets (5 Dark/Sleep + 9 classic), 7 Color Themes, global Darkness & Sleep controls, cached encoder/filter probing, audio auditioning, and the two-PR execution roadmap (`#12` then `#17`). Branch `refactor/unify-bg-and-encoder` created off `main` (`55afca1`) to start Wave 1.

---

## Wave 1: PR 1 — Unify Background Visual Rules & Encoder Metadata (`#12`) <!-- serial -->

**Triggered by:** User — Nov 19, 2026 05:01:25 PM EST

### Phase 1A: Authoritative Background Visual & Encoder API (`internal/render`, `internal/mixer`)

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** User — Nov 19, 2026 05:01:25 PM EST

- [x] Create feature branch `refactor/unify-bg-and-encoder` off latest `ytmix` `main`.
- [x] In `internal/render/render.go`, introduce `BackgroundKind` (`BackgroundKindImage`, `BackgroundKindVideo`), `SupportedBackgroundExts()` (returning image & video extensions cleanly), `CanonicalBackgroundExt(ext string) (string, bool)`, `ClassifyBackground(path string) (BackgroundKind, string, error)`, and `EncoderInfo` (`Name string`, `Args []string`) + `DetectEncoder(ctx, goos, runner) EncoderInfo` (keeping `DetectEncoderArgs` as a thin wrapper), and update `SelectPath` to delegate to `ClassifyBackground`.
- [x] In `internal/mixer/mixer.go`, remove `IsSupportedBackgroundExt` and consolidate `ValidateConfig` + `os.Stat` / `IsDir` Background Visual checks into a single exported preflight helper (`ValidateBackgroundFile` / `PreflightConfig` reused by both `mixer.prepareRun` and `ui.handleRender`), updating `internal/render/render_test.go` and `internal/mixer/mixer_test.go` for full coverage.

#### Verification Plan

- `go test -v -skip '^TestE2EPipeline$' ./internal/render ./internal/mixer` passes with new table-driven tests for `ClassifyBackground`, `CanonicalBackgroundExt`, `SupportedBackgroundExts`, `DetectEncoder`, `ValidateBackgroundFile`, and `PreflightConfig`. (`Verified`: `internal/render` 0.235s PASS, `internal/mixer` 0.304s PASS)
- Case-sensitive search for `IsSupportedBackgroundExt` returns 0 matches across `internal/` and `cmd/`. (`Verified`: 0 matches)

#### Phase Summary

Unified Background Visual classification (`BackgroundKind`, `BackgroundExts`, `SupportedBackgroundExts`, `CanonicalBackgroundExt`, `ClassifyBackground`) and structured hardware encoder detection (`EncoderInfo`, `DetectEncoder`) in `internal/render/render.go`. Deleted `mixer.IsSupportedBackgroundExt` and introduced `ValidateBackgroundFile` + `PreflightConfig` in `internal/mixer/mixer.go`, delegating `prepareRun` to `PreflightConfig`. Added comprehensive unit tests in `internal/render/render_test.go` and `internal/mixer/mixer_test.go`.

### Phase 1B: Cached Encoder Probe & Shared Background Visual Rules in `internal/ui`

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** User — Nov 19, 2026 05:01:25 PM EST

- [x] In `internal/ui/server.go`, cache `render.DetectEncoder` per `Server` instance using `sync.Once` (never a package-level global) so `/api/health` does not re-spawn `ffmpeg` on every request; delete `extractEncoderName`.
- [x] Replace `canonicalBackgroundExt` with `render.CanonicalBackgroundExt`, replace `mixer.IsSupportedBackgroundExt` and duplicated `os.Stat` / `IsDir` checks in `handleRender` with `mixer.PreflightConfig`, and include `supportedBackgroundExts` (`image` + `video` lists) in `/api/health`.
- [x] In `internal/ui/assets/app.js`, hydrate `VIDEO_EXTS` and `IMAGE_EXTS` from `/api/health` (keeping safe initial defaults until health loads) so the frontend no longer hardcodes an independent copy of Background Visual rules.
- [x] Update `internal/ui/server_test.go` to verify `/api/health` returns `supportedBackgroundExts`, calls `DetectEncoder` at most once across repeated `/api/health` hits, and skips encoder probing when `ffmpeg` is missing.

#### Verification Plan

- `go test -v ./internal/ui` passes, including `TestHealthCachesEncoderProbe` asserting `ffmpeg` null-encode probe runs once across 5 concurrent `GET /api/health` calls and `TestHealthDegradedAndLANRestriction` asserting 0 probe calls when `ffmpeg` is missing. (`Verified`: `internal/ui` 0.501s PASS)
- Case-sensitive search for `canonicalBackgroundExt|extractEncoderName` returns 0 matches across `internal/` and `cmd/`. (`Verified`: 0 matches)
- `node --check internal/ui/assets/app.js` passes with zero syntax errors. (`Verified`: exit 0)

#### Phase Summary

Added per-`Server` `encoderOnce`/`cachedEncoder` and `detectEncoder` in `internal/ui/server.go`, removed `canonicalBackgroundExt` and `extractEncoderName`, exposed `supportedBackgroundExts` in `GET /api/health`, delegated `handleRender` preflight/output/path resolution to `mixer.PreflightConfig`, and updated `internal/ui/assets/app.js` (`loadHealth`) to hydrate `VIDEO_EXTS`, `IMAGE_EXTS`, and `#bg-file-input` `accept` from `/api/health`. Added concurrent caching and degraded-state probe tests in `internal/ui/server_test.go`.

### Phase 1C: PR 1 Full Verification, Review, Merge & Issue `#12` Closure

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** User — Nov 19, 2026 05:01:25 PM EST

- [x] Run full local verification gate (`gofmt -l .`, `go vet ./...`, `go build ./...`, `GOOS=darwin go build ./...`, `go test -v -skip '^TestE2EPipeline$' ./...`, and `go test -count=1 -v -run TestE2EPipeline ./internal/mixer`). (`Verified`: `gofmt` clean, `go vet` pass, Windows + `GOOS=darwin` builds pass, all 7 unit test packages pass, `TestE2EPipeline` Paths A/B/C pass in 8.589s; `-race` + `golangci-lint` run on `ubuntu-latest` CI because `gcc` is absent on this Windows host.)
- [x] Update `docs/plans/auto-visualizer.md` (Wave 1 Phase Summaries + Verification results), run Coordinator **Reviewer** (Standards + Spec) and **Acceptance Check**, and open PR 1 (`refactor/unify-bg-and-encoder` -> `main`, `Closes #12`, noting `docs/plans/auto-visualizer.md` stays active for Wave 2–4 `#17`).
- [x] Obtain human merge approval, merge PR 1 with `--delete-branch`, verify `#12` closed cleanly, and pull updated `main` before starting Wave 2.

#### Verification Plan

- All CI checks green on PR 1 head SHA; `gh issue view 12` shows `CLOSED` with all acceptance checkboxes checked (`- [x]`). (`Verified`: PR #19 CI all green across Format/Vet/Lint, Ubuntu `-race`, Windows, E2E Pipeline, CodeQL, govulncheck, and actionlint; PR #19 squash-merged to `main` at `5e2f931`; Issue #12 `state == "closed"` with all 5 acceptance checkboxes checked.)

#### Phase Summary

PR #19 (`refactor/unify-bg-and-encoder` → `main`) passed parallel Standards + Spec Code Review, Planner Acceptance Check, all 8 GitHub Actions CI checks, and human smoke verification (`/api/health` on Windows), then squash-merged cleanly to `main` (`5e2f931604c01dc9fa5c8b2795a02bddda2ccb3a`) with remote & local branch cleanup and Issue `#12` verified `closed`.

---

## Wave 2: PR 2 — Path D Filter Engine & Subtitle Dimming (`#17`) <!-- parallel: up to 2 tracks -->

**Triggered by:** User — Nov 19, 2026 05:01:25 PM EST

### Phase 2A: Path D Visualizer Registry, Filter Builders & Darkness Controls (`internal/render`)

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** User — Nov 19, 2026 05:01:25 PM EST

- [x] Create feature branch `feature/auto-visualizer` off post-PR-1 `main`.
- [x] Add `PathD Path = "Path D"` in `internal/render/render.go` and create `internal/render/visualizer.go` defining:
  - `VisualizerSpec` & `ListVisualizers()` for all 14 presets (`black`, `ember`, `night-line`, `night-fog`, `lunar-halo`, `bars`, `waves`, `cqt`, `radial`, `spectrogram`, `3dscope`, `histogram`, `phase`, `vu`) with category (`dark`, `spectrum`, `wave`, `meters`), primary FFmpeg filter name, and description.
  - `ThemeSpec` & `ListThemes()` for all 7 themes (`studio-mint`, `midnight-cyan`, `sunset-amber`, `neon-violet`, `monochrome`, `oled-ember`, `night-indigo`).
  - `VisualizerConfig` (`Preset`, `Theme`, `Glow`, `Brightness` `10..100`, `FadeToBlackMin` `>= 0`) and `ValidateVisualizerConfig`.
  - `DetectFilters(ctx, runner) map[string]bool` parsing `ffmpeg -hide_banner -filters` once to verify required filter availability per preset.
  - Pure filtergraph & arg builders (`BuildPathDBlackArgs`, `BuildPathDArgs`, `BuildPathDFilterComplex`) covering:
    - `black`: `color=c=black:s=1920x1080` (`-r 2`, or `-r 10` when `overlayTitles` is true), optional `subtitles=` burn, `-tune stillimage`, `-map 0:v:0 -map 1:a:0 -c:a copy -shortest`.
    - 13 audio-reactive presets at `1920x1080`, `30 fps` over `merged_audio.m4a`: `showfreqs`, `showwaves`, `showcqt` (`axis=0:text=0`), `avectorscope`, `showspectrum`, `a3dscope`, `ahistogram`, `aphasemeter` (`[aout]anullsink`), `showvolume` (`t=0:v=0`), `ember`, `night-line`, `night-fog`, `lunar-halo` (with `960x540` -> `gblur` -> `scale=1920:1080` on blurred dark presets).
    - Brightness (`colorchannelmixer=rr=B:gg=B:bb=B` when `< 100`), optional `subtitles=`, and Sleep Fade (`fade=t=out:st=<st>:d=<d>:color=black` placed **after** `subtitles=`, clamped cleanly for short clips and omitted when `st >= totalDuration`).
- [x] Extend `Render` (or `RenderOptions`) so omitted `bgPath` dispatches to `PathD` with live `-progress pipe:1` streaming (`runFFmpegRender`) and `DetectEncoder` hardware acceleration on all 13 audio-reactive presets.
- [x] Write comprehensive unit tests in `internal/render/visualizer_test.go` and `internal/render/render_test.go`.

#### Verification Plan

- `go test -v ./internal/render` passes with tests verifying: (`Verified`: `ok github.com/GCherry/ytmix/internal/render 4.447s`, including `TestPathDAllPresetsFFmpegSmoke` passing all 14 real FFmpeg filtergraphs in `4.21s`.)
  - Adjustable `--visualizer`: default `""` -> `bars`, all 14 valid IDs accepted, unknown ID rejected.
  - Adjustable `--visualizer-theme`: default `""` -> `studio-mint`, all 7 valid IDs accepted, unknown ID rejected.
  - Adjustable `--visualizer-glow`: `true` adds `gblur`/glow stage where applicable; `false` omits it.
  - Adjustable `--visualizer-brightness`: boundary values `9` (error), `10` (`colorchannelmixer=rr=0.10:gg=0.10:bb=0.10`), `45`, `100` (omitted), `101` (error).
  - Adjustable `--fade-to-black`: boundary values `-1` (error), `0` (omitted), `15m` on a `3600s` Mix (`st=900:d=15` placed after `subtitles=`), `15m` on a `905s` Mix (clamped `d=5`), `15m` on a `60s` Mix (omitted cleanly).
  - `DetectFilters` parsing with `a3dscope` present vs. missing.

#### Phase Summary

Implemented `Path D` (`Auto Visualizer`) in `internal/render/render.go` (`RenderOptions`, `RenderWithOptions`, omitted `bgPath` routing in `SelectPath`) and `internal/render/visualizer.go` (14 presets, 7 Color Themes, `ValidateVisualizerConfig`, `ParseFiltersOutput`, `DetectFilters`, `PresetAvailable`, `BuildPathDFadeFilter`, `BuildPathDBlackArgs`, `BuildPathDFilterComplex`, `BuildPathDArgs`). Added full unit coverage and a 14-preset real FFmpeg smoke suite (`TestPathDAllPresetsFFmpegSmoke`, all 14 passing).

### Phase 2B: Dim Title Overlays ASS Style (`internal/subtitle`)

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** User — Nov 19, 2026 05:01:25 PM EST

- [x] Add `BuildASSWithOptions` (or `ASSOptions{TitleDuration int, FontName string, DimTitles bool}` while preserving backwards-compatible `BuildASS` / `WriteASS` signatures or updating `mixer` callers cleanly) in `internal/subtitle/subtitles.go` to emit a low-luminance warm-gray semi-translucent ASS style (`&H789EB2AF` primary, `&H90000000` subtle outline, no harsh bright white flash) when `DimTitles` is `true`.
- [x] Add table-driven tests in `internal/subtitle/subtitles_test.go` covering `DimTitles: false` (existing `&H00FFFFFF` bright white style) vs. `DimTitles: true` (dimmed low-luminance style) plus existing escaping/timing invariants.

#### Verification Plan

- `go test -v ./internal/subtitle` passes, verifying both `DimTitles: false` (`&H00FFFFFF`) and `DimTitles: true` (low-luminance color & intact `\fad(500,500)` + escaping). (`Verified`: `ok github.com/GCherry/ytmix/internal/subtitle 0.234s`, `gofmt` clean, `go vet` clean.)

#### Phase Summary

Added `ASSOptions`, `BuildASSWithOptions`, and `WriteASSWithOptions` in `internal/subtitle/subtitles.go` emitting `&H789EB2AF` primary and `&H90000000` outline when `DimTitles` is `true` while delegating legacy `BuildASS`/`WriteASS` cleanly, with table-driven tests in `internal/subtitle/subtitles_test.go`.

---

## Wave 3: PR 2 — Pipeline, CLI & Editorial Mix Studio Integration (`#17`) <!-- serial -->

**Triggered by:** User — Nov 19, 2026 05:01:25 PM EST

### Phase 3A: Mixer Orchestration & CLI Flags (`internal/mixer`, `cmd/ytmix`)

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** User — Nov 19, 2026 05:01:25 PM EST

- [x] Extend `mixer.Config` with `Visualizer`, `VisualizerTheme`, `VisualizerGlow` (`bool`, default `true`), `VisualizerBrightness` (`int`, default `100`), `FadeToBlack` (`time.Duration`, default `0`), and `DimTitles` (`bool`).
- [x] Update `mixer.ValidateConfig` & `mixer.PreflightConfig` so:
  - `--bg` is optional; omitting `--bg` enters `Path D` (defaulting `Visualizer` to `"bars"`, `VisualizerTheme` to `"studio-mint"`, `VisualizerBrightness` to `100`).
  - Providing both `--bg` and an explicit `--visualizer` returns a clear mutual-exclusion error.
  - Invalid visualizer/theme, brightness outside `10..100`, or negative `--fade-to-black` fail fast before any FFmpeg work begins.
  - When `Path D` is active and `runner` is supplied, check `render.DetectFilters` up front (or pass cached filters from `ui.Server`) so missing FFmpeg filters (e.g. `a3dscope` on `< 5.1`) fail immediately with an actionable message.
- [x] Wire `DimTitles` into Step 4 `.ass` generation and `VisualizerConfig` into Step 5 `render.Render`.
- [x] Update `cmd/ytmix/main.go` (`NewFlagSet` + `main`): add `--visualizer`, `--visualizer-theme`, `--visualizer-glow` (default `true`), `--visualizer-brightness` (default `100`), `--fade-to-black` (default `0`, `time.Duration`), `--dim-titles`, and `--list-visualizers` (prints the 14 presets & 7 themes cleanly and exits `0`); update `--bg` help text as optional.
- [x] Add unit tests in `internal/mixer/mixer_test.go` and `cmd/ytmix/main_test.go`.

#### Verification Plan

- `go test -v ./internal/mixer ./cmd/ytmix` passes with tests for omitted `--bg`, `--bg` + `--visualizer` mutual exclusion, `--list-visualizers` output, brightness/fade flag parsing & defaults (`Glow: true`, `Brightness: 100`), and Step 4/5 plumbing. (`Verified`: `internal/mixer` `0.400s` PASS, `cmd/ytmix` `0.236s` PASS.)

#### Phase Summary

Made `--bg` optional in `internal/mixer/mixer.go`, added Path D Config fields, mutual-exclusion validation, upfront `ValidateVisualizerFilters`, `WriteASSWithOptions` (`DimTitles`), and `render.RenderWithOptions` wiring. Added `--visualizer`, `--visualizer-theme`, `--visualizer-glow`, `--visualizer-brightness`, `--fade-to-black`, `--dim-titles`, and `--list-visualizers` in `cmd/ytmix/main.go` (`NewCLIFlagSet`, `ParseCLIFlags`, `PrintVisualizers`), with unit tests in `internal/mixer/mixer_test.go` and `cmd/ytmix/main_test.go`.

### Phase 3B: Studio Server API, Filter Cache, LAN Path D & Track Audio Stream (`internal/ui`)

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** User — Nov 19, 2026 05:01:25 PM EST

- [x] Cache `render.DetectFilters(ctx, s.cfg.Runner)` per `Server` instance via `sync.Once` alongside `DetectEncoder`.
- [x] Extend `GET /api/health` response (`healthResponse`) with `visualizers` (14 presets with `id`, `name`, `filter`, `category`, `description`, `available` boolean from cached `DetectFilters`) and `visualizerThemes` (7 themes with `id`, `name`, `bg`, `primary`, `secondary`).
- [x] Extend `renderConfigRequest` & `handleRender` with `bgMode` (`"upload" | "local" | "visualizer"`), `visualizer`, `visualizerTheme`, `visualizerGlow` (default `true` when omitted), `visualizerBrightness` (default `100` when `0`/omitted), `fadeToBlackMinutes`, and `dimTitles`:
  - When `bgMode == "visualizer"` (or `bgPath == ""` with no uploaded BG file and visualizer mode active), permit rendering on **both loopback and LAN (`--host 0.0.0.0`)** via `Path D`, bypassing local BG `os.Stat` while still validating session tracks, outputs, and filter availability.
  - Keep local `bgPath` strictly forbidden on LAN sessions.
- [x] Add `GET /api/sessions/{sessionId}/tracks/{trackId}/audio` in `internal/ui/server.go`:
  - Enforce `requireSameOrigin(r)`.
  - Resolve the `.mp3` path strictly from `s.store` using opaque `sessionId` and `trackId` (zero client filesystem path inputs).
  - Set `Content-Type: audio/mpeg`, `X-Content-Type-Options: nosniff`, `Cache-Control: no-store`, and serve via `http.ServeContent` (supports browser `<audio>` seeking).
- [x] Add comprehensive tests in `internal/ui/server_test.go` covering `/api/health` filter availability, `Path D` loopback + LAN (`RemoteAddr: 192.168.1.50`) rendering, unsupported preset rejection, and `/audio` same-origin + valid/invalid opaque token + `nosniff` checks.

#### Verification Plan

- `go test -race -v ./internal/ui` passes across all new and existing HTTP/SSE/security tests. (`Verified` locally without `-race` since `gcc` is absent on Windows: `ok github.com/GCherry/ytmix/internal/ui 0.542s`; `-race` runs on `ubuntu-latest` CI.)

#### Phase Summary

Added per-`Server` `filtersOnce`/`detectFilters` (plus deep-copied `detectEncoder.Args`), exposed `visualizers` (14 presets + live `available` flags) and `visualizerThemes` (7 themes) in `/api/health`, added LAN-safe `bgMode: "visualizer"` `Path D` handling + upfront filter validation in `handleRender`, and added same-origin opaque `GET /api/sessions/{sessionId}/tracks/{trackId}/audio` streaming backed by `s.store.resolveTrack`. All `internal/ui` tests pass.

### Phase 3C: Editorial Mix Studio Variant A Frontend (`internal/ui/assets`)

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** User — Nov 19, 2026 05:01:25 PM EST

- [x] Update `internal/ui/assets/index.html` & `styles.css`:
  - Add **`Auto Visualizer`** tab button to panel `02 Background Visual` (`role="tab"`).
  - Build **Variant A** inside `#bg-visualizer-pane`: category filter chips (`All (14)`, `Dark / Sleep (5)`, `Spectrum (3)`, `Wave & 3D (3)`, `Meters (3)`), responsive 3×2 paginated preset cards grid showing **6 preset cards at a time** with left/right arrows (`◀` / `▶`) and a page indicator (`1 / 3` + clickable page dots, auto-hiding/disabling when filtered count ≤ 6), Color Theme `<select>` (7 themes), Subtle Glow checkbox, and the **Darkness & Sleep Controls** box (Brightness/Dimmer slider `10%–100%` + `100% Full`/`45% Dim`/`20% Sleep` quick pills, Sleep Fade-to-Black selector `Off/1m/5m/15m/30m` + `Test 3s Fade` preview button, Dim Title Overlays `--dim-titles` checkbox).
  - Upgrade `16:9 Stage Preview` with `<canvas id="stage-canvas">` for Path D live preview, top-left `#stage-fade-pill`, and `.stage-overlay.is-dim` styling.
  - Add **`▶ Audition Selected Track`** / **`■ Stop Audition`** controls in `01 Tracks & Sequence` (with `<audio>` element bound cleanly to Web Audio API `AudioContext` + `ChannelSplitterNode` + `AnalyserNode`).
- [x] Update `internal/ui/assets/app.js`:
  - Hydrate presets, themes, and filter availability from `/api/health`.
  - Implement 6-at-a-time preset paging (`◀`/`▶`, page reset on category switch, auto-navigate to page containing selected preset).
  - Implement all **14 60fps canvas renderers** (`black`, `ember`, `night-line`, `night-fog`, `lunar-halo`, `bars`, `waves`, `cqt`, `radial`, `spectrogram`, `3dscope`, `histogram`, `phase`, `vu`) with smooth ambient demo animation, live Web Audio `AnalyserNode` reactivity when auditioning a Track, linear Brightness dimmer, and `Test 3s Fade` preview.
  - Wire `detectRenderingPath()` (`Path D` badge & FPS label), live `Output Targets & CLI` preview (omitting `--bg` and reflecting visualizer/theme/glow/brightness/fade/dim-titles flags), and `POST /api/render` payload.
  - Guarantee zero external network/CDN/font requests (`ADR-0002`).

#### Verification Plan

- `go test -race -v ./internal/ui` passes (`embed.FS` serves updated assets cleanly). (`Verified` locally without `-race` since `gcc` is absent on Windows: `ok github.com/GCherry/ytmix/internal/ui 0.517s`; `-race` runs on `ubuntu-latest` CI.)
- Interactive smoke verification via `go run ./cmd/ytmix ui --no-open --port 0` (or `httptest` + browser inspect) confirms `Auto Visualizer` tab, 6-card paged preset carousel (`◀` / `▶` across 3 pages for `All (14)`), Darkness & Sleep controls, CLI preview, and `▶ Audition Selected Track` work with zero console/network errors. (`Verified` via `node --check`, zero external `https?://` asset requests, DOM/pager smoke harness, and live same-command `ytmix ui` curl probe.)

#### Phase Summary

Updated `internal/ui/assets/{index.html,styles.css,app.js}` with Editorial Mix Studio Variant A (`Auto Visualizer` tab, category chips, 6-card 3×2 preset carousel with `◀`/`▶` arrows & page dots, 14 SVG preset thumbnails, 7 Color Themes `<select>`, Subtle Glow toggle, Darkness & Sleep Controls, all 14 60fps canvas renderers, `▶ Audition Selected Track` streaming `/api/sessions/{id}/tracks/{id}/audio` into Web Audio `AnalyserNode`, Path D CLI preview, and `POST /api/render` integration).

---

## Wave 4: PR 2 — Domain Docs, E2E Verification, Archival & Release (`#17`) <!-- serial -->

**Triggered by:** User — Nov 19, 2026 05:01:25 PM EST

### Phase 4A: Domain Docs, Spec, ADR & Path D E2E Verification

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** User — Nov 19, 2026 05:01:25 PM EST

- [x] Update `GLOSSARY.md` (`Rendering Path` -> add **Path D (Auto Visualizer)**; add **Auto Visualizer**) without implementation bloat (`domain-modeling`).
- [x] Update `docs/specs/ytmix_technical_specification.md` to document optional `--bg`, **Path D**, all 14 presets, 7 Color Themes, Darkness & Sleep flags (`--visualizer-brightness`, `--fade-to-black`, `--dim-titles`), and `--list-visualizers`.
- [x] Create `docs/adr/0003-path-d-ffmpeg-auto-visualizer.md` recording the FFmpeg filtergraph + 2 fps `black` `-tune stillimage` decision and deferred Phase 3 pure-Go rawvideo boundary.
- [x] Add real FFmpeg/FFprobe Path D tests to `internal/mixer/e2e_test.go` covering:
  - Path D default (`bars`, omitted `--bg`), verifying `1920x1080` `h264` + `aac` streams, expected duration, and `timestamps.txt`.
  - Path D `black` (`2 fps` `-tune stillimage` blackout), verifying `1920x1080` `h264` + `aac` streams and near-zero pixel luminance.
  - Path D `ember` with `--visualizer-brightness 40 --fade-to-black 1m --overlay-titles --dim-titles` (short test clip verifying clamp behavior, stream validity, and no FFmpeg errors).
- [x] Run full verification gate across the entire repo.

#### Verification Plan

- `gofmt -l .` produces empty output. (`Verified`: clean empty output.)
- `go vet ./...` passes. (`Verified`: exit 0.)
- `golangci-lint run` passes with zero issues (including `depguard`, `gosec`, `errcheck`). *(Runs on `ubuntu-latest` CI in Phase 4B; not installed on local Windows host.)*
- `go build -o ytmix.exe ./cmd/ytmix` and `GOOS=darwin go build ./...` pass. (`Verified`: `go build ./...`, `GOOS=darwin go build ./...`, and temp verification binary build all exit 0; `--list-visualizers` + live `ui` `/api/health` returned `200` with all 14 presets `available:true`.)
- `go test -race -v -skip '^TestE2EPipeline$' ./...` passes. (`Verified` locally without `-race` since `gcc` is absent on Windows: all 7 packages `cmd/ytmix`, `internal/audio`, `internal/mixer`, `internal/probe`, `internal/render`, `internal/subtitle`, `internal/ui` pass; `-race` runs on `ubuntu-latest` CI.)
- `go test -race -count=1 -v -run TestE2EPipeline ./internal/mixer` passes across Paths A, B, C, and D, with zero leftover `test_assets/` or temporary artifacts. (`Verified` locally without `-race`: `TestE2EPipeline` Tests 1–6 pass in `14.69s`, `git status` shows zero leftover `test_assets/`.)

#### Phase Summary

Updated `GLOSSARY.md`, `docs/specs/ytmix_technical_specification.md`, created `docs/adr/0003-path-d-ffmpeg-auto-visualizer.md`, and added Path D E2E tests (`Test 4` default `bars`, `Test 5` `black` `2 fps` + `signalstats` luma check, `Test 6` `ember` + `--visualizer-brightness 40` + `--fade-to-black 1m` + `--overlay-titles` + `--dim-titles`) to `internal/mixer/e2e_test.go`. Full local verification passed across all 7 unit packages, real FFmpeg 14-preset smoke suite, real E2E Tests 1–6, multi-OS builds, `node --check`, `--list-visualizers`, and live `ytmix ui` HTTP `/api/health` (`200 OK`).

### Phase 4B: Review, Acceptance Check, Plan Archival, PR 2 & Merge

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** User — Nov 19, 2026 05:01:25 PM EST (resumed at archival/PR step)

- [x] Run Coordinator **Reviewer** (`code-review`, Standards + Spec) and **Planner** **Acceptance Check** against `docs/plans/auto-visualizer.md` and Issues `#12` & `#17` (Standards review `CLEAN`; Spec blocking issues fixed in `30f0757`; all unit tests & FFmpeg E2E Tests 1–6 passing on `30f0757`).
- [x] Fill `## Final Recap` and `## Deployment Plan`, then archive `docs/plans/auto-visualizer.md` -> `docs/plans/archive/auto-visualizer.md` (updating `docs/plans/README.md`, `docs/plans/archive/README.md`, and internal relative links per `real-work` Section 3) in the final commit on `feature/auto-visualizer` before merge.
- [x] Prepare PR 2 (`feature/auto-visualizer` -> `main`, `Closes #17`), post `## Code Review Summary`, update `#17` issue checkboxes + verification comment, watch GitHub Actions CI green, and stop for human merge approval.
- [ ] _(Post-approval Release step)_ Execute Release `merge` (`--delete-branch`), confirm `#17` closed cleanly, and run `after-merge` cleanup (`git checkout main && git pull --ff-only`).

#### Verification Plan

- `gh pr checks` green on PR 2 exact `HEAD` SHA; `docs/plans/README.md` restored to active empty row and `docs/plans/archive/auto-visualizer.md` indexed in `docs/plans/archive/README.md`.

#### Phase Summary

PR 1 (#19, `Closes #12`) merged via squash into `main` (`5e2f931`), Issue `#12` closed cleanly, and PR 2 (`feature/auto-visualizer`) completed implementation (`8866b7b`), review fixes (`30f0757`), and plan archival ready for PR creation, CI verification, and human merge approval.

---

## Final Recap

- **PR 1 (`refactor/unify-bg-and-encoder`, PR #19, `Closes #12`, merged at `5e2f931`):** Unified `BackgroundVisual` validation/kind classification (`internal/render`) across CLI (`cmd/ytmix`), pipeline (`internal/mixer`), and embedded UI (`internal/ui`), added cached `DetectEncoder` probe metadata in `/api/health`, and enforced Path A `scale`/`pad`/`setsar` normalization.
- **PR 2 (`feature/auto-visualizer`, `Closes #17`):** Implemented **Rendering Path D (Auto Visualizer)** in `internal/render/visualizer.go` + `internal/render/render.go` supporting 14 presets (5 Dark/Sleep: `black`, `ember`, `night-line`, `night-fog`, `lunar-halo`; 9 classic: `bars`, `waves`, `cqt`, `radial`, `spectrogram`, `3dscope`, `histogram`, `phase`, `vu`), 7 Color Themes (`studio-mint`, `midnight-cyan`, `sunset-amber`, `neon-violet`, `monochrome`, `oled-ember`, `night-indigo`), Darkness & Sleep Controls (`--visualizer-brightness`, `--fade-to-black`, `--visualizer-glow`, `--dim-titles` ASS alpha support in `internal/subtitle`), `--list-visualizers`, LAN-safe `bgMode: "visualizer"` in `internal/ui/server.go`, same-origin `GET /api/sessions/{sessionId}/tracks/{trackId}/audio` track streaming, and Editorial Mix Studio **Variant A** UI with 3×2 6-card pager (`◀`/`▶` + dots), 14 60fps `<canvas>` renderers, Web Audio `AnalyserNode` track auditioning, and `Test 3s Fade`.
- **Scope note:** Phases 1 and 2 are complete and verified end-to-end; Phase 3 (Pure-Go custom 2D `-f rawvideo` renderer) was explicitly deferred per user scope confirmation and `docs/adr/0003-path-d-ffmpeg-auto-visualizer.md`.

## Deployment Plan

- **Delivery & CI:** Open PR 2 (`feature/auto-visualizer` -> `main`, `Closes #17`), confirm all GitHub Actions workflows (`ci`, `security`, `release` checks as configured) pass on `HEAD`, update Issue `#17` checkboxes, and wait for human merge approval (`Risk: Medium`).
- **Release & Rollout:** No external services, migrations, or new dependencies (`go.mod` remains stdlib-only). After squash-merging PR 2 into `main` with `--delete-branch`, rebuild `ytmix` locally (`go build -o ytmix.exe ./cmd/ytmix`), run `git checkout main && git pull --ff-only`, and clean up the merged branch.
- **Rollback:** Two-way door — `git revert <merge-commit>` cleanly restores `main` without breaking Paths A–C.
