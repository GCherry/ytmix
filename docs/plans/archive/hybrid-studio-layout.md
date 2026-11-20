# Hybrid Studio Layout Redesign (`A+C`)

**Origin:** Nov 19, 2026 — User design exploration & prototype selection (`A+C Hybrid Studio`: Sticky right `16:9 Stage Preview` + left tabbed `01 Tracks & Sequence` → `02+03 Visuals & Overlays` side-by-side).

Redesign the Editorial Mix Studio frontend (`internal/ui/assets/`) so users can configure **Background Visual** (`02`), **Crossfade** / **Loudness Normalization**, and **Title Overlays** (`03`) side-by-side while keeping the `16:9 Stage Preview` continuously visible without vertical scrolling.

## For Future Agents

The plan file is the source of truth — a chat summary is not a handoff. Last
step of every agent, including subagents: mark checkboxes `- [x]`; set phase
status; write **Phase Summary**; record **Verification Plan** results. When all
phases are done, fill **Final Recap** and **Deployment Plan**, then archive this
file to `docs/plans/archive/hybrid-studio-layout.md` per the `real-work` skill (Section 3).

**Repo:** `ytmix` (`/home/antigravity/GCherry/ytmix`) — read [`AGENTS.md`](../../../AGENTS.md) before building or testing.

**Branch:** `feature/hybrid-studio-layout` off `main`.

**Domain docs:** [`GLOSSARY.md`](../../../GLOSSARY.md), [`docs/adr/0002-embedded-http-ui-and-sse-telemetry.md`](../../adr/0002-embedded-http-ui-and-sse-telemetry.md).

**Related skills:** `coordinator`, `real-work`, `prototype`, `code-review`.

## Architecture Overview

All layout, styling, and client-side state updates live strictly inside the embedded frontend assets (`internal/ui/assets/`) plus static asset assertions in `internal/ui/server_test.go`. Zero Go HTTP routes, pipeline semantics, or CLI flags change:

```text
internal/ui/assets/
├── index.html          # Left column: tabbed [01 Tracks & Sequence] -> [02+03 Visuals & Overlays side-by-side]
│                       # Right column (sticky): 16:9 Stage Preview + Stage Track/Audition bar + Utility tabs (Chapters | Output & CLI | Telemetry) + pinned Render Mix dock
├── styles.css          # Sticky right monitor (.col-right), left workbench tabs (.studio-tabs), dual 02+03 grid (.dual-studio-grid), responsive fallbacks (<=1180px, <=980px, <=640px)
└── app.js              # Left/right tab switching, Stage-adjacent Track selector + Audition sync, auto-switch to Telemetry on render start
internal/ui/server_test.go
                        # Assert new Hybrid Studio DOM IDs and CSS classes in TestStaticAssetsAndHealth
```

## Open Questions

| # | Question | Owner | Resolve by | Blocking? |
|---|----------|-------|------------|-----------|
| _(none — all scope confirmed by user prior to plan creation)_ | | | | |

### Resolved

| # | Question | Resolution |
|---|----------|------------|
| 1 | **Layout selection** | Resolved Nov 19, 2026 07:55:00 PM PST — User selected `A+C Hybrid Studio` from interactive prototypes (`ytmix_stage_layout_prototypes.html`). |
| 2 | **Left workbench tab order** | Resolved Nov 19, 2026 07:55:00 PM PST — User specified `01 Tracks & Sequence` first, followed by `02+03 Visuals & Overlays` side-by-side. |
| 3 | **Initial tab & Stage Track switcher** | Resolved Nov 19, 2026 08:00:00 PM PST — User approved starting on `01 Tracks & Sequence` and keeping the synchronized `▶ Audition` + Track selector directly beneath the `16:9 Stage Preview`. |

## Decisions (confirmed)

