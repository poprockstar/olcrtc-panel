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
	if err := flags.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *bind != "" {
		cfg.BindAddress = *bind
	}

	httpServer := &http.Server{
		Addr:              cfg.BindAddress,
		Handler:           server.New(cfg, webui.Assets()),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errs := make(chan error, 1)
	go func() {
		slog.Info("olcpanel serving", "bind", cfg.BindAddress)
		errs <- httpServer.ListenAndServe()
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-signals:
		slog.Info("shutdown requested", "signal", sig.String())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(ctx)
	case err := <-errs:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func usage() error {
	fmt.Fprint(os.Stderr, commandUsage())
	return nil
}

func commandUsage() string {
	return `olcpanel manages a local OlcRTC VPS panel.

Usage:
  olcpanel serve [--bind 127.0.0.1:8888]

Environment:
  OLCPANEL_BIND  HTTP bind address. Defaults to 127.0.0.1:8888.
`
}
