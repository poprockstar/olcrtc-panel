#!/usr/bin/env bash
set -euo pipefail

REPO="poprockstar/olcrtc-panel"
INSTALL_BIN="/usr/local/bin/olcpanel"
ENV_FILE="/etc/default/olcpanel"
SERVICE_FILE="/etc/systemd/system/olcpanel.service"
DATA_DIR="/var/lib/olcpanel"
RUNTIME_DIR="${DATA_DIR}/runtime"
BACKUP_DIR="${DATA_DIR}/backups"
LOG_DIR="/var/log/olcpanel"

require_root() {
  if [ "$(id -u)" -ne 0 ]; then
    echo "This installer must run as root." >&2
    exit 1
  fi
}

require_debian_ubuntu() {
  if [ ! -r /etc/os-release ]; then
    echo "This installer supports Debian/Ubuntu amd64 hosts." >&2
    exit 1
  fi
  . /etc/os-release
  case "${ID:-}" in
    debian|ubuntu) ;;
    *)
      case " ${ID_LIKE:-} " in
        *" debian "*) ;;
        *) echo "This installer supports Debian/Ubuntu amd64 hosts." >&2; exit 1 ;;
      esac
      ;;
  esac
  if [ "$(uname -m)" != "x86_64" ]; then
    echo "This installer currently supports amd64/x86_64 only." >&2
    exit 1
  fi
}

normalize_base_path() {
  local raw="${1:-}"
  raw="${raw#"${raw%%[![:space:]]*}"}"
  raw="${raw%"${raw##*[![:space:]]}"}"
  if [ -z "${raw}" ] || [ "${raw}" = "/" ]; then
    printf ''
    return
  fi
  case "${raw}" in
    *[[:space:]]*|*\?*|*#*|*"/.."*|*"../"*|*"/."*|*"./"*)
      echo "Invalid URI path: ${raw}" >&2
      exit 1
      ;;
  esac
  case "${raw}" in
    /*) ;;
    *) raw="/${raw}" ;;
  esac
  while [ "${raw%/}" != "${raw}" ]; do raw="${raw%/}"; done
  case "${raw}" in
    /api|/api/*|/sub|/sub/*|/c|/c/*|/assets|/assets/*)
      echo "URI path uses a reserved route prefix: ${raw}" >&2
      exit 1
      ;;
  esac
  printf '%s' "${raw}"
}

download_binary() {
  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "${tmp}"' RETURN

  if [ -n "${OLCPANEL_BINARY:-}" ]; then
    install -m 0755 "${OLCPANEL_BINARY}" "${INSTALL_BIN}"
    return
  fi

  local archive_url="${OLCPANEL_DOWNLOAD_URL:-}"
  if [ -z "${archive_url}" ]; then
    if [ -n "${OLCPANEL_VERSION:-}" ]; then
      archive_url="https://github.com/${REPO}/releases/download/${OLCPANEL_VERSION}/olcpanel-linux-amd64.tar.gz"
    else
      archive_url="https://github.com/${REPO}/releases/latest/download/olcpanel-linux-amd64.tar.gz"
    fi
  fi

  curl -fL "${archive_url}" -o "${tmp}/olcpanel-linux-amd64.tar.gz"
  tar -xzf "${tmp}/olcpanel-linux-amd64.tar.gz" -C "${tmp}"
  install -m 0755 "${tmp}/olcpanel" "${INSTALL_BIN}"
}

detect_server_ip() {
  local ip_addr
  ip_addr="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i=1;i<=NF;i++) if ($i=="src") {print $(i+1); exit}}')"
  if [ -z "${ip_addr}" ]; then
    ip_addr="$(hostname -I 2>/dev/null | awk '{print $1}')"
  fi
  printf '%s' "${ip_addr:-VDS_SERVER_IP}"
}

require_root
require_debian_ubuntu

read -r -p "Panel port [8888]: " PANEL_PORT
PANEL_PORT="${PANEL_PORT:-8888}"
case "${PANEL_PORT}" in
  ''|*[!0-9]*) echo "Panel port must be numeric." >&2; exit 1 ;;
esac

read -r -p "Optional URI path, for example /panel [root]: " RAW_BASE_PATH
BASE_PATH="$(normalize_base_path "${RAW_BASE_PATH:-}")"

install -d -m 0755 "${DATA_DIR}" "${RUNTIME_DIR}" "${BACKUP_DIR}" "${LOG_DIR}" /etc/olcpanel
download_binary

cat > "${ENV_FILE}" <<EOF
OLCPANEL_BIND=0.0.0.0:${PANEL_PORT}
OLCPANEL_BASE_PATH=${BASE_PATH}
OLCPANEL_DATABASE_URL=sqlite:///etc/olcpanel/panel.db
OLCPANEL_RUNTIME_DIR=${RUNTIME_DIR}
OLCPANEL_LOG_PATH=${LOG_DIR}/panel.log
EOF
chmod 0644 "${ENV_FILE}"

install -m 0644 deploy/olcpanel.service "${SERVICE_FILE}" 2>/dev/null || cat > "${SERVICE_FILE}" <<'EOF'
[Unit]
Description=OlcRTC Panel
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=-/etc/default/olcpanel
ExecStartPre=/usr/local/bin/olcpanel migrate
ExecStart=/usr/local/bin/olcpanel serve
ExecReload=/bin/kill -HUP $MAINPID
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
EOF

olcpanel migrate
systemctl daemon-reload
systemctl enable --now olcpanel

SERVER_IP="$(detect_server_ip)"
echo "OlcRTC Panel is installed and running."
echo "http://${SERVER_IP}:${PANEL_PORT}${BASE_PATH}/"
