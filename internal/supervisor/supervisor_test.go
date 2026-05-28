package supervisor_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"olcpanel/internal/clients"
	"olcpanel/internal/storage"
	"olcpanel/internal/supervisor"
)

func TestReloadStartsNewActiveLocations(t *testing.T) {
	db := testDB(t)
	_, location := seedClientLocation(t, db)
	runner := &recordingRunner{}
	sup := supervisor.New(db, supervisor.WithRunner(runner), supervisor.WithClock(fixedClock()))

	result, err := sup.Reload(context.Background())
	if err != nil {
		t.Fatalf("Reload returned error: %v", err)
	}

	assertSummary(t, result.Summary, supervisor.Summary{Started: 1})
	assertAction(t, result.Actions, location.ID, supervisor.ActionStarted, supervisor.ReasonNew)
	if got := runner.calls; len(got) != 1 || got[0] != "start:"+location.ID {
		t.Fatalf("runner calls = %#v, want start for location", got)
	}
}

func TestReloadLeavesUnchangedLocationsRunning(t *testing.T) {
	db := testDB(t)
	_, location := seedClientLocation(t, db)
	runner := &recordingRunner{}
	sup := supervisor.New(db, supervisor.WithRunner(runner), supervisor.WithClock(fixedClock()))
	if _, err := sup.Reload(context.Background()); err != nil {
		t.Fatalf("initial Reload returned error: %v", err)
	}
	runner.calls = nil

	result, err := sup.Reload(context.Background())
	if err != nil {
		t.Fatalf("Reload returned error: %v", err)
	}

	assertSummary(t, result.Summary, supervisor.Summary{Unchanged: 1})
	assertAction(t, result.Actions, location.ID, supervisor.ActionUnchanged, supervisor.ReasonUnchanged)
	if len(runner.calls) != 0 {
		t.Fatalf("runner calls = %#v, want none", runner.calls)
	}
}

func TestReloadRestartsOnlyChangedLocations(t *testing.T) {
	db := testDB(t)
	client, changed := seedClientLocation(t, db)
	unchanged := createLocation(t, db, client.ID, "Secondary", "wbstream", "datachannel")
	runner := &recordingRunner{}
	sup := supervisor.New(db, supervisor.WithRunner(runner), supervisor.WithClock(fixedClock()))
	if _, err := sup.Reload(context.Background()); err != nil {
		t.Fatalf("initial Reload returned error: %v", err)
	}
	runner.calls = nil

	updateLocation(t, db, client.ID, changed.ID, clients.LocationInput{
		Name:      "Changed",
		Provider:  "wbstream",
		Transport: "seichannel",
	})
	result, err := sup.Reload(context.Background())
	if err != nil {
		t.Fatalf("Reload returned error: %v", err)
	}

	assertSummary(t, result.Summary, supervisor.Summary{Restarted: 1, Unchanged: 1})
	assertAction(t, result.Actions, changed.ID, supervisor.ActionRestarted, supervisor.ReasonChanged)
	assertAction(t, result.Actions, unchanged.ID, supervisor.ActionUnchanged, supervisor.ReasonUnchanged)
	if got := runner.calls; len(got) != 1 || got[0] != "restart:"+changed.ID {
		t.Fatalf("runner calls = %#v, want restart for changed location only", got)
	}
}

func TestReloadStopsRemovedLocations(t *testing.T) {
	db := testDB(t)
	client, location := seedClientLocation(t, db)
	runner := &recordingRunner{}
	sup := supervisor.New(db, supervisor.WithRunner(runner), supervisor.WithClock(fixedClock()))
	if _, err := sup.Reload(context.Background()); err != nil {
		t.Fatalf("initial Reload returned error: %v", err)
	}
	runner.calls = nil

	if err := clients.DeleteLocation(context.Background(), db, client.ID, location.ID); err != nil {
		t.Fatalf("DeleteLocation returned error: %v", err)
	}
	result, err := sup.Reload(context.Background())
	if err != nil {
		t.Fatalf("Reload returned error: %v", err)
	}

	assertSummary(t, result.Summary, supervisor.Summary{Stopped: 1})
	assertAction(t, result.Actions, location.ID, supervisor.ActionStopped, supervisor.ReasonRemoved)
	if got := runner.calls; len(got) != 1 || got[0] != "stop:"+location.ID {
		t.Fatalf("runner calls = %#v, want stop for removed location", got)
	}
}

