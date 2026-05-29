# Phase 14 Release Candidate Validation

Date: 2026-05-29

## Scope

Phase 14 is a hardening and release-candidate validation phase. No public API,
schema, CLI, or UI changes were planned or made during this pass.

Current result: RC validation is partially complete. Local and static gates
passed, but the required clean Debian/Ubuntu amd64 root VPS install, update,
rollback, and real netstack root-e2e drills were not available from this
workspace and remain required before a public release announcement.

## Local Environment

- Workspace: `D:\projects\olcrtc_panel`
- Branch: `phase-14-hardening-rc`
- Host validation environment: Windows amd64
- Go: `go version go1.26.3 windows/amd64`
- Node.js: `v24.15.0`
- npm: `11.15.0`
- Shell syntax checker: Git Bash at `C:\Program Files\Git\bin\bash.exe`

## Local Command Results

- `go test ./...`: passed.
- `npm --prefix frontend run test`: passed, 2 test files and 13 tests.
- `npm --prefix frontend run build`: passed and rebuilt `internal/webui/dist`.
- `go build -o bin/olcpanel ./cmd/olcpanel`: passed.
- `GOOS=linux GOARCH=amd64 go build -o bin\olcpanel-linux-amd64 .\cmd\olcpanel`: passed.
- `bash -n install.sh`: passed using Git Bash.
- `bash -n update.sh`: passed using Git Bash.
- `go test -tags root_e2e ./internal/netstack`: passed on Windows, but this
  is not a substitute for the Linux-only root-e2e file because that file has
  the `linux && root_e2e` build constraint.

The default Windows `bash.exe` route failed because it points at a missing WSL
`/bin/bash`; Git Bash was used for syntax validation instead.

## VPS Validation

Required VPS profile:

- OS/image: not run, no clean Debian/Ubuntu amd64 VPS with root access was
  available in this workspace.
- Kernel: not recorded.
- Architecture: not recorded.

Required commands not completed:

- `go test -tags root_e2e ./internal/netstack` on Linux as root.
- Clean install with `OLCPANEL_BINARY=/path/to/olcpanel-linux-amd64 ./install.sh`.
- `systemctl status olcpanel --no-pager`.
- `curl -fsS http://127.0.0.1:8888/api/v1/state` after install.
- Update with `OLCPANEL_BINARY=/path/to/new/olcpanel-linux-amd64 ./update.sh`.
- `curl -fsS http://127.0.0.1:8888/api/v1/state` after update.
- Rollback drill with a bad replacement binary or bad archive.

## Installer Result

Static validation passed:

- `install.sh` parses with `bash -n`.
- The installer preserves the app's development default of
  `127.0.0.1:8888` outside installer deployment and writes public installer
  deployments as `OLCPANEL_BIND=0.0.0.0:PORT`.
- `/etc/default/olcpanel` receives configuration values only:
  `OLCPANEL_BIND`, `OLCPANEL_BASE_PATH`, `OLCPANEL_DATABASE_URL`,
  `OLCPANEL_RUNTIME_DIR`, and `OLCPANEL_LOG_PATH`.
- No generated session, API, subscription, room, or crypto secrets are written
  to `/etc/default/olcpanel`.

Runtime validation on a clean VPS was not run.

## Updater Result

Static validation passed:

- `update.sh` parses with `bash -n`.
- The updater preserves `/etc/default/olcpanel` by sourcing it and replacing
  only `/usr/local/bin/olcpanel`.
- The updater backs up the current binary to `/usr/local/bin/olcpanel.bak`
  before installing the new binary.

Runtime update validation on a clean VPS was not run.

## Rollback Drill Result

Not run. The script path for rollback is present: failed migration, failed
service restart, or failed post-update state smoke calls `rollback`, moves
`/usr/local/bin/olcpanel.bak` back to `/usr/local/bin/olcpanel`, and attempts
`systemctl restart olcpanel`.

This must still be proven on a clean Debian/Ubuntu amd64 root host by forcing a
bad replacement binary or bad archive and confirming the previous service stays
reachable.

## Secrets And Filesystem Review

- Runtime `server.yaml` files are written by `internal/runtimeconfig` with
  private permissions. Tests assert `0600` on platforms that can report POSIX
  modes accurately.
- The installer creates `/var/lib/olcpanel`, `/var/lib/olcpanel/runtime`,
  `/var/lib/olcpanel/backups`, `/var/log/olcpanel`, and `/etc/olcpanel`.
- The default database path remains `sqlite:///etc/olcpanel/panel.db`.
- The default backup path remains `/var/lib/olcpanel/backups`.
- The default runtime path remains `/var/lib/olcpanel/runtime`.
- The default log path remains `/var/log/olcpanel/panel.log`.
- `/etc/default/olcpanel` is installed as `0644` and contains only deployment
  configuration, not generated secrets.

## Architecture Compliance Review

- One Go binary with embedded React UI remains the architecture.
- Default bind remains `127.0.0.1:8888` for local/manual runs.
- Debian/Ubuntu systemd remains the primary production deployment path.
- Docker remains secondary documented deployment guidance.
- v1 remains local-node only. Repository search found schema and backend
  `node_id` reservations plus UI text that says `127.0.0.1 local node`; no
  unfinished multi-node controls were found in `frontend/src`.
- `/api/v1` remains the admin API namespace.
- Private `/sub/{token}` remains the default sharing path. The legacy
  `/c/{client_id}` route remains disabled by default and is only shown in the
  UI when `public_client_endpoint_enabled` is enabled.

## RC Known Limitations

- Phase 14 cannot be called complete until Linux/root validation succeeds on a
  clean Debian/Ubuntu amd64 host.
- The Windows `root_e2e` command does not compile or run the Linux-only
  root-e2e test file.
- Clean install, update, rollback, and systemd service smoke checks remain
  pending.
- Real OlcRTC session validation remains a Linux deployment check.
- PostgreSQL URLs are recognized structurally, but SQLite remains the only
  runtime-verified database.
- Browser-level visual QA remains pending in an environment with a configured
  browser testing workflow.

## Release Notes

- Phase 14 produced validation documentation only.
- No public API, schema, CLI, or UI behavior changed.
- Local Go, frontend, build, cross-build, and script syntax gates passed.
- The release remains RC-blocked on clean Linux/root install, update, rollback,
  and netstack e2e validation.

## Rollback Guidance

For installed systemd deployments, keep the last known-good binary and database
backup before updating.

If `update.sh` fails after replacing the binary, it is expected to restore
`/usr/local/bin/olcpanel.bak` and restart `olcpanel`. If manual recovery is
needed:

```bash
systemctl stop olcpanel
install -m 0755 /usr/local/bin/olcpanel.bak /usr/local/bin/olcpanel
/usr/local/bin/olcpanel migrate
systemctl start olcpanel
curl -fsS http://127.0.0.1:8888/api/v1/state
```

If state data must be rolled back, restore a known-good Panel backup archive
with the service stopped, then start the service and verify `/api/v1/state`.
