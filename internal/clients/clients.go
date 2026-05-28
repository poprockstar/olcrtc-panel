package clients

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

const localNodeID = "local"

var ErrNotFound = errors.New("not found")

type QuotaState string

const (
	QuotaUnlimited   QuotaState = "unlimited"
	QuotaWithinLimit QuotaState = "within_limit"
	QuotaExceeded    QuotaState = "exceeded"
)

type ExpiryState string

const (
	ExpiryNone    ExpiryState = "none"
	ExpiryActive  ExpiryState = "active"
	ExpiryExpired ExpiryState = "expired"
)

type TransportStability string

const (
	TransportStable   TransportStability = "stable"
	TransportUnstable TransportStability = "unstable"
)

type RawPayload string

func (payload RawPayload) MarshalJSON() ([]byte, error) {
	if strings.TrimSpace(string(payload)) == "" {
		return []byte(`{}`), nil
	}
	if !json.Valid([]byte(payload)) {
		return nil, errors.New("transport payload is not valid JSON")
	}
	return []byte(payload), nil
}

type Client struct {
	ID             string      `json:"id"`
	Name           string      `json:"name"`
	Enabled        bool        `json:"enabled"`
	ExpiresAt      *time.Time  `json:"expires_at"`
	QuotaBytes     *int64      `json:"quota_bytes"`
	QuotaUsedBytes int64       `json:"quota_used_bytes"`
	QuotaState     QuotaState  `json:"quota_state"`
	ExpiryState    ExpiryState `json:"expiry_state"`
	LocationsCount int         `json:"locations_count"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

type ClientInput struct {
	Name       string     `json:"name"`
	Enabled    *bool      `json:"enabled"`
	ExpiresAt  *time.Time `json:"expires_at"`
	QuotaBytes *int64     `json:"quota_bytes"`
}

type Location struct {
	ID                 string             `json:"id"`
	ClientID           string             `json:"client_id"`
	Name               string             `json:"name"`
	Enabled            bool               `json:"enabled"`
	Provider           string             `json:"provider"`
	Transport          string             `json:"transport"`
	TransportStability TransportStability `json:"transport_stability"`
	RoomID             string             `json:"room_id"`
	CryptoKey          string             `json:"crypto_key"`
	TransportPayload   RawPayload         `json:"transport_payload"`
	DNS                string             `json:"dns"`
	RuntimeStatus      string             `json:"runtime_status"`
	CreatedAt          time.Time          `json:"created_at"`
	UpdatedAt          time.Time          `json:"updated_at"`
}

type LocationInput struct {
	Name             string `json:"name"`
	Enabled          *bool  `json:"enabled"`
	Provider         string `json:"provider"`
	Transport        string `json:"transport"`
	RoomID           string `json:"room_id"`
	CryptoKey        string `json:"crypto_key"`
	TransportPayload string `json:"transport_payload"`
	DNS              string `json:"dns"`
}

func CreateClient(ctx context.Context, db *sql.DB, input ClientInput) (Client, error) {
	name, enabled, err := normalizeClientInput(input)
	if err != nil {
		return Client{}, err
	}
	id, err := randomID("cl")
	if err != nil {
		return Client{}, err
	}
	if input.QuotaBytes != nil && *input.QuotaBytes < 0 {
		return Client{}, errors.New("quota_bytes must be null or non-negative")
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO clients(id, node_id, name, enabled, expires_at, quota_bytes, quota_used_bytes, updated_at)
VALUES (?, ?, ?, ?, ?, ?, 0, CURRENT_TIMESTAMP)`,
		id, localNodeID, name, boolInt(enabled), formatNullableTime(input.ExpiresAt), nullableInt64(input.QuotaBytes)); err != nil {
		return Client{}, fmt.Errorf("insert client: %w", err)
	}
	return GetClient(ctx, db, id)
}

