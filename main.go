package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func main() {
	handled, err := tryRunSelfUpdateWorker(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "self-update worker failed: %v\n", err)
		os.Exit(1)
	}
	if handled {
		return
	}

	cfg, err := loadConfig("config.json")
	if err != nil {
		panic(err)
	}
	if err := validateRuntimeConfig(cfg); err != nil {
		panic(err)
	}
	if err := ensureDirectories(cfg); err != nil {
		panic(err)
	}

	logWriter, err := newDynamicLogWriter(cfg.LogFile)
	if err != nil {
		panic(err)
	}
	defer logWriter.Close()

	logger := slog.New(slog.NewTextHandler(logWriter, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	if isDefaultAuthHash(cfg.AuthKeySHA256) {
		logger.Warn("当前仍在使用默认密钥哈希，请尽快修改 config.json")
	}

	store, err := newDeploymentStore(cfg.DeploymentsFile)
	if err != nil {
		panic(err)
	}
	tmpl, err := parseTemplates()
	if err != nil {
		panic(err)
	}
	staticFS, err := fs.Sub(webAssets, "web/static")
	if err != nil {
		panic(err)
	}

	app := &App{
		cfg:         cfg,
		cfgPath:     "config.json",
		logWriter:   logWriter,
		logger:      logger,
		templates:   tmpl,
		store:       store,
		sessions:    newSessionManager(),
		events:      newEventHub(),
		static:      http.FileServer(http.FS(staticFS)),
		projectTask: make(map[string]struct{}),
		schedCancel: make(map[string]func()),
	}
	app.resumeScheduledDeployments()

	logger.Info("updater server started",
		"addr", cfg.ListenAddr,
		"default_project", cfg.DefaultProjectID,
		"projects", len(cfg.Projects),
	)
	if err := http.ListenAndServe(cfg.ListenAddr, app.routes()); err != nil {
		panic(err)
	}
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", a.static))
	mux.HandleFunc("/login", a.handleLogin)
	mux.HandleFunc("/logout", a.requireAuth(a.handleLogout))
	mux.HandleFunc("/", a.requireAuth(a.handleIndex))
	mux.HandleFunc("/initial-deploy", a.requireAuth(a.handleInitialDeployPage))
	mux.HandleFunc("/partials/deployments", a.requireAuth(a.handleDeploymentsPartial))
	mux.HandleFunc("/partials/deployments/rows", a.requireAuth(a.handleDeploymentsRows))
	mux.HandleFunc("/api/upload", a.requireAuth(a.handleUpload))
	mux.HandleFunc("/api/preview", a.requireAuth(a.handlePreview))
	mux.HandleFunc("/api/self-update", a.requireAuth(a.handleSelfUpdate))
	mux.HandleFunc("/api/config", a.requireAuth(a.handleConfigAPI))
	mux.HandleFunc("/api/notify/test", a.requireAuth(a.handleNotifyTestAPI))
	mux.HandleFunc("/api/projects", a.requireAuth(a.handleProjectsAPI))
	mux.HandleFunc("/api/projects/", a.requireAuth(a.handleProjectItemAPI))
	mux.HandleFunc("/api/deployments/", a.requireAuth(a.handleDeploymentAPIs))
	return withRecover(mux, a.logger)
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	cfg := a.currentConfig()
	if r.Method == http.MethodGet {
		if _, ok := a.authUser(r); ok {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		_ = a.templates.ExecuteTemplate(w, "login.html", map[string]any{
			"Error":                  "",
			"ShowDefaultPasswordTip": isDefaultAuthHash(cfg.AuthKeySHA256),
		})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	key := r.FormValue("key")
	if !isKeyMatch(cfg.AuthKeySHA256, key) {
		_ = a.templates.ExecuteTemplate(w, "login.html", map[string]any{
			"Error":                  "密钥错误",
			"ShowDefaultPasswordTip": isDefaultAuthHash(cfg.AuthKeySHA256),
		})
		return
	}

	token := a.sessions.Create("key-user", 8*time.Hour)
	http.SetCookie(w, &http.Cookie{
		Name:     cfg.SessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Now().Add(8 * time.Hour),
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	cfg := a.currentConfig()
	cookie, err := r.Cookie(cfg.SessionCookie)
	if err == nil && cookie.Value != "" {
		a.sessions.Delete(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cfg.SessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a.renderConsolePage(w, false)
	return
}

func (a *App) handleInitialDeployPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	a.renderConsolePage(w, true)
}

func (a *App) renderConsolePage(w http.ResponseWriter, initialDeployPage bool) {
	cfg := a.currentConfig()
	project := getDefaultProject(cfg)
	_ = a.templates.ExecuteTemplate(w, "index.html", map[string]any{
		"ServiceName":       project.ServiceName,
		"TargetDir":         project.TargetDir,
		"MaxUploadMB":       project.MaxUploadMB,
		"CurrentVersion":    project.CurrentVersion,
		"InitialDeployPage": initialDeployPage,
		"PageTitle": func() string {
			if initialDeployPage {
				return "首次部署专页"
			}
			return "常规部署"
		}(),
		"StandardDeployPath": "/",
		"InitialDeployPath":  "/initial-deploy",
	})
}

func (a *App) handleDeploymentsPartial(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data := buildDeploymentsPageData(a.store.List(), r)
	_ = a.templates.ExecuteTemplate(w, "deployments.html", data)
}

func (a *App) handleDeploymentsRows(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data := buildDeploymentsPageData(a.store.List(), r)
	_ = a.templates.ExecuteTemplate(w, "deployments_rows_fragment", data)
}

func (a *App) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg := a.currentConfig()

	maxBytes := maxProjectUploadBytes(cfg)
	if maxBytes <= 0 {
		maxBytes = 1024 * 1024 * 1024
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("上传数据解析失败: %v", err)})
		return
	}

	projectID := strings.TrimSpace(r.FormValue("project_id"))
	if projectID == "" {
		projectID = cfg.DefaultProjectID
	}
	project, found := findProjectByID(cfg.Projects, projectID)
	if !found {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("未找到程序: %s", projectID)})
		return
	}
	deployEntry := normalizeDeployEntry(r.FormValue("deploy_entry"))
	clearTargetBeforeDeploy := parseBoolFormValue(r.FormValue("clear_target_before_deploy"))
	targetExists, targetEmpty, targetCheckErr := inspectTargetDirState(project.TargetDir)
	if targetCheckErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": fmt.Sprintf("检查目标目录失败: %v", targetCheckErr)})
		return
	}
	targetHasExistingFiles := targetExists && !targetEmpty
	initialDeploy := isRuntimeInitialDeploy(targetExists, targetEmpty, deployEntry == DeployEntryInitial && clearTargetBeforeDeploy)
	if initialDeploy && deployEntry != DeployEntryInitial {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "检测到目标目录为空或不存在，请前往“首次部署专页”完成首次部署，常规部署页已禁止继续以降低误操作"})
		return
	}
	if targetHasExistingFiles && deployEntry == DeployEntryInitial && !clearTargetBeforeDeploy {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "首次部署专页检测到目标目录已有内容。请先确认“清空现有文件后再部署”，再继续首次部署。"})
		return
	}

	file, header, err := r.FormFile("package")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少上传文件字段 package"})
		return
	}
	defer file.Close()
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".zip") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "仅支持 .zip 文件"})
		return
	}
	projectMaxBytes := project.MaxUploadMB * 1024 * 1024
	if header.Size > 0 && projectMaxBytes > 0 && header.Size > projectMaxBytes {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": fmt.Sprintf("文件超过程序 %s 的上传限制: %d MB", project.Name, project.MaxUploadMB),
		})
		return
	}

	id := newID("dep")
	uploadPath := filepath.Join(cfg.UploadDir, id+".zip")
	if err := saveMultipartFile(file, uploadPath); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": fmt.Sprintf("保存上传文件失败: %v", err)})
		return
	}
	if info, statErr := os.Stat(uploadPath); statErr == nil {
		projectMaxBytes := project.MaxUploadMB * 1024 * 1024
		if projectMaxBytes > 0 && info.Size() > projectMaxBytes {
			_ = os.Remove(uploadPath)
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": fmt.Sprintf("文件超过程序 %s 的上传限制: %d MB", project.Name, project.MaxUploadMB),
			})
			return
		}
	}

	now := time.Now()
	requestVersion := normalizeVersion(r.FormValue("target_version"))
	targetVersion := requestVersion
	if targetVersion == "" {
		nextVer, nextErr := nextPatchVersion(project.CurrentVersion)
		if nextErr != nil {
			_ = os.Remove(uploadPath)
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("当前版本格式错误，无法自动递增: %v", nextErr)})
			return
		}
		targetVersion = nextVer
	}
	if !isValidVersion(targetVersion) {
		_ = os.Remove(uploadPath)
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("版本号格式错误: %s，正确格式示例: 0.0.2 / 0.1.1 / 1.0.1", targetVersion)})
		return
	}
	replaceMode := normalizeReplaceMode(r.FormValue("replace_mode"))
	if strings.TrimSpace(r.FormValue("replace_mode")) == "" {
		replaceMode = normalizeReplaceMode(project.DefaultReplaceMode)
	}
	scheduledAt, hasSchedule, err := parseScheduledAtFormValue(r.FormValue("scheduled_at"))
	if err != nil {
		_ = os.Remove(uploadPath)
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	runNow := !hasSchedule
	locked := false
	if runNow {
		if ok, reason := a.tryAcquireProjectTask(project.ID); !ok {
			_ = os.Remove(uploadPath)
			writeJSON(w, http.StatusConflict, map[string]any{"error": reason})
			return
		}
		locked = true
	}
	defer func() {
		if locked {
			a.releaseProjectTask(project.ID)
		}
	}()

	status := "queued"
	startedAt := now
	var scheduledAtPtr *time.Time
	if hasSchedule {
		status = "scheduled"
		startedAt = time.Time{}
		planned := scheduledAt
		scheduledAtPtr = &planned
	}
	dep := Deployment{
		ID:                      id,
		Type:                    "deploy",
		Version:                 targetVersion,
		ProjectID:               project.ID,
		ProjectName:             project.Name,
		InitialDeploy:           initialDeploy,
		BackupSkipped:           initialDeploy,
		ReplaceMode:             replaceMode,
		BackupIgnore:            append([]string{}, project.BackupIgnore...),
		ReplaceIgnore:           append([]string{}, resolveReplaceIgnoreRulesForTarget(project.TargetDir, project.ReplaceIgnore, project.BackupIgnore)...),
		Status:                  status,
		Note:                    strings.TrimSpace(r.FormValue("note")),
		LoginIP:                 clientIP(r),
		CreatedAt:               now,
		ScheduledAt:             scheduledAtPtr,
		StartedAt:               startedAt,
		UploadFile:              uploadPath,
		ServiceName:             project.ServiceName,
		TargetDir:               project.TargetDir,
		ServiceInstallMode:      project.ServiceInstallMode,
		ServiceExePath:          project.ServiceExePath,
		ServiceArgs:             append([]string{}, project.ServiceArgs...),
		ServiceDisplayName:      project.ServiceDisplayName,
		ServiceDescription:      project.ServiceDescription,
		ServiceStartType:        project.ServiceStartType,
		ClearTargetBeforeDeploy: clearTargetBeforeDeploy,
	}
	if initialDeploy {
		dep.ReplaceIgnore = nil
	}
	if dep.Note == "" {
		dep.Note = "(未填写更新说明)"
	}

	if err := a.store.Add(dep); err != nil {
		_ = os.Remove(uploadPath)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": fmt.Sprintf("记录部署任务失败: %v", err)})
		return
	}

	if runNow {
		go a.runDeployment(id, project.ID)
		locked = false
	} else {
		a.scheduleDeploymentTask(id, project.ID, scheduledAt)
	}
	respStatus := "queued"
	respMessage := ""
	if hasSchedule {
		respStatus = "scheduled"
		respMessage = fmt.Sprintf("任务已加入等待队列，计划执行时间: %s", scheduledAt.Format("2006-01-02 15:04:05"))
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"id":                   id,
		"status":               respStatus,
		"version":              targetVersion,
		"project_id":           project.ID,
		"project_name":         project.Name,
		"service_install_mode": project.ServiceInstallMode,
		"scheduled_at":         scheduledAtPtr,
		"message":              respMessage,
	})
}

