#!/usr/bin/env bash
set -euo pipefail

REPO="poprockstar/olcrtc-panel"
INSTALL_BIN="/usr/local/bin/olcpanel"
OLCRTC_BIN="/usr/local/bin/olcrtc"
ENV_FILE="/etc/default/olcpanel"
SERVICE_FILE="/etc/systemd/system/olcpanel.service"
DATA_DIR="/var/lib/olcpanel"
RUNTIME_DIR="${DATA_DIR}/runtime"
BACKUP_DIR="${DATA_DIR}/backups"
LOG_DIR="/var/log/olcpanel"
OLCRTC_SOURCE_DIR="${DATA_DIR}/olcrtc-src"
OLCRTC_CACHE_DIR="/var/cache/olcpanel/olcrtc"
OLCRTC_IMAGE="${OLCRTC_IMAGE:-docker.io/library/golang:1.26-alpine3.22}"
PANEL_SOURCE_DIR="${DATA_DIR}/panel-src"
PANEL_CACHE_DIR="/var/cache/olcpanel/panel"
PANEL_IMAGE="${PANEL_IMAGE:-${OLCRTC_IMAGE}}"

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

install_system_dependencies() {
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y --no-install-recommends ca-certificates curl tar git podman iproute2 iptables procps
}

ensure_swap_for_olcrtc_build() {
  if [ "${OLCPANEL_SKIP_SWAP:-}" = "1" ]; then
    echo "Skipping swap setup because OLCPANEL_SKIP_SWAP=1."
    return
  fi

  local total_kb min_kb
  total_kb="$(awk '/^(MemTotal|SwapTotal):/ { total += $2 } END { print total + 0 }' /proc/meminfo)"
  min_kb=$((4 * 1024 * 1024))
  if [ "${total_kb}" -ge "${min_kb}" ]; then
    return
  fi

  if swapon --show=NAME --noheadings 2>/dev/null | awk '$1 == "/swapfile" { found = 1 } END { exit found ? 0 : 1 }'; then
    return
  fi

  echo "Less than 4GB RAM+swap detected; preparing /swapfile for OlcRTC build."
  if [ ! -e /swapfile ]; then
    fallocate -l 4G /swapfile || dd if=/dev/zero of=/swapfile bs=1M count=4096
    chmod 600 /swapfile
    mkswap /swapfile
  fi
  swapon /swapfile
  grep -qsE '^[[:space:]]*/swapfile[[:space:]]+none[[:space:]]+swap[[:space:]]' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab
}

install_olcrtc() {
  local target="${1:-${OLCRTC_BIN}}"
  local repo_url="${OLCRTC_REPO_URL:-https://github.com/openlibrecommunity/olcrtc}"
  local ref="${OLCRTC_REF:-master}"
  local gomod_cache="${OLCRTC_CACHE_DIR}/gomod"
  local gobuild_cache="${OLCRTC_CACHE_DIR}/gobuild"

  install -d -m 0755 "${OLCRTC_SOURCE_DIR}" "${OLCRTC_CACHE_DIR}"
  if [ "${OLCRTC_NO_CACHE:-}" = "1" ]; then
    chmod -R u+w "${gomod_cache}" "${gobuild_cache}" 2>/dev/null || true
    rm -rf "${gomod_cache}" "${gobuild_cache}"
  fi
  install -d -m 0755 "${gomod_cache}" "${gobuild_cache}"

  if [ ! -d "${OLCRTC_SOURCE_DIR}/.git" ]; then
    rm -rf "${OLCRTC_SOURCE_DIR}"
    git clone --recurse-submodules "${repo_url}" "${OLCRTC_SOURCE_DIR}"
  else
    git -C "${OLCRTC_SOURCE_DIR}" remote set-url origin "${repo_url}"
    git -C "${OLCRTC_SOURCE_DIR}" fetch --tags origin
  fi

  if git -C "${OLCRTC_SOURCE_DIR}" rev-parse --verify --quiet "origin/${ref}^{commit}" >/dev/null; then
    git -C "${OLCRTC_SOURCE_DIR}" checkout -B "${ref}" "origin/${ref}"
  else
    git -C "${OLCRTC_SOURCE_DIR}" checkout -f "${ref}"
  fi
  git -C "${OLCRTC_SOURCE_DIR}" submodule sync --recursive
  git -C "${OLCRTC_SOURCE_DIR}" submodule update --init --recursive

  podman pull "${OLCRTC_IMAGE}"
  podman run --rm \
    --network host \
    -v "${OLCRTC_SOURCE_DIR}":/app:Z \
    -v "${gomod_cache}":/go/pkg/mod:Z \
    -v "${gobuild_cache}":/root/.cache/go-build:Z \
    -w /app \
    "${OLCRTC_IMAGE}" \
    sh -c "go mod download && go build -trimpath -ldflags='-s -w' -o olcrtc ./cmd/olcrtc"

  if [ ! -f "${OLCRTC_SOURCE_DIR}/olcrtc" ]; then
    echo "OlcRTC build did not produce ${OLCRTC_SOURCE_DIR}/olcrtc." >&2
    exit 1
  fi
  install -m 0755 "${OLCRTC_SOURCE_DIR}/olcrtc" "${target}"
}

