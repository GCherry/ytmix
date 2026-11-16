# ytmix CLI Implementation Plan

**Origin:** Nov 15, 2026 — User request to plan the architecture and implementation of [`docs/specs/ytmix_technical_specification.md`](../specs/ytmix_technical_specification.md) using a clean, zero-dependency Go CLI layout.

Implement `ytmix`, a zero-dependency Go (`1.22+`) CLI that compiles a directory of `.mp3` Tracks and a Background Visual (`.jpg`/`.jpeg`/`.png`/`.webp`/`.mp4`/`.mov`/`.gif`) into a 1080p YouTube `.mp4` Mix and a `timestamps.txt` Chapter Timestamps file across three smart FFmpeg Rendering Paths.

## For Future Agents

The plan file is the source of truth — a chat summary is not a handoff. Last
step of every agent, including subagents: mark checkboxes `- [x]`; set phase
status; write **Phase Summary**; record **Verification Plan** results. When all
phases are done, fill **Final Recap** and **Deployment Plan**, then archive this
file per the real-work skill (Section 3).

**Repo:** `ytmix` (`D:\CodingWorkspace\ytmix\ytmix`) — read its [`AGENTS.md`](../../AGENTS.md) before building or testing.

**Branch:** `feature/ytmix-cli` off `main` (or `main` directly if preferred for initial scaffold).

**Domain docs:** [`GLOSSARY.md`](../../GLOSSARY.md), [`docs/adr/0001-ffmpeg-rendering-paths-and-ass-subtitles.md`](../adr/0001-ffmpeg-rendering-paths-and-ass-subtitles.md)

**Related skills:** `tdd` (verification), `code-review` (post-build review)

## Architecture Overview (Standard Go CLI Layout)

The CLI follows a standard Go `cmd/` + `internal/` layout with zero external dependencies, acyclic package boundaries (`cmd/ytmix` -> `internal/mixer` -> `internal/{audio,probe,subtitle,render}`, with `internal/audio` -> `internal/probe`), and co-located unit tests:

```text
ytmix/
├── go.mod                          # module github.com/GCherry/ytmix (Go 1.22+, zero external deps)
├── cmd/ytmix/
│   ├── main.go                     # CLI flag parsing, top-level execution & exit codes
│   └── main_test.go                # Flag parsing unit tests
└── internal/
    ├── mixer/
    │   ├── mixer.go                # Core domain types (Track, Config, CommandRunner), validation & pipeline orchestration
    │   └── mixer_test.go           # Config validation, ProbeTracks & pipeline orchestration unit tests
    ├── probe/
    │   ├── ffprobe.go              # Step 1: .mp3 discovery, sort/shuffle, ffprobe JSON duration + ID3 title/filename parser
    │   └── ffprobe_test.go         # Discovery, Display Title & ffprobe JSON unit tests
    ├── audio/
    │   ├── audio.go                # Step 2: Optional loudnorm (-14 LUFS to .wav) + re-probe, concat demuxer or acrossfade merge
    │   └── audio_test.go           # Concat escaping, acrossfade filter, normalization & merge unit tests
    ├── subtitle/
    │   ├── chapters.go             # Step 3: Cumulative start-time math & timestamps.txt writer (MM:SS / HH:MM:SS)
    │   ├── chapters_test.go        # Chapter start-time & MM:SS/HH:MM:SS boundary unit tests
    │   └── subtitles.go            # Step 4 (Phase 2): Dynamic .ass subtitle generator (1920x1080, bottom-left, \fad(500,500))
    └── render/
        └── render.go               # Step 5 (Phase 2): Runtime HW encoder detection + Path A / Path B / Path C FFmpeg execution
```

## Open Questions

| # | Question | Owner | Resolve by | Blocking? |
|---|----------|-------|------------|-----------|
| _(none — all Wave 0 questions resolved)_ | | | | |

### Resolved

