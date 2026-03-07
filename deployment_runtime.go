package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	postStopSettleDelay = 2 * time.Second
	fileOpRetryTimes    = 5
	fileOpRetryDelay    = 1500 * time.Millisecond
)

func (a *App) runDeployment(id, projectID string) {
	defer a.releaseProjectTask(projectID)
	defer a.notifyDeploymentIfNeeded(id)
	defer func() {
		if rec := recover(); rec != nil {
			a.logger.Error("deployment panic", "deployment_id", id, "panic", rec)
			_ = a.store.UpdateField(id, func(dep *Deployment) {
				dep.Status = "failed"
				now := time.Now()
				dep.FinishedAt = &now
				dep.DurationMs = now.Sub(dep.StartedAt).Milliseconds()
				dep.Error = fmt.Sprintf("panic: %v", rec)
			})
			a.publish(id, "error", "部署异常崩溃: %v", rec)
		}
	}()

	dep, ok := a.store.Get(id)
	if !ok {
		return
	}
	cfg := a.currentConfig()
	var project ManagedProject
	if dep.ProjectID != "" {
		if p, exists := findProjectByID(cfg.Projects, dep.ProjectID); exists {
			project = p
			dep.ServiceName = p.ServiceName
			if dep.TargetDir == "" {
				dep.TargetDir = p.TargetDir
			}
			if len(dep.BackupIgnore) == 0 {
				dep.BackupIgnore = append([]string{}, p.BackupIgnore...)
			}
			if len(dep.ReplaceIgnore) == 0 {
				dep.ReplaceIgnore = append([]string{}, p.ReplaceIgnore...)
			}
			if dep.ServiceInstallMode == "" {
				dep.ServiceInstallMode = p.ServiceInstallMode
			}
			if dep.ServiceExePath == "" {
				dep.ServiceExePath = p.ServiceExePath
			}
			if len(dep.ServiceArgs) == 0 {
				dep.ServiceArgs = append([]string{}, p.ServiceArgs...)
			}
			if dep.ServiceDisplayName == "" {
				dep.ServiceDisplayName = p.ServiceDisplayName
			}
			if dep.ServiceDescription == "" {
				dep.ServiceDescription = p.ServiceDescription
			}
			if dep.ServiceStartType == "" {
				dep.ServiceStartType = p.ServiceStartType
			}
		}
	}
	if dep.TargetDir == "" {
		dp := getDefaultProject(cfg)
		if dep.TargetDir == "" {
			dep.TargetDir = dp.TargetDir
		}
	}
	start := time.Now()
	_ = a.store.UpdateField(id, func(d *Deployment) {
		d.Status = "deploying"
		d.StartedAt = start
	})

	finish := func(status string, err error, changed []ChangedFile, backupPath string) {
		now := time.Now()
		_ = a.store.UpdateField(id, func(d *Deployment) {
			d.Status = status
			d.FinishedAt = &now
			d.DurationMs = now.Sub(start).Milliseconds()
			if len(changed) > 0 {
				d.Changed = changed
			}
			if backupPath != "" {
				d.BackupFile = backupPath
			}
			if err != nil {
				d.Error = err.Error()
			} else {
				d.Error = ""
			}
		})
	}

	a.publishProgress(id, "info", "准备部署", 5, "部署开始")
	targetExists, targetEmpty, targetCheckErr := inspectTargetDirState(dep.TargetDir)
	if targetCheckErr != nil {
		finish("failed", fmt.Errorf("检查目标目录失败: %w", targetCheckErr), nil, "")
		a.publish(id, "error", "检查目标目录失败: %v", targetCheckErr)
		return
	}
	targetHasExistingFiles := targetExists && !targetEmpty
	dep.InitialDeploy = isRuntimeInitialDeploy(targetExists, targetEmpty, dep.ClearTargetBeforeDeploy)
	dep.ServiceInstallMode = normalizeServiceInstallMode(dep.ServiceInstallMode)
	dep.ServiceStartType = normalizeServiceStartType(dep.ServiceStartType)
	_ = a.store.UpdateField(id, func(d *Deployment) {
		d.InitialDeploy = dep.InitialDeploy
		d.ServiceInstallMode = dep.ServiceInstallMode
		d.ServiceExePath = dep.ServiceExePath
		d.ServiceArgs = append([]string{}, dep.ServiceArgs...)
		d.ServiceDisplayName = dep.ServiceDisplayName
		d.ServiceDescription = dep.ServiceDescription
		d.ServiceStartType = dep.ServiceStartType
		d.ClearTargetBeforeDeploy = dep.ClearTargetBeforeDeploy
	})
	if dep.InitialDeploy {
		if dep.ClearTargetBeforeDeploy && targetHasExistingFiles {
			a.publish(id, "warn", "首次部署专页检测到目标目录已有内容：确认后将先清空该目录，再执行首次部署")
		} else {
			a.publish(id, "info", "检测到首次部署：目标目录为空或不存在")
		}
		if project.ID != "" && !project.AllowInitialDeploy {
			finish("failed", errors.New("当前程序未开启首次部署，请在程序配置中勾选 allow_initial_deploy 后重试"), nil, "")
			a.publish(id, "error", "当前程序未开启首次部署，请在程序配置中勾选 allow_initial_deploy 后重试")
			return
		}
	}
	backupRules := dep.BackupIgnore
	if len(backupRules) == 0 {
		backupRules = cfg.BackupIgnore
	}
	backupIgnore := loadBackupIgnoreMatcherForTarget(dep.TargetDir, backupRules)
	replaceRules := dep.ReplaceIgnore
	if dep.InitialDeploy {
		replaceRules = nil
		a.publish(id, "info", "首次部署不应用 replace_ignore / backup_ignore，压缩包内容会完整下发")
	} else if len(replaceRules) == 0 {
		replaceRules = resolveReplaceIgnoreRulesForTarget(dep.TargetDir, cfg.ReplaceIgnore, cfg.BackupIgnore)
	}
	replaceIgnore := newIgnoreMatcher(append(append([]string{}, replaceRules...), ".replaceignore"))
	dep.ReplaceMode = normalizeReplaceMode(dep.ReplaceMode)
	removeMissing := dep.ReplaceMode == ReplaceModeFull
	if removeMissing {
		a.publish(id, "info", "替换模式: 全部替换（目标目录中上传包不存在的文件将被删除）")
	} else {
		a.publish(id, "info", "替换模式: 局部替换（仅覆盖上传包中的文件，不删除其他文件）")
	}

	backupPath := filepath.Join(cfg.BackupDir, id+".zip")
	if dep.InitialDeploy {
		backupPath = ""
		dep.BackupSkipped = true
		_ = a.store.UpdateField(id, func(d *Deployment) {
			d.BackupFile = ""
			d.BackupSkipped = true
		})
		a.publishProgress(id, "info", "备份目标目录", 30, "首次部署跳过备份：目标目录为空或不存在")
	} else {
		a.publishProgress(id, "info", "备份目标目录", 15, "开始备份目标目录")
		if err := zipDirectory(dep.TargetDir, backupPath, backupIgnore); err != nil {
			finish("failed", fmt.Errorf("备份失败: %w", err), nil, "")
			a.publish(id, "error", "备份失败: %v", err)
			return
		}
		_ = a.store.UpdateField(id, func(d *Deployment) {
			d.BackupFile = backupPath
			d.BackupSkipped = false
		})
		a.publishProgress(id, "info", "备份目标目录", 30, "备份完成: %s", backupPath)
	}

	workDir := filepath.Join(cfg.WorkDir, id)
	extractDir := filepath.Join(workDir, "extract")
	_ = os.RemoveAll(workDir)
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		finish("failed", fmt.Errorf("创建临时目录失败: %w", err), nil, backupPath)
		a.publish(id, "error", "创建临时目录失败: %v", err)
		return
	}
	defer os.RemoveAll(workDir)

	a.publishProgress(id, "info", "解压上传包", 40, "解压上传包")
	if err := extractZip(dep.UploadFile, extractDir); err != nil {
		finish("failed", fmt.Errorf("解压失败: %w", err), nil, backupPath)
		a.publish(id, "error", "解压失败: %v", err)
		return
	}

	serviceManaged := dep.ServiceName != ""
	serviceExistsNow := false
	serviceShouldCreate := false
	if serviceManaged {
		var serviceErr error
		serviceExistsNow, serviceErr = serviceExists(dep.ServiceName)
		if serviceErr != nil {
			finish("failed", fmt.Errorf("检查服务状态失败: %w", serviceErr), nil, backupPath)
			a.publish(id, "error", "检查服务状态失败: %v", serviceErr)
			return
		}
		serviceShouldCreate = !serviceExistsNow && dep.ServiceInstallMode != ServiceInstallModeNone
	}
	if serviceManaged && serviceExistsNow {
		a.publishProgress(id, "info", "停止服务", 55, "停止服务: %s", dep.ServiceName)
		if err := stopService(dep.ServiceName, 45*time.Second); err != nil {
			finish("failed", fmt.Errorf("停止服务失败: %w", err), nil, backupPath)
			a.publish(id, "error", "停止服务失败: %v", err)
			return
		}
		a.publish(id, "info", "服务已停止")
		a.waitAfterServiceStop(id, "替换文件", 60, dep.ServiceName)
	} else if serviceManaged && serviceShouldCreate {
		a.publish(id, "info", "服务 %s 当前不存在，将在部署完成后自动创建", dep.ServiceName)
	} else if serviceManaged {
		finish("failed", fmt.Errorf("服务 %s 不存在，且当前项目未启用服务安装", dep.ServiceName), nil, backupPath)
		a.publish(id, "error", "服务 %s 不存在，且当前项目未启用服务安装", dep.ServiceName)
		return
	} else {
		a.publish(id, "warn", "service_name 为空，跳过停止服务，直接替换文件")
	}
	if err := os.MkdirAll(dep.TargetDir, 0755); err != nil {
		finish("failed", fmt.Errorf("创建目标目录失败: %w", err), nil, backupPath)
		a.publish(id, "error", "创建目标目录失败: %v", err)
		return
	}
	if dep.ClearTargetBeforeDeploy && targetHasExistingFiles {
		a.publishProgress(id, "warn", "清空目标目录", 66, "检测到首次部署前目标目录已有内容，开始清空目标目录")
		if err := a.runFileOpWithRetry(id, "清空目标目录", 66, "清空目标目录", func() error {
			return clearDirWithIgnore(dep.TargetDir, newIgnoreMatcher(nil))
		}); err != nil {
			finish("failed", fmt.Errorf("清空目标目录失败: %w", err), nil, backupPath)
			a.publish(id, "error", "清空目标目录失败: %v", err)
			return
		}
		a.publish(id, "warn", "目标目录已清空，开始部署压缩包内容")
	}

	a.publishProgress(id, "info", "替换文件", 70, "开始替换文件")
	var changed []ChangedFile
	err := a.runFileOpWithRetry(id, "替换文件", 70, "替换文件", func() error {
		var syncErr error
		changed, syncErr = syncDirectories(extractDir, dep.TargetDir, replaceIgnore, removeMissing)
		return syncErr
	})
	if err != nil {
		if serviceManaged {
			if restartErr := startService(dep.ServiceName, 45*time.Second); restartErr != nil {
				err = fmt.Errorf("%v; 尝试恢复启动服务失败: %v", err, restartErr)
			}
		}
		finish("failed", fmt.Errorf("替换文件失败: %w", err), nil, backupPath)
		a.publish(id, "error", "替换文件失败: %v", err)
		return
	}
	a.publishProgress(id, "info", "替换文件", 82, "文件替换完成，变更文件数: %d", len(changed))
	if serviceShouldCreate {
		serviceCfg, cfgErr := buildServiceInstallConfig(cfg, dep)
		if cfgErr != nil {
			finish("failed", cfgErr, changed, backupPath)
			a.publish(id, "error", "%v", cfgErr)
			return
		}
		a.publishProgress(id, "info", "安装服务", 86, "创建服务: %s", dep.ServiceName)
		if err := createService(dep.ServiceName, serviceCfg); err != nil {
			finish("failed", fmt.Errorf("创建服务失败: %w", err), changed, backupPath)
			a.publish(id, "error", "创建服务失败: %v", err)
			return
		}
		dep.ServiceCreated = true
		_ = a.store.UpdateField(id, func(d *Deployment) { d.ServiceCreated = true })
		a.publish(id, "info", "服务已创建: %s", dep.ServiceName)
	}

	if serviceManaged {
		a.publishProgress(id, "info", "启动服务", 90, "启动服务: %s", dep.ServiceName)
		if err := startService(dep.ServiceName, 45*time.Second); err != nil {
			finish("failed", fmt.Errorf("启动服务失败: %w", err), changed, backupPath)
			a.publish(id, "error", "启动服务失败: %v", err)
			return
		}
	} else {
		a.publish(id, "warn", "service_name 为空，跳过启动服务")
	}

	if dep.Version != "" {
		if err := a.setProjectCurrentVersion(dep.ProjectID, dep.Version); err != nil {
			a.publish(id, "warn", "部署成功，但写入当前版本失败: %v", err)
		} else {
			a.publishProgress(id, "info", "更新版本号", 95, "当前版本已更新为: %s", dep.Version)
		}
	}

	finish("success", nil, changed, backupPath)
	a.publishProgress(id, "info", "部署完成", 100, "部署完成，耗时 %d ms", time.Since(start).Milliseconds())
}

