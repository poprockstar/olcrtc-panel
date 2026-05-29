# OlcRTC Panel Project Status

Last updated: 2026-05-29

## Architecture Approval

- Architecture document: `docs/architecture/olcrtc-panel-architecture.md`
- Approved architecture version/date: v1 baseline, approved 2026-05-27
- Phase 0 review result: approved with implementation guardrails below
- Canonical scope: v1 is local VPS/local-node only
- Production runtime target: Linux servers, with Debian/Ubuntu native systemd deployment as the primary path
- Multi-node status: schema-reserved only; no API or UI controls may expose unfinished multi-node behavior

## Phase State

- Current phase: Phase 14 - Hardening And Release Candidate validation is in progress. Local and static gates passed; clean Debian/Ubuntu root install, update, rollback, and Linux netstack e2e validation remain required before RC completion.
- Completed phases:
  - Phase 0 - Architecture Approval And Project Tracking
  - Phase 1 - Repository Skeleton And Build Pipeline
  - Phase 2 - Storage, Migrations, And Settings
  - Phase 3 - Auth And Security Baseline
  - Phase 4 - Clients And Locations Domain API
  - Phase 5 - Subscription Rendering
  - Phase 6 - Supervisor And Reload Diff
  - Phase 7 - Runtime Config And Process Launch
  - Phase 8 - Netns, Veth, NAT, DNS, And TC
  - Phase 9 - Traffic Accounting, Quotas, And Expiry
  - Phase 10 - Logs, Metrics, And Runtime Status API
  - Phase 11 - Frontend Operational UI
  - Phase 12 - Backup, Restore, Import, Export
  - Phase 13 - Installer, Updater, Systemd, Docker
- Next session target: run Phase 14 on a clean Debian/Ubuntu amd64 root VPS and complete install, update, rollback, systemd, `/api/v1/state`, and `go test -tags root_e2e ./internal/netstack` validation.

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
- Runtime configuration options added:
  - `OLCPANEL_RUNTIME_DIR` and `olcpanel serve --runtime-dir`, default `/var/lib/olcpanel/runtime`
  - `OLCPANEL_OLCRTC_BINARY` and `olcpanel serve --olcrtc-binary`, default `olcrtc`
- `internal/runtimeconfig` renders deterministic per-location server YAML at `<runtime-dir>/<location_id>/server.yaml`.
- Runtime YAML includes `mode: srv`, `auth.provider`, `room.id`, `crypto.key`, `net.transport`, `net.dns`, `data: data`, and transport-specific `vp8`, `sei`, or `video` sections where required.
- Runtime config writes are atomic where practical, create per-location directories with private permissions, and call `chmod 0600` for secret-bearing `server.yaml` files.
- `internal/supervisor` now includes a real process runner that starts `olcrtc <server.yaml>`, restarts changed locations, stops removed or ineligible locations, and removes stopped location runtime directories.
- Unexpected child exits are recorded as failed process status without an automatic restart loop.
- `olcpanel serve` wires the real process runner and performs an initial best-effort supervisor reload; missing `olcrtc` prevents affected active locations from launching but does not prevent HTTP server startup.
- Schema migration version 5 adds nullable `locations.speed_limit_bps`; `null` means unlimited and positive values enable per-location shaping.
- Location create/update APIs now accept and return `speed_limit_bps`, rejecting zero or negative values.
- Runtime network configuration added:
  - `OLCPANEL_NETWORK_CIDR` and `olcpanel serve --network-cidr`, default `10.255.0.0/16`
  - `olcpanel doctor --network-cidr` uses the same default.
- `internal/netstack` added with deterministic resource derivation:
  - namespace `olcp-<sha256-11hex>`
  - host veth `olh-<sha256-11hex>`
  - namespace veth `oln-<sha256-11hex>`
  - deterministic per-location `/30` allocation inside the configured runtime CIDR.