func ListClients(ctx context.Context, db *sql.DB) ([]Client, error) {
	rows, err := db.QueryContext(ctx, `
SELECT c.id, c.name, c.enabled, c.expires_at, c.quota_bytes, c.quota_used_bytes, COUNT(l.id), c.created_at, c.updated_at
FROM clients c
LEFT JOIN locations l ON l.client_id = c.id
WHERE c.node_id = ?
GROUP BY c.id
ORDER BY c.created_at, c.id`, localNodeID)
	if err != nil {
		return nil, fmt.Errorf("list clients: %w", err)
	}
	defer rows.Close()

	var result []Client
	for rows.Next() {
		client, err := scanClient(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, client)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate clients: %w", err)
	}
	return result, nil
}

func GetClient(ctx context.Context, db *sql.DB, id string) (Client, error) {
	row := db.QueryRowContext(ctx, `
SELECT c.id, c.name, c.enabled, c.expires_at, c.quota_bytes, c.quota_used_bytes, COUNT(l.id), c.created_at, c.updated_at
FROM clients c
LEFT JOIN locations l ON l.client_id = c.id
WHERE c.node_id = ? AND c.id = ?
GROUP BY c.id`, localNodeID, id)
	client, err := scanClient(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Client{}, ErrNotFound
	}
	return client, err
}

func UpdateClient(ctx context.Context, db *sql.DB, id string, input ClientInput) (Client, error) {
	name, enabled, err := normalizeClientInput(input)
	if err != nil {
		return Client{}, err
	}
	if input.QuotaBytes != nil && *input.QuotaBytes < 0 {
		return Client{}, errors.New("quota_bytes must be null or non-negative")
	}
	result, err := db.ExecContext(ctx, `
UPDATE clients
SET name = ?, enabled = ?, expires_at = ?, quota_bytes = ?, updated_at = CURRENT_TIMESTAMP
WHERE node_id = ? AND id = ?`,
		name, boolInt(enabled), formatNullableTime(input.ExpiresAt), nullableInt64(input.QuotaBytes), localNodeID, id)
	if err != nil {
		return Client{}, fmt.Errorf("update client: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return Client{}, ErrNotFound
	}
	return GetClient(ctx, db, id)
}

func DeleteClient(ctx context.Context, db *sql.DB, id string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete client: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM locations WHERE node_id = ? AND client_id = ?`, localNodeID, id); err != nil {
		return fmt.Errorf("delete client locations: %w", err)
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM clients WHERE node_id = ? AND id = ?`, localNodeID, id)
	if err != nil {
		return fmt.Errorf("delete client: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete client: %w", err)
	}
	return nil
}

