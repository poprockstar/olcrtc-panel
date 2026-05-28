package subscriptions

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"olcpanel/internal/clients"
)

var ErrNoEnabledLocations = errors.New("no enabled locations")

type Snapshot struct {
	Client    clients.Client
	Locations []clients.Location
	UpdatedAt time.Time
	Refresh   string
}

func Render(snapshot Snapshot) (string, error) {
	enabled := make([]clients.Location, 0, len(snapshot.Locations))
	for _, location := range snapshot.Locations {
		if location.Enabled {
			enabled = append(enabled, location)
		}
	}
	if len(enabled) == 0 {
		return "", ErrNoEnabledLocations
	}

	updatedAt := snapshot.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	refresh := strings.TrimSpace(snapshot.Refresh)
	if refresh == "" {
		refresh = "10m"
	}
	used, available := quotaFields(snapshot.Client)

	var builder strings.Builder
	fmt.Fprintf(&builder, "#name: %s\n", snapshot.Client.Name)
	fmt.Fprintf(&builder, "#update: %d\n", updatedAt.Unix())
	fmt.Fprintf(&builder, "#refresh: %s\n", refresh)
	fmt.Fprintf(&builder, "#used: %s\n", used)
	fmt.Fprintf(&builder, "#available: %s\n", available)

	for _, location := range enabled {
		uri, err := RenderURI(snapshot.Client.Name, location)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&builder, "%s\n", uri)
		fmt.Fprintf(&builder, "##name: %s\n", location.Name)
		fmt.Fprintf(&builder, "##used: %s\n", used)
		fmt.Fprintf(&builder, "##available: %s\n", available)
		fmt.Fprintf(&builder, "##comment: %s\n", locationComment(location))
	}
	return builder.String(), nil
}

func RenderURI(clientName string, location clients.Location) (string, error) {
	payload, err := renderPayload(location.Transport, string(location.TransportPayload))
	if err != nil {
		return "", err
	}
	comment := strings.TrimSpace(clientName)
	if comment != "" && strings.TrimSpace(location.Name) != "" {
		comment += " / " + strings.TrimSpace(location.Name)
	} else if comment == "" {
		comment = strings.TrimSpace(location.Name)
	}
	return fmt.Sprintf("olcrtc://%s?%s%s@%s#%s$%s",
		location.Provider,
		location.Transport,
		payload,
		location.RoomID,
		location.CryptoKey,
		comment,
	), nil
}

type payloadField struct {
	source string
	alias  string
}

func renderPayload(transport, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" || transport == "datachannel" {
		return "", nil
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return "", fmt.Errorf("decode transport payload: %w", err)
	}
	if len(values) == 0 {
		return "", nil
	}

	fields, defaults := payloadContract(transport)
	if len(fields) == 0 {
		return "", nil
	}
	if payloadMatchesDefaults(values, defaults) {
		return "", nil
	}

	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		value, ok := values[field.source]
		if !ok {
			continue
		}
		parts = append(parts, field.alias+"="+formatPayloadValue(value))
	}
	if len(parts) == 0 {
		return "", nil
	}
	return "<" + strings.Join(parts, "&") + ">", nil
}

func payloadContract(transport string) ([]payloadField, map[string]any) {
	switch transport {
	case "vp8channel":
		return []payloadField{
				{source: "fps", alias: "vp8-fps"},
				{source: "batch_size", alias: "vp8-batch"},
			}, map[string]any{
				"fps":        float64(60),
				"batch_size": float64(64),
			}
	case "seichannel":
		return []payloadField{
				{source: "fps", alias: "fps"},
				{source: "batch_size", alias: "batch"},
				{source: "fragment_size", alias: "frag"},
				{source: "ack_timeout_ms", alias: "ack-ms"},
			}, map[string]any{
				"fps":            float64(60),
				"batch_size":     float64(64),
				"fragment_size":  float64(900),
				"ack_timeout_ms": float64(2000),
			}
	case "videochannel":
		return []payloadField{
				{source: "width", alias: "video-w"},
				{source: "height", alias: "video-h"},
				{source: "fps", alias: "video-fps"},
				{source: "bitrate", alias: "video-bitrate"},
				{source: "hw", alias: "video-hw"},
				{source: "codec", alias: "video-codec"},
				{source: "qr_size", alias: "video-qr-size"},
				{source: "qr_recovery", alias: "video-qr-recovery"},
				{source: "tile_module", alias: "video-tile-module"},
				{source: "tile_rs", alias: "video-tile-rs"},
			}, map[string]any{
				"width":       float64(1080),
				"height":      float64(1080),
				"fps":         float64(60),
				"bitrate":     "5000k",
				"hw":          "none",
				"codec":       "qrcode",
				"qr_size":     float64(0),
				"qr_recovery": "low",
			}
	default:
		return nil, nil
	}
}

func payloadMatchesDefaults(values, defaults map[string]any) bool {
	for key, want := range defaults {
		got, ok := values[key]
		if !ok || !payloadValuesEqual(got, want) {
			return false
		}
	}
	for key, got := range values {
		if _, ok := defaults[key]; ok {
			continue
		}
		if !payloadValuesEqual(got, float64(0)) && !payloadValuesEqual(got, "") {
			return false
		}
	}
	return true
}

func payloadValuesEqual(got, want any) bool {
	switch wantValue := want.(type) {
	case float64:
		gotNumber, ok := got.(float64)
		return ok && gotNumber == wantValue
	case string:
		gotText, ok := got.(string)
		return ok && gotText == wantValue
	default:
		return got == want
	}
}

func formatPayloadValue(value any) string {
	switch typed := value.(type) {
	case float64:
		if typed == math.Trunc(typed) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	default:
		return fmt.Sprint(typed)
	}
}

func quotaFields(client clients.Client) (string, string) {
	used := formatBytes(client.QuotaUsedBytes)
	if client.QuotaBytes == nil {
		return used + "/unlimited", "unlimited"
	}
	total := *client.QuotaBytes
	available := total - client.QuotaUsedBytes
	if available < 0 {
		available = 0
	}
	return used + "/" + formatBytes(total), formatBytes(available)
}

func formatBytes(value int64) string {
	if value == 0 {
		return "0b"
	}
	units := []struct {
		name string
		size int64
	}{
		{"tb", 1024 * 1024 * 1024 * 1024},
		{"gb", 1024 * 1024 * 1024},
		{"mb", 1024 * 1024},
		{"kb", 1024},
	}
	for _, unit := range units {
		if value >= unit.size {
			amount := float64(value) / float64(unit.size)
			if math.Trunc(amount) == amount {
				return strconv.FormatInt(int64(amount), 10) + unit.name
			}
			return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(amount, 'f', 2, 64), "0"), ".") + unit.name
		}
	}
	return strconv.FormatInt(value, 10) + "b"
}

func locationComment(location clients.Location) string {
	stability := strings.TrimSpace(string(location.TransportStability))
	status := strings.TrimSpace(location.RuntimeStatus)
	switch {
	case stability != "" && status != "":
		return stability + " / " + status
	case stability != "":
		return stability
	case status != "":
		return status
	default:
		return "pending"
	}
}
