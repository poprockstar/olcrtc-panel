package subscriptions_test

import (
	"strings"
	"testing"
	"time"

	"olcpanel/internal/clients"
	"olcpanel/internal/subscriptions"
)

const testKey = "d823fa01cb3e0609b67322f7cf984c4ee2e4ce2e294936fc24ef38c9e59f4799"

func TestRenderURIsForSupportedTransports(t *testing.T) {
	for name, tc := range map[string]struct {
		location clients.Location
		want     string
	}{
		"datachannel omits payload": {
			location: clients.Location{
				Name:             "Data",
				Provider:         "wbstream",
				Transport:        "datachannel",
				RoomID:           "room-data",
				CryptoKey:        testKey,
				TransportPayload: `{}`,
			},
			want: "olcrtc://wbstream?datachannel@room-data#" + testKey + "$Client / Data",
		},
		"vp8channel converts aliases": {
			location: clients.Location{
				Name:             "VP8",
				Provider:         "wbstream",
				Transport:        "vp8channel",
				RoomID:           "room-vp8",
				CryptoKey:        testKey,
				TransportPayload: `{"batch_size":32,"fps":30}`,
			},
			want: "olcrtc://wbstream?vp8channel<vp8-fps=30&vp8-batch=32>@room-vp8#" + testKey + "$Client / VP8",
		},
		"seichannel converts aliases": {
			location: clients.Location{
				Name:             "SEI",
				Provider:         "jitsi",
				Transport:        "seichannel",
				RoomID:           "room-sei",
				CryptoKey:        testKey,
				TransportPayload: `{"ack_timeout_ms":1500,"batch_size":16,"fps":24,"fragment_size":700}`,
			},
			want: "olcrtc://jitsi?seichannel<fps=24&batch=16&frag=700&ack-ms=1500>@room-sei#" + testKey + "$Client / SEI",
		},
		"videochannel converts aliases": {
			location: clients.Location{
				Name:      "Video",
				Provider:  "telemost",
				Transport: "videochannel",
				RoomID:    "room-video",
				CryptoKey: testKey,
				TransportPayload: `{
					"width":720,
					"height":720,
					"fps":30,
					"bitrate":"2M",
					"hw":"nvenc",
					"codec":"tile",
					"qr_size":128,
					"qr_recovery":"high",
					"tile_module":8,
					"tile_rs":20
				}`,
			},
			want: "olcrtc://telemost?videochannel<video-w=720&video-h=720&video-fps=30&video-bitrate=2M&video-hw=nvenc&video-codec=tile&video-qr-size=128&video-qr-recovery=high&video-tile-module=8&video-tile-rs=20>@room-video#" + testKey + "$Client / Video",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := subscriptions.RenderURI("Client", tc.location)
			if err != nil {
				t.Fatalf("RenderURI returned error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("URI = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRenderURIOmitsDefaultPayloads(t *testing.T) {
	got, err := subscriptions.RenderURI("Client", clients.Location{
		Name:             "Default VP8",
		Provider:         "wbstream",
		Transport:        "vp8channel",
		RoomID:           "room-default",
		CryptoKey:        testKey,
		TransportPayload: `{"batch_size":64,"fps":60}`,
	})
	if err != nil {
		t.Fatalf("RenderURI returned error: %v", err)
	}
	if strings.Contains(got, "<") || strings.Contains(got, ">") {
		t.Fatalf("URI = %q, want default payload omitted", got)
	}
}

func TestRenderPlaintextSubscriptionIncludesMetadataAndEnabledLocations(t *testing.T) {
	quota := int64(1024 * 1024 * 1024)
	got, err := subscriptions.Render(subscriptions.Snapshot{
		Client: clients.Client{
			Name:           "Acme",
			Enabled:        true,
			QuotaBytes:     &quota,
			QuotaUsedBytes: 10 * 1024 * 1024,
		},
		Locations: []clients.Location{
			{
				Name:               "Primary",
				Enabled:            true,
				Provider:           "wbstream",
				Transport:          "datachannel",
				TransportStability: clients.TransportStable,
				RuntimeStatus:      "pending",
				RoomID:             "room-01",
				CryptoKey:          testKey,
				TransportPayload:   `{}`,
			},
			{
				Name:             "Disabled",
				Enabled:          false,
				Provider:         "wbstream",
				Transport:        "datachannel",
				RoomID:           "room-disabled",
				CryptoKey:        testKey,
				TransportPayload: `{}`,
			},
		},
		UpdatedAt: time.Unix(1778011200, 0).UTC(),
		Refresh:   "10m",
	})
	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}

	want := strings.Join([]string{
		"#name: Acme",
		"#update: 1778011200",
		"#refresh: 10m",
		"#used: 10mb/1gb",
		"#available: 1014mb",
		"olcrtc://wbstream?datachannel@room-01#" + testKey + "$Acme / Primary",
		"##name: Primary",
		"##used: 10mb/1gb",
		"##available: 1014mb",
		"##comment: stable / pending",
		"",
	}, "\n")
	if got != want {
		t.Fatalf("subscription:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(got, "Disabled") || strings.Contains(got, "room-disabled") {
		t.Fatalf("subscription includes disabled location:\n%s", got)
	}
}

func TestRenderReturnsNotFoundForNoEnabledLocations(t *testing.T) {
	_, err := subscriptions.Render(subscriptions.Snapshot{
		Client: clients.Client{Name: "Acme", Enabled: true},
		Locations: []clients.Location{{
			Name:      "Disabled",
			Enabled:   false,
			Provider:  "wbstream",
			Transport: "datachannel",
			RoomID:    "room-disabled",
			CryptoKey: testKey,
		}},
	})
	if err != subscriptions.ErrNoEnabledLocations {
		t.Fatalf("Render error = %v, want ErrNoEnabledLocations", err)
	}
}
