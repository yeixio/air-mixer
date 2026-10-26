package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/yeixio/air-mixer/internal/audio"
	"github.com/yeixio/air-mixer/internal/db"
	"github.com/yeixio/air-mixer/internal/protocols"
	"github.com/yeixio/air-mixer/internal/protocols/airplay"
	"github.com/yeixio/air-mixer/internal/protocols/local"
	"github.com/yeixio/air-mixer/internal/router"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type fakeServer struct {
	id, name string
	audio    chan protocols.Packet
	startErr error
}

func (s *fakeServer) ID() string                     { return s.id }
func (s *fakeServer) Name() string                   { return s.name }
func (s *fakeServer) Protocol() string               { return "fake" }
func (s *fakeServer) Start(context.Context) error    { return s.startErr }
func (s *fakeServer) Stop() error                    { return nil }
func (s *fakeServer) Audio() <-chan protocols.Packet { return s.audio }

type fakeClient struct {
	id, name string
	sent     chan []float32
}

func (c *fakeClient) ID() string                    { return c.id }
func (c *fakeClient) Name() string                  { return c.name }
func (c *fakeClient) Protocol() string              { return "fake" }
func (c *fakeClient) Connect(context.Context) error { return nil }
func (c *fakeClient) Disconnect() error             { return nil }
func (c *fakeClient) Connected() bool               { return true }
func (c *fakeClient) Send(p protocols.Packet) error {
	select {
	case c.sent <- p.Samples:
	default:
	}
	return nil
}

// fakeFactory builds fakes and remembers them by ID.
type fakeFactory struct {
	mu       sync.Mutex
	servers  map[string]*fakeServer
	clients  map[string]*fakeClient
	startErr error
}

func newFakeFactory() *fakeFactory {
	return &fakeFactory{servers: map[string]*fakeServer{}, clients: map[string]*fakeClient{}}
}