func (a *App) handlePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg := a.currentConfig()
	maxBytes := maxProjectUploadBytes(cfg)
	if maxBytes <= 0 {
		maxBytes = 1024 * 1024 * 1024
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("预演数据解析失败: %v", err)})
		return
	}

	projectID := strings.TrimSpace(r.FormValue("project_id"))
	if projectID == "" {
		projectID = cfg.DefaultProjectID
	}
	project, found := findProjectByID(cfg.Projects, projectID)
	if !found {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("未找到程序: %s", projectID)})
		return
	}
	deployEntry := normalizeDeployEntry(r.FormValue("deploy_entry"))

	file, header, err := r.FormFile("package")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少上传文件字段 package"})
		return
	}
	defer file.Close()
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".zip") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "预演仅支持 .zip 文件"})
		return
	}
	projectMaxBytes := project.MaxUploadMB * 1024 * 1024
	if header.Size > 0 && projectMaxBytes > 0 && header.Size > projectMaxBytes {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": fmt.Sprintf("文件超过程序 %s 的上传限制: %d MB", project.Name, project.MaxUploadMB),
		})
		return
	}

	replaceMode := normalizeReplaceMode(r.FormValue("replace_mode"))
	if strings.TrimSpace(r.FormValue("replace_mode")) == "" {
		replaceMode = normalizeReplaceMode(project.DefaultReplaceMode)
	}
	removeMissing := replaceMode == ReplaceModeFull
	targetExists, targetEmpty, targetCheckErr := inspectTargetDirState(project.TargetDir)
	if targetCheckErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": fmt.Sprintf("检查目标目录失败: %v", targetCheckErr)})
		return
	}
	targetHasExistingFiles := targetExists && !targetEmpty
	initialDeploy := isPreviewInitialDeploy(targetExists, targetEmpty, deployEntry)
	initialDeployAllowed := !initialDeploy || project.AllowInitialDeploy
	requiresInitialClearConfirm := deployEntry == DeployEntryInitial && targetHasExistingFiles
	requiresInitialPage := initialDeploy && deployEntry != DeployEntryInitial
	requiresStandardPage := false

	previewID := newID("preview")
	workDir := filepath.Join(cfg.WorkDir, previewID)
	uploadPath := filepath.Join(workDir, "package.zip")
	_ = os.RemoveAll(workDir)
	defer os.RemoveAll(workDir)
	if err := os.MkdirAll(workDir, 0755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": fmt.Sprintf("创建预演目录失败: %v", err)})
		return
	}
	if err := saveMultipartFile(file, uploadPath); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": fmt.Sprintf("保存预演上传文件失败: %v", err)})
		return
	}

	replaceRules := resolveReplaceIgnoreRulesForTarget(project.TargetDir, project.ReplaceIgnore, project.BackupIgnore)
	if initialDeploy {
		replaceRules = nil
	}
	replaceIgnore := newIgnoreMatcher(append(append([]string{}, replaceRules...), ".replaceignore"))
	changed, ignoredPaths, err := previewZipChanges(uploadPath, project.TargetDir, replaceIgnore, removeMissing)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": fmt.Sprintf("预演失败: %v", err)})
		return
	}

	added := 0
	updated := 0
	deleted := 0
	for _, c := range changed {
		switch c.Action {
		case "added":
			added++
		case "updated":
			updated++
		case "deleted":
			deleted++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                             true,
		"type":                           "preview",
		"project_id":                     project.ID,
		"project_name":                   project.Name,
		"replace_mode":                   replaceMode,
		"deploy_entry":                   deployEntry,
		"initial_deploy":                 initialDeploy,
		"initial_deploy_allowed":         initialDeployAllowed,
		"requires_initial_clear_confirm": requiresInitialClearConfirm,
		"requires_initial_page":          requiresInitialPage,
		"requires_standard_page":         requiresStandardPage,
		"allow_initial_deploy":           project.AllowInitialDeploy,
		"backup_skipped":                 initialDeploy,
		"service_install_mode":           project.ServiceInstallMode,
		"service_name":                   project.ServiceName,
		"service_exe_path":               project.ServiceExePath,
		"changed":                        changed,
		"replace_ignore":                 replaceRules,
		"ignored_paths":                  ignoredPaths,
		"summary": map[string]any{
			"total":         len(changed),
			"added":         added,
			"updated":       updated,
			"deleted":       deleted,
			"ignored_paths": len(ignoredPaths),
		},
	})
}

