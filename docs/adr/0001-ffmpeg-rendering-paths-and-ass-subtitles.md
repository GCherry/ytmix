# 0001: Three-Path FFmpeg Rendering and ASS Subtitle Overlays

Status: accepted

`ytmix` shells out directly to `ffmpeg` and `ffprobe` using Go's standard library (`os/exec`), dynamically selects one of three video Rendering Paths (Path A pre-normalized stream-copy loop, Path B hardware-accelerated `.ass` burn loop, Path C low-framerate `-tune stillimage`), and generates Advanced SubStation Alpha (`.ass`) scripts rather than FFmpeg `drawtext` filters for Title Overlays.

## Considered Options

- **In-process CGo bindings (`libav*`) vs. `os/exec` CLI wrapper:** CGo bindings complicate cross-platform compilation on Windows/macOS/Linux and add heavy build dependencies without improving encoding speed.
- **Single uniform 30fps re-encode vs. Three Rendering Paths:** Re-encoding a 60-minute 1080p30 video for every run takes minutes of CPU/GPU work. Pre-normalizing a short loop and using `-stream_loop -1 -c:v copy` (Path A) finishes a 60-minute mix in seconds, while `-tune stillimage` at 2/10 fps (Path C) cuts static-image frame processing by 66–93%.
- **FFmpeg `drawtext` filter chains vs. `.ass` subtitle scripts:** Chaining `drawtext` filters per track fails on song titles containing apostrophes, colons, or backslashes and hits command-line length limits on large tracklists. Writing a single `titles.ass` file handles arbitrary Unicode titles cleanly and supports declarative `\fad(500,500)` fade tags.

## Consequences

- Windows paths passed to FFmpeg's `subtitles=` filter must have backslashes converted to `/` and colons escaped (`\:`) so `libass` does not misparse drive letters (`C:`) as filter option delimiters.
- Hardware encoder availability (`h264_nvenc` on Windows/Linux, `h264_videotoolbox` on macOS) must be verified with a fast 1-frame null-mux probe rather than relying solely on `ffmpeg -encoders` text output, which lists compiled-in encoders even when GPU drivers/hardware are absent.
