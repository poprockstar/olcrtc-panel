package main

import (
	"context"
	"strings"
	"syscall"
	"testing"

	"olcpanel/internal/storage"
	"olcpanel/internal/supervisor"
)

func TestServeRejectsUnexpectedArgument(t *testing.T) {
	err := run([]string{"serve", "unexpected"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "unexpected argument") {
		t.Fatalf("expected unexpected argument error, got %q", err.Error())
	}
}

func TestUsageIncludesDoctorNetworkCIDRAndTrafficSampleIntervalLogPathAndBasePath(t *testing.T) {
	usage := commandUsage()
	for _, want := range []string{"olcpanel doctor", "--network-cidr", "OLCPANEL_NETWORK_CIDR", "--traffic-sample-interval", "OLCPANEL_TRAFFIC_SAMPLE_INTERVAL", "--log-path", "OLCPANEL_LOG_PATH", "--base-path", "OLCPANEL_BASE_PATH"} {
		if !strings.Contains(usage, want) {
			t.Fatalf("usage does not contain %q:\n%s", want, usage)
		}
	}
}

func TestServeRejectsInvalidBasePath(t *testing.T) {
	err := run([]string{"serve", "--base-path", "/api"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "base path") {
		t.Fatalf("expected base path error, got %q", err.Error())
	}
}

func TestServeRejectsInvalidTrafficSampleInterval(t *testing.T) {
	err := run([]string{"serve", "--traffic-sample-interval", "0s"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "traffic sample interval") {
		t.Fatalf("expected traffic sample interval error, got %q", err.Error())
	}
}

func TestDoctorRejectsUnexpectedArgument(t *testing.T) {
	err := run([]string{"doctor", "unexpected"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "unexpected argument") {
		t.Fatalf("expected unexpected argument error, got %q", err.Error())
	}
}

func TestMigrateCommandRunsMigrations(t *testing.T) {
	databaseURL := "sqlite:///" + strings.ReplaceAll(t.TempDir(), "\\", "/") + "/panel.db"

	if err := run([]string{"migrate", "--database-url", databaseURL}); err != nil {
		t.Fatalf("run migrate returned error: %v", err)
	}

	db, err := storage.Open(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	settings, err := storage.GetSettings(context.Background(), db)
	if err != nil {
		t.Fatalf("GetSettings returned error: %v", err)
	}
	if settings.UILocale != "en" {
		t.Fatalf("UILocale = %q, want migrated default", settings.UILocale)
	}
}

func TestHandleServerSignalReloadsOnSIGHUPWithoutShutdown(t *testing.T) {
	reloader := &fakeReloader{err: errReloadFailed{}}

	shutdown, err := handleServerSignal(syscall.SIGHUP, nil, reloader)
	if err != nil {
		t.Fatalf("handleServerSignal returned error: %v", err)
	}
	if shutdown {
		t.Fatal("shutdown = true, want false for best-effort reload")
	}
	if reloader.calls != 1 {
		t.Fatalf("reloader calls = %d, want 1", reloader.calls)
	}
}

type fakeReloader struct {
	calls int
	err   error
}

func (reloader *fakeReloader) Reload(context.Context) (supervisor.ReloadResult, error) {
	reloader.calls++
	return supervisor.ReloadResult{Summary: supervisor.Summary{Started: 1}}, reloader.err
}

type errReloadFailed struct{}

func (errReloadFailed) Error() string {
	return "reload failed"
}