func (a *App) runRollback(id, sourceID, projectID string) {
	defer a.releaseProjectTask(projectID)
	defer a.notifyDeploymentIfNeeded(id)
	defer func() {
		if rec := recover(); rec != nil {
			a.logger.Error("rollback panic", "deployment_id", id, "panic", rec)
			_ = a.store.UpdateField(id, func(dep *Deployment) {
				dep.Status = "failed"
				now := time.Now()
				dep.FinishedAt = &now
				dep.DurationMs = now.Sub(dep.StartedAt).Milliseconds()
				dep.Error = fmt.Sprintf("panic: %v", rec)
			})
			a.publish(id, "error", "回滚异常崩溃: %v", rec)
		}
	}()

	dep, ok := a.store.Get(id)
	if !ok {
		return
	}
	cfg := a.currentConfig()
	source, ok := a.store.Get(sourceID)
	if !ok {
		_ = a.store.UpdateField(id, func(d *Deployment) {
			d.Status = "failed"
			now := time.Now()
			d.FinishedAt = &now
			d.DurationMs = now.Sub(d.StartedAt).Milliseconds()
			d.Error = "源部署记录不存在"
		})
		return
	}

	start := time.Now()
	_ = a.store.UpdateField(id, func(d *Deployment) {
		d.Status = "rollbacking"
		d.StartedAt = start
	})

	finish := func(status string, err error) {
		now := time.Now()
		_ = a.store.UpdateField(id, func(d *Deployment) {
			d.Status = status
			d.FinishedAt = &now
			d.DurationMs = now.Sub(start).Milliseconds()
			if err != nil {
				d.Error = err.Error()
			} else {
				d.Error = ""
			}
		})
	}

	if dep.BackupFile == "" {
		dep.BackupFile = source.BackupFile
	}
	dep.ServiceName = source.ServiceName
	if dep.TargetDir == "" {
		dep.TargetDir = source.TargetDir
	}
	if len(dep.ReplaceIgnore) == 0 && len(source.ReplaceIgnore) > 0 {
		dep.ReplaceIgnore = append([]string{}, source.ReplaceIgnore...)
	}
	if dep.BackupFile == "" {
		finish("failed", errors.New("回滚失败: 备份文件路径为空"))
		a.publish(id, "error", "回滚失败: 备份文件路径为空")
		return
	}
	if _, err := os.Stat(dep.BackupFile); err != nil {
		finish("failed", fmt.Errorf("回滚失败: 找不到备份文件 %s", dep.BackupFile))
		a.publish(id, "error", "回滚失败: 找不到备份文件 %s", dep.BackupFile)
		return
	}

	replaceRules := dep.ReplaceIgnore
	if len(replaceRules) == 0 {
		replaceRules = resolveReplaceIgnoreRulesForTarget(dep.TargetDir, cfg.ReplaceIgnore, cfg.BackupIgnore)
	}
	replaceIgnore := newIgnoreMatcher(append(append([]string{}, replaceRules...), ".replaceignore"))
	a.publishProgress(id, "info", "准备回滚", 8, "回滚开始，目标记录: %s", sourceID)

	serviceManaged := dep.ServiceName != ""
	if serviceManaged {
		a.publishProgress(id, "info", "停止服务", 30, "停止服务: %s", dep.ServiceName)
		if err := stopService(dep.ServiceName, 45*time.Second); err != nil {
			finish("failed", fmt.Errorf("停止服务失败: %w", err))
			a.publish(id, "error", "停止服务失败: %v", err)
			return
		}
		a.waitAfterServiceStop(id, "清理目标目录", 40, dep.ServiceName)
	} else {
		a.publish(id, "warn", "service_name 为空，跳过停止服务，直接回滚文件")
	}

	a.publishProgress(id, "info", "清理目标目录", 50, "清理目标目录（保留忽略项）")
	if err := a.runFileOpWithRetry(id, "清理目标目录", 50, "清理目标目录", func() error {
		return clearDirWithIgnore(dep.TargetDir, replaceIgnore)
	}); err != nil {
		if serviceManaged {
			_ = startService(dep.ServiceName, 45*time.Second)
		}
		finish("failed", fmt.Errorf("清理目标目录失败: %w", err))
		a.publish(id, "error", "清理目标目录失败: %v", err)
		return
	}

	a.publishProgress(id, "info", "恢复备份包", 70, "恢复备份包: %s", dep.BackupFile)
	if err := a.runFileOpWithRetry(id, "恢复备份包", 70, "恢复备份包", func() error {
		return extractZip(dep.BackupFile, dep.TargetDir)
	}); err != nil {
		if serviceManaged {
			if restartErr := startService(dep.ServiceName, 45*time.Second); restartErr != nil {
				err = fmt.Errorf("%v; 尝试恢复启动服务失败: %v", err, restartErr)
			}
		}
		finish("failed", fmt.Errorf("恢复备份失败: %w", err))
		a.publish(id, "error", "恢复备份失败: %v", err)
		return
	}

	if serviceManaged {
		a.publishProgress(id, "info", "启动服务", 90, "启动服务: %s", dep.ServiceName)
		if err := startService(dep.ServiceName, 45*time.Second); err != nil {
			finish("failed", fmt.Errorf("启动服务失败: %w", err))
			a.publish(id, "error", "启动服务失败: %v", err)
			return
		}
	} else {
		a.publish(id, "warn", "service_name 为空，跳过启动服务")
	}

	if source.Version != "" {
		projectID := source.ProjectID
		if projectID == "" {
			projectID = dep.ProjectID
		}
		if err := a.setProjectCurrentVersion(projectID, source.Version); err != nil {
			a.publish(id, "warn", "回滚成功，但写入当前版本失败: %v", err)
		} else {
			a.publishProgress(id, "info", "更新版本号", 95, "当前版本已回滚为: %s", source.Version)
		}
	}

	finish("success", nil)
	a.publishProgress(id, "info", "回滚完成", 100, "回滚完成，耗时 %d ms", time.Since(start).Milliseconds())
}

