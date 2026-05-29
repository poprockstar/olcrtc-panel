# OlcRTC Panel

OlcRTC Panel is a local Linux VPS control panel for managing OlcRTC clients, locations, subscriptions, quotas, runtime processes, and network isolation.

The v1 target is a single Linux server:

- one Go binary with embedded React UI;
- default bind address `127.0.0.1:8888`;
- SQLite by default in later storage phases;
- systemd as the primary production runtime;
- Docker as a secondary, documented option because netns/veth/tc needs elevated Linux capabilities.

## Development

Build the frontend:

```bash
npm --prefix frontend install
npm --prefix frontend run build
```

Build the backend binary:

```bash
mkdir -p bin
go build -o bin/olcpanel ./cmd/olcpanel
```

Build a Linux amd64 binary from another platform:

```bash
mkdir -p bin
GOOS=linux GOARCH=amd64 go build -o bin/olcpanel-linux-amd64 ./cmd/olcpanel
```

The Makefile targets create `bin/` automatically:

```bash
make backend
make linux
```

Run locally:

```bash
./bin/olcpanel serve
```

Run locally with an explicit SQLite database:

```bash
./bin/olcpanel serve --database-url sqlite:///tmp/olcpanel.db
```

Run with explicit OlcRTC runtime paths:

```bash
./bin/olcpanel serve \
  --runtime-dir /var/lib/olcpanel/runtime \
  --olcrtc-binary /usr/local/bin/olcrtc
```

Apply database migrations without starting the server:

```bash
./bin/olcpanel migrate --database-url sqlite:///tmp/olcpanel.db
```

Create and restore full SQLite backup archives. API restore only accepts known
backup records; CLI restore is intended for stopped-service maintenance:

```bash
./bin/olcpanel backup --database-url sqlite:///tmp/olcpanel.db --output /tmp/olcpanel-backups
./bin/olcpanel restore --database-url sqlite:///tmp/olcpanel.db --file /tmp/olcpanel-backups/olcpanel-backup-20260529-120000.zip
```

Export and append-import portable Panel JSON for settings, clients, and nested
locations. Imported clients receive new IDs and subscription tokens; settings
are applied only when requested:

```bash
./bin/olcpanel export --database-url sqlite:///tmp/olcpanel.db --output /tmp/olcpanel-export.json
./bin/olcpanel import --database-url sqlite:///tmp/olcpanel.db --file /tmp/olcpanel-export.json
./bin/olcpanel import --database-url sqlite:///tmp/olcpanel.db --file /tmp/olcpanel-export.json --apply-settings
```

Reset or create an admin account from the CLI:

```bash
./bin/olcpanel reset-admin --database-url sqlite:///tmp/olcpanel.db --username admin --password 'correct horse battery'
```

The default database URL is `sqlite:///etc/olcpanel/panel.db`. Override it with
`OLCPANEL_DATABASE_URL` or the `--database-url` flag. PostgreSQL-style URLs
(`postgres://` and `postgresql://`) are recognized structurally, but Phase 2
runtime verification is SQLite-focused.

The runtime config directory defaults to `/var/lib/olcpanel/runtime`. Override
it with `OLCPANEL_RUNTIME_DIR` or `--runtime-dir`. For each eligible location,
the supervisor writes `<runtime-dir>/<location_id>/server.yaml` with private
file permissions and launches the configured OlcRTC binary as:

```bash
olcrtc <runtime-dir>/<location_id>/server.yaml
```

The OlcRTC binary defaults to `olcrtc` on `PATH`. Override it with
`OLCPANEL_OLCRTC_BINARY` or `--olcrtc-binary`. If the binary is missing, the
panel still starts; reloads that need to launch active locations fail clearly
and can be retried after installing or pointing to the binary.

Check state:

```bash
curl http://127.0.0.1:8888/api/v1/state
```

Create the first admin account. This endpoint only works while
`setup_required` is `true`:

```bash
curl -c cookies.txt -X POST http://127.0.0.1:8888/api/v1/setup \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"correct horse battery"}'
```

The setup and login responses include a `csrf_token`. Browser-session mutation
requests must send that value in `X-CSRF-Token`:

```bash
curl -b cookies.txt http://127.0.0.1:8888/api/v1/settings
curl -b cookies.txt -X PUT http://127.0.0.1:8888/api/v1/settings \
  -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF_TOKEN" \
  -d '{"ui_locale":"en","public_client_endpoint_enabled":false,"backup_path":"/var/lib/olcpanel/backups","quota_lock_mode":"stop"}'
```

Log in after setup:

```bash
curl -c cookies.txt -X POST http://127.0.0.1:8888/api/v1/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"correct horse battery"}'
```

Create an API key from an authenticated browser session. The raw `olcp_...`
token is returned only once:

```bash
curl -b cookies.txt -X POST http://127.0.0.1:8888/api/v1/api-keys \
  -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF_TOKEN" \
  -d '{"name":"deploy"}'
```

API keys can authenticate protected admin APIs without CSRF:

```bash
curl -H "Authorization: Bearer $OLCPANEL_API_TOKEN" \
  http://127.0.0.1:8888/api/v1/settings
```

## Clients And Subscriptions

Authenticated client responses include a private `subscription_token`. New
clients receive a generated `sub_...` token, and `GET /sub/{token}` serves that
client's enabled locations as `text/plain; charset=utf-8` using the official
`olcrtc://` subscription format:

```bash
curl -H "Authorization: Bearer $OLCPANEL_API_TOKEN" \
  -X POST http://127.0.0.1:8888/api/v1/clients \
  -H 'Content-Type: application/json' \
  -d '{"name":"Client"}'

curl http://127.0.0.1:8888/sub/sub_example_private_token
```

Rotate a private subscription token without rotating location rooms:

```bash
curl -b cookies.txt -X POST http://127.0.0.1:8888/api/v1/clients/cl_example/rotate \
  -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF_TOKEN" \
  -d '{"rotate_subscription_token":true}'
```

After rotation, the old `/sub/{token}` URL returns `404`. The legacy
`/c/{client_id}` plaintext endpoint is disabled by default and only serves the
same subscription document when `public_client_endpoint_enabled` is set to
`true`.
