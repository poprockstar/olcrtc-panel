package supervisor

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"

	"olcpanel/internal/netstack"
	"olcpanel/internal/runtimeconfig"
)

type ProcessStatus string

const (
	ProcessUnknown ProcessStatus = ""
	ProcessRunning ProcessStatus = "running"
	ProcessStopped ProcessStatus = "stopped"
	ProcessFailed  ProcessStatus = "failed"
)

type ProcessRunner struct {
	renderer   runtimeconfig.Renderer
	runtimeDir string
	binary     string
	ipBinary   string
	netstack   NetworkStack
	mu         sync.Mutex
	processes  map[string]*managedProcess
	statuses   map[string]ProcessStatus
}

type NetworkStack interface {
	Ensure(context.Context, LocationState) error
	Cleanup(context.Context, LocationState) error
}

type ProcessRunnerOptions struct {
	Netstack NetworkStack
	IPBinary string
}

type managedProcess struct {
	cmd      *exec.Cmd
	done     chan error
	stopping bool
}

func NewProcessRunner(runtimeDir, binary string) *ProcessRunner {
	return NewProcessRunnerWithOptions(runtimeDir, binary, ProcessRunnerOptions{})
}

func NewProcessRunnerWithOptions(runtimeDir, binary string, options ProcessRunnerOptions) *ProcessRunner {
	ipBinary := options.IPBinary
	if strings.TrimSpace(ipBinary) == "" {
		ipBinary = "ip"
	}
	return &ProcessRunner{
		renderer:   runtimeconfig.NewRenderer(runtimeDir),
		runtimeDir: runtimeDir,
		binary:     binary,
		ipBinary:   ipBinary,
		netstack:   options.Netstack,
		processes:  make(map[string]*managedProcess),
		statuses:   make(map[string]ProcessStatus),
	}
}

func (runner *ProcessRunner) RuntimeDir() string {
	return runner.runtimeDir
}

func (runner *ProcessRunner) Status(locationID string) ProcessStatus {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.statuses[locationID]
}

func (runner *ProcessRunner) Start(ctx context.Context, state LocationState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(runner.binary) == "" {
		return fmt.Errorf("olcrtc binary is required")
	}
	if runner.hasProcess(state.LocationID) {
		return fmt.Errorf("location %s is already running", state.LocationID)
	}
	if runner.netstack != nil {
		if err := runner.netstack.Ensure(ctx, state); err != nil {
			return fmt.Errorf("ensure network for location %s: %w", state.LocationID, err)
		}
	}
	configPath, err := runner.renderer.Render(runtimeLocation(state))
	if err != nil {
		runner.cleanupNetwork(ctx, state)
		return err
	}

	cmd := runner.command(state, configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		runner.cleanupNetwork(ctx, state)
		return fmt.Errorf("start %s for location %s: %w", runner.binary, state.LocationID, err)
	}

	process := &managedProcess{cmd: cmd, done: make(chan error, 1)}
	runner.mu.Lock()
	runner.processes[state.LocationID] = process
	runner.statuses[state.LocationID] = ProcessRunning
	runner.mu.Unlock()

	go runner.watch(state.LocationID, process)
	return nil
}

func (runner *ProcessRunner) hasProcess(locationID string) bool {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.processes[locationID] != nil
}

func (runner *ProcessRunner) Restart(ctx context.Context, oldState, newState LocationState) error {
	if err := runner.Stop(ctx, oldState); err != nil {
		return err
	}
	return runner.Start(ctx, newState)
}

func (runner *ProcessRunner) Stop(ctx context.Context, state LocationState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	process := runner.markStopping(state.LocationID)
	if process != nil && process.cmd.Process != nil {
		_ = process.cmd.Process.Kill()
		<-process.done
	}
	if err := runner.renderer.Remove(state.LocationID); err != nil {
		return err
	}
	if err := runner.cleanupNetwork(ctx, state); err != nil {
		return err
	}

	runner.mu.Lock()
	delete(runner.processes, state.LocationID)
	runner.statuses[state.LocationID] = ProcessStopped
	runner.mu.Unlock()
	return nil
}

func (runner *ProcessRunner) Validate(ctx context.Context, states []LocationState) error {
	if validator, ok := runner.netstack.(interface {
		Validate(context.Context, []LocationState) error
	}); ok {
		return validator.Validate(ctx, states)
	}
	return nil
}

func (runner *ProcessRunner) command(state LocationState, configPath string) *exec.Cmd {
	if runner.netstack == nil {
		return exec.Command(runner.binary, configPath)
	}
	names := netstack.NamesForLocation(state.LocationID)
	return exec.Command(runner.ipBinary, "netns", "exec", names.Namespace, runner.binary, configPath)
}

func (runner *ProcessRunner) cleanupNetwork(ctx context.Context, state LocationState) error {
	if runner.netstack == nil {
		return nil
	}
	return runner.netstack.Cleanup(ctx, state)
}

func (runner *ProcessRunner) markStopping(locationID string) *managedProcess {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	process := runner.processes[locationID]
	if process != nil {
		process.stopping = true
	}
	return process
}

func (runner *ProcessRunner) watch(locationID string, process *managedProcess) {
	err := process.cmd.Wait()
	process.done <- err
	close(process.done)

	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.processes[locationID] != process {
		return
	}
	delete(runner.processes, locationID)
	if process.stopping {
		runner.statuses[locationID] = ProcessStopped
		return
	}
	runner.statuses[locationID] = ProcessFailed
	slog.Warn("olcrtc process exited unexpectedly", "location_id", locationID, "error", err)
}

func runtimeLocation(state LocationState) runtimeconfig.Location {
	return runtimeconfig.Location{
		LocationID:       state.LocationID,
		Provider:         state.Provider,
		Transport:        state.Transport,
		RoomID:           state.RoomID,
		CryptoKey:        state.CryptoKey,
		TransportPayload: state.TransportPayload,
		DNS:              state.DNS,
	}
}