func (a *App) handleSelfUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cfg := a.currentConfig()
	maxMB := cfg.MaxUploadMB
	if maxMB <= 0 {
		maxMB = 1024
	}
	maxBytes := maxMB * 1024 * 1024
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("上传数据解析失败: %v", err)})
		return
	}

	file, header, err := r.FormFile("package")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少上传文件字段 package"})
		return
	}
	defer file.Close()
	if !strings.HasSuffix(strings.ToLower(strings.TrimSpace(header.Filename)), ".exe") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "自更新仅支持上传 .exe 文件"})
		return
	}
	if header.Size > 0 && header.Size > maxBytes {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("文件超过上传限制: %d MB", maxMB)})
		return
	}

	targetVersion := normalizeVersion(r.FormValue("target_version"))
	if targetVersion != "" && !isValidVersion(targetVersion) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("版本号格式错误: %s，正确格式示例: 0.0.2 / 0.1.1 / 1.0.1", targetVersion)})
		return
	}

	id := newID("self")
	uploadPath := filepath.Join(cfg.UploadDir, id+".exe")
	if err := saveMultipartFile(file, uploadPath); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": fmt.Sprintf("保存上传文件失败: %v", err)})
		return
	}
	if info, statErr := os.Stat(uploadPath); statErr == nil && info.Size() > maxBytes {
		_ = os.Remove(uploadPath)
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("文件超过上传限制: %d MB", maxMB)})
		return
	}

	if ok, reason := a.tryAcquireSelfTask(); !ok {
		_ = os.Remove(uploadPath)
		writeJSON(w, http.StatusConflict, map[string]any{"error": reason})
		return
	}
	locked := true
	defer func() {
		if locked {
			a.releaseSelfTask()
		}
	}()

	now := time.Now()
	dep := Deployment{
		ID:          id,
		Type:        "self_update",
		Version:     targetVersion,
		ProjectID:   "__self__",
		ProjectName: "SimpleRemoteUpdate",
		ReplaceMode: "self",
		Status:      "queued",
		Note:        strings.TrimSpace(r.FormValue("note")),
		LoginIP:     clientIP(r),
		CreatedAt:   now,
		StartedAt:   now,
		UploadFile:  uploadPath,
	}
	if dep.Note == "" {
		dep.Note = "(未填写自更新说明)"
	}

	if err := a.store.Add(dep); err != nil {
		_ = os.Remove(uploadPath)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": fmt.Sprintf("记录自更新任务失败: %v", err)})
		return
	}

	go a.runSelfUpdate(id)
	locked = false
	writeJSON(w, http.StatusAccepted, map[string]any{
		"id":           id,
		"status":       "queued",
		"version":      targetVersion,
		"project_id":   "__self__",
		"project_name": "SimpleRemoteUpdate",
		"message":      "自更新任务已启动，程序将自动重启",
	})
}

func (a *App) handleDeploymentAPIs(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/deployments/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]

	if len(parts) == 1 && r.Method == http.MethodGet {
		dep, ok := a.store.Get(id)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "deployment not found"})
			return
		}
		writeJSON(w, http.StatusOK, dep)
		return
	}

	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}

	switch parts[1] {
	case "events":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		a.handleDeploymentEvents(w, r, id)
	case "note":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		a.handleUpdateNote(w, r, id)
	case "rollback":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		a.handleRollback(w, r, id)
	case "cancel":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		a.handleCancelDeployment(w, r, id)
	default:
		http.NotFound(w, r)
	}
}

func (a *App) handleDeploymentEvents(w http.ResponseWriter, r *http.Request, id string) {
	if _, ok := a.store.Get(id); !ok {
		http.Error(w, "deployment not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", http.StatusInternalServerError)
		return
	}
	subID, ch, unsubscribe := a.events.Subscribe(id)
	defer unsubscribe()

	a.logger.Info("sse subscriber connected", "deployment_id", id, "sub_id", subID)
	defer a.logger.Info("sse subscriber disconnected", "deployment_id", id, "sub_id", subID)

	_, _ = io.WriteString(w, ": connected\n\n")
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case evt := <-ch:
			writeSSE(w, evt)
			flusher.Flush()
		case <-heartbeat.C:
			_, _ = io.WriteString(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func (a *App) handleUpdateNote(w http.ResponseWriter, r *http.Request, id string) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	note := strings.TrimSpace(r.FormValue("note"))
	if note == "" {
		note = "(未填写更新说明)"
	}
	if err := a.store.UpdateField(id, func(dep *Deployment) { dep.Note = note }); err != nil {
		http.Error(w, "update failed", http.StatusInternalServerError)
		return
	}
	a.handleDeploymentsPartial(w, r)
}

func (a *App) handleRollback(w http.ResponseWriter, r *http.Request, sourceID string) {
	source, ok := a.store.Get(sourceID)
	if !ok {
		http.Error(w, "deployment not found", http.StatusNotFound)
		return
	}
	if source.BackupFile == "" {
		if source.InitialDeploy || source.BackupSkipped {
			http.Error(w, "该部署属于首次部署且未生成备份，当前版本不支持自动回滚到部署前空目录", http.StatusBadRequest)
			return
		}
		http.Error(w, "该部署记录没有备份文件，无法回滚", http.StatusBadRequest)
		return
	}

	projectID := strings.TrimSpace(source.ProjectID)
	if projectID == "" {
		projectID = a.currentConfig().DefaultProjectID
	}
	if ok, reason := a.tryAcquireProjectTask(projectID); !ok {
		http.Error(w, reason, http.StatusConflict)
		return
	}
	locked := true
	defer func() {
		if locked {
			a.releaseProjectTask(projectID)
		}
	}()

	now := time.Now()
	id := newID("rb")
	rollback := Deployment{
		ID:                 id,
		Type:               "rollback",
		RollbackOf:         sourceID,
		Version:            source.Version,
		ProjectID:          projectID,
		ProjectName:        source.ProjectName,
		ReplaceMode:        source.ReplaceMode,
		BackupIgnore:       append([]string{}, source.BackupIgnore...),
		ReplaceIgnore:      append([]string{}, source.ReplaceIgnore...),
		Status:             "queued",
		Note:               fmt.Sprintf("回滚到 %s", sourceID),
		LoginIP:            clientIP(r),
		CreatedAt:          now,
		StartedAt:          now,
		BackupFile:         source.BackupFile,
		ServiceName:        source.ServiceName,
		TargetDir:          source.TargetDir,
		InitialDeploy:      source.InitialDeploy,
		BackupSkipped:      source.BackupSkipped,
		ServiceInstallMode: source.ServiceInstallMode,
		ServiceExePath:     source.ServiceExePath,
		ServiceArgs:        append([]string{}, source.ServiceArgs...),
		ServiceDisplayName: source.ServiceDisplayName,
		ServiceDescription: source.ServiceDescription,
		ServiceStartType:   source.ServiceStartType,
	}
	if err := a.store.Add(rollback); err != nil {
		http.Error(w, "回滚任务创建失败", http.StatusInternalServerError)
		return
	}
	go a.runRollback(id, sourceID, projectID)
	locked = false
	a.handleDeploymentsPartial(w, r)
}

func (a *App) handleCancelDeployment(w http.ResponseWriter, r *http.Request, id string) {
	dep, ok := a.store.Get(id)
	if !ok {
		http.Error(w, "deployment not found", http.StatusNotFound)
		return
	}
	status := strings.ToLower(strings.TrimSpace(dep.Status))
	if dep.Type != "deploy" || dep.ScheduledAt == nil || (status != "scheduled" && status != "queued") {
		http.Error(w, "该任务当前不可取消", http.StatusBadRequest)
		return
	}

	a.cancelScheduledDeploymentTask(id)
	canceled := false
	uploadFile := ""
	now := time.Now()
	if err := a.store.UpdateField(id, func(d *Deployment) {
		s := strings.ToLower(strings.TrimSpace(d.Status))
		if s != "scheduled" && s != "queued" {
			return
		}
		canceled = true
		d.Status = "canceled"
		d.FinishedAt = &now
		d.DurationMs = now.Sub(d.CreatedAt).Milliseconds()
		d.Error = "任务已取消"
		uploadFile = strings.TrimSpace(d.UploadFile)
	}); err != nil {
		http.Error(w, "取消任务失败", http.StatusInternalServerError)
		return
	}
	if !canceled {
		http.Error(w, "任务已开始执行，无法取消", http.StatusConflict)
		return
	}
	if uploadFile != "" {
		_ = os.Remove(uploadFile)
	}
	a.publish(id, "warn", "计划任务已取消")
	a.notifyDeploymentIfNeeded(id)
	a.handleDeploymentsPartial(w, r)
}

func (a *App) handleConfigAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		cfg := a.currentConfig()
		writeJSON(w, http.StatusOK, configSnapshot(cfg))
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := parseRequestForm(r); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求参数解析失败"})
		return
	}

	scope := strings.ToLower(strings.TrimSpace(r.FormValue("scope")))
	switch scope {
	case "", "system":
		a.handleSaveSystemConfig(w, r)
	case "project":
		a.handleSaveProjectConfig(w, r)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("未知配置保存范围: %s", scope)})
	}
}

