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

- Current phase: Phase 6 - Supervisor And Reload Diff complete; Phase 7 - Runtime Config And Process Launch is next.
- Completed phases:
  - Phase 0 - Architecture Approval And Project Tracking
  - Phase 1 - Repository Skeleton And Build Pipeline
  - Phase 2 - Storage, Migrations, And Settings
  - Phase 3 - Auth And Security Baseline
  - Phase 4 - Clients And Locations Domain API
  - Phase 5 - Subscription Rendering
  - Phase 6 - Supervisor And Reload Diff
- Next session target: start Phase 7 runtime config rendering and real OlcRTC process launch.

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
- `internal/clients` domain package added with client/location models, SQLite persistence, validation, generated record IDs, generated 64-character hex crypto keys, generated room IDs, derived quota/expiry states, and per-location runtime status placeholders.
- Schema migration version 3 adds Phase 4 fields for clients (`enabled`, `expires_at`, `quota_bytes`, `quota_used_bytes`) and locations (`enabled`, `provider`, `transport`, `room_id`, `crypto_key`, `transport_payload`, `dns`).
- Authenticated client APIs added under `/api/v1/clients`: list, create, read, update, delete, and rotate.
- Authenticated location APIs added under `/api/v1/clients/{id}/locations`: list, create, update, and delete.
- Browser session mutations for clients, locations, and rotation require CSRF; API-key requests continue to bypass CSRF.
- OlcRTC provider/transport validation added for Phase 4 presets: `telemost`, `wbstream`, and `jitsi`; stable and unstable transports are accepted, unsupported combinations are rejected.
- `transport_payload` is normalized and returned as JSON for `datachannel`, `vp8channel`, `seichannel`, and `videochannel`; `datachannel` requires an empty object.
- `POST /api/v1/clients/{id}/rotate` rotates crypto keys by default and rotates stored room IDs when `rotate_rooms` is true.
- JSON request decoding now caps body size, rejects empty/malformed/unknown/trailing JSON, and returns `413` for oversized API request bodies.
- CSRF verification now uses constant-time hash comparison.
- In-memory rate limiting now keys off `RemoteAddr` only and prunes expired attempt keys.
- API-key revoke now reports missing or already-revoked keys as not found; `DELETE /api/v1/api-keys/{id}` returns `404` for that case.
- SQLite connections now enable `PRAGMA foreign_keys = ON` and set `PRAGMA busy_timeout = 5000`.
- Server route registration is split into focused state, auth, settings, clients, API-key, and static groups while preserving `server.New(cfg, assets, options...)`.
- Embedded frontend copy now reflects the current API-backed state instead of the Phase 1 skeleton.
- Schema migration version 4 adds `clients.subscription_token`, backfills existing clients with private `sub_...` tokens, and enforces token uniqueness.
- New clients receive private subscription tokens, and authenticated client JSON includes `subscription_token`.
- `internal/subscriptions` added as pure rendering logic for official `olcrtc://` URIs and plaintext `sub.md` subscription documents.
- Subscription URI rendering supports `datachannel`, `vp8channel`, `seichannel`, and `videochannel` payload aliases and omits data/default payload blocks.
- Video transport payload validation now accepts the official `nvenc`, `high`/`highest`, `tile_module`, and `tile_rs` fields used by the URI renderer.
- `GET /sub/{token}` added as an unauthenticated plaintext subscription endpoint.
- `GET /c/{client_id}` added as an opt-in plaintext endpoint gated by `public_client_endpoint_enabled`; it remains disabled by default.
- `POST /api/v1/clients/{id}/rotate` accepts `rotate_subscription_token`; rotating it invalidates old `/sub/{token}` links.
- `internal/supervisor` added with an in-memory per-location desired-state diff and fakeable runner boundary.
- Supervisor reload loads local desired state from SQLite clients, locations, settings, expiry, and quota fields.
- Reload actions report `started`, `restarted`, `stopped`, `unchanged`, and `skipped` with reasons for new, changed, removed, disabled, expired, quota-locked, and unchanged locations.
- Phase 6 runner behavior is intentionally no-op by default; real `olcrtc` config rendering and process launch remain deferred to Phase 7.
- Authenticated `POST /api/v1/reload` added; browser sessions require CSRF and API-key requests bypass CSRF like other admin APIs.
- `olcpanel serve` now owns one supervisor instance and handles `SIGHUP` as a best-effort supervisor reload without terminating the server on reload failure.
- Standalone `olcpanel reload` remains deferred until daemon IPC or HTTP-client behavior is designed.

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

## Phase 4 Completion Notes

- Schema version: `3` (`003_phase4_clients_locations.sql`).
- Client API status: `GET/POST /api/v1/clients`, `GET/PUT/DELETE /api/v1/clients/{id}`, and `POST /api/v1/clients/{id}/rotate` are implemented.
- Location API status: `GET/POST /api/v1/clients/{id}/locations` and `PUT/DELETE /api/v1/clients/{id}/locations/{location_id}` are implemented.
- Client response fields include enabled state, optional expiry/quota, used quota, derived quota/expiry states, location count, and timestamps.
- Location response fields include enabled state, provider, transport, transport stability, room ID, crypto key, normalized transport payload, DNS, runtime status, and timestamps.
- Runtime enforcement for disabled, expired, and quota-exceeded states remains deferred to later supervisor/accounting phases.
- No frontend operator UI was added in Phase 4; direct API usage remains the management path until Phase 11.

