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
      }

      if (!data.localPathsAllowed) {
        $("tracks-mode-local").disabled = true;
        $("bg-mode-local").disabled = true;
        $("tracks-mode-local").title = "Disabled on LAN / --host 0.0.0.0 sessions";
        $("bg-mode-local").title = "Disabled on LAN / --host 0.0.0.0 sessions";
        setTracksMode("upload");
        setBgMode("upload");
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
    const out = $("out-path-input").value.trim() || "output.mp4";
    const ts = $("timestamps-path-input").value.trim() || "timestamps.txt";
    const crossfade = Number($("crossfade-slider").value) || 0;
    const parts = ["ytmix", "--audio-dir", quoteArg(audioDir), "--bg", quoteArg(bg), "--out", quoteArg(out)];
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
    $("progress-fill").style.width = "0%";
    $("telemetry-state").textContent = "Running";
    $("download-bar").hidden = true;
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

    state.rendering = true;
    resetTelemetry();
    $("telemetry-log").textContent = "Submitting Mix configuration…";
    renderAll();

    const cfgPayload = {
      sessionId: state.sessionId,
      trackIds: state.tracks.map((t) => t.id),
      bgPath: state.bgMode === "local" ? $("bg-path-input").value.trim() : "",
      out: $("out-path-input").value.trim() || "output.mp4",
      timestamps: $("timestamps-path-input").value.trim() || "timestamps.txt",
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
      appendLog(payload.message);
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
    $("probe-local-btn").addEventListener("click", probeLocalFolder);

    $("bg-file-input").addEventListener("change", (e) => handleBgFile(e.target.files[0]));
    bindDropzone($("bg-dropzone"), (files) => handleBgFile(files[0]));
    $("bg-path-input").addEventListener("input", () => renderAll());

    $("shuffle-toggle").addEventListener("change", () => {
      if ($("shuffle-toggle").checked && state.tracks.length > 1) {
        shuffleTracksInPlace();
      }
      renderAll();
    });

    ["crossfade-slider", "normalize-toggle", "overlay-toggle", "font-name-input", "title-duration-input", "out-path-input", "timestamps-path-input"].forEach((id) => {
      $(id).addEventListener("input", renderAll);
      $(id).addEventListener("change", renderAll);
    });

    $("copy-chapters-btn").addEventListener("click", () => copyText($("chapters-preview").textContent, $("copy-chapters-btn")));
    $("copy-cli-btn").addEventListener("click", () => copyText($("cli-preview").textContent, $("copy-cli-btn")));
    $("render-btn").addEventListener("click", startRender);
  }

  initTheme();
  bindEvents();
  loadHealth();
  renderAll();
})();