- Netstack reconciliation creates namespaces/veth pairs, assigns host and namespace IPs, brings links/routes up, writes `/etc/netns/<namespace>/resolv.conf`, enables IPv4 forwarding, maintains an `OLCPANEL-NAT` chain, and applies/removes symmetric `tc tbf` shaping.
- Runtime DNS keeps the full `host:port` value in OlcRTC YAML while netns `resolv.conf` writes the host portion as a `nameserver`.
- Process runner now launches active locations through `ip netns exec <namespace> <olcrtc> <server.yaml>` when netstack is configured, and cleans netstack resources plus runtime config on stop or failed start.
- Supervisor validates active location subnet collisions before mutating runtime state.
- `quota_lock_mode=stop` still stops quota-exceeded locations; `quota_lock_mode=disable_traffic` keeps them process-eligible and asks netstack to bring the namespace veth down.
- `olcpanel doctor` added as a read-only diagnostic command for required Linux commands, IPv4 forwarding, and stale OlcPanel namespaces/veths; unhealthy findings cause a nonzero exit.
- Linux/root e2e coverage for real netns/veth/NAT/tc paths is isolated behind `go test -tags root_e2e ./internal/netstack`.
- Runtime traffic accounting options added:
  - `OLCPANEL_TRAFFIC_SAMPLE_INTERVAL` and `olcpanel serve --traffic-sample-interval`, default `30s`.
- Schema migration version 6 adds `traffic_counter_state` for per-location raw veth counter baselines, last sample time, and reset counts.
- Traffic counter lookup indexes were added for client/location/time and location/time history queries.
- `internal/traffic` samples deterministic host veth counters derived from `netstack.NamesForLocation`, stores the first sample as a baseline, and appends later positive deltas to `traffic_counters`.
- Counter resets or recreated veths update the stored baseline and increment `traffic_counter_state.reset_count` without subtracting usage.
- Persisted deltas increment `clients.quota_used_bytes`, so client API and subscription quota metadata now reflect live accounting totals.
- `olcpanel serve` starts the traffic sampler after the initial supervisor reload and stops it during server shutdown.
- Quota-exceeded and expiry transitions detected by the sampler trigger a best-effort supervisor reload once for the transition.
- Observability logging options added:
  - `OLCPANEL_LOG_PATH` and `olcpanel serve --log-path`, default `/var/log/olcpanel/panel.log`.
