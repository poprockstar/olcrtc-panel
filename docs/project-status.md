# OlcRTC Panel Project Status

Last updated: 2026-05-27

## Architecture Approval

- Architecture document: `docs/architecture/olcrtc-panel-architecture.md`
- Approved architecture version/date: v1 baseline, approved 2026-05-27
- Phase 0 review result: approved with implementation guardrails below
- Canonical scope: v1 is local VPS/local-node only
- Production runtime target: Linux servers, with Debian/Ubuntu native systemd deployment as the primary path
- Multi-node status: schema-reserved only; no API or UI controls may expose unfinished multi-node behavior

## Phase State

- Current phase: Phase 4 - Clients And Locations Domain API
- Completed phases:
  - Phase 0 - Architecture Approval And Project Tracking
  - Phase 1 - Repository Skeleton And Build Pipeline
  - Phase 2 - Storage, Migrations, And Settings
  - Phase 3 - Auth And Security Baseline
- Next session target: start Phase 4 client and location domain APIs

## Implemented Capabilities

- Architecture baseline reviewed and approved.
- Project status tracking file created.
- Development phase plan created at `docs/development/phase-plan.md`.
- Architecture compliance checklist established for every phase.
- Rule established: no feature phase is accepted unless this status file and tests are updated.
- Go module initialized as `olcpanel`.
- Backend CLI skeleton added at `cmd/olcpanel` with `olcpanel serve`.
- Minimal config loading added with default bind `127.0.0.1:8888` and `OLCPANEL_BIND` override.
- HTTP server added with `GET /api/v1/state` and embedded UI serving at `/`.
- React + TypeScript + Vite frontend skeleton added under `frontend/`.
- Frontend production build outputs to `internal/webui/dist` and is embedded into the Go binary.
- `Makefile` added with `build`, `backend`, `frontend`, `linux`, and `test` targets.
- `Makefile` `backend` and `linux` targets now create `bin/` before writing build outputs.
- README added with Linux server target and Phase 1 build/run commands.
- CLI now rejects unexpected positional arguments after `olcpanel serve` flags.
- Pure-Go SQLite storage added with `modernc.org/sqlite` pinned to a Go 1.23-compatible version.
- Database URL config added with default `sqlite:///etc/olcpanel/panel.db`, `OLCPANEL_DATABASE_URL`, and `--database-url` overrides.
- PostgreSQL URL schemes are structurally recognized for future wiring.
- Embedded migration version 1 creates Phase 2 tables: `users`, `sessions`, `api_keys`, `clients`, `locations`, `traffic_counters`, `settings`, `nodes`, `backups`, and `integration_mappings`.
- Migrations are idempotent and seed one internal local node plus the four default core settings once.
- `olcpanel migrate` added and uses the same migration path as `olcpanel serve`.
- `olcpanel serve` opens the configured database and runs migrations before starting HTTP.
- `GET /api/v1/settings` and `PUT /api/v1/settings` added for core settings.
- Settings validation added for locale, quota lock mode, and non-empty backup path.
- Frontend top-level dependencies updated to React 19.2.6, Vite 8.0.14, TypeScript 6.0.3, and matching React/Vite type/plugin packages.
- Backend SQLite dependency updated to `modernc.org/sqlite v1.50.1`; `go mod tidy` refreshed required indirect dependencies.
- Standard Vite client ambient declarations added so TypeScript 6 accepts CSS side-effect imports.
- Auth package added with bcrypt password hashing, first-admin setup, login verification, secure random sessions, CSRF tokens, API token generation, SHA-256 token hashing, and in-memory rate limiting.
- Phase 3 migration adds CSRF metadata and auth indexes for sessions/API keys.
- `POST /api/v1/setup`, `POST /api/v1/login`, and `POST /api/v1/logout` added.
- `GET /api/v1/api-keys`, `POST /api/v1/api-keys`, and `DELETE /api/v1/api-keys/{id}` added for session-authenticated admins.
- Existing settings APIs are now protected: before setup they return setup-required errors; after setup they require a valid session or API key.
- Browser session mutations require `X-CSRF-Token`; API key requests use `Authorization: Bearer olcp_...` and bypass CSRF.
- `/api/v1/state` now reads setup status from the users table and reports authenticated state for valid session/API-key requests.
- Session cookies are `HttpOnly`, `SameSite=Lax`, path `/`, and set `Secure` for HTTPS or `X-Forwarded-Proto: https`.
- Unsafe browser requests with an `Origin` header are checked against same-origin before CSRF-protected mutations.
- README updated with first-run setup, login, session CSRF, and API-key examples.

