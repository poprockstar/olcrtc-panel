package runtimeconfig_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"olcpanel/internal/runtimeconfig"
)

func TestRenderDatachannelConfigOmitsTransportSection(t *testing.T) {
	renderer := runtimeconfig.NewRenderer(t.TempDir())

	path, err := renderer.Render(location("datachannel", `{}`))
	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}

	got := readFile(t, path)
	want := strings.Join([]string{
		"mode: srv",
		"auth:",
		"  provider: wbstream",
		"room:",
		"  id: \"room-one\"",
		"crypto:",
		"  key: \"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\"",
		"net:",
		"  transport: datachannel",
		"  dns: \"8.8.8.8:53\"",
		"data: data",
		"",
	}, "\n")
	if got != want {
		t.Fatalf("rendered config:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(got, "vp8:") || strings.Contains(got, "sei:") || strings.Contains(got, "video:") {
		t.Fatalf("datachannel config contains transport payload section:\n%s", got)
	}
	assertPrivateFile(t, path)
}

func TestRenderVP8ConfigMapsPayload(t *testing.T) {
	renderer := runtimeconfig.NewRenderer(t.TempDir())

	path, err := renderer.Render(location("vp8channel", `{"batch_size":32,"fps":30}`))
	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}

	got := readFile(t, path)
	if !strings.Contains(got, "vp8:\n  fps: 30\n  batch_size: 32\n") {
		t.Fatalf("vp8 payload not rendered as expected:\n%s", got)
	}
}

func TestRenderSEIConfigMapsPayload(t *testing.T) {
	renderer := runtimeconfig.NewRenderer(t.TempDir())

	path, err := renderer.Render(location("seichannel", `{"ack_timeout_ms":1500,"batch_size":16,"fps":24,"fragment_size":700}`))
	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}

	got := readFile(t, path)
	want := "sei:\n  fps: 24\n  batch_size: 16\n  fragment_size: 700\n  ack_timeout_ms: 1500\n"
	if !strings.Contains(got, want) {
		t.Fatalf("sei payload not rendered as expected:\n%s", got)
	}
}

func TestRenderVideoConfigMapsAcceptedPayload(t *testing.T) {
	renderer := runtimeconfig.NewRenderer(t.TempDir())

	payload := `{"bitrate":"6000k","codec":"tile","fps":30,"height":1080,"hw":"none","qr_recovery":"high","qr_size":256,"tile_module":8,"tile_rs":30,"width":1080}`
	path, err := renderer.Render(location("videochannel", payload))
	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}

	got := readFile(t, path)
	want := strings.Join([]string{
		"video:",
		"  codec: \"tile\"",
		"  width: 1080",
		"  height: 1080",
		"  fps: 30",
		"  bitrate: \"6000k\"",
		"  hw: \"none\"",
		"  qr_recovery: \"high\"",
		"  qr_size: 256",
		"  tile_module: 8",
		"  tile_rs: 30",
		"",
	}, "\n")
	if !strings.Contains(got, want) {
		t.Fatalf("video payload not rendered as expected:\n%s", got)
	}
}

func TestRenderWritesConfigUnderLocationDirectory(t *testing.T) {
	root := t.TempDir()
	renderer := runtimeconfig.NewRenderer(root)

	path, err := renderer.Render(location("datachannel", `{}`))
	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}

	want := filepath.Join(root, "loc_test", "server.yaml")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	return string(data)
}

func assertPrivateFile(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat returned error: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %v, want 0600", got)
	}
}

func location(transport, payload string) runtimeconfig.Location {
	return runtimeconfig.Location{
		LocationID:       "loc_test",
		Provider:         "wbstream",
		Transport:        transport,
		RoomID:           "room-one",
		CryptoKey:        "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		TransportPayload: payload,
		DNS:              "8.8.8.8:53",
	}
}