func CreateLocation(ctx context.Context, db *sql.DB, clientID string, input LocationInput) (Location, error) {
	if _, err := GetClient(ctx, db, clientID); err != nil {
		return Location{}, err
	}
	normalized, err := normalizeLocationInput(input)
	if err != nil {
		return Location{}, err
	}
	id, err := randomID("loc")
	if err != nil {
		return Location{}, err
	}
	if normalized.RoomID == "" {
		normalized.RoomID, err = generateRoomID(normalized.Provider)
		if err != nil {
			return Location{}, err
		}
	}
	if normalized.CryptoKey == "" {
		normalized.CryptoKey, err = GenerateCryptoKey()
		if err != nil {
			return Location{}, err
		}
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO locations(id, node_id, client_id, name, enabled, provider, transport, room_id, crypto_key, transport_payload, dns, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		id, localNodeID, clientID, normalized.Name, boolInt(*normalized.Enabled), normalized.Provider, normalized.Transport, normalized.RoomID, normalized.CryptoKey, normalized.TransportPayload, normalized.DNS); err != nil {
		return Location{}, fmt.Errorf("insert location: %w", err)
	}
	return GetLocation(ctx, db, clientID, id)
}

func ListLocations(ctx context.Context, db *sql.DB, clientID string) ([]Location, error) {
	if _, err := GetClient(ctx, db, clientID); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
SELECT id, client_id, name, enabled, provider, transport, room_id, crypto_key, transport_payload, dns, created_at, updated_at
FROM locations
WHERE node_id = ? AND client_id = ?
ORDER BY created_at, id`, localNodeID, clientID)
	if err != nil {
		return nil, fmt.Errorf("list locations: %w", err)
	}
	defer rows.Close()

	var result []Location
	for rows.Next() {
		location, err := scanLocation(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, location)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate locations: %w", err)
	}
	return result, nil
}

func GetLocation(ctx context.Context, db *sql.DB, clientID, id string) (Location, error) {
	row := db.QueryRowContext(ctx, `
SELECT id, client_id, name, enabled, provider, transport, room_id, crypto_key, transport_payload, dns, created_at, updated_at
FROM locations
WHERE node_id = ? AND client_id = ? AND id = ?`, localNodeID, clientID, id)
	location, err := scanLocation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Location{}, ErrNotFound
	}
	return location, err
}

func UpdateLocation(ctx context.Context, db *sql.DB, clientID, id string, input LocationInput) (Location, error) {
	if _, err := GetClient(ctx, db, clientID); err != nil {
		return Location{}, err
	}
	existing, err := GetLocation(ctx, db, clientID, id)
	if err != nil {
		return Location{}, err
	}
	normalized, err := normalizeLocationInput(input)
	if err != nil {
		return Location{}, err
	}
	if normalized.RoomID == "" {
		normalized.RoomID = existing.RoomID
	}
	if normalized.CryptoKey == "" {
		normalized.CryptoKey = existing.CryptoKey
	}
	result, err := db.ExecContext(ctx, `
UPDATE locations
SET name = ?, enabled = ?, provider = ?, transport = ?, room_id = ?, crypto_key = ?, transport_payload = ?, dns = ?, updated_at = CURRENT_TIMESTAMP
WHERE node_id = ? AND client_id = ? AND id = ?`,
		normalized.Name, boolInt(*normalized.Enabled), normalized.Provider, normalized.Transport, normalized.RoomID, normalized.CryptoKey, normalized.TransportPayload, normalized.DNS, localNodeID, clientID, id)
	if err != nil {
		return Location{}, fmt.Errorf("update location: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return Location{}, ErrNotFound
	}
	return GetLocation(ctx, db, clientID, id)
}

func DeleteLocation(ctx context.Context, db *sql.DB, clientID, id string) error {
	result, err := db.ExecContext(ctx, `DELETE FROM locations WHERE node_id = ? AND client_id = ? AND id = ?`, localNodeID, clientID, id)
	if err != nil {
		return fmt.Errorf("delete location: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

func RotateClientLocations(ctx context.Context, db *sql.DB, clientID string, rotateRooms bool) ([]Location, error) {
	locations, err := ListLocations(ctx, db, clientID)
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin rotate locations: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for i := range locations {
		key, err := GenerateCryptoKey()
		if err != nil {
			return nil, err
		}
		locations[i].CryptoKey = key
		if rotateRooms {
			room, err := generateRoomID(locations[i].Provider)
			if err != nil {
				return nil, err
			}
			locations[i].RoomID = room
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE locations
SET crypto_key = ?, room_id = ?, updated_at = CURRENT_TIMESTAMP
WHERE node_id = ? AND client_id = ? AND id = ?`, locations[i].CryptoKey, locations[i].RoomID, localNodeID, clientID, locations[i].ID); err != nil {
			return nil, fmt.Errorf("rotate location %s: %w", locations[i].ID, err)
		}
		locations[i].UpdatedAt = time.Now().UTC()
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit rotate locations: %w", err)
	}
	return ListLocations(ctx, db, clientID)
}

func DeriveStates(client Client, now time.Time) (QuotaState, ExpiryState) {
	quota := QuotaUnlimited
	if client.QuotaBytes != nil {
		if client.QuotaUsedBytes >= *client.QuotaBytes {
			quota = QuotaExceeded
		} else {
			quota = QuotaWithinLimit
		}
	}
	expiry := ExpiryNone
	if client.ExpiresAt != nil {
		if !client.ExpiresAt.After(now) {
			expiry = ExpiryExpired
		} else {
			expiry = ExpiryActive
		}
	}
	return quota, expiry
}

