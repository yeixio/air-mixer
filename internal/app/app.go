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
	"strings"
	"time"

	"github.com/yeixio/air-mixer/internal/api"
	"github.com/yeixio/air-mixer/internal/config"
	"github.com/yeixio/air-mixer/internal/db"
	"github.com/yeixio/air-mixer/internal/discovery"
	"github.com/yeixio/air-mixer/internal/playback"
	"github.com/yeixio/air-mixer/internal/protocols"
	"github.com/yeixio/air-mixer/internal/protocols/httpstream"
	"github.com/yeixio/air-mixer/internal/protocols/matinee"
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

	streams := httpstream.NewRegistry()
	account := matinee.NewAccount(ctx, store, cfg.Matinee.URL, log)
	defer account.Close()

	factory := opts.Factory
	if factory == nil {
		firstPort, lastPort := cfg.Server.AirPlayPorts()
		factory = service.NewFactory(service.Defaults{
			AirPlayPort: firstPort, AirPlayLastPort: lastPort, Interface: cfg.Server.Interface,
			DataDir: cfg.Server.DataDir,
			Version: opts.Version, WebPort: cfg.Server.WebPort, PublicURL: cfg.Server.PublicURL,
			Streams: streams, Matinee: account, Settings: store,
		}, log)
	}
	svc := service.New(store, r, manager, factory, log)
	if err := svc.Load(ctx); err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	setup, err := setupFromConfig(cfg.Setup)
	if err != nil {
		return err
	}
	if !setup.Empty() {
		if err := svc.Apply(ctx, setup); err != nil {
			return err
		}
	}

	// Declared after the manager so it stops first, while outputs are
	// still connected.
	player := playback.New(r, playback.Options{Logger: log})
	defer player.Close()

	disc := opts.Discovery
	if disc == nil {
		d := discovery.NewService()
		d.Exclude(ownInput(svc))
		disc = d
	}
	srv := &http.Server{
		Handler: api.New(api.Options{
			Service: svc, Discovery: disc, Player: player, Logger: log, Version: opts.Version,
			Streams: streams, Matinee: account,
		}),
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

// ownInput reports devices that are inputs on this host under another
// program's advertisement, such as a shairport-sync AirPlay 2 input: a
// device on one of this host's addresses named like one of the sources.
func ownInput(svc *service.Service) func(discovery.Device) bool {
	return func(d discovery.Device) bool {
		ip := net.ParseIP(d.Host())
		if ip == nil || !isLocalIP(ip) {
			return false
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		servers, err := svc.Servers(ctx)
		if err != nil {
			return false
		}
		for _, s := range servers {
			if strings.EqualFold(strings.TrimSpace(s.Name), strings.TrimSpace(d.Name)) {
				return true
			}
		}
		return false
	}
}

func isLocalIP(ip net.IP) bool {
	if ip.IsLoopback() {
		return true
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
			return true
		}
	}
	return false
}
