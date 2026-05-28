package clients_test

import (
	"context"
	"database/sql"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"olcpanel/internal/clients"
	"olcpanel/internal/storage"
)

func TestClientPersistenceLifecycle(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	quota := int64(1024)
	expires := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	created, err := clients.CreateClient(ctx, db, clients.ClientInput{
		Name:       "Acme",
		Enabled:    boolPtr(true),
		ExpiresAt:  &expires,
		QuotaBytes: &quota,
	})
	if err != nil {
		t.Fatalf("CreateClient returned error: %v", err)
	}
	if created.ID == "" || created.Name != "Acme" || !created.Enabled {
		t.Fatalf("created client = %#v, want generated id, name, enabled", created)
	}
	if !strings.HasPrefix(created.SubscriptionToken, "sub_") {
		t.Fatalf("subscription token = %q, want sub_ prefix", created.SubscriptionToken)
	}
	if created.QuotaState != clients.QuotaWithinLimit || created.ExpiryState != clients.ExpiryActive {
		t.Fatalf("states = %s/%s, want within_limit/active", created.QuotaState, created.ExpiryState)
	}

	listed, err := clients.ListClients(ctx, db)
	if err != nil {
		t.Fatalf("ListClients returned error: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("listed clients = %#v, want created client", listed)
	}
	if listed[0].SubscriptionToken != created.SubscriptionToken {
		t.Fatalf("listed subscription token = %q, want %q", listed[0].SubscriptionToken, created.SubscriptionToken)
	}

	disabled := false
	updated, err := clients.UpdateClient(ctx, db, created.ID, clients.ClientInput{Name: "Acme Updated", Enabled: &disabled})
	if err != nil {
		t.Fatalf("UpdateClient returned error: %v", err)
	}
	if updated.Name != "Acme Updated" || updated.Enabled {
		t.Fatalf("updated client = %#v, want updated name and disabled", updated)
	}

	if err := clients.DeleteClient(ctx, db, created.ID); err != nil {
		t.Fatalf("DeleteClient returned error: %v", err)
	}
	if _, err := clients.GetClient(ctx, db, created.ID); err != clients.ErrNotFound {
		t.Fatalf("GetClient after delete error = %v, want ErrNotFound", err)
	}
}

func TestNewClientsReceiveUniqueSubscriptionTokens(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	first, err := clients.CreateClient(ctx, db, clients.ClientInput{Name: "First"})
	if err != nil {
		t.Fatalf("CreateClient first returned error: %v", err)
	}
	second, err := clients.CreateClient(ctx, db, clients.ClientInput{Name: "Second"})
	if err != nil {
		t.Fatalf("CreateClient second returned error: %v", err)
	}

	if first.SubscriptionToken == "" || second.SubscriptionToken == "" {
		t.Fatalf("tokens = %q/%q, want generated tokens", first.SubscriptionToken, second.SubscriptionToken)
	}
	if first.SubscriptionToken == second.SubscriptionToken {
		t.Fatalf("tokens are not unique: %q", first.SubscriptionToken)
	}
}

func TestClientDerivedStates(t *testing.T) {
	now := time.Now().UTC()
	expired := now.Add(-time.Minute)
	future := now.Add(time.Minute)

	for name, tc := range map[string]struct {
		input     clients.Client
		quotaWant clients.QuotaState
		expWant   clients.ExpiryState
	}{
		"unlimited": {
			input:     clients.Client{Enabled: true},
			quotaWant: clients.QuotaUnlimited,
			expWant:   clients.ExpiryNone,
		},
		"quota exceeded": {
			input:     clients.Client{Enabled: true, QuotaBytes: int64Ptr(100), QuotaUsedBytes: 101},
			quotaWant: clients.QuotaExceeded,
			expWant:   clients.ExpiryNone,
		},
		"quota within": {
			input:     clients.Client{Enabled: true, QuotaBytes: int64Ptr(100), QuotaUsedBytes: 99, ExpiresAt: &future},
			quotaWant: clients.QuotaWithinLimit,
			expWant:   clients.ExpiryActive,
		},
		"expired": {
			input:     clients.Client{Enabled: true, ExpiresAt: &expired},
			quotaWant: clients.QuotaUnlimited,
			expWant:   clients.ExpiryExpired,
		},
	} {
		t.Run(name, func(t *testing.T) {
			gotQuota, gotExpiry := clients.DeriveStates(tc.input, now)
			if gotQuota != tc.quotaWant || gotExpiry != tc.expWant {
				t.Fatalf("states = %s/%s, want %s/%s", gotQuota, gotExpiry, tc.quotaWant, tc.expWant)
			}
		})
	}
}

func TestLocationPersistenceValidationAndGeneration(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	client, err := clients.CreateClient(ctx, db, clients.ClientInput{Name: "Client"})
	if err != nil {
		t.Fatalf("CreateClient returned error: %v", err)
	}

	location, err := clients.CreateLocation(ctx, db, client.ID, clients.LocationInput{
		Name:      "Main",
		Provider:  "wbstream",
		Transport: "datachannel",
	})
	if err != nil {
		t.Fatalf("CreateLocation returned error: %v", err)
	}
	if location.ID == "" || location.RoomID == "" || location.DNS != "8.8.8.8:53" {
		t.Fatalf("location = %#v, want generated id, generated room, default dns", location)
	}
	if _, err := hex.DecodeString(location.CryptoKey); err != nil || len(location.CryptoKey) != 64 {
		t.Fatalf("crypto key = %q, want 64-character hex", location.CryptoKey)
	}
	if location.TransportPayload != `{}` {
		t.Fatalf("payload = %q, want {}", location.TransportPayload)
	}
	if location.TransportStability != clients.TransportStable {
		t.Fatalf("stability = %q, want stable", location.TransportStability)
	}

	listed, err := clients.ListLocations(ctx, db, client.ID)
	if err != nil {
		t.Fatalf("ListLocations returned error: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != location.ID {
		t.Fatalf("locations = %#v, want created location", listed)
	}

	updated, err := clients.UpdateLocation(ctx, db, client.ID, location.ID, clients.LocationInput{
		Name:             "Video",
		Enabled:          boolPtr(false),
		Provider:         "jitsi",
		Transport:        "videochannel",
		TransportPayload: `{"codec":"tile","width":640,"height":480,"fps":30,"bitrate":"2M","hw":"none","qr_recovery":"low","qr_size":0}`,
		DNS:              "1.1.1.1:53",
	})
	if err != nil {
		t.Fatalf("UpdateLocation returned error: %v", err)
	}
	if updated.Name != "Video" || updated.Enabled || updated.Transport != "videochannel" || updated.DNS != "1.1.1.1:53" {
		t.Fatalf("updated location = %#v, want updated values", updated)
	}

	if err := clients.DeleteLocation(ctx, db, client.ID, location.ID); err != nil {
		t.Fatalf("DeleteLocation returned error: %v", err)
	}
	if _, err := clients.GetLocation(ctx, db, client.ID, location.ID); err != clients.ErrNotFound {
		t.Fatalf("GetLocation after delete error = %v, want ErrNotFound", err)
	}
}

func TestProviderTransportMatrixAcceptsStableAndUnstableOnly(t *testing.T) {
	for provider, transports := range map[string]map[string]clients.TransportStability{
		"telemost": {"vp8channel": clients.TransportStable, "videochannel": clients.TransportStable},
		"jitsi":    {"datachannel": clients.TransportUnstable, "vp8channel": clients.TransportStable, "seichannel": clients.TransportStable, "videochannel": clients.TransportStable},
		"wbstream": {"datachannel": clients.TransportStable, "vp8channel": clients.TransportStable, "seichannel": clients.TransportStable, "videochannel": clients.TransportStable},
	} {
		for transport, want := range transports {
			t.Run(provider+"_"+transport, func(t *testing.T) {
				got, err := clients.ValidateProviderTransport(provider, transport)
				if err != nil {
					t.Fatalf("ValidateProviderTransport returned error: %v", err)
				}
				if got != want {
					t.Fatalf("stability = %q, want %q", got, want)
				}
			})
		}
	}

	for _, pair := range []struct {
		provider  string
		transport string
	}{
		{"telemost", "datachannel"},
		{"telemost", "seichannel"},
		{"unknown", "datachannel"},
		{"wbstream", "unknown"},
	} {
		t.Run(pair.provider+"_"+pair.transport, func(t *testing.T) {
			if _, err := clients.ValidateProviderTransport(pair.provider, pair.transport); err == nil {
				t.Fatal("ValidateProviderTransport returned nil error, want rejection")
			}
		})
	}
}

func TestTransportPayloadValidationAndDefaults(t *testing.T) {
	for name, tc := range map[string]struct {
		transport string
		input     string
		want      string
		wantErr   bool
	}{
		"data default":    {"datachannel", "", `{}`, false},
		"data empty":      {"datachannel", `{}`, `{}`, false},
		"data rejects":    {"datachannel", `{"fps":60}`, "", true},
		"vp8 defaults":    {"vp8channel", "", `{"batch_size":64,"fps":60}`, false},
		"vp8 validates":   {"vp8channel", `{"fps":30,"batch_size":32}`, `{"batch_size":32,"fps":30}`, false},
		"sei defaults":    {"seichannel", "", `{"ack_timeout_ms":2000,"batch_size":64,"fps":60,"fragment_size":900}`, false},
		"video defaults":  {"videochannel", "", `{"bitrate":"5000k","codec":"qrcode","fps":60,"height":1080,"hw":"none","qr_recovery":"low","qr_size":0,"width":1080}`, false},
		"video official":  {"videochannel", `{"codec":"tile","width":720,"height":720,"fps":30,"bitrate":"2M","hw":"nvenc","qr_recovery":"highest","qr_size":128,"tile_module":8,"tile_rs":20}`, `{"bitrate":"2M","codec":"tile","fps":30,"height":720,"hw":"nvenc","qr_recovery":"highest","qr_size":128,"tile_module":8,"tile_rs":20,"width":720}`, false},
		"video bad codec": {"videochannel", `{"codec":"h264"}`, "", true},
		"invalid json":    {"vp8channel", `{`, "", true},
		"unknown payload": {"unknown", "", "", true},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := clients.NormalizeTransportPayload(tc.transport, tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatal("NormalizeTransportPayload returned nil error, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeTransportPayload returned error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("payload = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestRotateClientLocationsRotatesKeysAndOptionalRooms(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	client, err := clients.CreateClient(ctx, db, clients.ClientInput{Name: "Client"})
	if err != nil {
		t.Fatalf("CreateClient returned error: %v", err)
	}
	location, err := clients.CreateLocation(ctx, db, client.ID, clients.LocationInput{Name: "Main", Provider: "wbstream", Transport: "datachannel"})
	if err != nil {
		t.Fatalf("CreateLocation returned error: %v", err)
	}

	rotatedKeys, err := clients.RotateClientLocations(ctx, db, client.ID, false)
	if err != nil {
		t.Fatalf("RotateClientLocations keys returned error: %v", err)
	}
	if len(rotatedKeys) != 1 {
		t.Fatalf("rotated keys count = %d, want 1", len(rotatedKeys))
	}
	if rotatedKeys[0].CryptoKey == location.CryptoKey {
		t.Fatal("crypto key did not change")
	}
	if rotatedKeys[0].RoomID != location.RoomID {
		t.Fatal("room changed during key-only rotation")
	}

	rotatedRooms, err := clients.RotateClientLocations(ctx, db, client.ID, true)
	if err != nil {
		t.Fatalf("RotateClientLocations rooms returned error: %v", err)
	}
	if rotatedRooms[0].CryptoKey == rotatedKeys[0].CryptoKey {
		t.Fatal("crypto key did not change during room rotation")
	}
	if rotatedRooms[0].RoomID == rotatedKeys[0].RoomID {
		t.Fatal("room did not change during room rotation")
	}
}

func TestRotateClientSubscriptionTokenChangesTokenWithoutRotatingLocations(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	client, err := clients.CreateClient(ctx, db, clients.ClientInput{Name: "Client"})
	if err != nil {
		t.Fatalf("CreateClient returned error: %v", err)
	}
	location, err := clients.CreateLocation(ctx, db, client.ID, clients.LocationInput{Name: "Main", Provider: "wbstream", Transport: "datachannel"})
	if err != nil {
		t.Fatalf("CreateLocation returned error: %v", err)
	}

	rotated, err := clients.RotateClientSubscriptionToken(ctx, db, client.ID)
	if err != nil {
		t.Fatalf("RotateClientSubscriptionToken returned error: %v", err)
	}
	if rotated.SubscriptionToken == "" || rotated.SubscriptionToken == client.SubscriptionToken {
		t.Fatalf("rotated token = %q, original %q", rotated.SubscriptionToken, client.SubscriptionToken)
	}
	unchanged, err := clients.GetLocation(ctx, db, client.ID, location.ID)
	if err != nil {
		t.Fatalf("GetLocation returned error: %v", err)
	}
	if unchanged.RoomID != location.RoomID || unchanged.CryptoKey != location.CryptoKey {
		t.Fatalf("location after subscription rotation = %#v, want unchanged room/key", unchanged)
	}
}

func boolPtr(value bool) *bool {
	return &value
}

func int64Ptr(value int64) *int64 {
	return &value
}

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.Open(context.Background(), "sqlite:///"+filepath.ToSlash(filepath.Join(t.TempDir(), "panel.db")))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := storage.Migrate(context.Background(), db); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	return db
}
