package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

func main() {
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
	if cfg.AuthKeySHA256 == sha256Hex("ChangeMe123!") {
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
		cfg:       cfg,
		cfgPath:   "config.json",
		logWriter: logWriter,
		logger:    logger,
		templates: tmpl,
		store:     store,
		sessions:  newSessionManager(),
		events:    newEventHub(),
		static:    http.FileServer(http.FS(staticFS)),
	}

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
	mux.HandleFunc("/partials/deployments", a.requireAuth(a.handleDeploymentsPartial))
	mux.HandleFunc("/api/upload", a.requireAuth(a.handleUpload))
	mux.HandleFunc("/api/config", a.requireAuth(a.handleConfigAPI))
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
		_ = a.templates.ExecuteTemplate(w, "login.html", map[string]any{"Error": ""})
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
		_ = a.templates.ExecuteTemplate(w, "login.html", map[string]any{"Error": "密钥错误"})
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
	cfg := a.currentConfig()
	project := getDefaultProject(cfg)
	_ = a.templates.ExecuteTemplate(w, "index.html", map[string]any{
		"ServiceName":    project.ServiceName,
		"TargetDir":      project.TargetDir,
		"MaxUploadMB":    project.MaxUploadMB,
		"CurrentVersion": project.CurrentVersion,
	})
}

func (a *App) handleDeploymentsPartial(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_ = a.templates.ExecuteTemplate(w, "deployments.html", map[string]any{
		"Deployments": a.store.List(),
	})
}

func (a *App) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg := a.currentConfig()
	if atomic.LoadInt32(&a.deploying) == 1 {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "当前已有部署任务在执行，请稍后再试"})
		return
	}

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

	if !atomic.CompareAndSwapInt32(&a.deploying, 0, 1) {
		_ = os.Remove(uploadPath)
		writeJSON(w, http.StatusConflict, map[string]any{"error": "当前已有部署任务在执行，请稍后再试"})
		return
	}

	now := time.Now()
	requestVersion := normalizeVersion(r.FormValue("target_version"))
	targetVersion := requestVersion
	if targetVersion == "" {
		nextVer, nextErr := nextPatchVersion(project.CurrentVersion)
		if nextErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("当前版本格式错误，无法自动递增: %v", nextErr)})
			return
		}
		targetVersion = nextVer
	}
	if !isValidVersion(targetVersion) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("版本号格式错误: %s，正确格式示例: 0.0.2 / 0.1.1 / 1.0.1", targetVersion)})
		return
	}

	dep := Deployment{
		ID:            id,
		Type:          "deploy",
		Version:       targetVersion,
		ProjectID:     project.ID,
		ProjectName:   project.Name,
		BackupIgnore:  append([]string{}, project.BackupIgnore...),
		ReplaceIgnore: append([]string{}, resolveReplaceIgnoreRulesForTarget(project.TargetDir, project.ReplaceIgnore, project.BackupIgnore)...),
		Status:        "queued",
		Note:          strings.TrimSpace(r.FormValue("note")),
		LoginIP:       clientIP(r),
		CreatedAt:     now,
		StartedAt:     now,
		UploadFile:    uploadPath,
		ServiceName:   project.ServiceName,
		TargetDir:     project.TargetDir,
	}
	if dep.Note == "" {
		dep.Note = "(未填写更新说明)"
	}

	if err := a.store.Add(dep); err != nil {
		atomic.StoreInt32(&a.deploying, 0)
		_ = os.Remove(uploadPath)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": fmt.Sprintf("记录部署任务失败: %v", err)})
		return
	}

	go a.runDeployment(id)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"id":           id,
		"status":       "queued",
		"version":      targetVersion,
		"project_id":   project.ID,
		"project_name": project.Name,
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
	if atomic.LoadInt32(&a.deploying) == 1 {
		http.Error(w, "当前已有任务在执行", http.StatusConflict)
		return
	}
	source, ok := a.store.Get(sourceID)
	if !ok {
		http.Error(w, "deployment not found", http.StatusNotFound)
		return
	}
	if source.BackupFile == "" {
		http.Error(w, "该部署记录没有备份文件，无法回滚", http.StatusBadRequest)
		return
	}
	if !atomic.CompareAndSwapInt32(&a.deploying, 0, 1) {
		http.Error(w, "当前已有任务在执行", http.StatusConflict)
		return
	}

	now := time.Now()
	id := newID("rb")
	rollback := Deployment{
		ID:            id,
		Type:          "rollback",
		RollbackOf:    sourceID,
		Version:       source.Version,
		ProjectID:     source.ProjectID,
		ProjectName:   source.ProjectName,
		BackupIgnore:  append([]string{}, source.BackupIgnore...),
		ReplaceIgnore: append([]string{}, source.ReplaceIgnore...),
		Status:        "queued",
		Note:          fmt.Sprintf("回滚到 %s", sourceID),
		LoginIP:       clientIP(r),
		CreatedAt:     now,
		StartedAt:     now,
		BackupFile:    source.BackupFile,
		ServiceName:   source.ServiceName,
		TargetDir:     source.TargetDir,
	}
	if err := a.store.Add(rollback); err != nil {
		atomic.StoreInt32(&a.deploying, 0)
		http.Error(w, "回滚任务创建失败", http.StatusInternalServerError)
		return
	}
	go a.runRollback(id, sourceID)
	a.handleDeploymentsPartial(w, r)
}