- `internal/observability` added with JSONL log entries, safe append, parent directory creation, `panel.log` to `panel.log.1` rotation at 25 MiB, query filtering, and plaintext formatting.
- Panel `slog` output now fans out to stderr and the JSONL file sink.
- OlcRTC child stdout/stderr still mirror to the panel process streams and are also captured as `olcrtc_stdout` and `olcrtc_stderr` log entries with client/location metadata.
- Authenticated `GET /api/v1/logs` added with level/source/client/location/time/search/limit filters; `format=json` is the default and `format=text` returns copy/download-friendly plaintext.
- `internal/metrics` added for dashboard snapshots with panel uptime, nullable host metrics, client/location/process counts, traffic totals, quota warnings/exceeded counts, expired clients, and per-client summaries.
- Linux host metrics read `/proc/stat`, `/proc/meminfo`, and filesystem stats; non-Linux builds return nullable host fields.
- Authenticated `GET /api/v1/metrics` added for session or API-key reads without CSRF.
- Supervisor now exposes a read-only per-location runtime status snapshot, and location API responses overlay `running`, `stopped`, `failed`, or `pending` from the supervisor when available.
- Failed child exits are visible through metrics process counts and location `runtime_status` responses.
- Phase 11 frontend dependencies added: TanStack Query for API state, lucide-react for operational icons, qrcode for subscription QR generation, and Vitest/Testing Library for frontend behavior coverage.
- Embedded React UI replaced the placeholder screen with authenticated setup/login, session and CSRF persistence, and a responsive admin shell.
- UI localization now supports English and Russian, using saved `ui_locale` after authentication and browser language before settings load.
- Dashboard view consumes `GET /api/v1/metrics` for uptime, host resources, client/location/process counts, traffic totals, quota alerts, expired clients, and per-client summaries.
- Clients view consumes existing client and location CRUD APIs, including enabled state, expiry, quota fields, provider/transport presets, runtime status display, and unsupported provider/transport prevention.
- Subscription panel builds private `/sub/{subscription_token}` URLs, fetches plaintext subscriptions, extracts `olcrtc://` lines, provides copy actions, and renders QR codes for the private URL and selected URI.
- Logs view consumes `GET /api/v1/logs` with level/source/client/location/search/limit filters, JSON display, and plaintext download via `format=text`.
- Settings view loads and saves the complete settings object for `ui_locale`, `public_client_endpoint_enabled`, `backup_path`, and `quota_lock_mode`.
- Schema migration version 7 extends backup records with archive format version, size, checksum, completion time, and error metadata while preserving existing rows.
- `internal/backup` added for `olcpanel-backup-v1` ZIP archive creation and validation. Each archive contains `manifest.json` and `panel.db`; the manifest records format version, creation time, schema version, app name, and database size/checksum.
- Backup records are persisted in SQLite, and authenticated `GET /api/v1/backups` lists known local-node backup records.
- Authenticated `POST /api/v1/backup` creates a validated SQLite backup archive in the configured `backup_path`; browser sessions require CSRF and API-key requests follow existing admin auth behavior.
- Authenticated `POST /api/v1/restore` restores only a known completed backup record by validating the archive, stopping all managed runtime processes, replacing managed SQLite state transactionally, rerunning migrations, and reloading the supervisor.
- Supervisor now exposes `StopAll(ctx)` so restore can pause runtime processes before state replacement.
- Authenticated `GET /api/v1/export` returns normalized Panel JSON for settings, clients, and nested locations.
- Authenticated `POST /api/v1/import` imports Panel JSON append-only by default, creating fresh client IDs and subscription tokens while preserving exported room IDs and crypto keys for locations.
- Panel JSON import validates provider/transport combinations, transport payloads, DNS, speed limits, and settings when settings import is explicitly requested.
- CLI commands added:
  - `olcpanel backup [--database-url] [--runtime-dir] [--output PATH]`
  - `olcpanel restore --file PATH [--database-url]`
  - `olcpanel export --output PATH [--database-url]`
  - `olcpanel import --file PATH [--database-url] [--apply-settings]`
  - `olcpanel reset-admin --username USER --password PASS [--database-url]`
- Frontend Backups view now lists real backup records, shows the configured backup path, creates backups, restores selected completed backups with confirmation, exports Panel JSON, and imports Panel JSON with success/error state.
- Embedded frontend assets in `internal/webui/dist/` were rebuilt after the Phase 12 UI build.
- Runtime base path configuration added with `OLCPANEL_BASE_PATH` and `olcpanel serve --base-path`; empty or `/` serves from root, while `/panel` serves the full app below `/panel/`.
- Base path validation normalizes leading/trailing slashes and rejects spaces, query strings, fragments, parent/current path segments, and reserved first segments that collide with `/api`, `/sub`, `/c`, or `/assets`.
- `GET /api/v1/state` now includes `base_path`.
- Server routing now supports prefixed UI, API, subscription, public client, and asset paths; `/panel` redirects to `/panel/`, root redirects to the configured panel path, and unprefixed app routes are unavailable when a base path is configured.
- Browser session cookies now use the configured base path as their `Path` when the panel is served below a URI prefix.
- Frontend Vite output now uses relative assets, receives the runtime base path from the injected HTML bootstrap and state response, prefixes API/subscription fetches, and generates `/sub/{token}` and `/c/{id}` URLs under the configured base path.
- `install.sh` added for one-line Debian/Ubuntu amd64 installs. It prompts for public port and optional URI path, writes `/etc/default/olcpanel`, installs the Linux release binary or a supplied binary/download URL, installs systemd, runs migrations, enables the service, and prints the final public URL.
- `update.sh` added for release updates. It preserves `/etc/default/olcpanel`, database, runtime configs, logs, and backups, installs the selected release, runs migrations, restarts the service, health-checks the configured base path, and rolls back the previous binary on failure.
- `deploy/olcpanel.service` added with `/etc/default/olcpanel` environment loading, migration before serve, and `SIGHUP` reload wiring.
- GitHub Actions release workflow added for `v*` tags to build `olcpanel-linux-amd64.tar.gz` and a SHA256 checksum.
- Docker packaging remains secondary with documented host-network/privileged requirements and a Compose example.
- Reverse-proxy notes added for Caddy and Nginx TLS termination while keeping `OLCPANEL_BASE_PATH` aligned with the public URI prefix.

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