func (f *fakeFactory) Server(v *db.Server) (protocols.Server, error) {
	if v.Protocol != "fake" {
		return nil, invalid("unsupported server protocol %q", v.Protocol)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	s := &fakeServer{id: v.ID, name: v.Name, audio: make(chan protocols.Packet, 8), startErr: f.startErr}
	f.servers[v.ID] = s
	return s, nil
}

func (f *fakeFactory) Client(v *db.Client) (protocols.Client, error) {
	if v.Protocol != "fake" {
		return nil, invalid("unsupported client protocol %q", v.Protocol)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	c := &fakeClient{id: v.ID, name: v.Name, sent: make(chan []float32, 64)}
	f.clients[v.ID] = c
	return c, nil
}

func (f *fakeFactory) server(id string) *fakeServer {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.servers[id]
}

func (f *fakeFactory) client(id string) *fakeClient {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.clients[id]
}

type env struct {
	svc     *Service
	db      *db.DB
	router  *router.Router
	manager *protocols.Manager
	factory *fakeFactory
}

func newEnv(t *testing.T, path string) *env {
	t.Helper()
	if path == "" {
		path = filepath.Join(t.TempDir(), "test.db")
	}
	store, err := db.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	r := router.New(quiet)
	r.Start()
	m := protocols.NewManager(r, quiet)
	f := newFakeFactory()
	t.Cleanup(func() { m.Close(); r.Stop(); store.Close() })
	return &env{svc: New(store, r, m, f, quiet), db: store, router: r, manager: m, factory: f}
}

func isInvalid(err error) bool {
	var ie *InvalidError
	return errors.As(err, &ie)
}

func TestServerCRUD(t *testing.T) {
	e := newEnv(t, "")
	ctx := context.Background()

	for _, bad := range []*db.Server{
		{Protocol: "fake"},
		{Name: "x"},
		{Name: "x", Protocol: "nope"},
		{ID: "a/b", Name: "x", Protocol: "fake"},
	} {
		if err := e.svc.CreateServer(ctx, bad); !isInvalid(err) {
			t.Errorf("CreateServer(%+v) = %v, want InvalidError", bad, err)
		}
	}

	v := &db.Server{Name: "Living room", Protocol: "fake", Enabled: true}
	if err := e.svc.CreateServer(ctx, v); err != nil {
		t.Fatal(err)
	}
	if v.ID == "" || e.factory.server(v.ID) == nil {
		t.Fatalf("server not created and started: %+v", v)
	}
	if err := e.svc.CreateServer(ctx, &db.Server{ID: v.ID, Name: "dup", Protocol: "fake"}); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate = %v", err)
	}
	if st := e.svc.Status(); len(st.Adapters) != 1 || st.Adapters[0].State != protocols.StateRunning || !st.RouterRunning {
		t.Errorf("status %+v", st)
	}

	v.Enabled = false
	v.Name = "Kitchen"
	if err := e.svc.UpdateServer(ctx, v); err != nil {
		t.Fatal(err)
	}
	if len(e.svc.Status().Adapters) != 0 {
		t.Error("disabled server still running")
	}
	got, err := e.svc.Server(ctx, v.ID)
	if err != nil || got.Name != "Kitchen" || got.Enabled {
		t.Errorf("Server = %+v, %v", got, err)
	}
	if err := e.svc.UpdateServer(ctx, &db.Server{ID: "missing", Name: "x", Protocol: "fake"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("update missing = %v", err)
	}

	list, err := e.svc.Servers(ctx)
	if err != nil || len(list) != 1 {
		t.Errorf("Servers = %v, %v", list, err)
	}
	if err := e.svc.DeleteServer(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteServer(ctx, v.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete = %v", err)
	}
}

func TestServerStartFailureRollsBack(t *testing.T) {
	e := newEnv(t, "")
	ctx := context.Background()
	e.factory.startErr = errors.New("address in use")
	err := e.svc.CreateServer(ctx, &db.Server{ID: "s1", Name: "x", Protocol: "fake", Enabled: true})
	if err == nil {
		t.Fatal("start failure swallowed")
	}
	if _, err := e.svc.Server(ctx, "s1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("server saved despite failing to start: %v", err)
	}
	// Disabled servers are not started, so they save.
	if err := e.svc.CreateServer(ctx, &db.Server{ID: "s2", Name: "x", Protocol: "fake"}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.UpdateServer(ctx, &db.Server{ID: "s2", Name: "x", Protocol: "fake", Enabled: true}); err == nil {
		t.Error("UpdateServer hid the start failure")
	}
}

func TestClientCRUD(t *testing.T) {
	e := newEnv(t, "")
	ctx := context.Background()
	if err := e.svc.CreateClient(ctx, &db.Client{Name: "x", Protocol: "nope"}); !isInvalid(err) {
		t.Errorf("unsupported protocol = %v", err)
	}
	v := &db.Client{Name: "Speaker", Protocol: "fake", Enabled: true}
	if err := e.svc.CreateClient(ctx, v); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		st := e.svc.Status()
		if len(st.Adapters) == 1 && st.Adapters[0].State == protocols.StateConnected {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("client never connected: %+v", st)
		}
		time.Sleep(5 * time.Millisecond)
	}

	v.Enabled = false
	if err := e.svc.UpdateClient(ctx, v); err != nil {
		t.Fatal(err)
	}
	if len(e.svc.Status().Adapters) != 0 {
		t.Error("disabled client still registered")
	}
	if err := e.svc.UpdateClient(ctx, &db.Client{ID: "missing", Name: "x", Protocol: "fake"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("update missing = %v", err)
	}
	if got, err := e.svc.Client(ctx, v.ID); err != nil || got.Enabled {
		t.Errorf("Client = %+v, %v", got, err)
	}
	if list, err := e.svc.Clients(ctx); err != nil || len(list) != 1 {
		t.Errorf("Clients = %v, %v", list, err)
	}
	if err := e.svc.DeleteClient(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteClient(ctx, v.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete = %v", err)
	}
}

func TestRoutes(t *testing.T) {
	e := newEnv(t, "")
	ctx := context.Background()
	if _, err := e.svc.CreateRoute(ctx, router.Route{Name: "r"}); !isInvalid(err) {
		t.Errorf("route without source = %v", err)
	}
	r, err := e.svc.CreateRoute(ctx, router.Route{Name: "r", SourceName: "In", DestNames: []string{"Out"}, Volume: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.ID == "" || r.MixMode != router.MixModeReplace {
		t.Errorf("defaults not filled: %+v", r)
	}
	if _, err := e.svc.CreateRoute(ctx, r); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate route = %v", err)
	}

	r.Volume = 0.5
	r.MixMode = ""
	if _, err := e.svc.UpdateRoute(ctx, r); err != nil {
		t.Fatal(err)
	}
	st, err := e.svc.Route(r.ID)
	if err != nil || st.Volume != 0.5 || st.MixMode != router.MixModeReplace {
		t.Errorf("Route = %+v, %v", st, err)
	}
	if _, err := e.svc.UpdateRoute(ctx, router.Route{ID: "missing", Name: "r", SourceName: "In", DestNames: []string{"Out"}, Volume: 1}); !errors.Is(err, ErrNotFound) {
		t.Errorf("update missing = %v", err)
	}
	if _, err := e.svc.UpdateRoute(ctx, router.Route{ID: r.ID, Name: "r", SourceName: "In", DestNames: []string{"Out"}, Volume: 7}); !isInvalid(err) {
		t.Errorf("volume 7 = %v", err)
	}
	if len(e.svc.Routes()) != 1 {
		t.Error("Routes")
	}
	if err := e.svc.DeleteRoute(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Route(r.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted route = %v", err)
	}
	if err := e.svc.DeleteRoute(ctx, r.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete = %v", err)
	}
}

// TestEndToEnd checks that audio from a server reaches a client through a
// route, and that Load restores the setup from the database.
func TestEndToEnd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "e2e.db")
	ctx := context.Background()

	e := newEnv(t, path)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(e.svc.CreateServer(ctx, &db.Server{ID: "in", Name: "Phone", Protocol: "fake", Enabled: true}))
	must(e.svc.CreateClient(ctx, &db.Client{ID: "out", Name: "Speaker", Protocol: "fake", Enabled: true}))
	must(e.svc.CreateServer(ctx, &db.Server{ID: "off", Name: "Off", Protocol: "fake"}))
	_, err := e.svc.CreateRoute(ctx, router.Route{Name: "r", SourceName: "Phone", DestNames: []string{"Speaker"}, Volume: 1, Enabled: true})
	must(err)
	expectAudio(t, e)
	e.manager.Close()
	e.router.Stop()
	e.db.Close()

	// A fresh process over the same database.
	e2 := newEnv(t, path)
	must(e2.svc.Load(ctx))
	if e2.factory.server("off") != nil {
		t.Error("disabled server started")
	}
	expectAudio(t, e2)
	if err := e2.svc.Ping(ctx); err != nil {
		t.Error(err)
	}
}

func expectAudio(t *testing.T, e *env) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s, c := e.factory.server("in"), e.factory.client("out"); s != nil && c != nil {
			s.audio <- protocols.Packet{Samples: []float32{0.25, 0.25}, Format: audio.Internal}
			select {
			case got := <-c.sent:
				if len(got) > 0 && got[0] == 0.25 {
					return
				}
			case <-time.After(20 * time.Millisecond):
			}
		} else {
			time.Sleep(5 * time.Millisecond)
		}
	}
	t.Fatal("audio did not reach the client")
}

func TestFactory(t *testing.T) {
	f := NewFactory(Defaults{AirPlayPort: 5000, Interface: "auto"}, quiet)
	s, err := f.Server(&db.Server{ID: "s", Name: "S", Protocol: "airplay", Config: `{"address":"127.0.0.1:0"}`})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.(*airplay.Server); !ok {
		t.Errorf("server is %T", s)
	}
	if _, err := f.Server(&db.Server{Protocol: "airplay", Config: "{"}); !isInvalid(err) {
		t.Errorf("bad JSON = %v", err)
	}
	if _, err := f.Server(&db.Server{Protocol: "upnp"}); !isInvalid(err) {
		t.Errorf("unknown protocol = %v", err)
	}

	c, err := f.Client(&db.Client{ID: "c", Name: "C", Protocol: "airplay", Config: `{"address":"10.0.0.2:7000","latency_ms":500}`})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.(*airplay.Client); !ok {
		t.Errorf("client is %T", c)
	}
	if _, err := f.Client(&db.Client{Protocol: "airplay"}); !isInvalid(err) {
		t.Errorf("airplay without address = %v", err)
	}
	if _, err := f.Client(&db.Client{Protocol: "airplay", Address: "x", Config: `{"latency_ms":-1}`}); !isInvalid(err) {
		t.Errorf("negative latency = %v", err)
	}
	if c, err := f.Client(&db.Client{Protocol: "local"}); err != nil {
		t.Error(err)
	} else if _, ok := c.(*local.Client); !ok {
		t.Errorf("client is %T", c)
	}
	if _, err := f.Client(&db.Client{Protocol: "cast"}); !isInvalid(err) {
		t.Errorf("unknown protocol = %v", err)
	}
	if a, b := NewID("x"), NewID("x"); a == b {
		t.Error("NewID repeated")
	}
}
