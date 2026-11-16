# Technical Specification: `ytmix` — Automated YouTube Mix & Compilation Builder CLI

## 1. Overview & Objective
`ytmix` is a high-performance, zero-dependency Go CLI tool that wraps `ffmpeg` and `ffprobe` to compile a directory of `.mp3` audio files and a background visual (either a static image or a short looping video/GIF) into a 1080p `.mp4` video ready for YouTube upload. Alongside the video, it calculates exact track start times and outputs a YouTube-formatted chapter file (`timestamps.txt`).

---

## 2. System & Architectural Constraints
- **Language:** Go (`1.22+`), utilizing standard library packages (`os/exec`, `flag`, `path/filepath`, `runtime`, `fmt`, `strings`, `math/rand`).
- **External Dependencies:** `ffmpeg` and `ffprobe` must be available on the system `PATH`. Do not use heavy CGo bindings or in-memory audio/video processing libraries; shell out directly via `os/exec`.
- **Hardware Acceleration Detection:** Inspect `runtime.GOOS` (and optionally probe `ffmpeg -encoders`) to select the optimal H.264 encoder when video re-encoding is required:
  - `darwin`: `h264_videotoolbox` (with `-b:v 6000k` or `-q:v 65`)
  - `windows` / `linux`: Check if `h264_nvenc` is available; fall back cleanly to `libx264 -preset veryfast -crf 20`.
- **Temporary Workspace:** Create a deterministic temporary working directory (`os.MkdirTemp("", "ytmix-*")`) for intermediate normalized audio tracks, concat lists, pre-normalized video loops, and `.ass` subtitle scripts. Clean up this directory via `defer os.RemoveAll(tempDir)` on exit.

---

## 3. CLI Flags & Interface

| Flag | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `--audio-dir` | `string` | `./tracks` | Directory containing input `.mp3` files. |
| `--bg` | `string` | *(Required)* | Path to background visual (`.jpg`, `.jpeg`, `.png`, `.webp`, `.mp4`, `.mov`, `.gif`). |
| `--out` | `string` | `output.mp4` | Output `.mp4` video path. |
| `--timestamps` | `string` | `timestamps.txt` | Output file path for YouTube chapter timestamps. |
| `--crossfade` | `float64` | `0` | Crossfade duration in seconds between tracks (`0` uses fast concat demuxer; `>0` uses `acrossfade`). |
| `--normalize` | `bool` | `false` | Normalize each track to YouTube's `-14 LUFS` target (`loudnorm=I=-14:TP=-1.5:LRA=11`). |
| `--overlay-titles` | `bool` | `false` | Burn the current track title into the bottom-left corner of the video via dynamic `.ass` subtitles. |
| `--title-duration` | `int` | `0` | Seconds to display each track title (`0` = stays visible for the entire track; `>0` = fades in/out for the first $N$ seconds). |
| `--font-name` | `string` | `Arial` | Font family name used inside the generated `.ass` subtitle script. |
| `--shuffle` | `bool` | `false` | Randomize track order instead of sorting alphabetically. |

---

## 4. Step-by-Step Pipeline Architecture

### Step 1: Track Discovery & Metadata Extraction
1. Scan `--audio-dir` for case-insensitive `.mp3` files. Exit with a clear error if fewer than 1 track is found.
2. Sort tracks alphabetically by filename, or shuffle if `--shuffle` is set.
3. For each track:
   - Run `ffprobe -v error -show_entries format=duration -show_entries format_tags=title -of json <file>` to extract the exact floating-point duration in seconds and any embedded ID3 `title` tag.
   - If the ID3 `title` tag is empty, derive the clean display title from the filename: strip the `.mp3` extension and strip leading track numbering patterns (regex: `^\d+[\s._-]+`).

### Step 2: Audio Processing & Normalization
1. **Optional Loudness Normalization (`--normalize`):**
   - For each input track, transcode to a temporary high-bitrate AAC or WAV file in `tempDir` applying `-af "loudnorm=I=-14:TP=-1.5:LRA=11"`.
   - Re-probe the normalized file duration to guarantee sample-accurate timestamp math.
2. **Audio Merging (`merged_audio.m4a`):**
   - **If `--crossfade == 0`:** Write an FFmpeg concat demuxer file (`concat_list.txt`) with escaped paths (`file '/path/to/track.mp3'`) and encode into `tempDir/merged_audio.m4a` using `-f concat -safe 0 -i concat_list.txt -c:a aac -b:a 320k -ar 48000`.
   - **If `--crossfade > 0`:** Build an `-filter_complex` chain applying `acrossfade=d=<crossfade>:c1=tri:c2=tri` sequentially across all inputs and output to `tempDir/merged_audio.m4a` (`-c:a aac -b:a 320k -ar 48000`).

### Step 3: Timestamp Calculation & `timestamps.txt` Generation
1. Calculate each track's start time $T_i$:
   - $T_0 = 0.0$
   - For $i > 0$:
     $$T_i = T_{i-1} + \text{Duration}_{i-1} - \text{Crossfade}$$