## Phase 5 Completion Notes

- Schema version: `4` (`004_phase5_subscription_tokens.sql`).
- Private subscription token status: every client has a unique `sub_...` token; new clients receive one at creation and existing clients are backfilled during migration.
- Subscription API status: `GET /sub/{token}` returns `text/plain; charset=utf-8` without admin auth for enabled clients with at least one enabled location.
- Public client endpoint status: `GET /c/{client_id}` is disabled by default and only returns the same plaintext subscription when `public_client_endpoint_enabled` is `true`.
- Subscription renderer status: outputs `#name`, `#update`, `#refresh`, `#used`, `#available`, per-location `olcrtc://` lines, and `##name`, `##used`, `##available`, `##comment`.
- URI payload status: renders official aliases for VP8 (`vp8-fps`, `vp8-batch`), SEI (`fps`, `batch`, `frag`, `ack-ms`), and video (`video-*`) payload fields; `datachannel` and default payloads omit the payload block.
- Token rotation status: `rotate_subscription_token` changes only the private subscription token unless `rotate_rooms` is also requested; old token URLs return `404`.
- Runtime enforcement for disabled, expired, and quota-exceeded states remains deferred; Phase 5 only reports current quota metadata in the plaintext output.

## Phase 6 Completion Notes

- Supervisor package status: desired-state loading and in-memory diffing are implemented for the local node.
- Reload decision status: new active locations start, unchanged locations are left alone, changed locations restart, removed locations stop, and ineligible non-running locations are reported as skipped.
- Enforcement status: disabled clients, disabled locations, expired clients, and quota-exceeded clients in `quota_lock_mode=stop` stop running supervisor entries.
- Runtime runner status: the default runner is no-op, so Phase 6 records and exposes decisions without launching real `olcrtc` processes.
- Admin reload API status: `POST /api/v1/reload` returns a summary and per-location action list.
- Daemon reload status: `SIGHUP` calls the same supervisor reload path and logs the resulting counts; reload failures are logged and do not stop the HTTP server.
- CLI reload status: standalone `olcpanel reload` was intentionally not added in Phase 6.

## Review Hardening Fixes Notes

- No new roadmap features were pulled forward.
- Public route names and successful response schemas were preserved.
- Intentional error behavior changes:
  - trailing JSON and empty/malformed JSON return `400`;
  - oversized JSON request bodies return `413`;
  - deleting a missing or already-revoked API key returns `404`.
- Existing user-owned `go.mod` and `go.sum` dependency edits were preserved.

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
- Phase 4:
  - `go test ./...` passed.
  - `npm --prefix frontend run build` passed.
  - `go build -o bin\olcpanel.exe .\cmd\olcpanel` passed.
  - `GOOS=linux GOARCH=amd64 go build -o bin\olcpanel-linux-amd64 .\cmd\olcpanel` passed after rerunning outside the Windows sandbox when the sandbox failed to spawn the cross-build process.
- Review hardening fixes:
  - `go test ./internal/auth ./internal/storage ./internal/server` passed.
  - `go test ./...` passed.
  - `go vet ./...` passed.
  - `npm --prefix frontend run build` passed.
  - `go build -o bin/olcpanel ./cmd/olcpanel` passed.
  - `GOOS=linux GOARCH=amd64 go build -o bin/olcpanel-linux-amd64 ./cmd/olcpanel` passed.
- Phase 5:
  - `go test ./...` passed.
  - `npm --prefix frontend run build` passed.
  - `go build -o bin/olcpanel ./cmd/olcpanel` passed.
  - `GOOS=linux GOARCH=amd64 go build -o bin/olcpanel-linux-amd64 ./cmd/olcpanel` passed.
- Phase 6:
  - `go test ./...` passed.
  - `npm --prefix frontend run build` passed.
  - `go build -o bin/olcpanel ./cmd/olcpanel` passed.
  - `GOOS=linux GOARCH=amd64 go build -o bin/olcpanel-linux-amd64 ./cmd/olcpanel` passed.

## Open Risks And Blockers

- SQLite is the only Phase 2 runtime-verified database. PostgreSQL URLs are recognized, but PostgreSQL driver/runtime support remains a future implementation task.
- Rate limiting is in-memory, keyed by direct `RemoteAddr`, and resets on process restart, which is acceptable for v1 local-node scope but not a distributed deployment model.
- Frontend auth/setup/login and client/location screens are not implemented yet; operators must use direct API calls until Phase 11.
- Phase 6 enforces disabled, expired, and stop-mode quota states only inside the in-memory supervisor; real process termination will matter once Phase 7 adds actual `olcrtc` launch.
- `quota_lock_mode=disable_traffic` remains pending netns/tc traffic-control work and does not stop locations in Phase 6.
- Standalone CLI reload remains pending daemon IPC or HTTP-client design.
- Public `/c/{client_id}` intentionally exposes stable client IDs when enabled; the private `/sub/{token}` endpoint remains the default safer sharing path.
- Upstream OlcRTC documentation currently names the Jitsi-like transport carrier as `jazz`; Phase 4 preserves the planned public API enum `jitsi` while using the same stable/unstable transport matrix.
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