## Phase 0 Architecture Review Notes

- The architecture is coherent around a modular Go monolith, embedded React UI, SQLite default storage, and local VPS runtime control.
- v1 boundaries are now explicit: multi-node is limited to schema fields such as `node_id`; no production API or UI controls for multi-node are accepted in v1.
- The plan uses `/api/v1/state` for the first skeleton endpoint while the architecture lists `GET /state` under the `/api/v1` prefix. Implementation should serve `GET /api/v1/state`.
- PostgreSQL is optional and structural until storage and migration phases implement full compatibility.
- Docker is secondary to native systemd deployment because netns/veth/tc requires elevated Linux capabilities.

## Phase 1 Completion Notes

- Backend binary builds to `bin/olcpanel.exe`.
- Linux production binary target is `bin/olcpanel`; cross-build target is `bin/olcpanel-linux-amd64`.
- `olcpanel serve --bind 127.0.0.1:8888` starts locally.
- `/api/v1/state` returns:
  - `service: olcpanel`;
  - `api_version: v1`;
  - `setup_required: true`;
  - `bind_address: 127.0.0.1:8888`.
- `/` returns the embedded React UI shell.
- Server smoke test was run locally on PID 22200 and stopped after verification.

## Phase 2 Completion Notes

- Schema version: `1` (`001_phase2_core.sql`).
- Migration status: fresh and repeated SQLite migrations pass; repeated migration does not duplicate default rows.
- Settings API status: `GET /api/v1/settings` returns persisted defaults; `PUT /api/v1/settings` fully replaces the core settings set.
- Settings shape:
  - `ui_locale`: `en` or `ru`
  - `public_client_endpoint_enabled`: boolean, default `false`
  - `backup_path`: non-empty path, default `/var/lib/olcpanel/backups`
  - `quota_lock_mode`: `stop` or `disable_traffic`
- Transitional security state: settings endpoints are intentionally unauthenticated until Phase 3.

## Phase 3 Completion Notes

- Schema version: `2` (`002_phase3_auth.sql`).
- Auth status: first admin setup is single-use; username policy is 3-64 characters and passwords require at least 12 characters.
- Session status: login/setup issue an HTTP-only session cookie plus a CSRF token; logout revokes the current session and clears the cookie.
- API key status: raw API tokens are prefixed with `olcp_`, stored only as SHA-256 hashes, returned only at creation, and can be revoked.
- Admin API protection: settings and API-key management are blocked before setup; settings accepts session or API-key auth after setup; API-key management itself requires a browser admin session.
- Rate limiting status: setup attempts, login failures, and repeated failed API-key auth are limited in memory for the v1 local-node deployment.
- Frontend auth UI remains deferred to Phase 11.

## Dependency Update Notes

- No API, route, database schema, CLI, or intended UI behavior changes were made.
- Embedded frontend assets in `internal/webui/dist/` were rebuilt after the dependency update.
- `go mod tidy` raised the module directive from `go 1.23.0` to `go 1.25.0` for the selected `modernc.org/sqlite v1.50.1` dependency set.
- TypeScript 6 required adding `frontend/src/vite-env.d.ts` for Vite's CSS module declarations.

## Test Status