func (a *App) handleNotifyTestAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := parseRequestForm(r); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求参数解析失败"})
		return
	}

	cfg := a.currentConfig()
	savedEmail := strings.TrimSpace(cfg.NotifyEmail)
	email := strings.TrimSpace(r.FormValue("notify_email"))
	if email == "" {
		email = savedEmail
	}

	authCode := strings.TrimSpace(r.FormValue("notify_email_auth_code"))
	if authCode == "" && strings.EqualFold(email, savedEmail) {
		authCode = strings.TrimSpace(cfg.NotifyEmailAuthCode)
	}

	if email == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请先填写 notify_email"})
		return
	}
	if authCode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请先填写 notify_email_auth_code"})
		return
	}
	if _, err := mail.ParseAddress(email); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("notify_email 格式错误: %v", err)})
		return
	}

	nowText := time.Now().Format("2006-01-02 15:04:05")
	subject := fmt.Sprintf("[SimpleRemoteUpdate] 邮件通知测试 %s", nowText)
	body := strings.Join([]string{
		"这是一封测试邮件，用于验证更新通知邮箱配置是否可用。",
		fmt.Sprintf("发送时间: %s", nowText),
		"",
		"若你收到此邮件，说明 notify_email 与 notify_email_auth_code 配置有效。",
	}, "\n")
	if err := sendNotifyEmail(email, authCode, subject, body); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": fmt.Sprintf("发送测试邮件失败: %v", err)})
		return
	}
	a.logger.Info("测试邮件已发送", "to", email)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"message": fmt.Sprintf("测试邮件已发送到 %s，请检查收件箱/垃圾箱", email),
	})
}

func (a *App) handleSaveSystemConfig(w http.ResponseWriter, r *http.Request) {
	oldCfg := a.currentConfig()
	newCfg := oldCfg

	newCfg.ListenAddr = strings.TrimSpace(r.FormValue("listen_addr"))
	newCfg.SessionCookie = strings.TrimSpace(r.FormValue("session_cookie"))
	newCfg.UploadDir = strings.TrimSpace(r.FormValue("upload_dir"))
	newCfg.WorkDir = strings.TrimSpace(r.FormValue("work_dir"))
	newCfg.BackupDir = strings.TrimSpace(r.FormValue("backup_dir"))
	newCfg.DeploymentsFile = strings.TrimSpace(r.FormValue("deployments_file"))
	newCfg.LogFile = strings.TrimSpace(r.FormValue("log_file"))
	newCfg.NotifyEmail = strings.TrimSpace(r.FormValue("notify_email"))
	if _, ok := r.Form["nssm_exe_path"]; ok {
		newCfg.NSSMExePath = strings.TrimSpace(r.FormValue("nssm_exe_path"))
	}
	if _, ok := r.Form["notify_email_auth_code"]; ok {
		if authCode := strings.TrimSpace(r.FormValue("notify_email_auth_code")); authCode != "" {
			newCfg.NotifyEmailAuthCode = authCode
		}
	}
	if _, ok := r.Form["self_update_service_name"]; ok {
		newCfg.SelfUpdateServiceName = strings.TrimSpace(r.FormValue("self_update_service_name"))
	}

	defaultProjectID := strings.TrimSpace(r.FormValue("default_project_id"))
	if defaultProjectID != "" {
		newCfg.DefaultProjectID = defaultProjectID
	}

	newKey := strings.TrimSpace(r.FormValue("new_auth_key"))
	if newKey != "" {
		newCfg.AuthKeySHA256 = sha256Hex(newKey)
	}

	restartFields, err := a.applyConfigChanges(w, r, oldCfg, newCfg)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	finalCfg := a.currentConfig()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"message":        "系统配置保存成功，已自动刷新运行配置",
		"restart_needed": len(restartFields) > 0,
		"restart_fields": restartFields,
		"config":         configSnapshot(finalCfg),
	})
}

func (a *App) handleSaveProjectConfig(w http.ResponseWriter, r *http.Request) {
	oldCfg := a.currentConfig()
	newCfg := oldCfg

	projectID := strings.TrimSpace(r.FormValue("project_id"))
	if projectID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "project_id 不能为空"})
		return
	}

	idx := -1
	for i := range newCfg.Projects {
		if newCfg.Projects[i].ID == projectID {
			idx = i
			break
		}
	}
	if idx < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("程序不存在: %s", projectID)})
		return
	}

	project := newCfg.Projects[idx]
	project.Name = firstNonEmpty(strings.TrimSpace(r.FormValue("name")), projectID)
	project.ServiceName = strings.TrimSpace(r.FormValue("service_name"))
	project.TargetDir = strings.TrimSpace(r.FormValue("target_dir"))
	project.CurrentVersion = normalizeVersion(r.FormValue("current_version"))
	project.AllowInitialDeploy = parseBoolFormValue(r.FormValue("allow_initial_deploy"))
	project.ServiceInstallMode = normalizeServiceInstallMode(r.FormValue("service_install_mode"))
	project.ServiceExePath = strings.TrimSpace(r.FormValue("service_exe_path"))
	project.ServiceArgs = splitLinesTrim(r.FormValue("service_args_text"))
	project.ServiceDisplayName = strings.TrimSpace(r.FormValue("service_display_name"))
	project.ServiceDescription = strings.TrimSpace(r.FormValue("service_description"))
	project.ServiceStartType = normalizeServiceStartType(r.FormValue("service_start_type"))
	if project.CurrentVersion == "" {
		project.CurrentVersion = "0.0.1"
	}
	if !isValidVersion(project.CurrentVersion) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("current_version 格式错误: %s，必须是 x.y.z", project.CurrentVersion)})
		return
	}
	rawReplaceMode := strings.TrimSpace(r.FormValue("default_replace_mode"))
	if rawReplaceMode != "" {
		project.DefaultReplaceMode = normalizeReplaceMode(rawReplaceMode)
	} else {
		project.DefaultReplaceMode = normalizeReplaceMode(project.DefaultReplaceMode)
	}

	maxUploadMB, err := parsePositiveInt64(r.FormValue("max_upload_mb"), "max_upload_mb")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	project.MaxUploadMB = maxUploadMB
	project.BackupIgnore = splitLinesTrim(r.FormValue("backup_ignore_text"))
	project.ReplaceIgnore = splitLinesTrim(r.FormValue("replace_ignore_text"))

	newCfg.Projects[idx] = project
	if parseBoolFormValue(r.FormValue("set_default_project")) {
		newCfg.DefaultProjectID = projectID
	}

	restartFields, err := a.applyConfigChanges(w, r, oldCfg, newCfg)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	finalCfg := a.currentConfig()
	saveMsg := fmt.Sprintf("程序 %s 配置保存成功，已自动刷新运行配置", project.Name)
	if project.ServiceName == "" {
		saveMsg += "；提示：service_name 为空，部署/回滚时将跳过服务启停，仅执行文件替换"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
		"message":           saveMsg,
		"restart_needed":    len(restartFields) > 0,
		"restart_fields":    restartFields,
		"active_project_id": projectID,
		"config":            configSnapshot(finalCfg),
	})
}

