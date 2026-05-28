# OlcRTC Panel

OlcRTC Panel is a local Linux VPS control panel for managing OlcRTC clients, locations, subscriptions, quotas, runtime processes, and network isolation.

The v1 target is a single Linux server:

- one Go binary with embedded React UI;
- default bind address `127.0.0.1:8888`;
- SQLite by default in later storage phases;
- systemd as the primary production runtime;
- Docker as a secondary, documented option because netns/veth/tc needs elevated Linux capabilities.

## Phase 1 Development

Build the frontend:

```bash
npm --prefix frontend install
npm --prefix frontend run build
```

Build the backend binary:

```bash
go build -o bin/olcpanel ./cmd/olcpanel
```

Build a Linux amd64 binary from another platform:

```bash
GOOS=linux GOARCH=amd64 go build -o bin/olcpanel-linux-amd64 ./cmd/olcpanel
```

Run locally:

```bash
./bin/olcpanel serve
```

Check state:

```bash
curl http://127.0.0.1:8888/api/v1/state
```
