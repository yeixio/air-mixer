package protocols

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/yeixio/air-mixer/internal/audio"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// fakeRouter records registrations.
type fakeRouter struct {
	mu      sync.Mutex
	sources map[string]<-chan []float32
	sinks   map[string]chan<- []float32
}

func newFakeRouter() *fakeRouter {
	return &fakeRouter{sources: map[string]<-chan []float32{}, sinks: map[string]chan<- []float32{}}
}

func (r *fakeRouter) RegisterSource(id, _ string, s <-chan []float32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sources[id] = s
}
func (r *fakeRouter) UnregisterSource(id string) { r.mu.Lock(); delete(r.sources, id); r.mu.Unlock() }
func (r *fakeRouter) RegisterSink(id, _ string, c chan<- []float32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sinks[id] = c
}
func (r *fakeRouter) UnregisterSink(id string) { r.mu.Lock(); delete(r.sinks, id); r.mu.Unlock() }

func (r *fakeRouter) source(id string) <-chan []float32 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sources[id]
}

func (r *fakeRouter) sink(id string) chan<- []float32 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sinks[id]
}

type fakeServer struct {
	id       string
	audio    chan Packet
	startErr error
	stopped  bool
}

func (s *fakeServer) ID() string                  { return s.id }
func (s *fakeServer) Name() string                { return s.id + "-name" }
func (s *fakeServer) Protocol() string            { return "fake" }
func (s *fakeServer) Start(context.Context) error { return s.startErr }
func (s *fakeServer) Stop() error                 { s.stopped = true; return nil }
func (s *fakeServer) Audio() <-chan Packet        { return s.audio }

type fakeClient struct {
	id string

	mu           sync.Mutex
	failures     int   // Connect fails this many times
	connectErr   error // returned for failures
	attempts     int
	connected    bool
	disconnected bool
	sent         chan []float32
	sendErr      error
}

func (c *fakeClient) ID() string       { return c.id }
func (c *fakeClient) Name() string     { return c.id + "-name" }
func (c *fakeClient) Protocol() string { return "fake" }
func (c *fakeClient) Connect(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.attempts++
	if c.attempts <= c.failures {
		return c.connectErr
	}
	c.connected = true
	return nil
}
func (c *fakeClient) Disconnect() error {
	c.mu.Lock()
	c.disconnected = true
	c.mu.Unlock()
	return nil
}
func (c *fakeClient) Connected() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.connected }
func (c *fakeClient) Send(p Packet) error {
	if p.Format != audio.Internal {
		return errors.New("bad format")
	}
	c.sent <- p.Samples
	return c.sendErr
}