## Phase 7 Completion Notes

- Runtime path status: generated OlcRTC server configs are written under `/var/lib/olcpanel/runtime` by default, with CLI and environment overrides available.
- OlcRTC binary status: runtime launch uses `olcrtc` by default and can be pointed at an absolute or alternate binary name.
- Config renderer status: datachannel emits no extra transport section; VP8, SEI, and video transports map normalized payload fields to their documented YAML sections.
- File permission status: Unix builds call `chmod 0600` for `server.yaml`; Windows verification cannot observe POSIX mode bits accurately, so the mode assertion is skipped on Windows while the chmod path remains covered by code review and Linux builds.
- Process runner status: fake-process tests cover start, restart, stop cleanup, and unexpected child exit without automatic restart.
- Startup behavior status: initial reload is best-effort so a missing `olcrtc` binary is logged clearly without preventing the panel from serving admin APIs.

## Phase 8 Completion Notes

- Schema version: `5` (`005_phase8_location_network.sql`).
- Network capability requirements: production netstack reconciliation requires Linux plus `CAP_NET_ADMIN` for netns/veth/tc/iptables and permission to write `/etc/netns/<namespace>/resolv.conf`; service startup also writes `net.ipv4.ip_forward=1` through `sysctl`.
- Runtime network status: active locations are assigned deterministic `/30` subnets under the configured runtime CIDR; collisions are rejected before supervisor start/restart/stop mutations.
- NAT status: the panel owns an `OLCPANEL-NAT` chain in the `nat` table and reconciles MASQUERADE rules for location subnets without intentional duplicates.
- Traffic shaping status: `speed_limit_bps = null` removes qdisc state; positive values apply TBF shaping to both host and namespace veth ends.
- Quota lock status: `stop` preserves Phase 6 stop behavior; `disable_traffic` keeps the OlcRTC process eligible while disabling the namespace veth.
- Doctor status: `olcpanel doctor` prints human-readable findings and exits nonzero for missing commands, disabled forwarding, database read issues, and stale OlcPanel namespace/veth resources.
- Root/e2e status: real Linux checks are present but excluded from normal unit tests with the `linux && root_e2e` build tag.

## Phase 9 Completion Notes

- Schema version: `6` (`006_phase9_traffic_accounting.sql`).
- Accounting status: enabled local locations are sampled through their deterministic host veth sysfs counters; first samples establish baselines and subsequent samples persist append-only deltas.
- Reset handling status: lower raw counters are treated as reset/recreated veth baselines, counted in `traffic_counter_state.reset_count`, and never written as negative usage.
- Quota status: `clients.quota_used_bytes` is incremented by `rx_delta + tx_delta`, keeping admin API and subscription quota fields current.
- Runtime enforcement status: the sampler detects quota and expiry transitions after startup and calls the supervisor reload path once per crossing so existing Phase 6/8 stop or traffic-disable behavior applies.
- Startup status: sampling runs in the `serve` process after the initial best-effort reload, logs sample errors, and does not stop the HTTP service on sample failures.

