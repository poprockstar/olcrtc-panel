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

Apply database migrations without starting the server:

```bash
./bin/olcpanel migrate --database-url sqlite:///tmp/olcpanel.db
```

The default database URL is `sqlite:///etc/olcpanel/panel.db`. Override it with
`OLCPANEL_DATABASE_URL` or the `--database-url` flag. PostgreSQL-style URLs
(`postgres://` and `postgresql://`) are recognized structurally, but Phase 2
runtime verification is SQLite-focused.

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