func (a *App) handleProjectsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := parseRequestForm(r); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求参数解析失败"})
		return
	}

	oldCfg := a.currentConfig()
	newCfg := oldCfg

	projectID := strings.TrimSpace(r.FormValue("id"))
	if projectID == "" {
		projectID = nextProjectID(newCfg.Projects)
	}
	if _, exists := findProjectByID(newCfg.Projects, projectID); exists {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("程序ID重复: %s", projectID)})
		return
	}

	maxUploadRaw := strings.TrimSpace(r.FormValue("max_upload_mb"))
	maxUploadMB := newCfg.MaxUploadMB
	if maxUploadMB <= 0 {
		maxUploadMB = 1024
	}
	if maxUploadRaw != "" {
		v, err := parsePositiveInt64(maxUploadRaw, "max_upload_mb")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		maxUploadMB = v
	}

	project := ManagedProject{
		ID:                 projectID,
		Name:               firstNonEmpty(strings.TrimSpace(r.FormValue("name")), projectID),
		ServiceName:        strings.TrimSpace(r.FormValue("service_name")),
		TargetDir:          strings.TrimSpace(r.FormValue("target_dir")),
		CurrentVersion:     normalizeVersion(r.FormValue("current_version")),
		DefaultReplaceMode: normalizeReplaceMode(r.FormValue("default_replace_mode")),
		AllowInitialDeploy: parseBoolFormValue(r.FormValue("allow_initial_deploy")),
		ServiceInstallMode: normalizeServiceInstallMode(r.FormValue("service_install_mode")),
		ServiceExePath:     strings.TrimSpace(r.FormValue("service_exe_path")),
		ServiceArgs:        splitLinesTrim(r.FormValue("service_args_text")),
		ServiceDisplayName: strings.TrimSpace(r.FormValue("service_display_name")),
		ServiceDescription: strings.TrimSpace(r.FormValue("service_description")),
		ServiceStartType:   normalizeServiceStartType(r.FormValue("service_start_type")),
		MaxUploadMB:        maxUploadMB,
		BackupIgnore:       splitLinesTrim(r.FormValue("backup_ignore_text")),
		ReplaceIgnore:      splitLinesTrim(r.FormValue("replace_ignore_text")),
	}
	if project.CurrentVersion == "" {
		project.CurrentVersion = "0.0.1"
	}
	if !isValidVersion(project.CurrentVersion) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("current_version 格式错误: %s，必须是 x.y.z", project.CurrentVersion)})
		return
	}
	if project.TargetDir == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "target_dir 不能为空"})
		return
	}
	if len(project.BackupIgnore) == 0 {
		dp := getDefaultProject(newCfg)
		project.BackupIgnore = append([]string{}, dp.BackupIgnore...)
	}
	if len(project.ReplaceIgnore) == 0 {
		dp := getDefaultProject(newCfg)
		project.ReplaceIgnore = append([]string{}, dp.ReplaceIgnore...)
	}
	if strings.TrimSpace(r.FormValue("default_replace_mode")) == "" {
		dp := getDefaultProject(newCfg)
		project.DefaultReplaceMode = normalizeReplaceMode(dp.DefaultReplaceMode)
	}

	newCfg.Projects = append(newCfg.Projects, project)
	if parseBoolFormValue(r.FormValue("set_default_project")) {
		newCfg.DefaultProjectID = project.ID
	}

	restartFields, err := a.applyConfigChanges(w, r, oldCfg, newCfg)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	finalCfg := a.currentConfig()
	createMsg := fmt.Sprintf("程序 %s 已创建", project.Name)
	if project.ServiceName == "" {
		createMsg += "；提示：service_name 为空，部署/回滚时将跳过服务启停，仅执行文件替换"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
		"message":           createMsg,
		"restart_needed":    len(restartFields) > 0,
		"restart_fields":    restartFields,
		"active_project_id": project.ID,
		"config":            configSnapshot(finalCfg),
	})
}

func (a *App) handleProjectItemAPI(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/projects/"))
	if projectID == "" || strings.Contains(projectID, "/") {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	oldCfg := a.currentConfig()
	if len(oldCfg.Projects) <= 1 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "至少保留一个程序，无法删除最后一个程序"})
		return
	}

	newCfg := oldCfg
	filtered := make([]ManagedProject, 0, len(newCfg.Projects)-1)
	removed := false
	for _, p := range newCfg.Projects {
		if p.ID == projectID {
			removed = true
			continue
		}
		filtered = append(filtered, p)
	}
	if !removed {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": fmt.Sprintf("程序不存在: %s", projectID)})
		return
	}
	newCfg.Projects = filtered
	if newCfg.DefaultProjectID == projectID {
		newCfg.DefaultProjectID = filtered[0].ID
	}

	restartFields, err := a.applyConfigChanges(w, r, oldCfg, newCfg)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	finalCfg := a.currentConfig()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
		"message":           fmt.Sprintf("程序 %s 已删除", projectID),
		"restart_needed":    len(restartFields) > 0,
		"restart_fields":    restartFields,
		"active_project_id": finalCfg.DefaultProjectID,
		"config":            configSnapshot(finalCfg),
	})
}

func parseRequestForm(r *http.Request) error {
	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if strings.HasPrefix(contentType, "multipart/form-data") {
		return r.ParseMultipartForm(32 << 20)
	}
	return r.ParseForm()
}

func configSnapshot(cfg Config) map[string]any {
	projectsJSON, _ := json.MarshalIndent(cfg.Projects, "", "  ")
	dp := getDefaultProject(cfg)
	return map[string]any{
		"listen_addr":                cfg.ListenAddr,
		"session_cookie":             cfg.SessionCookie,
		"default_project_id":         cfg.DefaultProjectID,
		"projects_json":              string(projectsJSON),
		"projects":                   cfg.Projects,
		"current_version":            dp.CurrentVersion,
		"upload_dir":                 cfg.UploadDir,
		"work_dir":                   cfg.WorkDir,
		"backup_dir":                 cfg.BackupDir,
		"deployments_file":           cfg.DeploymentsFile,
		"log_file":                   cfg.LogFile,
		"nssm_exe_path":              cfg.NSSMExePath,
		"notify_email":               cfg.NotifyEmail,
		"notify_email_auth_code_set": strings.TrimSpace(cfg.NotifyEmailAuthCode) != "",
		"self_update_service_name":   cfg.SelfUpdateServiceName,
		"service_name":               dp.ServiceName,
		"target_dir":                 dp.TargetDir,
		"replace_mode":               dp.DefaultReplaceMode,
		"default_replace_mode":       dp.DefaultReplaceMode,
		"allow_initial_deploy":       dp.AllowInitialDeploy,
		"service_install_mode":       dp.ServiceInstallMode,
		"service_exe_path":           dp.ServiceExePath,
		"service_args_text":          strings.Join(dp.ServiceArgs, "\n"),
		"service_display_name":       dp.ServiceDisplayName,
		"service_description":        dp.ServiceDescription,
		"service_start_type":         dp.ServiceStartType,
		"backup_ignore_text":         strings.Join(dp.BackupIgnore, "\n"),
		"replace_ignore_text":        strings.Join(dp.ReplaceIgnore, "\n"),
		"max_upload_mb":              dp.MaxUploadMB,
	}
}

func (a *App) applyConfigChanges(w http.ResponseWriter, r *http.Request, oldCfg, newCfg Config) ([]string, error) {
	normalizeProjects(&newCfg)
	if newCfg.SessionCookie == "" {
		newCfg.SessionCookie = "updater_session"
	}
	if err := validateRuntimeConfig(newCfg); err != nil {
		return nil, err
	}
	if err := ensureDirectories(newCfg); err != nil {
		return nil, fmt.Errorf("目录校验失败: %w", err)
	}

	appliedLog := false
	appliedStore := false
	if oldCfg.LogFile != newCfg.LogFile {
		if err := a.logWriter.SwitchFile(newCfg.LogFile); err != nil {
			return nil, fmt.Errorf("切换日志文件失败: %w", err)
		}
		appliedLog = true
	}
	if oldCfg.DeploymentsFile != newCfg.DeploymentsFile {
		if err := a.store.SwitchFile(newCfg.DeploymentsFile); err != nil {
			if appliedLog {
				_ = a.logWriter.SwitchFile(oldCfg.LogFile)
			}
			return nil, fmt.Errorf("切换部署记录文件失败: %w", err)
		}
		appliedStore = true
	}

	if err := saveConfig(a.cfgPath, newCfg); err != nil {
		if appliedStore {
			_ = a.store.SwitchFile(oldCfg.DeploymentsFile)
		}
		if appliedLog {
			_ = a.logWriter.SwitchFile(oldCfg.LogFile)
		}
		return nil, fmt.Errorf("保存配置失败: %w", err)
	}

	a.replaceConfig(newCfg)
	if oldCfg.SessionCookie != newCfg.SessionCookie {
		if oldCookie, err := r.Cookie(oldCfg.SessionCookie); err == nil && oldCookie.Value != "" {
			http.SetCookie(w, &http.Cookie{
				Name:     oldCfg.SessionCookie,
				Value:    "",
				Path:     "/",
				HttpOnly: true,
				MaxAge:   -1,
			})
			http.SetCookie(w, &http.Cookie{
				Name:     newCfg.SessionCookie,
				Value:    oldCookie.Value,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
				Expires:  time.Now().Add(8 * time.Hour),
			})
		}
	}

	restartFields := make([]string, 0, 1)
	if oldCfg.ListenAddr != newCfg.ListenAddr {
		restartFields = append(restartFields, "listen_addr")
	}
	return restartFields, nil
}