## Phase 10 Completion Notes

- Schema version: unchanged at `6`; Phase 10 adds runtime/API surfaces without a database migration.
- Log status: panel logs are written to stderr and the configured JSONL file, with rotation to `.1` at 25 MiB. Missing log files query as empty, while unavailable sinks return `503`.
- Log API status: `GET /api/v1/logs` is admin-only, supports session or API-key auth without CSRF, and returns JSON by default or plaintext with `format=text`.
- Process output status: OlcRTC stdout/stderr are still mirrored to the service streams and are captured as structured log entries with `client_id` and `location_id`.
- Metrics API status: `GET /api/v1/metrics` returns one dashboard snapshot for panel uptime, host resources, clients, locations, processes, traffic, quotas, expiry, and per-client summaries.
- Runtime status status: supervisor snapshots now surface real per-location `running`, `failed`, and `pending` states through metrics and location API responses; stopped is represented when a status provider reports it.
- Platform status: Linux host resource metrics use `/proc` and filesystem stats; Windows/non-Linux verification intentionally returns nullable host fields.

## Phase 11 Completion Notes

- Frontend status: authenticated operator UI is implemented for first-run setup, login/logout, dashboard metrics, clients, locations, subscriptions, logs, settings, reload, and backups readiness.
- API contract status: no backend route, schema, CLI, or response-shape changes were made; the UI consumes the existing `/api/v1`, `/sub/{token}`, session cookie, and CSRF behavior.
- Settings save status: the UI sends the full settings object on `PUT /api/v1/settings`, preserving the Phase 2 replace-all contract.
- Provider/transport status: the UI exposes only the Phase 4 provider and transport enums and prevents unsupported combinations before submit while still surfacing backend errors.
- Subscription status: private token URLs remain the default; public `/c/{client_id}` is only shown when `public_client_endpoint_enabled` is enabled.
- Backup status: backup and restore actions remain deferred to Phase 12 and are not shown as disabled fake controls.

## Phase 12 Completion Notes

- Backup format status: `olcpanel-backup-v1` ZIP archives contain `manifest.json` plus `panel.db`; manifest validation checks archive format version, database size, and SHA-256 checksum before restore.
- Backup record status: the `backups` table now stores format version, size, checksum, completed time, and error metadata for completed archives.
- API backup status: `/api/v1/backups`, `/api/v1/backup`, `/api/v1/restore`, `/api/v1/export`, and `/api/v1/import` are admin-only; browser mutations require CSRF and API-key auth bypasses CSRF consistently with other admin routes.
- Restore status: API restore is intentionally limited to known completed backup records, stops managed runtime, replaces SQLite state transactionally from the validated snapshot, reruns migrations, and reloads supervisor so runtime configs are regenerated from database state.
- CLI status: backup, restore, export, import, and reset-admin commands are available for stopped-service maintenance and migration workflows.
- Panel JSON status: export includes settings, clients, and nested locations; import is append-only by default, creates new client identities/tokens, preserves location room IDs and crypto keys, and applies settings only when explicitly requested.
- Frontend status: the Backups screen now provides real backup creation, backup list, restore confirmation, Panel JSON export, and Panel JSON import controls.

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

## Phase 14 RC Validation Notes

