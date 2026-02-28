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
  - upload + deployment flow
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

## Push Workflow (User Shortcut)
- If the user says `推送`, execute this workflow by default:
  - summarize what features/fixes were updated in this round;
  - build executable with `go build -o updater.exe .`;
  - commit code changes with a clear Conventional Commit message;
  - push to GitHub remote branch (normally `origin/main`);
  - create/update GitHub Release with title format `SimpleRemoteUpdate <version>` (for example: `SimpleRemoteUpdate v0.1.2`) and include clear release notes about updated features/fixes.
- Unless the user explicitly asks, do not commit runtime data (`data/`, `config.json`) with the push.