func ValidateProviderTransport(provider, transport string) (TransportStability, error) {
	matrix := map[string]map[string]TransportStability{
		"telemost": {
			"vp8channel":   TransportStable,
			"videochannel": TransportStable,
		},
		"jitsi": {
			"datachannel":  TransportUnstable,
			"vp8channel":   TransportStable,
			"seichannel":   TransportStable,
			"videochannel": TransportStable,
		},
		"wbstream": {
			"datachannel":  TransportStable,
			"vp8channel":   TransportStable,
			"seichannel":   TransportStable,
			"videochannel": TransportStable,
		},
	}
	transports, ok := matrix[provider]
	if !ok {
		return "", fmt.Errorf("provider must be one of telemost, wbstream, jitsi")
	}
	stability, ok := transports[transport]
	if !ok {
		return "", fmt.Errorf("transport %q is not supported by provider %q", transport, provider)
	}
	return stability, nil
}

func NormalizeTransportPayload(transport, payload string) (string, error) {
	payload = strings.TrimSpace(payload)
	switch transport {
	case "datachannel":
		if payload == "" {
			return `{}`, nil
		}
		var object map[string]any
		if err := json.Unmarshal([]byte(payload), &object); err != nil {
			return "", errors.New("transport_payload must be a JSON object")
		}
		if len(object) != 0 {
			return "", errors.New("datachannel transport_payload must be empty")
		}
		return `{}`, nil
	case "vp8channel":
		defaults := map[string]any{"fps": float64(60), "batch_size": float64(64)}
		return normalizeObjectPayload(payload, defaults, map[string]validator{
			"fps":        positiveInteger,
			"batch_size": positiveInteger,
		})
	case "seichannel":
		defaults := map[string]any{"fps": float64(60), "batch_size": float64(64), "fragment_size": float64(900), "ack_timeout_ms": float64(2000)}
		return normalizeObjectPayload(payload, defaults, map[string]validator{
			"fps":            positiveInteger,
			"batch_size":     positiveInteger,
			"fragment_size":  positiveInteger,
			"ack_timeout_ms": positiveInteger,
		})
	case "videochannel":
		defaults := map[string]any{
			"codec":       "qrcode",
			"width":       float64(1080),
			"height":      float64(1080),
			"fps":         float64(60),
			"bitrate":     "5000k",
			"hw":          "none",
			"qr_recovery": "low",
			"qr_size":     float64(0),
		}
		return normalizeObjectPayload(payload, defaults, map[string]validator{
			"codec":       oneOf("qrcode", "tile"),
			"width":       positiveInteger,
			"height":      positiveInteger,
			"fps":         positiveInteger,
			"bitrate":     nonEmptyString,
			"hw":          oneOf("none", "auto"),
			"qr_recovery": oneOf("low", "medium", "quartile", "high"),
			"qr_size":     nonNegativeInteger,
		})
	default:
		return "", errors.New("transport must be one of datachannel, vp8channel, seichannel, videochannel")
	}
}

func GenerateCryptoKey() (string, error) {
	var data [32]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", fmt.Errorf("generate crypto key: %w", err)
	}
	return hex.EncodeToString(data[:]), nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanClient(row scanner) (Client, error) {
	var client Client
	var enabled int
	var expires sql.NullString
	var quota sql.NullInt64
	var created, updated string
	if err := row.Scan(&client.ID, &client.Name, &enabled, &expires, &quota, &client.QuotaUsedBytes, &client.LocationsCount, &created, &updated); err != nil {
		return Client{}, err
	}
	client.Enabled = enabled != 0
	client.ExpiresAt = nullableTime(expires)
	if quota.Valid {
		value := quota.Int64
		client.QuotaBytes = &value
	}
	client.CreatedAt, _ = parseTime(created)
	client.UpdatedAt, _ = parseTime(updated)
	client.QuotaState, client.ExpiryState = DeriveStates(client, time.Now().UTC())
	return client, nil
}

func scanLocation(row scanner) (Location, error) {
	var location Location
	var enabled int
	var payload string
	var created, updated string
	if err := row.Scan(&location.ID, &location.ClientID, &location.Name, &enabled, &location.Provider, &location.Transport, &location.RoomID, &location.CryptoKey, &payload, &location.DNS, &created, &updated); err != nil {
		return Location{}, err
	}
	location.Enabled = enabled != 0
	location.TransportPayload = RawPayload(payload)
	location.TransportStability, _ = ValidateProviderTransport(location.Provider, location.Transport)
	location.RuntimeStatus = "pending"
	location.CreatedAt, _ = parseTime(created)
	location.UpdatedAt, _ = parseTime(updated)
	return location, nil
}

