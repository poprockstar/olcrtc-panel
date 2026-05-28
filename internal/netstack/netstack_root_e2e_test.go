//go:build linux && root_e2e

package netstack_test

import (
	"context"
	"os"
	"testing"

	"olcpanel/internal/netstack"
)

func TestRootE2EEnsureAndCleanupAreRepeatable(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root_e2e requires root or equivalent network capabilities")
	}
	ctx := context.Background()
	speed := int64(1000000)
	stack := netstack.New(netstack.Options{NetworkCIDR: "10.254.0.0/16"})
	state := netstack.LocationState{
		LocationID:     "loc_root_e2e_phase8",
		DNS:            "1.1.1.1:53",
		SpeedLimitBPS:  &speed,
		TrafficEnabled: true,
	}
	t.Cleanup(func() {
		_ = stack.Cleanup(context.Background(), state.LocationID)
	})

	if err := stack.Ensure(ctx, state); err != nil {
		t.Fatalf("first Ensure returned error: %v", err)
	}
	if err := stack.Ensure(ctx, state); err != nil {
		t.Fatalf("second Ensure returned error: %v", err)
	}
	if err := stack.Cleanup(ctx, state.LocationID); err != nil {
		t.Fatalf("first Cleanup returned error: %v", err)
	}
	if err := stack.Cleanup(ctx, state.LocationID); err != nil {
		t.Fatalf("second Cleanup returned error: %v", err)
	}
}
