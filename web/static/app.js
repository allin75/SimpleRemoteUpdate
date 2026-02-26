(() => {
  const uploadForm = document.getElementById("upload-form");
  const progressBar = document.getElementById("upload-progress-bar");
  const progressLabel = document.getElementById("upload-progress-label");
  const uploadMessage = document.getElementById("upload-message");
  const logPanel = document.getElementById("log-panel");
  const logDeploymentId = document.getElementById("log-deployment-id");
  const deploymentsContainer = document.getElementById("deployments-container");
  const configForm = document.getElementById("config-form");
  const configMessage = document.getElementById("config-message");
  const runtimeSummary = document.getElementById("runtime-summary");
  const maxUploadLabel = document.getElementById("max-upload-label");
  const projectSelect = document.getElementById("project-select");
  const targetVersionInput = document.getElementById("target-version-input");
  const nextVersionLabel = document.getElementById("next-version-label");
  const changesDialog = document.getElementById("changes-dialog");
  const changesDialogTitle = document.getElementById("changes-dialog-title");
  const changesDialogSubtitle = document.getElementById("changes-dialog-subtitle");
  const changesIgnoreList = document.getElementById("changes-ignore-list");
  const changesFileBody = document.getElementById("changes-file-body");
  const changesDialogClose = document.getElementById("changes-dialog-close");

  let eventSource = null;
  let projectsCache = [];

  function setConfigMessage(text) {
    if (configMessage) configMessage.textContent = text;
  }

  function syncRuntimeMeta(cfg, selectedProject = null) {
    const p = selectedProject || getSelectedProject() || null;
    const serviceName = p?.service_name || cfg.service_name || "-";
    const targetDir = p?.target_dir || cfg.target_dir || "-";
    const currentVersion = p?.current_version || cfg.current_version || "-";
    const maxUpload = p?.max_upload_mb || cfg.max_upload_mb || "-";
    if (runtimeSummary) {
      runtimeSummary.textContent = `服务: ${serviceName} | 目录: ${targetDir} | 当前版本: ${currentVersion}`;
    }
    if (maxUploadLabel) {
      maxUploadLabel.textContent = maxUpload;
    }
    if (nextVersionLabel) {
      nextVersionLabel.textContent = `默认下一版本: ${nextPatchVersion(currentVersion || "0.0.1")}`;
    }
    if (targetVersionInput) {
      targetVersionInput.placeholder = `留空自动递增为 ${nextPatchVersion(currentVersion || "0.0.1")}`;
    }
  }

  function getSelectedProject() {
    if (!projectSelect || projectsCache.length === 0) return null;
    const id = `${projectSelect.value || ""}`.trim();
    return projectsCache.find((p) => p.id === id) || projectsCache[0] || null;
  }

  function renderProjectSelect(projects, defaultProjectId) {
    projectsCache = Array.isArray(projects) ? projects : [];
    if (!projectSelect) return;
    projectSelect.innerHTML = "";
    projectsCache.forEach((p) => {
      const opt = document.createElement("option");
      opt.value = p.id;
      opt.textContent = `${p.name || p.id} (${p.service_name || "-"})`;
      projectSelect.appendChild(opt);
    });
    if (projectsCache.length === 0) return;
    const exists = projectsCache.some((p) => p.id === defaultProjectId);
    projectSelect.value = exists ? defaultProjectId : projectsCache[0].id;
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
    const line = document.createElement("div");
    const levelClass =
      level === "error" ? "text-rose-300" : level === "warn" ? "text-amber-300" : "text-emerald-300";
    line.className = levelClass;
    line.textContent = text;
    logPanel.appendChild(line);
    logPanel.scrollTop = logPanel.scrollHeight;
  }

  function setProgress(percent) {
    const p = Math.max(0, Math.min(100, Math.floor(percent)));
    progressBar.style.width = `${p}%`;
    progressLabel.textContent = `${p}%`;
  }

  function connectLogs(id) {
    if (!id) return;
    if (eventSource) {
      eventSource.close();
      eventSource = null;
    }
    logPanel.innerHTML = "";
    logDeploymentId.textContent = `当前日志: ${id}`;
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
    try {
      const res = await fetch("/partials/deployments", { credentials: "same-origin" });
      if (!res.ok) return;
      deploymentsContainer.innerHTML = await res.text();
    } catch (_e) {}
  }

  async function loadConfig() {
    if (!configForm) return;
    try {
      const res = await fetch("/api/config", { credentials: "same-origin" });
      if (!res.ok) {
        setConfigMessage("读取配置失败");
        return;
      }
      const cfg = await res.json();
      Object.keys(cfg).forEach((k) => {
        const input = configForm.elements.namedItem(k);
        if (input) input.value = cfg[k] ?? "";
      });
      if (configForm) {
        const projectsEditor = configForm.elements.namedItem("projects_json");
        if (projectsEditor && Array.isArray(cfg.projects)) {
          projectsEditor.value = JSON.stringify(cfg.projects, null, 2);
        }
      }
      renderProjectSelect(cfg.projects, cfg.default_project_id);
      syncRuntimeMeta(cfg);
      setConfigMessage("配置已加载");
    } catch (_e) {
      setConfigMessage("读取配置失败");
    }
  }

  async function showChangesDialog(id) {
    if (!id || !changesDialog) return;
    changesDialogTitle.textContent = `变更明细 - ${id}`;
    changesDialogSubtitle.textContent = "加载中...";
    changesIgnoreList.innerHTML = "";
    changesFileBody.innerHTML = "";
    if (typeof changesDialog.showModal === "function") {
      changesDialog.showModal();
    } else {
      changesDialog.setAttribute("open", "open");
    }
    try {
      const res = await fetch(`/api/deployments/${id}`, { credentials: "same-origin" });
      if (!res.ok) {
        changesDialogSubtitle.textContent = `加载失败 (${res.status})`;
        return;
      }
      const dep = await res.json();
      const changed = Array.isArray(dep.changed) ? dep.changed : [];
      let replaceIgnore = Array.isArray(dep.replace_ignore) ? dep.replace_ignore : [];
      if (replaceIgnore.length === 0 && configForm) {
        const raw = `${configForm.elements.namedItem("replace_ignore_text")?.value || ""}`;
        replaceIgnore = raw
          .split("\n")
          .map((v) => v.trim())
          .filter((v) => v.length > 0);
      }
      changesDialogSubtitle.textContent = `程序: ${dep.project_name || dep.project_id || "-"} | 类型: ${dep.type || "-"} | 版本: ${dep.version || "-"} | 状态: ${dep.status || "-"}`;

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
      const selected = getSelectedProject();
      const cfgStub = {
        service_name: selected?.service_name,
        target_dir: selected?.target_dir,
        current_version: selected?.current_version,
        max_upload_mb: selected?.max_upload_mb,
      };
      syncRuntimeMeta(cfgStub, selected);
    });
  }

  uploadForm.addEventListener("submit", (e) => {
    e.preventDefault();
    const formData = new FormData(uploadForm);
      if (!formData.get("package")) {
        uploadMessage.textContent = "请选择 zip 文件";
        return;
      }
    if (projectSelect && !`${formData.get("project_id") || ""}`.trim()) {
      uploadMessage.textContent = "请选择程序";
      return;
    }
    const targetVersion = `${formData.get("target_version") || ""}`.trim();
    if (targetVersion && !isValidVersion(targetVersion)) {
      uploadMessage.textContent = "版本号格式错误，示例: 0.0.2 / 0.1.1 / 1.0.1";
      return;
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
        loadConfig();
        uploadForm.reset();
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

  if (configForm) {
    configForm.addEventListener("submit", async (e) => {
      e.preventDefault();
      const projectsEditor = configForm.elements.namedItem("projects_json");
      const defaultProjectIdInput = configForm.elements.namedItem("default_project_id");
      if (projectsEditor && defaultProjectIdInput) {
        try {
          const projects = JSON.parse(`${projectsEditor.value || "[]"}`);
          if (Array.isArray(projects)) {
            const defaultID = `${defaultProjectIdInput.value || ""}`.trim();
            const dp =
              projects.find((p) => `${p.id || ""}`.trim() === defaultID) ||
              projects[0] ||
              null;
            if (dp) {
              const bindMap = {
                service_name: dp.service_name || "",
                target_dir: dp.target_dir || "",
                current_version: dp.current_version || "",
                max_upload_mb: dp.max_upload_mb || "",
                backup_ignore_text: Array.isArray(dp.backup_ignore) ? dp.backup_ignore.join("\n") : "",
                replace_ignore_text: Array.isArray(dp.replace_ignore) ? dp.replace_ignore.join("\n") : "",
              };
              Object.keys(bindMap).forEach((k) => {
                const input = configForm.elements.namedItem(k);
                if (input) input.value = bindMap[k];
              });
            }
          }
        } catch (_err) {
          setConfigMessage("projects_json 不是合法 JSON");
          return;
        }
      }
      const formData = new FormData(configForm);
      setConfigMessage("保存中...");
      try {
        const res = await fetch("/api/config", {
          method: "POST",
          body: formData,
          credentials: "same-origin",
        });
        const payload = await res.json().catch(() => ({}));
        if (!res.ok) {
          setConfigMessage(payload.error || `保存失败 (${res.status})`);
          return;
        }
        setConfigMessage(payload.message || "保存成功");
        const keyInput = configForm.elements.namedItem("new_auth_key");
        if (keyInput) keyInput.value = "";
        if (payload.restart_needed) {
          setConfigMessage(`${payload.message || "保存成功"}（以下项需重启生效: ${(payload.restart_fields || []).join(", ")}）`);
        }
        loadConfig();
      } catch (_e) {
        setConfigMessage("保存失败");
      }
    });
  }

  if (changesDialogClose && changesDialog) {
    changesDialogClose.addEventListener("click", () => {
      if (typeof changesDialog.close === "function") {
        changesDialog.close();
      } else {
        changesDialog.removeAttribute("open");
      }
    });
    changesDialog.addEventListener("click", (e) => {
      const rect = changesDialog.getBoundingClientRect();
      const inDialog =
        rect.top <= e.clientY &&
        e.clientY <= rect.top + rect.height &&
        rect.left <= e.clientX &&
        e.clientX <= rect.left + rect.width;
      if (!inDialog) {
        if (typeof changesDialog.close === "function") {
          changesDialog.close();
        } else {
          changesDialog.removeAttribute("open");
        }
      }
    });
  }

  loadConfig();
})();