build_olcpanel() {
  local target="${1:-${INSTALL_BIN}}"
  local repo_url="${OLCPANEL_REPO_URL:-https://github.com/${REPO}.git}"
  local ref="${OLCPANEL_REF:-${OLCPANEL_VERSION:-master}}"
  local gomod_cache="${PANEL_CACHE_DIR}/gomod"
  local gobuild_cache="${PANEL_CACHE_DIR}/gobuild"

  install -d -m 0755 "${PANEL_SOURCE_DIR}" "${PANEL_CACHE_DIR}"
  if [ "${OLCPANEL_NO_CACHE:-}" = "1" ]; then
    chmod -R u+w "${gomod_cache}" "${gobuild_cache}" 2>/dev/null || true
    rm -rf "${gomod_cache}" "${gobuild_cache}"
  fi
  install -d -m 0755 "${gomod_cache}" "${gobuild_cache}"

  if [ ! -d "${PANEL_SOURCE_DIR}/.git" ]; then
    rm -rf "${PANEL_SOURCE_DIR}"
    git clone "${repo_url}" "${PANEL_SOURCE_DIR}"
  else
    git -C "${PANEL_SOURCE_DIR}" remote set-url origin "${repo_url}"
    git -C "${PANEL_SOURCE_DIR}" fetch --tags origin
  fi

  if git -C "${PANEL_SOURCE_DIR}" rev-parse --verify --quiet "origin/${ref}^{commit}" >/dev/null; then
    git -C "${PANEL_SOURCE_DIR}" checkout -B "${ref}" "origin/${ref}"
  else
    git -C "${PANEL_SOURCE_DIR}" checkout -f "${ref}"
  fi

  echo "Building olcpanel from source (${ref})."
  podman pull "${PANEL_IMAGE}"
  podman run --rm \
    --network host \
    -v "${PANEL_SOURCE_DIR}":/app:Z \
    -v "${gomod_cache}":/go/pkg/mod:Z \
    -v "${gobuild_cache}":/root/.cache/go-build:Z \
    -w /app \
    "${PANEL_IMAGE}" \
    sh -c "go build -trimpath -ldflags='-s -w' -o olcpanel ./cmd/olcpanel"

  if [ ! -f "${PANEL_SOURCE_DIR}/olcpanel" ]; then
    echo "olcpanel build did not produce ${PANEL_SOURCE_DIR}/olcpanel." >&2
    exit 1
  fi
  install -m 0755 "${PANEL_SOURCE_DIR}/olcpanel" "${target}"
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

  if curl -fL "${archive_url}" -o "${tmp}/olcpanel-linux-amd64.tar.gz"; then
    tar -xzf "${tmp}/olcpanel-linux-amd64.tar.gz" -C "${tmp}"
    install -m 0755 "${tmp}/olcpanel" "${INSTALL_BIN}"
    return
  fi

  echo "Could not download olcpanel release archive; falling back to source build." >&2
  build_olcpanel "${INSTALL_BIN}"
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
install_system_dependencies

read -r -p "Panel port [8888]: " PANEL_PORT
PANEL_PORT="${PANEL_PORT:-8888}"
case "${PANEL_PORT}" in
  ''|*[!0-9]*) echo "Panel port must be numeric." >&2; exit 1 ;;
esac

read -r -p "Optional URI path, for example /panel [root]: " RAW_BASE_PATH
BASE_PATH="$(normalize_base_path "${RAW_BASE_PATH:-}")"

install -d -m 0755 "${DATA_DIR}" "${RUNTIME_DIR}" "${BACKUP_DIR}" "${LOG_DIR}" /etc/olcpanel "${OLCRTC_CACHE_DIR}" "${PANEL_CACHE_DIR}"
ensure_swap_for_olcrtc_build
install_olcrtc "${OLCRTC_BIN}"
download_binary

cat > "${ENV_FILE}" <<EOF
OLCPANEL_BIND=0.0.0.0:${PANEL_PORT}
OLCPANEL_BASE_PATH=${BASE_PATH}
OLCPANEL_DATABASE_URL=sqlite:///etc/olcpanel/panel.db
OLCPANEL_RUNTIME_DIR=${RUNTIME_DIR}
OLCPANEL_OLCRTC_BINARY=/usr/local/bin/olcrtc
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
