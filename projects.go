package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

var serviceRestartScheduleParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

func normalizeProjects(cfg *Config) {
	if len(cfg.Projects) == 0 {
		cfg.Projects = []ManagedProject{{
			ID:                    "default",
			Name:                  firstNonEmpty(strings.TrimSpace(cfg.ServiceName), "默认程序"),
			ServiceName:           strings.TrimSpace(cfg.ServiceName),
			ServiceRestartEnabled: false,
			ServiceRestartCron:    "",
			ServiceRestartTime:    "",
			TargetDir:             strings.TrimSpace(cfg.TargetDir),
			CurrentVersion:        firstNonEmpty(strings.TrimSpace(cfg.CurrentVersion), "0.0.1"),
			DefaultReplaceMode:    normalizeReplaceMode(cfg.ReplaceMode),
			AllowInitialDeploy:    false,
			ServiceInstallMode:    normalizeServiceInstallMode(""),
			ServiceExePath:        "",
			ServiceArgs:           nil,
			ServiceDisplayName:    "",
			ServiceDescription:    "",
			ServiceStartType:      normalizeServiceStartType(""),
			ReverseProxyEnabled:   false,
			ReverseProxyBindIP:    defaultReverseProxyBindIP(),
			ReverseProxyRules:     nil,
			RuntimeLogDir:         "",
			BackupIgnore:          append([]string{}, cfg.BackupIgnore...),
			ReplaceIgnore:         append([]string{}, cfg.ReplaceIgnore...),
			MaxUploadMB:           cfg.MaxUploadMB,
		}}
	}

	seen := make(map[string]struct{}, len(cfg.Projects))
	out := make([]ManagedProject, 0, len(cfg.Projects))
	for i, p := range cfg.Projects {
		p.ID = strings.TrimSpace(p.ID)
		if p.ID == "" {
			p.ID = fmt.Sprintf("project-%d", i+1)
		}
		if _, exists := seen[p.ID]; exists {
			continue
		}
		seen[p.ID] = struct{}{}

		p.Name = firstNonEmpty(strings.TrimSpace(p.Name), p.ID)
		p.ServiceName = strings.TrimSpace(p.ServiceName)
		p.ServiceRestartCron = normalizeServiceRestartCron(p.ServiceRestartCron)
		p.ServiceRestartTime = normalizeLegacyServiceRestartTime(p.ServiceRestartTime)
		if p.ServiceRestartCron == "" && p.ServiceRestartTime != "" {
			p.ServiceRestartCron = legacyServiceRestartTimeToCron(p.ServiceRestartTime)
			p.ServiceRestartTime = ""
		}
		p.TargetDir = strings.TrimSpace(p.TargetDir)
		p.CurrentVersion = firstNonEmpty(strings.TrimSpace(p.CurrentVersion), "0.0.1")
		mode := strings.TrimSpace(p.DefaultReplaceMode)
		if mode == "" {
			mode = cfg.ReplaceMode
		}
		p.DefaultReplaceMode = normalizeReplaceMode(mode)
		p.ServiceInstallMode = normalizeServiceInstallMode(p.ServiceInstallMode)
		p.ServiceExePath = strings.TrimSpace(p.ServiceExePath)
		p.ServiceArgs = normalizeServiceArgs(p.ServiceArgs)
		p.ServiceDisplayName = strings.TrimSpace(p.ServiceDisplayName)
		p.ServiceDescription = strings.TrimSpace(p.ServiceDescription)
		p.ServiceStartType = normalizeServiceStartType(p.ServiceStartType)
		p.ReverseProxyBindIP = normalizeReverseProxyBindIP(p.ReverseProxyBindIP)
		p.ReverseProxyRules = normalizeReverseProxyRules(p.ReverseProxyRules)
		p.RuntimeLogDir = strings.TrimSpace(p.RuntimeLogDir)
		applyReverseProxyDefaults(&p)
		if p.MaxUploadMB <= 0 {
			p.MaxUploadMB = cfg.MaxUploadMB
		}
		if p.MaxUploadMB <= 0 {
			p.MaxUploadMB = 1024
		}
		if p.BackupIgnore == nil {
			p.BackupIgnore = append([]string{}, cfg.BackupIgnore...)
		}
		if p.ReplaceIgnore == nil {
			p.ReplaceIgnore = append([]string{}, cfg.ReplaceIgnore...)
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		out = []ManagedProject{{
			ID:                    "default",
			Name:                  "默认程序",
			ServiceName:           strings.TrimSpace(cfg.ServiceName),
			ServiceRestartEnabled: false,
			ServiceRestartCron:    "",
			ServiceRestartTime:    "",
			TargetDir:             strings.TrimSpace(cfg.TargetDir),
			CurrentVersion:        "0.0.1",
			DefaultReplaceMode:    normalizeReplaceMode(cfg.ReplaceMode),
			AllowInitialDeploy:    false,
			ServiceInstallMode:    normalizeServiceInstallMode(""),
			ServiceExePath:        "",
			ServiceArgs:           nil,
			ServiceDisplayName:    "",
			ServiceDescription:    "",
			ServiceStartType:      normalizeServiceStartType(""),
			ReverseProxyEnabled:   false,
			ReverseProxyBindIP:    defaultReverseProxyBindIP(),
			ReverseProxyRules:     nil,
			RuntimeLogDir:         "",
			BackupIgnore:          append([]string{}, cfg.BackupIgnore...),
			ReplaceIgnore:         append([]string{}, cfg.ReplaceIgnore...),
			MaxUploadMB:           firstInt64(cfg.MaxUploadMB, 1024),
		}}
	}
	cfg.Projects = out

	cfg.DefaultProjectID = strings.TrimSpace(cfg.DefaultProjectID)
	if cfg.DefaultProjectID == "" {
		cfg.DefaultProjectID = cfg.Projects[0].ID
	}
	if _, ok := findProjectByID(cfg.Projects, cfg.DefaultProjectID); !ok {
		cfg.DefaultProjectID = cfg.Projects[0].ID
	}
	dp := getDefaultProject(*cfg)
	cfg.ServiceName = dp.ServiceName
	cfg.TargetDir = dp.TargetDir
	cfg.CurrentVersion = dp.CurrentVersion
	cfg.ReplaceMode = dp.DefaultReplaceMode
	cfg.BackupIgnore = append([]string{}, dp.BackupIgnore...)
	cfg.ReplaceIgnore = append([]string{}, dp.ReplaceIgnore...)
	cfg.MaxUploadMB = dp.MaxUploadMB
}

func findProjectByID(projects []ManagedProject, id string) (ManagedProject, bool) {
	for _, p := range projects {
		if p.ID == id {
			return p, true
		}
	}
	return ManagedProject{}, false
}

func getDefaultProject(cfg Config) ManagedProject {
	if p, ok := findProjectByID(cfg.Projects, cfg.DefaultProjectID); ok {
		return p
	}
	if len(cfg.Projects) > 0 {
		return cfg.Projects[0]
	}
	return ManagedProject{
		ID:                    "default",
		Name:                  "默认程序",
		ServiceName:           cfg.ServiceName,
		ServiceRestartEnabled: false,
		ServiceRestartCron:    "",
		ServiceRestartTime:    "",
		TargetDir:             cfg.TargetDir,
		CurrentVersion:        firstNonEmpty(cfg.CurrentVersion, "0.0.1"),
		DefaultReplaceMode:    normalizeReplaceMode(cfg.ReplaceMode),
		AllowInitialDeploy:    false,
		ServiceInstallMode:    normalizeServiceInstallMode(""),
		ServiceExePath:        "",
		ServiceArgs:           nil,
		ServiceDisplayName:    "",
		ServiceDescription:    "",
		ServiceStartType:      normalizeServiceStartType(""),
		ReverseProxyEnabled:   false,
		ReverseProxyBindIP:    defaultReverseProxyBindIP(),
		ReverseProxyRules:     nil,
		RuntimeLogDir:         "",
		BackupIgnore:          append([]string{}, cfg.BackupIgnore...),
		ReplaceIgnore:         append([]string{}, cfg.ReplaceIgnore...),
		MaxUploadMB:           firstInt64(cfg.MaxUploadMB, 1024),
	}
}

func normalizeReplaceMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case ReplaceModePartial:
		return ReplaceModePartial
	default:
		return ReplaceModeFull
	}
}