func TestReloadStopsRunningLocationsForDisabledClient(t *testing.T) {
	db := testDB(t)
	client, location := seedClientLocation(t, db)
	runner := &recordingRunner{}
	sup := supervisor.New(db, supervisor.WithRunner(runner), supervisor.WithClock(fixedClock()))
	if _, err := sup.Reload(context.Background()); err != nil {
		t.Fatalf("initial Reload returned error: %v", err)
	}
	runner.calls = nil

	disabled := false
	if _, err := clients.UpdateClient(context.Background(), db, client.ID, clients.ClientInput{Name: client.Name, Enabled: &disabled}); err != nil {
		t.Fatalf("UpdateClient returned error: %v", err)
	}
	result, err := sup.Reload(context.Background())
	if err != nil {
		t.Fatalf("Reload returned error: %v", err)
	}

	assertSummary(t, result.Summary, supervisor.Summary{Stopped: 1})
	assertAction(t, result.Actions, location.ID, supervisor.ActionStopped, supervisor.ReasonDisabledClient)
}

func TestReloadStopsRunningDisabledLocations(t *testing.T) {
	db := testDB(t)
	client, location := seedClientLocation(t, db)
	runner := &recordingRunner{}
	sup := supervisor.New(db, supervisor.WithRunner(runner), supervisor.WithClock(fixedClock()))
	if _, err := sup.Reload(context.Background()); err != nil {
		t.Fatalf("initial Reload returned error: %v", err)
	}
	runner.calls = nil

	disabled := false
	updateLocation(t, db, client.ID, location.ID, clients.LocationInput{
		Name:      location.Name,
		Enabled:   &disabled,
		Provider:  location.Provider,
		Transport: location.Transport,
	})
	result, err := sup.Reload(context.Background())
	if err != nil {
		t.Fatalf("Reload returned error: %v", err)
	}

	assertSummary(t, result.Summary, supervisor.Summary{Stopped: 1})
	assertAction(t, result.Actions, location.ID, supervisor.ActionStopped, supervisor.ReasonDisabledLocation)
}

func TestReloadStopsRunningLocationsForExpiredClients(t *testing.T) {
	db := testDB(t)
	client, location := seedClientLocation(t, db)
	runner := &recordingRunner{}
	sup := supervisor.New(db, supervisor.WithRunner(runner), supervisor.WithClock(fixedClock()))
	if _, err := sup.Reload(context.Background()); err != nil {
		t.Fatalf("initial Reload returned error: %v", err)
	}
	runner.calls = nil

	expiresAt := fixedNow().Add(-time.Minute)
	if _, err := clients.UpdateClient(context.Background(), db, client.ID, clients.ClientInput{Name: client.Name, ExpiresAt: &expiresAt}); err != nil {
		t.Fatalf("UpdateClient returned error: %v", err)
	}
	result, err := sup.Reload(context.Background())
	if err != nil {
		t.Fatalf("Reload returned error: %v", err)
	}

	assertSummary(t, result.Summary, supervisor.Summary{Stopped: 1})
	assertAction(t, result.Actions, location.ID, supervisor.ActionStopped, supervisor.ReasonExpiredClient)
}

