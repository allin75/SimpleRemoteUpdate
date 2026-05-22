package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeProjectsKeepsBusinessServiceConfigWithReverseProxy(t *testing.T) {
	cfg := Config{
		ReplaceMode:      ReplaceModeFull,
		MaxUploadMB:      256,
		DefaultProjectID: "proxy",
		Projects: []ManagedProject{
			{
				ID:                  "proxy",
				Name:                "业务程序",
				TargetDir:           "C:/Proxy",
				CurrentVersion:      "1.0.0",
				DefaultReplaceMode:  ReplaceModeFull,
				ServiceName:         "business-service",
				ServiceInstallMode:  ServiceInstallModeNSSM,
				ServiceExePath:      "MyBusiness.exe",
				ServiceArgs:         []string{"--port", "8080"},
				ReverseProxyEnabled: true,
				ReverseProxyBindIP:  "",
				ReverseProxyRules: []ReverseProxyRule{
					{
						Protocol:   ReverseProxyProtocolTCP,
						ListenPort: 15432,
						RemoteHost: "192.168.10.20",
						RemotePort: 5432,
					},
				},
			},
		},
	}

	normalizeProjects(&cfg)
	project := cfg.Projects[0]
	if project.ServiceName != "business-service" {
		t.Fatalf("expected service name preserved, got %q", project.ServiceName)
	}
	if project.ServiceInstallMode != ServiceInstallModeNSSM {
		t.Fatalf("expected install mode preserved, got %q", project.ServiceInstallMode)
	}
	if project.ServiceExePath != "MyBusiness.exe" {
		t.Fatalf("expected business exe preserved, got %q", project.ServiceExePath)
	}
	if len(project.ServiceArgs) != 2 || project.ServiceArgs[0] != "--port" || project.ServiceArgs[1] != "8080" {
		t.Fatalf("unexpected service args: %#v", project.ServiceArgs)
	}
	if project.ReverseProxyBindIP != defaultReverseProxyBindIP() {
		t.Fatalf("expected default bind ip, got %q", project.ReverseProxyBindIP)
	}
}

func TestValidateRuntimeConfigAllowsReverseProxyOnBusinessProject(t *testing.T) {
	cfg := Config{
		ListenAddr:       ":12333",
		SessionCookie:    "updater_session",
		DefaultProjectID: "proxy",
		TargetDir:        "E:/Target",
		UploadDir:        "data/uploads",
		WorkDir:          "data/work",
		BackupDir:        "data/backups",
		DeploymentsFile:  "data/deployments.json",
		LogFile:          "data/updater.log",
		MaxUploadMB:      128,
		Projects: []ManagedProject{
			{
				ID:                  "proxy",
				Name:                "业务程序",
				TargetDir:           "E:/Target",
				CurrentVersion:      "1.0.0",
				DefaultReplaceMode:  ReplaceModeFull,
				ServiceName:         "business-service",
				ServiceInstallMode:  ServiceInstallModeNSSM,
				ServiceExePath:      "MyBusiness.exe",
				ReverseProxyEnabled: true,
				ReverseProxyBindIP:  "0.0.0.0",
				ReverseProxyRules: []ReverseProxyRule{
					{
						Protocol:   ReverseProxyProtocolTCP,
						ListenPort: 18080,
						RemoteHost: "192.168.1.10",
						RemotePort: 8080,
					},
				},
				MaxUploadMB: 128,
			},
		},
	}

	if err := validateRuntimeConfig(cfg); err != nil {
		t.Fatalf("expected config valid, got %v", err)
	}
}

func TestValidateRuntimeConfigRequiresCronWhenTimedRestartEnabled(t *testing.T) {
	cfg := Config{
		ListenAddr:       ":12333",
		SessionCookie:    "updater_session",
		DefaultProjectID: "proxy",
		TargetDir:        "E:/Target",
		UploadDir:        "data/uploads",
		WorkDir:          "data/work",
		BackupDir:        "data/backups",
		DeploymentsFile:  "data/deployments.json",
		LogFile:          "data/updater.log",
		MaxUploadMB:      128,
		Projects: []ManagedProject{
			{
				ID:                    "proxy",
				Name:                  "业务程序",
				ServiceName:           "business-service",
				ServiceRestartEnabled: true,
				ServiceRestartCron:    "",
				TargetDir:             "E:/Target",
				CurrentVersion:        "1.0.0",
				DefaultReplaceMode:    ReplaceModeFull,
				MaxUploadMB:           128,
			},
		},
	}

	if err := validateRuntimeConfig(cfg); err == nil {
		t.Fatal("expected invalid config when timed restart is enabled without cron")
	}
}

