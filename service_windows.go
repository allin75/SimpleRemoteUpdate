//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func serviceExists(name string) (bool, error) {
	m, err := mgr.Connect()
	if err != nil {
		return false, err
	}
	defer m.Disconnect()

	s, err := m.OpenService(strings.TrimSpace(name))
	if err != nil {
		if isServiceMissingError(err) {
			return false, nil
		}
		return false, err
	}
	defer s.Close()
	return true, nil
}

func createService(name string, cfg ServiceInstallConfig) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("service name is empty")
	}
	installMode := normalizeServiceInstallMode(cfg.InstallMode)
	if installMode == ServiceInstallModeNSSM {
		return createServiceWithNSSM(name, cfg)
	}
	exePath := strings.TrimSpace(cfg.ExecutablePath)
	if exePath == "" {
		return errors.New("service executable path is empty")
	}
	absPath, err := filepath.Abs(exePath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(absPath); err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	startType, err := windowsServiceStartType(cfg.StartType)
	if err != nil {
		return err
	}
	serviceCfg := mgr.Config{
		DisplayName:  firstNonEmpty(strings.TrimSpace(cfg.DisplayName), name),
		Description:  strings.TrimSpace(cfg.Description),
		StartType:    startType,
		ErrorControl: mgr.ErrorNormal,
	}
	s, err := m.CreateService(name, absPath, serviceCfg, cfg.Arguments...)
	if err != nil {
		return err
	}
	defer s.Close()
	return nil
}

func createServiceWithNSSM(name string, cfg ServiceInstallConfig) error {
	exePath := strings.TrimSpace(cfg.ExecutablePath)
	if exePath == "" {
		return errors.New("service executable path is empty")
	}
	absPath, err := filepath.Abs(exePath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(absPath); err != nil {
		return err
	}
	nssmPath, err := resolveNSSMPath(cfg.NSSMExePath, filepath.Dir(absPath))
	if err != nil {
		return err
	}
	args := append([]string{"install", name, absPath}, cfg.Arguments...)
	if err := runNSSMCommand(nssmPath, args...); err != nil {
		return err
	}
	appDir := filepath.Dir(absPath)
	if err := runNSSMCommand(nssmPath, "set", name, "AppDirectory", appDir); err != nil {
		return err
	}
	if displayName := strings.TrimSpace(cfg.DisplayName); displayName != "" {
		if err := runNSSMCommand(nssmPath, "set", name, "DisplayName", displayName); err != nil {
			return err
		}
	}
	if description := strings.TrimSpace(cfg.Description); description != "" {
		if err := runNSSMCommand(nssmPath, "set", name, "Description", description); err != nil {
			return err
		}
	}
	if err := runNSSMCommand(nssmPath, "set", name, "Start", nssmStartTypeValue(cfg.StartType)); err != nil {
		return err
	}
	return nil
}

func stopServiceImpl(ctx context.Context, name string, timeout time.Duration) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(name)
	if err != nil {
		return err
	}
	defer s.Close()

	st, err := s.Query()
	if err != nil {
		return err
	}
	if st.State == svc.Stopped {
		return nil
	}

	st, err = s.Control(svc.Stop)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "service has not been started") {
			return nil
		}
		return err
	}

	deadline := time.Now().Add(timeout)
	for st.State != svc.Stopped {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return errors.New("等待服务停止超时")
		}
		time.Sleep(500 * time.Millisecond)
		st, err = s.Query()
		if err != nil {
			return err
		}
	}
	return nil
}

func startServiceImpl(ctx context.Context, name string, timeout time.Duration) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(name)
	if err != nil {
		return err
	}
	defer s.Close()

	st, err := s.Query()
	if err != nil {
		return err
	}
	if st.State == svc.Running {
		return nil
	}

	if err := s.Start(); err != nil {
		lower := strings.ToLower(err.Error())
		if !strings.Contains(lower, "already running") {
			return fmt.Errorf("启动服务失败: %w", err)
		}
	}

	deadline := time.Now().Add(timeout)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return errors.New("等待服务启动超时")
		}
		st, err = s.Query()
		if err != nil {
			return err
		}
		if st.State == svc.Running {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func selfUpdateWorkerSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_BREAKAWAY_FROM_JOB | windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
	}
}

func windowsServiceStartType(v string) (uint32, error) {
	switch normalizeServiceStartType(v) {
	case ServiceStartTypeManual:
		return mgr.StartManual, nil
	case ServiceStartTypeDisabled:
		return mgr.StartDisabled, nil
	case ServiceStartTypeAutomatic:
		return mgr.StartAutomatic, nil
	default:
		return 0, fmt.Errorf("unsupported service start type: %s", v)
	}
}

func isServiceMissingError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "does not exist") || strings.Contains(msg, "service does not exist") || strings.Contains(msg, "cannot find")
}

func resolveNSSMPath(configured string, appDir string) (string, error) {
	if configured = strings.TrimSpace(configured); configured != "" {
		candidate := configured
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(appDir, filepath.FromSlash(candidate))
		}
		absPath, err := filepath.Abs(candidate)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(absPath); err != nil {
			return "", fmt.Errorf("nssm_exe_path 无法使用: %w", err)
		}
		return absPath, nil
	}
	defaultLocal := filepath.Join(appDir, "nssm.exe")
	if _, err := os.Stat(defaultLocal); err == nil {
		return defaultLocal, nil
	}
	path, err := exec.LookPath("nssm.exe")
	if err != nil {
		return "", errors.New("未找到 nssm.exe；请将 nssm.exe 放到程序目录，或在系统配置中填写相对/绝对 nssm_exe_path")
	}
	return path, nil
}

func runNSSMCommand(nssmPath string, args ...string) error {
	cmd := exec.Command(nssmPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			return fmt.Errorf("执行 NSSM 命令失败: %w", err)
		}
		return fmt.Errorf("执行 NSSM 命令失败: %w: %s", err, msg)
	}
	return nil
}

func nssmStartTypeValue(v string) string {
	switch normalizeServiceStartType(v) {
	case ServiceStartTypeManual:
		return "SERVICE_DEMAND_START"
	case ServiceStartTypeDisabled:
		return "SERVICE_DISABLED"
	default:
		return "SERVICE_AUTO_START"
	}
}
