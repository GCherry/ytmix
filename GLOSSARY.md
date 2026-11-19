# ytmix

Automated CLI builder that compiles a directory of MP3 tracks and a background visual into a 1080p YouTube mix video with formatted chapter timestamps.

## Language

**Track**:
A single input `.mp3` audio file discovered in `--audio-dir`, paired with its probed duration and resolved display title.
_Avoid_: Song, clip, audio segment

**Display Title**:
The human-readable name of a Track shown in Chapter Timestamps and Title Overlays, resolved from the embedded ID3 `title` tag or cleaned from the filename (`^\d+[\s._-]+` stripped).
_Avoid_: Track name, filename, label

**Background Visual**:
The optional visual asset (`--bg`) rendered for the duration of the Mix—either a static image (`.jpg`, `.jpeg`, `.png`, `.webp`) or a looping video/animation (`.mp4`, `.mov`, `.gif`). When omitted, `ytmix` enters **Auto Visualizer** (`Path D`).
_Avoid_: Cover file, backdrop, wallpaper

**Auto Visualizer**:
Built-in **Path D** video generation (`--visualizer`, `--visualizer-theme`, Darkness & Sleep controls) that renders either a 1080p FFmpeg audio-reactive visualization or a `2 fps` pure `#000000` blackout sleep screen directly from `merged_audio.m4a` when no Background Visual file is provided.
_Avoid_: Screensaver, audio animation, equalizer widget

**Mix**:
The final compiled compilation consisting of the merged audio stream (`merged_audio.m4a`), the 1080p `.mp4` output video (`--out`), and the companion Chapter Timestamps file (`--timestamps`).
_Avoid_: Project, playlist, export

**Crossfade**:
A triangular-curve audio overlap (`--crossfade` seconds) between consecutive Tracks that shortens total Mix duration and shifts subsequent Track start times earlier by the overlap duration.
_Avoid_: Transition, blend, fade-out

**Loudness Normalization**:
Pre-merge EBU R128 loudness adjustment (`--normalize`) targeting YouTube's `-14 LUFS` standard (`TP=-1.5`, `LRA=11`) applied per Track before duration re-probing and merging.
_Avoid_: Volume leveling, gain boost, compression

**Chapter Timestamps**:
The YouTube-formatted plain-text schedule (`timestamps.txt`) listing each Track's cumulative start time (`MM:SS` or `HH:MM:SS`) and Display Title, always starting at `00:00`.
_Avoid_: Tracklist, cue sheet, description file

**Title Overlay**:
An optional burned-in bottom-left text caption (`--overlay-titles`) generated as an Advanced SubStation Alpha (`.ass`) subtitle script with 500ms fade-in/out tags for each Track.
_Avoid_: Watermark, lower-third, drawtext

**Rendering Path**:
One of four deterministic FFmpeg video encoding strategies selected from whether a Background Visual is provided, its media type, and whether Title Overlays are enabled:
- **Path A (Stream Copy Loop)**: Looping video/GIF without Title Overlays; pre-normalizes the short loop once to 1080p H.264 and muxes infinitely via `-stream_loop -1 -c:v copy`.
- **Path B (Hardware-Accelerated Overlay Loop)**: Looping video/GIF with Title Overlays; burns `titles.ass` across the full duration using hardware acceleration (`h264_videotoolbox` or `h264_nvenc`) with `libx264` fallback.
- **Path C (Low-FPS Still Image)**: Static image Background Visual encoded with `-loop 1 -tune stillimage` at `2 fps` (no overlays) or `10 fps` (with Title Overlays).
- **Path D (Auto Visualizer)**: Omitted `--bg`; generates a 1080p music-reactive FFmpeg filtergraph (`30 fps`, hardware-accelerated with `libx264` fallback) or pure `#000000` sleep blackout screen (`2 fps` / `10 fps`, `-tune stillimage`) across 14 built-in presets, 7 Color Themes, and global Darkness & Sleep controls (`--visualizer-brightness`, `--fade-to-black`, `--dim-titles`).
_Avoid_: Render mode, preset, profile