func TestReloadStopsQuotaExceededClientsWhenQuotaLockModeIsStop(t *testing.T) {
	db := testDB(t)
	client, location := seedClientLocation(t, db)
	runner := &recordingRunner{}
	sup := supervisor.New(db, supervisor.WithRunner(runner), supervisor.WithClock(fixedClock()))
	if _, err := sup.Reload(context.Background()); err != nil {
		t.Fatalf("initial Reload returned error: %v", err)
	}
	runner.calls = nil

	if _, err := db.ExecContext(context.Background(), `UPDATE clients SET quota_bytes = 100, quota_used_bytes = 100 WHERE id = ?`, client.ID); err != nil {
		t.Fatalf("mark quota exceeded: %v", err)
	}
	result, err := sup.Reload(context.Background())
	if err != nil {
		t.Fatalf("Reload returned error: %v", err)
	}

	assertSummary(t, result.Summary, supervisor.Summary{Stopped: 1})
	assertAction(t, result.Actions, location.ID, supervisor.ActionStopped, supervisor.ReasonQuotaLocked)
}

func TestReloadSkipsInactiveLocationsThatAreNotRunning(t *testing.T) {
	db := testDB(t)
	client, location := seedClientLocation(t, db)
	disabled := false
	updateLocation(t, db, client.ID, location.ID, clients.LocationInput{
		Name:      location.Name,
		Enabled:   &disabled,
		Provider:  location.Provider,
		Transport: location.Transport,
	})
	sup := supervisor.New(db, supervisor.WithClock(fixedClock()))

	result, err := sup.Reload(context.Background())
	if err != nil {
		t.Fatalf("Reload returned error: %v", err)
	}

	assertSummary(t, result.Summary, supervisor.Summary{Skipped: 1})
	assertAction(t, result.Actions, location.ID, supervisor.ActionSkipped, supervisor.ReasonDisabledLocation)
}

type recordingRunner struct {
	calls []string
}

func (runner *recordingRunner) Start(_ context.Context, location supervisor.LocationState) error {
	runner.calls = append(runner.calls, "start:"+location.LocationID)
	return nil
}

func (runner *recordingRunner) Restart(_ context.Context, _, location supervisor.LocationState) error {
	runner.calls = append(runner.calls, "restart:"+location.LocationID)
	return nil
}

func (runner *recordingRunner) Stop(_ context.Context, location supervisor.LocationState) error {
	runner.calls = append(runner.calls, "stop:"+location.LocationID)
	return nil
}

func assertSummary(t *testing.T, got, want supervisor.Summary) {
	t.Helper()
	if got != want {
		t.Fatalf("summary = %#v, want %#v", got, want)
	}
}

func assertAction(t *testing.T, actions []supervisor.ActionResult, locationID string, action supervisor.Action, reason supervisor.Reason) {
	t.Helper()
	for _, got := range actions {
		if got.LocationID == locationID {
			if got.Action != action || got.Reason != reason {
				t.Fatalf("action for %s = %#v, want action %q reason %q", locationID, got, action, reason)
			}
			return
		}
	}
	t.Fatalf("actions = %#v, missing location %s", actions, locationID)
}

func seedClientLocation(t *testing.T, db *sql.DB) (clients.Client, clients.Location) {
	t.Helper()
	client, err := clients.CreateClient(context.Background(), db, clients.ClientInput{Name: "Client"})
	if err != nil {
		t.Fatalf("CreateClient returned error: %v", err)
	}
	location := createLocation(t, db, client.ID, "Main", "wbstream", "datachannel")
	return client, location
}

func createLocation(t *testing.T, db *sql.DB, clientID, name, provider, transport string) clients.Location {
	t.Helper()
	location, err := clients.CreateLocation(context.Background(), db, clientID, clients.LocationInput{
		Name:      name,
		Provider:  provider,
		Transport: transport,
	})
	if err != nil {
		t.Fatalf("CreateLocation returned error: %v", err)
	}
	return location
}

func updateLocation(t *testing.T, db *sql.DB, clientID, locationID string, input clients.LocationInput) {
	t.Helper()
	if _, err := clients.UpdateLocation(context.Background(), db, clientID, locationID, input); err != nil {
		t.Fatalf("UpdateLocation returned error: %v", err)
	}
}

func fixedClock() func() time.Time {
	return fixedNow
}

func fixedNow() time.Time {
	return time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
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
