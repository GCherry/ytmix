# CI, Code Quality, and Security Workflows (`ytmix`)

**Origin:** Nov 16, 2026 — [Issue #2](https://github.com/GCherry/ytmix/issues/2) (`Analyze ytmix codebase and build GitHub Actions for code quality, tests, and vulnerability scanning`).

Audit the `ytmix` Go `1.22+` codebase and implement GitHub Actions workflows, `.golangci.yml`, and `.github/dependabot.yml` to enforce Go formatting, linting, compilation, unit + E2E tests, workflow syntax validation, vulnerability scanning, and least-privilege supply-chain hygiene.

## For Future Agents

The plan file is the source of truth — a chat summary is not a handoff. Last
step of every agent, including subagents: mark checkboxes `- [x]`; set phase
status; write **Phase Summary**; record **Verification Plan** results. When all
phases are done, fill **Final Recap** and **Deployment Plan**, then archive this
file per the real-work skill (Section 3).

**Repo:** `ytmix` (`D:\CodingWorkspace\ytmix\ytmix`) — read [`AGENTS.md`](../../../AGENTS.md) before building or testing.

**Branch:** `feature/ci-security-workflows` off `main` (PR #6, `Closes #2`).

**Domain docs:** [`GLOSSARY.md`](../../../GLOSSARY.md), [`docs/adr/0001-ffmpeg-rendering-paths-and-ass-subtitles.md`](../../adr/0001-ffmpeg-rendering-paths-and-ass-subtitles.md), [`docs/specs/ytmix_technical_specification.md`](../../specs/ytmix_technical_specification.md), [`docs/plans/archive/ytmix-cli-implementation.md`](ytmix-cli-implementation.md).

**Related skills:** `coordinator`, `real-work`, `code-review` (pre-merge).

## Codebase & Security Audit Summary

**Branch & Layout State (verified on `main` at `b67d2ae`, post-PR #4):**
- `feature/ytmix-cli` has already merged into `main` via PR #4 (`b67d2ae`). Both the documentation/specs and the full Go `1.22+` implementation now reside on `main`.
- `go.mod`: `module github.com/GCherry/ytmix`, `go 1.22`, zero external Go dependencies (standard library only).
- Package layout matches `AGENTS.md`: `cmd/ytmix` (`main.go`, `main_test.go`) -> `internal/mixer` (`mixer.go`, `mixer_test.go`, `e2e_test.go`) -> `internal/probe` (`ffprobe.go`, `ffprobe_test.go`), `internal/audio` (`audio.go`, `audio_test.go`), `internal/subtitle` (`chapters.go`, `chapters_test.go`, `subtitles.go`, `subtitles_test.go`), `internal/render` (`render.go`, `render_test.go`).
- Repository visibility: `GCherry/ytmix` is `PUBLIC`, so GitHub Code Scanning / SARIF uploads (`github/codeql-action`) are available natively.

**Security-Relevant Surfaces & Intentional Configurations:**
1. **Subprocess execution (`gosec` G204):** `internal/mixer/mixer.go:54` executes `exec.CommandContext(ctx, name, args...)` inside `ExecCommandRunner`. Per ADR-0001 and `AGENTS.md`, `ytmix` deliberately shells out to `ffmpeg` and `ffprobe` without CGo or shell wrappers (`sh -c`/`cmd /c`). All production callers pass literal `"ffmpeg"` or `"ffprobe"` binary names and discrete argument slices (`internal/probe/ffprobe.go`, `internal/audio/audio.go`, `internal/render/render.go`), and `internal/mixer/e2e_test.go` invokes `ExecCommandRunner` directly with literal binary names. Documented exclusion/annotation for `G204` on `ExecCommandRunner` (rather than disabling `gosec` globally) is required.
2. **Non-cryptographic randomness (`gosec` G404):** `internal/probe/ffprobe.go:6,52` uses `math/rand/v2` (`rand.Shuffle`) for `--shuffle`, and `internal/probe/ffprobe_test.go:66` uses deterministic `rand.New(rand.NewPCG(42, 99))`. Randomness only reorders `.mp3` Track playback sequence and carries zero cryptographic or security impact. Exclude `G404` intentionally with documented rationale in `.golangci.yml` (or scoped `#nosec G404`).
3. **File permissions on `os.WriteFile` (`gosec` G306):** `internal/audio/audio.go:140` (`concat_list.txt` inside `tempDir`), `internal/subtitle/subtitles.go:108` (`titles.ass` inside `tempDir`), and `internal/subtitle/chapters.go:76` (`timestamps.txt` user output) currently pass `0644`. Temporary files inside `os.MkdirTemp("", "ytmix-*")` (`0700` dir) can tighten to `0600`, while user-facing `timestamps.txt` permission (`0644` vs `0600`) is confirmed via `G306` configuration or tight permission update.
4. **Unchecked best-effort error returns (`errcheck` / `gosec` G104):** `internal/mixer/mixer.go:158` runs `defer os.RemoveAll(tempDir)` and lines `164,179,194,203,208,211,217` plus `cmd/ytmix/main.go:53,61` write CLI progress/errors via `fmt.Fprintf(stdout/os.Stderr, ...)`. Configure `errcheck` `exclude-functions` (and `gosec` `G104` handling) or explicitly handle/ignore `os.RemoveAll` cleanly so CI linting is deterministic without noisy boilerplate.
5. **User-supplied filesystem paths (`gosec` G304):** CLI flags (`--audio-dir`, `--bg`, `--out`, `--timestamps`) accept local user paths; production code validates `--bg` extension and `os.Stat` directory vs file (`internal/mixer/mixer.go:83-85,146-152`) and uses `os.ReadDir` / `os.WriteFile`.
6. **FFmpeg filter & concat escaping:** `internal/audio/audio.go:14-17` (`escapeConcatPath`) escapes `\` -> `/` and `'` -> `'\''`; `internal/render/render.go:23-27,46-48` (`EscapeFFmpegFilterPath`) escapes `\` -> `/`, `:` -> `\:`, `'` -> `\'`; `internal/subtitle/subtitles.go:10-23,44-54` escapes `\`, `{`, `}`, and line breaks in `.ass` Display Titles and commas/newlines in `--font-name`.
7. **Windows `gofmt` line-ending pitfall:** Local Windows checkouts with `core.autocrlf=true` convert `.go` files to CRLF on disk, causing `gofmt -l` on Windows to flag every file even when the Git index stores LF. CI must run `gofmt -l .` on `ubuntu-latest` (or enforce `*.go text eol=lf` in `.gitattributes`).

## Open Questions

| # | Question | Owner | Resolve by | Blocking? |
|---|----------|-------|------------|-----------|
| _(none — all Wave 0 questions resolved)_ | | | | |

### Resolved

| # | Question / Item | Resolution |
|---|-----------------|------------|
| 1 | **OS matrix & Windows coverage** | Resolved Nov 16, 2026 03:32:30 PM EST — User accepted recommendation: run `go build ./...` and `go test -v ./...` on both `ubuntu-latest` and `windows-latest` (adding `.gitattributes` `*.go text eol=lf`), with `-race`, `gofmt -l .`, `golangci-lint`, and `actionlint` on `ubuntu-latest`. |
| 2 | **Live FFmpeg E2E in CI (`TestE2EPipeline`)** | Resolved Nov 16, 2026 03:32:30 PM EST — User accepted recommendation: install `ffmpeg` on `ubuntu-latest` in CI so `TestE2EPipeline` (Paths A, B, and C) runs automatically on every PR and push to `main`. |
| 3 | **Security tooling & `G306` (`0644` vs `0600`)** | Resolved Nov 16, 2026 03:32:30 PM EST — User accepted recommendation: run both `gosec` (via `golangci-lint` with documented `G204`/`G404` exclusions) and `.github/workflows/security.yml` (`govulncheck ./...` + CodeQL `go` and `actions` with job-scoped `security-events: write`), and tighten `tempDir` `os.WriteFile` modes (`concat_list.txt`, `titles.ass`) to `0600` while keeping user-facing `timestamps.txt` `0644` with documented `#nosec G306`. |
| 4 | **Third-party action pinning** | Resolved Nov 16, 2026 03:32:30 PM EST — User accepted recommendation: pin all third-party actions to full 40-character commit SHAs with `# vX.Y.Z` comments, kept updated weekly by `.github/dependabot.yml` (`gomod` + `github-actions`). |

## Decisions (confirmed)

| Topic | Decision |
|-------|----------|
| Go Version & Dependencies | Target Go `1.22+` (`go.mod` `go 1.22`); preserve zero external Go runtime dependencies (enforced via `depguard` in `.golangci.yml`). |
| `go.mod` Presence Guard | Detect `go.mod` presence in a lightweight preflight job (`has_go_mod`) and gate Go lint/build/test/vuln/CodeQL-Go jobs on it so branches lacking `go.mod` do not fail trivially. |
| OS Matrix & Windows Verification | Run build + unit tests on both `ubuntu-latest` and `windows-latest`; run `-race`, `gofmt -l .`, `golangci-lint`, `actionlint`, and live `ffmpeg` E2E (`TestE2EPipeline`) on `ubuntu-latest`; enforce `*.go text eol=lf` in `.gitattributes`. |
| Least-Privilege Workflow Permissions | Set top-level `permissions: contents: read` on every workflow; elevate `security-events: write` (and `actions: read`) strictly on the CodeQL SARIF upload jobs. |
| Documented Security Exclusions & File Modes | Never disable `gosec` wholesale; document explicit `#nosec` rationale for `G204` (`ExecCommandRunner` calling hardcoded `"ffmpeg"`/`"ffprobe"`), `G404` (`math/rand/v2` `--shuffle` playback order), and `G306` on user-facing `timestamps.txt` (`0644`), while tightening `tempDir` writes (`concat_list.txt`, `titles.ass`) to `0600`. |
| Architecture & Linter Alignment | `.golangci.yml` v2 aligns with `AGENTS.md` (zero external deps via `depguard`, `gofmt`, `govet`, `errcheck` with explicit `os.RemoveAll`/`fmt.Fprintf`/`fmt.Fprintln` exclusions, `staticcheck`, `gosec`, `unused`, `ineffassign`, `misspell`, `nolintlint`). |
| Security Scanning & Dependabot | Dedicated `.github/workflows/security.yml` running `govulncheck ./...` and GitHub CodeQL (`go` and `actions`) on PR, push to `main`, and weekly schedule, plus `.github/dependabot.yml` (weekly `gomod` and `github-actions`). |
| Third-Party Action Pinning | Pin all third-party actions to full 40-char commit SHAs with `# vX.Y.Z` comments. |
| Workflow Validation | Validate all `.github/workflows/*.yml` files in CI using `actionlint` (`v1.7.12`). |

## Grill

**Recorded:** Nov 16, 2026 02:45:00 PM EST

### Design tree

```text
ytmix CI, Code Quality & Security Workflows Design Tree
├── 1. Branch State & Docs Guard
│   ├── `feature/ytmix-cli` already merged to `main` (b67d2ae); `e2e_test.go` & `internal/render` live on `main` [Verified]
│   └── `hashFiles('go.mod') != ''` guard keeps docs-only branches green [Auto-taken Low-risk — G3]
├── 2. Code Quality & Static Analysis (`.golangci.yml` + `ci.yml`)
│   ├── `gofmt -l .` on `ubuntu-latest` avoids Windows `core.autocrlf` false positives [Auto-taken Critical — G2]
│   ├── `go vet ./...`, `go build ./...`, `golangci-lint`, `actionlint` [Locked]
│   └── OS matrix (`ubuntu-latest` + `windows-latest` vs Linux + `GOOS=windows` cross-build) [Open Question #1]
├── 3. Automated Tests (`go test` + E2E)
│   ├── Unit tests (`go test -race -v ./...` on Linux) via `CommandRunner`/`lookPath` seams [Locked]
│   └── Live `ffmpeg`/`ffprobe` provisioning for `TestE2EPipeline` (Paths A, B, C) [Open Question #2]
└── 4. Security, Vulnerability & Supply-Chain Scanning
    ├── Least-privilege `permissions: contents: read` + job-scoped `security-events: write` [Auto-taken Critical — G1]
    ├── Documented `G204` (`ExecCommandRunner`) & `G404` (`math/rand/v2`) exclusions [Auto-taken Critical — G4]
    ├── `gosec` + CodeQL (`go` & `actions`) + `G306` (`0644` vs `0600`) [Open Question #3]
    ├── Full commit-SHA pinning vs major-tag pinning for third-party actions [Open Question #4]
    └── `govulncheck ./...` (PR/push + weekly schedule) & `.github/dependabot.yml` [Locked]
```

### Auto-taken (Critical / Low-risk)

| # | Class | Rec | Where applied |
|---|-------|-----|---------------|
| G1 | Critical | Enforce top-level `permissions: contents: read` on all GitHub Actions workflows and restrict `security-events: write` strictly to SARIF upload jobs per Issue #2 least-privilege requirement. | Decisions / Wave 1 |
| G2 | Critical | Run `gofmt -l .` on `ubuntu-latest` (and avoid running raw `gofmt -l` on CRLF Windows checkouts) because Windows `core.autocrlf=true` flags every LF-committed `.go` file as unformatted. | Decisions / Wave 1 |
| G3 | Low-risk | Guard Go CI/security steps with `hashFiles('go.mod') != ''` so docs-only branches without `go.mod` do not fail trivially, while accounting for `main` already containing `go.mod` post-PR #4. | Decisions / Wave 1 |
| G4 | Critical | Document explicit rationale for `gosec` `G204` (`ExecCommandRunner` in `internal/mixer/mixer.go:54` invoking literal `"ffmpeg"`/`"ffprobe"`) and `G404` (`math/rand/v2` in `internal/probe/ffprobe.go:52` for `--shuffle`) rather than disabling `gosec` globally. | Audit Summary / Decisions / Wave 1 |

### Remaining (Other → Open Questions)

Mirrored in **Open Questions** (`#1`–`#4` above) and asked to the user at the Wave 0 Gate.

## Wave 0: Codebase Audit, Security Analysis & Plan Lock <!-- serial -->

**Triggered by:** User (Coordinator → Planner) — Nov 16, 2026 02:45:00 PM EST

### Phase 0: Codebase Audit, Security Analysis & CI/Security Plan

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Agent (Planner) — Nov 16, 2026 02:45:00 PM EST

- [x] Audit `main` (`b67d2ae`, post-PR #4 merge of `feature/ytmix-cli`), `go.mod`, all Go packages/tests, `AGENTS.md`, `GLOSSARY.md`, `docs/adr/0001-ffmpeg-rendering-paths-and-ass-subtitles.md`, and `docs/specs/ytmix_technical_specification.md`.
- [x] Audit security-relevant patterns (`os/exec` in `ExecCommandRunner`, `math/rand/v2` in `DiscoverAudioFiles`, `os.WriteFile` modes, `defer os.RemoveAll`, FFmpeg/ASS escaping, and Windows `core.autocrlf` vs `gofmt`).
- [x] Author this `real-work` plan in `docs/plans/ci-security-workflows.md`, update `docs/plans/README.md`, and complete the post-write `## Grill` pass.
- [x] Claim Issue #2 (`@GCherry`, `work-started` label, kickoff comment linking this plan and `feature/ci-security-workflows`).
- [x] Resolve Open Questions #1–#4 with the user and obtain Wave 0 approval before starting Wave 1.

#### Verification Plan

- Verified `docs/plans/ci-security-workflows.md` and `docs/plans/README.md` use canonical `GLOSSARY.md` terms without `_Avoid_` synonyms.
- Verified Issue #2 has `work-started` label, `GCherry` assignee, and kickoff comment (`#issuecomment-6927219152`).
- Confirmed all 4 Open Questions were resolved by user (`take recommendations`) on Nov 16, 2026 03:32:30 PM EST and recorded in `### Resolved` and `## Decisions (confirmed)`.

#### Phase Summary

Completed Nov 16, 2026 03:32:30 PM EST by Agent (Planner). Audited `main` (`b67d2ae`), documented `gosec` (`G204`, `G404`, `G306`), `errcheck`, and Windows `gofmt` findings, claimed Issue #2, and locked all Wave 0 decisions.

## Wave 1: Linter Config, CI & Security Workflows, and Dependabot <!-- serial -->

**Triggered by:** User (Wave 0 approval) — Nov 16, 2026 03:32:30 PM EST

### Phase 1: `.golangci.yml`, GitHub Actions Workflows, Dependabot & Intentional Annotations

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Agent (Engineering) — Nov 16, 2026 03:32:30 PM EST

- [x] Add `.golangci.yml` tailored to `ytmix`'s Go `1.22+` zero-dependency codebase (`depguard`, `errcheck`, `gosec`, `govet`, `ineffassign`, `misspell`, `nolintlint`, `staticcheck`, `unused`) with documented exclusions/configurations for `errcheck` (`os.RemoveAll`, `fmt.Fprintf`, `fmt.Fprintln`), `gosec` (`G104` deferred to `errcheck` with rationale), and `_test.go` `gosec` exclusions.
- [x] Apply Wave-0-approved code hygiene fixes: `.gitattributes` (`*.go`/`*.yml`/`*.yaml`/`*.sh` `eol=lf`), `0600` temp-file permissions in `internal/audio/audio.go` (`concat_list.txt`) and `internal/subtitle/subtitles.go` (`titles.ass`), and documented `#nosec` annotations on `internal/mixer/mixer.go:54` (`G204`), `internal/probe/ffprobe.go:52` (`G404`), and `internal/subtitle/chapters.go:76` (`G306`).
- [x] Create `.github/workflows/ci.yml` covering:
  - Triggers on `pull_request` (`main`), `push` (`main`), and `workflow_dispatch`, with `permissions: contents: read` and `concurrency` cancellation.
  - `detect` job (`has_go_mod`) guarding Go jobs so branches lacking `go.mod` skip cleanly.
  - `workflow-lint` (`actionlint@v1.7.12`), `lint` (`gofmt -l .`, `go vet ./...`, `golangci-lint@v2.15.0` via SHA-pinned `golangci/golangci-lint-action`), `test` (`ubuntu-latest` + `windows-latest` matrix with `-race` + coverage summary on Linux and `GOOS=darwin` cross-build check), and `e2e` (`ffmpeg` apt install + `TestE2EPipeline` across Paths A, B, C).
- [x] Create `.github/workflows/security.yml` covering:
  - Triggers on `pull_request` (`main`), `push` (`main`), weekly `schedule` (`0 8 * * 1`), and `workflow_dispatch`, with top-level `permissions: contents: read`.
  - `govulncheck ./...` (`golang/govulncheck-action` SHA-pinned, guarded on `has_go_mod`).
  - `codeql-actions` and `codeql-go` (`github/codeql-action` SHA-pinned, job-scoped `security-events: write`, `actions: read`).
- [x] Create `.github/dependabot.yml` with weekly update schedules for `gomod` and `github-actions`.

#### Verification Plan

- `gofmt -w D:\CodingWorkspace\ytmix\ytmix` & `gofmt -l D:\CodingWorkspace\ytmix\ytmix`: exited `0` with 0 unformatted files.
- `go -C D:\CodingWorkspace\ytmix\ytmix vet ./...`: exited `0` with 0 warnings.
- `go -C D:\CodingWorkspace\ytmix\ytmix build ./...` (Windows native, `GOOS=linux`, `GOOS=darwin`): all 3 exited `0`.
- `go -C D:\CodingWorkspace\ytmix\ytmix test -count=1 -v ./...`: exited `0` across all 6 packages (`cmd/ytmix` `0.346s`, `internal/audio` `0.358s`, `internal/mixer` `8.518s` including live `TestE2EPipeline` Paths A/B/C in `8.17s`, `internal/probe` `0.317s`, `internal/render` `0.286s`, `internal/subtitle` `0.311s`).
- `golangci-lint` and `actionlint` are not installed locally; remote execution on the PR in Wave 2 will verify both tools directly on GitHub Actions.

#### Phase Summary

Completed Nov 16, 2026 03:37:30 PM EST by Agent (Engineering) on `feature/ci-security-workflows`. Added `.gitattributes`, `.golangci.yml` (v2 schema), `.github/workflows/ci.yml`, `.github/workflows/security.yml`, and `.github/dependabot.yml` with full commit-SHA action pinning and least-privilege `permissions`; tightened temp-file permissions (`0600`) in `audio.go`/`subtitles.go`; and added documented `#nosec` rationale comments (`G204`, `G404`, `G306`) in `mixer.go`, `ffprobe.go`, and `chapters.go`. Ready for `Reviewer` (`code-review`), `Planner` (`Acceptance Check`), and `Release` (`pre-merge` + live CI verification) in Wave 2.

## Wave 2: PR Verification, Issue Sync & Plan Archival <!-- serial -->

**Triggered by:** Wave 1 completion — Nov 16, 2026 03:38:00 PM EST

### Phase 2: Code Review, Live GitHub Actions CI Verification & Closure

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Agent (Release & Coordinator) — Nov 16, 2026 03:51:30 PM EST

- [x] Complete `Reviewer` (`code-review` Standards + Spec) and `Planner` (`Acceptance Check`) on `feature/ci-security-workflows`.
- [x] Push `feature/ci-security-workflows` and open PR targeting `main` with `Closes #2`; verify all GitHub Actions checks (`ci.yml`, `security.yml`, `actionlint`, `golangci-lint`, `govulncheck`, tests) pass green on the PR.
- [x] Update all `- [ ]` acceptance checkboxes on Issue #2 to `- [x]` and post PR + verification summary comment.
- [x] Fill **Final Recap** and **Deployment Plan**, then archive `docs/plans/ci-security-workflows.md` → `docs/plans/archive/ci-security-workflows.md` (updating `docs/plans/README.md`, `docs/plans/archive/README.md`, and internal relative links) in the final pre-merge commit on `feature/ci-security-workflows`.

#### Verification Plan

- `Planner` `Acceptance Check` (`0e2c499`): `PASS` (0 gaps).
- `Reviewer` Round 1 (`0e2c499`): 1 blocking finding (`gosec` `G104` handling) -> fixed in `25c3c76`.
- `/code-review` Round 2 (`25c3c76`, Claude Opus 5.5 High): `Standards`: `PASS (0 blocking)`; `Spec`: `PASS (0 blocking)`. Full Code Review Summary posted to PR #6 (`#issuecomment-6928949924`).
- Live CI fixes (`e193ef8`) verified locally (`gofmt -l`, `go vet ./...`, `go build ./...`, `go test -count=1 -short ./...` all exited `0`) and on GitHub Actions PR #6 (`e193ef8`): all 11 checks passed (`Validate Workflows (actionlint)`, `Detect Go Module` x2, `Format, Vet & Lint`, `Build & Unit Tests (ubuntu-latest)`, `Build & Unit Tests (windows-latest)`, `FFmpeg E2E Pipeline (Paths A, B, C)`, `Go Vulnerability Scan (govulncheck)`, `CodeQL`, `CodeQL (GitHub Actions)`, `CodeQL (Go)`).
- `docs/plans/README.md` restored to empty active plans and `docs/plans/archive/README.md` indexes `ci-security-workflows.md`.

#### Phase Summary

Completed Nov 16, 2026 03:51:30 PM EST by Agent (`Release` / `Coordinator`) on `feature/ci-security-workflows` (`PR #6`). Resolved Round 1 `G104` finding (`25c3c76`), passed Round 2 `/code-review` on Claude Opus 5.5 High (`0` blocking findings), resolved live CI findings (`e193ef8` — cross-platform `escapeConcatPath`, `gosec` `G602`, `staticcheck` `ST1005`), verified all 11 GitHub Actions checks green on PR #6, posted the Code Review Summary to PR #6, synced Issue #2 checkboxes, and archived this plan to `docs/plans/archive/ci-security-workflows.md`.

## Final Recap

- **Delivered on `feature/ci-security-workflows` (`PR #6`, `Closes #2`):**
  - `.golangci.yml` (v2 schema) with `depguard` enforcing zero external Go dependencies, `errcheck`, `gosec` (documented `G104` deferral to `errcheck`, `_test.go` exemption), `govet`, `staticcheck`, `ineffassign`, `unused`, `misspell`, and `nolintlint`.
  - `.gitattributes` enforcing LF line endings on `.go`, `.yml`, `.yaml`, and `.sh` files so Windows `core.autocrlf=true` does not break `gofmt -l`.
  - Targeted Go hygiene & security annotations: `0600` temp-file permissions (`concat_list.txt` in `internal/audio/audio.go`, `titles.ass` in `internal/subtitle/subtitles.go`), documented `#nosec` annotations (`G204` in `internal/mixer/mixer.go:54`, `G404` in `internal/probe/ffprobe.go:52`, `G306` in `internal/subtitle/chapters.go:76`), cross-platform backslash normalization in `escapeConcatPath`, `gosec` `G602` bounds check in `BuildASS`, and `staticcheck` `ST1005` lowercase error strings.
  - `.github/workflows/ci.yml` (`actionlint@v1.7.12`, `has_go_mod` guard, `gofmt -l .`, `go vet ./...`, `golangci-lint@v2.15.0`, `ubuntu-latest` + `windows-latest` build/unit tests + `GOOS=darwin` cross-build, `ffmpeg` E2E `TestE2EPipeline`).
  - `.github/workflows/security.yml` (`govulncheck`, CodeQL `actions` + `go` on PR, push to `main`, and weekly schedule, least-privilege `contents: read` + job-scoped `security-events: write`) and `.github/dependabot.yml` (weekly `gomod` + `github-actions`), with all third-party actions pinned to 40-char commit SHAs.

## Deployment Plan

1. **Human Merge Gate (`High` Risk Tier):** Review and merge [PR #6](https://github.com/GCherry/ytmix/pull/6) into `main` once final archive-commit CI checks report green (`Closes #2` auto-closes Issue #2 on merge).
2. **Post-merge:** Verify `CI` and `Security` workflows pass on `main`, and confirm `feature/ci-security-workflows` is deleted remotely and locally.