- Phase 14 validation record created at `docs/development/phase14-release-candidate.md`.
- No API, schema, CLI, or UI behavior changes were made during this pass.
- Local Go, frontend, build, Linux cross-build, and shell syntax gates passed.
- Installer and updater static review confirmed `/etc/default/olcpanel` stores deployment configuration only, not generated secrets.
- Runtime config review confirmed secret-bearing `server.yaml` files are written through the private `0600` path in `internal/runtimeconfig`.
- Path review confirmed the documented defaults remain `/etc/olcpanel/panel.db`, `/var/lib/olcpanel/runtime`, `/var/lib/olcpanel/backups`, and `/var/log/olcpanel/panel.log`.
- Bind review confirmed local/manual defaults remain `127.0.0.1:8888`; installer deployments intentionally write `0.0.0.0:PORT`.
- Route review confirmed private `/sub/{token}` remains the default sharing route, while `/c/{client_id}` remains disabled by default and UI-gated by `public_client_endpoint_enabled`.
- Multi-node review confirmed v1 remains local-node only; schema/backend `node_id` reservations are present, but no unfinished multi-node operator controls were found in `frontend/src`.
- Required clean Linux/root install, update, rollback, and real netstack root-e2e validation were not run because no clean Debian/Ubuntu amd64 root VPS was available in this workspace.

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
- Phase 7:
  - `go test ./...` passed.
  - `npm --prefix frontend run build` passed.
  - `go build -o bin\olcpanel.exe .\cmd\olcpanel` passed.
  - `GOOS=linux GOARCH=amd64 go build -o bin\olcpanel-linux-amd64 .\cmd\olcpanel` passed after rerunning outside the Windows sandbox when the sandbox failed to spawn the cross-build process.
- Phase 8:
  - `go test ./...` passed.
  - `npm --prefix frontend run build` passed.
  - `go build -o bin/olcpanel ./cmd/olcpanel` passed.
  - `GOOS=linux GOARCH=amd64 go build -o bin/olcpanel-linux-amd64 ./cmd/olcpanel` passed.
  - `go test -tags root_e2e ./internal/netstack` was not run in this Windows session; it is intentionally isolated for a Linux root/capability-enabled environment.
- Phase 9:
  - `go test ./...` passed.
  - `npm --prefix frontend run build` passed.
  - `go build -o bin/olcpanel ./cmd/olcpanel` passed.
  - `GOOS=linux GOARCH=amd64 go build -o bin/olcpanel-linux-amd64 ./cmd/olcpanel` passed.
  - `go test -tags root_e2e ./internal/netstack` was not run in this Windows session; it remains a Linux/root/capability-enabled verification path.
- Phase 10:
  - `go test ./...` passed.
  - `npm --prefix frontend run build` passed.
  - `go build -o bin\olcpanel.exe .\cmd\olcpanel` passed.
  - `GOOS=linux GOARCH=amd64 go build -o bin\olcpanel-linux-amd64 .\cmd\olcpanel` passed after rerunning outside the Windows sandbox when the sandbox failed to spawn the cross-build process.
  - `go test -tags root_e2e ./internal/netstack` was not run in this Windows session; it remains a Linux/root/capability-enabled verification path.
- Phase 11:
  - `npm --prefix frontend install` passed; audit reported 0 vulnerabilities.
  - `npm --prefix frontend run test` passed with 8 Vitest/Testing Library tests.
  - `npm --prefix frontend run build` passed and rebuilt embedded assets.
  - `go test ./...` passed.
  - `go build -o bin/olcpanel ./cmd/olcpanel` passed.
  - `GOOS=linux GOARCH=amd64 go build -o bin/olcpanel-linux-amd64 ./cmd/olcpanel` passed.
  - Vite dev server HTTP smoke at `http://127.0.0.1:5173/` returned HTTP 200 with the OlcRTC Panel shell.
  - Full browser interaction QA was not run because the Browser Node REPL tool was not exposed in this session and Playwright is not installed/configured in the project.
- Phase 12:
  - `go test ./...` passed.
  - `npm --prefix frontend run test` passed with 10 Vitest/Testing Library tests.
  - `npm --prefix frontend run build` passed and rebuilt embedded assets.
  - `go build -o bin\olcpanel ./cmd/olcpanel` passed.
  - `GOOS=linux GOARCH=amd64 go build -o bin\olcpanel-linux-amd64 ./cmd/olcpanel` passed after rerunning outside the Windows sandbox when the sandbox failed to spawn the cross-build process.