func (a *App) notifyDeploymentIfNeeded(depID string) {
	dep, ok := a.store.Get(depID)
	if !ok {
		return
	}
	status := strings.ToLower(strings.TrimSpace(dep.Status))
	if status != "success" && status != "failed" && status != "canceled" && status != "cancelled" {
		return
	}
	if dep.Type != "deploy" && dep.Type != "rollback" {
		return
	}

	cfg := a.currentConfig()
	email := strings.TrimSpace(cfg.NotifyEmail)
	authCode := strings.TrimSpace(cfg.NotifyEmailAuthCode)
	if email == "" || authCode == "" {
		return
	}

	projectName := strings.TrimSpace(dep.ProjectName)
	if projectName == "" {
		projectName = strings.TrimSpace(dep.ProjectID)
	}
	if projectName == "" {
		projectName = "-"
	}
	subject := fmt.Sprintf("[SimpleRemoteUpdate] %s %s %s", dep.Type, status, dep.ID)
	body := fmt.Sprintf(
		"任务ID: %s\n类型: %s\n程序: %s\n版本: %s\n状态: %s\n创建时间: %s\n完成时间: %s\n耗时: %d ms\n说明: %s\n错误: %s\n",
		dep.ID,
		dep.Type,
		projectName,
		firstNonEmpty(dep.Version, "-"),
		dep.Status,
		dep.CreatedAt.Format("2006-01-02 15:04:05"),
		formatTimePtr(dep.FinishedAt),
		dep.DurationMs,
		firstNonEmpty(dep.Note, "-"),
		firstNonEmpty(dep.Error, "-"),
	)
	if err := sendNotifyEmail(email, authCode, subject, body); err != nil {
		a.logger.Warn("更新结果邮件发送失败", "deployment_id", depID, "error", err.Error())
		return
	}
	a.logger.Info("更新结果邮件已发送", "deployment_id", depID, "to", email)
}

func sendNotifyEmail(email, authCode, subject, body string) error {
	email = strings.TrimSpace(email)
	authCode = strings.TrimSpace(authCode)
	if email == "" || authCode == "" {
		return errors.New("notify email config is empty")
	}
	host, port, err := resolveSMTPServer(email)
	if err != nil {
		return err
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	auth := smtp.PlainAuth("", email, authCode, host)
	fromDisplay := fmt.Sprintf("SimpleRemoteUpdate <%s>", email)
	msg := strings.Join([]string{
		fmt.Sprintf("From: %s", fromDisplay),
		fmt.Sprintf("To: %s", email),
		fmt.Sprintf("Subject: %s", subject),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"",
		body,
	}, "\r\n")
	return smtp.SendMail(addr, auth, email, []string{email}, []byte(msg))
}

func resolveSMTPServer(email string) (string, int, error) {
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return "", 0, errors.New("notify_email 格式错误")
	}
	domain := strings.ToLower(strings.TrimSpace(email[at+1:]))
	switch domain {
	case "qq.com":
		return "smtp.qq.com", 587, nil
	case "163.com":
		return "smtp.163.com", 587, nil
	case "126.com":
		return "smtp.126.com", 587, nil
	case "gmail.com":
		return "smtp.gmail.com", 587, nil
	default:
		return "smtp." + domain, 587, nil
	}
}

func formatTimePtr(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	return t.Format("2006-01-02 15:04:05")
}

func parsePositiveInt64(raw, field string) (int64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, fmt.Errorf("%s 不能为空", field)
	}
	value, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s 必须为正整数，当前值: %q", field, raw)
	}
	return value, nil
}

func parseBoolFormValue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func nextProjectID(projects []ManagedProject) string {
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("project-%d", i)
		if _, exists := findProjectByID(projects, candidate); !exists {
			return candidate
		}
	}
}

const scheduledTaskRetryInterval = 5 * time.Second

func parseScheduledAtFormValue(raw string) (time.Time, bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, false, nil
	}
	layouts := []string{
		"2006-01-02T15:04",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04",
		"2006-01-02 15:04:05",
	}
	var (
		parsed time.Time
		err    error
	)
	for _, layout := range layouts {
		parsed, err = time.ParseInLocation(layout, trimmed, time.Local)
		if err == nil {
			break
		}
	}
	if err != nil {
		return time.Time{}, false, errors.New("计划执行时间格式错误，示例: 2026-03-05T10:30")
	}
	if !parsed.After(time.Now().Add(5 * time.Second)) {
		return time.Time{}, false, errors.New("计划执行时间必须晚于当前时间至少 5 秒")
	}
	return parsed, true, nil
}

func (a *App) scheduleDeploymentTask(depID, projectID string, runAt time.Time) {
	ctx, cancel := context.WithCancel(context.Background())
	a.schedMu.Lock()
	if old := a.schedCancel[depID]; old != nil {
		old()
	}
	a.schedCancel[depID] = cancel
	a.schedMu.Unlock()

	go a.runScheduledDeployment(ctx, depID, projectID, runAt)
}

func (a *App) cancelScheduledDeploymentTask(depID string) {
	a.schedMu.Lock()
	cancel := a.schedCancel[depID]
	delete(a.schedCancel, depID)
	a.schedMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (a *App) clearScheduledDeploymentTask(depID string) {
	a.schedMu.Lock()
	delete(a.schedCancel, depID)
	a.schedMu.Unlock()
}

func (a *App) runScheduledDeployment(ctx context.Context, depID, projectID string, runAt time.Time) {
	defer a.clearScheduledDeploymentTask(depID)

	delay := time.Until(runAt)
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}

	a.publish(depID, "info", "计划时间到达，进入执行队列")
	_ = a.store.UpdateField(depID, func(dep *Deployment) {
		if strings.EqualFold(dep.Status, "scheduled") {
			dep.Status = "queued"
			dep.Error = ""
		}
	})

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		dep, ok := a.store.Get(depID)
		if !ok {
			return
		}
		status := strings.ToLower(strings.TrimSpace(dep.Status))
		if status == "canceled" || status == "cancelled" || status == "success" || status == "failed" {
			return
		}
		if status != "queued" && status != "scheduled" {
			return
		}

		actualProjectID := strings.TrimSpace(projectID)
		if actualProjectID == "" {
			actualProjectID = strings.TrimSpace(dep.ProjectID)
		}
		if ok, _ := a.tryAcquireProjectTask(actualProjectID); ok {
			latest, exists := a.store.Get(depID)
			if !exists || strings.EqualFold(latest.Status, "canceled") || strings.EqualFold(latest.Status, "cancelled") {
				a.releaseProjectTask(actualProjectID)
				return
			}
			a.publish(depID, "info", "计划任务开始执行")
			go a.runDeployment(depID, actualProjectID)
			return
		}
		time.Sleep(scheduledTaskRetryInterval)
	}
}

func (a *App) resumeScheduledDeployments() {
	for _, dep := range a.store.List() {
		if dep.Type != "deploy" || dep.ScheduledAt == nil {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(dep.Status))
		if status != "scheduled" && status != "queued" {
			continue
		}
		runAt := *dep.ScheduledAt
		if status == "queued" || runAt.Before(time.Now()) {
			runAt = time.Now().Add(1 * time.Second)
		}
		a.scheduleDeploymentTask(dep.ID, dep.ProjectID, runAt)
	}
}

func (a *App) tryAcquireProjectTask(projectID string) (bool, string) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		projectID = "__default__"
	}
	a.taskMu.Lock()
	defer a.taskMu.Unlock()
	if a.selfTask {
		return false, "当前正在执行 SimpleRemoteUpdate 自更新，请稍后再试"
	}
	if _, exists := a.projectTask[projectID]; exists {
		return false, fmt.Sprintf("程序 %s 当前已有任务在执行，请稍后再试", projectID)
	}
	a.projectTask[projectID] = struct{}{}
	return true, ""
}

func (a *App) releaseProjectTask(projectID string) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		projectID = "__default__"
	}
	a.taskMu.Lock()
	defer a.taskMu.Unlock()
	delete(a.projectTask, projectID)
}

func (a *App) tryAcquireSelfTask() (bool, string) {
	a.taskMu.Lock()
	defer a.taskMu.Unlock()
	if a.selfTask {
		return false, "当前已有自更新任务在执行，请稍后再试"
	}
	if len(a.projectTask) > 0 {
		return false, "当前有部署/回滚任务在执行，请稍后再试"
	}
	a.selfTask = true
	return true, ""
}

func (a *App) releaseSelfTask() {
	a.taskMu.Lock()
	defer a.taskMu.Unlock()
	a.selfTask = false
}

func (a *App) authUser(r *http.Request) (string, bool) {
	cfg := a.currentConfig()
	cookie, err := r.Cookie(cfg.SessionCookie)
	if err != nil || cookie.Value == "" {
		return "", false
	}
	return a.sessions.Get(cookie.Value)
}

func (a *App) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.authUser(r); !ok {
			if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/partials/") {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func ensureDirectories(cfg Config) error {
	dirs := []string{
		cfg.UploadDir,
		cfg.WorkDir,
		cfg.BackupDir,
		filepath.Dir(cfg.DeploymentsFile),
		filepath.Dir(cfg.LogFile),
	}
	for _, d := range dirs {
		if d == "" || d == "." {
			continue
		}
		if err := os.MkdirAll(d, 0755); err != nil {
			return err
		}
	}
	return nil
}

