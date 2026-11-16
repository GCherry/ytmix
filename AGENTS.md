# AGENTS.md — ytmix

Instructions for coding agents working in `ytmix`.

## Project Overview

`ytmix` is a zero-dependency Go (`1.22+`) CLI tool that wraps `ffmpeg` and `ffprobe` to compile a directory of `.mp3` audio files and a background visual (static image or looping video/GIF) into a 1080p `.mp4` video with YouTube chapter timestamps (`timestamps.txt`).

## Source-of-Truth Docs

- **Technical spec:** Read [`spec docs/ytmix_technical_specification.md`](spec%20docs/ytmix_technical_specification.md) before implementing or changing CLI flags, audio/subtitle/video pipeline steps, or hardware acceleration behavior.
- **Pipeline blueprint:** See [`spec docs/ytmix_architecture_pipeline_blueprint.html`](spec%20docs/ytmix_architecture_pipeline_blueprint.html) for the visual architecture overview.
- **Feature plans:** Multi-step feature plans live in `plans/` (create with `real-work`, archive completed plans to `plans/archive/`).

## Build, Test & Verification

Prerequisite: `go` (`1.22+`), `ffmpeg`, and `ffprobe` must be available on `PATH`.

```powershell
# Build binary
go build -o ytmix.exe .

# Static checks & unit tests
go vet ./...
go test ./...
```

- **End-to-end verification:** Whenever you implement or modify the rendering/audio/subtitle pipeline, run the synthetic `ffmpeg`/`ffprobe` verification suite defined in Section 5 of [`spec docs/ytmix_technical_specification.md`](spec%20docs/ytmix_technical_specification.md) across all three rendering paths (Path A: looping video copy, Path B: looping video with overlays + normalize, Path C: static image with overlays), assert streams/resolution/duration/`timestamps.txt` via `ffprobe`, and remove `test_assets/` and generated test outputs afterward.

## Architectural & Coding Conventions

- **Zero external Go dependencies:** Use Go standard library packages only (`os/exec`, `flag`, `path/filepath`, `runtime`, `fmt`, `strings`, `math/rand`, `encoding/json`, `regexp`, `os`). Shell out to `ffmpeg` and `ffprobe` via `os/exec` rather than adding CGo bindings.
- **Deterministic temp workspace:** Create a per-run temp directory (`os.MkdirTemp("", "ytmix-*")`) for intermediate files (`merged_audio.m4a`, `concat_list.txt`, `norm_loop.mp4`, `titles.ass`, normalized tracks) and always clean it up with `defer os.RemoveAll(tempDir)`.
- **Subtitle overlays:** Generate `.ass` subtitle files (`titles.ass`) for `--overlay-titles` instead of `drawtext` filters to avoid shell/filter escaping bugs.
- **Stream mapping:** Explicitly pass `-map 0:v:0 -map 1:a:0` on final video mux/encode commands so stray audio streams in background videos are stripped.
- **Cross-platform encoder selection:** Detect OS/encoders (`h264_videotoolbox` on `darwin`, `h264_nvenc` if available on `windows`/`linux`, `libx264` fallback) as specified in Section 2 of the spec.

## Agent skills

### Issue tracker

GitHub Issues (`GCherry/ytmix`) via the `gh` CLI or GitHub MCP tools. See [`docs/agents/issue-tracker.md`](docs/agents/issue-tracker.md).

### Triage labels

Canonical five roles (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`) plus `work-started` and priority labels (`priority: now`, `priority: soon`, `priority: later`). See [`docs/agents/triage-labels.md`](docs/agents/triage-labels.md).

### Domain docs

Single-context (`GLOSSARY.md` + `docs/adr/` at the repo root). See [`docs/agents/domain.md`](docs/agents/domain.md).

### Shared skills

Shared engineering and coordination skills live in `chill-hq/.agents/skills/` (never duplicated in this repo). Standalone sessions in `ytmix` read them through the local git-ignored `.agents/skills` junction (plus `.claude/skills` / `.cursor/skills`); commit files under `.agents/skills/` here only for intentional `ytmix`-specific overrides.

