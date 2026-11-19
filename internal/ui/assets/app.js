(function () {
  "use strict";

  const THEME_KEY = "ytmix_theme";
  const VIDEO_EXTS = new Set([".mp4", ".mov", ".gif"]);
  const IMAGE_EXTS = new Set([".jpg", ".jpeg", ".png", ".webp"]);

  const state = {
    health: null,
    sessionId: "",
    tracksMode: "upload", // "upload" | "local"
    bgMode: "upload",     // "upload" | "local"
    tracks: [],           // { id, fileName, displayTitle, duration }
    selectedTrackIdx: 0,
    bgFile: null,
    bgObjectUrl: "",
    rendering: false,
    eventSource: null,
    lastCompleteJob: null,
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
    const bgName = activeBgName();
    const ext = extOf(bgName);
    const overlay = $("overlay-toggle").checked;
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
    return { path: "", kind: "", fpsLabel: "Select Background Visual" };
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
        $("lan-output-note").hidden = false;
        setTracksMode("upload");
        setBgMode("upload");
      } else {
        $("out-dir-row").hidden = false;
        $("out-dir-input").disabled = false;
        $("browse-out-dir-btn").disabled = false;
        $("browse-audio-dir-btn").hidden = false;
        $("browse-audio-dir-btn").disabled = false;
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
    state.bgMode = mode;
    const isUpload = mode === "upload";
    $("bg-mode-upload").classList.toggle("is-active", isUpload);
    $("bg-mode-local").classList.toggle("is-active", !isUpload);
    $("bg-mode-upload").setAttribute("aria-selected", String(isUpload));
    $("bg-mode-local").setAttribute("aria-selected", String(!isUpload));
    $("bg-upload-pane").hidden = !isUpload;
    $("bg-local-pane").hidden = isUpload;
    renderStage();
    renderCLI();
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
    state.sessionId = data.sessionId || "";
    state.tracks = Array.isArray(data.tracks) ? data.tracks : [];
    if ($("shuffle-toggle").checked && state.tracksMode === "upload") {
      shuffleTracksInPlace();
    }
    state.selectedTrackIdx = 0;
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
    state.tracks.splice(index, 1);
    if (state.selectedTrackIdx >= state.tracks.length) {
      state.selectedTrackIdx = Math.max(0, state.tracks.length - 1);
    }
    renderAll();
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
    $("bg-upload-label").textContent = file.name;
    renderStage();
    renderCLI();
  }

  function renderTracksTable(sched) {
    const tbody = $("track-tbody");
    tbody.innerHTML = "";
    if (state.tracks.length === 0) {
      const row = document.createElement("tr");
      row.className = "empty-row";
      const cell = document.createElement("td");
      cell.colSpan = 4;
      cell.textContent = "No Tracks loaded. Upload .mp3 files or probe a local folder.";
      row.appendChild(cell);
      tbody.appendChild(row);
      $("tracks-summary").textContent = "0 Tracks · 00:00";
      return;
    }

    $("tracks-summary").textContent = `${state.tracks.length} Track(s) · ${formatTime(sched.totalDuration, false)}`;
    state.tracks.forEach((tr, idx) => {
      const row = document.createElement("tr");
      row.className = "track-row" + (idx === state.selectedTrackIdx ? " is-selected" : "");
      row.addEventListener("click", (e) => {
        if (e.target.closest("button")) return;
        state.selectedTrackIdx = idx;
        renderTracksTable(sched);
        renderStageOverlay();
      });

      const tdStart = document.createElement("td");
      tdStart.className = "col-start";
      tdStart.textContent = formatTime(sched.starts[idx] || 0, false);

      const tdTitle = document.createElement("td");
      tdTitle.textContent = tr.displayTitle;

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
      upBtn.textContent = "↑";
      upBtn.ariaLabel = `Move ${tr.displayTitle} up`;
      upBtn.disabled = idx === 0;
      upBtn.addEventListener("click", () => moveTrack(idx, -1));

      const downBtn = document.createElement("button");
      downBtn.type = "button";
      downBtn.className = "icon-btn";
      downBtn.textContent = "↓";
      downBtn.ariaLabel = `Move ${tr.displayTitle} down`;
      downBtn.disabled = idx === state.tracks.length - 1;
      downBtn.addEventListener("click", () => moveTrack(idx, 1));

      const delBtn = document.createElement("button");
      delBtn.type = "button";
      delBtn.className = "icon-btn is-remove";
      delBtn.textContent = "×";
      delBtn.ariaLabel = `Remove ${tr.displayTitle}`;
      delBtn.addEventListener("click", () => removeTrack(idx));

      wrap.append(upBtn, downBtn, delBtn);
      tdAct.appendChild(wrap);
      row.append(tdStart, tdTitle, tdLen, tdAct);
      tbody.appendChild(row);
    });
  }

  function renderStage() {
    const info = detectRenderingPath();
    const bgName = activeBgName();
    $("stage-bg-badge").textContent = bgName ? baseName(bgName) : "No visual";
    $("stage-path-badge").textContent = info.path || "Select BG";
    $("stage-fps-meta").textContent = info.fpsLabel;

    const img = $("stage-img");
    const vid = $("stage-video");
    const empty = $("stage-empty");

    let src = "";
    if (state.bgMode === "upload" && state.bgObjectUrl) {
      src = state.bgObjectUrl;
    } else if (state.bgMode === "local" && bgName && state.health && state.health.localPathsAllowed) {
      src = `/api/preview-bg?path=${encodeURIComponent(bgName)}`;
    }

    if (!src || !info.kind) {
      img.hidden = true;
      img.removeAttribute("src");
      delete img.dataset.currentSrc;
      vid.hidden = true;
      vid.removeAttribute("src");
      empty.hidden = false;
    } else if (info.kind === "video") {
      empty.hidden = true;
      img.hidden = true;
      img.removeAttribute("src");
      delete img.dataset.currentSrc;
      if (vid.getAttribute("src") !== src) {
        vid.src = src;
      }
      vid.hidden = false;
    } else {
      empty.hidden = true;
      vid.hidden = true;
      vid.removeAttribute("src");
      if (img.dataset.currentSrc !== src) {
        img.dataset.currentSrc = src;
        img.src = src;
      }
      img.hidden = false;
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
    overlayEl.hidden = false;
  }

  function renderCLI() {
    const audioDir = state.tracksMode === "local" ? ($("audio-dir-input").value.trim() || "./tracks") : "./tracks";
    const bg = activeBgName() || "<background-visual>";
    const localAllowed = Boolean(!state.health || state.health.localPathsAllowed);
    const outDir = localAllowed ? $("out-dir-input").value.trim() : "";
    const out = normalizeFilename($("out-path-input").value, ".mp4", "output.mp4");
    const ts = normalizeFilename($("timestamps-path-input").value, ".txt", "timestamps.txt");
    const crossfade = Number($("crossfade-slider").value) || 0;
    const parts = ["ytmix", "--audio-dir", quoteArg(audioDir), "--bg", quoteArg(bg)];
    if (outDir) parts.push("--out-dir", quoteArg(outDir));
    parts.push("--out", quoteArg(out));
    if (ts !== "timestamps.txt") parts.push("--timestamps", quoteArg(ts));
    if (crossfade > 0) parts.push("--crossfade", String(crossfade));
    if ($("normalize-toggle").checked) parts.push("--normalize");
    if ($("overlay-toggle").checked) {
      parts.push("--overlay-titles");
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

    renderTracksTable(sched);
    $("chapters-preview").textContent = sched.text;
    renderStage();
    renderCLI();

    const hasBg = Boolean(activeBgName()) && Boolean(detectRenderingPath().path);
    const hasTracks = state.tracks.length > 0;
    $("render-btn").disabled = state.rendering || !hasTracks || !hasBg || !sched.validCrossfade;
  }

  function resetTelemetry() {
    state.lastCompleteJob = null;
    $("progress-fill").style.width = "0%";
    $("telemetry-state").textContent = "Running";
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
    $("telemetry-log").textContent = "Submitting Mix configuration…";
    renderAll();

    const cfgPayload = {
      sessionId: state.sessionId,
      trackIds: state.tracks.map((t) => t.id),
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
      $("progress-fill").style.width = `${payload.progress || 0}%`;
      $("telemetry-state").textContent = `Running · ${payload.progress || 0}%`;
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
      $("telemetry-state").textContent = parts.join(" · ");
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
    $("tracks-mode-upload").addEventListener("click", () => setTracksMode("upload"));
    $("tracks-mode-local").addEventListener("click", () => setTracksMode("local"));
    $("bg-mode-upload").addEventListener("click", () => setBgMode("upload"));
    $("bg-mode-local").addEventListener("click", () => setBgMode("local"));

    $("tracks-file-input").addEventListener("change", (e) => probeUploadedFiles(e.target.files));
    bindDropzone($("tracks-dropzone"), probeUploadedFiles);
    $("browse-audio-dir-btn").addEventListener("click", (e) => openFSModal("audioDir", e.currentTarget));
    $("probe-local-btn").addEventListener("click", probeLocalFolder);

    $("bg-file-input").addEventListener("change", (e) => handleBgFile(e.target.files[0]));
    bindDropzone($("bg-dropzone"), (files) => handleBgFile(files[0]));
    $("bg-path-input").addEventListener("input", () => renderAll());

    $("browse-out-dir-btn").addEventListener("click", (e) => openFSModal("outDir", e.currentTarget));

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
