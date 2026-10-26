// Package app wires Air-Mixer together and runs it.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/yeixio/air-mixer/internal/api"
	"github.com/yeixio/air-mixer/internal/config"
	"github.com/yeixio/air-mixer/internal/db"
	"github.com/yeixio/air-mixer/internal/discovery"
	"github.com/yeixio/air-mixer/internal/protocols"
	"github.com/yeixio/air-mixer/internal/router"
	"github.com/yeixio/air-mixer/internal/service"
)

// shutdownTimeout bounds how long in-flight HTTP requests may take to
// finish on shutdown.
const shutdownTimeout = 5 * time.Second

// Options configures Run.
type Options struct {
	Config  *config.Config
	Logger  *slog.Logger
	Version string
	// Factory builds protocol adapters. Nil means the built-in protocols.
	Factory service.Factory
	// Discovery finds devices. Nil means the built-in discoverers.
	Discovery api.Discoverer
	// Ready, if set, receives the HTTP listen address once serving.
	Ready func(addr net.Addr)
}

// Run starts Air-Mixer and blocks until ctx is cancelled, then shuts down
// cleanly.
func Run(ctx context.Context, opts Options) error {
	cfg, log := opts.Config, opts.Logger
	if log == nil {
		log = slog.Default()
	}

	if err := os.MkdirAll(filepath.Dir(cfg.Database.Path), 0o755); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	store, err := db.Open(ctx, cfg.Database.Path)
	if err != nil {
		return err
	}
	defer store.Close()

	r := router.New(log)
	r.Start()
	defer r.Stop()

	manager := protocols.NewManager(r, log)
	defer manager.Close()

	factory := opts.Factory
	if factory == nil {
		factory = service.NewFactory(service.Defaults{AirPlayPort: cfg.Server.AirPlayPort, Interface: cfg.Server.Interface}, log)
	}
	svc := service.New(store, r, manager, factory, log)
	if err := svc.Load(ctx); err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	disc := opts.Discovery
	if disc == nil {
		disc = discovery.NewService()
	}
	srv := &http.Server{
		Handler:           api.New(api.Options{Service: svc, Discovery: disc, Logger: log, Version: opts.Version}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	ln, err := net.Listen("tcp", cfg.Addr())
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	log.Info("Air-Mixer started", "version", opts.Version, "web", "http://"+ln.Addr().String(), "database", cfg.Database.Path)
	if opts.Ready != nil {
		opts.Ready(ln.Addr())
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}