func stopService(name string, timeout time.Duration) error {
	return stopServiceImpl(context.Background(), name, timeout)
}

func startService(name string, timeout time.Duration) error {
	return startServiceImpl(context.Background(), name, timeout)
}

func validateRuntimeConfig(cfg Config) error {
	if strings.TrimSpace(cfg.ListenAddr) == "" {
		return errors.New("listen_addr 不能为空")
	}
	if strings.TrimSpace(cfg.SessionCookie) == "" {
		return errors.New("session_cookie 不能为空")
	}
	if len(cfg.Projects) == 0 {
		return errors.New("projects 不能为空，至少需要一个程序")
	}
	ids := map[string]struct{}{}
	for _, p := range cfg.Projects {
		if strings.TrimSpace(p.ID) == "" {
			return errors.New("projects.id 不能为空")
		}
		if _, exists := ids[p.ID]; exists {
			return fmt.Errorf("projects.id 重复: %s", p.ID)
		}
		ids[p.ID] = struct{}{}
		if strings.TrimSpace(p.Name) == "" {
			return fmt.Errorf("projects(%s).name 不能为空", p.ID)
		}
		if strings.TrimSpace(p.TargetDir) == "" {
			return fmt.Errorf("projects(%s).target_dir 不能为空", p.ID)
		}
		if strings.TrimSpace(p.CurrentVersion) == "" {
			return fmt.Errorf("projects(%s).current_version 不能为空", p.ID)
		}
		if !isValidVersion(p.CurrentVersion) {
			return fmt.Errorf("projects(%s).current_version 格式错误，必须是 x.y.z", p.ID)
		}
		if p.MaxUploadMB <= 0 {
			return fmt.Errorf("projects(%s).max_upload_mb 必须大于 0", p.ID)
		}
		if p.ServiceInstallMode != ServiceInstallModeNone {
			if strings.TrimSpace(p.ServiceName) == "" {
				return fmt.Errorf("projects(%s).service_name 不能为空（启用服务安装时必填）", p.ID)
			}
			if p.ServiceInstallMode != ServiceInstallModeWindows && p.ServiceInstallMode != ServiceInstallModeNSSM {
				return fmt.Errorf("projects(%s).service_install_mode 非法: %s", p.ID, p.ServiceInstallMode)
			}
			if strings.TrimSpace(p.ServiceExePath) == "" {
				return fmt.Errorf("projects(%s).service_exe_path 不能为空（启用服务安装时请填写压缩包解压后的 exe 文件名或相对路径）", p.ID)
			}
		}
	}
	if strings.TrimSpace(cfg.DefaultProjectID) == "" {
		return errors.New("default_project_id 不能为空")
	}
	if _, ok := ids[cfg.DefaultProjectID]; !ok {
		return fmt.Errorf("default_project_id 不存在: %s", cfg.DefaultProjectID)
	}
	if strings.TrimSpace(cfg.TargetDir) == "" {
		return errors.New("target_dir 不能为空")
	}
	if strings.TrimSpace(cfg.UploadDir) == "" {
		return errors.New("upload_dir 不能为空")
	}
	if strings.TrimSpace(cfg.WorkDir) == "" {
		return errors.New("work_dir 不能为空")
	}
	if strings.TrimSpace(cfg.BackupDir) == "" {
		return errors.New("backup_dir 不能为空")
	}
	if strings.TrimSpace(cfg.DeploymentsFile) == "" {
		return errors.New("deployments_file 不能为空")
	}
	if strings.TrimSpace(cfg.LogFile) == "" {
		return errors.New("log_file 不能为空")
	}
	if email := strings.TrimSpace(cfg.NotifyEmail); email != "" {
		if _, err := mail.ParseAddress(email); err != nil {
			return fmt.Errorf("notify_email 格式错误: %v", err)
		}
	}
	if strings.TrimSpace(cfg.NotifyEmailAuthCode) != "" && strings.TrimSpace(cfg.NotifyEmail) == "" {
		return errors.New("配置了 notify_email_auth_code 时，notify_email 不能为空")
	}
	if cfg.MaxUploadMB <= 0 {
		return errors.New("max_upload_mb 必须大于 0")
	}
	return nil
}

func (a *App) currentConfig() Config {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	return a.cfg
}

func (a *App) replaceConfig(cfg Config) {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	a.cfg = cfg
}

func (a *App) setProjectCurrentVersion(projectID, version string) error {
	version = normalizeVersion(version)
	if !isValidVersion(version) {
		return fmt.Errorf("版本号格式错误: %s", version)
	}
	cfg := a.currentConfig()
	if strings.TrimSpace(projectID) == "" {
		projectID = cfg.DefaultProjectID
	}
	found := false
	for i := range cfg.Projects {
		if cfg.Projects[i].ID != projectID {
			continue
		}
		found = true
		if cfg.Projects[i].CurrentVersion == version {
			return nil
		}
		cfg.Projects[i].CurrentVersion = version
		break
	}
	if !found {
		return fmt.Errorf("程序不存在: %s", projectID)
	}
	normalizeProjects(&cfg)
	if err := saveConfig(a.cfgPath, cfg); err != nil {
		return err
	}
	a.replaceConfig(cfg)
	return nil
}

