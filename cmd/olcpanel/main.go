package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"olcpanel/internal/config"
	"olcpanel/internal/netstack"
	"olcpanel/internal/observability"
	"olcpanel/internal/server"
	"olcpanel/internal/storage"
	"olcpanel/internal/supervisor"
	"olcpanel/internal/traffic"
	"olcpanel/internal/webui"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usage()
	}

	switch args[0] {
	case "serve":
		return serve(args[1:])
	case "migrate":
		return migrate(args[1:])
	case "doctor":
		return doctor(args[1:])
	case "-h", "--help", "help":
		return usage()
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args[0], commandUsage())
	}
}

func serve(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	bind := flags.String("bind", "", "HTTP bind address")
	databaseURL := flags.String("database-url", "", "database URL")
	runtimeDir := flags.String("runtime-dir", "", "runtime directory for generated OlcRTC configs")
	olcrtcBinary := flags.String("olcrtc-binary", "", "OlcRTC binary path or name")
	networkCIDR := flags.String("network-cidr", "", "runtime network CIDR for location namespaces")
	trafficSampleInterval := flags.String("traffic-sample-interval", "", "traffic accounting sample interval")
	logPath := flags.String("log-path", "", "panel JSONL log path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}

	cfg, err := config.LoadWithOptions(config.LoadOptions{
		BindAddress:           *bind,
		DatabaseURL:           *databaseURL,
		RuntimeDir:            *runtimeDir,
		OlcRTCBinary:          *olcrtcBinary,
		NetworkCIDR:           *networkCIDR,
		TrafficSampleInterval: *trafficSampleInterval,
		LogPath:               *logPath,
	})
	if err != nil {
		return err
	}

	logSink := observability.NewFileSink(cfg.LogPath)
	slog.SetDefault(slog.New(observability.NewFanoutHandler(
		slog.NewTextHandler(os.Stderr, nil),
		observability.NewSlogHandler(logSink, "panel"),
	)))

	ctx := context.Background()
	db, err := storage.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := storage.Migrate(ctx, db); err != nil {
		return err
	}
	stack := netstack.New(netstack.Options{NetworkCIDR: cfg.NetworkCIDR})
	if err := stack.EnsureForwarding(ctx); err != nil {
		return err
	}
	runtimeRunner := supervisor.NewProcessRunnerWithOptions(cfg.RuntimeDir, cfg.OlcRTCBinary, supervisor.ProcessRunnerOptions{
		Netstack: netstackAdapter{stack: stack},
		LogSink:  logSink,
	})
	processSupervisor := supervisor.New(db, supervisor.WithRunner(runtimeRunner))
	if result, err := processSupervisor.Reload(ctx); err != nil {
		slog.Error("initial supervisor reload failed", "error", err)
	} else {
		slog.Info(
			"initial supervisor reload applied",
			"started", result.Summary.Started,
			"restarted", result.Summary.Restarted,
			"stopped", result.Summary.Stopped,
			"unchanged", result.Summary.Unchanged,
			"skipped", result.Summary.Skipped,
		)
	}

	samplerCtx, stopSampler := context.WithCancel(context.Background())
	defer stopSampler()
	trafficSampler := traffic.NewSampler(db, traffic.SysfsCounterReader{}, traffic.Options{
		Reload: func(ctx context.Context) error {
			result, err := processSupervisor.Reload(ctx)
			if err != nil {
				return err
			}
			slog.Info(
				"supervisor reload applied after traffic state transition",
				"started", result.Summary.Started,
				"restarted", result.Summary.Restarted,
				"stopped", result.Summary.Stopped,
				"unchanged", result.Summary.Unchanged,
				"skipped", result.Summary.Skipped,
			)
			return nil
		},
	})
	samplerDone := make(chan struct{})
	go func() {
		defer close(samplerDone)
		trafficSampler.Run(samplerCtx, cfg.TrafficSampleInterval)
	}()
	defer func() {
		stopSampler()
		<-samplerDone
	}()

	httpServer := &http.Server{
		Addr:              cfg.BindAddress,
		Handler:           server.New(cfg, webui.Assets(), server.WithDatabase(db), server.WithSupervisor(processSupervisor), server.WithLogStore(logSink)),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errs := make(chan error, 1)
	go func() {
		slog.Info("olcpanel serving", "bind", cfg.BindAddress)
		errs <- httpServer.ListenAndServe()
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)

	for {
		select {
		case sig := <-signals:
			shutdown, err := handleServerSignal(sig, httpServer, processSupervisor)
			if err != nil {
				return err
			}
			if shutdown {
				return nil
			}
		case err := <-errs:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		}
	}
}

type serveReloader interface {
	Reload(context.Context) (supervisor.ReloadResult, error)
}

func handleServerSignal(sig os.Signal, httpServer *http.Server, reloader serveReloader) (bool, error) {
	if sig == syscall.SIGHUP {
		if reloader == nil {
			slog.Warn("reload requested but supervisor is unavailable", "signal", sig.String())
			return false, nil
		}
		result, err := reloader.Reload(context.Background())
		if err != nil {
			slog.Error("supervisor reload failed", "signal", sig.String(), "error", err)
			return false, nil
		}
		slog.Info(
			"supervisor reload applied",
			"signal", sig.String(),
			"started", result.Summary.Started,
			"restarted", result.Summary.Restarted,
			"stopped", result.Summary.Stopped,
			"unchanged", result.Summary.Unchanged,
			"skipped", result.Summary.Skipped,
		)
		return false, nil
	}

	slog.Info("shutdown requested", "signal", sig.String())
	if httpServer == nil {
		return true, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return true, httpServer.Shutdown(ctx)
}

func migrate(args []string) error {
	flags := flag.NewFlagSet("migrate", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	databaseURL := flags.String("database-url", "", "database URL")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}

	cfg, err := config.LoadWithOptions(config.LoadOptions{DatabaseURL: *databaseURL})
	if err != nil {
		return err
	}

	ctx := context.Background()
	db, err := storage.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	return storage.Migrate(ctx, db)
}

func doctor(args []string) error {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	databaseURL := flags.String("database-url", "", "database URL")
	networkCIDR := flags.String("network-cidr", "", "runtime network CIDR for location namespaces")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}

	cfg, err := config.LoadWithOptions(config.LoadOptions{
		DatabaseURL: *databaseURL,
		NetworkCIDR: *networkCIDR,
	})
	if err != nil {
		return err
	}

	ctx := context.Background()
	activeIDs, findings := activeLocationIDs(ctx, cfg.DatabaseURL)
	stack := netstack.New(netstack.Options{NetworkCIDR: cfg.NetworkCIDR})
	report := stack.Doctor(ctx, activeIDs)
	for _, finding := range findings {
		report.Healthy = false
		report.Findings = append(report.Findings, finding)
	}
	fmt.Fprint(os.Stdout, report.String())
	if !report.Healthy {
		return errors.New("doctor found unhealthy runtime state")
	}
	return nil
}

func activeLocationIDs(ctx context.Context, databaseURL string) ([]string, []string) {
	db, err := storage.Open(ctx, databaseURL)
	if err != nil {
		return nil, []string{"database unavailable: " + err.Error()}
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `
SELECT l.id
FROM locations l
JOIN clients c ON c.id = l.client_id AND c.node_id = l.node_id
WHERE l.node_id = ? AND l.enabled = 1 AND c.enabled = 1
ORDER BY l.id`, "local")
	if err != nil {
		return nil, []string{"database query failed: " + err.Error()}
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return ids, []string{"database scan failed: " + err.Error()}
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return ids, []string{"database iteration failed: " + err.Error()}
	}
	return ids, nil
}

type netstackAdapter struct {
	stack *netstack.Stack
}

func (adapter netstackAdapter) Ensure(ctx context.Context, state supervisor.LocationState) error {
	return adapter.stack.Ensure(ctx, netstack.LocationState{
		LocationID:     state.LocationID,
		DNS:            state.DNS,
		SpeedLimitBPS:  state.SpeedLimitBPS,
		TrafficEnabled: !state.TrafficDisabled,
	})
}

func (adapter netstackAdapter) Cleanup(ctx context.Context, state supervisor.LocationState) error {
	return adapter.stack.Cleanup(ctx, state.LocationID)
}

func (adapter netstackAdapter) Validate(ctx context.Context, states []supervisor.LocationState) error {
	return adapter.stack.Validate(ctx, supervisorStatesToNetstack(states))
}

func supervisorStatesToNetstack(states []supervisor.LocationState) []netstack.LocationState {
	result := make([]netstack.LocationState, 0, len(states))
	for _, state := range states {
		result = append(result, netstack.LocationState{LocationID: state.LocationID})
	}
	return result
}

func usage() error {
	fmt.Fprint(os.Stderr, commandUsage())
	return nil
}

func commandUsage() string {
	return `olcpanel manages a local OlcRTC VPS panel.

Usage:
  olcpanel serve [--bind 127.0.0.1:8888] [--database-url sqlite:///etc/olcpanel/panel.db] [--runtime-dir /var/lib/olcpanel/runtime] [--olcrtc-binary olcrtc] [--network-cidr 10.255.0.0/16] [--traffic-sample-interval 30s] [--log-path /var/log/olcpanel/panel.log]
  olcpanel migrate [--database-url sqlite:///etc/olcpanel/panel.db]
  olcpanel doctor [--database-url sqlite:///etc/olcpanel/panel.db] [--network-cidr 10.255.0.0/16]

Environment:
  OLCPANEL_BIND           HTTP bind address. Defaults to 127.0.0.1:8888.
  OLCPANEL_DATABASE_URL   Database URL. Defaults to sqlite:///etc/olcpanel/panel.db.
  OLCPANEL_RUNTIME_DIR    Runtime config directory. Defaults to /var/lib/olcpanel/runtime.
  OLCPANEL_OLCRTC_BINARY  OlcRTC binary path or name. Defaults to olcrtc.
  OLCPANEL_NETWORK_CIDR   Runtime network CIDR. Defaults to 10.255.0.0/16.
  OLCPANEL_TRAFFIC_SAMPLE_INTERVAL
                          Traffic accounting sample interval. Defaults to 30s.
  OLCPANEL_LOG_PATH       Panel JSONL log path. Defaults to /var/log/olcpanel/panel.log.
`
}