func normalizeDeployEntry(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case DeployEntryInitial:
		return DeployEntryInitial
	default:
		return DeployEntryStandard
	}
}

func normalizeServiceInstallMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case ServiceInstallModeWindows:
		return ServiceInstallModeWindows
	case ServiceInstallModeNSSM:
		return ServiceInstallModeNSSM
	default:
		return ServiceInstallModeNone
	}
}

func normalizeServiceStartType(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case ServiceStartTypeManual:
		return ServiceStartTypeManual
	case ServiceStartTypeDisabled:
		return ServiceStartTypeDisabled
	default:
		return ServiceStartTypeAutomatic
	}
}

func normalizeServiceRestartCron(v string) string {
	return strings.TrimSpace(v)
}

func normalizeLegacyServiceRestartTime(v string) string {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return ""
	}
	if _, err := time.Parse("15:04", trimmed); err != nil {
		return ""
	}
	return trimmed
}

func legacyServiceRestartTimeToCron(v string) string {
	trimmed := normalizeLegacyServiceRestartTime(v)
	if trimmed == "" {
		return ""
	}
	parsed, err := time.Parse("15:04", trimmed)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d %d * * *", parsed.Minute(), parsed.Hour())
}

func effectiveServiceRestartSpec(project ManagedProject) string {
	if spec := normalizeServiceRestartCron(project.ServiceRestartCron); spec != "" {
		return spec
	}
	return legacyServiceRestartTimeToCron(project.ServiceRestartTime)
}

func validateServiceRestartSpec(spec string) error {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return fmt.Errorf("service_restart_cron 不能为空")
	}
	if _, err := serviceRestartScheduleParser.Parse(spec); err != nil {
		return fmt.Errorf("service_restart_cron 格式错误: %w", err)
	}
	return nil
}

func normalizeServiceArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" {
			continue
		}
		out = append(out, arg)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func defaultReverseProxyBindIP() string {
	return "0.0.0.0"
}

func normalizeReverseProxyBindIP(v string) string {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return defaultReverseProxyBindIP()
	}
	return trimmed
}

func normalizeReverseProxyProtocol(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case ReverseProxyProtocolUDP:
		return ReverseProxyProtocolUDP
	default:
		return ReverseProxyProtocolTCP
	}
}

func normalizeReverseProxyRules(rules []ReverseProxyRule) []ReverseProxyRule {
	out := make([]ReverseProxyRule, 0, len(rules))
	for _, rule := range rules {
		rule.Name = strings.TrimSpace(rule.Name)
		rule.Protocol = normalizeReverseProxyProtocol(rule.Protocol)
		rule.RemoteHost = strings.TrimSpace(rule.RemoteHost)
		if rule.ListenPort <= 0 || rule.RemotePort <= 0 || rule.RemoteHost == "" {
			continue
		}
		out = append(out, rule)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func applyReverseProxyDefaults(p *ManagedProject) {
	_ = p
}

func firstNonEmpty(v string, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func firstInt64(v, fallback int64) int64 {
	if v <= 0 {
		return fallback
	}
	return v
}