- Phase 0: documentation-only; no automated tests required.
- Phase 1:
  - `npm --prefix frontend install` passed.
  - `npm --prefix frontend run build` passed.
  - `C:\Program Files\Go\bin\go.exe test ./...` passed.
  - `C:\Program Files\Go\bin\go.exe build -o bin\olcpanel.exe .\cmd\olcpanel` passed.
  - Cross-build `GOOS=linux GOARCH=amd64 go build -o bin\olcpanel-linux-amd64 ./cmd/olcpanel` passed.
  - `Invoke-RestMethod http://127.0.0.1:8888/api/v1/state` passed.
  - `Invoke-WebRequest http://127.0.0.1:8888/` returned HTTP 200.
- Phase 1 review fixes:
  - `go test ./cmd/olcpanel -run TestServeRejectsUnexpectedArgument` passed.
  - `go test ./...` passed.
  - `npm --prefix frontend run build` passed.
  - Direct Windows backend build `go build -o bin/olcpanel.exe ./cmd/olcpanel` passed.
  - Direct Linux cross-build `GOOS=linux GOARCH=amd64 go build -o bin/olcpanel-linux-amd64 ./cmd/olcpanel` passed.
  - `make backend` and `make linux` could not be run because `make` is not installed in this Windows shell.
- Phase 2:
  - `go test ./...` passed after adding storage, migration, CLI, config, and API tests.
  - `npm --prefix frontend run build` passed.
  - `go build -o bin\olcpanel.exe .\cmd\olcpanel` passed.
  - `GOOS=linux GOARCH=amd64 go build -o bin\olcpanel-linux-amd64 .\cmd\olcpanel` passed.
- Dependency update:
  - `npm --prefix frontend install` passed; audit reported 0 vulnerabilities.
  - `npm --prefix frontend run build` passed with TypeScript 6.0.3 and Vite 8.0.14.
  - `go test ./...` passed.
  - `go build -o bin/olcpanel.exe ./cmd/olcpanel` passed.
  - `GOOS=linux GOARCH=amd64 go build -o bin/olcpanel-linux-amd64 ./cmd/olcpanel` passed.
- Phase 3:
  - `go test ./...` passed.
  - `npm --prefix frontend run build` passed.
  - `go build -o bin/olcpanel.exe ./cmd/olcpanel` passed.
  - `GOOS=linux GOARCH=amd64 go build -o bin/olcpanel-linux-amd64 ./cmd/olcpanel` passed.

## Open Risks And Blockers

- SQLite is the only Phase 2 runtime-verified database. PostgreSQL URLs are recognized, but PostgreSQL driver/runtime support remains a future implementation task.
- Phase 3 rate limiting is in-memory and resets on process restart, which is acceptable for v1 local-node scope but not a distributed deployment model.
- Frontend auth/setup/login screens are not implemented yet; operators must use direct API calls until Phase 11.
- Current verification ran from Windows, but production assumptions and later e2e tests must target Linux servers.
- Linux netns/veth/tc/root tests will require an isolated Linux environment and must stay separate from normal unit tests.
- The updated Go dependency set now records `go 1.25.0`; future environments need a compatible Go toolchain.

## Architecture Compliance Checklist

Review this checklist at the end of every phase.

- [x] One Go binary with embedded React UI remains the target architecture.
- [x] SQLite default, PostgreSQL optional.
- [x] Local VPS/node only in v1.
- [x] `/api/v1` admin API.
- [x] Private-token-first subscriptions.
- [x] Per-location supervisor/process model.
- [x] Per-location netns/veth/tc default.
- [x] `127.0.0.1:8888` default bind.
- [x] RU/EN UI support remains required; frontend skeleton includes initial RU/EN copy selection.
- [x] No unfinished multi-node UI exposure.

## Status Update Gate

Every implementation phase must update this file before it is considered complete. The update must include:

- what changed;
- what tests passed;
- what is blocked or risky;
- the next phase or next session target;
- an architecture compliance checklist review.
