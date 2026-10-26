package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeixio/air-mixer/internal/config"
	"github.com/yeixio/air-mixer/internal/discovery"
)

type noDiscovery struct{}

func (noDiscovery) DiscoverAll(context.Context, time.Duration) ([]discovery.Device, error) {
	return nil, nil
}

func (noDiscovery) Discover(context.Context, string, time.Duration) ([]discovery.Device, error) {
	return nil, nil
}

func testConfig(t *testing.T) *config.Config {
	dir := t.TempDir()
	return &config.Config{
		Server:   config.ServerConfig{Host: "127.0.0.1", WebPort: 0, AirPlayPort: 0, DataDir: dir},
		Database: config.DatabaseConfig{Path: filepath.Join(dir, "sub", "airmixer.db")},
		Logging:  config.LoggingConfig{Level: "info", Format: "text"},
	}
}

func TestRun(t *testing.T) {
	cfg := testConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Config:    cfg,
			Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
			Version:   "v-test",
			Discovery: noDiscovery{},
			Ready:     func(a net.Addr) { ready <- a },
		})
	}()

	var addr net.Addr
	select {
	case addr = <-ready:
	case err := <-done:
		t.Fatalf("Run exited early: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("not ready")
	}
	base := "http://" + addr.String()

	resp, err := http.Get(base + "/health")
	if err != nil {
		t.Fatal(err)
	}
	var h map[string]string
	json.NewDecoder(resp.Body).Decode(&h)
	resp.Body.Close()
	if resp.StatusCode != 200 || h["version"] != "v-test" {
		t.Errorf("health %d %v", resp.StatusCode, h)
	}

	// A route survives a restart.
	resp, err = http.Post(base+"/api/routes", "application/json",
		strings.NewReader(`{"name":"r","source_name":"a","dest_names":["b"]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("create route = %d", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown hung")
	}

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go func() {
		done <- Run(ctx2, Options{Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Discovery: noDiscovery{}, Ready: func(a net.Addr) { ready <- a }})
	}()
	addr = <-ready
	resp, err = http.Get("http://" + addr.String() + "/api/routes")
	if err != nil {
		t.Fatal(err)
	}
	var routes []map[string]any
	json.NewDecoder(resp.Body).Decode(&routes)
	resp.Body.Close()
	if len(routes) != 1 {
		t.Errorf("routes after restart: %v", routes)
	}
	cancel2()
	<-done
}

func TestRunErrors(t *testing.T) {
	cfg := testConfig(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	cfg.Server.WebPort = ln.Addr().(*net.TCPAddr).Port
	err = Run(context.Background(), Options{Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Discovery: noDiscovery{}})
	if err == nil || !strings.Contains(err.Error(), "listen") {
		t.Errorf("busy port: %v", err)
	}

	cfg = testConfig(t)
	cfg.Database.Path = filepath.Join(cfg.Server.DataDir, "db-is-a-dir")
	if err := mkdir(cfg.Database.Path); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), Options{Config: cfg, Discovery: noDiscovery{}}); err == nil {
		t.Error("database that is a directory accepted")
	}
}

func mkdir(p string) error { return os.MkdirAll(p, 0o755) }