func normalizeClientInput(input ClientInput) (string, bool, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" || len(name) > 120 {
		return "", false, errors.New("client name is required and must be at most 120 characters")
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	return name, enabled, nil
}

func normalizeLocationInput(input LocationInput) (LocationInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len(input.Name) > 120 {
		return LocationInput{}, errors.New("location name is required and must be at most 120 characters")
	}
	input.Provider = strings.TrimSpace(input.Provider)
	input.Transport = strings.TrimSpace(input.Transport)
	if _, err := ValidateProviderTransport(input.Provider, input.Transport); err != nil {
		return LocationInput{}, err
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	input.Enabled = &enabled
	input.RoomID = strings.TrimSpace(input.RoomID)
	input.CryptoKey = strings.TrimSpace(input.CryptoKey)
	if input.CryptoKey != "" {
		if len(input.CryptoKey) != 64 {
			return LocationInput{}, errors.New("crypto_key must be 64 hex characters")
		}
		if _, err := hex.DecodeString(input.CryptoKey); err != nil {
			return LocationInput{}, errors.New("crypto_key must be 64 hex characters")
		}
	}
	payload, err := NormalizeTransportPayload(input.Transport, input.TransportPayload)
	if err != nil {
		return LocationInput{}, err
	}
	input.TransportPayload = payload
	input.DNS = strings.TrimSpace(input.DNS)
	if input.DNS == "" {
		input.DNS = "8.8.8.8:53"
	}
	if _, _, err := net.SplitHostPort(input.DNS); err != nil {
		return LocationInput{}, errors.New("dns must be host:port")
	}
	return input, nil
}

func generateRoomID(provider string) (string, error) {
	id, err := randomID("room")
	if err != nil {
		return "", err
	}
	if provider == "jitsi" {
		return "olcpanel-" + strings.ReplaceAll(id, "_", "-"), nil
	}
	return id, nil
}

func randomID(prefix string) (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(data[:]), nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func formatNullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func parseTime(value string) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed, nil
	}
	return time.Parse("2006-01-02 15:04:05", value)
}

func nullableTime(value sql.NullString) *time.Time {
	if !value.Valid {
		return nil
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil
	}
	return &parsed
}

type validator func(any) error

func normalizeObjectPayload(payload string, defaults map[string]any, validators map[string]validator) (string, error) {
	values := make(map[string]any, len(defaults))
	for key, value := range defaults {
		values[key] = value
	}
	if payload != "" {
		var supplied map[string]any
		if err := json.Unmarshal([]byte(payload), &supplied); err != nil {
			return "", errors.New("transport_payload must be a JSON object")
		}
		for key, value := range supplied {
			validate, ok := validators[key]
			if !ok {
				return "", fmt.Errorf("unsupported transport_payload field %q", key)
			}
			if err := validate(value); err != nil {
				return "", fmt.Errorf("%s %w", key, err)
			}
			values[key] = value
		}
	}
	for key, validate := range validators {
		if err := validate(values[key]); err != nil {
			return "", fmt.Errorf("%s %w", key, err)
		}
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("encode transport_payload: %w", err)
	}
	return string(data), nil
}

func positiveInteger(value any) error {
	number, ok := value.(float64)
	if !ok || number <= 0 || number != float64(int64(number)) {
		return errors.New("must be a positive integer")
	}
	return nil
}

func nonNegativeInteger(value any) error {
	number, ok := value.(float64)
	if !ok || number < 0 || number != float64(int64(number)) {
		return errors.New("must be a non-negative integer")
	}
	return nil
}

func nonEmptyString(value any) error {
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return errors.New("must be a non-empty string")
	}
	return nil
}

func oneOf(options ...string) validator {
	return func(value any) error {
		text, ok := value.(string)
		if !ok {
			return errors.New("must be a string")
		}
		for _, option := range options {
			if text == option {
				return nil
			}
		}
		return fmt.Errorf("must be one of %s", strings.Join(options, ", "))
	}
}