func TestValidateServiceRestartSpecAcceptsCronInterval(t *testing.T) {
	if err := validateServiceRestartSpec("*/30 * * * *"); err != nil {
		t.Fatalf("expected cron interval to be valid, got %v", err)
	}
	if err := validateServiceRestartSpec("@every 30m"); err != nil {
		t.Fatalf("expected descriptor interval to be valid, got %v", err)
	}
}

func TestNormalizeProjectsMigratesLegacyRestartTimeToCron(t *testing.T) {
	cfg := Config{
		ReplaceMode:      ReplaceModeFull,
		MaxUploadMB:      256,
		DefaultProjectID: "proxy",
		Projects: []ManagedProject{
			{
				ID:                    "proxy",
				Name:                  "业务程序",
				ServiceName:           "business-service",
				ServiceRestartEnabled: true,
				ServiceRestartTime:    "22:15",
				TargetDir:             "C:/Proxy",
				CurrentVersion:        "1.0.0",
				DefaultReplaceMode:    ReplaceModeFull,
			},
		},
	}

	normalizeProjects(&cfg)
	project := cfg.Projects[0]
	if project.ServiceRestartCron != "15 22 * * *" {
		t.Fatalf("expected legacy time migrated to cron, got %q", project.ServiceRestartCron)
	}
	if project.ServiceRestartTime != "" {
		t.Fatalf("expected legacy time cleared after migration, got %q", project.ServiceRestartTime)
	}
}

func TestReverseProxyProjectSignatureTracksRuleChanges(t *testing.T) {
	base := ManagedProject{
		ID:                  "proxy",
		ReverseProxyEnabled: true,
		ReverseProxyBindIP:  "0.0.0.0",
		ReverseProxyRules: []ReverseProxyRule{
			{
				Protocol:   ReverseProxyProtocolTCP,
				ListenPort: 15432,
				RemoteHost: "192.168.10.20",
				RemotePort: 5432,
			},
		},
	}

	changed := base
	changed.ReverseProxyRules = []ReverseProxyRule{
		{
			Protocol:   ReverseProxyProtocolTCP,
			ListenPort: 15432,
			RemoteHost: "192.168.10.20",
			RemotePort: 6432,
		},
	}

	if reverseProxyProjectSignature(base) == reverseProxyProjectSignature(changed) {
		t.Fatal("expected signature to change when reverse proxy rules change")
	}
	if reverseProxyListenAddress("", 15432) != "0.0.0.0:15432" {
		t.Fatalf("unexpected listen address: %s", reverseProxyListenAddress("", 15432))
	}
}

func TestResolveRuntimeLogDirRejectsPathsOutsideTarget(t *testing.T) {
	targetDir := t.TempDir()
	project := ManagedProject{
		ID:            "app",
		Name:          "业务程序",
		TargetDir:     targetDir,
		CurrentVersion: "1.0.0",
		MaxUploadMB:   128,
		RuntimeLogDir: "../outside",
	}

	if _, _, err := resolveRuntimeLogDir(project); err == nil {
		t.Fatal("expected runtime log dir outside target to be rejected")
	}
}