| # | Question / Item | Resolution |
|---|-----------------|------------|
| G1 | Windows drive-letter colon (`C:`) and backslash escaping in FFmpeg `subtitles=` filter | Resolved Nov 15, 2026 09:22:00 PM EST — Auto-applied (Critical): convert backslashes to `/` and escape `:` as `\:` and `'` as `\'` inside `escapeFFmpegFilterPath` so `subtitles='C\:/.../titles.ass'` succeeds on Windows. |
| G2 | Single-quote and Windows path escaping in FFmpeg concat demuxer (`concat_list.txt`) | Resolved Nov 15, 2026 09:22:00 PM EST — Auto-applied (Critical): normalize paths via `filepath.ToSlash` and escape `'` as `'\''` inside `file '...'` entries. |
| G3 | Single-track and excessive `--crossfade` edge cases in `acrossfade` | Resolved Nov 15, 2026 09:22:00 PM EST — Auto-applied (Critical): validate `--crossfade >= 0`, reject `--crossfade >= minTrackDuration` with a clear error before encoding, and fall back to direct single-track encode when `len(tracks) == 1`. |
| G4 | False-positive `h264_nvenc` detection when probing `ffmpeg -encoders` | Resolved Nov 15, 2026 09:22:00 PM EST — Auto-applied (Critical): verify hardware encoder usability via a fast 1-frame null encode (`ffmpeg -v error -f lavfi -i color=c=black:s=64x64:d=0.1 -c:v <enc> -f null -`) before selecting `h264_nvenc` or `h264_videotoolbox`, falling back cleanly to `libx264 -preset veryfast -crf 20`. |
| G5 | Consistent `setsar=1` across all three Rendering Paths and `.ass` text escaping | Resolved Nov 15, 2026 09:22:00 PM EST — Auto-applied (Low-risk): include `setsar=1` in Path A, Path B, and Path C filter chains per spec Section 5, and escape `{`, `}`, and `\` in Display Titles inside `titles.ass`. |
| 1 (G-O1) | Intermediate format for `--normalize`: `.wav` (`pcm_s16le`, 48kHz) vs `.m4a` (`aac`) in `tempDir` | Resolved Nov 15, 2026 09:25:33 PM EST — User confirmed: normalize each Track to temporary uncompressed `.wav` (`pcm_s16le -ar 48000`) in `tempDir` so lossy AAC encoding happens only once during `merged_audio.m4a` creation. |
| 2 (G-O2) | Track Display Title precedence (ID3 `title` vs filename fallback) | Resolved Nov 15, 2026 09:25:33 PM EST — User confirmed: stick strictly to the 10 CLI flags in the spec; use trimmed ID3 `title` when non-empty and fallback to cleaned filename (`^\d+[\s._-]+` stripped). |
| 3 (G-O3) | Title Overlay prefix & final Track end-time bound | Resolved Nov 15, 2026 09:25:33 PM EST — User confirmed: keep the `♪ ` prefix (`{\fad(500,500)}♪ <Display Title>`) and bound the final Track's subtitle end time by total Mix duration $T_N = T_{N-1} + \text{Duration}_{N-1}$. |
| 4 (G-O4) | CLI progress & logging UX | Resolved Nov 15, 2026 09:25:33 PM EST — User confirmed: print concise `[1/5]..[5/5]` step status lines + total elapsed time to stdout, and pass `-hide_banner -loglevel error` to `ffmpeg` (surfacing full `stderr` on failure). |

## Decisions (confirmed)

| Topic | Decision |
|-------|----------|
| Language & Dependencies | Go `1.22+` standard library only (`os/exec`, `flag`, `path/filepath`, `runtime`, `fmt`, `strings`, `math/rand/v2` or `math/rand`, `encoding/json`, `regexp`, `os`); zero external Go module dependencies. |
| Package Layout | Standard Go CLI layout: `cmd/ytmix` entrypoint + domain packages under `internal/` (`mixer`, `probe`, `audio`, `subtitle`, `render`) with co-located `*_test.go` files and no unnecessary interfaces or `pkg/`/`utils/` bloat. |
| Temp Workspace Lifecycle | Create `os.MkdirTemp("", "ytmix-*")` in `internal/mixer/mixer.go` and guarantee cleanup on all exit paths via `defer os.RemoveAll(tempDir)`. |
| CLI Flags & Display Title Precedence | Strictly implement the 10 flags from Spec Section 3. Use trimmed ID3 `title` when non-empty; otherwise strip `.mp3` and leading numbering (`^\d+[\s._-]+`) from the filename. |
| Loudness Normalization Format | When `--normalize` is enabled, transcode each Track to temporary uncompressed `.wav` (`-c:a pcm_s16le -ar 48000 -af "loudnorm=I=-14:TP=-1.5:LRA=11"`) in `tempDir` and re-probe duration before merging once to AAC (`merged_audio.m4a`). |
| Stream Mapping | Always pass `-map 0:v:0 -map 1:a:0` on final video mux/encode commands so any audio embedded in `--bg` is stripped. |
| Windows Filter & Concat Escaping | Escape Windows paths in `subtitles=` filter (`\` → `/`, `:` → `\:`, `'` → `\'`) and in `concat_list.txt` (`\` → `/`, `'` → `'\''`). |
| Crossfade Guardrails | Validate `--crossfade >= 0`; require every Track duration (post-normalization if `--normalize` is enabled) to strictly exceed `--crossfade` when `len(tracks) > 1`; bypass `acrossfade` filter when `len(tracks) == 1`. |
| Hardware Encoder Probe | Test `h264_videotoolbox` (`darwin`) and `h264_nvenc` (`windows`/`linux`) with a 1-frame `lavfi` null-mux probe so machines without NVIDIA drivers or hardware cleanly fall back to `libx264 -preset veryfast -crf 20`. |
| Timestamp Formatting | Format `timestamps.txt` as `MM:SS <Display Title>` when total Mix duration $< 3600\text{s}$, and `HH:MM:SS <Display Title>` when $\ge 3600\text{s}$, always starting at `00:00` (or `00:00:00`) on line 1. |
| Subtitle Format & Final Track Bound | Prefix every `.ass` dialogue line with `{\fad(500,500)}♪ <Display Title>` (escaping `{`, `}`, `\`), and bound the final Track's end time by total Mix duration $T_N = T_{N-1} + \text{Duration}_{N-1}$. |
| Console Progress & Error Logging | Print `[1/5]..[5/5]` step progress + elapsed time to stdout; run `ffmpeg` with `-hide_banner -loglevel error` and include full captured `stderr` if any command exits non-zero. |

## Grill

**Recorded:** Nov 15, 2026 09:22:00 PM EST (Frontier resolved Nov 15, 2026 09:25:33 PM EST)

### Design tree

```text
ytmix Architecture & Pipeline Design Tree
├── 1. Module & Package Shape
│   └── Flat `package main` with stage-cohesive files (probe, audio, chapters, subtitles, video) [Locked]
├── 2. Step 1: Track Discovery & Metadata
│   ├── Case-insensitive `.mp3` scan, alphabetical sort vs. `--shuffle` [Locked]
│   └── ID3 `title` precedence vs. filename fallback (`^\d+[\s._-]+`) [Resolved — G-O2]
├── 3. Step 2: Audio Normalization & Merging
│   ├── Intermediate `.wav` (`pcm_s16le`, 48kHz) for `--normalize` before final AAC encode [Resolved — G-O1]
│   ├── `concat_list.txt` single-quote & slash escaping on Windows [Auto-taken Critical — G2]
│   └── `acrossfade` guardrails for 1-track input or `crossfade >= trackDuration` [Auto-taken Critical — G3]
├── 4. Step 3 & 4: Chapter Timestamps & `.ass` Title Overlays
│   ├── Cumulative start time math ($T_i = T_{i-1} + D_{i-1} - \text{Crossfade}$) & `MM:SS` / `HH:MM:SS` switch at 3600s [Locked]
│   ├── `.ass` character escaping (`{`, `}`, `\`) [Auto-taken Low-risk — G5]
│   └── `♪ ` prefix and final track end-time bound ($T_N = \text{TotalMixDuration}$) [Resolved — G-O3]
└── 5. Step 5: Smart Video Rendering & UX
    ├── Windows path escaping (`C\:/...`) for `subtitles=` filter in Path B & Path C [Auto-taken Critical — G1]
    ├── Runtime 1-frame hardware encoder probe (`h264_nvenc` / `h264_videotoolbox`) [Auto-taken Critical — G4]
    ├── `setsar=1` consistency across Paths A, B, and C [Auto-taken Low-risk — G5]
    └── Console `[1/5]..[5/5]` step progress & `-hide_banner -loglevel error` UX [Resolved — G-O4]
```

### Auto-taken (Critical / Low-risk)

| # | Class | Rec | Where applied |
|---|-------|-----|---------------|
| G1 | Critical | Escape Windows paths passed to FFmpeg `subtitles=` filter (`\` → `/`, `:` → `\:`, `'` → `\'`) so `C:\...` drive colons do not break `libass` option parsing on Windows. | Decisions / Phase 2 (`internal/subtitle`, `internal/render`) |
| G2 | Critical | Normalize paths to forward slashes and escape single quotes (`'\''`) in `concat_list.txt` so Windows paths and apostrophes in filenames do not break FFmpeg concat demuxer. | Decisions / Phase 1 (`internal/audio`) |
| G3 | Critical | Guard `--crossfade`: reject negative values or `--crossfade >= minTrackDuration`, and bypass `acrossfade` when `len(tracks) == 1`. | Decisions / Phase 1 (`internal/audio`) |
| G4 | Critical | Probe `h264_nvenc` / `h264_videotoolbox` with a 1-frame null encode rather than relying only on `ffmpeg -encoders` text listing, preventing runtime crashes when GPU hardware/drivers are unavailable. | Decisions / Phase 2 (`internal/render`) |
| G5 | Low-risk | Include `setsar=1` in all 3 Rendering Paths (matching Spec Section 5) and sanitize `{`, `}`, and `\` in Display Titles when writing `titles.ass`. | Decisions / Phase 2 (`internal/subtitle`, `internal/render`) |

### Remaining (Other → Open Questions)

All 4 `Other` frontier questions (`G-O1` through `G-O4`) were asked and resolved with the user on Nov 15, 2026 09:25:33 PM EST (see **Resolved** and **Decisions (confirmed)**).

## Wave 0: Architecture Plan, Docs & Scope Lock <!-- serial -->

**Triggered by:** User — Nov 15, 2026 09:22:00 PM EST

### Phase 0: Architecture Plan, Domain Docs, README & Grill Review

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Agent (Planner) — Nov 15, 2026 09:22:00 PM EST

- [x] Create [`AGENTS.md`](../../AGENTS.md) with build, test, verification, and architectural conventions.
- [x] Create [`GLOSSARY.md`](../../GLOSSARY.md) and [`docs/adr/0001-ffmpeg-rendering-paths-and-ass-subtitles.md`](../adr/0001-ffmpeg-rendering-paths-and-ass-subtitles.md).
- [x] Update [`README.md`](../../README.md) with prerequisites, build instructions, CLI flag reference, usage examples, and links to the spec docs.
- [x] Author this `real-work` plan (`plans/ytmix-cli-implementation.md`, later moved to `docs/plans/ytmix-cli-implementation.md`) and run the `grill-with-docs` pass.
- [x] Resolve Open Questions #1–#4 with the user and obtain Wave 0 sign-off before starting Wave 1.

#### Verification Plan

- Verified [`AGENTS.md`](../../AGENTS.md), [`GLOSSARY.md`](../../GLOSSARY.md), [`docs/adr/0001-ffmpeg-rendering-paths-and-ass-subtitles.md`](../adr/0001-ffmpeg-rendering-paths-and-ass-subtitles.md), [`README.md`](../../README.md), and [`docs/plans/ytmix-cli-implementation.md`](ytmix-cli-implementation.md) exist and honor canonical `GLOSSARY.md` terms without `_Avoid_` synonyms.
- Confirmed user answers to Open Questions #1–#4 (`G-O1`–`G-O4`) are locked in **Decisions (confirmed)** and **Resolved**.

#### Phase Summary

Completed Nov 15, 2026 09:25:33 PM EST by Agent (Planner). Created `AGENTS.md`, `GLOSSARY.md`, `docs/adr/0001-ffmpeg-rendering-paths-and-ass-subtitles.md`, updated `README.md`, and completed the `grill-with-docs` interview on `plans/ytmix-cli-implementation.md`. All 5 Critical/Low-risk FFmpeg cross-platform edge cases (`G1`–`G5`) and all 4 user decisions (`G-O1`–`G-O4`) are locked. Ready for `Engineering` to execute Wave 1 (`Phase 1`) and Wave 2 (`Phase 2`) when requested.

## Wave 1: Core Audio, Metadata & Chapter Pipeline (Steps 1–3) <!-- serial -->

**Triggered by:** Subagent (Engineering) — Nov 16, 2026 11:16:37 AM EST

### Phase 1: Go Module Scaffold, Track Probing, Audio Merging & Chapter Timestamps

Status: Complete <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Subagent (Engineering) — Nov 16, 2026 11:16:37 AM EST

- [x] Initialize `go.mod` (`module github.com/GCherry/ytmix`, `go 1.22`).
- [x] Implement `main.go` CLI flag definitions (`--audio-dir`, `--bg`, `--out`, `--timestamps`, `--crossfade`, `--normalize`, `--overlay-titles`, `--title-duration`, `--font-name`, `--shuffle`), upfront validation (required `--bg`, supported file extension check, `ffmpeg`/`ffprobe` `exec.LookPath` check), `[1/5]..[5/5]` stdout step logging with elapsed time, and `os.MkdirTemp("", "ytmix-*")` with `defer os.RemoveAll(tempDir)`.
- [x] Implement `probe.go` (Step 1): case-insensitive `.mp3` directory scan, alphabetical sort or `--shuffle`, `ffprobe -v error -show_entries format=duration -show_entries format_tags=title -of json <file>` execution and JSON parsing, and Display Title resolution (trimmed ID3 `title` or filename with `.mp3` and `^\d+[\s._-]+` stripped).
- [x] Implement `audio.go` (Step 2):
  - When `--normalize` is enabled, transcode each Track to temporary uncompressed `.wav` (`-c:a pcm_s16le -ar 48000 -af "loudnorm=I=-14:TP=-1.5:LRA=11"`) in `tempDir` and re-probe its exact post-normalization duration.
  - Validate `--crossfade` against Track durations (`crossfade < duration` for every Track when `len(tracks) > 1`).
  - Merge audio into `tempDir/merged_audio.m4a` (`-c:a aac -b:a 320k -ar 48000`) via `concat_list.txt` (`-f concat -safe 0`) when `--crossfade == 0` (or `len(tracks) == 1`), or via sequential `acrossfade=d=<d>:c1=tri:c2=tri` `-filter_complex` chain when `--crossfade > 0`.
- [x] Implement `chapters.go` (Step 3): calculate cumulative start times ($T_0 = 0.0$, $T_i = T_{i-1} + D_{i-1} - \text{Crossfade}$), format as `MM:SS <Display Title>` (or `HH:MM:SS <Display Title>` when total Mix duration $\ge 3600\text{s}$), and write `--timestamps` (`timestamps.txt`).
- [x] Add unit tests in `ytmix_test.go` covering Display Title regex stripping, cumulative timestamp calculation with and without crossfade, `MM:SS` vs `HH:MM:SS` formatting at the `3600s` boundary, `concat_list.txt` escaping, and `acrossfade` filter graph generation.

#### Verification Plan

- `gofmt -l .`: exited `0` with 0 unformatted files.
- `go vet ./...`: exited `0` with 0 warnings.
- `go test -v ./...`: exited `0` (`ok github.com/GCherry/ytmix 0.229s`), all 7 top-level tests / 16 subtests passed (`TestParseFlagsAndValidation`, `TestTitleAndTrackDiscovery`, `TestConcatListEscaping`, `TestAcrossfadeFilterGraph`, `TestAudioNormalizationAndMerge`, `TestTimestampsCalculationAndFormatting`, `TestRunPipelineSteps1To3AndPhase2Guard`), using stubbed `CommandRunner` and `lookPath` seams (`ffmpeg`/`ffprobe` are not on sandbox `PATH`, so live binary execution waits for Phase 2 / user verification).
- `go test -v -run "TestTitle|TestTimestamps|TestConcat|TestAcrossfade" ./...`: exited `0` (`ok github.com/GCherry/ytmix 0.211s`), all 4 matching test suites passed.
- `go build -o ytmix.exe .` & `.\ytmix.exe --help`: exited `0`, printed all 10 CLI flags and defaults; temporary `ytmix.exe` removed before commit.

#### Phase Summary

Completed Nov 16, 2026 11:16:37 AM EST by Subagent (Engineering) on branch `feature/ytmix-cli`. Implemented the zero-dependency Go 1.22 Phase 1 scaffold (`go.mod`, `main.go`, `probe.go`, `audio.go`, `chapters.go`, `ytmix_test.go`) covering CLI flag parsing & upfront validation for all 10 flags, `ytmix-*` temporary workspace lifecycle, `[1/5]..[5/5]` progress logging with a Phase 2 guard for Steps 4–5, case-insensitive `.mp3` Track discovery (`sort` vs `--shuffle`) with `ffprobe` JSON metadata & Display Title resolution, optional `-14 LUFS` `.wav` Loudness Normalization + duration re-probing, `concat_list.txt` escaping & sequential `acrossfade` merging into `merged_audio.m4a`, and `MM:SS`/`HH:MM:SS` Chapter Timestamps output (`timestamps.txt`). Ready for `Reviewer` verification and user testing before Phase 2.

## Wave 2: Subtitle Overlays, Smart Video Rendering & E2E Verification (Steps 4–5) <!-- serial -->

**Triggered by:** Pending Wave 1 completion — Nov 15, 2026 09:25:33 PM EST

### Phase 2: `.ass` Subtitle Generator, 3-Path Video Renderer & Automated E2E Verification

Status: Not Started <!-- Not Started | In Progress | Complete | Abandoned -->
**Triggered by:** Pending Wave 1 completion — Nov 15, 2026 09:25:33 PM EST

- [ ] Implement `internal/subtitle/subtitles.go` (Step 4): generate `tempDir/titles.ass` (`PlayResX: 1920`, `PlayResY: 1080`, `Alignment=1`, `MarginL=60`, `MarginV=55`, `Fontsize=42`, `--font-name`, white primary color with dark outline/shadow), computing each Track's end time as $\min(T_i + \text{TitleDuration}, T_{i+1})$ when `--title-duration > 0` or $T_{i+1}$ when `0` (where $T_N$ is total Mix duration), with `{\fad(500,500)}♪ <Display Title>` and escaped `{`, `}`, `\`.
- [ ] Implement `internal/render/render.go` (Step 5) and wire Steps 4–5 into `internal/mixer/mixer.go`:
  - Implement runtime hardware encoder detection (`darwin` → test `h264_videotoolbox -b:v 6000k`; `windows`/`linux` → test `h264_nvenc -preset p4 -cq 20` or `-b:v 6000k`; fallback → `libx264 -preset veryfast -crf 20`).
  - Implement `EscapeFFmpegFilterPath` (`\` → `/`, `:` → `\:`, `'` → `\'`) for `subtitles='...'`.
  - Implement **Path A** (`.mp4`/`.mov`/`.gif` + `--overlay-titles=false`): pre-normalize loop to `tempDir/norm_loop.mp4` (`1920x1080`, `setsar=1`, `fps=30`, `yuv420p`, `libx264 -preset fast -crf 18 -an`), then mux with `-stream_loop -1 -i norm_loop.mp4 -i merged_audio.m4a -map 0:v:0 -map 1:a:0 -c:v copy -c:a copy -shortest <out>`.
  - Implement **Path B** (`.mp4`/`.mov`/`.gif` + `--overlay-titles=true`): encode with `-stream_loop -1`, `scale/pad/setsar=1,subtitles='...',format=yuv420p`, `-map 0:v:0 -map 1:a:0`, detected HW/SW encoder, `-c:a copy -shortest <out>`.
  - Implement **Path C** (`.jpg`/`.jpeg`/`.png`/`.webp`): encode with `-loop 1 -framerate 2` (or `10` when `--overlay-titles=true`), `scale/pad/setsar=1[,subtitles='...'],format=yuv420p`, `-map 0:v:0 -map 1:a:0 -c:v libx264 -tune stillimage -preset veryfast -crf 18 -c:a copy -shortest <out>`.
- [ ] Add co-located unit tests in `internal/subtitle/subtitles_test.go` and `internal/render/render_test.go` for `.ass` timestamp formatting (`H:MM:SS.cc`), `--title-duration` clamping at `0` and `>0`, `--font-name` injection, Windows filter path escaping, and Rendering Path selection.
- [ ] Implement and execute the Section 5 Automated E2E Verification Suite (`TestE2EPipeline` in `internal/mixer/e2e_test.go` or verification run):
  - Generate synthetic `test_assets/` (two 4s `.mp3` sine tracks at different volumes, one 2s `1280x720` 30fps `.mp4` loop, one `.png` image).
  - Run **Test 1 (Path A):** Video loop, no overlays, `--crossfade 1` → assert `1920x1080`, `h264` + `aac`, duration $\approx 7.0\text{s}$, valid `timestamps.txt`.
  - Run **Test 2 (Path B):** Video loop, `--overlay-titles`, `--normalize` → assert `1920x1080`, `h264` + `aac`, duration $\approx 8.0\text{s}$, valid `timestamps.txt`.
  - Run **Test 3 (Path C):** Static image, `--overlay-titles` → assert `1920x1080`, `h264` + `aac`, duration $\approx 8.0\text{s}$, valid `timestamps.txt`.
  - Clean up `test_assets/` and test outputs automatically.

#### Verification Plan

- Run `go build -o ytmix.exe ./cmd/ytmix`, `go vet ./...`, and `go test -v ./...` (including `TestE2EPipeline` across Path A, Path B, and Path C).
- Verify all 3 E2E test outputs pass `ffprobe` stream, resolution (`1920x1080`), codec (`h264`/`aac`), duration (`7.0s` / `8.0s`), and `timestamps.txt` assertions, and verify `test_assets/` and `ytmix.exe` test artifacts are cleaned up afterward.

#### Phase Summary

_(write when phase completes)_

## Final Recap

_(write when all phases complete)_

## Deployment Plan

_(write when all phases complete)_
