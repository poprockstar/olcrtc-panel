package supervisor_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"olcpanel/internal/netstack"
	"olcpanel/internal/observability"
	"olcpanel/internal/supervisor"
)

func TestMain(m *testing.M) {
	if os.Getenv("OLCPANEL_FAKE_OLCRTC") == "1" {
		os.Exit(runFakeOlcRTC())
	}
	os.Exit(m.Run())
}

func TestProcessRunnerStartsChildWithGeneratedConfigPath(t *testing.T) {
	exe := fakeExecutable(t)
	stack := &fakeStack{}
	runner := supervisor.NewProcessRunnerWithOptions(t.TempDir(), exe, supervisor.ProcessRunnerOptions{
		Netstack: stack,
		IPBinary: exe,
	})
	state := processLocation("loc_start", "room-start")

	if err := runner.Start(context.Background(), state); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	t.Cleanup(func() { _ = runner.Stop(context.Background(), state) })

	configPath := filepath.Join(runner.RuntimeDir(), state.LocationID, "server.yaml")
	waitForFile(t, configPath)
	args := readProcessArgs(t, runner.RuntimeDir(), state.LocationID)
	names := netstack.NamesForLocation(state.LocationID)
	wantArgs := "netns exec " + names.Namespace + " " + exe + " " + configPath
	if args != wantArgs {
		t.Fatalf("child args = %q, want %q", args, wantArgs)
	}
	if len(stack.ensureCalls) != 1 || stack.ensureCalls[0].LocationID != state.LocationID {
		t.Fatalf("ensure calls = %#v, want location before start", stack.ensureCalls)
	}
}

func TestProcessRunnerRestartRewritesConfigAndStartsNewProcess(t *testing.T) {
	exe := fakeExecutable(t)
	runner := supervisor.NewProcessRunner(t.TempDir(), exe)
	oldState := processLocation("loc_restart", "room-old")
	newState := processLocation("loc_restart", "room-new")
	t.Cleanup(func() {
		_ = runner.Stop(context.Background(), newState)
		_ = runner.Stop(context.Background(), oldState)
	})

	if err := runner.Start(context.Background(), oldState); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	firstPID := waitForPID(t, runner.RuntimeDir(), oldState.LocationID)

	if err := runner.Restart(context.Background(), oldState, newState); err != nil {
		t.Fatalf("Restart returned error: %v", err)
	}
	secondPID := waitForDifferentPID(t, runner.RuntimeDir(), oldState.LocationID, firstPID)

	if firstPID == secondPID {
		t.Fatalf("restart reused pid %q", firstPID)
	}
	config := readProcessConfig(t, runner.RuntimeDir(), newState.LocationID)
	if !strings.Contains(config, `id: "room-new"`) {
		t.Fatalf("config after restart did not include new room:\n%s", config)
	}
}

func TestProcessRunnerStopTerminatesProcessAndRemovesRuntimeDirectory(t *testing.T) {
	exe := fakeExecutable(t)
	stack := &fakeStack{}
	runner := supervisor.NewProcessRunnerWithOptions(t.TempDir(), exe, supervisor.ProcessRunnerOptions{
		Netstack: stack,
		IPBinary: exe,
	})
	state := processLocation("loc_stop", "room-stop")

	if err := runner.Start(context.Background(), state); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	waitForPID(t, runner.RuntimeDir(), state.LocationID)

	if err := runner.Stop(context.Background(), state); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(runner.RuntimeDir(), state.LocationID)); !os.IsNotExist(err) {
		t.Fatalf("runtime dir still exists or stat failed: %v", err)
	}
	if got := runner.Status(state.LocationID); got != supervisor.ProcessStopped {
		t.Fatalf("status = %q, want stopped", got)
	}
	if len(stack.cleanupCalls) != 1 || stack.cleanupCalls[0].LocationID != state.LocationID {
		t.Fatalf("cleanup calls = %#v, want location on stop", stack.cleanupCalls)
	}
}

