#!/usr/bin/env bash
set -euo pipefail

REPO="poprockstar/olcrtc-panel"
INSTALL_BIN="/usr/local/bin/olcpanel"
OLCRTC_BIN="/usr/local/bin/olcrtc"
ENV_FILE="/etc/default/olcpanel"
BACKUP_BIN="/usr/local/bin/olcpanel.bak"
BACKUP_OLCRTC_BIN="/usr/local/bin/olcrtc.bak"
DATA_DIR="/var/lib/olcpanel"
OLCRTC_SOURCE_DIR="${DATA_DIR}/olcrtc-src"
OLCRTC_CACHE_DIR="/var/cache/olcpanel/olcrtc"
OLCRTC_IMAGE="${OLCRTC_IMAGE:-docker.io/library/golang:1.26-alpine3.22}"
PANEL_SOURCE_DIR="${DATA_DIR}/panel-src"
PANEL_CACHE_DIR="/var/cache/olcpanel/panel"
PANEL_IMAGE="${PANEL_IMAGE:-${OLCRTC_IMAGE}}"

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

if [ "$(id -u)" -ne 0 ]; then
  echo "This updater must run as root." >&2
  exit 1
fi

if [ ! -x "${INSTALL_BIN}" ]; then
  echo "OlcRTC Panel is not installed at ${INSTALL_BIN}." >&2
  exit 1
fi

if [ -r "${ENV_FILE}" ]; then
  . "${ENV_FILE}"
fi

install_system_dependencies
install -d -m 0755 "${DATA_DIR}" "${OLCRTC_CACHE_DIR}" "${PANEL_CACHE_DIR}"
ensure_swap_for_olcrtc_build

PANEL_PORT="${OLCPANEL_BIND##*:}"
PANEL_PORT="${PANEL_PORT:-8888}"
BASE_PATH="${OLCPANEL_BASE_PATH:-}"
if [ "${BASE_PATH}" = "/" ]; then
  BASE_PATH=""
fi

tmp="$(mktemp -d)"
HAD_OLCRTC=0
cleanup() {
  rm -rf "${tmp}"
}
trap cleanup EXIT

archive_url="${OLCPANEL_DOWNLOAD_URL:-}"
if [ -z "${archive_url}" ]; then
  if [ -n "${OLCPANEL_VERSION:-}" ]; then
    archive_url="https://github.com/${REPO}/releases/download/${OLCPANEL_VERSION}/olcpanel-linux-amd64.tar.gz"
  else
    archive_url="https://github.com/${REPO}/releases/latest/download/olcpanel-linux-amd64.tar.gz"
  fi
fi

if curl -fL "${archive_url}" -o "${tmp}/olcpanel-linux-amd64.tar.gz"; then
  tar -xzf "${tmp}/olcpanel-linux-amd64.tar.gz" -C "${tmp}"
else
  echo "Could not download olcpanel release archive; falling back to source build." >&2
  build_olcpanel "${tmp}/olcpanel"
fi
install_olcrtc "${tmp}/olcrtc"

cp -p "${INSTALL_BIN}" "${BACKUP_BIN}"
if [ -x "${OLCRTC_BIN}" ]; then
  HAD_OLCRTC=1
  cp -p "${OLCRTC_BIN}" "${BACKUP_OLCRTC_BIN}"
fi
install -m 0755 "${tmp}/olcpanel" "${INSTALL_BIN}"
install -m 0755 "${tmp}/olcrtc" "${OLCRTC_BIN}"

rollback() {
  echo "Update failed; rolling back previous binaries." >&2
  mv "${BACKUP_BIN}" "${INSTALL_BIN}"
  if [ -f "${BACKUP_OLCRTC_BIN}" ]; then
    mv "${BACKUP_OLCRTC_BIN}" "${OLCRTC_BIN}"
  elif [ "${HAD_OLCRTC}" = "0" ]; then
    rm -f "${OLCRTC_BIN}"
  fi
  systemctl restart olcpanel || true
}

if ! olcpanel migrate; then
  rollback
  exit 1
fi

if ! systemctl restart olcpanel; then
  rollback
  exit 1
fi

for _ in 1 2 3 4 5 6 7 8 9 10; do
  if curl -fsS "http://127.0.0.1:${PANEL_PORT}${BASE_PATH}/api/v1/state" >/dev/null; then
    rm -f "${BACKUP_BIN}" "${BACKUP_OLCRTC_BIN}"
    echo "OlcRTC Panel updated successfully."
    exit 0
  fi
  sleep 2
done

rollback
exit 1