func (a *App) handleConfigAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		cfg := a.currentConfig()
		projectsJSON, _ := json.MarshalIndent(cfg.Projects, "", "  ")
		dp := getDefaultProject(cfg)
		writeJSON(w, http.StatusOK, map[string]any{
			"listen_addr":         cfg.ListenAddr,
			"session_cookie":      cfg.SessionCookie,
			"default_project_id":  cfg.DefaultProjectID,
			"projects_json":       string(projectsJSON),
			"current_version":     dp.CurrentVersion,
			"upload_dir":          cfg.UploadDir,
			"work_dir":            cfg.WorkDir,
			"backup_dir":          cfg.BackupDir,
			"deployments_file":    cfg.DeploymentsFile,
			"log_file":            cfg.LogFile,
			"service_name":        dp.ServiceName,
			"target_dir":          dp.TargetDir,
			"backup_ignore_text":  strings.Join(dp.BackupIgnore, "\n"),
			"replace_ignore_text": strings.Join(dp.ReplaceIgnore, "\n"),
			"max_upload_mb":       dp.MaxUploadMB,
			"projects":            cfg.Projects,
		})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求参数解析失败"})
			return
		}
	} else {
		if err := r.ParseForm(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求参数解析失败"})
			return
		}
	}

	oldCfg := a.currentConfig()
	newCfg := oldCfg
	newCfg.ListenAddr = strings.TrimSpace(r.FormValue("listen_addr"))
	newCfg.SessionCookie = strings.TrimSpace(r.FormValue("session_cookie"))
	newCfg.UploadDir = strings.TrimSpace(r.FormValue("upload_dir"))
	newCfg.WorkDir = strings.TrimSpace(r.FormValue("work_dir"))
	newCfg.BackupDir = strings.TrimSpace(r.FormValue("backup_dir"))
	newCfg.DeploymentsFile = strings.TrimSpace(r.FormValue("deployments_file"))
	newCfg.LogFile = strings.TrimSpace(r.FormValue("log_file"))
	newCfg.DefaultProjectID = strings.TrimSpace(r.FormValue("default_project_id"))

	projectsJSON := strings.TrimSpace(r.FormValue("projects_json"))
	if projectsJSON != "" {
		var projects []ManagedProject
		if err := json.Unmarshal([]byte(projectsJSON), &projects); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("projects_json 解析失败: %v", err)})
			return
		}
		newCfg.Projects = projects
	}
	normalizeProjects(&newCfg)

	// 默认程序编辑区覆盖默认项目，便于不手改 JSON 也能保存
	serviceName := strings.TrimSpace(r.FormValue("service_name"))
	if serviceName == "" {
		serviceName = getDefaultProject(newCfg).ServiceName
	}
	dp, ok := findProjectByID(newCfg.Projects, newCfg.DefaultProjectID)
	if ok {
		dp.ServiceName = serviceName
		if target := strings.TrimSpace(r.FormValue("target_dir")); target != "" {
			dp.TargetDir = target
		}
		if ver := normalizeVersion(r.FormValue("current_version")); ver != "" {
			dp.CurrentVersion = ver
		}
		if maxUploadRaw := strings.TrimSpace(r.FormValue("max_upload_mb")); maxUploadRaw != "" {
			maxUpload, err := strconv.ParseInt(maxUploadRaw, 10, 64)
			if err != nil || maxUpload <= 0 {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("max_upload_mb 必须为正整数，当前值: %q", maxUploadRaw)})
				return
			}
			dp.MaxUploadMB = maxUpload
		}
		dp.BackupIgnore = splitLinesTrim(r.FormValue("backup_ignore_text"))
		dp.ReplaceIgnore = splitLinesTrim(r.FormValue("replace_ignore_text"))
		for i := range newCfg.Projects {
			if newCfg.Projects[i].ID == dp.ID {
				newCfg.Projects[i] = dp
				break
			}
		}
		normalizeProjects(&newCfg)
	}

	newKey := strings.TrimSpace(r.FormValue("new_auth_key"))
	if newKey != "" {
		newCfg.AuthKeySHA256 = sha256Hex(newKey)
	}
	if newCfg.SessionCookie == "" {
		newCfg.SessionCookie = "updater_session"
	}
	if err := validateRuntimeConfig(newCfg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if err := ensureDirectories(newCfg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("目录校验失败: %v", err)})
		return
	}

	appliedLog := false
	appliedStore := false
	if oldCfg.LogFile != newCfg.LogFile {
		if err := a.logWriter.SwitchFile(newCfg.LogFile); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("切换日志文件失败: %v", err)})
			return
		}
		appliedLog = true
	}
	if oldCfg.DeploymentsFile != newCfg.DeploymentsFile {
		if err := a.store.SwitchFile(newCfg.DeploymentsFile); err != nil {
			if appliedLog {
				_ = a.logWriter.SwitchFile(oldCfg.LogFile)
			}
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("切换部署记录文件失败: %v", err)})
			return
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
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": fmt.Sprintf("保存配置失败: %v", err)})
		return
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

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"message":        "配置保存成功，已自动刷新运行配置",
		"restart_needed": len(restartFields) > 0,
		"restart_fields": restartFields,
		"projects":       newCfg.Projects,
	})
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
		if strings.TrimSpace(p.ServiceName) == "" {
			return fmt.Errorf("projects(%s).service_name 不能为空", p.ID)
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
	}
	if strings.TrimSpace(cfg.DefaultProjectID) == "" {
		return errors.New("default_project_id 不能为空")
	}
	if _, ok := ids[cfg.DefaultProjectID]; !ok {
		return fmt.Errorf("default_project_id 不存在: %s", cfg.DefaultProjectID)
	}
	if strings.TrimSpace(cfg.ServiceName) == "" {
		return errors.New("service_name 不能为空")
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
