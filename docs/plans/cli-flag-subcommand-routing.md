# Consolidate CLI FlagSets, Subcommand Routing, and Unified Help

**Origin:** Nov 27, 2026 — [Issue #14](https://github.com/GCherry/ytmix/issues/14) (`refactor(cli): consolidate flag/subcommand routing and --out-dir ergonomics`), triaged and confirmed with user via `/coordinator`

Eliminate duplicate `flag.FlagSet` construction across `ytmix`, `ytmix ui`, and `ytmix mcp`, extract shared path/output flag registration (`--audio-dir`, `--bg`, `--out-dir`, `--out`, `--timestamps`), add unified `--help` / `help [command]` subcommand discovery, reject unexpected positional arguments consistently across root and `ui`, and extract a testable `run()` entrypoint to raise `cmd/ytmix` test coverage.

## For Future Agents

The plan file is the source of truth — a chat summary is not a handoff. Last
step of every agent, including subagents: mark checkboxes `- [x]`; set phase
status; write **Phase Summary**; record **Verification Plan** results. When all
phases are done, fill **Final Recap** and **Deployment Plan**, then archive this
file to `docs/plans/archive/` per the `real-work` skill (Section 3).

**Repo:** `GCherry/ytmix` (`/home/antigravity/GCherry/ytmix`) — read [AGENTS.md](../../AGENTS.md) before building or testing.

**Branch:** `refactor/cli-flag-subcommand-routing` off `main`.

**Domain docs:** [GLOSSARY.md](../../GLOSSARY.md), [ADR 0002](../adr/0002-embedded-http-ui-and-sse-telemetry.md).

**Related skills:** `architect` (Stage 2 seam check), `tdd` (red-green verification), `code-review` (Standards + Spec), `real-work` (write-back & archival).

## Open Questions

| # | Question | Owner | Resolve by | Blocking? |
|---|----------|-------|------------|-----------|

### Resolved

| # | Question | Resolution | Resolved at |
|---|----------|------------|-------------|
| 1 | Issue #14 Open Design Question: `github.com/alecthomas/kong` (Option A) vs. consolidated stdlib `flag` (Option B)? | User selected **Option B** (consolidated stdlib `flag`, shared path-flag helper, unified `--help`/`help` routing, testable `run()` entrypoint, zero new dependencies). | Nov 27, 2026 04:07:23 PM UTC |
| 2 | Should `ytmix --help` / `ytmix help [command]` document `ui`/`mcp`/`help` and should root/`ui` reject unexpected positional args? | User confirmed **both**: print unified root usage listing `ui`, `mcp`, `help` subcommands and reject unexpected positional arguments on root and `ui` (matching `mcp`), while keeping `ytmix --bg ...` and `ytmix ui ...` backwards compatible. | Nov 27, 2026 04:07:23 PM UTC |

## Decisions (confirmed)

| Topic | Decision |
|---|---|
| Dependency policy | Keep Option B (pure Go stdlib `flag`); add zero new external dependencies and leave `.golangci.yml` `depguard` (`list-mode: strict`) unchanged. |
| Single-`FlagSet` parsing & usage | Construct each `FlagSet` (`ytmix`, `ytmix ui`, `ytmix mcp`) once per invocation; bind `fs.Usage` to `fs.Output()` so `flag.ErrHelp` switches `fs.SetOutput(helpOut)` and prints usage from the same `FlagSet` instead of constructing a second `FlagSet`. |
| Shared path/output flags | Extract a shared helper registering `--audio-dir`, `--bg`, `--out-dir`, `--out`, and `--timestamps` (canonical defaults `./tracks`, `""`, `""`, `output.mp4`, `timestamps.txt`) across root `ytmix` (`mixer.Config`) and `ytmix ui` (`UIConfig`). |
| Subcommand & help routing | Support `ytmix --help` / `-h` (writes unified usage to `stderr`, exit `0`), `ytmix help` / `ytmix help <subcommand>` (`ui`, `mcp`; writes to `stdout`, exit `0`; unknown topic or extra args exit `2`), and preserve `ytmix ui` and `ytmix mcp` routing. |
| Positional argument validation | Reject unexpected positional arguments (`fs.NArg() > 0`) in both `ParseCLIOptions` (root Mix) and `ParseUIFlags` (`ytmix ui`), matching `ParseMCPFlags`. |
| Testable CLI entrypoint | Extract `run(ctx context.Context, args []string, deps cliDeps) int` from `main()` so flag dispatch, help output, `--list-visualizers`, `--estimate`, Mix execution, UI server launch, MCP server launch, and exit codes (`0`/`1`/`2`) are unit-tested without `os.Exit`. |
| Exported helper compatibility | Preserve existing exported signatures (`UIConfig`, `IsUISubcommand`, `NewUIFlagSet`, `ParseUIFlags`, `IsMCPSubcommand`, `NewMCPFlagSet`, `ParseMCPFlags`, `CLIOptions`, `NewFlagSet`, `NewCLIFlagSet`, `NewCLIFlagSetWithOptions`, `ParseFlags`, `ParseCLIFlags`, `ParseCLIOptions`, `RunEstimate`, `PrintVisualizers`). |

## Grill

**Recorded:** Nov 27, 2026 04:08:00 PM UTC

### Design tree

```text
Issue #14 (Consolidate CLI FlagSets, Subcommand Routing, and Unified Help)
├── Triage & Staleness Audit [Confirmed: still needed, partially stale refs updated]
│   ├── #9 (--out-dir) already closed & implemented; share 5 path flags via helper (G2)
│   ├── mcp subcommand + --estimate/--list-visualizers added since #14; include mcp in help (G1)
│   └── main.go line numbers & 33% coverage updated (now 58.6%)
├── Option B Architecture (Stdlib flag) [Confirmed: zero new deps, depguard intact]
│   ├── Single FlagSet per parse+help via fs.Output() & fs.SetOutput(helpOut) (G1)
│   ├── Shared pathFlagTargets registration helper (G2)
│   └── Testable run(ctx, args, cliDeps) int entrypoint (G4)
└── Subcommand, Help & Positional Arg Semantics [Confirmed: help [cmd] + reject NArg>0]
    ├── Unified root Usage listing mix flags + ui, mcp, help
    ├── ParseMCPFlags/ParseUIFlags/ParseCLIOptions return flag.ErrHelp cleanly when helpOut is nil (G3)
    └── Reject unexpected positional args on root & ui while keeping flags-only root Mix intact
```

### Auto-taken (Critical / Low-risk)

| # | Class | Rec | Where applied |
|---|-------|-----|---------------|
| G1 | **Critical** | `NewMCPFlagSet` previously closed over `output` inside `fs.Usage`; switch all `fs.Usage` printers (`ytmix`, `ytmix ui`, `ytmix mcp`) to write to `fs.Output()` so `fs.SetOutput(helpOut); fs.Usage()` works on the single constructed `FlagSet` without creating a second `FlagSet`. | Phase 1A / Decisions |
| G2 | **Low-risk** | Unify registration of the 5 shared Mix/UI path flags (`--audio-dir`, `--bg`, `--out-dir`, `--out`, `--timestamps`) in a package-private `registerPathFlags` helper using canonical glossary terms (`Track`, `Background Visual`, `Mix`, `Chapter Timestamps`) and identical defaults. | Phase 1A / Decisions |
| G3 | **Critical** | Keep `ParseCLIOptions`, `ParseUIFlags`, and `ParseMCPFlags` returning `flag.ErrHelp` silently (`io.Discard`) when called by library/test callers, while internal `parseWithHelp` used by `run()` prints usage once to `stderr` on `flag.ErrHelp` before returning exit code `0`. | Phase 1A / Decisions |
| G4 | **Low-risk** | Add table-driven tests in `cmd/ytmix/main_test.go` covering `run()` across `--help`, `help`, `help ui`, `help mcp`, `help bad`, positional argument errors, unknown flags, `--list-visualizers`, `--estimate`, Mix success/error + TTY newline, UI dispatch, MCP dispatch (`context.Canceled` vs error), and `runUIServer` loopback ephemeral-port startup/shutdown. | Phase 1A |

### Remaining (Other → Open Questions)

_(None — all design questions were resolved with the user prior to plan authoring.)_

## Wave 0: Triage, Staleness Audit & Plan Authoring <!-- serial -->

**Triggered by:** User — Nov 27, 2026 04:07:23 PM UTC

### Phase 0: Triage Issue #14, Lock Scope & Author Plan

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Agent — Nov 27, 2026 04:07:23 PM UTC

- [x] Audit [Issue #14](https://github.com/GCherry/ytmix/issues/14) against `main@a96d22a` (verify stale line numbers, `#9` closure, `58.6%` current coverage, `mcp` addition, and `.golangci.yml` `depguard` policy).
- [x] Resolve Open Design Question (`Option B` consolidated stdlib `flag`) and subcommand/positional-arg scope with the user.
- [x] Create branch `refactor/cli-flag-subcommand-routing` off `main`, author `docs/plans/cli-flag-subcommand-routing.md`, and register in `docs/plans/README.md`.
- [x] Claim and triage [Issue #14](https://github.com/GCherry/ytmix/issues/14) (`needs-triage` -> `ready-for-agent` + `work-started`, assign `@me`, post `Agent Brief` + kickoff comment).

#### Verification Plan

- `git branch --show-current` outputs `refactor/cli-flag-subcommand-routing` — **Passed**.
- `gh issue view 14` shows assignee `GCherry`, labels `enhancement`, `priority: later`, `ready-for-agent`, `work-started`, `Agent Brief`, and kickoff comment (`https://github.com/GCherry/ytmix/issues/14#issuecomment-7222899589`) — **Passed**.

#### Phase Summary

Completed staleness audit against `main@a96d22a`, resolved Issue #14's Open Design Question with the user in favor of Option B (consolidated stdlib `flag`, shared path-flag registration, unified `--help` / `help [command]`, positional-arg rejection on root and `ui`, and a testable `run()` entrypoint), created branch `refactor/cli-flag-subcommand-routing`, claimed Issue #14, and authored this plan.

## Wave 1: CLI Consolidation, Tests & Verification <!-- serial -->

**Triggered by:** User — Nov 27, 2026 04:07:23 PM UTC

### Phase 1A: Consolidate FlagSets, Subcommand Routing, Help & Testable `run()`

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Agent — Nov 27, 2026 04:08:00 PM UTC

- [x] Write failing tests first (`tdd` red-green slices) in `cmd/ytmix/main_test.go` for: unified root `--help` mentioning `ui`, `mcp`, and `help`; `ytmix help`, `ytmix help ui`, `ytmix help mcp`, `ytmix help unknown`; positional arg rejection on root (`ytmix foo`) and `ui` (`ytmix ui foo`); and `run()` dispatch/exit-code paths (`0`, `1`, `2`).
- [x] Extract `registerPathFlags` in `cmd/ytmix/main.go` to deduplicate `--audio-dir`, `--bg`, `--out-dir`, `--out`, and `--timestamps` across `NewUIFlagSet` and `newCLIFlagSet`.
- [x] Eliminate duplicate `FlagSet` creation on `flag.ErrHelp` by routing `parseFlagSet` through a single constructed `FlagSet` whose `Usage` writes to `fs.Output()` and redirects via `fs.SetOutput(helpOut)` on help.
- [x] Add unified root `Usage`, `ytmix ui` `Usage`, `help [subcommand]` routing, and `fs.NArg() > 0` rejection in `ParseCLIOptions` and `ParseUIFlags`.
- [x] Extract `cliDeps` + `run(ctx context.Context, args []string, deps cliDeps) int` from `main()` and add unit tests covering all CLI subcommands, error paths, TTY error newline, and `runUIServer` loopback lifecycle in `cmd/ytmix/main_test.go`.

#### Verification Plan

- `gofmt -l .` — **Passed** (`exit 0`, empty output).
- `go vet ./...` — **Passed** (`exit 0`).
- `go test -count=1 -race -cover ./cmd/ytmix` — **Passed** (`exit 0`, `ok github.com/GCherry/ytmix/cmd/ytmix 1.067s coverage: 90.2% of statements`, up from `58.6%`).
- `go test -race -skip '^TestE2EPipeline$' ./...` — **Passed** (`exit 0`, all 8 packages `cmd/ytmix`, `internal/audio`, `internal/mcp`, `internal/mixer`, `internal/probe`, `internal/render`, `internal/subtitle`, `internal/ui` passed). *(Skipped `TestE2EPipeline` locally because `ffmpeg`/`ffprobe` are not installed on this VM; covered by the CI `e2e` job on PR.)*

#### Phase Summary

Refactored `cmd/ytmix/main.go` to deduplicate the 5 shared path/output flags via `registerPathFlags`, eliminate duplicate `FlagSet` construction on `flag.ErrHelp` via `parseFlagSet` + `fs.Output()`, add unified root/`ui`/`mcp` usage text and `ytmix help [command]` routing, reject unexpected positional arguments in root and `ui`, and extract `cliDeps` + `run(ctx, args, deps) int`. Expanded `cmd/ytmix/main_test.go` to `90.2%` statement coverage with `-race` clean.

### Phase 1B: CI-Parity Lint & Spec Sanity Check

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Agent — Nov 27, 2026 04:08:00 PM UTC

- [x] Check whether `golangci-lint` (`v2.15+`) is available (or install locally if needed) and run `golangci-lint run`.
- [x] Run `go build ./...` and `GOOS=darwin go build ./...` matching `.github/workflows/ci.yml`.
- [x] Verify backwards compatibility of CLI invocations (`ytmix --audio-dir ... --bg ...`, `ytmix ui ...`, `ytmix mcp`) and confirm zero unintended changes outside `cmd/ytmix/` and `docs/plans/`.

#### Verification Plan

- `GOTOOLCHAIN=go1.27.4 go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0 && /go/bin/golangci-lint run` — **Passed** (`exit 0`, `0 issues.` with `golangci-lint v2.15.0` built on `go1.27.4`).
- `go build ./... && GOOS=darwin go build ./... && GOOS=windows go build ./...` — **Passed** (`exit 0`).
- Binary smoke test (`go build -o /tmp/ytmix_check ./cmd/ytmix` with `--help`, `help`, `help ui`, `help mcp`, then removed) — **Passed** (`exit 0`).

#### Phase Summary

Verified CI-parity `golangci-lint v2.15.0` (`0 issues`), cross-OS builds (`linux`, `darwin`, `windows`), and compiled binary help/subcommand output.

## Final Recap

- Consolidated `cmd/ytmix/main.go` using pure Go stdlib `flag` (Option B), eliminating duplicate `FlagSet` creation on `flag.ErrHelp`, extracting shared path/output flag registration (`registerPathFlags`), adding unified root usage (`ui`, `mcp`, `help`) and `ytmix help [command]`, rejecting unexpected positional arguments on root and `ui`, and extracting a dependency-injected `run(ctx, args, deps) int` entrypoint.
- Raised `cmd/ytmix` statement coverage from `58.6%` (and `33.0%` at issue filing) to `90.2%` (`go test -count=1 -race -cover ./cmd/ytmix`), with all unit tests and `golangci-lint v2.15.0` (`0 issues`) passing.

## Deployment Plan

- Open a pull request from `refactor/cli-flag-subcommand-routing` into `main` referencing `Closes #14` (after `Reviewer` 2-axis `code-review`, `Planner` `Acceptance Check`, and `Release` plan archival to `docs/plans/archive/cli-flag-subcommand-routing.md`).
- Confirm GitHub Actions CI (`workflow-lint`, `lint`, `test`, `e2e`) passes cleanly; wait for human merge approval (`Medium` risk tier).

