# Repository Guidelines

## Project Structure & Module Organization
This project is a Go-based Windows remote updater with embedded web UI.

- Root Go services and runtime logic:
  - `main.go` (HTTP routes, config APIs, deployment APIs)
  - `deployment_runtime.go` (deploy/rollback workflow)
  - `file_ops.go` (zip/extract/sync/ignore rules)
  - `store_sessions_events.go` (deployment store, sessions, SSE hub)
  - `projects.go`, `versioning.go`, `app_types.go` (domain models/helpers)
- Platform-specific service control:
  - `service_windows.go` (Windows service manager)
  - `service_stub.go` (non-Windows stub)
- Web assets:
  - `web/templates/*.html` (server-rendered views/partials)
  - `web/static/*` (frontend JS/CSS)
- Runtime files:
  - `config.json`, `data/` (uploads, backups, logs, deployments)

## Build, Test, and Development Commands
- `go run .` — run the updater locally.
- `go build -o updater.exe .` — build Windows executable.
- `go build -trimpath -ldflags="-s -w" -o updater.exe .` — release-oriented build (smaller binary).
- `go test ./...` — run all Go tests.
- `gofmt -w .` — format Go code.
- `node --check web/static/app.js` — quick JS syntax check for frontend changes.

## Coding Style & Naming Conventions
- Follow standard Go formatting (`gofmt`) and idioms.
- Keep packages/files focused by responsibility (routing, runtime, storage, file ops).
- Use descriptive camelCase for local variables; exported types/functions use PascalCase.
- Template IDs and API field names should remain stable to avoid frontend/backend drift.

## Testing Guidelines
- Place tests as `*_test.go` beside related Go files.
- Prefer table-driven tests for parsing, versioning, and ignore-rule matching.
- For UI/API changes, manually verify:
  - login/config save
  - upload -> auto preview -> confirm deployment flow
  - full/partial replace behavior
  - replace-ignore and ignored-path visibility in change dialog
  - multi-project concurrency (different projects should not block each other)
  - self-update workflow
  - rollback flow
  - deployment list pagination/lazy loading

## Commit & Pull Request Guidelines
- Use Conventional Commits (observed history): `feat:`, `fix:`, `refactor:`, `chore:`.
  - Example: `feat: add paginated lazy loading for deployments`
- PRs should include:
  - concise change summary
  - risk/rollback notes
  - config impact (if `config.json` fields changed)
  - screenshots/GIFs for UI updates

## Security & Configuration Tips
- Do not commit real secrets, production keys, or runtime artifacts (`data/`, `updater.exe`).
- Store only SHA-256 key hashes in config (`auth_key_sha256`), never plaintext keys.
- Validate ignore rules carefully; wrong patterns may skip required files during deploy/rollback.
- Any new `config.json` field must have a backward-compatible default value in code.
- Any `projects[]` schema change must update both `README.md` and this guide in the same PR.

## Runtime Artifacts
- Never commit runtime/generated files unless explicitly requested:
  - `config.json`
  - `data/`
  - `.vs/`
  - `updater.exe`
  - `updater.exe~`

## Push Workflow (User Shortcut)
- If the user says `推送`, execute this workflow by default:
  - summarize what features/fixes were updated in this round;
  - run quality checks: `go test ./...` and `node --check web/static/app.js`;
  - build executable with `go build -trimpath -ldflags="-s -w" -o updater.exe .`;
  - commit code changes with a clear Conventional Commit message;
  - push to GitHub remote branch (normally `origin/main`);
  - create/update GitHub Release with title format `SimpleRemoteUpdate <version>` (for example: `SimpleRemoteUpdate v0.1.2`), upload `updater.exe`, and include clear release notes.
- Mandatory release rule: when pushing, Release name must be `SimpleRemoteUpdate + 版本号`, and the Release notes must clearly describe all updated features/fixes in this push; do not use vague notes.
- Unless the user explicitly asks, do not commit runtime data (`data/`, `config.json`) with the push.

## Release Notes Template
- Use this structure for each release note:
  - `新增`: new capabilities
  - `修复`: bug fixes
  - `影响范围`: API/UI/config/runtime impact
  - `回滚说明`: how to revert safely if needed
