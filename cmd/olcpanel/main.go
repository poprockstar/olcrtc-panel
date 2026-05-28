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
	"olcpanel/internal/server"
	"olcpanel/internal/storage"
	"olcpanel/internal/supervisor"
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
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}

	cfg, err := config.LoadWithOptions(config.LoadOptions{
		BindAddress:  *bind,
		DatabaseURL:  *databaseURL,
		RuntimeDir:   *runtimeDir,
		OlcRTCBinary: *olcrtcBinary,
	})
	if err != nil {
		return err
	}

	ctx := context.Background()
	db, err := storage.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := storage.Migrate(ctx, db); err != nil {
		return err
	}
	runtimeRunner := supervisor.NewProcessRunner(cfg.RuntimeDir, cfg.OlcRTCBinary)
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

	httpServer := &http.Server{
		Addr:              cfg.BindAddress,
		Handler:           server.New(cfg, webui.Assets(), server.WithDatabase(db), server.WithSupervisor(processSupervisor)),
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

func usage() error {
	fmt.Fprint(os.Stderr, commandUsage())
	return nil
}

func commandUsage() string {
	return `olcpanel manages a local OlcRTC VPS panel.

Usage:
  olcpanel serve [--bind 127.0.0.1:8888] [--database-url sqlite:///etc/olcpanel/panel.db] [--runtime-dir /var/lib/olcpanel/runtime] [--olcrtc-binary olcrtc]
  olcpanel migrate [--database-url sqlite:///etc/olcpanel/panel.db]

Environment:
  OLCPANEL_BIND           HTTP bind address. Defaults to 127.0.0.1:8888.
  OLCPANEL_DATABASE_URL   Database URL. Defaults to sqlite:///etc/olcpanel/panel.db.
  OLCPANEL_RUNTIME_DIR    Runtime config directory. Defaults to /var/lib/olcpanel/runtime.
  OLCPANEL_OLCRTC_BINARY  OlcRTC binary path or name. Defaults to olcrtc.
`
}
