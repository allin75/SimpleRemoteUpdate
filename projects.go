package main

import (
	"fmt"
	"strings"
)

func normalizeProjects(cfg *Config) {
	if len(cfg.Projects) == 0 {
		cfg.Projects = []ManagedProject{{
			ID:             "default",
			Name:           firstNonEmpty(strings.TrimSpace(cfg.ServiceName), "默认程序"),
			ServiceName:    strings.TrimSpace(cfg.ServiceName),
			TargetDir:      strings.TrimSpace(cfg.TargetDir),
			CurrentVersion: firstNonEmpty(strings.TrimSpace(cfg.CurrentVersion), "0.0.1"),
			BackupIgnore:   append([]string{}, cfg.BackupIgnore...),
			ReplaceIgnore:  append([]string{}, cfg.ReplaceIgnore...),
			MaxUploadMB:    cfg.MaxUploadMB,
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
		p.TargetDir = strings.TrimSpace(p.TargetDir)
		p.CurrentVersion = firstNonEmpty(strings.TrimSpace(p.CurrentVersion), "0.0.1")
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
			ID:             "default",
			Name:           "默认程序",
			ServiceName:    strings.TrimSpace(cfg.ServiceName),
			TargetDir:      strings.TrimSpace(cfg.TargetDir),
			CurrentVersion: "0.0.1",
			BackupIgnore:   append([]string{}, cfg.BackupIgnore...),
			ReplaceIgnore:  append([]string{}, cfg.ReplaceIgnore...),
			MaxUploadMB:    firstInt64(cfg.MaxUploadMB, 1024),
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
		ID:             "default",
		Name:           "默认程序",
		ServiceName:    cfg.ServiceName,
		TargetDir:      cfg.TargetDir,
		CurrentVersion: firstNonEmpty(cfg.CurrentVersion, "0.0.1"),
		BackupIgnore:   append([]string{}, cfg.BackupIgnore...),
		ReplaceIgnore:  append([]string{}, cfg.ReplaceIgnore...),
		MaxUploadMB:    firstInt64(cfg.MaxUploadMB, 1024),
	}
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
