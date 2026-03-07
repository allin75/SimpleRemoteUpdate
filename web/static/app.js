(() => {
  const pageMode = `${document.body?.dataset?.pageMode || "standard"}`.trim().toLowerCase();
  const initialDeployURL = `${document.body?.dataset?.initialDeployUrl || "/initial-deploy"}`.trim() || "/initial-deploy";
  const standardDeployURL = `${document.body?.dataset?.standardDeployUrl || "/"}`.trim() || "/";
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
  const logStageLabel = document.getElementById("log-stage-label");
  const logStageProgressLabel = document.getElementById("log-stage-progress-label");
  const logStageProgressBar = document.getElementById("log-stage-progress-bar");
  const deploymentsContainer = document.getElementById("deployments-container");
  const runtimeSummary = document.getElementById("runtime-summary");
  const maxUploadLabel = document.getElementById("max-upload-label");
  const projectSelect = document.getElementById("project-select");
  const uploadReplaceMode = document.getElementById("upload-replace-mode");
  const targetVersionInput = document.getElementById("target-version-input");
  const scheduledAtInput = document.getElementById("scheduled-at-input");
  const nextVersionLabel = document.getElementById("next-version-label");

  const projectSidebar = document.getElementById("project-sidebar");
  const activeProjectTitle = document.getElementById("active-project-title");
  const addProjectBtn = document.getElementById("add-project-btn");
  const deleteProjectBtn = document.getElementById("delete-project-btn");
  const projectForm = document.getElementById("project-form");
  const projectMessage = document.getElementById("project-message");

  const systemForm = document.getElementById("system-form");
  const systemMessage = document.getElementById("system-message");
  const systemTestEmailBtn = document.getElementById("system-test-email-btn");
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
  const changesIgnoredPaths = document.getElementById("changes-ignored-paths");
  const changesFileBody = document.getElementById("changes-file-body");
  const changesDialogClose = document.getElementById("changes-dialog-close");
  const changesPreviewActions = document.getElementById("changes-preview-actions");
  const changesPreviewHint = document.getElementById("changes-preview-hint");
  const changesPreviewCancel = document.getElementById("changes-preview-cancel");
  const changesPreviewConfirm = document.getElementById("changes-preview-confirm");
  const activeProjectStorageKey = "updater.activeProjectId";

  let eventSource = null;
  let configCache = null;
  let projectsCache = [];
  let activeProjectId = "";
  let pendingUploadPayload = null;

  function getQueryProjectId() {
    try {
      return `${new URLSearchParams(window.location.search).get("project_id") || ""}`.trim();
    } catch (_err) {
      return "";
    }
  }

  function getStoredProjectId() {
    try {
      return `${window.sessionStorage.getItem(activeProjectStorageKey) || ""}`.trim();
    } catch (_err) {
      return "";
    }
  }

  function setStoredProjectId(projectID) {
    try {
      if (`${projectID || ""}`.trim()) {
        window.sessionStorage.setItem(activeProjectStorageKey, `${projectID}`.trim());
      } else {
        window.sessionStorage.removeItem(activeProjectStorageKey);
      }
    } catch (_err) {}
  }

  function buildProjectPageURL(baseURL, projectID) {
    const url = new URL(baseURL, window.location.origin);
    const pid = `${projectID || ""}`.trim();
    if (pid) {
      url.searchParams.set("project_id", pid);
    } else {
      url.searchParams.delete("project_id");
    }
    return `${url.pathname}${url.search}${url.hash}`;
  }

  function syncProjectNavigation(projectID = activeProjectId) {
    const pid = `${projectID || ""}`.trim();
    document.querySelectorAll(`a[href="${standardDeployURL}"], a[href="${initialDeployURL}"]`).forEach((link) => {
      const href = `${link.getAttribute("href") || ""}`.trim();
      if (href === standardDeployURL || href === initialDeployURL) {
        link.setAttribute("href", buildProjectPageURL(href, pid));
      }
    });
    try {
      const currentURL = new URL(window.location.href);
      if (pid) {
        currentURL.searchParams.set("project_id", pid);
      } else {
        currentURL.searchParams.delete("project_id");
      }
      window.history.replaceState(null, "", `${currentURL.pathname}${currentURL.search}${currentURL.hash}`);
    } catch (_err) {}
  }

  function getPreviewSummary(dep) {
    const summary = dep?.summary || {};
    return {
      total: Number(summary.total || 0),
      added: Number(summary.added || 0),
      updated: Number(summary.updated || 0),
      deleted: Number(summary.deleted || 0),
    };
  }

  function buildDeletionConfirmMessage(dep) {
    const summary = getPreviewSummary(dep);
    const projectName = dep?.project_name || dep?.project_id || "当前程序";
    if (summary.deleted > 0) {
      return `检测到常规部署将删除目标目录中 ${summary.deleted} 个现有文件。确认继续后，这些不在压缩包内的文件会被删除。是否继续部署？`;
    }
    return `${projectName} 当前使用 full 全量替换。确认继续后，目标目录中不在压缩包内的现有文件可能被删除。是否继续部署？`;
  }

  function buildInitialClearConfirmMessage(dep) {
    const projectName = dep?.project_name || dep?.project_id || "当前程序";
    return `${projectName} 的目标目录中已存在文件。确认继续后，系统会先清空该目录中的现有内容，再执行首次部署；此操作不可恢复。是否继续？`;
  }

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

  function formatDateTimeLocalValue(date) {
    const d = date instanceof Date ? date : new Date();
    const pad = (n) => `${n}`.padStart(2, "0");
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
  }

  function refreshScheduleInputMin() {
    if (!scheduledAtInput) return;
    const minDate = new Date(Date.now() + 60 * 1000);
    scheduledAtInput.min = formatDateTimeLocalValue(minDate);
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

  function focusLogPanel() {
    if (!logPanel) return;
    try {
      logPanel.scrollIntoView({ behavior: "smooth", block: "center" });
    } catch (_err) {
      logPanel.scrollIntoView();
    }
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

  function setLogStageProgress(stage, percent) {
    const p = Number(percent);
    const stageText = `${stage || ""}`.trim();
    if (logStageLabel) {
      logStageLabel.textContent = stageText ? `阶段: ${stageText}` : "阶段: -";
    }
    if (!Number.isFinite(p) || p < 0) {
      if (logStageProgressLabel) logStageProgressLabel.textContent = "-";
      return;
    }
    const safe = Math.max(0, Math.min(100, Math.floor(p)));
    if (logStageProgressBar) {
      logStageProgressBar.style.width = `${safe}%`;
    }
    if (logStageProgressLabel) {
      logStageProgressLabel.textContent = `${safe}%`;
    }
  }

  function cloneFormData(formData) {
    const cloned = new FormData();
    for (const [key, value] of formData.entries()) {
      cloned.append(key, value);
    }
    return cloned;
  }

  function setPreviewActionsVisible(visible) {
    if (!changesPreviewActions) return;
    if (visible) {
      changesPreviewActions.classList.remove("hidden");
      return;
    }
    changesPreviewActions.classList.add("hidden");
  }

  function setPreviewHint(text) {
    if (!changesPreviewHint) return;
    changesPreviewHint.textContent = `${text || ""}`.trim();
  }

  function setPreviewConfirmLabel(text) {
    if (!changesPreviewConfirm) return;
    changesPreviewConfirm.textContent = `${text || ""}`.trim() || "确认部署";
  }

  function setPreviewConfirmEnabled(enabled) {
    if (!changesPreviewConfirm) return;
    changesPreviewConfirm.disabled = !enabled;
    changesPreviewConfirm.classList.toggle("opacity-50", !enabled);
    changesPreviewConfirm.classList.toggle("cursor-not-allowed", !enabled);
  }

  function clearPendingUpload() {
    pendingUploadPayload = null;
    setPreviewActionsVisible(false);
    setPreviewHint("");
    setPreviewConfirmLabel("确认部署");
    setPreviewConfirmEnabled(true);
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
    const initialMode = p?.allow_initial_deploy ? "允许首次部署" : "仅更新已存在程序";
    const installMode = p?.service_install_mode === "windows_service"
      ? "自动安装原生 Windows 服务"
      : p?.service_install_mode === "nssm"
        ? "自动安装 NSSM 服务"
        : "不安装服务";
    setText(runtimeSummary, `服务: ${serviceName} | 目录: ${targetDir} | 当前版本: ${currentVersion} | ${initialMode} | ${installMode}`);
    setText(maxUploadLabel, maxUpload);
    setText(nextVersionLabel, `默认下一版本: ${nextPatchVersion(currentVersion || "0.0.1")}`);
    if (targetVersionInput) {
      targetVersionInput.placeholder = `留空自动递增为 ${nextPatchVersion(currentVersion || "0.0.1")}`;
    }
    if (uploadReplaceMode) {
      uploadReplaceMode.value = pageMode === "initial" ? "full" : p?.default_replace_mode === "partial" ? "partial" : "full";
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
        <div class="mt-1 text-xs text-slate-500 break-all">首次部署: ${p.allow_initial_deploy ? "允许" : "关闭"} | 服务安装: ${p.service_install_mode === "windows_service" ? "Windows 服务" : p.service_install_mode === "nssm" ? "NSSM" : "无"}</div>
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
      service_install_mode: project?.service_install_mode === "windows_service"
        ? "windows_service"
        : project?.service_install_mode === "nssm"
          ? "nssm"
          : pageMode === "initial"
            ? "nssm"
            : "none",
      service_exe_path: project?.service_exe_path || "",
      service_display_name: project?.service_display_name || "",
      service_description: project?.service_description || "",
      service_start_type: project?.service_start_type || "automatic",
      service_args_text: Array.isArray(project?.service_args) ? project.service_args.join("\n") : "",
      backup_ignore_text: Array.isArray(project?.backup_ignore) ? project.backup_ignore.join("\n") : "",
      replace_ignore_text: Array.isArray(project?.replace_ignore) ? project.replace_ignore.join("\n") : "",
    };
    Object.keys(map).forEach((k) => {
      const input = projectForm.elements.namedItem(k);
      if (!input) return;
      if (input instanceof RadioNodeList) return;
      if (`${input.type || ""}`.toLowerCase() === "checkbox") {
        input.checked = Boolean(map[k]);
        input.value = "true";
        return;
      }
      input.value = map[k];
    });
    const setDefaultInput = projectForm.elements.namedItem("set_default_project");
    if (setDefaultInput) {
      const checked = configCache?.default_project_id === project?.id;
      if (`${setDefaultInput.type || ""}`.toLowerCase() === "checkbox") {
        setDefaultInput.checked = checked;
        setDefaultInput.value = "true";
      } else {
        setDefaultInput.value = checked ? "true" : "false";
      }
    }
    const allowInitialInput = projectForm.elements.namedItem("allow_initial_deploy");
    if (allowInitialInput) {
      const checked = Boolean(project?.allow_initial_deploy);
      if (`${allowInitialInput.type || ""}`.toLowerCase() === "checkbox") {
        allowInitialInput.checked = checked;
        allowInitialInput.value = "true";
      } else {
        allowInitialInput.value = checked ? "true" : "false";
      }
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
      nssm_exe_path: cfg.nssm_exe_path || "nssm.exe",
      notify_email: cfg.notify_email || "",
      self_update_service_name: cfg.self_update_service_name || "",
    };
    Object.keys(map).forEach((k) => {
      const input = systemForm.elements.namedItem(k);
      if (input) input.value = map[k];
    });
    const keyInput = systemForm.elements.namedItem("new_auth_key");
    if (keyInput) keyInput.value = "";
    const notifyKeyInput = systemForm.elements.namedItem("notify_email_auth_code");
    if (notifyKeyInput) notifyKeyInput.value = "";
  }

  function selectProject(projectID, options = {}) {
    const { syncUpload = true } = options;
    const project = getProjectByID(projectID) || projectsCache[0] || null;
    if (!project) {
      activeProjectId = "";
      setStoredProjectId("");
      syncProjectNavigation("");
      fillProjectForm(null);
      syncRuntimeMeta(null);
      return;
    }
    activeProjectId = project.id;
    setStoredProjectId(project.id);
    syncProjectNavigation(project.id);
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
        `${getQueryProjectId() || ""}`.trim() ||
        `${getStoredProjectId() || ""}`.trim() ||
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

  async function connectLogs(id) {
    if (!id) return;
    if (eventSource) {
      eventSource.close();
      eventSource = null;
    }
    if (!logPanel) return;
    logPanel.innerHTML = "";
    focusLogPanel();
    setText(logDeploymentId, `当前日志: ${id}`);
    setLogStageProgress("", -1);
    appendLog(`[${new Date().toLocaleTimeString()}] 正在加载任务信息...`);

    let dep = null;
    try {
      const res = await fetch(`/api/deployments/${id}`, { credentials: "same-origin" });
      if (!res.ok) {
        appendLog(`[${new Date().toLocaleTimeString()}] 任务信息读取失败 (${res.status})`, "error");
        return;
      }
      dep = await res.json();
      appendLog(
        `[${new Date().toLocaleTimeString()}] 任务状态: ${dep.status || "-"} | 类型: ${dep.type || "-"} | 版本: ${dep.version || "-"}`,
      );
      if (dep.error) {
        appendLog(`[${new Date().toLocaleTimeString()}] 错误: ${dep.error}`, "error");
      }
    } catch (_err) {
      appendLog(`[${new Date().toLocaleTimeString()}] 任务信息读取失败`, "error");
      return;
    }

    const status = `${dep?.status || ""}`.trim().toLowerCase();
    const doneStatuses = new Set(["success", "failed", "canceled", "cancelled"]);
    if (doneStatuses.has(status)) {
      appendLog(`[${new Date().toLocaleTimeString()}] 任务已结束，当前显示为任务摘要（无实时增量日志）`, "warn");
      return;
    }

    appendLog(`[${new Date().toLocaleTimeString()}] 连接实时日志流...`);
    eventSource = new EventSource(`/api/deployments/${id}/events`);
    eventSource.onmessage = (e) => {
      try {
        const payload = JSON.parse(e.data);
        appendLog(`[${payload.time}] [${payload.level}] ${payload.text}`, payload.level);
        if (payload && ((payload.stage && `${payload.stage}`.trim() !== "") || Number(payload.progress) >= 0)) {
          setLogStageProgress(payload.stage || "", Number(payload.progress));
        }
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

  function renderChangesDialogData(dep, titleText) {
    if (!changesDialog) return;
    const changed = Array.isArray(dep?.changed) ? dep.changed : [];
    let replaceIgnore = Array.isArray(dep?.replace_ignore) ? dep.replace_ignore : [];
    const ignoredPaths = Array.isArray(dep?.ignored_paths) ? dep.ignored_paths : [];
    const initialDeploy = Boolean(dep?.initial_deploy);
    const initialDeployAllowed = dep?.initial_deploy_allowed !== false;
    const summary = getPreviewSummary(dep);
    const hasDeleteRisk = !initialDeploy && `${dep?.replace_mode || "full"}`.trim().toLowerCase() === "full" && summary.deleted > 0;
    if (!initialDeploy && replaceIgnore.length === 0 && projectForm) {
      const raw = `${projectForm.elements.namedItem("replace_ignore_text")?.value || ""}`;
      replaceIgnore = raw
        .split("\n")
        .map((v) => v.trim())
        .filter((v) => v.length > 0);
    }

    changesDialogTitle.textContent = titleText || "变更明细";
    changesDialogSubtitle.textContent =
      `程序: ${dep?.project_name || dep?.project_id || "-"} | 类型: ${dep?.type || "-"} | 版本: ${dep?.version || "-"} | 状态: ${dep?.status || "-"} | 替换模式: ${dep?.replace_mode || "full"} | 首次部署: ${initialDeploy ? "是" : "否"} | 服务安装: ${dep?.service_install_mode === "windows_service" ? "Windows 服务" : dep?.service_install_mode === "nssm" ? "NSSM" : "无"}`;
    if (`${dep?.type || ""}`.trim() === "preview" && changed.length === 0) {
      changesDialogSubtitle.textContent += " | 结果: 无文件变更，建议取消本次部署";
    }
    if (initialDeploy) {
      changesDialogSubtitle.textContent += ` | 目标目录不存在或为空，将执行首次部署${dep?.service_install_mode === "windows_service" ? "并尝试创建原生 Windows 服务" : dep?.service_install_mode === "nssm" ? "并尝试通过 NSSM 创建服务" : ""}`;
      changesDialogSubtitle.textContent += " | 首次部署不会应用文件忽略规则";
      if (!initialDeployAllowed) {
        changesDialogSubtitle.textContent += " | 当前程序未开启首次部署，正式部署会失败";
      }
    } else if (dep?.requires_initial_clear_confirm) {
      changesDialogSubtitle.textContent += " | 风险提示: 目标目录已有内容，确认后将先清空该目录，再执行首次部署";
    } else if (hasDeleteRisk) {
      changesDialogSubtitle.textContent += ` | 风险提示: 确认部署后将删除目标目录中 ${summary.deleted} 个不在压缩包内的现有文件`;
    }
    changesIgnoreList.innerHTML = "";
    if (changesIgnoredPaths) changesIgnoredPaths.innerHTML = "";
    changesFileBody.innerHTML = "";

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

    if (ignoredPaths.length === 0) {
      const empty = document.createElement("div");
      empty.className = "text-slate-500";
      empty.textContent = "无忽略命中路径";
      if (changesIgnoredPaths) changesIgnoredPaths.appendChild(empty);
    } else {
      ignoredPaths.forEach((path) => {
        const row = document.createElement("div");
        row.className = "break-all";
        row.textContent = path;
        if (changesIgnoredPaths) changesIgnoredPaths.appendChild(row);
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
      return;
    }

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

  async function showChangesDialog(id) {
    if (!id || !changesDialog) return;
    clearPendingUpload();
    changesDialogTitle.textContent = `变更明细 - ${id}`;
    changesDialogSubtitle.textContent = "加载中...";
    changesIgnoreList.innerHTML = "";
    if (changesIgnoredPaths) changesIgnoredPaths.innerHTML = "";
    changesFileBody.innerHTML = "";
    openDialog(changesDialog);
    try {
      const res = await fetch(`/api/deployments/${id}`, { credentials: "same-origin" });
      if (!res.ok) {
        changesDialogSubtitle.textContent = `加载失败 (${res.status})`;
        return;
      }
      const dep = await res.json();
      renderChangesDialogData(dep, `变更明细 - ${id}`);
    } catch (_e) {
      changesDialogSubtitle.textContent = "加载失败";
    }
  }

  window.refreshDeployments = refreshDeployments;
  window.updaterViewLogs = connectLogs;
  window.updaterShowChanges = showChangesDialog;
  refreshScheduleInputMin();

  function buildUploadFormData() {
    if (!uploadForm) return { ok: false, error: "上传表单不存在" };
    const formData = new FormData(uploadForm);
    formData.set("deploy_entry", `${formData.get("deploy_entry") || pageMode || "standard"}`.trim().toLowerCase() || "standard");
    const selectedID = `${formData.get("project_id") || activeProjectId || ""}`.trim();
    if (!selectedID) {
      return { ok: false, error: "请选择程序" };
    }
    formData.set("project_id", selectedID);
    if (!formData.get("package")) {
      return { ok: false, error: "请选择 zip 文件" };
    }
    const targetVersion = `${formData.get("target_version") || ""}`.trim();
    if (targetVersion && !isValidVersion(targetVersion)) {
      return { ok: false, error: "版本号格式错误，示例: 0.0.2 / 0.1.1 / 1.0.1" };
    }
    const replaceMode = `${formData.get("replace_mode") || ""}`.trim().toLowerCase();
    if (replaceMode !== "full" && replaceMode !== "partial") {
      formData.set("replace_mode", "full");
    }
    const scheduledAt = `${formData.get("scheduled_at") || ""}`.trim();
    if (scheduledAt) {
      const planned = new Date(scheduledAt);
      if (!Number.isFinite(planned.getTime())) {
        return { ok: false, error: "计划执行时间格式错误" };
      }
      if (planned.getTime() <= Date.now() + 5000) {
        return { ok: false, error: "计划执行时间必须晚于当前时间至少 5 秒" };
      }
    }
    return { ok: true, selectedID, formData };
  }

  if (projectSelect) {
    projectSelect.addEventListener("change", () => {
      selectProject(projectSelect.value, { syncUpload: false });
    });
  }

  function executeUpload(formData, selectedID) {
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
        const isScheduled = `${payload.status || ""}`.toLowerCase() === "scheduled";
        if (isScheduled) {
          uploadMessage.textContent =
            payload.message || `任务已排队，任务ID: ${payload.id || "-"}，计划执行时间: ${payload.scheduled_at || "-"}`;
        } else {
          uploadMessage.textContent = `上传完成，任务ID: ${payload.id || "-"}，程序: ${payload.project_name || payload.project_id || "-"}，目标版本: ${payload.version || "-"}`;
          if (payload.id) connectLogs(payload.id);
        }
        refreshDeployments();
        loadConfig(selectedID, true);
        uploadForm.reset();
        if (projectSelect) projectSelect.value = selectedID;
        refreshScheduleInputMin();
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
  }

  async function previewBeforeUpload(prepared) {
    await new Promise((resolve) => {
      let analyzeTimer = null;
      let analyzeProgress = 60;
      setProgress(0);
      uploadMessage.textContent = "正在上传预演包...";

      const xhr = new XMLHttpRequest();
      xhr.open("POST", "/api/preview", true);
      xhr.withCredentials = true;

      xhr.upload.onprogress = (ev) => {
        if (!ev.lengthComputable) return;
        // 预演进度前 60% 显示上传进度
        const p = Math.max(0, Math.min(60, Math.floor((ev.loaded / ev.total) * 60)));
        setProgress(p);
      };

      xhr.upload.onloadend = () => {
        setProgress(60);
        uploadMessage.textContent = "服务器正在分析变更...";
        analyzeTimer = window.setInterval(() => {
          analyzeProgress = Math.min(95, analyzeProgress + 1);
          setProgress(analyzeProgress);
        }, 160);
      };

      xhr.onload = () => {
        if (analyzeTimer) {
          window.clearInterval(analyzeTimer);
        }
        if (xhr.status >= 200 && xhr.status < 300) {
          let payload = {};
          try {
            payload = JSON.parse(xhr.responseText);
          } catch (_err) {}
          setProgress(100);
          renderChangesDialogData(
            {
              ...payload,
              status: "preview",
              type: "preview",
            },
            "预演结果（确认后才会部署）"
          );
          pendingUploadPayload = {
            selectedID: prepared.selectedID,
            formData: cloneFormData(prepared.formData),
            requiresDeletionConfirm: false,
            deletionConfirmMessage: "",
            requiresInitialClearConfirm: false,
            initialClearConfirmMessage: "",
          };
          if (payload?.requires_initial_page) {
            setPreviewHint(`检测到目标目录为空或不存在。请切换到“首次部署专页”继续，当前页面已禁止确认部署：${initialDeployURL}`);
            setPreviewConfirmLabel("请前往首次部署专页");
            setPreviewConfirmEnabled(false);
          } else if (payload?.requires_initial_clear_confirm) {
            pendingUploadPayload.requiresInitialClearConfirm = true;
            pendingUploadPayload.initialClearConfirmMessage = buildInitialClearConfirmMessage(payload);
            pendingUploadPayload.formData.set("clear_target_before_deploy", "true");
            setPreviewHint("检测到首次部署目标目录中已存在文件。若继续，系统会先清空该目录中的现有内容，再部署压缩包；此操作不可恢复，请谨慎确认。");
            setPreviewConfirmLabel("确认清空后首次部署");
            setPreviewConfirmEnabled(true);
          } else if (payload?.requires_standard_page) {
            setPreviewHint(`目标目录已有内容，请返回“常规部署”页面继续：${standardDeployURL}`);
            setPreviewConfirmLabel("请返回常规部署");
            setPreviewConfirmEnabled(false);
          } else if (payload?.initial_deploy && payload?.initial_deploy_allowed === false) {
            setPreviewHint("检测到目标目录为空或不存在，但当前程序未开启首次部署；若直接确认，部署会失败，请先在程序配置中开启 allow_initial_deploy。");
            setPreviewConfirmLabel("仍然部署");
            setPreviewConfirmEnabled(true);
          } else if (payload?.initial_deploy) {
            setPreviewHint(
              `检测到首次部署：本次会跳过备份${payload?.service_install_mode === "windows_service" ? "，并在部署后尝试创建原生 Windows 服务" : payload?.service_install_mode === "nssm" ? "，并在部署后尝试通过 NSSM 创建服务" : ""}。`
            );
            setPreviewConfirmLabel("确认部署");
            setPreviewConfirmEnabled(true);
          } else if (`${payload?.replace_mode || "full"}`.trim().toLowerCase() === "full" && Number(payload?.summary?.deleted || 0) > 0) {
            pendingUploadPayload.requiresDeletionConfirm = true;
            pendingUploadPayload.deletionConfirmMessage = buildDeletionConfirmMessage(payload);
            setPreviewHint(`检测到本次常规部署会删除 ${payload?.summary?.deleted || 0} 个目标目录中的现有文件。确认后，这些不在压缩包内的文件将被删除，请谨慎操作。`);
            setPreviewConfirmLabel("确认删除并部署");
            setPreviewConfirmEnabled(true);
          } else if (Number(payload?.summary?.total || 0) === 0) {
            setPreviewHint("预演结果无文件变更，建议取消本次部署。");
            setPreviewConfirmLabel("仍然部署");
            setPreviewConfirmEnabled(true);
          } else {
            setPreviewHint("");
            setPreviewConfirmLabel("确认部署");
            setPreviewConfirmEnabled(true);
          }
          setPreviewActionsVisible(true);
          openDialog(changesDialog);
          uploadMessage.textContent = `预演完成：共 ${payload?.summary?.total || 0} 项变更，请在弹窗确认部署`;
          resolve();
          return;
        }
        let msg = `预演失败 (${xhr.status})`;
        try {
          const payload = JSON.parse(xhr.responseText);
          if (payload.error) msg = payload.error;
        } catch (_err) {}
        uploadMessage.textContent = msg;
        setProgress(0);
        resolve();
      };

      xhr.onerror = () => {
        if (analyzeTimer) {
          window.clearInterval(analyzeTimer);
        }
        uploadMessage.textContent = "网络错误，预演失败";
        setProgress(0);
        resolve();
      };

      xhr.send(cloneFormData(prepared.formData));
    });
  }

  if (uploadForm) {
    uploadForm.addEventListener("submit", async (e) => {
      e.preventDefault();
      const prepared = buildUploadFormData();
      if (!prepared.ok) {
        uploadMessage.textContent = prepared.error || "上传参数不正确";
        return;
      }
      await previewBeforeUpload(prepared);
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

  if (systemTestEmailBtn && systemForm) {
    systemTestEmailBtn.addEventListener("click", async () => {
      const formData = new FormData(systemForm);
      setSystemMessage("正在发送测试邮件...");
      systemTestEmailBtn.disabled = true;
      try {
        const res = await fetch("/api/notify/test", {
          method: "POST",
          body: formData,
          credentials: "same-origin",
        });
        const payload = await res.json().catch(() => ({}));
        if (!res.ok) {
          setSystemMessage(payload.error || `测试邮件发送失败 (${res.status})`);
          return;
        }
        setSystemMessage(payload.message || "测试邮件发送成功");
      } catch (_e) {
        setSystemMessage("测试邮件发送失败");
      } finally {
        systemTestEmailBtn.disabled = false;
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

  if (changesPreviewCancel && changesDialog) {
    changesPreviewCancel.addEventListener("click", () => {
      clearPendingUpload();
      closeDialog(changesDialog);
    });
  }

  if (changesPreviewConfirm) {
    changesPreviewConfirm.addEventListener("click", () => {
      if (!pendingUploadPayload) {
        uploadMessage.textContent = "未找到待确认的上传任务，请重新提交";
        return;
      }
      const payload = pendingUploadPayload;
      if (payload.requiresInitialClearConfirm) {
        const ok = window.confirm(payload.initialClearConfirmMessage || "确认清空后再首次部署吗？");
        if (!ok) {
          return;
        }
      }
      if (payload.requiresDeletionConfirm) {
        const ok = window.confirm(payload.deletionConfirmMessage || "确认继续部署吗？");
        if (!ok) {
          return;
        }
      }
      clearPendingUpload();
      closeDialog(changesDialog);
      executeUpload(cloneFormData(payload.formData), payload.selectedID);
    });
  }

  if (changesDialogClose && changesDialog) {
    changesDialogClose.addEventListener("click", () => {
      clearPendingUpload();
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
        clearPendingUpload();
        closeDialog(changesDialog);
      }
    });
  }

  syncProjectNavigation(getQueryProjectId() || getStoredProjectId());
  loadConfig(getQueryProjectId() || getStoredProjectId());
})();