func (a *App) runSelfUpdate(id string) {
	defer a.releaseSelfTask()
	defer func() {
		if rec := recover(); rec != nil {
			a.logger.Error("self-update panic", "deployment_id", id, "panic", rec)
			_ = a.store.UpdateField(id, func(dep *Deployment) {
				dep.Status = "failed"
				now := time.Now()
				dep.FinishedAt = &now
				dep.DurationMs = now.Sub(dep.StartedAt).Milliseconds()
				dep.Error = fmt.Sprintf("panic: %v", rec)
			})
			a.publish(id, "error", "自更新异常崩溃: %v", rec)
		}
	}()

	dep, ok := a.store.Get(id)
	if !ok {
		return
	}
	cfg := a.currentConfig()
	start := time.Now()
	_ = a.store.UpdateField(id, func(d *Deployment) {
		d.Status = "self_updating"
		d.StartedAt = start
	})

	finish := func(status string, err error, changed []ChangedFile, backupPath string) {
		now := time.Now()
		_ = a.store.UpdateField(id, func(d *Deployment) {
			d.Status = status
			d.FinishedAt = &now
			d.DurationMs = now.Sub(start).Milliseconds()
			if len(changed) > 0 {
				d.Changed = changed
			}
			if backupPath != "" {
				d.BackupFile = backupPath
			}
			if err != nil {
				d.Error = err.Error()
			} else {
				d.Error = ""
			}
		})
	}

	a.publishProgress(id, "info", "准备自更新", 10, "SimpleRemoteUpdate 自更新开始")
	exePath, err := os.Executable()
	if err != nil {
		finish("failed", fmt.Errorf("读取当前程序路径失败: %w", err), nil, "")
		a.publish(id, "error", "读取当前程序路径失败: %v", err)
		return
	}
	exePath, _ = filepath.Abs(exePath)
	a.publish(id, "info", "当前程序路径: %s", exePath)

	backupPath := filepath.Join(cfg.BackupDir, id+"-updater-old.exe")
	if backupPath, err = filepath.Abs(backupPath); err != nil {
		finish("failed", fmt.Errorf("解析备份路径失败: %w", err), nil, "")
		a.publish(id, "error", "解析备份路径失败: %v", err)
		return
	}
	a.publishProgress(id, "info", "备份当前程序", 35, "备份当前程序: %s", backupPath)
	if err := copyFile(exePath, backupPath); err != nil {
		finish("failed", fmt.Errorf("备份当前程序失败: %w", err), nil, "")
		a.publish(id, "error", "备份当前程序失败: %v", err)
		return
	}

	workDir := filepath.Join(cfg.WorkDir, id, "self-update")
	if workDir, err = filepath.Abs(workDir); err != nil {
		finish("failed", fmt.Errorf("解析自更新工作目录失败: %w", err), nil, backupPath)
		a.publish(id, "error", "解析自更新工作目录失败: %v", err)
		return
	}
	_ = os.RemoveAll(workDir)
	if err := os.MkdirAll(workDir, 0755); err != nil {
		finish("failed", fmt.Errorf("创建自更新工作目录失败: %w", err), nil, backupPath)
		a.publish(id, "error", "创建自更新工作目录失败: %v", err)
		return
	}

	a.publishProgress(id, "info", "准备新版本文件", 55, "准备新版本文件")
	stagedPath := filepath.Join(workDir, "updater.new.exe")
	if err := copyFile(dep.UploadFile, stagedPath); err != nil {
		finish("failed", fmt.Errorf("准备新版本文件失败: %w", err), nil, backupPath)
		a.publish(id, "error", "准备新版本文件失败: %v", err)
		return
	}
	newSize := int64(0)
	if info, statErr := os.Stat(stagedPath); statErr == nil {
		newSize = info.Size()
	}

	workerPath := filepath.Join(workDir, "self-update-worker.exe")
	a.publishProgress(id, "info", "准备更新工作进程", 72, "准备更新工作进程")
	if err := copyFile(exePath, workerPath); err != nil {
		finish("failed", fmt.Errorf("准备自更新工作进程失败: %w", err), nil, backupPath)
		a.publish(id, "error", "准备自更新工作进程失败: %v", err)
		return
	}
	deploymentsFile := cfg.DeploymentsFile
	if deploymentsFile, err = filepath.Abs(deploymentsFile); err != nil {
		finish("failed", fmt.Errorf("解析 deployments_file 路径失败: %w", err), nil, backupPath)
		a.publish(id, "error", "解析 deployments_file 路径失败: %v", err)
		return
	}
	logFile := cfg.LogFile
	if logFile, err = filepath.Abs(logFile); err != nil {
		finish("failed", fmt.Errorf("解析 log_file 路径失败: %w", err), nil, backupPath)
		a.publish(id, "error", "解析 log_file 路径失败: %v", err)
		return
	}
	selfUpdateServiceName := resolveSelfUpdateServiceName(cfg)
	if selfUpdateServiceName != "" {
		a.publish(id, "info", "自更新将通过服务重启: %s", selfUpdateServiceName)
	} else {
		a.publish(id, "warn", "未检测到自更新服务名，回退为进程直启方式重启")
	}

	changed := []ChangedFile{{
		Path:   filepath.Base(exePath),
		Action: "updated",
		Size:   newSize,
	}}
	now := time.Now()
	_ = a.store.UpdateField(id, func(d *Deployment) {
		d.Status = "switching"
		d.TargetDir = filepath.Dir(exePath)
		d.BackupFile = backupPath
		d.Changed = changed
		d.FinishedAt = &now
		d.DurationMs = now.Sub(start).Milliseconds()
		d.Error = ""
	})

	args := []string{
		workerPath,
		"--self-update-worker",
		"--target", exePath,
		"--source", stagedPath,
		"--backup", backupPath,
		"--deployment-id", id,
		"--deployments-file", deploymentsFile,
		"--log-file", logFile,
		"--wait-seconds", "120",
	}
	if selfUpdateServiceName != "" {
		args = append(args, "--service-name", selfUpdateServiceName)
	}

	procAttr := &os.ProcAttr{
		Dir: workDir,
		Files: []*os.File{
			os.Stdin,
			os.Stdout,
			os.Stderr,
		},
		Sys: selfUpdateWorkerSysProcAttr(),
	}
	proc, err := os.StartProcess(workerPath, args, procAttr)
	if err != nil && procAttr.Sys != nil {
		a.publish(id, "warn", "使用 breakaway 模式启动自更新工作进程失败，回退普通启动: %v", err)
		procAttr.Sys = nil
		proc, err = os.StartProcess(workerPath, args, procAttr)
	}
	if err != nil {
		finish("failed", fmt.Errorf("启动自更新工作进程失败: %w", err), changed, backupPath)
		a.publish(id, "error", "启动自更新工作进程失败: %v", err)
		return
	}

	a.publishProgress(id, "info", "切换新版本", 90, "自更新工作进程已启动，PID=%d", proc.Pid)
	a.publishProgress(id, "warn", "切换新版本", 96, "当前进程即将退出，替换完成后将自动重启")
	time.Sleep(1200 * time.Millisecond)
	os.Exit(0)
}