func splitLinesTrim(raw string) []string {
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

func maxProjectUploadBytes(cfg Config) int64 {
	var maxMB int64 = 0
	for _, p := range cfg.Projects {
		if p.MaxUploadMB > maxMB {
			maxMB = p.MaxUploadMB
		}
	}
	if maxMB <= 0 {
		maxMB = cfg.MaxUploadMB
	}
	if maxMB <= 0 {
		maxMB = 1024
	}
	return maxMB * 1024 * 1024
}

func buildDeploymentsPageData(all []Deployment, r *http.Request) map[string]any {
	offset, limit := parsePageArgs(r, 0, 20, 200)
	total := len(all)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	items := make([]Deployment, 0)
	if offset < end {
		items = all[offset:end]
	}
	nextOffset := offset + len(items)
	hasMore := nextOffset < total
	return map[string]any{
		"Deployments": items,
		"Offset":      offset,
		"Limit":       limit,
		"Total":       total,
		"NextOffset":  nextOffset,
		"HasMore":     hasMore,
	}
}

func parsePageArgs(r *http.Request, defaultOffset, defaultLimit, maxLimit int) (int, int) {
	offset := defaultOffset
	limit := defaultLimit
	if r == nil {
		return offset, limit
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v >= 0 {
			offset = v
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			limit = v
		}
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	return offset, limit
}

type selfUpdateWorkerOptions struct {
	TargetPath      string
	SourcePath      string
	BackupPath      string
	DeploymentID    string
	DeploymentsFile string
	LogFile         string
	ServiceName     string
	WaitSeconds     int
}

const (
	selfUpdateSwapSettleDelay   = 1500 * time.Millisecond
	selfUpdateReplaceRetryTimes = 5
	selfUpdateReplaceRetryDelay = 1200 * time.Millisecond
	selfUpdateRestartDelay      = 2000 * time.Millisecond
	selfUpdateRestartRetryTimes = 5
	selfUpdateRestartRetryDelay = 2000 * time.Millisecond
	selfUpdateServiceOpTimeout  = 90 * time.Second
)

func tryRunSelfUpdateWorker(args []string) (bool, error) {
	if len(args) == 0 || strings.TrimSpace(args[0]) != "--self-update-worker" {
		return false, nil
	}

	fs := flag.NewFlagSet("self-update-worker", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := selfUpdateWorkerOptions{}
	fs.StringVar(&opts.TargetPath, "target", "", "target executable path")
	fs.StringVar(&opts.SourcePath, "source", "", "staged new executable path")
	fs.StringVar(&opts.BackupPath, "backup", "", "backup executable path")
	fs.StringVar(&opts.DeploymentID, "deployment-id", "", "deployment id")
	fs.StringVar(&opts.DeploymentsFile, "deployments-file", "", "deployments json file")
	fs.StringVar(&opts.LogFile, "log-file", "", "log file")
	fs.StringVar(&opts.ServiceName, "service-name", "", "self updater service name")
	fs.IntVar(&opts.WaitSeconds, "wait-seconds", 120, "wait seconds")
	if err := fs.Parse(args[1:]); err != nil {
		return true, err
	}
	return true, runSelfUpdateWorker(opts)
}

func runSelfUpdateWorker(opts selfUpdateWorkerOptions) error {
	opts.TargetPath = strings.TrimSpace(opts.TargetPath)
	opts.SourcePath = strings.TrimSpace(opts.SourcePath)
	opts.BackupPath = strings.TrimSpace(opts.BackupPath)
	opts.DeploymentID = strings.TrimSpace(opts.DeploymentID)
	opts.DeploymentsFile = strings.TrimSpace(opts.DeploymentsFile)
	opts.LogFile = strings.TrimSpace(opts.LogFile)
	opts.ServiceName = strings.TrimSpace(opts.ServiceName)
	if opts.WaitSeconds <= 0 {
		opts.WaitSeconds = 120
	}

	if opts.TargetPath == "" || opts.SourcePath == "" || opts.BackupPath == "" {
		return errors.New("missing required args: --target --source --backup")
	}

	appendSelfUpdateLog(opts.LogFile, "[self-update] worker started: target=%s source=%s backup=%s", opts.TargetPath, opts.SourcePath, opts.BackupPath)
	if opts.ServiceName != "" {
		appendSelfUpdateLog(opts.LogFile, "[self-update] service-aware mode enabled: service=%s", opts.ServiceName)
		if err := stopService(opts.ServiceName, selfUpdateServiceOpTimeout); err != nil {
			appendSelfUpdateLog(opts.LogFile, "[self-update] stop service failed: %v", err)
			_ = updateSelfUpdateResult(opts, "failed", fmt.Sprintf("停止自更新服务失败(%s): %v", opts.ServiceName, err), 0)
			return err
		}
		appendSelfUpdateLog(opts.LogFile, "[self-update] service stopped: %s", opts.ServiceName)
	}
	newSize := int64(0)
	if info, err := os.Stat(opts.SourcePath); err == nil {
		newSize = info.Size()
	}

	waitErr := waitAndSwapExecutable(opts)
	if waitErr != nil {
		appendSelfUpdateLog(opts.LogFile, "[self-update] failed: %v", waitErr)
		_ = updateSelfUpdateResult(opts, "failed", waitErr.Error(), newSize)
		return waitErr
	}

	appendSelfUpdateLog(opts.LogFile, "[self-update] executable swapped successfully")
	appendSelfUpdateLog(opts.LogFile, "[self-update] wait %.1fs before restart", selfUpdateRestartDelay.Seconds())
	time.Sleep(selfUpdateRestartDelay)
	if opts.ServiceName != "" {
		if err := startSelfUpdateServiceWithRetry(opts); err != nil {
			appendSelfUpdateLog(opts.LogFile, "[self-update] service restart failed after retries: %v", err)
			_ = updateSelfUpdateResult(opts, "failed", fmt.Sprintf("自更新完成但服务重启失败(%s): %v", opts.ServiceName, err), newSize)
			return err
		}
	} else {
		if err := restartSelfUpdatedProcess(opts); err != nil {
			appendSelfUpdateLog(opts.LogFile, "[self-update] restart failed after retries: %v", err)
			_ = updateSelfUpdateResult(opts, "failed", fmt.Sprintf("自更新完成但重启失败: %v", err), newSize)
			return err
		}
	}

	appendSelfUpdateLog(opts.LogFile, "[self-update] restart success")
	_ = updateSelfUpdateResult(opts, "success", "", newSize)
	return nil
}

func waitAndSwapExecutable(opts selfUpdateWorkerOptions) error {
	if err := os.MkdirAll(filepath.Dir(opts.BackupPath), 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(opts.TargetPath), 0755); err != nil {
		return err
	}

	var renameErr error
	renamed := false
	for i := 0; i < opts.WaitSeconds; i++ {
		renameErr = os.Rename(opts.TargetPath, opts.BackupPath)
		if renameErr == nil {
			renamed = true
			break
		}
		time.Sleep(1 * time.Second)
	}
	if !renamed {
		return fmt.Errorf("等待旧进程退出超时，无法替换程序: %w", renameErr)
	}
	appendSelfUpdateLog(opts.LogFile, "[self-update] old process exited, wait %.1fs before swapping executable", selfUpdateSwapSettleDelay.Seconds())
	time.Sleep(selfUpdateSwapSettleDelay)

	var replaceErr error
	for attempt := 1; attempt <= selfUpdateReplaceRetryTimes; attempt++ {
		replaceErr = os.Rename(opts.SourcePath, opts.TargetPath)
		if replaceErr == nil {
			if attempt > 1 {
				appendSelfUpdateLog(opts.LogFile, "[self-update] executable swap succeeded on retry %d/%d", attempt, selfUpdateReplaceRetryTimes)
			}
			return nil
		}
		if attempt < selfUpdateReplaceRetryTimes {
			appendSelfUpdateLog(opts.LogFile, "[self-update] executable swap failed (%d/%d): %v; retry in %dms", attempt, selfUpdateReplaceRetryTimes, replaceErr, selfUpdateReplaceRetryDelay.Milliseconds())
			time.Sleep(selfUpdateReplaceRetryDelay)
		}
	}
	_ = os.Rename(opts.BackupPath, opts.TargetPath)
	return fmt.Errorf("写入新版本失败，重试 %d 次后仍失败: %w", selfUpdateReplaceRetryTimes, replaceErr)
}

func restartSelfUpdatedProcess(opts selfUpdateWorkerOptions) error {
	var lastErr error
	for attempt := 1; attempt <= selfUpdateRestartRetryTimes; attempt++ {
		if _, err := os.StartProcess(opts.TargetPath, []string{opts.TargetPath}, &os.ProcAttr{
			Dir: filepath.Dir(opts.TargetPath),
			Files: []*os.File{
				os.Stdin,
				os.Stdout,
				os.Stderr,
			},
		}); err == nil {
			if attempt > 1 {
				appendSelfUpdateLog(opts.LogFile, "[self-update] restart succeeded on retry %d/%d", attempt, selfUpdateRestartRetryTimes)
			}
			return nil
		} else {
			lastErr = err
		}
		if attempt < selfUpdateRestartRetryTimes {
			appendSelfUpdateLog(opts.LogFile, "[self-update] restart attempt failed (%d/%d): %v; retry in %dms", attempt, selfUpdateRestartRetryTimes, lastErr, selfUpdateRestartRetryDelay.Milliseconds())
			time.Sleep(selfUpdateRestartRetryDelay)
		}
	}
	return fmt.Errorf("重启失败，重试 %d 次后仍失败: %w", selfUpdateRestartRetryTimes, lastErr)
}

func startSelfUpdateServiceWithRetry(opts selfUpdateWorkerOptions) error {
	var lastErr error
	for attempt := 1; attempt <= selfUpdateRestartRetryTimes; attempt++ {
		lastErr = startService(opts.ServiceName, selfUpdateServiceOpTimeout)
		if lastErr == nil {
			if attempt > 1 {
				appendSelfUpdateLog(opts.LogFile, "[self-update] service restart succeeded on retry %d/%d", attempt, selfUpdateRestartRetryTimes)
			}
			return nil
		}
		if attempt < selfUpdateRestartRetryTimes {
			appendSelfUpdateLog(opts.LogFile, "[self-update] service restart failed (%d/%d): %v; retry in %dms", attempt, selfUpdateRestartRetryTimes, lastErr, selfUpdateRestartRetryDelay.Milliseconds())
			time.Sleep(selfUpdateRestartRetryDelay)
		}
	}
	return fmt.Errorf("服务重启失败，重试 %d 次后仍失败: %w", selfUpdateRestartRetryTimes, lastErr)
}

func updateSelfUpdateResult(opts selfUpdateWorkerOptions, status, errMsg string, newSize int64) error {
	if opts.DeploymentsFile == "" || opts.DeploymentID == "" {
		return nil
	}
	var lastErr error
	for i := 0; i < 20; i++ {
		store, err := newDeploymentStore(opts.DeploymentsFile)
		if err != nil {
			lastErr = err
			time.Sleep(200 * time.Millisecond)
			continue
		}
		now := time.Now()
		err = store.UpdateField(opts.DeploymentID, func(dep *Deployment) {
			dep.Status = status
			dep.FinishedAt = &now
			if !dep.StartedAt.IsZero() {
				dep.DurationMs = now.Sub(dep.StartedAt).Milliseconds()
			}
			dep.Error = errMsg
			if newSize > 0 && len(dep.Changed) == 0 {
				dep.Changed = []ChangedFile{{
					Path:   filepath.Base(opts.TargetPath),
					Action: "updated",
					Size:   newSize,
				}}
			}
		})
		if err == nil {
			return nil
		}
		lastErr = err
		time.Sleep(200 * time.Millisecond)
	}
	return lastErr
}

func appendSelfUpdateLog(logFile, format string, args ...any) {
	logFile = strings.TrimSpace(logFile)
	if logFile == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(logFile), 0755); err != nil {
		return
	}
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	line := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprintf(f, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), line)
}