func TestProcessRunnerCleansNetworkWhenStartFailsAfterEnsure(t *testing.T) {
	exe := fakeExecutable(t)
	stack := &fakeStack{}
	runner := supervisor.NewProcessRunnerWithOptions(t.TempDir(), exe, supervisor.ProcessRunnerOptions{
		Netstack: stack,
		IPBinary: filepath.Join(t.TempDir(), "missing-ip"),
	})
	state := processLocation("loc_fail", "room-fail")

	err := runner.Start(context.Background(), state)
	if err == nil {
		t.Fatal("Start returned nil error, want start failure")
	}
	if len(stack.ensureCalls) != 1 {
		t.Fatalf("ensure calls = %#v, want one ensure", stack.ensureCalls)
	}
	if len(stack.cleanupCalls) != 1 || stack.cleanupCalls[0].LocationID != state.LocationID {
		t.Fatalf("cleanup calls = %#v, want cleanup after failed start", stack.cleanupCalls)
	}
}

func TestProcessRunnerRecordsUnexpectedExitWithoutRestart(t *testing.T) {
	exe := fakeExecutable(t)
	runner := supervisor.NewProcessRunner(t.TempDir(), exe)
	state := processLocation("loc_exit", "room-exit")

	if err := runner.Start(context.Background(), state); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	waitForStatus(t, runner, state.LocationID, supervisor.ProcessFailed)
	if got := countLaunches(t, runner.RuntimeDir(), state.LocationID); got != 1 {
		t.Fatalf("launch count = %d, want no automatic restart", got)
	}
}

func TestProcessRunnerCapturesChildOutputWithLocationMetadata(t *testing.T) {
	exe := fakeExecutable(t)
	logs := &recordingLogSink{}
	runner := supervisor.NewProcessRunnerWithOptions(t.TempDir(), exe, supervisor.ProcessRunnerOptions{
		LogSink: logs,
	})
	state := processLocation("loc_output", "room-output")

	if err := runner.Start(context.Background(), state); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	t.Cleanup(func() { _ = runner.Stop(context.Background(), state) })

	waitForLogMessages(t, logs, "stdout says hello", "stderr says hello")
	entries := logs.entriesSnapshot()
	if len(entries) < 2 {
		t.Fatalf("entries = %#v, want stdout and stderr entries", entries)
	}
	assertCapturedOutput(t, entries, "olcrtc_stdout", state.ClientID, state.LocationID, "stdout says hello")
	assertCapturedOutput(t, entries, "olcrtc_stderr", state.ClientID, state.LocationID, "stderr says hello")
}

func processLocation(id, room string) supervisor.LocationState {
	return supervisor.LocationState{
		LocationID:       id,
		ClientID:         "cl_test",
		Name:             "Test",
		Provider:         "wbstream",
		Transport:        "datachannel",
		RoomID:           room,
		CryptoKey:        "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		TransportPayload: `{}`,
		DNS:              "8.8.8.8:53",
	}
}

func fakeExecutable(t *testing.T) string {
	t.Helper()
	t.Setenv("OLCPANEL_FAKE_OLCRTC", "1")
	return os.Args[0]
}

func runFakeOlcRTC() int {
	if len(os.Args) < 2 {
		return 2
	}
	configPath := os.Args[len(os.Args)-1]
	dir := filepath.Dir(configPath)
	count := 0
	if data, err := os.ReadFile(filepath.Join(dir, "launches.txt")); err == nil {
		count = mustAtoi(strings.TrimSpace(string(data)))
	}
	count++
	_ = os.WriteFile(filepath.Join(dir, "launches.txt"), []byte(intString(count)+"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "args.txt"), []byte(strings.Join(os.Args[1:], " ")+"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "pid.txt"), []byte(intString(os.Getpid())+"\n"), 0o644)
	data, _ := os.ReadFile(configPath)
	if strings.Contains(string(data), `id: "room-output"`) {
		_, _ = os.Stdout.WriteString("stdout says hello\n")
		_, _ = os.Stderr.WriteString("stderr says hello\n")
		select {}
	}
	if strings.Contains(string(data), `id: "room-exit"`) {
		return 42
	}
	select {}
}

