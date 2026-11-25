(function () {
  "use strict";

  const THEME_KEY = "ytmix_theme";
  const VIZ_PAGE_SIZE = 6;
  let VIDEO_EXTS = new Set([".mp4", ".mov", ".gif"]);
  let IMAGE_EXTS = new Set([".jpg", ".jpeg", ".png", ".webp"]);

  const PRESET_THUMBS = {
    black: '<rect x="1" y="1" width="42" height="24" fill="#000000" stroke="#2D2F2E" stroke-width="1"/><path d="M25 8 A6 6 0 1 0 29 17 A4.8 4.8 0 1 1 25 8 Z" fill="#6B6254"/>',
    ember: '<ellipse cx="22" cy="14" rx="16" ry="5" fill="#7C2D12" fill-opacity="0.55"/><path d="M5 14 Q14 10 22 14 T39 14" fill="none" stroke="#B45309" stroke-width="2"/>',
    "night-line": '<path d="M3 19 Q10 17 16 19 T28 19 T41 19" fill="none" stroke="#475569" stroke-width="1.4"/>',
    "night-fog": '<ellipse cx="14" cy="22" rx="11" ry="7" fill="#7C2D12" fill-opacity="0.55"/><ellipse cx="29" cy="22" rx="11" ry="6" fill="#312E81" fill-opacity="0.55"/>',
    "lunar-halo": '<circle cx="22" cy="13" r="7" fill="none" stroke="#9A7648" stroke-width="2.5" stroke-opacity="0.55"/><circle cx="22" cy="13" r="7" fill="none" stroke="#C8AF8A" stroke-width="0.9" stroke-opacity="0.65"/>',
    bars: '<rect x="5" y="14" width="4" height="9" fill="currentColor"/><rect x="11" y="7" width="4" height="16" fill="currentColor"/><rect x="17" y="10" width="4" height="13" fill="currentColor"/><rect x="23" y="4" width="4" height="19" fill="currentColor"/><rect x="29" y="12" width="4" height="11" fill="currentColor"/><rect x="35" y="16" width="4" height="7" fill="currentColor"/>',
    waves: '<path d="M3 13 Q8 4 12 13 T21 13 T30 13 T41 13" fill="none" stroke="currentColor" stroke-width="2"/><line x1="3" y1="13" x2="41" y2="13" stroke="currentColor" stroke-opacity="0.35"/>',
    cqt: '<rect x="4" y="15" width="3" height="6" fill="currentColor"/><rect x="9" y="9" width="3" height="12" fill="currentColor"/><rect x="14" y="5" width="3" height="16" fill="currentColor"/><rect x="19" y="11" width="3" height="10" fill="currentColor"/><rect x="24" y="7" width="3" height="14" fill="currentColor"/><rect x="29" y="13" width="3" height="8" fill="currentColor"/><rect x="34" y="10" width="3" height="11" fill="currentColor"/><line x1="4" y1="22" x2="40" y2="22" stroke="#E8BE5A" stroke-width="1.5"/>',
    radial: '<circle cx="22" cy="13" r="8" fill="none" stroke="currentColor" stroke-width="1.4" stroke-opacity="0.45"/><path d="M22 4 L26 11 L32 13 L25 16 L22 22 L19 16 L12 13 L18 10 Z" fill="none" stroke="currentColor" stroke-width="1.5"/>',
    spectrogram: '<rect x="4" y="5" width="36" height="4" fill="currentColor" fill-opacity="0.25"/><rect x="4" y="10" width="36" height="5" fill="currentColor" fill-opacity="0.55"/><rect x="4" y="16" width="36" height="5" fill="currentColor" fill-opacity="0.9"/>',
    "3dscope": '<path d="M4 20 Q14 14 22 18 T40 15" fill="none" stroke="currentColor" stroke-width="1.5"/><path d="M9 15 Q17 10 23 13 T35 11" fill="none" stroke="currentColor" stroke-opacity="0.65" stroke-width="1.3"/><path d="M14 10 Q20 7 24 9 T31 8" fill="none" stroke="currentColor" stroke-opacity="0.4" stroke-width="1.1"/>',
    histogram: '<path d="M4 21 L12 21 L18 11 L22 5 L26 11 L32 21 L40 21" fill="currentColor" fill-opacity="0.35" stroke="currentColor" stroke-width="1.4"/>',
    phase: '<line x1="22" y1="3" x2="22" y2="23" stroke="#E8BE5A" stroke-width="1" stroke-dasharray="2 2"/><path d="M20 5 L26 9 L17 13 L25 17 L21 21" fill="none" stroke="currentColor" stroke-width="1.7"/>',
    vu: '<rect x="5" y="7" width="28" height="4" rx="1" fill="currentColor"/><rect x="5" y="15" width="22" height="4" rx="1" fill="#E8BE5A"/><line x1="36" y1="6" x2="36" y2="20" stroke="currentColor" stroke-width="1.5"/>',
  };

  let PRESETS = [
    { id: "black", name: "Pure Blackout Screen", filter: "color=c=black", category: "dark", description: "True #000000 pitch-black screen (2 fps)", isDark: true, available: true },
    { id: "ember", name: "Dim Ember Pulse", filter: "showwaves+gblur", category: "dark", description: "Soft warm fireplace ember breath", isDark: true, available: true },
    { id: "night-line", name: "Midnight Whisper Line", filter: "showwaves+pad", category: "dark", description: "Ultra-dim thin hairline on OLED black", isDark: true, available: true },
    { id: "night-fog", name: "Sub-Bass Night Fog", filter: "showfreqs+gblur", category: "dark", description: "Blurred low ambient horizon glow", isDark: true, available: true },
    { id: "lunar-halo", name: "Dim Lunar Halo", filter: "avectorscope+gblur", category: "dark", description: "Faint slow-breathing halo in pitch black", isDark: true, available: true },
    { id: "bars", name: "Spectrum Bars", filter: "showfreqs", category: "spectrum", description: "Classic vertical EQ bars (log freq)", isDark: false, available: true },
    { id: "waves", name: "Smooth Waveform", filter: "showwaves", category: "wave", description: "Centered horizontal sound wave", isDark: false, available: true },
    { id: "cqt", name: "Musical Piano Spectrum", filter: "showcqt", category: "spectrum", description: "Pitch-spaced bars + harmonic ribbon", isDark: false, available: true },
    { id: "radial", name: "Radial / Stereo Burst", filter: "avectorscope", category: "wave", description: "Circular polar stereo bloom", isDark: false, available: true },
    { id: "spectrogram", name: "Scrolling Spectrogram", filter: "showspectrum", category: "spectrum", description: "Flowing frequency heatmap waterfall", isDark: false, available: true },
    { id: "3dscope", name: "3D Depth Ribbon", filter: "a3dscope", category: "wave", description: "Receding 3D waveform corridor", isDark: false, available: true },
    { id: "histogram", name: "Audio Histogram", filter: "ahistogram", category: "meters", description: "Stereo amplitude density + waterfall", isDark: false, available: true },
    { id: "phase", name: "Stereo Phase Meter", filter: "aphasemeter", category: "meters", description: "L/R correlation trace + median line", isDark: false, available: true },
    { id: "vu", name: "Studio VU Meters", filter: "showvolume", category: "meters", description: "Dual-channel L/R console peak bars", isDark: false, available: true },
  ];

  let THEMES = [
    { id: "studio-mint", name: "Studio Mint & Gold", bg: "#0B0C0D", primary: "#3EE6B5", secondary: "#E8BE5A", glow: "rgba(62, 230, 181, 0.45)" },
    { id: "midnight-cyan", name: "Midnight Cyan", bg: "#070B14", primary: "#38BDF8", secondary: "#818CF8", glow: "rgba(56, 189, 248, 0.45)" },
    { id: "sunset-amber", name: "Sunset Amber", bg: "#120A09", primary: "#FB923C", secondary: "#F43F5E", glow: "rgba(251, 146, 60, 0.45)" },
    { id: "neon-violet", name: "Neon Violet", bg: "#0D0814", primary: "#C084FC", secondary: "#2DD4BF", glow: "rgba(192, 132, 252, 0.45)" },
    { id: "monochrome", name: "Monochrome Paper", bg: "#0A0A0A", primary: "#F3F2EC", secondary: "#949A95", glow: "rgba(243, 242, 236, 0.35)" },
    { id: "oled-ember", name: "OLED Ember (Zero Blue)", bg: "#000000", primary: "#9A3412", secondary: "#5C1E09", glow: "rgba(154, 52, 18, 0.35)" },
    { id: "night-indigo", name: "Deep Night Indigo", bg: "#000000", primary: "#3B4F73", secondary: "#1E293B", glow: "rgba(59, 79, 115, 0.35)" },
  ];

  let RESOLUTIONS = [
    { id: "1080p", name: "1080p Full HD", width: 1920, height: 1080, description: "1920x1080 · Fast export (~300 MB RAM, default)" },
    { id: "1440p", name: "1440p QHD", width: 2560, height: 1440, description: "2560x1440 · Sharper fullscreen & YouTube VP9 (~490 MB RAM)" },
    { id: "4k", name: "4K UHD", width: 3840, height: 2160, description: "3840x2160 · Max sharpness (~4x slower CPU filter load, large file)" },
  ];

  const CATEGORY_DEFS = [
    { id: "all", label: "All" },
    { id: "dark", label: "Dark / Sleep" },
    { id: "spectrum", label: "Spectrum" },
    { id: "wave", label: "Wave & 3D" },
    { id: "meters", label: "Meters" },
  ];

  const state = {
    health: null,
    sessionId: "",
    tracksMode: "upload", // "upload" | "local"
    bgMode: "upload",     // "upload" | "local" | "visualizer" | "black"
    tracks: [],           // { id, fileName, displayTitle, duration, ... }
    selectedTrackIdx: 0,
    perTrackEnabled: false,
    trackMediaLayout: "auto",   // "auto" | "fullscreen" | "card"
    trackTransition: "cut",     // "cut" | "dip"
    editingTrackId: "",
    pendingTrackUploadId: "",
    trackMediaError: "",
    dragTrackId: "",
    mediaBustCounter: 0,
    bgFile: null,
    bgObjectUrl: "",
    bgMeta: null,
    estimate: null,
    estimateTimer: 0,
    estimateSeq: 0,
    lastEstimateKey: "",
    rendering: false,
    eventSource: null,
    lastCompleteJob: null,
    visualizer: "bars",
    visualizerTheme: "studio-mint",
    visualizerResolution: "1080p",
    visualizerGlow: true,
    visualizerBrightness: 100,
    fadeToBlackMin: 0,
    dimTitles: false,
    vizCategory: "all",
    vizPage: 0,
    fadePreviewStartMs: null,
    fadePreviewFactor: 1,
    fadePreviewState: "idle", // "idle" | "fading" | "blackout"
    auditioning: false,
    pickerBusy: false,
    leftTab: "tracks",      // "tracks" | "studio"
    rightTab: "chapters",   // "chapters" | "output" | "telemetry"
    fsModal: {
      open: false,
      mode: "outDir", // "outDir" | "audioDir"
      current: "",
      parent: "",
      roots: [],
      entries: [],
      returnFocusEl: null,
    },
  };

  const $ = (id) => document.getElementById(id);

  function extOf(name) {
    const idx = name.lastIndexOf(".");
    return idx >= 0 ? name.slice(idx).toLowerCase() : "";
  }

  function baseName(path) {
    if (!path) return "";
    const parts = path.replace(/\\/g, "/").split("/");
    return parts[parts.length - 1] || path;
  }

  function normalizeFilename(raw, defaultExt, fallback) {
    const trimmed = String(raw || "").trim();
    if (!trimmed || trimmed.endsWith("/") || trimmed.endsWith("\\")) return fallback;
    const base = baseName(trimmed);
    if (!base || base === "." || base === "..") return fallback;
    const dotIdx = base.lastIndexOf(".");
    if (dotIdx === -1) {
      return `${trimmed}${defaultExt}`;
    }
    return trimmed;
  }

  function canSaveToLanFolder() {
    return Boolean(
      state.health &&
      !state.health.localPathsAllowed &&
      typeof window.showDirectoryPicker === "function" &&
      window.isSecureContext
    );
  }

  function formatTime(sec, forceHours) {
    const s = Math.max(0, Math.round(sec));
    const hrs = Math.floor(s / 3600);
    const mins = Math.floor((s % 3600) / 60);
    const rem = s % 60;
    if (forceHours || hrs > 0) {
      return `${String(hrs).padStart(2, "0")}:${String(mins).padStart(2, "0")}:${String(rem).padStart(2, "0")}`;
    }
    return `${String(mins).padStart(2, "0")}:${String(rem).padStart(2, "0")}`;
  }

  function formatDurationHuman(sec) {
    const s = Math.max(0, Math.round(Number(sec) || 0));
    if (s < 60) {
      return `${s}s`;
    }
    const hrs = Math.floor(s / 3600);
    const mins = Math.floor((s % 3600) / 60);
    const rem = s % 60;
    if (hrs > 0) {
      return mins > 0 ? `${hrs}h ${mins}m` : `${hrs}h`;
    }
    return rem > 0 ? `${mins}m ${rem}s` : `${mins}m`;
  }

  function getPreset(id) {
    return PRESETS.find((p) => p.id === id) || PRESETS[0];
  }

  function getTheme(id) {
    return THEMES.find((t) => t.id === id) || THEMES[0];
  }

  function getResolution(id) {
    return RESOLUTIONS.find((r) => r.id === id) || RESOLUTIONS[0];
  }

  function filteredPresets() {
    if (state.vizCategory === "all") return PRESETS;
    return PRESETS.filter((p) => p.category === state.vizCategory);
  }

  function syncVizPageToSelection() {
    const list = filteredPresets();
    const idx = list.findIndex((p) => p.id === state.visualizer);
    if (idx >= 0) {
      state.vizPage = Math.floor(idx / VIZ_PAGE_SIZE);
    } else {
      state.vizPage = 0;
    }
  }

  function computeSchedule() {
    const crossfade = Number($("crossfade-slider").value) || 0;
    if (state.tracks.length === 0) {
      return { starts: [], totalDuration: 0, text: "00:00 Add .mp3 Tracks to preview chapters", validCrossfade: true, minDuration: 0 };
    }

    let minDuration = Infinity;
    const starts = [0];
    for (let i = 0; i < state.tracks.length; i++) {
      if (state.tracks[i].duration < minDuration) {
        minDuration = state.tracks[i].duration;
      }
      if (i > 0) {
        const prev = starts[i - 1] + state.tracks[i - 1].duration - crossfade;
        starts.push(Math.max(0, prev));
      }
    }

    const last = state.tracks.length - 1;
    const totalDuration = starts[last] + state.tracks[last].duration;
    const includeHours = totalDuration >= 3600;
    const lines = state.tracks.map((tr, i) => `${formatTime(starts[i], includeHours)} ${tr.displayTitle}`);
    const validCrossfade = !(crossfade > 0 && state.tracks.length > 1 && crossfade >= minDuration);
    return { starts, totalDuration, text: lines.join("\n"), validCrossfade, minDuration };
  }

  function activeBgName() {
    if (state.bgMode === "upload" && state.bgFile) {
      return state.bgFile.name;
    }
    if (state.bgMode === "local") {
      return $("bg-path-input").value.trim();
    }
    return "";
  }

  function detectRenderingPath() {
    const overlay = $("overlay-toggle").checked;
    if (state.bgMode === "black") {
      const resSpec = getResolution(state.visualizerResolution);
      return {
        path: "Path D",
        kind: "black",
        fpsLabel: overlay ? `${resSpec.id} · 10 fps · -tune stillimage · Pure #000000 Blackout` : `${resSpec.id} · 2 fps · -tune stillimage · Pure #000000 Blackout`,
      };
    }
    if (state.bgMode === "visualizer") {
      const preset = getPreset(state.visualizer);
      const resSpec = getResolution(state.visualizerResolution);
      if (preset.id === "black") {
        return {
          path: "Path D",
          kind: "visualizer",
          fpsLabel: overlay ? `${resSpec.id} · 10 fps · -tune stillimage · Pure #000000 Blackout` : `${resSpec.id} · 2 fps · -tune stillimage · Pure #000000 Blackout`,
        };
      }
      return {
        path: "Path D",
        kind: "visualizer",
        fpsLabel: `${resSpec.id} · 30 fps · FFmpeg ${preset.filter}`,
      };
    }

    const bgName = activeBgName();
    const ext = extOf(bgName);
    if (VIDEO_EXTS.has(ext)) {
      return {
        path: overlay ? "Path B" : "Path A",
        kind: "video",
        fpsLabel: overlay ? "Native FPS · Burned .ass" : "30 fps · Stream Copy Loop",
      };
    }
    if (IMAGE_EXTS.has(ext)) {
      return {
        path: "Path C",
        kind: "image",
        fpsLabel: overlay ? "10 fps · -tune stillimage" : "2 fps · -tune stillimage",
      };
    }
    return { path: "", kind: "", fpsLabel: "Select Background Visual or Auto Visualizer" };
  }

  function initTheme() {
    const saved = localStorage.getItem(THEME_KEY);
    const theme = saved === "light" ? "light" : "dark";
    applyTheme(theme);
    $("theme-toggle").addEventListener("click", () => {
      const next = document.documentElement.dataset.theme === "dark" ? "light" : "dark";
      applyTheme(next);
      localStorage.setItem(THEME_KEY, next);
    });
  }

  function applyTheme(theme) {
    document.documentElement.dataset.theme = theme;
    $("theme-label").textContent = theme === "dark" ? "Studio Paper" : "Obsidian Chassis";
  }

  async function loadHealth() {
    try {
      const res = await fetch("/api/health");
      const data = await res.json();
      state.health = data;

      if (data.supportedBackgroundExts) {
        if (Array.isArray(data.supportedBackgroundExts.video) && data.supportedBackgroundExts.video.length > 0) {
          VIDEO_EXTS = new Set(data.supportedBackgroundExts.video.map((e) => String(e).toLowerCase()));
        }
        if (Array.isArray(data.supportedBackgroundExts.image) && data.supportedBackgroundExts.image.length > 0) {
          IMAGE_EXTS = new Set(data.supportedBackgroundExts.image.map((e) => String(e).toLowerCase()));
        }
        const bgInput = $("bg-file-input");
        if (bgInput) {
          bgInput.accept = [...IMAGE_EXTS, ...VIDEO_EXTS].join(",");
        }
      }

      if (Array.isArray(data.visualizers) && data.visualizers.length > 0) {
        const prevMap = new Map(PRESETS.map((p) => [p.id, p]));
        PRESETS = data.visualizers.map((v) => {
          const fallback = prevMap.get(v.id) || {};
          return {
            id: v.id,
            name: v.name || fallback.name || v.id,
            filter: v.filter || fallback.filter || "",
            category: v.category || fallback.category || "spectrum",
            description: v.description || fallback.description || "",
            isDark: typeof v.isDark === "boolean" ? v.isDark : Boolean(fallback.isDark),
            available: typeof v.available === "boolean" ? v.available : true,
          };
        });
        const current = getPreset(state.visualizer);
        if (current && !current.available) {
          const firstReady = PRESETS.find((p) => p.available);
          if (firstReady) {
            state.visualizer = firstReady.id;
            syncVizPageToSelection();
          }
        }
      }

      if (Array.isArray(data.visualizerThemes) && data.visualizerThemes.length > 0) {
        THEMES = data.visualizerThemes.map((t) => ({
          id: t.id,
          name: t.name,
          bg: t.bg,
          primary: t.primary,
          secondary: t.secondary,
          glow: t.glow,
        }));
      }

      if (Array.isArray(data.visualizerResolutions) && data.visualizerResolutions.length > 0) {
        RESOLUTIONS = data.visualizerResolutions.map((r) => ({
          id: r.id,
          name: r.name,
          width: r.width,
          height: r.height,
          description: r.description,
        }));
      }

      const ok = data.ffmpeg && data.ffprobe;
      $("health-dot").className = "health-dot " + (ok ? "is-ok" : "is-bad");
      $("health-text").textContent = ok
        ? `${data.encoder} · ${data.goos}`
        : `Missing: ${!data.ffmpeg ? "ffmpeg " : ""}${!data.ffprobe ? "ffprobe" : ""}`.trim();

      if (!ok) {
        const banner = $("health-banner");
        banner.textContent = "ffmpeg and ffprobe must be installed and available on PATH before probing or rendering.";
        banner.hidden = false;
      }

      if (data.defaults) {
        if (data.defaults.audioDir) {
          $("audio-dir-input").value = data.defaults.audioDir;
        }
        if (data.defaults.bg && data.localPathsAllowed) {
          $("bg-path-input").value = data.defaults.bg;
          setBgMode("local");
        }
        if (data.defaults.outDir && data.localPathsAllowed) {
          $("out-dir-input").value = data.defaults.outDir;
        }
        if (data.defaults.out) {
          $("out-path-input").value = data.defaults.out;
        }
        if (data.defaults.timestamps) {
          $("timestamps-path-input").value = data.defaults.timestamps;
        }
      }

      if (!data.localPathsAllowed) {
        $("tracks-mode-local").disabled = true;
        $("bg-mode-local").disabled = true;
        $("tracks-mode-local").title = "Disabled on LAN / --host 0.0.0.0 sessions";
        $("bg-mode-local").title = "Disabled on LAN / --host 0.0.0.0 sessions";
        $("out-dir-row").hidden = true;
        $("out-dir-input").disabled = true;
        $("browse-out-dir-btn").disabled = true;
        $("browse-audio-dir-btn").hidden = true;
        $("browse-audio-dir-btn").disabled = true;
        $("browse-bg-path-btn").hidden = true;
        $("browse-bg-path-btn").disabled = true;
        $("lan-output-note").hidden = false;
        setTracksMode("upload");
        if (state.bgMode === "local") {
          setBgMode("upload");
        }
      } else {
        $("out-dir-row").hidden = false;
        $("out-dir-input").disabled = false;
        $("browse-out-dir-btn").disabled = false;
        $("browse-audio-dir-btn").hidden = false;
        $("browse-audio-dir-btn").disabled = false;
        $("browse-bg-path-btn").hidden = false;
        $("browse-bg-path-btn").disabled = false;
        $("lan-output-note").hidden = true;
      }
      renderAll();
    } catch (err) {
      $("health-dot").className = "health-dot is-bad";
      $("health-text").textContent = "Server unreachable";
    }
  }

  function setTracksMode(mode) {
    state.tracksMode = mode;
    const isUpload = mode === "upload";
    $("tracks-mode-upload").classList.toggle("is-active", isUpload);
    $("tracks-mode-local").classList.toggle("is-active", !isUpload);
    $("tracks-mode-upload").setAttribute("aria-selected", String(isUpload));
    $("tracks-mode-local").setAttribute("aria-selected", String(!isUpload));
    $("tracks-upload-pane").hidden = !isUpload;
    $("tracks-local-pane").hidden = isUpload;
  }

  function setBgMode(mode) {
    if (mode !== "upload" && mode !== "local" && mode !== "visualizer" && mode !== "black") return;
    state.bgMode = mode;
    ["upload", "local", "visualizer", "black"].forEach((m) => {
      const btn = $(`bg-mode-${m}`);
      const active = mode === m;
      if (btn) {
        btn.classList.toggle("is-active", active);
        btn.setAttribute("aria-selected", String(active));
      }
    });
    $("bg-upload-pane").hidden = mode !== "upload";
    $("bg-local-pane").hidden = mode !== "local";
    $("bg-visualizer-pane").hidden = mode !== "visualizer";
    if ($("bg-black-pane")) $("bg-black-pane").hidden = mode !== "black";
    if (mode === "visualizer") {
      resetStageBuffers();
      startStageLoop();
    } else {
      clearFadePreview();
      stopStageLoop();
    }
    renderAll();
  }

  async function probeUploadedFiles(fileList) {
    const files = Array.from(fileList || []).filter((f) => extOf(f.name) === ".mp3");
    if (files.length === 0) {
      appendLog("Select one or more .mp3 files.");
      return;
    }

    const fd = new FormData();
    for (const f of files) {
      fd.append("tracks", f, f.name);
    }

    appendLog(`Probing ${files.length} uploaded Track(s)…`);
    try {
      const res = await fetch("/api/probe", { method: "POST", body: fd });
      const data = await res.json();
      if (!res.ok) {
        throw new Error(data.error || "Probe failed");
      }
      applyProbeResult(data);
    } catch (err) {
      appendLog(`Probe error: ${err.message}`);
    }
  }

  async function probeLocalFolder() {
    const audioDir = $("audio-dir-input").value.trim();
    if (!audioDir) return;
    appendLog(`Probing local folder ${audioDir}…`);
    try {
      const res = await fetch("/api/probe", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ audioDir, shuffle: $("shuffle-toggle").checked }),
      });
      const data = await res.json();
      if (!res.ok) {
        throw new Error(data.error || "Local probe failed");
      }
      applyProbeResult(data);
    } catch (err) {
      appendLog(`Probe error: ${err.message}`);
    }
  }

  function applyProbeResult(data) {
    stopAudition();
    state.sessionId = data.sessionId || "";
    const rawTracks = Array.isArray(data.tracks) ? data.tracks : [];
    state.tracks = rawTracks.map((t) => {
      const defSrc = t.defaultMediaSource || "inherit";
      const defMode = t.defaultLayerMode || "backdrop";
      let mediaName = "";
      let mediaKind = "";
      if (defSrc === "sidecar") {
        mediaName = t.sidecarName || "";
        mediaKind = t.sidecarKind || "";
      } else if (defSrc === "id3") {
        mediaName = "Embedded Cover (ID3)";
        mediaKind = "image";
      }
      return {
        ...t,
        mediaSource: defSrc,
        mediaName,
        mediaKind,
        layerMode: defMode,
        explicitEmpty: false,
        previewSrc: t.previewUrl || "",
      };
    });
    if ($("shuffle-toggle").checked && state.tracksMode === "upload") {
      shuffleTracksInPlace();
    }
    state.selectedTrackIdx = 0;
    state.perTrackEnabled = false;
    state.editingTrackId = "";
    state.trackMediaError = "";
    appendLog(`Loaded ${state.tracks.length} Track(s).`);
    renderAll();
  }

  function shuffleTracksInPlace() {
    for (let i = state.tracks.length - 1; i > 0; i--) {
      const j = Math.floor(Math.random() * (i + 1));
      const tmp = state.tracks[i];
      state.tracks[i] = state.tracks[j];
      state.tracks[j] = tmp;
    }
  }

  function moveTrack(index, delta) {
    const target = index + delta;
    if (target < 0 || target >= state.tracks.length) return;
    const tmp = state.tracks[index];
    state.tracks[index] = state.tracks[target];
    state.tracks[target] = tmp;
    if (state.selectedTrackIdx === index) {
      state.selectedTrackIdx = target;
    } else if (state.selectedTrackIdx === target) {
      state.selectedTrackIdx = index;
    }
    renderAll();
  }

  function removeTrack(index) {
    const wasSelected = index === state.selectedTrackIdx;
    state.tracks.splice(index, 1);
    if (state.tracks.length === 0) {
      stopAudition();
      state.selectedTrackIdx = 0;
    } else if (index < state.selectedTrackIdx) {
      state.selectedTrackIdx = Math.max(0, state.selectedTrackIdx - 1);
    } else if (state.selectedTrackIdx >= state.tracks.length) {
      state.selectedTrackIdx = Math.max(0, state.tracks.length - 1);
      if (wasSelected && state.auditioning) {
        playSelectedTrackAudition();
      }
    } else if (wasSelected && state.auditioning) {
      playSelectedTrackAudition();
    }
    renderAll();
  }

  function probeClientBgMeta(file, objectUrl, ext) {
    if (!file || !objectUrl) return;
    if (IMAGE_EXTS.has(ext) || ext === ".gif") {
      const img = new Image();
      img.onload = () => {
        if (state.bgObjectUrl !== objectUrl) return;
        state.bgMeta = {
          durationSec: 0,
          fps: 0,
          width: img.naturalWidth || 0,
          height: img.naturalHeight || 0,
        };
        scheduleEstimate(true);
      };
      img.src = objectUrl;
      return;
    }
    if (VIDEO_EXTS.has(ext)) {
      const vid = document.createElement("video");
      vid.preload = "metadata";
      vid.muted = true;
      vid.onloadedmetadata = () => {
        if (state.bgObjectUrl !== objectUrl) return;
        state.bgMeta = {
          durationSec: Number.isFinite(vid.duration) && vid.duration > 0 ? vid.duration : 0,
          fps: 0,
          width: vid.videoWidth || 0,
          height: vid.videoHeight || 0,
        };
        vid.removeAttribute("src");
        scheduleEstimate(true);
      };
      vid.onerror = () => {
        vid.removeAttribute("src");
      };
      vid.src = objectUrl;
    }
  }

  function handleBgFile(file) {
    if (!file) return;
    const ext = extOf(file.name);
    if (!VIDEO_EXTS.has(ext) && !IMAGE_EXTS.has(ext)) {
      appendLog(`Unsupported Background Visual extension ${ext || "(none)"}.`);
      return;
    }
    if (state.bgObjectUrl) {
      URL.revokeObjectURL(state.bgObjectUrl);
    }
    state.bgFile = file;
    state.bgObjectUrl = URL.createObjectURL(file);
    state.bgMeta = null;
    state.estimate = null;
    state.lastEstimateKey = "";
    $("bg-upload-label").textContent = file.name;
    probeClientBgMeta(file, state.bgObjectUrl, ext);
    renderAll();
  }

  function setLeftTab(tab) {
    if (tab !== "tracks" && tab !== "studio") return;
    state.leftTab = tab;
    const isTracks = tab === "tracks";
    const tabTracks = $("left-tab-tracks");
    const tabStudio = $("left-tab-studio");
    if (tabTracks && tabStudio) {
      tabTracks.classList.toggle("is-active", isTracks);
      tabStudio.classList.toggle("is-active", !isTracks);
      tabTracks.setAttribute("aria-selected", String(isTracks));
      tabStudio.setAttribute("aria-selected", String(!isTracks));
    }
    const paneTracks = $("left-pane-tracks");
    const paneStudio = $("left-pane-studio");
    if (paneTracks) paneTracks.hidden = !isTracks;
    if (paneStudio) paneStudio.hidden = isTracks;
  }

  function setRightTab(tab) {
    if (tab !== "chapters" && tab !== "output" && tab !== "telemetry") return;
    state.rightTab = tab;
    ["chapters", "output", "telemetry"].forEach((name) => {
      const btn = $(`right-tab-${name}`);
      const pane = $(`right-pane-${name}`);
      const active = tab === name;
      if (btn) {
        btn.classList.toggle("is-active", active);
        btn.setAttribute("aria-selected", String(active));
      }
      if (pane) {
        pane.hidden = !active;
      }
    });
  }

  function setTelemetryTabBadge(label, statusClass) {
    const badge = $("telemetry-tab-badge");
    if (!badge) return;
    badge.textContent = label;
    badge.classList.remove("is-running", "is-done", "is-error");
    if (statusClass) {
      badge.classList.add(statusClass);
    }
  }

  function renderAuditionBar() {
    const btn = $("audition-btn");
    const stageBtn = $("stage-audition-btn");
    const nameEl = $("audition-track-name");
    const badge = $("audio-source-badge");
    const tr = state.tracks[state.selectedTrackIdx];
    const canAudition = Boolean(state.sessionId && tr && tr.id);

    btn.disabled = !canAudition;
    if (stageBtn) stageBtn.disabled = !canAudition;
    if (state.auditioning && canAudition) {
      btn.textContent = "\u25A0 Stop Audition";
      btn.classList.remove("btn-mint");
      btn.classList.add("btn-gold");
      if (stageBtn) {
        stageBtn.textContent = "\u25A0 Stop";
        stageBtn.classList.remove("btn-mint");
        stageBtn.classList.add("btn-gold");
      }
      nameEl.textContent = tr.displayTitle;
      badge.textContent = "Track Stream";
      badge.classList.add("is-live");
    } else {
      btn.textContent = "\u25B6 Audition Selected Track";
      btn.classList.add("btn-mint");
      btn.classList.remove("btn-gold");
      if (stageBtn) {
        stageBtn.textContent = "\u25B6 Audition";
        stageBtn.classList.add("btn-mint");
        stageBtn.classList.remove("btn-gold");
      }
      nameEl.textContent = tr ? tr.displayTitle : "";
      badge.textContent = "Ambient Sim";
      badge.classList.remove("is-live");
    }
  }

  function renderStageTrackSelect() {
    const sel = $("stage-track-select");
    if (!sel) return;
    sel.innerHTML = "";
    if (state.tracks.length === 0) {
      const opt = document.createElement("option");
      opt.value = "0";
      opt.textContent = "No Tracks loaded";
      sel.appendChild(opt);
      sel.disabled = true;
      return;
    }
    sel.disabled = false;
    state.tracks.forEach((tr, idx) => {
      const opt = document.createElement("option");
      opt.value = String(idx);
      opt.textContent = `${idx + 1}. ${tr.displayTitle}`;
      if (idx === state.selectedTrackIdx) {
        opt.selected = true;
      }
      sel.appendChild(opt);
    });
  }

  function trackSourceLabel(tr) {
    if (!tr || tr.explicitEmpty || tr.layerMode === "backdrop") return "L0";
    switch (tr.mediaSource) {
      case "upload": return "Uploaded";
      case "local": return "Local";
      case "sidecar": return "Sidecar";
      case "id3": return "ID3 Art";
      default: return "L0";
    }
  }

  function reorderTrackById(dragId, targetId, placeAfter) {
    if (!dragId || !targetId || dragId === targetId) return;
    const fromIdx = state.tracks.findIndex((t) => t.id === dragId);
    let toIdx = state.tracks.findIndex((t) => t.id === targetId);
    if (fromIdx < 0 || toIdx < 0) return;

    const selectedTrack = state.tracks[state.selectedTrackIdx];
    const copy = [...state.tracks];
    const [moved] = copy.splice(fromIdx, 1);
    toIdx = copy.findIndex((t) => t.id === targetId);
    if (toIdx < 0) return;
    if (placeAfter) toIdx += 1;
    copy.splice(toIdx, 0, moved);
    state.tracks = copy;
    if (selectedTrack) {
      const nextSel = state.tracks.findIndex((t) => t.id === selectedTrack.id);
      if (nextSel >= 0) state.selectedTrackIdx = nextSel;
    }
    renderAll();
  }

  function applyTrackMediaResponse(trackId, data) {
    const idx = state.tracks.findIndex((t) => t.id === trackId);
    if (idx < 0) return;
    const tr = state.tracks[idx];
    tr.mediaSource = data.source || "inherit";
    tr.mediaName = data.mediaName || "";
    tr.mediaKind = data.mediaKind || "";
    tr.layerMode = data.layerMode || "backdrop";
    tr.explicitEmpty = Boolean(data.explicitEmpty);
    tr.previewSrc = data.previewUrl ? `${data.previewUrl}?v=${++state.mediaBustCounter}` : "";
    state.trackMediaError = "";
    state.selectedTrackIdx = idx;
  }

  async function uploadTrackMedia(trackId, file) {
    if (!state.sessionId || !trackId || !file) return;
    const ext = extOf(file.name);
    if (!VIDEO_EXTS.has(ext) && !IMAGE_EXTS.has(ext)) {
      state.trackMediaError = `Unsupported media extension ${ext || "(none)"}.`;
      renderTracksTable(computeSchedule());
      return;
    }
    const fd = new FormData();
    fd.append("file", file, file.name);
    try {
      const res = await fetch(`/api/sessions/${encodeURIComponent(state.sessionId)}/tracks/${encodeURIComponent(trackId)}/media`, {
        method: "POST",
        body: fd,
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || "Track media upload failed");
      applyTrackMediaResponse(trackId, data);
      state.perTrackEnabled = true;
      renderAll();
    } catch (err) {
      state.trackMediaError = err.message;
      renderTracksTable(computeSchedule());
    }
  }

  async function bindTrackLocalMedia(trackId, localPath) {
    if (!state.sessionId || !trackId) return;
    const trimmed = String(localPath || "").trim();
    if (!trimmed) {
      state.trackMediaError = "Enter a local image or video path.";
      renderTracksTable(computeSchedule());
      return;
    }
    try {
      const res = await fetch(`/api/sessions/${encodeURIComponent(state.sessionId)}/tracks/${encodeURIComponent(trackId)}/media`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ localPath: trimmed }),
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || "Failed to bind local Track media");
      applyTrackMediaResponse(trackId, data);
      state.perTrackEnabled = true;
      renderAll();
    } catch (err) {
      state.trackMediaError = err.message;
      renderTracksTable(computeSchedule());
    }
  }

  async function revertTrackMediaToDefault(trackId) {
    if (!state.sessionId || !trackId) return;
    try {
      const res = await fetch(`/api/sessions/${encodeURIComponent(state.sessionId)}/tracks/${encodeURIComponent(trackId)}/media`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ revertToDefault: true }),
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || "Failed to restore default Track media");
      applyTrackMediaResponse(trackId, data);
      state.perTrackEnabled = true;
      renderAll();
    } catch (err) {
      state.trackMediaError = err.message;
      renderTracksTable(computeSchedule());
    }
  }

  async function setTrackLayerMode(trackId, mode) {
    const idx = state.tracks.findIndex((t) => t.id === trackId);
    if (idx < 0 || !state.sessionId) return;
    const tr = state.tracks[idx];
    if (mode === "backdrop") {
      try {
        const res = await fetch(`/api/sessions/${encodeURIComponent(state.sessionId)}/tracks/${encodeURIComponent(trackId)}/media?mode=backdrop`, {
          method: "DELETE",
        });
        const data = await res.json();
        if (!res.ok) throw new Error(data.error || "Failed to set L0 Only");
        applyTrackMediaResponse(trackId, data);
        state.perTrackEnabled = true;
        renderAll();
      } catch (err) {
        state.trackMediaError = err.message;
        renderTracksTable(computeSchedule());
      }
      return;
    }

    const hasMedia = Boolean(tr.previewSrc) || (tr.defaultMediaSource === "sidecar" || tr.defaultMediaSource === "id3") || tr.mediaSource === "upload" || tr.mediaSource === "local";
    if (!hasMedia) {
      state.editingTrackId = trackId;
      state.trackMediaError = "Attach an image or video first before selecting Fullscreen or Art Card.";
      state.selectedTrackIdx = idx;
      renderTracksTable(computeSchedule());
      return;
    }

    try {
      const res = await fetch(`/api/sessions/${encodeURIComponent(state.sessionId)}/tracks/${encodeURIComponent(trackId)}/media`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ layerMode: mode }),
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || "Failed to update layer mode");
      applyTrackMediaResponse(trackId, data);
      state.perTrackEnabled = true;
      renderAll();
    } catch (err) {
      state.editingTrackId = trackId;
      state.trackMediaError = err.message;
      renderTracksTable(computeSchedule());
    }
  }

  function renderPerTrackBar() {
    const toggle = $("per-track-toggle");
    const hint = $("per-track-hint");
    const layoutSel = $("track-layout-select");
    const transSel = $("track-transition-select");
    if (!toggle || !hint) return;

    toggle.checked = state.perTrackEnabled;
    if (layoutSel) layoutSel.value = state.trackMediaLayout;
    if (transSel) transSel.value = state.trackTransition;

    const autoPaired = state.tracks.filter((t) => t.defaultMediaSource === "sidecar" || t.defaultMediaSource === "id3").length;
    const customAttached = state.tracks.filter((t) => t.mediaSource === "upload" || t.mediaSource === "local").length;
    if (!state.perTrackEnabled) {
      hint.textContent = autoPaired > 0
        ? `Off · ${autoPaired} Track(s) have auto-paired art (sidecar / ID3)`
        : "Off — using Layer 0 Mix Backdrop only";
    } else {
      const parts = ["Active · Option A"];
      if (customAttached > 0) parts.push(`${customAttached} custom`);
      if (autoPaired > 0) parts.push(`${autoPaired} auto-paired`);
      parts.push(state.trackTransition === "dip" ? "0.5s dip" : "cut");
      hint.textContent = parts.join(" · ");
    }
  }

  function renderTracksTable(sched) {
    const badgeEl = $("left-tracks-badge");
    const durEl = $("left-tab-duration");
    if (badgeEl) badgeEl.textContent = `(${state.tracks.length})`;
    if (durEl) durEl.textContent = formatTime(sched.totalDuration, false);

    renderPerTrackBar();

    const tbody = $("track-tbody");
    tbody.innerHTML = "";
    if (state.tracks.length === 0) {
      const row = document.createElement("tr");
      row.className = "empty-row";
      const cell = document.createElement("td");
      cell.colSpan = 5;
      cell.textContent = "No Tracks loaded. Upload .mp3 files or probe a local folder.";
      row.appendChild(cell);
      tbody.appendChild(row);
      $("tracks-summary").textContent = "0 Tracks · 00:00";
      renderStageTrackSelect();
      renderAuditionBar();
      return;
    }

    $("tracks-summary").textContent = `${state.tracks.length} Track(s) · ${formatTime(sched.totalDuration, false)}`;
    renderStageTrackSelect();
    const includeHours = sched.totalDuration >= 3600;
    const localAllowed = Boolean(state.health && state.health.localPathsAllowed);

    state.tracks.forEach((tr, idx) => {
      const row = document.createElement("tr");
      row.className = "track-row" + (idx === state.selectedTrackIdx ? " is-selected" : "");
      row.draggable = !state.rendering && state.tracks.length > 1;
      row.dataset.trackId = tr.id;

      row.addEventListener("dragstart", (e) => {
        if (e.target.closest("button, input, select, a")) {
          e.preventDefault();
          return;
        }
        state.dragTrackId = tr.id;
        row.classList.add("is-dragging");
        if (e.dataTransfer) {
          e.dataTransfer.effectAllowed = "move";
          e.dataTransfer.setData("text/plain", tr.id);
        }
      });
      row.addEventListener("dragover", (e) => {
        if (!state.dragTrackId || state.dragTrackId === tr.id) return;
        e.preventDefault();
        const rect = row.getBoundingClientRect();
        const after = e.clientY > rect.top + rect.height / 2;
        row.classList.toggle("is-drag-over-top", !after);
        row.classList.toggle("is-drag-over-bottom", after);
      });
      row.addEventListener("dragleave", () => {
        row.classList.remove("is-drag-over-top", "is-drag-over-bottom");
      });
      row.addEventListener("drop", (e) => {
        e.preventDefault();
        const rect = row.getBoundingClientRect();
        const after = e.clientY > rect.top + rect.height / 2;
        row.classList.remove("is-drag-over-top", "is-drag-over-bottom");
        const dragId = state.dragTrackId || (e.dataTransfer ? e.dataTransfer.getData("text/plain") : "");
        state.dragTrackId = "";
        reorderTrackById(dragId, tr.id, after);
      });
      row.addEventListener("dragend", () => {
        state.dragTrackId = "";
        document.querySelectorAll(".track-row").forEach((r) => {
          r.classList.remove("is-dragging", "is-drag-over-top", "is-drag-over-bottom");
        });
      });

      row.addEventListener("click", (e) => {
        if (e.target.closest("button, input, select")) return;
        const changed = state.selectedTrackIdx !== idx;
        state.selectedTrackIdx = idx;
        renderTracksTable(sched);
        renderStage();
        renderAuditionBar();
        if (changed && state.auditioning) {
          playSelectedTrackAudition();
        }
      });

      const tdStart = document.createElement("td");
      tdStart.className = "col-start";
      tdStart.textContent = formatTime(sched.starts[idx] || 0, includeHours);

      const tdTitle = document.createElement("td");
      const titleCell = document.createElement("div");
      titleCell.className = "track-title-cell";

      const grip = document.createElement("span");
      grip.className = "drag-grip mono";
      grip.title = "Drag to reorder";
      grip.textContent = "⋮⋮";

      const showThumb = !tr.explicitEmpty && tr.layerMode !== "backdrop" && Boolean(tr.previewSrc);
      let thumbEl;
      if (showThumb && tr.mediaKind === "video") {
        thumbEl = document.createElement("video");
        thumbEl.className = "track-thumb";
        thumbEl.src = tr.previewSrc;
        thumbEl.muted = true;
        thumbEl.loop = true;
        thumbEl.autoplay = true;
        thumbEl.playsInline = true;
      } else if (showThumb) {
        thumbEl = document.createElement("img");
        thumbEl.className = "track-thumb";
        thumbEl.src = tr.previewSrc;
        thumbEl.alt = "";
      } else {
        thumbEl = document.createElement("span");
        thumbEl.className = "track-thumb track-thumb-l0 mono";
        thumbEl.textContent = "L0";
      }

      const metaDiv = document.createElement("div");
      metaDiv.className = "track-title-meta";

      const titleSpan = document.createElement("span");
      titleSpan.className = "track-display-title";
      titleSpan.textContent = tr.displayTitle;

      const subDiv = document.createElement("div");
      subDiv.className = "track-media-sub";
      const srcPill = document.createElement("span");
      const srcType = !tr.explicitEmpty && tr.layerMode !== "backdrop" ? tr.mediaSource : "inherit";
      srcPill.className = `source-pill mono is-${srcType}`;
      srcPill.textContent = trackSourceLabel(tr);
      const mediaNameSpan = document.createElement("span");
      mediaNameSpan.className = "track-media-name mono";
      mediaNameSpan.textContent = (!tr.explicitEmpty && tr.layerMode !== "backdrop" && tr.mediaName)
        ? tr.mediaName
        : "Layer 0 Mix Backdrop";
      subDiv.append(srcPill, mediaNameSpan);

      metaDiv.append(titleSpan, subDiv);
      titleCell.append(grip, thumbEl, metaDiv);
      tdTitle.appendChild(titleCell);

      const tdVisual = document.createElement("td");
      tdVisual.className = "col-visual";
      const visWrap = document.createElement("div");
      visWrap.className = "visual-controls-wrap";

      const pills = document.createElement("div");
      pills.className = "mode-pills";
      const effectiveMode = tr.explicitEmpty ? "backdrop" : (tr.layerMode || "backdrop");
      [
        { id: "fullscreen", label: "Fullscreen" },
        { id: "card", label: "Art Card" },
        { id: "backdrop", label: "L0 Only" },
      ].forEach((m) => {
        const pillBtn = document.createElement("button");
        pillBtn.type = "button";
        pillBtn.className = "mode-pill" + (effectiveMode === m.id ? " is-active" : "");
        pillBtn.textContent = m.label;
        pillBtn.disabled = state.rendering;
        pillBtn.addEventListener("click", (e) => {
          e.stopPropagation();
          setTrackLayerMode(tr.id, m.id);
        });
        pills.appendChild(pillBtn);
      });

      const pickerBtn = document.createElement("button");
      pickerBtn.type = "button";
      pickerBtn.className = "media-picker-btn" + (state.editingTrackId === tr.id ? " is-open" : "");
      pickerBtn.textContent = "Media▾";
      pickerBtn.disabled = state.rendering;
      pickerBtn.addEventListener("click", (e) => {
        e.stopPropagation();
        state.trackMediaError = "";
        state.editingTrackId = state.editingTrackId === tr.id ? "" : tr.id;
        state.selectedTrackIdx = idx;
        renderTracksTable(sched);
        renderStage();
        renderAuditionBar();
      });

      visWrap.append(pills, pickerBtn);
      tdVisual.appendChild(visWrap);

      const tdLen = document.createElement("td");
      tdLen.className = "col-len";
      tdLen.textContent = formatTime(tr.duration, false);

      const tdAct = document.createElement("td");
      tdAct.className = "col-actions";
      const wrap = document.createElement("div");
      wrap.className = "row-actions";

      const upBtn = document.createElement("button");
      upBtn.type = "button";
      upBtn.className = "icon-btn";
      upBtn.textContent = "▲";
      upBtn.ariaLabel = `Move ${tr.displayTitle} up`;
      upBtn.disabled = idx === 0 || state.rendering;
      upBtn.addEventListener("click", () => moveTrack(idx, -1));

      const downBtn = document.createElement("button");
      downBtn.type = "button";
      downBtn.className = "icon-btn";
      downBtn.textContent = "▼";
      downBtn.ariaLabel = `Move ${tr.displayTitle} down`;
      downBtn.disabled = idx === state.tracks.length - 1 || state.rendering;
      downBtn.addEventListener("click", () => moveTrack(idx, 1));

      const delBtn = document.createElement("button");
      delBtn.type = "button";
      delBtn.className = "icon-btn is-remove";
      delBtn.textContent = "×";
      delBtn.ariaLabel = `Remove ${tr.displayTitle}`;
      delBtn.disabled = state.rendering;
      delBtn.addEventListener("click", () => removeTrack(idx));

      wrap.append(upBtn, downBtn, delBtn);
      tdAct.appendChild(wrap);
      row.append(tdStart, tdTitle, tdVisual, tdLen, tdAct);
      tbody.appendChild(row);

      if (state.editingTrackId === tr.id) {
        const drawerRow = document.createElement("tr");
        drawerRow.className = "track-media-drawer-row";
        const drawerCell = document.createElement("td");
        drawerCell.colSpan = 5;

        const drawer = document.createElement("div");
        drawer.className = "track-media-drawer";

        const actions = document.createElement("div");
        actions.className = "drawer-actions";

        const uploadBtn = document.createElement("button");
        uploadBtn.type = "button";
        uploadBtn.className = "btn btn-mint";
        uploadBtn.textContent = "Upload File…";
        uploadBtn.addEventListener("click", () => {
          state.pendingTrackUploadId = tr.id;
          const fileIn = $("track-media-file-input");
          fileIn.value = "";
          fileIn.click();
        });
        actions.appendChild(uploadBtn);

        if (tr.defaultMediaSource === "sidecar" || tr.defaultMediaSource === "id3") {
          const defBtn = document.createElement("button");
          defBtn.type = "button";
          defBtn.className = "btn btn-secondary";
          defBtn.textContent = tr.defaultMediaSource === "sidecar" ? `Use Sidecar (${tr.sidecarName})` : "Use ID3 Cover";
          defBtn.addEventListener("click", () => revertTrackMediaToDefault(tr.id));
          actions.appendChild(defBtn);
        }

        const l0Btn = document.createElement("button");
        l0Btn.type = "button";
        l0Btn.className = "btn btn-secondary";
        l0Btn.textContent = "L0 Only";
        l0Btn.addEventListener("click", () => setTrackLayerMode(tr.id, "backdrop"));
        actions.appendChild(l0Btn);

        const doneBtn = document.createElement("button");
        doneBtn.type = "button";
        doneBtn.className = "btn btn-ghost";
        doneBtn.textContent = "Done";
        doneBtn.addEventListener("click", () => {
          state.editingTrackId = "";
          state.trackMediaError = "";
          renderTracksTable(sched);
        });
        actions.appendChild(doneBtn);
        drawer.appendChild(actions);

        if (localAllowed) {
          const localRow = document.createElement("div");
          localRow.className = "drawer-local-row";
          const localInput = document.createElement("input");
          localInput.type = "text";
          localInput.className = "input mono";
          localInput.placeholder = "Local path to image, GIF, or MP4…";
          const bindBtn = document.createElement("button");
          bindBtn.type = "button";
          bindBtn.className = "btn btn-secondary";
          bindBtn.textContent = "Use Local Path";
          bindBtn.addEventListener("click", () => bindTrackLocalMedia(tr.id, localInput.value));
          localRow.append(localInput, bindBtn);
          drawer.appendChild(localRow);
        }

        if (state.trackMediaError) {
          const errEl = document.createElement("p");
          errEl.className = "field-hint error-text";
          errEl.textContent = state.trackMediaError;
          drawer.appendChild(errEl);
        }

        drawerCell.appendChild(drawer);
        drawerRow.appendChild(drawerCell);
        tbody.appendChild(drawerRow);
      }
    });
    renderAuditionBar();
  }

  function renderVisualizerControls() {
    const chipsHost = $("viz-cat-chips");
    chipsHost.innerHTML = "";
    CATEGORY_DEFS.forEach((cat) => {
      const count = cat.id === "all" ? PRESETS.length : PRESETS.filter((p) => p.category === cat.id).length;
      const chip = document.createElement("button");
      chip.type = "button";
      chip.className = "cat-chip" + (state.vizCategory === cat.id ? " is-active" : "");
      chip.textContent = `${cat.label} (${count})`;
      chip.addEventListener("click", () => {
        state.vizCategory = cat.id;
        syncVizPageToSelection();
        renderVisualizerControls();
      });
      chipsHost.appendChild(chip);
    });

    const filtered = filteredPresets();
    const totalPages = Math.max(1, Math.ceil(filtered.length / VIZ_PAGE_SIZE));
    if (state.vizPage >= totalPages) state.vizPage = totalPages - 1;
    if (state.vizPage < 0) state.vizPage = 0;

    const sliceStart = state.vizPage * VIZ_PAGE_SIZE;
    const pagePresets = filtered.slice(sliceStart, sliceStart + VIZ_PAGE_SIZE);
    const activeTheme = getTheme(state.visualizerTheme);

    const grid = $("viz-preset-grid");
    grid.innerHTML = "";
    pagePresets.forEach((p) => {
      const btn = document.createElement("button");
      btn.type = "button";
      btn.className = "preset-card"
        + (p.isDark ? " is-dark-preset" : "")
        + (state.visualizer === p.id ? " is-selected" : "")
        + (!p.available ? " is-unavailable" : "");
      btn.disabled = !p.available;
      btn.setAttribute("aria-pressed", String(state.visualizer === p.id));
      if (!p.available) {
        btn.title = `${p.name} requires FFmpeg filter metadata unavailable on this system`;
      }

      const top = document.createElement("div");
      top.className = "preset-card-top";

      const svgNS = "http://www.w3.org/2000/svg";
      const svg = document.createElementNS(svgNS, "svg");
      svg.setAttribute("class", "preset-thumb");
      svg.setAttribute("viewBox", "0 0 44 26");
      svg.style.color = activeTheme.primary;
      svg.innerHTML = Object.prototype.hasOwnProperty.call(PRESET_THUMBS, p.id) ? PRESET_THUMBS[p.id] : "";

      const topRight = document.createElement("div");
      topRight.className = "preset-top-right";
      if (!p.available) {
        const unavail = document.createElement("span");
        unavail.className = "unavailable-tag mono";
        unavail.textContent = "N/A";
        topRight.appendChild(unavail);
      } else if (p.isDark) {
        const sleepTag = document.createElement("span");
        sleepTag.className = "sleep-tag mono";
        sleepTag.textContent = "Sleep";
        topRight.appendChild(sleepTag);
      }
      const checkDot = document.createElement("span");
      checkDot.className = "preset-check";
      checkDot.setAttribute("aria-hidden", "true");
      topRight.appendChild(checkDot);

      top.append(svg, topRight);

      const metaWrap = document.createElement("div");
      const nameEl = document.createElement("div");
      nameEl.className = "preset-name";
      nameEl.textContent = p.name;
      const slugEl = document.createElement("div");
      slugEl.className = "preset-slug mono";
      slugEl.textContent = `${p.id} · ${p.filter}`;
      metaWrap.append(nameEl, slugEl);

      const descEl = document.createElement("div");
      descEl.className = "preset-desc";
      descEl.textContent = p.description;

      btn.append(top, metaWrap, descEl);
      btn.addEventListener("click", () => {
        if (!p.available) return;
        if (state.visualizer !== p.id) {
          state.visualizer = p.id;
          resetStageBuffers();
          clearFadePreview();
        }
        renderAll();
      });
      grid.appendChild(btn);
    });

    const pager = $("viz-pager");
    pager.hidden = totalPages <= 1;
    if (totalPages > 1) {
      $("viz-page-prev").disabled = state.vizPage === 0;
      $("viz-page-next").disabled = state.vizPage >= totalPages - 1;
      $("viz-page-info").textContent = `${state.vizPage + 1} / ${totalPages}`;
      const dotsHost = $("viz-page-dots");
      dotsHost.innerHTML = "";
      for (let i = 0; i < totalPages; i++) {
        const dot = document.createElement("button");
        dot.type = "button";
        dot.className = "preset-page-dot" + (i === state.vizPage ? " is-active" : "");
        dot.setAttribute("aria-label", `Page ${i + 1} of ${totalPages}`);
        dot.setAttribute("aria-selected", String(i === state.vizPage));
        dot.addEventListener("click", () => {
          state.vizPage = i;
          renderVisualizerControls();
        });
        dotsHost.appendChild(dot);
      }
    }

    const isBlack = state.visualizer === "black";
    const themeSelect = $("viz-theme-select");
    themeSelect.innerHTML = "";
    THEMES.forEach((th) => {
      const opt = document.createElement("option");
      opt.value = th.id;
      opt.textContent = th.name;
      if (th.id === state.visualizerTheme) opt.selected = true;
      themeSelect.appendChild(opt);
    });
    themeSelect.disabled = isBlack;

    const glowToggle = $("viz-glow-toggle");
    glowToggle.checked = state.visualizerGlow;
    glowToggle.disabled = isBlack;

    const resSelect = $("viz-resolution-select");
    if (resSelect) {
      resSelect.innerHTML = "";
      RESOLUTIONS.forEach((res) => {
        const opt = document.createElement("option");
        opt.value = res.id;
        opt.textContent = `${res.name} (${res.width}x${res.height})`;
        if (res.id === state.visualizerResolution) opt.selected = true;
        resSelect.appendChild(opt);
      });
    }
    const resHint = $("viz-resolution-hint");
    if (resHint) {
      resHint.textContent = getResolution(state.visualizerResolution).description;
    }

    $("darkness-mode-note").textContent = isBlack ? "Pure #000000 blackout (0% light)" : "Applies across any visualizer preset";
    $("viz-brightness-slider").value = String(state.visualizerBrightness);
    $("viz-brightness-slider").disabled = isBlack;
    $("viz-brightness-val").textContent = isBlack ? "0% (Blackout)" : `${state.visualizerBrightness}%`;

    document.querySelectorAll("#darkness-box .quick-pill").forEach((pill) => {
      const val = Number(pill.dataset.dim);
      pill.disabled = isBlack;
      pill.classList.toggle("is-active", !isBlack && val === state.visualizerBrightness);
    });

    const fadeSel = $("viz-fade-select");
    fadeSel.value = String(state.fadeToBlackMin);
    fadeSel.disabled = isBlack;
    const testFadeBtn = $("test-fade-btn");
    testFadeBtn.disabled = isBlack;
    testFadeBtn.textContent = state.fadePreviewState === "idle" ? "Test 3s Fade" : "Restore Preview";
    $("blackout-fade-hint").hidden = !isBlack;
    $("dim-titles-toggle").checked = state.dimTitles;
  }

  function setStageMediaSlot(imgEl, vidEl, src, kind) {
    if (!imgEl || !vidEl) return;
    if (!src || !kind) {
      imgEl.hidden = true;
      imgEl.removeAttribute("src");
      delete imgEl.dataset.currentSrc;
      vidEl.hidden = true;
      vidEl.removeAttribute("src");
      return;
    }
    if (kind === "video") {
      imgEl.hidden = true;
      imgEl.removeAttribute("src");
      delete imgEl.dataset.currentSrc;
      if (vidEl.getAttribute("src") !== src) {
        vidEl.src = src;
      }
      vidEl.hidden = false;
    } else {
      vidEl.hidden = true;
      vidEl.removeAttribute("src");
      if (imgEl.dataset.currentSrc !== src) {
        imgEl.dataset.currentSrc = src;
        imgEl.src = src;
      }
      imgEl.hidden = false;
    }
  }

  function renderStage() {
    const info = detectRenderingPath();
    const frame = $("stage-frame");
    const img = $("stage-img");
    const vid = $("stage-video");
    const canvas = $("stage-canvas");
    const empty = $("stage-empty");
    const l1FullWrap = $("stage-l1-fullscreen");
    const l1FullImg = $("stage-l1-img");
    const l1FullVid = $("stage-l1-video");
    const l1CardWrap = $("stage-l1-card-wrap");
    const l1CardImg = $("stage-l1-card-img");
    const l1CardVid = $("stage-l1-card-video");

    const tr = state.tracks[state.selectedTrackIdx];
    const l1Active = Boolean(state.perTrackEnabled && tr && !tr.explicitEmpty && tr.layerMode !== "backdrop" && tr.previewSrc);
    const l1Mode = l1Active ? (tr.layerMode === "card" ? "card" : "fullscreen") : "backdrop";
    const showL0 = !(l1Active && l1Mode === "fullscreen");

    if (frame) {
      frame.classList.toggle("is-black-l0", showL0 && state.bgMode === "black");
    }

    if (l1Active && l1Mode === "fullscreen") {
      if (l1FullWrap) l1FullWrap.hidden = false;
      if (l1CardWrap) l1CardWrap.hidden = true;
      setStageMediaSlot(l1FullImg, l1FullVid, tr.previewSrc, tr.mediaKind || "image");
      setStageMediaSlot(l1CardImg, l1CardVid, "", "");
    } else if (l1Active && l1Mode === "card") {
      if (l1FullWrap) l1FullWrap.hidden = true;
      if (l1CardWrap) l1CardWrap.hidden = false;
      setStageMediaSlot(l1FullImg, l1FullVid, "", "");
      setStageMediaSlot(l1CardImg, l1CardVid, tr.previewSrc, tr.mediaKind || "image");
    } else {
      if (l1FullWrap) l1FullWrap.hidden = true;
      if (l1CardWrap) l1CardWrap.hidden = true;
      setStageMediaSlot(l1FullImg, l1FullVid, "", "");
      setStageMediaSlot(l1CardImg, l1CardVid, "", "");
    }

    if (showL0 && state.bgMode === "visualizer") {
      setStageMediaSlot(img, vid, "", "");
      empty.hidden = true;
      canvas.hidden = false;
      startStageLoop();
    } else if (showL0 && state.bgMode === "black") {
      canvas.hidden = true;
      $("stage-fade-pill").hidden = true;
      stopStageLoop();
      setStageMediaSlot(img, vid, "", "");
      empty.hidden = true;
    } else if (showL0) {
      canvas.hidden = true;
      $("stage-fade-pill").hidden = true;
      stopStageLoop();

      const bgName = activeBgName();
      let src = "";
      if (state.bgMode === "upload" && state.bgObjectUrl) {
        src = state.bgObjectUrl;
      } else if (state.bgMode === "local" && bgName && state.health && state.health.localPathsAllowed) {
        src = `/api/preview-bg?path=${encodeURIComponent(bgName)}`;
      }
      if (!src || !info.kind) {
        setStageMediaSlot(img, vid, "", "");
        empty.hidden = false;
      } else {
        empty.hidden = true;
        setStageMediaSlot(img, vid, src, info.kind);
      }
    } else {
      canvas.hidden = true;
      $("stage-fade-pill").hidden = true;
      stopStageLoop();
      setStageMediaSlot(img, vid, "", "");
      empty.hidden = true;
    }

    if (state.perTrackEnabled && tr) {
      const modeLabel = l1Mode === "card" ? "Art Card" : l1Mode === "fullscreen" ? "Fullscreen" : "L0 Only";
      const l0Label = state.bgMode === "visualizer" ? `viz: ${getPreset(state.visualizer).id}` : state.bgMode === "black" ? "Black" : (activeBgName() ? baseName(activeBgName()) : "L0");
      $("stage-bg-badge").textContent = `${modeLabel} · ${l0Label}`;
      $("stage-path-badge").textContent = "Option A";
      const metaEl = $("stage-fps-meta");
      if (metaEl) {
        metaEl.textContent = `Track ${state.selectedTrackIdx + 1}: ${modeLabel} · ${state.trackTransition === "dip" ? "0.5s Dip" : "Cut"} (Segmented Option A)`;
      }
    } else if (state.bgMode === "visualizer") {
      const preset = getPreset(state.visualizer);
      $("stage-bg-badge").textContent = `viz: ${preset.id}`;
      $("stage-path-badge").textContent = "Path D";
      renderStageFpsMeta(info);
    } else if (state.bgMode === "black") {
      $("stage-bg-badge").textContent = "Black (#000)";
      $("stage-path-badge").textContent = "Path D";
      renderStageFpsMeta(info);
    } else {
      const bgName = activeBgName();
      $("stage-bg-badge").textContent = bgName ? baseName(bgName) : "No visual";
      $("stage-path-badge").textContent = info.path || "Select BG";
      renderStageFpsMeta(info);
    }

    renderStageOverlay();
  }

  function renderStageOverlay() {
    const overlayEl = $("stage-overlay");
    const overlayText = $("stage-overlay-text");
    const enabled = $("overlay-toggle").checked;
    if (!enabled) {
      overlayEl.hidden = true;
      return;
    }
    const tr = state.tracks[state.selectedTrackIdx];
    const title = tr ? tr.displayTitle : "Display Title";
    overlayText.textContent = `♪ ${title}`;
    overlayText.style.fontFamily = $("font-name-input").value.trim() || "Arial";
    overlayEl.classList.toggle("is-dim", Boolean(state.dimTitles));
    overlayEl.hidden = false;
  }

  function buildTrackMediaManifest() {
    let layer0;
    if (state.bgMode === "black") {
      layer0 = { mode: "black" };
    } else if (state.bgMode === "visualizer") {
      layer0 = {
        mode: "visualizer",
        visualizer: state.visualizer,
        visualizerTheme: state.visualizerTheme,
        visualizerResolution: state.visualizerResolution,
        visualizerGlow: state.visualizerGlow,
        visualizerBrightness: state.visualizerBrightness,
      };
      if (state.fadeToBlackMin > 0 && state.visualizer !== "black") {
        layer0.fadeToBlack = `${state.fadeToBlackMin}m`;
      }
    } else {
      layer0 = {
        mode: "media",
        bgPath: activeBgName() || "<background-visual>",
      };
    }

    const tracks = state.tracks.map((tr) => {
      const effectiveMode = tr.explicitEmpty ? "backdrop" : (tr.layerMode || "backdrop");
      const entry = {
        file: tr.fileName,
        layerMode: effectiveMode,
      };
      if (effectiveMode !== "backdrop" && (tr.mediaSource === "upload" || tr.mediaSource === "local" || tr.mediaSource === "sidecar") && tr.mediaName) {
        entry.media = tr.mediaName;
      }
      return entry;
    });

    return {
      layer0,
      auto: true,
      layout: state.trackMediaLayout,
      transition: state.trackTransition,
      tracks,
    };
  }

  function downloadManifestJSON() {
    const manifest = buildTrackMediaManifest();
    const blob = new Blob([JSON.stringify(manifest, null, 2)], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "track_media_manifest.json";
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }

  function renderCLI() {
    const audioDir = state.tracksMode === "local" ? ($("audio-dir-input").value.trim() || "./tracks") : "./tracks";
    const localAllowed = Boolean(!state.health || state.health.localPathsAllowed);
    const outDir = localAllowed ? $("out-dir-input").value.trim() : "";
    const out = normalizeFilename($("out-path-input").value, ".mp4", "output.mp4");
    const ts = normalizeFilename($("timestamps-path-input").value, ".txt", "timestamps.txt");
    const crossfade = Number($("crossfade-slider").value) || 0;
    const parts = ["ytmix", "--audio-dir", quoteArg(audioDir)];

    if (state.bgMode === "black") {
      parts.push("--visualizer", "black");
      if (state.visualizerResolution && state.visualizerResolution !== "1080p") {
        parts.push("--visualizer-resolution", state.visualizerResolution);
      }
    } else if (state.bgMode === "visualizer") {
      parts.push("--visualizer", state.visualizer);
      if (state.visualizerResolution && state.visualizerResolution !== "1080p") {
        parts.push("--visualizer-resolution", state.visualizerResolution);
      }
      if (state.visualizer !== "black") {
        if (state.visualizerTheme !== "studio-mint") {
          parts.push("--visualizer-theme", state.visualizerTheme);
        }
        if (!state.visualizerGlow) {
          parts.push("--visualizer-glow=false");
        }
        if (state.visualizerBrightness < 100) {
          parts.push("--visualizer-brightness", String(state.visualizerBrightness));
        }
        if (state.fadeToBlackMin > 0) {
          parts.push("--fade-to-black", `${state.fadeToBlackMin}m`);
        }
      }
    } else {
      const bg = activeBgName() || "<background-visual>";
      parts.push("--bg", quoteArg(bg));
    }

    if (state.perTrackEnabled) {
      const hasCustom = state.tracks.some((t) =>
        t.explicitEmpty ||
        t.mediaSource === "upload" ||
        t.mediaSource === "local" ||
        (t.defaultLayerMode && t.layerMode && t.layerMode !== t.defaultLayerMode)
      );
      parts.push("--track-media", hasCustom ? "track_media_manifest.json" : "auto");
      if (state.trackMediaLayout && state.trackMediaLayout !== "auto") {
        parts.push("--track-media-layout", state.trackMediaLayout);
      }
      if (state.trackTransition && state.trackTransition !== "cut") {
        parts.push("--track-transition", state.trackTransition);
      }
    }

    if (outDir) parts.push("--out-dir", quoteArg(outDir));
    parts.push("--out", quoteArg(out));
    if (ts !== "timestamps.txt") parts.push("--timestamps", quoteArg(ts));
    if (crossfade > 0) parts.push("--crossfade", String(crossfade));
    if ($("normalize-toggle").checked) parts.push("--normalize");
    if ($("overlay-toggle").checked) {
      parts.push("--overlay-titles");
      if (state.dimTitles) {
        parts.push("--dim-titles");
      }
      const dur = Number($("title-duration-input").value) || 0;
      if (dur > 0) parts.push("--title-duration", String(dur));
      const font = $("font-name-input").value.trim();
      if (font && font !== "Arial") parts.push("--font-name", quoteArg(font));
    }
    if ($("shuffle-toggle").checked) parts.push("--shuffle");

    $("cli-preview").textContent = parts.join(" ");
    $("render-btn-target").textContent = baseName(out);

    if (state.lastCompleteJob) {
      $("download-output-link").download = baseName(out) || state.lastCompleteJob.outputName || "output.mp4";
      $("download-ts-link").download = baseName(ts) || state.lastCompleteJob.timestampsName || "timestamps.txt";
    }
  }

  function quoteArg(val) {
    return /\s/.test(val) ? `"${val}"` : val;
  }

  function renderAll() {
    const cf = Number($("crossfade-slider").value) || 0;
    $("crossfade-val").textContent = `${cf.toFixed(1)} s`;
    $("crossfade-badge").textContent = `${cf.toFixed(1)}s Crossfade`;

    const sched = computeSchedule();
    const warn = $("crossfade-warning");
    if (!sched.validCrossfade) {
      warn.textContent = `Crossfade (${cf}s) must be less than shortest Track (${sched.minDuration.toFixed(2)}s).`;
      warn.hidden = false;
    } else {
      warn.hidden = true;
    }

    renderVisualizerControls();
    renderTracksTable(sched);
    $("chapters-preview").textContent = sched.text;
    renderStage();
    renderCLI();

    const vizReady = state.bgMode === "visualizer" && Boolean(getPreset(state.visualizer).available);
    const blackReady = state.bgMode === "black";
    const hasBg = blackReady || vizReady || (Boolean(activeBgName()) && Boolean(detectRenderingPath().path));
    const hasTracks = state.tracks.length > 0;
    $("render-btn").disabled = state.rendering || !hasTracks || !hasBg || !sched.validCrossfade;

    scheduleEstimate(false);
    renderEstimateUI();
  }

  function renderStageFpsMeta(info) {
    const el = $("stage-fps-meta");
    if (!el) return;
    if (state.perTrackEnabled && state.tracks.length > 0) {
      return;
    }
    const est = state.estimate;
    if (est && est.renderingPath && est.renderingPath === info.path) {
      if (est.renderingPath === "Path A" || est.renderingPath === "Path B") {
        const fps = Math.round(est.loopFPS || 30);
        if (est.loopDurationSec > 0 && est.loopCycles > 0) {
          el.textContent = `${est.loopDurationSec.toFixed(1)}s loop · ${fps} fps · ×${est.loopCycles} cycles (${est.renderingPath})`;
          return;
        }
        el.textContent = `Loop · ${fps} fps (${est.renderingPath})`;
        return;
      }
      if (est.renderingPath === "Path C") {
        const overlay = $("overlay-toggle").checked;
        el.textContent = `Still image · ${overlay ? "10 fps" : "2 fps"} · -tune stillimage (${est.renderingPath})`;
        return;
      }
      if (est.renderingPath === "Path D") {
        el.textContent = `Auto Visualizer · ${info.fpsLabel}`;
        return;
      }
    }
    el.textContent = info.fpsLabel;
  }

  function buildTrackOverridesPayload() {
    if (!state.perTrackEnabled || !state.sessionId) return [];
    return state.tracks.map((tr) => ({
      trackId: tr.id,
      layerMode: tr.explicitEmpty ? "backdrop" : (tr.layerMode || "backdrop"),
      source: tr.explicitEmpty ? "backdrop" : (tr.mediaSource || "inherit"),
    }));
  }

  function buildEstimatePayload(sched) {
    if (!sched || !sched.validCrossfade || state.tracks.length === 0) return null;
    const info = detectRenderingPath();
    if (!info.path) return null;

    const payload = {
      bgMode: state.bgMode,
      layer0Mode: state.bgMode,
      crossfade: Number($("crossfade-slider").value) || 0,
      normalize: $("normalize-toggle").checked,
      overlayTitles: $("overlay-toggle").checked,
      segmented: state.perTrackEnabled,
      trackMediaLayout: state.trackMediaLayout,
      trackTransition: state.trackTransition,
    };
    if (state.sessionId) {
      payload.sessionId = state.sessionId;
      payload.trackIds = state.tracks.map((t) => t.id);
      if (state.perTrackEnabled) {
        payload.trackOverrides = buildTrackOverridesPayload();
      }
    } else {
      payload.trackDurations = state.tracks.map((t) => t.duration);
    }

    if (state.bgMode === "black") {
      payload.visualizer = "black";
      payload.visualizerResolution = state.visualizerResolution;
    } else if (state.bgMode === "visualizer") {
      if (!getPreset(state.visualizer).available) return null;
      payload.visualizer = state.visualizer;
      payload.visualizerTheme = state.visualizerTheme;
      payload.visualizerResolution = state.visualizerResolution;
      payload.visualizerGlow = state.visualizerGlow;
    } else if (state.bgMode === "upload") {
      if (!state.bgFile) return null;
      payload.bgName = state.bgFile.name;
      if (state.bgMeta) {
        payload.bgMeta = state.bgMeta;
      }
    } else if (state.bgMode === "local") {
      if (!state.health || !state.health.localPathsAllowed) return null;
      const bgPath = $("bg-path-input").value.trim();
      if (!bgPath) return null;
      payload.bgPath = bgPath;
    }
    return payload;
  }

  function scheduleEstimate(force) {
    const sched = computeSchedule();
    const payload = buildEstimatePayload(sched);
    if (!payload) {
      clearTimeout(state.estimateTimer);
      state.estimateTimer = 0;
      state.lastEstimateKey = "";
      if (state.estimate !== null) {
        state.estimate = null;
        renderEstimateUI();
        renderStageFpsMeta(detectRenderingPath());
      }
      return;
    }
    const key = JSON.stringify(payload);
    if (!force && key === state.lastEstimateKey) {
      return;
    }
    state.lastEstimateKey = key;
    clearTimeout(state.estimateTimer);
    state.estimateTimer = setTimeout(() => {
      fetchEstimate(payload);
    }, 140);
  }

  async function fetchEstimate(payload) {
    const seq = ++state.estimateSeq;
    try {
      const res = await fetch("/api/estimate", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      if (!res.ok) {
        return;
      }
      const data = await res.json();
      if (seq !== state.estimateSeq) return;
      state.estimate = data;
      renderEstimateUI();
      renderStageFpsMeta(detectRenderingPath());
    } catch {
      // Keep UI responsive if transient estimate request fails.
    }
  }

  function renderEstimateUI() {
    const strip = $("estimate-strip");
    const pill = $("estimate-pill");
    const legend = $("estimate-legend");
    const segAudio = $("cost-seg-audio");
    const segVideo = $("cost-seg-video");
    const btnEst = $("render-btn-est");
    if (!strip || !pill || !legend || !segAudio || !segVideo) return;

    const est = state.estimate;
    if (!est || !(est.pointSec > 0)) {
      strip.hidden = true;
      if (btnEst) btnEst.textContent = "";
      return;
    }

    strip.hidden = false;
    const lowStr = formatDurationHuman(est.lowSec);
    const highStr = formatDurationHuman(est.highSec);
    const pointStr = formatDurationHuman(est.pointSec);
    pill.textContent = `Est. ~${lowStr} – ${highStr} · ${est.renderingPath}`;

    const rawAudioW = est.weights && typeof est.weights.audio === "number" ? est.weights.audio : 0.5;
    const audioPct = Math.max(0, Math.min(100, Math.round(rawAudioW * 100)));
    const videoPct = Math.max(0, 100 - audioPct);
    legend.textContent = `Audio ${audioPct}% · Video ${videoPct}%`;
    segAudio.style.width = `${audioPct}%`;
    segVideo.style.width = `${videoPct}%`;
    if (btnEst) {
      btnEst.textContent = ` (Est. ~${pointStr})`;
    }
  }

  function resetTelemetry() {
    state.lastCompleteJob = null;
    $("progress-fill").style.width = "0%";
    $("telemetry-state").textContent = "Running";
    setTelemetryTabBadge("Running", "is-running");
    const accEl = $("telemetry-accuracy");
    if (accEl) {
      accEl.hidden = true;
      accEl.textContent = "";
    }
    $("download-bar").hidden = true;
    $("save-lan-folder-btn").hidden = true;
    $("save-lan-folder-btn").textContent = "Save to Folder…";
    $("save-lan-folder-btn").disabled = false;
    document.querySelectorAll("#step-list li").forEach((li) => {
      li.classList.remove("is-active", "is-done", "is-error");
    });
  }

  function markStep(stepNum, isDone) {
    document.querySelectorAll("#step-list li").forEach((li) => {
      const n = Number(li.dataset.step);
      if (n < stepNum) {
        li.classList.remove("is-active");
        li.classList.add("is-done");
      } else if (n === stepNum) {
        li.classList.toggle("is-done", Boolean(isDone));
        li.classList.toggle("is-active", !isDone);
      }
    });
  }

  function appendLog(line) {
    const log = $("telemetry-log");
    log.textContent = log.textContent === "Ready to render." ? line : `${log.textContent}\n${line}`;
    log.scrollTop = log.scrollHeight;
  }

  async function startRender() {
    if (state.rendering) return;
    if (state.eventSource) {
      state.eventSource.close();
      state.eventSource = null;
    }

    const outVal = normalizeFilename($("out-path-input").value, ".mp4", "output.mp4");
    const tsVal = normalizeFilename($("timestamps-path-input").value, ".txt", "timestamps.txt");
    $("out-path-input").value = outVal;
    $("timestamps-path-input").value = tsVal;

    const localAllowed = Boolean(state.health && state.health.localPathsAllowed);
    const outDirVal = localAllowed ? $("out-dir-input").value.trim() : "";

    state.rendering = true;
    resetTelemetry();
    setRightTab("telemetry");
    $("telemetry-log").textContent = "Submitting Mix configuration…";
    renderAll();

    const cfgPayload = {
      sessionId: state.sessionId,
      trackIds: state.tracks.map((t) => t.id),
      bgMode: state.bgMode,
      layer0Mode: state.bgMode,
      bgPath: state.bgMode === "local" ? $("bg-path-input").value.trim() : "",
      outDir: outDirVal,
      out: outVal,
      timestamps: tsVal,
      crossfade: Number($("crossfade-slider").value) || 0,
      normalize: $("normalize-toggle").checked,
      overlayTitles: $("overlay-toggle").checked,
      titleDuration: Math.max(0, Math.round(Number($("title-duration-input").value) || 0)),
      fontName: $("font-name-input").value.trim() || "Arial",
      shuffle: $("shuffle-toggle").checked,
      visualizer: state.bgMode === "visualizer" ? state.visualizer : (state.bgMode === "black" ? "black" : ""),
      visualizerTheme: state.visualizerTheme,
      visualizerResolution: state.visualizerResolution,
      visualizerGlow: state.visualizerGlow,
      visualizerBrightness: state.visualizerBrightness,
      fadeToBlackMinutes: (state.bgMode === "black" || state.visualizer === "black") ? 0 : state.fadeToBlackMin,
      dimTitles: state.dimTitles,
      segmented: state.perTrackEnabled,
      trackMediaLayout: state.trackMediaLayout,
      trackTransition: state.trackTransition,
      trackOverrides: buildTrackOverridesPayload(),
    };

    try {
      let res;
      if (state.bgMode === "upload" && state.bgFile) {
        const fd = new FormData();
        fd.append("config", JSON.stringify(cfgPayload));
        fd.append("bg", state.bgFile, state.bgFile.name);
        res = await fetch("/api/render", { method: "POST", body: fd });
      } else {
        res = await fetch("/api/render", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(cfgPayload),
        });
      }

      const data = await res.json();
      if (!res.ok) {
        throw new Error(data.error || "Failed to start Mix render");
      }

      appendLog(`Job ${data.jobId} started (${data.renderingPath}).`);
      subscribeSSE(data.eventsUrl);
    } catch (err) {
      state.rendering = false;
      $("telemetry-state").textContent = "Error";
      setTelemetryTabBadge("Error", "is-error");
      appendLog(`Error: ${err.message}`);
      renderAll();
    }
  }

  function subscribeSSE(eventsUrl) {
    const es = new EventSource(eventsUrl);
    state.eventSource = es;

    es.addEventListener("step", (e) => {
      const payload = JSON.parse(e.data);
      markStep(payload.step, false);
      const pct = payload.progress || 0;
      $("progress-fill").style.width = `${pct}%`;
      if (typeof payload.remainingSec === "number" && payload.remainingSec >= 0) {
        const remText = `~${formatDurationHuman(payload.remainingSec)}`;
        $("telemetry-state").textContent = `Running · ${pct}% · ${remText} left`;
        setTelemetryTabBadge(`${pct}% · ${remText}`, "is-running");
      } else {
        $("telemetry-state").textContent = `Running · ${pct}%`;
        setTelemetryTabBadge(`${pct}%`, "is-running");
      }
      appendLog(payload.message);
    });

    es.addEventListener("progress", (e) => {
      const payload = JSON.parse(e.data);
      const pct = Math.max(0, Math.min(99, Number(payload.progress) || 0));
      $("progress-fill").style.width = `${pct}%`;
      const parts = ["Running", `${pct}%`];
      if (payload.speed) {
        parts.push(payload.speed);
      }
      let badgeText = `${pct}%`;
      if (typeof payload.remainingSec === "number" && payload.remainingSec >= 0) {
        const remText = `~${formatDurationHuman(payload.remainingSec)}`;
        parts.push(`${remText} left`);
        badgeText = `${pct}% · ${remText}`;
      }
      $("telemetry-state").textContent = parts.join(" · ");
      setTelemetryTabBadge(badgeText, "is-running");
    });

    es.addEventListener("log", (e) => {
      const payload = JSON.parse(e.data);
      appendLog(payload.message);
    });

    es.addEventListener("complete", (e) => {
      const payload = JSON.parse(e.data);
      es.close();
      state.eventSource = null;
      state.rendering = false;
      state.lastCompleteJob = payload;
      markStep(6, true);
      $("progress-fill").style.width = "100%";
      $("telemetry-state").textContent = `Complete (${payload.renderingPath})`;
      setTelemetryTabBadge("Done", "is-done");
      if (typeof payload.estimatedSec === "number" && payload.estimatedSec > 0 && typeof payload.actualSec === "number") {
        const diffPct = Math.round(((payload.actualSec - payload.estimatedSec) / payload.estimatedSec) * 100);
        const sign = diffPct > 0 ? "+" : "";
        const accText = `Estimated ~${formatDurationHuman(payload.estimatedSec)} · Actual ${formatDurationHuman(payload.actualSec)} (${sign}${diffPct}%)`;
        const accEl = $("telemetry-accuracy");
        if (accEl) {
          accEl.textContent = accText;
          accEl.hidden = false;
        }
        appendLog(accText);
      }
      if (payload.timestampsText) {
        $("chapters-preview").textContent = payload.timestampsText;
      }
      const outLink = $("download-output-link");
      outLink.href = payload.outputUrl;
      outLink.download = payload.outputName || "output.mp4";
      const tsLink = $("download-ts-link");
      tsLink.href = payload.timestampsUrl;
      tsLink.download = payload.timestampsName || "timestamps.txt";

      const saveBtn = $("save-lan-folder-btn");
      saveBtn.hidden = !canSaveToLanFolder();
      saveBtn.textContent = "Save to Folder…";
      saveBtn.disabled = false;

      $("download-bar").hidden = false;
      renderAll();
    });

    es.addEventListener("error", (e) => {
      if (e.data) {
        const payload = JSON.parse(e.data);
        appendLog(`Render failed: ${payload.message}`);
      } else {
        appendLog("Telemetry connection closed.");
      }
      document.querySelectorAll("#step-list li.is-active").forEach((li) => {
        li.classList.remove("is-active");
        li.classList.add("is-error");
      });
      es.close();
      state.eventSource = null;
      state.rendering = false;
      $("telemetry-state").textContent = "Failed";
      setTelemetryTabBadge("Failed", "is-error");
      renderAll();
    });
  }

  async function saveToLanFolder() {
    if (!state.lastCompleteJob || !canSaveToLanFolder()) return;
    let dirHandle;
    try {
      dirHandle = await window.showDirectoryPicker({ mode: "readwrite" });
    } catch (err) {
      if (err && err.name === "AbortError") return;
      appendLog(`Save error: ${err ? err.message : "directory picker failed"}`);
      return;
    }

    const saveBtn = $("save-lan-folder-btn");
    saveBtn.disabled = true;
    saveBtn.textContent = "Saving…";

    const outName = baseName(normalizeFilename($("out-path-input").value, ".mp4", state.lastCompleteJob.outputName || "output.mp4"));
    const tsName = baseName(normalizeFilename($("timestamps-path-input").value, ".txt", state.lastCompleteJob.timestampsName || "timestamps.txt"));

    try {
      await writeResponseToFileHandle(dirHandle, outName, state.lastCompleteJob.outputUrl);
      await writeResponseToFileHandle(dirHandle, tsName, state.lastCompleteJob.timestampsUrl);
      saveBtn.textContent = `Saved to ${dirHandle.name || "Folder"}`;
      appendLog(`Saved ${outName} and ${tsName} to folder "${dirHandle.name || "selected"}".`);
    } catch (err) {
      saveBtn.textContent = "Save to Folder…";
      appendLog(`Save error: ${err.message}`);
    } finally {
      saveBtn.disabled = false;
    }
  }

  async function writeResponseToFileHandle(dirHandle, fileName, url) {
    const res = await fetch(url);
    if (!res.ok) {
      throw new Error(`download ${fileName} failed (${res.status})`);
    }
    const fh = await dirHandle.getFileHandle(fileName, { create: true });
    const writable = await fh.createWritable();
    if (res.body && typeof res.body.pipeTo === "function") {
      await res.body.pipeTo(writable);
      return;
    }
    const blob = await res.blob();
    await writable.write(blob);
    await writable.close();
  }

  // Web Audio Track Audition + 14 60fps Canvas Renderers
  let audioCtx = null;
  let analyser = null;
  let analyserL = null;
  let analyserR = null;
  let freqBuf = null;
  let timeBufL = null;
  let timeBufR = null;
  let mediaSourceCreated = false;
  let stageAnimId = 0;

  const specCanvas = document.createElement("canvas");
  specCanvas.width = 480;
  specCanvas.height = 270;
  const specCtx = specCanvas.getContext("2d");

  const histCanvas = document.createElement("canvas");
  histCanvas.width = 480;
  histCanvas.height = 220;
  const histCtx = histCanvas.getContext("2d");

  const phaseCanvas = document.createElement("canvas");
  phaseCanvas.width = 480;
  phaseCanvas.height = 270;
  const phaseCtx = phaseCanvas.getContext("2d");

  const radialCanvas = document.createElement("canvas");
  radialCanvas.width = 960;
  radialCanvas.height = 540;
  const radialCtx = radialCanvas.getContext("2d");

  const scopeHistory = [];
  const MAX_SCOPE_HISTORY = 24;
  const peakHold = { L: 0, R: 0, lSmooth: 0, rSmooth: 0 };

  function resetStageBuffers() {
    specCtx.clearRect(0, 0, specCanvas.width, specCanvas.height);
    histCtx.clearRect(0, 0, histCanvas.width, histCanvas.height);
    phaseCtx.clearRect(0, 0, phaseCanvas.width, phaseCanvas.height);
    radialCtx.clearRect(0, 0, radialCanvas.width, radialCanvas.height);
    scopeHistory.length = 0;
    peakHold.L = 0;
    peakHold.R = 0;
    peakHold.lSmooth = 0;
    peakHold.rSmooth = 0;
  }

  function clearFadePreview() {
    state.fadePreviewStartMs = null;
    state.fadePreviewFactor = 1;
    state.fadePreviewState = "idle";
    const overlayEl = $("stage-overlay");
    if (overlayEl) {
      overlayEl.style.opacity = "";
    }
  }

  function ensureAudioGraph() {
    if (!audioCtx) {
      const AC = window.AudioContext || window.webkitAudioContext;
      if (!AC) {
        throw new Error("Web Audio API is not supported in this browser");
      }
      audioCtx = new AC();
      analyser = audioCtx.createAnalyser();
      analyser.fftSize = 1024;
      analyser.smoothingTimeConstant = 0.82;

      const splitter = audioCtx.createChannelSplitter(2);
      analyserL = audioCtx.createAnalyser();
      analyserR = audioCtx.createAnalyser();
      analyserL.fftSize = 512;
      analyserR.fftSize = 512;

      analyser.connect(splitter);
      splitter.connect(analyserL, 0);
      splitter.connect(analyserR, 1);
      analyser.connect(audioCtx.destination);

      freqBuf = new Uint8Array(analyser.frequencyBinCount);
      timeBufL = new Uint8Array(analyserL.fftSize);
      timeBufR = new Uint8Array(analyserR.fftSize);
    }
    if (!mediaSourceCreated) {
      const audioEl = $("audition-audio");
      const mediaSrc = audioCtx.createMediaElementSource(audioEl);
      mediaSrc.connect(analyser);
      mediaSourceCreated = true;
    }
    if (audioCtx.state === "suspended") {
      audioCtx.resume().catch(() => {});
    }
  }

  async function playSelectedTrackAudition() {
    const tr = state.tracks[state.selectedTrackIdx];
    if (!state.sessionId || !tr || !tr.id) {
      stopAudition();
      return;
    }
    try {
      ensureAudioGraph();
      const audioEl = $("audition-audio");
      const nextSrc = `/api/sessions/${encodeURIComponent(state.sessionId)}/tracks/${encodeURIComponent(tr.id)}/audio`;
      if (audioEl.getAttribute("src") !== nextSrc) {
        audioEl.src = nextSrc;
      }
      await audioEl.play();
      state.auditioning = true;
      renderAuditionBar();
    } catch (err) {
      state.auditioning = false;
      renderAuditionBar();
      appendLog(`Audio audition error: ${err && err.message ? err.message : "playback failed"}`);
    }
  }

  function stopAudition() {
    const audioEl = $("audition-audio");
    if (audioEl) {
      audioEl.pause();
      audioEl.removeAttribute("src");
    }
    if (state.auditioning) {
      state.auditioning = false;
      renderAuditionBar();
    }
  }

  function toggleAudition() {
    if (state.auditioning) {
      stopAudition();
    } else {
      playSelectedTrackAudition();
    }
  }

  const simFreq = new Float32Array(64);
  const simWaveL = new Float32Array(256);
  const simWaveR = new Float32Array(256);

  function sampleAudio(t) {
    if (state.auditioning && analyser) {
      analyser.getByteFrequencyData(freqBuf);
      analyserL.getByteTimeDomainData(timeBufL);
      analyserR.getByteTimeDomainData(timeBufR);

      for (let i = 0; i < 64; i++) {
        const logIdx = Math.floor(Math.pow(i / 63, 1.7) * (freqBuf.length * 0.65));
        simFreq[i] = freqBuf[Math.min(freqBuf.length - 1, logIdx)] / 255;
      }
      for (let i = 0; i < 256; i++) {
        simWaveL[i] = (timeBufL[i] - 128) / 128;
        simWaveR[i] = (timeBufR[i] - 128) / 128;
      }
      return { freq: simFreq, waveL: simWaveL, waveR: simWaveR };
    }

    const beat = (t * 1.866) % 1;
    const kick = Math.exp(-beat * 6.5);
    const snare = Math.exp(-(((t * 1.866 + 0.5) % 1)) * 9.0) * 0.7;

    for (let i = 0; i < 64; i++) {
      const x = i / 63;
      const bass = Math.exp(-x * 7) * (0.45 + 0.52 * kick);
      const mid = Math.exp(-Math.pow((x - 0.32) * 4.2, 2)) * (0.28 + 0.28 * Math.sin(t * 2.4 + i * 0.25) + 0.25 * snare);
      const air = Math.exp(-Math.pow((x - 0.72) * 5.0, 2)) * (0.14 + 0.16 * Math.sin(t * 5.7 + i * 0.7));
      const shimmer = 0.05 * Math.sin(t * 9.3 + i * 1.1) + 0.05;
      simFreq[i] = Math.max(0.02, Math.min(1, bass + mid + air + shimmer));
    }
    for (let i = 0; i < 256; i++) {
      const ph = (i / 256) * Math.PI * 2;
      const env = 0.35 + 0.5 * kick;
      simWaveL[i] = env * (0.55 * Math.sin(ph * 2 + t * 5.5) + 0.25 * Math.sin(ph * 5 - t * 8.2) + 0.12 * Math.cos(ph * 11 + t * 14));
      simWaveR[i] = env * (0.55 * Math.sin(ph * 2 + t * 5.5 + 0.35) + 0.25 * Math.sin(ph * 5 - t * 8.2 - 0.4) + 0.12 * Math.cos(ph * 11 + t * 14 + 0.6));
    }
    return { freq: simFreq, waveL: simWaveL, waveR: simWaveR };
  }

  function drawStageBackground(ctx, th, W, H) {
    ctx.fillStyle = th.bg;
    ctx.fillRect(0, 0, W, H);
    const grad = ctx.createRadialGradient(W * 0.5, H * 0.48, 40, W * 0.5, H * 0.5, W * 0.65);
    grad.addColorStop(0, "rgba(255,255,255,0.03)");
    grad.addColorStop(1, "rgba(0,0,0,0.48)");
    ctx.fillStyle = grad;
    ctx.fillRect(0, 0, W, H);
  }

  function applyGlow(ctx, th, radius) {
    if (state.visualizerGlow) {
      ctx.shadowColor = th.glow;
      ctx.shadowBlur = radius;
    } else {
      ctx.shadowBlur = 0;
    }
  }

  function renderPresetBlack(ctx, W, H) {
    ctx.fillStyle = "#000000";
    ctx.fillRect(0, 0, W, H);
  }

  function renderPresetEmber(ctx, audio, W, H, t) {
    ctx.fillStyle = "#000000";
    ctx.fillRect(0, 0, W, H);

    const bass = (audio.freq[0] + audio.freq[1] + audio.freq[2] + audio.freq[3]) * 0.25;
    const breathe = 0.5 + 0.5 * Math.sin(t * 0.9);
    const midY = H * 0.60;

    const glowGrad = ctx.createRadialGradient(W * 0.5, midY, 12, W * 0.5, midY, W * 0.46);
    glowGrad.addColorStop(0, `rgba(154, 52, 18, ${(0.18 + bass * 0.16 + breathe * 0.04).toFixed(3)})`);
    glowGrad.addColorStop(0.45, `rgba(92, 30, 9, ${(0.10 + bass * 0.08).toFixed(3)})`);
    glowGrad.addColorStop(1, "rgba(0, 0, 0, 0)");
    ctx.fillStyle = glowGrad;
    ctx.fillRect(0, 0, W, H);

    const padX = 96;
    const spanW = W - padX * 2;
    [
      { width: 22, color: "rgba(124, 45, 18, 0.22)", blur: 24 },
      { width: 10, color: "rgba(154, 52, 18, 0.34)", blur: 14 },
      { width: 3.5, color: "rgba(194, 88, 34, 0.46)", blur: 6 },
    ].forEach((layer) => {
      ctx.save();
      ctx.strokeStyle = layer.color;
      ctx.lineWidth = layer.width;
      ctx.lineCap = "round";
      if (state.visualizerGlow) {
        ctx.shadowColor = "rgba(154, 52, 18, 0.45)";
        ctx.shadowBlur = layer.blur;
      }
      ctx.beginPath();
      for (let i = 0; i < 128; i++) {
        const x = padX + (i / 127) * spanW;
        const env = Math.sin((i / 127) * Math.PI);
        const y = midY + audio.waveL[i * 2] * (48 + bass * 30) * env;
        if (i === 0) ctx.moveTo(x, y);
        else ctx.lineTo(x, y);
      }
      ctx.stroke();
      ctx.restore();
    });
  }

  function renderPresetNightLine(ctx, audio, th, W, H) {
    ctx.fillStyle = "#000000";
    ctx.fillRect(0, 0, W, H);

    const baseY = H * 0.72;
    const padX = 80;
    const spanW = W - padX * 2;

    ctx.strokeStyle = "rgba(51, 65, 85, 0.20)";
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.moveTo(padX, baseY);
    ctx.lineTo(W - padX, baseY);
    ctx.stroke();

    ctx.save();
    ctx.strokeStyle = th.id === "oled-ember" ? "rgba(154, 52, 18, 0.55)" : "rgba(71, 96, 130, 0.58)";
    ctx.lineWidth = 1.6;
    if (state.visualizerGlow) {
      ctx.shadowColor = "rgba(59, 79, 115, 0.35)";
      ctx.shadowBlur = 5;
    }
    ctx.beginPath();
    for (let i = 0; i < 180; i++) {
      const idx = Math.floor((i / 179) * 255);
      const x = padX + (i / 179) * spanW;
      const env = Math.pow(Math.sin((i / 179) * Math.PI), 0.6);
      const y = baseY + audio.waveL[idx] * (H * 0.08) * env;
      if (i === 0) ctx.moveTo(x, y);
      else ctx.lineTo(x, y);
    }
    ctx.stroke();
    ctx.restore();
  }

  function renderPresetNightFog(ctx, audio, W, H, t) {
    ctx.fillStyle = "#000000";
    ctx.fillRect(0, 0, W, H);

    const pools = [
      { x: 0.22, bin: 1, r: 230, rgb: "124, 45, 18" },
      { x: 0.38, bin: 4, r: 260, rgb: "154, 52, 18" },
      { x: 0.52, bin: 7, r: 280, rgb: "59, 46, 118" },
      { x: 0.68, bin: 10, r: 245, rgb: "49, 46, 129" },
      { x: 0.80, bin: 14, r: 210, rgb: "124, 45, 18" },
    ];

    pools.forEach((p, idx) => {
      const energy = audio.freq[p.bin] || 0.2;
      const driftX = W * p.x + Math.sin(t * 0.45 + idx * 1.4) * 24;
      const centerY = H * (0.90 - energy * 0.10);
      const radius = p.r + energy * 110;
      const alpha = Math.min(0.36, 0.10 + energy * 0.26);

      const grad = ctx.createRadialGradient(driftX, centerY, 8, driftX, centerY, radius);
      grad.addColorStop(0, `rgba(${p.rgb}, ${alpha.toFixed(3)})`);
      grad.addColorStop(0.55, `rgba(${p.rgb}, ${(alpha * 0.38).toFixed(3)})`);
      grad.addColorStop(1, "rgba(0, 0, 0, 0)");
      ctx.fillStyle = grad;
      ctx.fillRect(0, 0, W, H);
    });
  }

  function renderPresetLunarHalo(ctx, audio, th, W, H, t) {
    radialCtx.fillStyle = "rgba(0, 0, 0, 0.09)";
    radialCtx.fillRect(0, 0, W, H);

    const bass = (audio.freq[0] + audio.freq[1] + audio.freq[2]) / 3;
    const cx = W * 0.5;
    const cy = H * 0.48;
    const baseR = 114 + bass * 38 + Math.sin(t * 0.8) * 6;

    radialCtx.save();
    radialCtx.strokeStyle = th.id === "night-indigo"
      ? "rgba(59, 79, 115, 0.26)"
      : "rgba(154, 118, 72, 0.24)";
    radialCtx.lineWidth = 14;
    if (state.visualizerGlow) {
      radialCtx.shadowColor = th.id === "night-indigo"
        ? "rgba(59, 79, 115, 0.35)"
        : "rgba(154, 118, 72, 0.35)";
      radialCtx.shadowBlur = 22;
    }
    radialCtx.beginPath();
    for (let i = 0; i < 96; i++) {
      const a = (i / 96) * Math.PI * 2;
      const mod = audio.waveL[(i * 2) % 256] * 14;
      const x = cx + Math.cos(a) * (baseR + mod);
      const y = cy + Math.sin(a) * (baseR + mod);
      if (i === 0) radialCtx.moveTo(x, y);
      else radialCtx.lineTo(x, y);
    }
    radialCtx.closePath();
    radialCtx.stroke();

    radialCtx.strokeStyle = th.id === "night-indigo"
      ? "rgba(148, 163, 184, 0.24)"
      : "rgba(200, 175, 138, 0.24)";
    radialCtx.lineWidth = 1.6;
    radialCtx.stroke();
    radialCtx.restore();

    ctx.fillStyle = "#000000";
    ctx.fillRect(0, 0, W, H);
    ctx.drawImage(radialCanvas, 0, 0);
  }

  function renderPresetBars(ctx, audio, th, W, H) {
    const count = 48;
    const padX = 88;
    const usableW = W - padX * 2;
    const step = usableW / count;
    const barW = step * 0.68;
    const baseY = H * 0.78;
    const maxH = H * 0.56;

    const grad = ctx.createLinearGradient(0, baseY - maxH, 0, baseY);
    grad.addColorStop(0, th.secondary);
    grad.addColorStop(1, th.primary);
    ctx.fillStyle = grad;
    applyGlow(ctx, th, 16);

    for (let i = 0; i < count; i++) {
      const val = audio.freq[Math.floor((i / count) * 63)];
      const bh = Math.max(4, val * maxH);
      const x = padX + i * step;
      ctx.fillRect(x, baseY - bh, barW, bh);
    }
    ctx.shadowBlur = 0;

    ctx.fillStyle = th.primary;
    ctx.globalAlpha = 0.22;
    for (let i = 0; i < count; i++) {
      const val = audio.freq[Math.floor((i / count) * 63)];
      const bh = Math.max(2, val * maxH * 0.16);
      ctx.fillRect(padX + i * step, baseY + 4, barW, bh);
    }
    ctx.globalAlpha = 1;
  }

  function renderPresetWaves(ctx, audio, th, W, H) {
    const midY = H * 0.5;
    const amp = H * 0.28;
    const padX = 56;
    const usableW = W - padX * 2;

    ctx.strokeStyle = th.primary;
    ctx.lineWidth = 2.5;
    applyGlow(ctx, th, 14);
    ctx.beginPath();
    for (let i = 0; i < 160; i++) {
      const idx = Math.floor((i / 160) * 255);
      const v = Math.abs(audio.waveL[idx]);
      const x = padX + (i / 159) * usableW;
      const h = Math.max(2, v * amp);
      ctx.moveTo(x, midY - h);
      ctx.lineTo(x, midY + h);
    }
    ctx.stroke();

    ctx.strokeStyle = th.secondary;
    ctx.lineWidth = 2;
    ctx.beginPath();
    for (let i = 0; i < 256; i++) {
      const x = padX + (i / 255) * usableW;
      const y = midY + audio.waveR[i] * amp * 0.85;
      if (i === 0) ctx.moveTo(x, y);
      else ctx.lineTo(x, y);
    }
    ctx.stroke();
    ctx.shadowBlur = 0;
  }

  function renderPresetCqt(ctx, audio, th, W, H) {
    const keys = 60;
    const padX = 64;
    const usableW = W - padX * 2;
    const step = usableW / keys;
    const splitY = H * 0.64;

    applyGlow(ctx, th, 14);
    for (let i = 0; i < keys; i++) {
      const val = audio.freq[Math.min(63, Math.floor(i * 0.95))];
      const h = Math.max(3, val * H * 0.44);
      ctx.fillStyle = i % 12 === 0 ? th.secondary : th.primary;
      ctx.fillRect(padX + i * step, splitY - h, step * 0.72, h);
    }
    ctx.shadowBlur = 0;

    ctx.fillStyle = "rgba(255,255,255,0.12)";
    ctx.fillRect(padX, splitY + 3, usableW, 6);
    for (let i = 0; i < keys; i += 12) {
      ctx.fillStyle = th.secondary;
      ctx.fillRect(padX + i * step, splitY + 2, 3, 8);
    }

    for (let i = 0; i < keys; i++) {
      const val = audio.freq[Math.min(63, Math.floor(i * 0.95))];
      ctx.fillStyle = th.primary;
      ctx.globalAlpha = 0.12 + val * 0.65;
      ctx.fillRect(padX + i * step, splitY + 15, step * 0.85, H * 0.14);
    }
    ctx.globalAlpha = 1;
  }

  function renderPresetRadial(ctx, audio, th, W, H) {
    radialCtx.fillStyle = "rgba(5, 6, 6, 0.22)";
    radialCtx.fillRect(0, 0, W, H);

    const cx = W * 0.5;
    const cy = H * 0.49;
    const baseR = 92 + audio.freq[2] * 38;

    radialCtx.save();
    radialCtx.strokeStyle = th.primary;
    radialCtx.lineWidth = 2;
    if (state.visualizerGlow) {
      radialCtx.shadowColor = th.glow;
      radialCtx.shadowBlur = 14;
    }
    radialCtx.beginPath();
    for (let i = 0; i < 128; i++) {
      const a = (i / 128) * Math.PI * 2;
      const r = baseR + audio.waveL[i * 2] * 95 + audio.waveR[i * 2] * 45;
      const x = cx + Math.cos(a) * r;
      const y = cy + Math.sin(a) * r;
      if (i === 0) radialCtx.moveTo(x, y);
      else radialCtx.lineTo(x, y);
    }
    radialCtx.closePath();
    radialCtx.stroke();

    radialCtx.strokeStyle = th.secondary;
    radialCtx.lineWidth = 1.4;
    radialCtx.beginPath();
    for (let i = 0; i < 64; i++) {
      const l = audio.waveL[i * 4] * 120;
      const r = audio.waveR[i * 4] * 120;
      const px = cx + (l - r) * 0.7;
      const py = cy - (l + r) * 0.7;
      if (i === 0) radialCtx.moveTo(px, py);
      else radialCtx.lineTo(px, py);
    }
    radialCtx.stroke();
    radialCtx.restore();

    ctx.drawImage(radialCanvas, 0, 0);
  }

  function renderPresetSpectrogram(ctx, audio, th, W, H) {
    const sw = specCanvas.width;
    const sh = specCanvas.height;
    specCtx.drawImage(specCanvas, -2, 0);

    for (let y = 0; y < sh; y++) {
      const bin = Math.floor(((sh - 1 - y) / sh) * 63);
      const v = audio.freq[bin];
      if (v < 0.08) {
        specCtx.fillStyle = th.bg;
      } else {
        specCtx.fillStyle = v > 0.62 ? th.secondary : th.primary;
        specCtx.globalAlpha = Math.min(1, v * 1.15);
      }
      specCtx.fillRect(sw - 2, y, 2, 1);
      specCtx.globalAlpha = 1;
    }

    ctx.save();
    applyGlow(ctx, th, 8);
    ctx.drawImage(specCanvas, 48, 36, W - 96, H - 88);
    ctx.restore();
    ctx.strokeStyle = "rgba(255,255,255,0.12)";
    ctx.strokeRect(48, 36, W - 96, H - 88);
  }

  function renderPreset3DScope(ctx, audio, th, W, H) {
    const slice = [];
    for (let i = 0; i < 64; i++) {
      slice.push(audio.waveL[i * 4]);
    }
    scopeHistory.unshift(slice);
    if (scopeHistory.length > MAX_SCOPE_HISTORY) scopeHistory.pop();

    for (let d = scopeHistory.length - 1; d >= 0; d--) {
      const depth = d / MAX_SCOPE_HISTORY;
      const scale = 1 - depth * 0.62;
      const yBase = H * (0.76 - depth * 0.5);
      const alpha = (1 - depth) * 0.9;
      const pts = scopeHistory[d];

      ctx.save();
      ctx.globalAlpha = alpha;
      ctx.strokeStyle = d === 0 ? th.secondary : th.primary;
      ctx.lineWidth = d === 0 ? 3 : 1.5;
      if (d < 3) applyGlow(ctx, th, 12);

      ctx.beginPath();
      const span = (W - 140) * scale;
      const startX = (W - span) * 0.5;
      for (let i = 0; i < pts.length; i++) {
        const x = startX + (i / (pts.length - 1)) * span;
        const y = yBase + pts[i] * 72 * scale;
        if (i === 0) ctx.moveTo(x, y);
        else ctx.lineTo(x, y);
      }
      ctx.stroke();
      ctx.restore();
    }
  }

  function renderPresetHistogram(ctx, audio, th, W, H) {
    const bins = 48;
    const counts = new Float32Array(bins);
    for (let i = 0; i < 256; i++) {
      const idx = Math.max(0, Math.min(bins - 1, Math.floor((audio.waveL[i] * 0.5 + 0.5) * (bins - 1))));
      counts[idx] += 1;
    }

    const hw = histCanvas.width;
    histCtx.drawImage(histCanvas, 0, 2);
    for (let i = 0; i < bins; i++) {
      const density = Math.min(1, counts[i] / 18);
      histCtx.fillStyle = density > 0.55 ? th.secondary : th.primary;
      histCtx.globalAlpha = 0.08 + density * 0.85;
      histCtx.fillRect((i / bins) * hw, 0, hw / bins, 2);
    }
    histCtx.globalAlpha = 1;

    ctx.drawImage(histCanvas, 64, H * 0.38, W - 128, H * 0.45);

    const padX = 64;
    const step = (W - padX * 2) / bins;
    ctx.fillStyle = th.primary;
    applyGlow(ctx, th, 10);
    for (let i = 0; i < bins; i++) {
      const barH = Math.min(115, (counts[i] / 18) * 115);
      ctx.fillRect(padX + i * step, H * 0.34 - barH, step - 2, barH);
    }
    ctx.shadowBlur = 0;
  }

  function renderPresetPhase(ctx, audio, th, W, H) {
    let num = 0;
    let denL = 0;
    let denR = 0;
    for (let i = 0; i < 256; i++) {
      num += audio.waveL[i] * audio.waveR[i];
      denL += audio.waveL[i] * audio.waveL[i];
      denR += audio.waveR[i] * audio.waveR[i];
    }
    const corr = denL > 0 && denR > 0 ? num / Math.sqrt(denL * denR) : 0;

    const pw = phaseCanvas.width;
    phaseCtx.drawImage(phaseCanvas, 0, 2);
    phaseCtx.fillStyle = "rgba(0,0,0,0.35)";
    phaseCtx.fillRect(0, 0, pw, 2);

    const xPos = ((corr + 1) * 0.5) * (pw - 20) + 10;
    phaseCtx.fillStyle = th.primary;
    phaseCtx.fillRect(xPos - 4, 0, 8, 3);

    const boxX = 110;
    const boxW = W - 220;
    ctx.drawImage(phaseCanvas, boxX, 42, boxW, H - 96);

    ctx.strokeStyle = th.secondary;
    ctx.setLineDash([5, 5]);
    ctx.beginPath();
    ctx.moveTo(W * 0.5, 42);
    ctx.lineTo(W * 0.5, H - 54);
    ctx.stroke();
    ctx.setLineDash([]);

    ctx.fillStyle = th.secondary;
    applyGlow(ctx, th, 12);
    const liveX = boxX + ((corr + 1) * 0.5) * boxW;
    ctx.beginPath();
    ctx.arc(liveX, 52, 6, 0, Math.PI * 2);
    ctx.fill();
    ctx.shadowBlur = 0;
  }

  function renderPresetVu(ctx, audio, th, W, H) {
    let rmsL = 0;
    let rmsR = 0;
    for (let i = 0; i < 256; i++) {
      rmsL += audio.waveL[i] * audio.waveL[i];
      rmsR += audio.waveR[i] * audio.waveR[i];
    }
    const levelL = Math.min(1, Math.sqrt(rmsL / 256) * 2.1);
    const levelR = Math.min(1, Math.sqrt(rmsR / 256) * 2.1);
    peakHold.lSmooth = peakHold.lSmooth * 0.72 + levelL * 0.28;
    peakHold.rSmooth = peakHold.rSmooth * 0.72 + levelR * 0.28;
    peakHold.L = Math.max(peakHold.L * 0.985, peakHold.lSmooth);
    peakHold.R = Math.max(peakHold.R * 0.985, peakHold.rSmooth);

    const padX = 110;
    const barW = W - padX * 2;
    const barH = 44;
    const segments = 32;
    const segGap = 4;
    const segW = (barW - segGap * (segments - 1)) / segments;

    [
      { label: "CH L", val: peakHold.lSmooth, peak: peakHold.L, y: H * 0.36 },
      { label: "CH R", val: peakHold.rSmooth, peak: peakHold.R, y: H * 0.54 },
    ].forEach((ch) => {
      ctx.fillStyle = "rgba(243,242,236,0.75)";
      ctx.font = "bold 14px monospace";
      ctx.fillText(ch.label, padX - 54, ch.y + 28);

      ctx.fillStyle = "rgba(255,255,255,0.06)";
      ctx.fillRect(padX, ch.y, barW, barH);

      applyGlow(ctx, th, 10);
      const lit = Math.round(ch.val * segments);
      for (let s = 0; s < segments; s++) {
        if (s < lit) {
          ctx.fillStyle = s > segments * 0.78 ? th.secondary : th.primary;
        } else {
          ctx.fillStyle = "rgba(255,255,255,0.07)";
          ctx.shadowBlur = 0;
        }
        ctx.fillRect(padX + s * (segW + segGap), ch.y + 4, segW, barH - 8);
      }
      ctx.shadowBlur = 0;

      const peakX = padX + Math.min(barW - 3, ch.peak * barW);
      ctx.fillStyle = th.secondary;
      ctx.fillRect(peakX, ch.y - 3, 3, barH + 6);
    });
  }

  function stageFrameLoop(nowMs) {
    if (state.bgMode !== "visualizer") {
      stageAnimId = 0;
      return;
    }
    const canvas = $("stage-canvas");
    const ctx = canvas.getContext("2d");
    const W = canvas.width;
    const H = canvas.height;
    const t = nowMs * 0.001;

    if (state.visualizer === "black") {
      renderPresetBlack(ctx, W, H);
      $("stage-fade-pill").hidden = true;
    } else {
      const th = getTheme(state.visualizerTheme);
      const audio = sampleAudio(t);
      if (state.visualizer !== "ember" && state.visualizer !== "night-line" &&
          state.visualizer !== "night-fog" && state.visualizer !== "lunar-halo") {
        drawStageBackground(ctx, th, W, H);
      }

      switch (state.visualizer) {
        case "ember": renderPresetEmber(ctx, audio, W, H, t); break;
        case "night-line": renderPresetNightLine(ctx, audio, th, W, H); break;
        case "night-fog": renderPresetNightFog(ctx, audio, W, H, t); break;
        case "lunar-halo": renderPresetLunarHalo(ctx, audio, th, W, H, t); break;
        case "bars": renderPresetBars(ctx, audio, th, W, H); break;
        case "waves": renderPresetWaves(ctx, audio, th, W, H); break;
        case "cqt": renderPresetCqt(ctx, audio, th, W, H); break;
        case "radial": renderPresetRadial(ctx, audio, th, W, H); break;
        case "spectrogram": renderPresetSpectrogram(ctx, audio, th, W, H); break;
        case "3dscope": renderPreset3DScope(ctx, audio, th, W, H); break;
        case "histogram": renderPresetHistogram(ctx, audio, th, W, H); break;
        case "phase": renderPresetPhase(ctx, audio, th, W, H); break;
        case "vu": renderPresetVu(ctx, audio, th, W, H); break;
        default: renderPresetBars(ctx, audio, th, W, H); break;
      }

      const fadePill = $("stage-fade-pill");
      if (state.fadePreviewState === "fading" && state.fadePreviewStartMs !== null) {
        const elapsed = (nowMs - state.fadePreviewStartMs) / 3000;
        if (elapsed >= 1) {
          state.fadePreviewFactor = 0;
          state.fadePreviewState = "blackout";
          fadePill.hidden = false;
          fadePill.textContent = "Blackout active (0% light)";
          $("test-fade-btn").textContent = "Restore Preview";
        } else {
          state.fadePreviewFactor = 1 - elapsed;
          fadePill.hidden = false;
          fadePill.textContent = `Fading to black… ${Math.round(state.fadePreviewFactor * 100)}%`;
        }
      } else if (state.fadePreviewState === "blackout") {
        fadePill.hidden = false;
        fadePill.textContent = "Blackout active (0% light)";
      } else {
        fadePill.hidden = true;
      }

      const effectiveBrightness = (state.visualizerBrightness / 100) * state.fadePreviewFactor;
      if (effectiveBrightness < 0.999) {
        ctx.fillStyle = `rgba(0, 0, 0, ${(1 - effectiveBrightness).toFixed(3)})`;
        ctx.fillRect(0, 0, W, H);
      }
      $("stage-overlay").style.opacity = state.fadePreviewState === "idle" ? "" : state.fadePreviewFactor.toFixed(3);
    }

    stageAnimId = requestAnimationFrame(stageFrameLoop);
  }

  function startStageLoop() {
    if (!stageAnimId) {
      stageAnimId = requestAnimationFrame(stageFrameLoop);
    }
  }

  function stopStageLoop() {
    if (stageAnimId) {
      cancelAnimationFrame(stageAnimId);
      stageAnimId = 0;
    }
  }

  function setBrowseButtonsDisabled(disabled) {
    ["browse-audio-dir-btn", "browse-out-dir-btn", "browse-bg-path-btn"].forEach((id) => {
      const btn = $(id);
      if (btn) btn.disabled = disabled;
    });
  }

  async function pickNativePath(target, triggerEl) {
    if (!state.health || !state.health.localPathsAllowed || state.pickerBusy) return;
    const isBG = target === "bgFile";
    const kind = isBG ? "bgFile" : "folder";
    const initialPath = isBG
      ? $("bg-path-input").value.trim()
      : (target === "outDir" ? $("out-dir-input").value.trim() : $("audio-dir-input").value.trim());

    state.pickerBusy = true;
    setBrowseButtonsDisabled(true);
    let fallbackToModal = false;
    try {
      const res = await fetch("/api/fs/pick", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ kind, initialPath }),
      });
      let data = {};
      try {
        data = await res.json();
      } catch {
        data = {};
      }
      if (res.status === 503 && data && data.fallback) {
        fallbackToModal = true;
        return;
      }
      if (!res.ok) {
        appendLog(`Browse error: ${data.error || "Unable to open native picker"}`);
        return;
      }
      if (data.cancelled || !data.path) {
        return;
      }
      if (target === "audioDir") {
        $("audio-dir-input").value = data.path;
        renderAll();
        await probeLocalFolder();
      } else if (target === "outDir") {
        $("out-dir-input").value = data.path;
        renderAll();
      } else if (target === "bgFile") {
        $("bg-path-input").value = data.path;
        renderAll();
      }
    } catch (err) {
      appendLog(`Browse error: ${err.message}`);
    } finally {
      state.pickerBusy = false;
      const allowLocal = Boolean(state.health && state.health.localPathsAllowed);
      setBrowseButtonsDisabled(!allowLocal);
      if (fallbackToModal && allowLocal) {
        if (target === "audioDir" || target === "outDir") {
          openFSModal(target, triggerEl);
        } else {
          appendLog("Native file picker unavailable in this environment; enter a local Background Visual path manually.");
          $("bg-path-input").focus();
        }
      } else if (triggerEl && typeof triggerEl.focus === "function" && !triggerEl.disabled) {
        triggerEl.focus();
      }
    }
  }

  async function openFSModal(mode, triggerEl) {
    if (!state.health || !state.health.localPathsAllowed) return;
    state.fsModal.mode = mode;
    state.fsModal.returnFocusEl = triggerEl || document.activeElement;
    state.fsModal.open = true;

    const isOut = mode === "outDir";
    $("fs-modal-title").textContent = isOut ? "Choose Output Location" : "Choose Audio Folder";
    $("fs-modal-subtitle").textContent = isOut
      ? "Pick or create a destination folder and set output filenames."
      : "Pick a folder containing .mp3 Tracks to scan.";
    $("fs-confirm-btn").textContent = isOut ? "Select This Folder" : "Use Audio Folder";
    $("fs-filenames-section").hidden = !isOut;
    if (isOut) {
      $("fs-modal-out-name").value = $("out-path-input").value.trim() || "output.mp4";
      $("fs-modal-ts-name").value = $("timestamps-path-input").value.trim() || "timestamps.txt";
    }
    $("fs-new-folder-input").value = "";
    setModalError("");

    $("fs-modal").hidden = false;
    const initialPath = isOut ? $("out-dir-input").value.trim() : $("audio-dir-input").value.trim();
    const ok = await loadFSList(initialPath, true);
    if (!ok && initialPath) {
      await loadFSList("", false);
    }
    $("fs-confirm-btn").focus();
  }

  function closeFSModal() {
    if (!state.fsModal.open) return;
    state.fsModal.open = false;
    $("fs-modal").hidden = true;
    setModalError("");
    if (state.fsModal.returnFocusEl && typeof state.fsModal.returnFocusEl.focus === "function") {
      state.fsModal.returnFocusEl.focus();
    }
    state.fsModal.returnFocusEl = null;
  }

  function setModalError(msg) {
    const el = $("fs-modal-error");
    if (!msg) {
      el.textContent = "";
      el.hidden = true;
      return;
    }
    el.textContent = msg;
    el.hidden = false;
  }

  async function loadFSList(dirPath, silentOnMiss) {
    setModalError("");
    const query = dirPath ? `?path=${encodeURIComponent(dirPath)}` : "";
    try {
      const res = await fetch(`/api/fs/list${query}`);
      const data = await res.json();
      if (!res.ok) {
        if (silentOnMiss && (res.status === 404 || res.status === 403)) {
          return false;
        }
        throw new Error(data.error || "Cannot list directory");
      }
      state.fsModal.current = data.current || "";
      state.fsModal.parent = data.parent || "";
      state.fsModal.roots = Array.isArray(data.roots) ? data.roots : [];
      state.fsModal.entries = Array.isArray(data.entries) ? data.entries : [];
      renderFSModal();
      return true;
    } catch (err) {
      setModalError(err.message);
      return false;
    }
  }

  function renderFSModal() {
    const rootsBar = $("fs-roots-bar");
    rootsBar.innerHTML = "";
    let bestMatchPath = "";
    for (const root of state.fsModal.roots) {
      if (root.path && state.fsModal.current.startsWith(root.path) && root.path.length > bestMatchPath.length) {
        bestMatchPath = root.path;
      }
    }
    for (const root of state.fsModal.roots) {
      const chip = document.createElement("button");
      chip.type = "button";
      chip.className = "fs-root-chip" + (root.path === bestMatchPath ? " is-active" : "");
      chip.textContent = root.label;
      chip.title = root.path;
      chip.addEventListener("click", () => loadFSList(root.path, false));
      rootsBar.appendChild(chip);
    }

    $("fs-current-path").textContent = state.fsModal.current || "/";
    $("fs-up-btn").disabled = !state.fsModal.parent;

    const listEl = $("fs-entries-list");
    listEl.innerHTML = "";
    if (state.fsModal.entries.length === 0) {
      const empty = document.createElement("div");
      empty.className = "fs-empty";
      empty.textContent = "No subfolders here — click New Folder or confirm this folder.";
      listEl.appendChild(empty);
      return;
    }

    for (const entry of state.fsModal.entries) {
      const btn = document.createElement("button");
      btn.type = "button";
      btn.className = "fs-entry-btn";
      btn.setAttribute("role", "option");

      const nameSpan = document.createElement("span");
      nameSpan.textContent = entry.name;

      const metaSpan = document.createElement("span");
      metaSpan.className = "fs-entry-meta";
      metaSpan.textContent = "Open";

      btn.append(nameSpan, metaSpan);
      btn.addEventListener("click", () => loadFSList(entry.path, false));
      listEl.appendChild(btn);
    }
  }

  async function createNewFolder() {
    const name = $("fs-new-folder-input").value.trim();
    if (!name) {
      setModalError("Enter a folder name first.");
      $("fs-new-folder-input").focus();
      return;
    }
    setModalError("");
    try {
      const res = await fetch("/api/fs/mkdir", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ parentDir: state.fsModal.current, name }),
      });
      const data = await res.json();
      if (!res.ok) {
        throw new Error(data.error || "Cannot create folder");
      }
      $("fs-new-folder-input").value = "";
      await loadFSList(data.path || state.fsModal.current, false);
    } catch (err) {
      setModalError(err.message);
    }
  }

  function confirmFSModal() {
    if (!state.fsModal.current) {
      closeFSModal();
      return;
    }
    if (state.fsModal.mode === "outDir") {
      $("out-dir-input").value = state.fsModal.current;
      $("out-path-input").value = normalizeFilename($("fs-modal-out-name").value, ".mp4", "output.mp4");
      $("timestamps-path-input").value = normalizeFilename($("fs-modal-ts-name").value, ".txt", "timestamps.txt");
      renderAll();
    } else {
      $("audio-dir-input").value = state.fsModal.current;
      renderCLI();
    }
    closeFSModal();
  }

  function handleModalKeydown(e) {
    if (!state.fsModal.open) return;
    if (e.key === "Escape") {
      e.preventDefault();
      closeFSModal();
      return;
    }
    if (e.key !== "Tab") return;
    const dialog = $("fs-modal").querySelector(".modal-dialog");
    const focusables = Array.from(
      dialog.querySelectorAll('button:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])')
    ).filter((el) => !el.closest("[hidden]"));
    if (focusables.length === 0) return;
    const first = focusables[0];
    const last = focusables[focusables.length - 1];
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault();
      first.focus();
    }
  }

  function bindDropzone(el, onFiles) {
    el.addEventListener("dragover", (e) => {
      e.preventDefault();
      el.classList.add("is-dragover");
    });
    el.addEventListener("dragleave", () => el.classList.remove("is-dragover"));
    el.addEventListener("drop", (e) => {
      e.preventDefault();
      el.classList.remove("is-dragover");
      if (e.dataTransfer && e.dataTransfer.files) {
        onFiles(e.dataTransfer.files);
      }
    });
  }

  async function copyText(text, btn) {
    const prev = btn.textContent;
    try {
      await navigator.clipboard.writeText(text);
      btn.textContent = "Copied";
    } catch {
      btn.textContent = "Failed";
    }
    setTimeout(() => {
      btn.textContent = prev;
    }, 1200);
  }

  function bindEvents() {
    $("left-tab-tracks").addEventListener("click", () => setLeftTab("tracks"));
    $("left-tab-studio").addEventListener("click", () => setLeftTab("studio"));
    $("right-tab-chapters").addEventListener("click", () => setRightTab("chapters"));
    $("right-tab-output").addEventListener("click", () => setRightTab("output"));
    $("right-tab-telemetry").addEventListener("click", () => setRightTab("telemetry"));

    $("tracks-mode-upload").addEventListener("click", () => setTracksMode("upload"));
    $("tracks-mode-local").addEventListener("click", () => setTracksMode("local"));
    $("bg-mode-upload").addEventListener("click", () => setBgMode("upload"));
    $("bg-mode-local").addEventListener("click", () => setBgMode("local"));
    $("bg-mode-visualizer").addEventListener("click", () => setBgMode("visualizer"));
    if ($("bg-mode-black")) {
      $("bg-mode-black").addEventListener("click", () => setBgMode("black"));
    }

    if ($("per-track-toggle")) {
      $("per-track-toggle").addEventListener("change", (e) => {
        state.perTrackEnabled = Boolean(e.target.checked);
        renderAll();
      });
    }
    if ($("track-layout-select")) {
      $("track-layout-select").addEventListener("change", (e) => {
        state.trackMediaLayout = e.target.value || "auto";
        renderAll();
      });
    }
    if ($("track-transition-select")) {
      $("track-transition-select").addEventListener("change", (e) => {
        state.trackTransition = e.target.value || "cut";
        renderAll();
      });
    }
    if ($("track-media-file-input")) {
      $("track-media-file-input").addEventListener("change", (e) => {
        const file = e.target.files && e.target.files[0];
        const tid = state.pendingTrackUploadId;
        state.pendingTrackUploadId = "";
        if (file && tid) {
          uploadTrackMedia(tid, file);
        }
      });
    }

    $("audition-btn").addEventListener("click", toggleAudition);
    if ($("stage-audition-btn")) {
      $("stage-audition-btn").addEventListener("click", toggleAudition);
    }
    if ($("stage-track-select")) {
      $("stage-track-select").addEventListener("change", (e) => {
        const nextIdx = Math.max(0, Math.min(state.tracks.length - 1, Number(e.target.value) || 0));
        const changed = state.selectedTrackIdx !== nextIdx;
        state.selectedTrackIdx = nextIdx;
        renderTracksTable(computeSchedule());
        renderStage();
        renderAuditionBar();
        if (changed && state.auditioning) {
          playSelectedTrackAudition();
        }
      });
    }
    $("audition-audio").addEventListener("ended", () => {
      state.auditioning = false;
      renderAuditionBar();
    });

    $("viz-page-prev").addEventListener("click", () => {
      if (state.vizPage > 0) {
        state.vizPage--;
        renderVisualizerControls();
      }
    });
    $("viz-page-next").addEventListener("click", () => {
      const totalPages = Math.max(1, Math.ceil(filteredPresets().length / VIZ_PAGE_SIZE));
      if (state.vizPage < totalPages - 1) {
        state.vizPage++;
        renderVisualizerControls();
      }
    });

    $("viz-theme-select").addEventListener("change", (e) => {
      state.visualizerTheme = e.target.value;
      resetStageBuffers();
      renderAll();
    });
    $("viz-resolution-select").addEventListener("change", (e) => {
      state.visualizerResolution = e.target.value || "1080p";
      renderAll();
    });
    $("viz-glow-toggle").addEventListener("change", (e) => {
      state.visualizerGlow = e.target.checked;
      renderAll();
    });
    $("viz-brightness-slider").addEventListener("input", (e) => {
      state.visualizerBrightness = Number(e.target.value) || 100;
      renderVisualizerControls();
      renderCLI();
    });
    document.querySelectorAll("#darkness-box .quick-pill").forEach((pill) => {
      pill.addEventListener("click", () => {
        state.visualizerBrightness = Number(pill.dataset.dim) || 100;
        renderVisualizerControls();
        renderCLI();
      });
    });
    $("viz-fade-select").addEventListener("change", (e) => {
      state.fadeToBlackMin = Number(e.target.value) || 0;
      renderCLI();
    });
    $("test-fade-btn").addEventListener("click", () => {
      if (state.fadePreviewState === "idle") {
        state.fadePreviewStartMs = performance.now();
        state.fadePreviewFactor = 1;
        state.fadePreviewState = "fading";
        $("test-fade-btn").textContent = "Restore Preview";
      } else {
        clearFadePreview();
        $("test-fade-btn").textContent = "Test 3s Fade";
        $("stage-fade-pill").hidden = true;
      }
    });
    $("dim-titles-toggle").addEventListener("change", (e) => {
      state.dimTitles = e.target.checked;
      renderStageOverlay();
      renderCLI();
    });

    $("tracks-file-input").addEventListener("change", (e) => probeUploadedFiles(e.target.files));
    bindDropzone($("tracks-dropzone"), probeUploadedFiles);
    $("browse-audio-dir-btn").addEventListener("click", (e) => pickNativePath("audioDir", e.currentTarget));
    $("probe-local-btn").addEventListener("click", probeLocalFolder);

    $("bg-file-input").addEventListener("change", (e) => handleBgFile(e.target.files[0]));
    bindDropzone($("bg-dropzone"), (files) => handleBgFile(files[0]));
    $("bg-path-input").addEventListener("input", () => renderAll());
    $("browse-bg-path-btn").addEventListener("click", (e) => pickNativePath("bgFile", e.currentTarget));

    $("browse-out-dir-btn").addEventListener("click", (e) => pickNativePath("outDir", e.currentTarget));

    $("shuffle-toggle").addEventListener("change", () => {
      if ($("shuffle-toggle").checked && state.tracks.length > 1) {
        shuffleTracksInPlace();
      }
      renderAll();
    });

    ["crossfade-slider", "normalize-toggle", "overlay-toggle", "font-name-input", "title-duration-input", "audio-dir-input", "out-dir-input", "out-path-input", "timestamps-path-input"].forEach((id) => {
      $(id).addEventListener("input", renderAll);
      $(id).addEventListener("change", renderAll);
    });

    $("copy-chapters-btn").addEventListener("click", () => copyText($("chapters-preview").textContent, $("copy-chapters-btn")));
    if ($("download-manifest-btn")) {
      $("download-manifest-btn").addEventListener("click", downloadManifestJSON);
    }
    $("copy-cli-btn").addEventListener("click", () => copyText($("cli-preview").textContent, $("copy-cli-btn")));
    $("render-btn").addEventListener("click", startRender);
    $("save-lan-folder-btn").addEventListener("click", saveToLanFolder);

    $("fs-modal-close").addEventListener("click", closeFSModal);
    $("fs-cancel-btn").addEventListener("click", closeFSModal);
    $("fs-up-btn").addEventListener("click", () => {
      if (state.fsModal.parent) loadFSList(state.fsModal.parent, false);
    });
    $("fs-create-folder-btn").addEventListener("click", createNewFolder);
    $("fs-new-folder-input").addEventListener("keydown", (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        createNewFolder();
      }
    });
    $("fs-confirm-btn").addEventListener("click", confirmFSModal);
    $("fs-modal").addEventListener("click", (e) => {
      if (e.target === $("fs-modal")) closeFSModal();
    });
    document.addEventListener("keydown", handleModalKeydown);
  }

  initTheme();
  bindEvents();
  loadHealth();
  renderAll();
})();
