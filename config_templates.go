package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"os"
	"strings"
	"time"
)

func defaultConfig() Config {
	return Config{
		ListenAddr:      ":8090",
		SessionCookie:   "updater_session",
		AuthKeySHA256:   sha256Hex("ChangeMe123!"),
		CurrentVersion:  "0.0.1",
		UploadDir:       "data/uploads",
		WorkDir:         "data/work",
		BackupDir:       "data/backups",
		DeploymentsFile: "data/deployments.json",
		LogFile:         "data/updater.log",
		ServiceName:     "YourServiceName",
		TargetDir:       "C:/YourApp",
		ReplaceMode:     ReplaceModeFull,
		BackupIgnore:    []string{"logs/", "temp/", "*.log"},
		ReplaceIgnore:   []string{"logs/", "temp/", "*.log"},
		MaxUploadMB:     1024,
	}
}

func loadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg := defaultConfig()
			raw, _ := json.MarshalIndent(cfg, "", "  ")
			if writeErr := os.WriteFile(path, raw, 0644); writeErr != nil {
				return Config{}, writeErr
			}
			return cfg, nil
		}
		return Config{}, err
	}
	cfg := defaultConfig()
	if len(b) > 0 {
		if err := json.Unmarshal(b, &cfg); err != nil {
			return Config{}, err
		}
	}
	if cfg.AuthKeySHA256 == "" {
		cfg.AuthKeySHA256 = sha256Hex("ChangeMe123!")
	}
	if strings.TrimSpace(cfg.CurrentVersion) == "" {
		cfg.CurrentVersion = "0.0.1"
	}
	if cfg.ReplaceIgnore == nil {
		cfg.ReplaceIgnore = []string{"logs/", "temp/", "*.log"}
	}
	cfg.ReplaceMode = normalizeReplaceMode(cfg.ReplaceMode)
	if cfg.MaxUploadMB <= 0 {
		cfg.MaxUploadMB = 1024
	}
	normalizeProjects(&cfg)
	return cfg, nil
}

func saveConfig(path string, cfg Config) error {
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func parseTemplates() (*template.Template, error) {
	funcs := template.FuncMap{
		"fmtTime": func(t time.Time) string {
			if t.IsZero() {
				return "-"
			}
			return t.Format("2006-01-02 15:04:05")
		},
		"fmtMaybeTime": func(t *time.Time) string {
			if t == nil || t.IsZero() {
				return "-"
			}
			return t.Format("2006-01-02 15:04:05")
		},
		"fmtMs": func(ms int64) string {
			if ms <= 0 {
				return "-"
			}
			return fmt.Sprintf("%d ms", ms)
		},
		"shortError": func(s string) string {
			if len(s) <= 60 {
				return s
			}
			return s[:60] + "..."
		},
		"statusClass": func(status string) string {
			switch status {
			case "success":
				return "text-emerald-700"
			case "failed":
				return "text-rose-700"
			case "deploying", "rollbacking", "queued":
				return "text-amber-700"
			default:
				return "text-slate-700"
			}
		},
		"changedSummary": func(ch []ChangedFile) string {
			if len(ch) == 0 {
				return "-"
			}
			added := 0
			updated := 0
			deleted := 0
			for _, c := range ch {
				switch c.Action {
				case "added":
					added++
				case "updated":
					updated++
				case "deleted":
					deleted++
				}
			}
			return fmt.Sprintf("新增:%d 更新:%d 删除:%d", added, updated, deleted)
		},
		"fmtBytes": func(size int64) string {
			if size < 0 {
				return "-"
			}
			const unit = 1024
			if size < unit {
				return fmt.Sprintf("%d B", size)
			}
			div, exp := int64(unit), 0
			for n := size / unit; n >= unit; n /= unit {
				div *= unit
				exp++
			}
			return fmt.Sprintf("%.1f %ciB", float64(size)/float64(div), "KMGTPE"[exp])
		},
	}
	return template.New("pages").Funcs(funcs).ParseFS(webAssets, "web/templates/*.html")
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func isKeyMatch(expectedHash, input string) bool {
	want := strings.ToLower(strings.TrimSpace(expectedHash))
	got := strings.ToLower(sha256Hex(input))
	if len(want) != len(got) {
		return false
	}
	matched := byte(1)
	for i := 0; i < len(want); i++ {
		if want[i] != got[i] {
			matched = 0
		}
	}
	return matched == 1
}
