package olcpanel_test

import (
	"os"
	"strings"
	"testing"
)

func TestInstallScriptPreparesOlcRTCRuntime(t *testing.T) {
	script := readScript(t, "install.sh")

	requireContainsAll(t, script,
		"install_system_dependencies",
		"apt-get install -y",
		"ca-certificates",
		"curl",
		"tar",
		"git",
		"podman",
		"iproute2",
		"iptables",
		"procps",
		"ensure_swap_for_olcrtc_build",
		"OLCPANEL_SKIP_SWAP",
		"/swapfile",
		"fallocate -l 4G",
		"mkswap /swapfile",
		"swapon /swapfile",
		"install_olcrtc",
		"OLCRTC_REPO_URL",
		"OLCRTC_REF",
		"OLCRTC_NO_CACHE",
		"docker.io/library/golang:1.26-alpine3.22",
		"go build -trimpath -ldflags='-s -w' -o olcrtc ./cmd/olcrtc",
		"/usr/local/bin/olcrtc",
		"OLCPANEL_OLCRTC_BINARY=/usr/local/bin/olcrtc",
	)
}

func TestInstallScriptFallsBackToBuildingPanelFromSource(t *testing.T) {
	script := readScript(t, "install.sh")

	requireContainsAll(t, script,
		"PANEL_SOURCE_DIR",
		"PANEL_CACHE_DIR",
		"build_olcpanel",
		"OLCPANEL_REPO_URL",
		"OLCPANEL_REF",
		"OLCPANEL_NO_CACHE",
		`if curl -fL "${archive_url}" -o "${tmp}/olcpanel-linux-amd64.tar.gz"; then`,
		"Building olcpanel from source",
		"go build -trimpath -ldflags='-s -w' -o olcpanel ./cmd/olcpanel",
	)
}

func TestUpdateScriptUpdatesAndRollsBackOlcRTC(t *testing.T) {
	script := readScript(t, "update.sh")

	requireContainsAll(t, script,
		"install_system_dependencies",
		"ensure_swap_for_olcrtc_build",
		"install_olcrtc",
		"OLCRTC_REPO_URL",
		"OLCRTC_REF",
		"OLCRTC_NO_CACHE",
		"docker.io/library/golang:1.26-alpine3.22",
		"go build -trimpath -ldflags='-s -w' -o olcrtc ./cmd/olcrtc",
		`BACKUP_OLCRTC_BIN="/usr/local/bin/olcrtc.bak"`,
		"rollback",
		`mv "${BACKUP_OLCRTC_BIN}" "${OLCRTC_BIN}"`,
	)
}

func TestUpdateScriptFallsBackToBuildingPanelFromSource(t *testing.T) {
	script := readScript(t, "update.sh")

	requireContainsAll(t, script,
		"PANEL_SOURCE_DIR",
		"PANEL_CACHE_DIR",
		"build_olcpanel",
		"OLCPANEL_REPO_URL",
		"OLCPANEL_REF",
		"OLCPANEL_NO_CACHE",
		`if curl -fL "${archive_url}" -o "${tmp}/olcpanel-linux-amd64.tar.gz"; then`,
		"Building olcpanel from source",
		"go build -trimpath -ldflags='-s -w' -o olcpanel ./cmd/olcpanel",
	)
}

func readScript(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func requireContainsAll(t *testing.T, haystack string, needles ...string) {
	t.Helper()
	for _, needle := range needles {
		if !strings.Contains(haystack, needle) {
			t.Fatalf("script missing %q", needle)
		}
	}
}