func waitState(t *testing.T, m *Manager, id, state string) Status {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, st := range m.Status() {
			if st.ID == id && st.State == state {
				return st
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s never reached %s: %+v", id, state, m.Status())
	return Status{}
}

func TestServerLifecycle(t *testing.T) {
	r := newFakeRouter()
	m := NewManager(r, quiet)
	s := &fakeServer{id: "s1", audio: make(chan Packet, 4)}
	if err := m.AddServer(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if err := m.AddServer(context.Background(), &fakeServer{id: "s1"}); err == nil {
		t.Error("duplicate ID accepted")
	}
	st := waitState(t, m, "s1", StateRunning)
	if st.Kind != "server" || st.Name != "s1-name" || st.Protocol != "fake" {
		t.Errorf("status %+v", st)
	}

	src := r.source("s1")
	s.audio <- Packet{Samples: []float32{0.1, 0.2}, Format: audio.Internal}
	if got := <-src; len(got) != 2 || got[0] != 0.1 {
		t.Errorf("internal-format audio altered: %v", got)
	}
	// Mono 22.05 kHz is converted: two input frames become about four
	// stereo frames.
	s.audio <- Packet{Samples: []float32{0.5, 0.5}, Format: audio.Format{SampleRate: 22050, Channels: 1}}
	if got := <-src; len(got) < 4 || len(got)%2 != 0 {
		t.Errorf("converted audio has %d samples", len(got))
	}
	s.audio <- Packet{Samples: []float32{1}, Format: audio.Format{}}
	s.audio <- Packet{Samples: []float32{0.3, 0.3}, Format: audio.Internal}
	if got := <-src; got[0] != 0.3 {
		t.Errorf("invalid format not dropped: %v", got)
	}

	if err := m.RemoveServer("s1"); err != nil {
		t.Fatal(err)
	}
	if !s.stopped || r.source("s1") != nil {
		t.Error("server not stopped and unregistered")
	}
	if _, ok := <-src; ok {
		t.Error("source stream not closed")
	}
	if err := m.RemoveServer("s1"); !errors.Is(err, ErrUnknownAdapter) {
		t.Errorf("second RemoveServer = %v", err)
	}
}

func TestServerStartError(t *testing.T) {
	m := NewManager(newFakeRouter(), quiet)
	if err := m.AddServer(context.Background(), &fakeServer{id: "s1", startErr: errors.New("port in use")}); err == nil {
		t.Fatal("start error swallowed")
	}
	if len(m.Status()) != 0 {
		t.Error("failed server still listed")
	}
	// The ID is free again.
	if err := m.AddServer(context.Background(), &fakeServer{id: "s1", audio: make(chan Packet)}); err != nil {
		t.Fatal(err)
	}
	m.Close()
}

func TestClientLifecycle(t *testing.T) {
	r := newFakeRouter()
	m := NewManager(r, quiet)
	c := &fakeClient{id: "c1", sent: make(chan []float32, 4), sendErr: ErrBufferFull}
	if err := m.AddClient(c); err != nil {
		t.Fatal(err)
	}
	if err := m.AddClient(&fakeClient{id: "c1"}); err == nil {
		t.Error("duplicate ID accepted")
	}
	waitState(t, m, "c1", StateConnected)

	r.sink("c1") <- []float32{0.5, 0.5}
	select {
	case got := <-c.sent:
		if got[0] != 0.5 {
			t.Errorf("sent %v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("audio not forwarded")
	}

	if err := m.RemoveServer("c1"); !errors.Is(err, ErrUnknownAdapter) {
		t.Errorf("RemoveServer on a client = %v", err)
	}
	if err := m.RemoveClient("c1"); err != nil {
		t.Fatal(err)
	}
	if !c.disconnected || r.sink("c1") != nil {
		t.Error("client not disconnected and unregistered")
	}
}

func TestClientRetry(t *testing.T) {
	m := NewManager(newFakeRouter(), quiet)
	c := &fakeClient{id: "c1", failures: 1, connectErr: errors.New("connection refused"), sent: make(chan []float32, 1)}
	if err := m.AddClient(c); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "c1", StateConnecting)
	waitState(t, m, "c1", StateConnected) // after the one-second backoff
	m.Close()
	if !c.disconnected {
		t.Error("Close did not disconnect")
	}
	if err := m.AddClient(&fakeClient{id: "c2"}); err == nil {
		t.Error("AddClient after Close succeeded")
	}
}

func TestClientUnsupported(t *testing.T) {
	m := NewManager(newFakeRouter(), quiet)
	err := errors.Join(errors.New("needs pairing"), ErrUnsupportedDevice)
	c := &fakeClient{id: "c1", failures: 100, connectErr: err}
	if err := m.AddClient(c); err != nil {
		t.Fatal(err)
	}
	st := waitState(t, m, "c1", StateFailed)
	if st.Error == "" {
		t.Error("no error reported")
	}
	time.Sleep(50 * time.Millisecond)
	c.mu.Lock()
	attempts := c.attempts
	c.mu.Unlock()
	if attempts != 1 {
		t.Errorf("unsupported device retried %d times", attempts)
	}
	if err := m.RemoveClient("c1"); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveWhileConnecting(t *testing.T) {
	m := NewManager(newFakeRouter(), quiet)
	c := &fakeClient{id: "c1", failures: 100, connectErr: errors.New("timeout")}
	if err := m.AddClient(c); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "c1", StateConnecting)
	done := make(chan error)
	go func() { done <- m.RemoveClient("c1") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RemoveClient blocked on the retry loop")
	}
}
