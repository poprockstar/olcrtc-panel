#!/usr/bin/env bash
set -euo pipefail

REPO="poprockstar/olcrtc-panel"
INSTALL_BIN="/usr/local/bin/olcpanel"
ENV_FILE="/etc/default/olcpanel"
BACKUP_BIN="/usr/local/bin/olcpanel.bak"

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

PANEL_PORT="${OLCPANEL_BIND##*:}"
PANEL_PORT="${PANEL_PORT:-8888}"
BASE_PATH="${OLCPANEL_BASE_PATH:-}"
if [ "${BASE_PATH}" = "/" ]; then
  BASE_PATH=""
fi

tmp="$(mktemp -d)"
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

curl -fL "${archive_url}" -o "${tmp}/olcpanel-linux-amd64.tar.gz"
tar -xzf "${tmp}/olcpanel-linux-amd64.tar.gz" -C "${tmp}"

cp -p "${INSTALL_BIN}" "${BACKUP_BIN}"
install -m 0755 "${tmp}/olcpanel" "${INSTALL_BIN}"

rollback() {
  echo "Update failed; rolling back previous binary." >&2
  mv "${BACKUP_BIN}" "${INSTALL_BIN}"
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
    rm -f "${BACKUP_BIN}"
    echo "OlcRTC Panel updated successfully."
    exit 0
  fi
  sleep 2
done

rollback
exit 1
