package main

import (
	"embed"
	"html/template"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

//go:embed web/templates/*.html web/static/*
var webAssets embed.FS

type Config struct {
	ListenAddr       string           `json:"listen_addr"`
	SessionCookie    string           `json:"session_cookie"`
	AuthKeySHA256    string           `json:"auth_key_sha256"`
	CurrentVersion   string           `json:"current_version"`
	DefaultProjectID string           `json:"default_project_id"`
	Projects         []ManagedProject `json:"projects"`
	UploadDir        string           `json:"upload_dir"`
	WorkDir          string           `json:"work_dir"`
	BackupDir        string           `json:"backup_dir"`
	DeploymentsFile  string           `json:"deployments_file"`
	LogFile          string           `json:"log_file"`
	ServiceName      string           `json:"service_name"`
	TargetDir        string           `json:"target_dir"`
	ReplaceMode      string           `json:"replace_mode"`
	BackupIgnore     []string         `json:"backup_ignore"`
	ReplaceIgnore    []string         `json:"replace_ignore"`
	MaxUploadMB      int64            `json:"max_upload_mb"`
}

type ManagedProject struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	ServiceName        string   `json:"service_name"`
	TargetDir          string   `json:"target_dir"`
	CurrentVersion     string   `json:"current_version"`
	DefaultReplaceMode string   `json:"default_replace_mode"`
	BackupIgnore       []string `json:"backup_ignore"`
	ReplaceIgnore      []string `json:"replace_ignore"`
	MaxUploadMB        int64    `json:"max_upload_mb"`
}

type ChangedFile struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Size   int64  `json:"size"`
}

type Deployment struct {
	ID            string        `json:"id"`
	Type          string        `json:"type"`
	RollbackOf    string        `json:"rollback_of,omitempty"`
	Version       string        `json:"version,omitempty"`
	ProjectID     string        `json:"project_id,omitempty"`
	ProjectName   string        `json:"project_name,omitempty"`
	ReplaceMode   string        `json:"replace_mode,omitempty"`
	ReplaceIgnore []string      `json:"replace_ignore,omitempty"`
	BackupIgnore  []string      `json:"backup_ignore,omitempty"`
	Status        string        `json:"status"`
	Note          string        `json:"note"`
	LoginIP       string        `json:"login_ip"`
	CreatedAt     time.Time     `json:"created_at"`
	StartedAt     time.Time     `json:"started_at"`
	FinishedAt    *time.Time    `json:"finished_at,omitempty"`
	DurationMs    int64         `json:"duration_ms"`
	UploadFile    string        `json:"upload_file,omitempty"`
	BackupFile    string        `json:"backup_file,omitempty"`
	Changed       []ChangedFile `json:"changed"`
	Error         string        `json:"error,omitempty"`
	ServiceName   string        `json:"service_name"`
	TargetDir     string        `json:"target_dir"`
}

const (
	ReplaceModeFull    = "full"
	ReplaceModePartial = "partial"
)

type Event struct {
	Time  string `json:"time"`
	Level string `json:"level"`
	Text  string `json:"text"`
}

type IgnoreMatcher struct {
	patterns []string
}

type App struct {
	cfg       Config
	cfgPath   string
	cfgMu     sync.RWMutex
	logWriter *dynamicLogWriter
	logger    *slog.Logger
	templates *template.Template
	store     *deploymentStore
	sessions  *sessionManager
	events    *eventHub
	static    http.Handler
	deploying int32
}
