(() => {
  const uploadForm = document.getElementById("upload-form");
  const progressBar = document.getElementById("upload-progress-bar");
  const progressLabel = document.getElementById("upload-progress-label");
  const uploadMessage = document.getElementById("upload-message");
  const selfUpdateForm = document.getElementById("self-update-form");
  const selfUpdateProgressBar = document.getElementById("self-update-progress-bar");
  const selfUpdateProgressLabel = document.getElementById("self-update-progress-label");
  const selfUpdateMessage = document.getElementById("self-update-message");
  const logPanel = document.getElementById("log-panel");
  const logDeploymentId = document.getElementById("log-deployment-id");
  const deploymentsContainer = document.getElementById("deployments-container");
  const runtimeSummary = document.getElementById("runtime-summary");
  const maxUploadLabel = document.getElementById("max-upload-label");
  const projectSelect = document.getElementById("project-select");
  const uploadReplaceMode = document.getElementById("upload-replace-mode");
  const targetVersionInput = document.getElementById("target-version-input");
  const nextVersionLabel = document.getElementById("next-version-label");

  const projectSidebar = document.getElementById("project-sidebar");
  const activeProjectTitle = document.getElementById("active-project-title");
  const addProjectBtn = document.getElementById("add-project-btn");
  const deleteProjectBtn = document.getElementById("delete-project-btn");
  const projectForm = document.getElementById("project-form");
  const projectMessage = document.getElementById("project-message");

  const systemForm = document.getElementById("system-form");
  const systemMessage = document.getElementById("system-message");
  const defaultProjectSelect = document.getElementById("default-project-select");
  const openSystemConfigBtn = document.getElementById("open-system-config-btn");
  const systemConfigDialog = document.getElementById("system-config-dialog");
  const systemConfigClose = document.getElementById("system-config-close");
  const openSelfUpdateBtn = document.getElementById("open-self-update-btn");
  const selfUpdateDialog = document.getElementById("self-update-dialog");
  const selfUpdateClose = document.getElementById("self-update-close");

  const projectCreateDialog = document.getElementById("project-create-dialog");
  const projectCreateForm = document.getElementById("project-create-form");
  const projectCreateCancel = document.getElementById("project-create-cancel");
  const projectCreateMessage = document.getElementById("project-create-message");

  const changesDialog = document.getElementById("changes-dialog");
  const changesDialogTitle = document.getElementById("changes-dialog-title");
  const changesDialogSubtitle = document.getElementById("changes-dialog-subtitle");
  const changesIgnoreList = document.getElementById("changes-ignore-list");
  const changesFileBody = document.getElementById("changes-file-body");
  const changesDialogClose = document.getElementById("changes-dialog-close");

  let eventSource = null;
  let configCache = null;
  let projectsCache = [];
  let activeProjectId = "";

  function setText(el, text) {
    if (el) el.textContent = text;
  }

  function setSystemMessage(text) {
    setText(systemMessage, text);
  }

  function setProjectMessage(text) {
    setText(projectMessage, text);
  }

  function setCreateMessage(text) {
    setText(projectCreateMessage, text);
  }

  function setSelfUpdateMessage(text) {
    setText(selfUpdateMessage, text);
  }

  function openDialog(dialogEl) {
    if (!dialogEl) return;
    if (typeof dialogEl.showModal === "function") {
      dialogEl.showModal();
    } else {
      dialogEl.setAttribute("open", "open");
    }
  }

  function closeDialog(dialogEl) {
    if (!dialogEl) return;
    if (typeof dialogEl.close === "function") {
      dialogEl.close();
    } else {
      dialogEl.removeAttribute("open");
    }
  }

  function isValidVersion(version) {
    return /^\d+\.\d+\.\d+$/.test((version || "").trim());
  }

  function nextPatchVersion(version) {
    const v = (version || "").trim();
    if (!isValidVersion(v)) return "0.0.1";
    const parts = v.split(".").map((n) => Number(n));
    return `${parts[0]}.${parts[1]}.${parts[2] + 1}`;
  }

  function formatBytes(size) {
    const s = Number(size || 0);
    if (!Number.isFinite(s) || s < 0) return "-";
    if (s < 1024) return `${s} B`;
    const units = ["KiB", "MiB", "GiB", "TiB"];
    let value = s;
    let idx = -1;
    while (value >= 1024 && idx < units.length - 1) {
      value /= 1024;
      idx += 1;
    }
    return `${value.toFixed(1)} ${units[idx]}`;
  }

  function actionClass(action) {
    if (action === "added") return "text-emerald-700";
    if (action === "updated") return "text-amber-700";
    return "text-rose-700";
  }

  function appendLog(text, level = "info") {
    if (!logPanel) return;
    const line = document.createElement("div");
    const levelClass =
      level === "error" ? "text-rose-300" : level === "warn" ? "text-amber-300" : "text-emerald-300";
    line.className = levelClass;
    line.textContent = text;
    logPanel.appendChild(line);
    logPanel.scrollTop = logPanel.scrollHeight;
  }

  function setProgress(percent) {
    if (!progressBar || !progressLabel) return;
    const p = Math.max(0, Math.min(100, Math.floor(percent)));
    progressBar.style.width = `${p}%`;
    progressLabel.textContent = `${p}%`;
  }

  function setSelfUpdateProgress(percent) {
    if (!selfUpdateProgressBar || !selfUpdateProgressLabel) return;
    const p = Math.max(0, Math.min(100, Math.floor(percent)));
    selfUpdateProgressBar.style.width = `${p}%`;
    selfUpdateProgressLabel.textContent = `${p}%`;
  }

  function getProjectByID(id) {
    const pid = `${id || ""}`.trim();
    if (!pid) return null;
    return projectsCache.find((p) => p.id === pid) || null;
  }

  function getActiveProject() {
    if (activeProjectId) {
      const p = getProjectByID(activeProjectId);
      if (p) return p;
    }
    if (projectsCache.length > 0) return projectsCache[0];
    return null;
  }

  function syncRuntimeMeta(project) {
    const p = project || getActiveProject();
    const serviceName = p?.service_name || "-";
    const targetDir = p?.target_dir || "-";
    const currentVersion = p?.current_version || "-";
    const maxUpload = p?.max_upload_mb || "-";
    setText(runtimeSummary, `服务: ${serviceName} | 目录: ${targetDir} | 当前版本: ${currentVersion}`);
    setText(maxUploadLabel, maxUpload);
    setText(nextVersionLabel, `默认下一版本: ${nextPatchVersion(currentVersion || "0.0.1")}`);
    if (targetVersionInput) {
      targetVersionInput.placeholder = `留空自动递增为 ${nextPatchVersion(currentVersion || "0.0.1")}`;
    }
    if (uploadReplaceMode) {
      uploadReplaceMode.value = p?.default_replace_mode === "partial" ? "partial" : "full";
    }
  }

  function renderUploadProjectSelect(projects, selectedID) {
    if (!projectSelect) return;
    projectSelect.innerHTML = "";
    projects.forEach((p) => {
      const opt = document.createElement("option");
      opt.value = p.id;
      opt.textContent = `${p.name || p.id} (${p.service_name || "-"})`;
      projectSelect.appendChild(opt);
    });
    if (projects.length === 0) return;
    const exists = projects.some((p) => p.id === selectedID);
    projectSelect.value = exists ? selectedID : projects[0].id;
  }

  function renderDefaultProjectSelect(projects, defaultProjectID) {
    if (!defaultProjectSelect) return;
    defaultProjectSelect.innerHTML = "";
    projects.forEach((p) => {
      const opt = document.createElement("option");
      opt.value = p.id;
      opt.textContent = `${p.name || p.id} (${p.id})`;
      defaultProjectSelect.appendChild(opt);
    });
    if (projects.length === 0) return;
    const exists = projects.some((p) => p.id === defaultProjectID);
    defaultProjectSelect.value = exists ? defaultProjectID : projects[0].id;
  }

  function renderSidebar(projects, defaultProjectID) {
    if (!projectSidebar) return;
    projectSidebar.innerHTML = "";
    if (projects.length === 0) {
      const empty = document.createElement("div");
      empty.className = "text-xs text-slate-500";
      empty.textContent = "暂无程序";
      projectSidebar.appendChild(empty);
      return;
    }
    projects.forEach((p) => {
      const isActive = p.id === activeProjectId;
      const isDefault = p.id === defaultProjectID;
      const btn = document.createElement("button");
      btn.type = "button";
      btn.className = `w-full text-left rounded-lg border px-3 py-2 transition ${
        isActive ? "border-sky-400 bg-sky-50/10" : "border-slate-300 hover:bg-slate-50/10"
      }`;
      btn.dataset.projectId = p.id;
      btn.innerHTML = `
        <div class="flex items-center justify-between gap-2">
          <span class="font-medium text-sm">${p.name || p.id}</span>
          ${isDefault ? '<span class="text-[10px] px-1.5 py-0.5 rounded border border-emerald-400 text-emerald-700">默认</span>' : ""}
        </div>
        <div class="mt-1 text-xs text-slate-500 font-mono break-all">${p.id}</div>
        <div class="mt-1 text-xs text-slate-500 break-all">版本: ${p.current_version || "-"} | 服务: ${p.service_name || "-"}</div>
        <div class="mt-1 text-xs text-slate-500 break-all">默认替换: ${p.default_replace_mode === "partial" ? "partial" : "full"}</div>
      `;
      btn.addEventListener("click", () => selectProject(p.id));
      projectSidebar.appendChild(btn);
    });
  }

  function fillProjectForm(project) {
    if (!projectForm) return;
    const map = {
      project_id: project?.id || "",
      name: project?.name || "",
      current_version: project?.current_version || "",
      service_name: project?.service_name || "",
      target_dir: project?.target_dir || "",
      max_upload_mb: project?.max_upload_mb || "",
      default_replace_mode: project?.default_replace_mode === "partial" ? "partial" : "full",
      backup_ignore_text: Array.isArray(project?.backup_ignore) ? project.backup_ignore.join("\n") : "",
      replace_ignore_text: Array.isArray(project?.replace_ignore) ? project.replace_ignore.join("\n") : "",
    };
    Object.keys(map).forEach((k) => {
      const input = projectForm.elements.namedItem(k);
      if (input) input.value = map[k];
    });
    const setDefaultInput = projectForm.elements.namedItem("set_default_project");
    if (setDefaultInput) {
      setDefaultInput.checked = configCache?.default_project_id === project?.id;
    }
    const title = project ? `当前程序: ${project.name || project.id} (${project.id})` : "未选择程序";
    setText(activeProjectTitle, title);
  }

  function fillSystemForm(cfg) {
    if (!systemForm) return;
    const map = {
      listen_addr: cfg.listen_addr || "",
      session_cookie: cfg.session_cookie || "",
      upload_dir: cfg.upload_dir || "",
      work_dir: cfg.work_dir || "",
      backup_dir: cfg.backup_dir || "",
      deployments_file: cfg.deployments_file || "",
      log_file: cfg.log_file || "",
    };
    Object.keys(map).forEach((k) => {
      const input = systemForm.elements.namedItem(k);
      if (input) input.value = map[k];
    });
    const keyInput = systemForm.elements.namedItem("new_auth_key");
    if (keyInput) keyInput.value = "";
  }

  function selectProject(projectID, options = {}) {
    const { syncUpload = true } = options;
    const project = getProjectByID(projectID) || projectsCache[0] || null;
    if (!project) {
      activeProjectId = "";
      fillProjectForm(null);
      syncRuntimeMeta(null);
      return;
    }
    activeProjectId = project.id;
    fillProjectForm(project);
    syncRuntimeMeta(project);
    renderSidebar(projectsCache, configCache?.default_project_id || "");
    if (syncUpload && projectSelect) {
      projectSelect.value = project.id;
    }
  }

  function normalizeConfigPayload(payload) {
    if (payload && payload.config && payload.config.projects) return payload.config;
    return payload;
  }

  async function loadConfig(preferredProjectID = "", silent = false) {
    try {
      const res = await fetch("/api/config", { credentials: "same-origin" });
      if (!res.ok) {
        setSystemMessage(`读取配置失败 (${res.status})`);
        return;
      }
      const payload = await res.json();
      const cfg = normalizeConfigPayload(payload);
      configCache = cfg || {};
      projectsCache = Array.isArray(cfg?.projects) ? cfg.projects : [];
      fillSystemForm(configCache);
      renderDefaultProjectSelect(projectsCache, configCache.default_project_id);
      const candidateID =
        `${preferredProjectID || ""}`.trim() ||
        `${activeProjectId || ""}`.trim() ||
        `${configCache.default_project_id || ""}`.trim() ||
        (projectsCache[0] ? projectsCache[0].id : "");
      renderUploadProjectSelect(projectsCache, candidateID);
      selectProject(candidateID, { syncUpload: false });
      if (!silent) {
        setSystemMessage("配置已加载");
        setProjectMessage("配置已加载");
      }
    } catch (_e) {
      setSystemMessage("读取配置失败");
    }
  }

  function connectLogs(id) {
    if (!id) return;
    if (eventSource) {
      eventSource.close();
      eventSource = null;
    }
    if (!logPanel) return;
    logPanel.innerHTML = "";
    setText(logDeploymentId, `当前日志: ${id}`);
    appendLog(`[${new Date().toLocaleTimeString()}] 连接日志流...`);
    eventSource = new EventSource(`/api/deployments/${id}/events`);
    eventSource.onmessage = (e) => {
      try {
        const payload = JSON.parse(e.data);
        appendLog(`[${payload.time}] [${payload.level}] ${payload.text}`, payload.level);
      } catch (_err) {
        appendLog(e.data);
      }
    };
    eventSource.onerror = () => {
      appendLog(`[${new Date().toLocaleTimeString()}] 日志连接中断，等待重连...`, "warn");
    };
  }

  async function refreshDeployments() {
    if (!deploymentsContainer) return;
    const pageMeta = document.getElementById("deployments-page-meta");
    const limitRaw = Number(pageMeta?.dataset?.limit || 20);
    const limit = Number.isFinite(limitRaw) && limitRaw > 0 ? Math.floor(limitRaw) : 20;
    try {
      const res = await fetch(`/partials/deployments?offset=0&limit=${limit}`, { credentials: "same-origin" });
      if (!res.ok) return;
      deploymentsContainer.innerHTML = await res.text();
    } catch (_e) {}
  }

  async function showChangesDialog(id) {
    if (!id || !changesDialog) return;
    changesDialogTitle.textContent = `变更明细 - ${id}`;
    changesDialogSubtitle.textContent = "加载中...";
    changesIgnoreList.innerHTML = "";
    changesFileBody.innerHTML = "";
    openDialog(changesDialog);
    try {
      const res = await fetch(`/api/deployments/${id}`, { credentials: "same-origin" });
      if (!res.ok) {
        changesDialogSubtitle.textContent = `加载失败 (${res.status})`;
        return;
      }
      const dep = await res.json();
      const changed = Array.isArray(dep.changed) ? dep.changed : [];
      let replaceIgnore = Array.isArray(dep.replace_ignore) ? dep.replace_ignore : [];
      if (replaceIgnore.length === 0 && projectForm) {
        const raw = `${projectForm.elements.namedItem("replace_ignore_text")?.value || ""}`;
        replaceIgnore = raw
          .split("\n")
          .map((v) => v.trim())
          .filter((v) => v.length > 0);
      }
      changesDialogSubtitle.textContent = `程序: ${dep.project_name || dep.project_id || "-"} | 类型: ${dep.type || "-"} | 版本: ${dep.version || "-"} | 状态: ${dep.status || "-"} | 替换模式: ${dep.replace_mode || "full"}`;

      if (replaceIgnore.length === 0) {
        const empty = document.createElement("div");
        empty.className = "text-slate-500";
        empty.textContent = "未配置替换忽略规则";
        changesIgnoreList.appendChild(empty);
      } else {
        replaceIgnore.forEach((rule) => {
          const row = document.createElement("div");
          row.className = "break-all";
          row.textContent = rule;
          changesIgnoreList.appendChild(row);
        });
      }

      if (changed.length === 0) {
        const tr = document.createElement("tr");
        const td = document.createElement("td");
        td.colSpan = 3;
        td.className = "px-2 py-3 text-slate-500";
        td.textContent = "无文件变更";
        tr.appendChild(td);
        changesFileBody.appendChild(tr);
      } else {
        changed.forEach((item) => {
          const tr = document.createElement("tr");
          tr.className = "border-b border-slate-200";

          const tdAction = document.createElement("td");
          tdAction.className = `px-2 py-2 font-mono ${actionClass(item.action)}`;
          tdAction.textContent = item.action || "-";

          const tdPath = document.createElement("td");
          tdPath.className = "px-2 py-2 font-mono break-all";
          tdPath.textContent = item.path || "-";

          const tdSize = document.createElement("td");
          tdSize.className = "px-2 py-2 text-slate-500";
          tdSize.textContent = item.action === "deleted" ? "-" : formatBytes(item.size);

          tr.appendChild(tdAction);
          tr.appendChild(tdPath);
          tr.appendChild(tdSize);
          changesFileBody.appendChild(tr);
        });
      }
    } catch (_e) {
      changesDialogSubtitle.textContent = "加载失败";
    }
  }

  window.refreshDeployments = refreshDeployments;
  window.updaterViewLogs = connectLogs;
  window.updaterShowChanges = showChangesDialog;

  if (projectSelect) {
    projectSelect.addEventListener("change", () => {
      selectProject(projectSelect.value, { syncUpload: false });
    });
  }

  if (uploadForm) {
    uploadForm.addEventListener("submit", (e) => {
      e.preventDefault();
      const formData = new FormData(uploadForm);
      const selectedID = `${formData.get("project_id") || activeProjectId || ""}`.trim();
      if (!selectedID) {
        uploadMessage.textContent = "请选择程序";
        return;
      }
      formData.set("project_id", selectedID);
      if (!formData.get("package")) {
        uploadMessage.textContent = "请选择 zip 文件";
        return;
      }
      const targetVersion = `${formData.get("target_version") || ""}`.trim();
      if (targetVersion && !isValidVersion(targetVersion)) {
        uploadMessage.textContent = "版本号格式错误，示例: 0.0.2 / 0.1.1 / 1.0.1";
        return;
      }
      const replaceMode = `${formData.get("replace_mode") || ""}`.trim().toLowerCase();
      if (replaceMode !== "full" && replaceMode !== "partial") {
        formData.set("replace_mode", "full");
      }

      setProgress(0);
      uploadMessage.textContent = "正在上传...";
      const xhr = new XMLHttpRequest();
      xhr.open("POST", "/api/upload", true);
      xhr.withCredentials = true;

      xhr.upload.onprogress = (ev) => {
        if (!ev.lengthComputable) return;
        setProgress((ev.loaded / ev.total) * 100);
      };

      xhr.onload = () => {
        if (xhr.status >= 200 && xhr.status < 300) {
          let payload = {};
          try {
            payload = JSON.parse(xhr.responseText);
          } catch (_err) {}
          setProgress(100);
          uploadMessage.textContent = `上传完成，任务ID: ${payload.id || "-"}，程序: ${payload.project_name || payload.project_id || "-"}，目标版本: ${payload.version || "-"}`;
          if (payload.id) connectLogs(payload.id);
          refreshDeployments();
          loadConfig(selectedID, true);
          uploadForm.reset();
          if (projectSelect) projectSelect.value = selectedID;
          return;
        }
        let msg = `上传失败 (${xhr.status})`;
        try {
          const payload = JSON.parse(xhr.responseText);
          if (payload.error) msg = payload.error;
        } catch (_err) {}
        uploadMessage.textContent = msg;
      };

      xhr.onerror = () => {
        uploadMessage.textContent = "网络错误，上传失败";
      };

      xhr.send(formData);
    });
  }

  if (selfUpdateForm) {
    selfUpdateForm.addEventListener("submit", (e) => {
      e.preventDefault();
      const formData = new FormData(selfUpdateForm);
      if (!formData.get("package")) {
        setSelfUpdateMessage("请选择新的 exe 文件");
        return;
      }
      const targetVersion = `${formData.get("target_version") || ""}`.trim();
      if (targetVersion && !isValidVersion(targetVersion)) {
        setSelfUpdateMessage("版本号格式错误，示例: 0.0.2 / 0.1.1 / 1.0.1");
        return;
      }

      setSelfUpdateProgress(0);
      setSelfUpdateMessage("正在上传自更新包...");

      const xhr = new XMLHttpRequest();
      xhr.open("POST", "/api/self-update", true);
      xhr.withCredentials = true;

      xhr.upload.onprogress = (ev) => {
        if (!ev.lengthComputable) return;
        setSelfUpdateProgress((ev.loaded / ev.total) * 100);
      };

      xhr.onload = () => {
        if (xhr.status >= 200 && xhr.status < 300) {
          let payload = {};
          try {
            payload = JSON.parse(xhr.responseText);
          } catch (_err) {}
          setSelfUpdateProgress(100);
          setSelfUpdateMessage(
            `自更新任务已创建: ${payload.id || "-"}。程序将自动重启，页面会在服务恢复后自动刷新。`
          );
          if (payload.id) {
            connectLogs(payload.id);
          }
          refreshDeployments();
          let attempts = 0;
          const timer = window.setInterval(async () => {
            attempts += 1;
            try {
              const res = await fetch("/api/config", { credentials: "same-origin" });
              if (res.ok || res.status === 401) {
                window.clearInterval(timer);
                window.location.reload();
                return;
              }
            } catch (_err) {}
            if (attempts >= 60) {
              window.clearInterval(timer);
            }
          }, 2000);
          selfUpdateForm.reset();
          return;
        }
        let msg = `自更新上传失败 (${xhr.status})`;
        try {
          const payload = JSON.parse(xhr.responseText);
          if (payload.error) msg = payload.error;
        } catch (_err) {}
        setSelfUpdateMessage(msg);
      };

      xhr.onerror = () => {
        setSelfUpdateMessage("网络错误，自更新上传失败");
      };

      xhr.send(formData);
    });
  }

  if (systemForm) {
    systemForm.addEventListener("submit", async (e) => {
      e.preventDefault();
      const formData = new FormData(systemForm);
      formData.set("scope", "system");
      setSystemMessage("保存中...");
      try {
        const res = await fetch("/api/config", {
          method: "POST",
          body: formData,
          credentials: "same-origin",
        });
        const payload = await res.json().catch(() => ({}));
        if (!res.ok) {
          setSystemMessage(payload.error || `保存失败 (${res.status})`);
          return;
        }
        let msg = payload.message || "保存成功";
        if (payload.restart_needed) {
          msg = `${msg}（以下项需重启生效: ${(payload.restart_fields || []).join(", ")}）`;
        }
        setSystemMessage(msg);
        await loadConfig(activeProjectId, true);
      } catch (_e) {
        setSystemMessage("保存失败");
      }
    });
  }

  if (projectForm) {
    projectForm.addEventListener("submit", async (e) => {
      e.preventDefault();
      const formData = new FormData(projectForm);
      formData.set("scope", "project");
      setProjectMessage("保存中...");
      try {
        const res = await fetch("/api/config", {
          method: "POST",
          body: formData,
          credentials: "same-origin",
        });
        const payload = await res.json().catch(() => ({}));
        if (!res.ok) {
          setProjectMessage(payload.error || `保存失败 (${res.status})`);
          return;
        }
        let msg = payload.message || "保存成功";
        if (payload.restart_needed) {
          msg = `${msg}（以下项需重启生效: ${(payload.restart_fields || []).join(", ")}）`;
        }
        setProjectMessage(msg);
        await loadConfig(payload.active_project_id || activeProjectId, true);
      } catch (_e) {
        setProjectMessage("保存失败");
      }
    });
  }

  if (addProjectBtn && projectCreateDialog) {
    addProjectBtn.addEventListener("click", () => {
      setCreateMessage("");
      openDialog(projectCreateDialog);
    });
  }

  if (projectCreateCancel && projectCreateDialog) {
    projectCreateCancel.addEventListener("click", (e) => {
      e.preventDefault();
      closeDialog(projectCreateDialog);
    });
  }

  if (projectCreateForm) {
    projectCreateForm.addEventListener("submit", async (e) => {
      e.preventDefault();
      const formData = new FormData(projectCreateForm);
      setCreateMessage("创建中...");
      try {
        const res = await fetch("/api/projects", {
          method: "POST",
          body: formData,
          credentials: "same-origin",
        });
        const payload = await res.json().catch(() => ({}));
        if (!res.ok) {
          setCreateMessage(payload.error || `创建失败 (${res.status})`);
          return;
        }
        setCreateMessage(payload.message || "创建成功");
        projectCreateForm.reset();
        closeDialog(projectCreateDialog);
        await loadConfig(payload.active_project_id || "", true);
      } catch (_e) {
        setCreateMessage("创建失败");
      }
    });
  }

  if (openSystemConfigBtn && systemConfigDialog) {
    openSystemConfigBtn.addEventListener("click", () => {
      setSystemMessage("");
      openDialog(systemConfigDialog);
    });
  }

  if (openSelfUpdateBtn && selfUpdateDialog) {
    openSelfUpdateBtn.addEventListener("click", () => {
      setSelfUpdateMessage("");
      setSelfUpdateProgress(0);
      openDialog(selfUpdateDialog);
    });
  }

  if (selfUpdateClose && selfUpdateDialog) {
    selfUpdateClose.addEventListener("click", () => {
      closeDialog(selfUpdateDialog);
    });
    selfUpdateDialog.addEventListener("click", (e) => {
      const rect = selfUpdateDialog.getBoundingClientRect();
      const inDialog =
        rect.top <= e.clientY &&
        e.clientY <= rect.top + rect.height &&
        rect.left <= e.clientX &&
        e.clientX <= rect.left + rect.width;
      if (!inDialog) {
        closeDialog(selfUpdateDialog);
      }
    });
  }

  if (systemConfigClose && systemConfigDialog) {
    systemConfigClose.addEventListener("click", () => {
      closeDialog(systemConfigDialog);
    });
    systemConfigDialog.addEventListener("click", (e) => {
      const rect = systemConfigDialog.getBoundingClientRect();
      const inDialog =
        rect.top <= e.clientY &&
        e.clientY <= rect.top + rect.height &&
        rect.left <= e.clientX &&
        e.clientX <= rect.left + rect.width;
      if (!inDialog) {
        closeDialog(systemConfigDialog);
      }
    });
  }

  if (deleteProjectBtn) {
    deleteProjectBtn.addEventListener("click", async () => {
      const p = getActiveProject();
      if (!p) return;
      if (!window.confirm(`确认删除程序 ${p.name || p.id} (${p.id}) 吗？`)) {
        return;
      }
      setProjectMessage("删除中...");
      try {
        const res = await fetch(`/api/projects/${encodeURIComponent(p.id)}`, {
          method: "DELETE",
          credentials: "same-origin",
        });
        const payload = await res.json().catch(() => ({}));
        if (!res.ok) {
          setProjectMessage(payload.error || `删除失败 (${res.status})`);
          return;
        }
        setProjectMessage(payload.message || "删除成功");
        await loadConfig(payload.active_project_id || "", true);
      } catch (_e) {
        setProjectMessage("删除失败");
      }
    });
  }

  if (changesDialogClose && changesDialog) {
    changesDialogClose.addEventListener("click", () => {
      closeDialog(changesDialog);
    });
    changesDialog.addEventListener("click", (e) => {
      const rect = changesDialog.getBoundingClientRect();
      const inDialog =
        rect.top <= e.clientY &&
        e.clientY <= rect.top + rect.height &&
        rect.left <= e.clientX &&
        e.clientX <= rect.left + rect.width;
      if (!inDialog) {
        closeDialog(changesDialog);
      }
    });
  }

  loadConfig();
})();
