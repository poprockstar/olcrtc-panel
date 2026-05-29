package olcpanel_test

import (
	"os"
	"strings"
	"testing"
)

func TestInstallerScriptConfiguresPublicBindBasePathAndSystemd(t *testing.T) {
	body := readTextFile(t, "install.sh")

	for _, want := range []string{
		"/etc/default/olcpanel",
		"OLCPANEL_BIND=0.0.0.0:${PANEL_PORT}",
		"OLCPANEL_BASE_PATH=${BASE_PATH}",
		"systemctl enable --now olcpanel",
		"olcpanel migrate",
		"http://${SERVER_IP}:${PANEL_PORT}${BASE_PATH}/",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("install.sh does not contain %q", want)
		}
	}
}

func TestUpdateScriptPreservesConfigAndRollsBackOnHealthFailure(t *testing.T) {
	body := readTextFile(t, "update.sh")

	for _, want := range []string{
		"/etc/default/olcpanel",
		"OLCPANEL_VERSION",
		"cp -p \"${INSTALL_BIN}\" \"${BACKUP_BIN}\"",
		"olcpanel migrate",
		"systemctl restart olcpanel",
		"curl -fsS \"http://127.0.0.1:${PANEL_PORT}${BASE_PATH}/api/v1/state\"",
		"mv \"${BACKUP_BIN}\" \"${INSTALL_BIN}\"",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("update.sh does not contain %q", want)
		}
	}
}

func TestSystemdUnitLoadsEnvMigratesAndReloadsWithSIGHUP(t *testing.T) {
	body := readTextFile(t, "deploy/olcpanel.service")

	for _, want := range []string{
		"EnvironmentFile=-/etc/default/olcpanel",
		"ExecStartPre=/usr/local/bin/olcpanel migrate",
		"ExecStart=/usr/local/bin/olcpanel serve",
		"ExecReload=/bin/kill -HUP $MAINPID",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("olcpanel.service does not contain %q", want)
		}
	}
}

func TestReleaseWorkflowBuildsLinuxAmd64ArchiveAndChecksum(t *testing.T) {
	body := readTextFile(t, ".github/workflows/release.yml")

	for _, want := range []string{
		"tags:",
		"'v*'",
		"GOOS=linux GOARCH=amd64 go build",
		"olcpanel-linux-amd64.tar.gz",
		"sha256sum olcpanel-linux-amd64.tar.gz",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("release workflow does not contain %q", want)
		}
	}
}

func readTextFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