| Topic | Decision |
|-------|----------|
| Preserve Colors & Tokens | Keep all existing Dark (Anodized Obsidian) and Light (Studio Paper) CSS custom properties, typography stacks, hairline borders, and `>= 44px` touch targets unchanged. |
| Preserve Existing DOM IDs & API | Keep all existing element IDs (`#tracks-mode-upload`, `#bg-mode-visualizer`, `#crossfade-slider`, `#overlay-toggle`, `#chapters-preview`, `#cli-preview`, `#telemetry-log`, `#render-btn`, `#fs-modal`, etc.) intact so `app.js` and `server_test.go` stay backwards-compatible. |
| Left Tabbed Workbench | Left column (`.col-left`) opens on `01 Tracks & Sequence` (`#left-tab-tracks`), followed by `02+03 Visuals & Overlays` (`#left-tab-studio`) rendering `Background Visual` (`02`) and `Audio & Title Overlays` (`03`) side-by-side on desktop (`>= 1181px`, stacking cleanly below `1180px`). |
| Sticky Right Monitor Deck | Right column (`.col-right`) is `position: sticky; top: 0` (`max-height: 100vh`) on desktop (`> 980px`) with: (1) `16:9 Stage Preview`, (2) synchronized Stage Track selector + `#stage-audition-btn`, (3) utility tabs (`Chapters` \| `Output & CLI` \| `Telemetry`), and (4) pinned `.render-dock` footer containing `#render-btn` and `#download-bar`. |
| Auto-Telemetry Focus | Clicking `#render-btn` switches the right utility dock to the `Telemetry` tab automatically so `[1/5]`–`[5/5]` SSE progress and logs are immediately visible without hiding the `16:9 Stage Preview`. |
| Accessibility & Keyboard Navigation | Use semantic `role="tablist"` / `role="tab"` / `aria-selected` / `aria-controls` / `role="tabpanel"` attributes on both left and right tab bars, matching the existing segmented controls. |

## Grill

**Recorded:** Nov 19, 2026 08:02:00 PM PST

### Design tree

```text
Hybrid Studio Layout (A+C) Design Tree
├── 1. Left Column (01 Tracks -> 02+03 Visuals & Overlays)
│   ├── Tab order: [01 Tracks & Sequence] first, [02+03 Visuals & Overlays] second [Confirmed by User]
│   ├── Dual grid (.dual-studio-grid): 02 Background Visual (1.22fr, 2-col preset grid) + 03 Audio & Title Overlays (0.78fr) [Confirmed]
│   └── Responsive collapse: dual grid stacks at <= 1180px (3-col presets restored); studio-grid stacks at <= 980px [Auto-taken Low-risk — G2]
├── 2. Right Sticky Monitor Deck
│   ├── Sticky viewport lock (top: 0; max-height: 100vh; overflow-y: auto) keeps Stage visible while scrolling left [Confirmed]
│   ├── Stage-adjacent Track selector (#stage-track-select) + #stage-audition-btn stay synced with 01 Tracks & Title Overlay [Auto-taken Critical — G1]
│   ├── Utility tabs: Chapters | Output & CLI | Telemetry; auto-focus Telemetry on startRender() [Auto-taken Low-risk — G3]
│   └── Pinned .render-dock footer keeps Render Mix CTA & #download-bar accessible from any utility tab [Confirmed]
└── 3. Verification & ADR-0002 Compliance
    ├── Maintain zero external fonts/network dependencies and preserve all existing server_test.go assertions [Auto-taken Critical — G4]
    └── Extend TestStaticAssetsAndHealth + run go vet / go test ./... [Confirmed]
```

### Auto-taken (Critical / Low-risk)

| # | Class | Rec | Where applied |
|---|-------|-----|---------------|
| G1 | Critical | Do not duplicate `#audition-btn` ID; keep `#audition-btn` in `01 Tracks` and add synchronized `#stage-audition-btn` + `#stage-track-select` under the Stage so both controls mirror `state.auditioning` and `state.selectedTrackIdx` cleanly. | Decisions / Phase 1 |
| G2 | Low-risk | Use a 2-column `.preset-grid` inside the `02+03` side-by-side split on `>= 1181px` viewports (reverting to 3-col at `<= 1180px` and 2-col at `<= 640px`) so preset cards never truncate inside the narrower `02` sub-column. | Phase 1 (`styles.css`) |
| G3 | Low-risk | Automatically activate the `Telemetry` right utility tab when `startRender()` begins so `[1/5]`–`[5/5]` SSE telemetry and completion `#download-bar` appear immediately. | Decisions / Phase 1 (`app.js`) |
| G4 | Critical | Preserve all existing DOM IDs, CSS class selectors asserted by `internal/ui/server_test.go`, and air-gapped system font stacks per `ADR-0002`. | Decisions / Phase 1 |

### Remaining (Other → Open Questions)

_(none)_

## Wave 0: Planning & Branch Setup <!-- serial -->

**Triggered by:** User — Nov 19, 2026 08:00:00 PM PST

### Phase 0: Lock Hybrid Studio Plan
Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** User — Nov 19, 2026 08:00:00 PM PST

- [x] Create `feature/hybrid-studio-layout` branch off `main`.
- [x] Author `docs/plans/hybrid-studio-layout.md` with `## Grill` and verified `internal/ui/server_test.go` selectors.
- [x] Register active plan in `docs/plans/README.md`.

#### Verification Plan