type fakeStack struct {
	ensureCalls  []supervisor.LocationState
	cleanupCalls []supervisor.LocationState
}

func (stack *fakeStack) Ensure(_ context.Context, state supervisor.LocationState) error {
	stack.ensureCalls = append(stack.ensureCalls, state)
	return nil
}

func (stack *fakeStack) Cleanup(_ context.Context, state supervisor.LocationState) error {
	stack.cleanupCalls = append(stack.cleanupCalls, state)
	return nil
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func waitForPID(t *testing.T, runtimeDir, locationID string) string {
	t.Helper()
	path := filepath.Join(runtimeDir, locationID, "pid.txt")
	waitForFile(t, path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	return strings.TrimSpace(string(data))
}

func waitForDifferentPID(t *testing.T, runtimeDir, locationID, oldPID string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		pid := waitForPID(t, runtimeDir, locationID)
		if pid != "" && pid != oldPID {
			return pid
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for different pid")
	return ""
}

func waitForStatus(t *testing.T, runner *supervisor.ProcessRunner, locationID string, want supervisor.ProcessStatus) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := runner.Status(locationID); got == want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("status = %q, want %q", runner.Status(locationID), want)
}

func readProcessArgs(t *testing.T, runtimeDir, locationID string) string {
	t.Helper()
	path := filepath.Join(runtimeDir, locationID, "args.txt")
	waitForFile(t, path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	return strings.TrimSpace(string(data))
}

func readProcessConfig(t *testing.T, runtimeDir, locationID string) string {
	t.Helper()
	path := filepath.Join(runtimeDir, locationID, "server.yaml")
	waitForFile(t, path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	return string(data)
}

func countLaunches(t *testing.T, runtimeDir, locationID string) int {
	t.Helper()
	path := filepath.Join(runtimeDir, locationID, "launches.txt")
	waitForFile(t, path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	return mustAtoi(strings.TrimSpace(string(data)))
}

func mustAtoi(value string) int {
	var result int
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			panic("non-numeric value")
		}
		result = result*10 + int(ch-'0')
	}
	return result
}

func intString(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for value > 0 {
		i--
		digits[i] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[i:])
}

type recordingLogSink struct {
	mu      sync.Mutex
	entries []observability.LogEntry
}

func (sink *recordingLogSink) Append(_ context.Context, entry observability.LogEntry) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.entries = append(sink.entries, entry)
	return nil
}

func (sink *recordingLogSink) entriesSnapshot() []observability.LogEntry {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	entries := make([]observability.LogEntry, len(sink.entries))
	copy(entries, sink.entries)
	return entries
}

func waitForLogMessages(t *testing.T, sink *recordingLogSink, messages ...string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		entries := sink.entriesSnapshot()
		found := make(map[string]bool, len(messages))
		for _, entry := range entries {
			found[entry.Message] = true
		}
		all := true
		for _, message := range messages {
			if !found[message] {
				all = false
				break
			}
		}
		if all {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for log messages %v; entries = %#v", messages, sink.entriesSnapshot())
}

func assertCapturedOutput(t *testing.T, entries []observability.LogEntry, source, clientID, locationID, message string) {
	t.Helper()
	for _, entry := range entries {
		if entry.Source == source && entry.Message == message {
			if entry.ClientID != clientID || entry.LocationID != locationID || entry.Level != "info" {
				t.Fatalf("entry = %#v, want client/location metadata and info level", entry)
			}
			return
		}
	}
	t.Fatalf("entries = %#v, missing %s message %q", entries, source, message)
}
