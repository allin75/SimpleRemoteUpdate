package main

import "testing"

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