- `git branch --show-current` outputs `feature/hybrid-studio-layout`.
- `docs/plans/hybrid-studio-layout.md` contains `## Grill`, `## Decisions (confirmed)`, and runnable verification commands.

#### Phase Summary

Created `feature/hybrid-studio-layout`, authored `docs/plans/hybrid-studio-layout.md` with full design-tree grill and ADR-0002/test compatibility checks, and registered it in `docs/plans/README.md`.

## Wave 1: Hybrid Studio UI Implementation & Verification <!-- serial -->

**Triggered by:** User — Nov 19, 2026 08:02:00 PM PST

### Phase 1: Implement `A+C` Hybrid Layout (`index.html`, `styles.css`, `app.js`, `server_test.go`)
Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** User — Nov 19, 2026 08:02:00 PM PST

- [x] Restructure `internal/ui/assets/index.html` into `.col-left` tabbed workbench (`01 Tracks & Sequence` first, `02+03 Visuals & Overlays` side-by-side second) and sticky `.col-right` monitor deck (`16:9 Stage Preview`, Stage Track/Audition bar, `Chapters` / `Output & CLI` / `Telemetry` utility tabs, pinned `.render-dock`).
- [x] Update `internal/ui/assets/styles.css` with `.studio-tabs-bar`, `.dual-studio-grid`, sticky `.col-right`, `.stage-track-bar`, `.utility-tabs`, `.render-dock`, and responsive breakpoints (`<= 1180px`, `<= 980px`, `<= 640px`) preserving all Dark/Light palette tokens and `>= 44px` touch targets.
- [x] Update `internal/ui/assets/app.js` to wire left/right tab switching (`setLeftTab`, `setRightTab`), bidirectional `#stage-track-select` + `#stage-audition-btn` synchronization, `#left-tracks-badge` / `#left-tab-duration` updates, and auto-switching to `Telemetry` on `startRender()`.
- [x] Update `internal/ui/server_test.go` (`TestStaticAssetsAndHealth`) to assert the new Hybrid Studio HTML/CSS/JS hooks alongside existing assertions.

#### Verification Plan

- `node` JS syntax & DOM ID check on `internal/ui/assets/app.js` against `index.html`: passed (`JS syntax OK & all 112 DOM IDs verified in index.html`).
- `gofmt -l .`: clean (no unformatted Go files).
- `go vet ./...`: passed with zero warnings.
- `go build ./cmd/ytmix`: succeeded.
- `go test -v ./internal/ui/...`: all 11 UI test suites passed (`0.025s`).
- `go test ./...`: passed `cmd/ytmix`, `internal/audio`, `internal/probe`, `internal/render`, `internal/subtitle`, `internal/ui`; `internal/mixer` `TestE2EPipeline` hit pre-existing Debian 12 `ffmpeg 5.1.9` muxing duration deltas in untouched `Test_2` (`8.667s`), `Test_3` (`10.100s`), `Test_5` (`18.500s`).

#### Phase Summary

Implemented the `A+C` Hybrid Studio layout across `internal/ui/assets/{index.html,styles.css,app.js}` and extended `TestStaticAssetsAndHealth` in `internal/ui/server_test.go`. Verified JS syntax, 112 DOM ID bindings, `go vet ./...`, binary compilation, and all `internal/ui` tests.

## Final Recap

- **What shipped:** Redesigned `ytmix ui` into the `A+C` Hybrid Studio layout on `feature/hybrid-studio-layout`:
  - Left column (`1.24fr`) uses a workflow-ordered tab bar: **`01 Tracks & Sequence`** first, followed by **`02+03 Visuals & Overlays`** rendering `Background Visual` and `Audio & Title Overlays` side-by-side (`>= 1181px`, stacking cleanly below `1180px`).
  - Right column (`0.76fr`) is sticky (`position: sticky; top: 0; max-height: 100vh`) with the `16:9 Stage Preview`, a synchronized **Audition + Track Selector** bar directly below the Stage, tabbed **`Chapters`** / **`Output & CLI`** / **`Telemetry`** panels, and a pinned **`.render-dock`** footer (`Render Mix` + download bar).
  - Kept all Dark (Anodized Obsidian) and Light (Studio Paper) tokens, `>= 44px` touch targets, `ADR-0002` offline font stacks, and existing element IDs intact.

## Deployment Plan

- Embedded assets are compiled directly into the `ytmix` binary via `//go:embed assets` (`internal/ui/server.go`); no separate asset build or migration step is required.
- Review and merge `feature/hybrid-studio-layout` into `main` (and run `go build ./cmd/ytmix` or `ytmix ui` locally to visually inspect the live studio).