func resolveSelfUpdateServiceName(cfg Config) string {
	if v := strings.TrimSpace(cfg.SelfUpdateServiceName); v != "" {
		return v
	}
	for _, key := range []string{"NSSM_SERVICE_NAME", "SERVICE_NAME", "UPDATER_SERVICE_NAME"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}

func (a *App) waitAfterServiceStop(depID, stage string, progress int, serviceName string) {
	a.publishProgress(depID, "info", stage, progress, "服务已停止，等待 %.1f 秒释放文件句柄: %s", postStopSettleDelay.Seconds(), serviceName)
	time.Sleep(postStopSettleDelay)
}

func isTargetInitialDeploy(targetExists, targetEmpty bool) bool {
	return !targetExists || targetEmpty
}

func isPreviewInitialDeploy(targetExists, targetEmpty bool, deployEntry string) bool {
	if isTargetInitialDeploy(targetExists, targetEmpty) {
		return true
	}
	return normalizeDeployEntry(deployEntry) == DeployEntryInitial
}

func isRuntimeInitialDeploy(targetExists, targetEmpty bool, clearTargetBeforeDeploy bool) bool {
	if isTargetInitialDeploy(targetExists, targetEmpty) {
		return true
	}
	return clearTargetBeforeDeploy && targetExists && !targetEmpty
}

func inspectTargetDirState(target string) (exists bool, empty bool, err error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return false, true, errors.New("目标目录为空")
	}
	info, err := os.Stat(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, true, nil
		}
		return false, false, err
	}
	if !info.IsDir() {
		return false, false, fmt.Errorf("目标路径不是目录: %s", target)
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return true, false, err
	}
	return true, len(entries) == 0, nil
}