- Phase 13:
  - `go test ./...` passed.
  - `npm --prefix frontend run test` passed with 13 Vitest/Testing Library tests.
  - `npm --prefix frontend run build` passed and rebuilt embedded assets.
  - `go build -o bin/olcpanel ./cmd/olcpanel` passed.
  - `GOOS=linux GOARCH=amd64 go build -o bin/olcpanel-linux-amd64 ./cmd/olcpanel` passed.
  - `bash -n install.sh` and `bash -n update.sh` could not run because this Windows environment routes `bash` through a missing WSL binary.
- Phase 14 RC validation:
  - `go test ./...` passed.
  - `npm --prefix frontend run test` passed with 2 test files and 13 Vitest/Testing Library tests.
  - `npm --prefix frontend run build` passed and rebuilt embedded assets.
  - `go build -o bin/olcpanel ./cmd/olcpanel` passed.
  - `GOOS=linux GOARCH=amd64 go build -o bin\olcpanel-linux-amd64 .\cmd\olcpanel` passed.
  - `C:\Program Files\Git\bin\bash.exe -n install.sh` passed.
  - `C:\Program Files\Git\bin\bash.exe -n update.sh` passed.
  - `go test -tags root_e2e ./internal/netstack` passed on Windows, but this does not execute the Linux-only root-e2e test file because that file is guarded by `linux && root_e2e`.
  - `go test -tags root_e2e ./internal/netstack` on Linux as root was not run; no clean Debian/Ubuntu amd64 root VPS was available in this workspace.
  - Clean install smoke, update smoke, `systemctl status olcpanel --no-pager`, `curl -fsS http://127.0.0.1:8888/api/v1/state`, and rollback drill were not run; they remain Phase 14 RC blockers.

## Open Risks And Blockers

- SQLite is the only Phase 2 runtime-verified database. PostgreSQL URLs are recognized, but PostgreSQL driver/runtime support remains a future implementation task.
- Rate limiting is in-memory, keyed by direct `RemoteAddr`, and resets on process restart, which is acceptable for v1 local-node scope but not a distributed deployment model.
- Browser-level visual QA for Phase 11 remains to be performed in an environment with the Browser plugin Node REPL tool or a configured Playwright workflow.
- Real OlcRTC session verification remains a Linux deployment check; Phase 7 unit tests use fake executables to prove process lifecycle behavior.
- Standalone CLI reload remains pending daemon IPC or HTTP-client design.
- Public `/c/{client_id}` intentionally exposes stable client IDs when enabled; the private `/sub/{token}` endpoint remains the default safer sharing path.
- Upstream OlcRTC documentation currently names the Jitsi-like transport carrier as `jazz`; Phase 4 preserves the planned public API enum `jitsi` while using the same stable/unstable transport matrix.
- Current Phase 14 verification ran from Windows. Production netns/veth/tc behavior still must be verified in an isolated Linux environment with `go test -tags root_e2e ./internal/netstack` as root.
- One-line install/update scripts now have Git Bash syntax coverage and local build coverage, but a clean Debian/Ubuntu root install, update, and rollback drill remain required before RC completion or any public release announcement.
- `olcpanel doctor` detects stale OlcPanel namespaces/veths and required command/sysctl state, but deeper NAT/tc drift reporting may need additional hardening during Linux deployment validation.
- Traffic accounting depends on Linux sysfs veth statistics at runtime; Windows verification covers the reader with a temporary sysfs-shaped fixture, not real kernel counters.
- Host metrics are snapshot-only and intentionally not retained historically; long-term charts or Prometheus-style scraping remain future work.
- The JSONL log sink is local-file based and rotates only one previous file (`panel.log.1`); external log retention remains an operator/systemd concern.
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
