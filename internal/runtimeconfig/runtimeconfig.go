package runtimeconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const serverConfigName = "server.yaml"

type Location struct {
	LocationID       string
	Provider         string
	Transport        string
	RoomID           string
	CryptoKey        string
	TransportPayload string
	DNS              string
}

type Renderer struct {
	runtimeDir string
}

func NewRenderer(runtimeDir string) Renderer {
	return Renderer{runtimeDir: runtimeDir}
}

func (renderer Renderer) Render(location Location) (string, error) {
	path, err := renderer.configPath(location.LocationID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create runtime directory: %w", err)
	}

	body, err := renderYAML(location)
	if err != nil {
		return "", err
	}
	if err := writePrivateFile(path, []byte(body)); err != nil {
		return "", err
	}
	return path, nil
}

func (renderer Renderer) Remove(locationID string) error {
	dir, err := renderer.locationDir(locationID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove runtime directory: %w", err)
	}
	return nil
}

func (renderer Renderer) configPath(locationID string) (string, error) {
	dir, err := renderer.locationDir(locationID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, serverConfigName), nil
}

func (renderer Renderer) locationDir(locationID string) (string, error) {
	if strings.TrimSpace(renderer.runtimeDir) == "" {
		return "", errors.New("runtime dir is required")
	}
	if !safePathName(locationID) {
		return "", fmt.Errorf("invalid location id %q", locationID)
	}
	return filepath.Join(renderer.runtimeDir, locationID), nil
}

func renderYAML(location Location) (string, error) {
	var builder strings.Builder
	builder.WriteString("mode: srv\n")
	builder.WriteString("auth:\n")
	builder.WriteString("  provider: " + location.Provider + "\n")
	builder.WriteString("room:\n")
	builder.WriteString("  id: " + quote(location.RoomID) + "\n")
	builder.WriteString("crypto:\n")
	builder.WriteString("  key: " + quote(location.CryptoKey) + "\n")
	builder.WriteString("net:\n")
	builder.WriteString("  transport: " + location.Transport + "\n")
	builder.WriteString("  dns: " + quote(location.DNS) + "\n")

	payload, err := decodePayload(location.TransportPayload)
	if err != nil {
		return "", err
	}
	switch location.Transport {
	case "datachannel":
	case "vp8channel":
		builder.WriteString("vp8:\n")
		writeInt(&builder, "fps", payload)
		writeInt(&builder, "batch_size", payload)
	case "seichannel":
		builder.WriteString("sei:\n")
		writeInt(&builder, "fps", payload)
		writeInt(&builder, "batch_size", payload)
		writeInt(&builder, "fragment_size", payload)
		writeInt(&builder, "ack_timeout_ms", payload)
	case "videochannel":
		builder.WriteString("video:\n")
		writeString(&builder, "codec", payload)
		writeInt(&builder, "width", payload)
		writeInt(&builder, "height", payload)
		writeInt(&builder, "fps", payload)
		writeString(&builder, "bitrate", payload)
		writeString(&builder, "hw", payload)
		writeString(&builder, "qr_recovery", payload)
		writeInt(&builder, "qr_size", payload)
		writeOptionalInt(&builder, "tile_module", payload)
		writeOptionalInt(&builder, "tile_rs", payload)
	default:
		return "", fmt.Errorf("unsupported transport %q", location.Transport)
	}

	builder.WriteString("data: data\n")
	return builder.String(), nil
}

func decodePayload(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode transport payload: %w", err)
	}
	return payload, nil
}

func writeInt(builder *strings.Builder, key string, payload map[string]any) {
	builder.WriteString("  " + key + ": " + formatInt(payload[key]) + "\n")
}

func writeOptionalInt(builder *strings.Builder, key string, payload map[string]any) {
	value, ok := payload[key]
	if !ok {
		return
	}
	builder.WriteString("  " + key + ": " + formatInt(value) + "\n")
}

func writeString(builder *strings.Builder, key string, payload map[string]any) {
	value, _ := payload[key].(string)
	builder.WriteString("  " + key + ": " + quote(value) + "\n")
}

func formatInt(value any) string {
	switch typed := value.(type) {
	case json.Number:
		if integer, err := typed.Int64(); err == nil {
			return strconv.FormatInt(integer, 10)
		}
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	case int:
		return strconv.Itoa(typed)
	}
	return "0"
}

func quote(value string) string {
	return strconv.Quote(value)
}

func safePathName(value string) bool {
	if strings.TrimSpace(value) == "" || value == "." || value == ".." {
		return false
	}
	return !strings.ContainsAny(value, `/\`)
}

func writePrivateFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".server-*.yaml")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod config: %w", err)
	}
	cleanup = false
	return nil
}