func buildServiceInstallConfig(cfg Config, dep Deployment) (ServiceInstallConfig, error) {
	exePath := strings.TrimSpace(dep.ServiceExePath)
	if exePath == "" {
		return ServiceInstallConfig{}, errors.New("启用服务安装时，必须填写压缩包解压后的 exe 文件名或相对路径")
	}
	if !filepath.IsAbs(exePath) {
		exePath = filepath.Join(dep.TargetDir, filepath.FromSlash(exePath))
	}
	return ServiceInstallConfig{
		Name:           dep.ServiceName,
		InstallMode:    dep.ServiceInstallMode,
		DisplayName:    firstNonEmpty(dep.ServiceDisplayName, dep.ServiceName),
		Description:    dep.ServiceDescription,
		ExecutablePath: exePath,
		Arguments:      append([]string{}, dep.ServiceArgs...),
		StartType:      dep.ServiceStartType,
		NSSMExePath:    strings.TrimSpace(cfg.NSSMExePath),
	}, nil
}

func (a *App) runFileOpWithRetry(depID, stage string, progress int, opName string, fn func() error) error {
	var lastErr error
	for attempt := 1; attempt <= fileOpRetryTimes; attempt++ {
		lastErr = fn()
		if lastErr == nil {
			if attempt > 1 {
				a.publishProgress(depID, "info", stage, progress, "%s重试后成功（第 %d 次）", opName, attempt)
			}
			return nil
		}
		if attempt < fileOpRetryTimes {
			a.publish(depID, "warn", "%s失败（第 %d/%d 次）: %v；%dms 后重试", opName, attempt, fileOpRetryTimes, lastErr, fileOpRetryDelay.Milliseconds())
			time.Sleep(fileOpRetryDelay)
		}
	}
	return fmt.Errorf("%s重试 %d 次后仍失败: %w", opName, fileOpRetryTimes, lastErr)
}

func (a *App) publish(depID, level, format string, args ...any) {
	a.publishProgress(depID, level, "", -1, format, args...)
}

func (a *App) publishProgress(depID, level, stage string, progress int, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if progress > 100 {
		progress = 100
	}
	if progress < -1 {
		progress = -1
	}
	switch level {
	case "error":
		a.logger.Error(msg, "deployment_id", depID)
	case "warn":
		a.logger.Warn(msg, "deployment_id", depID)
	default:
		a.logger.Info(msg, "deployment_id", depID)
	}
	a.events.Publish(depID, Event{
		Time:     time.Now().Format("15:04:05"),
		Level:    level,
		Text:     msg,
		Stage:    stage,
		Progress: progress,
	})
}