func TestDiscoverRuntimeLogCandidatesRanksLogsDirectoryFirst(t *testing.T) {
	targetDir := t.TempDir()
	logsDir := filepath.Join(targetDir, "logs")
	traceDir := filepath.Join(targetDir, "runtime-trace")
	otherDir := filepath.Join(targetDir, "data")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}
	if err := os.MkdirAll(traceDir, 0755); err != nil {
		t.Fatalf("mkdir trace: %v", err)
	}
	if err := os.MkdirAll(otherDir, 0755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	if err := os.WriteFile(filepath.Join(logsDir, "app.log"), []byte("line-1\nline-2\n"), 0644); err != nil {
		t.Fatalf("write app.log: %v", err)
	}
	if err := os.WriteFile(filepath.Join(logsDir, "error.log.1"), []byte("rotate\n"), 0644); err != nil {
		t.Fatalf("write error.log.1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(traceDir, "trace.out"), []byte("trace\n"), 0644); err != nil {
		t.Fatalf("write trace.out: %v", err)
	}
	if err := os.WriteFile(filepath.Join(otherDir, "notes.txt"), []byte("not log\n"), 0644); err != nil {
		t.Fatalf("write notes.txt: %v", err)
	}

	project := ManagedProject{
		ID:             "app",
		Name:           "业务程序",
		TargetDir:      targetDir,
		CurrentVersion: "1.0.0",
		MaxUploadMB:    128,
	}
	candidates, err := discoverRuntimeLogCandidates(project)
	if err != nil {
		t.Fatalf("discover candidates: %v", err)
	}
	if len(candidates) == 0 {
		t.Fatal("expected candidates discovered")
	}
	if candidates[0].RelPath != "logs" {
		t.Fatalf("expected logs to rank first, got %#v", candidates[0])
	}
}

func TestReadRuntimeLogChunkReturnsLastLinesAndCursor(t *testing.T) {
	targetDir := t.TempDir()
	logPath := filepath.Join(targetDir, "app.log")
	var builder strings.Builder
	for i := 1; i <= 600; i++ {
		builder.WriteString(fmt.Sprintf("line-%03d\n", i))
	}
	if err := os.WriteFile(logPath, []byte(builder.String()), 0644); err != nil {
		t.Fatalf("write log file: %v", err)
	}

	chunk, err := readRuntimeLogChunk(logPath, -1, 200)
	if err != nil {
		t.Fatalf("read tail chunk: %v", err)
	}
	if !strings.Contains(chunk.Content, "line-600") {
		t.Fatalf("expected latest line in content, got %q", chunk.Content)
	}
	if strings.Contains(chunk.Content, "line-001") {
		t.Fatalf("expected chunk to be tailed, got full file")
	}
	if !chunk.HasMore || chunk.NextCursor <= 0 {
		t.Fatalf("expected more content before current chunk, got %#v", chunk)
	}

	older, err := readRuntimeLogChunk(logPath, chunk.NextCursor, 200)
	if err != nil {
		t.Fatalf("read older chunk: %v", err)
	}
	if !strings.Contains(older.Content, "line-400") {
		t.Fatalf("expected older chunk to contain prior lines, got %q", older.Content)
	}
	if strings.Contains(older.Content, "line-600") {
		t.Fatalf("expected older chunk not to overlap latest lines")
	}
}

func TestIsRuntimeLogDownloadAllowedUses50MBThreshold(t *testing.T) {
	if !isRuntimeLogDownloadAllowed(49 * 1024 * 1024) {
		t.Fatal("expected 49MB log to be downloadable")
	}
	if !isRuntimeLogDownloadAllowed(50 * 1024 * 1024) {
		t.Fatal("expected 50MB log to be downloadable")
	}
	if isRuntimeLogDownloadAllowed(50*1024*1024 + 1) {
		t.Fatal("expected log larger than 50MB to be blocked")
	}
}

func TestResolveRuntimeLogFileAcceptsFileNameAndDirPrefixedPath(t *testing.T) {
	targetDir := t.TempDir()
	logsDir := filepath.Join(targetDir, "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}
	logPath := filepath.Join(logsDir, "2026-03-06.log")
	if err := os.WriteFile(logPath, []byte("ok\n"), 0644); err != nil {
		t.Fatalf("write log file: %v", err)
	}

	project := ManagedProject{
		ID:             "app",
		Name:           "业务程序",
		TargetDir:      targetDir,
		CurrentVersion: "1.0.0",
		MaxUploadMB:    128,
	}

	dirPath, relDir, err := resolveRuntimeLogDirectoryByToken(project, "logs")
	if err != nil {
		t.Fatalf("resolve dir: %v", err)
	}

	for _, token := range []string{"2026-03-06.log", "logs/2026-03-06.log"} {
		gotPath, relFile, info, err := resolveRuntimeLogFile(project, dirPath, relDir, token)
		if err != nil {
			t.Fatalf("resolve file for %q: %v", token, err)
		}
		if gotPath != logPath {
			t.Fatalf("expected %q, got %q", logPath, gotPath)
		}
		if relFile != "logs/2026-03-06.log" {
			t.Fatalf("unexpected rel file for %q: %q", token, relFile)
		}
		if info == nil || info.IsDir() {
			t.Fatalf("expected file info for %q", token)
		}
	}
}

func TestSearchRuntimeLogFileFindsMatchesCaseInsensitive(t *testing.T) {
	targetDir := t.TempDir()
	logPath := filepath.Join(targetDir, "app.log")
	content := strings.Join([]string{
		"2026-03-06 INFO startup complete",
		"2026-03-06 WARN database reconnect",
		"2026-03-06 ERROR token expired",
		"2026-03-06 error api timeout",
	}, "\n") + "\n"
	if err := os.WriteFile(logPath, []byte(content), 0644); err != nil {
		t.Fatalf("write log file: %v", err)
	}

	result, err := searchRuntimeLogFile(logPath, "error", 50)
	if err != nil {
		t.Fatalf("search runtime log file: %v", err)
	}
	if result.Total != 2 {
		t.Fatalf("expected 2 matches, got %d", result.Total)
	}
	if len(result.Matches) != 2 {
		t.Fatalf("expected 2 returned matches, got %d", len(result.Matches))
	}
	if result.Matches[0].LineNumber != 3 {
		t.Fatalf("expected first match on line 3, got %d", result.Matches[0].LineNumber)
	}
	if !strings.Contains(strings.ToLower(result.Matches[1].LineText), "error api timeout") {
		t.Fatalf("unexpected second match: %#v", result.Matches[1])
	}
}