2. Format timestamps as `MM:SS Track Title` (or `HH:MM:SS Track Title` if total mix duration $\ge 3600$ seconds).
3. Write the formatted list to `--timestamps` (`timestamps.txt`), ensuring `00:00` is present on the first line so YouTube enables automatic chapters.

### Step 4: Subtitle-Based Track Overlays (`--overlay-titles`)
Avoid FFmpeg `drawtext` chains (which suffer from escaping bugs on apostrophes and colons). Instead, generate `tempDir/titles.ass` in Advanced SubStation Alpha (`.ass`) format:
1. Set `PlayResX: 1920` and `PlayResY: 1080`.
2. Define a clean bottom-left style (`Alignment=1`, `MarginL=60`, `MarginV=55`, `Fontsize=42`, white primary color with subtle dark outline/shadow).
3. For each track $i$:
   - Start time: $T_i$
   - End time: If `--title-duration > 0`, $\min(T_i + \text{TitleDuration}, T_{i+1})$; otherwise $T_{i+1}$.
   - Include a smooth 500ms fade-in and fade-out tag on every dialogue line:
     `Dialogue: 0,0:00:00.00,0:02:45.10,Default,,0,0,0,,{\fad(500,500)}♪ Track Title`

### Step 5: Smart Video Rendering (3 Execution Paths)
Always map the video stream exclusively from the background visual (`-map 0:v:0`) and the audio stream exclusively from `merged_audio.m4a` (`-map 1:a:0`) so any stray audio embedded in a background `.mp4` is stripped.

#### Path A: Looping Video WITHOUT Overlays (Ultra-Fast Stream Copy)
*Triggered when `--bg` is `.mp4`/`.mov`/`.gif` AND `--overlay-titles == false`.*
1. Pre-normalize the short video loop once into `tempDir/norm_loop.mp4`:
   ```bash
   ffmpeg -y -i <bg> -vf "scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=30,format=yuv420p" -c:v libx264 -preset fast -crf 18 -an tempDir/norm_loop.mp4
   ```
2. Mux the pre-normalized loop infinitely against `merged_audio.m4a` using `-c:v copy` (renders a 60-minute video in seconds without re-encoding frames):
   ```bash
   ffmpeg -y -stream_loop -1 -i tempDir/norm_loop.mp4 -i tempDir/merged_audio.m4a -map 0:v:0 -map 1:a:0 -c:v copy -c:a copy -shortest <out>
   ```

#### Path B: Looping Video WITH Overlays (Hardware-Accelerated Render)
*Triggered when `--bg` is `.mp4`/`.mov`/`.gif` AND `--overlay-titles == true`.*
1. Loop the input video, scale/pad to `1920x1080`, burn in `tempDir/titles.ass`, and encode using the detected hardware encoder (or `libx264 -preset veryfast` fallback):
   ```bash
   ffmpeg -y -stream_loop -1 -i <bg> -i tempDir/merged_audio.m4a -vf "scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,setsar=1,subtitles=tempDir/titles.ass,format=yuv420p" -map 0:v:0 -map 1:a:0 -c:v <hw_or_sw_encoder> -c:a copy -shortest <out>
   ```

#### Path C: Static Image Background
*Triggered when `--bg` is `.png`/`.jpg`/`.jpeg`/`.webp`.*
1. Set `-framerate 2` if `--overlay-titles == false`, or `-framerate 10` if `--overlay-titles == true` (so subtitle fades look smooth).
2. Run FFmpeg with `-loop 1` and `-tune stillimage`:
   ```bash
   ffmpeg -y -loop 1 -framerate <fps> -i <bg> -i tempDir/merged_audio.m4a -vf "scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,setsar=1[,subtitles=tempDir/titles.ass],format=yuv420p" -map 0:v:0 -map 1:a:0 -c:v libx264 -tune stillimage -preset veryfast -crf 18 -c:a copy -shortest <out>
   ```

---

## 5. Automated End-to-End Verification Plan
Upon completing the implementation, the agent must run an automated verification suite inside the project workspace:
1. Create a temporary `test_assets/` directory.
2. Generate two synthetic 4-second `.mp3` files at different volumes using `ffmpeg -f lavfi -i "sine=frequency=440:duration=4"`.
3. Generate a synthetic 2-second `.mp4` video loop (`testsrc=duration=2:size=1280x720:rate=30`) and a synthetic `.png` image.
4. Execute `ytmix` across all three paths:
   - **Test 1 (Path A):** Video loop, no overlays, `--crossfade 1`.
   - **Test 2 (Path B):** Video loop, `--overlay-titles`, `--normalize`.
   - **Test 3 (Path C):** Static image, `--overlay-titles`.
5. Assert via `ffprobe` that each output `.mp4` exists, has `1920x1080` resolution, contains both video (`h264`) and audio (`aac`) streams, matches expected duration (`7.0s` with 1s crossfade, `8.0s` without), and produces a valid `timestamps.txt`.
6. Clean up `test_assets/` and test outputs after verification passes.