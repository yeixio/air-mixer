package protocols

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/yeixio/air-mixer/internal/audio"
)

// Router is the part of router.Router the Manager uses.
type Router interface {
	RegisterSource(id, name string, stream <-chan []float32)
	UnregisterSource(id string)
	RegisterSink(id, name string, ch chan<- []float32)
	UnregisterSink(id string)
}

// Adapter states reported by Manager.Status.
const (
	StateRunning    = "running"    // a server is accepting connections
	StateConnecting = "connecting" // a client is trying to reach its device
	StateConnected  = "connected"  // a client is playing
	StateFailed     = "failed"     // a client gave up; see Status.Error
)

// Status describes a registered adapter.
type Status struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Kind     string `json:"kind"` // "server" or "client"
	State    string `json:"state"`
	Error    string `json:"error,omitempty"`
}

// ErrUnknownAdapter is returned when removing an adapter that is not
// registered.
var ErrUnknownAdapter = errors.New("adapter not registered")

const (
	streamBuffer      = 64
	connectTimeout    = 10 * time.Second
	minConnectBackoff = time.Second
	maxConnectBackoff = 30 * time.Second
)

// Manager runs protocol adapters and connects them to the router: servers
// become router sources and clients become router sinks. Clients that
// cannot connect are retried in the background with backoff, so a speaker
// that is switched off starts playing when it comes back.
type Manager struct {
	router Router
	log    *slog.Logger

	mu       sync.Mutex
	adapters map[string]*adapter
	closed   bool
}

type adapter struct {
	kind   string
	server Server
	client Client
	cancel context.CancelFunc
	done   chan struct{}

	mu    sync.Mutex
	state string
	err   error
}

func (a *adapter) set(state string, err error) {
	a.mu.Lock()
	a.state, a.err = state, err
	a.mu.Unlock()
}

// NewManager returns a Manager that registers adapters with r.
func NewManager(r Router, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{router: r, log: logger.With("component", "protocols"), adapters: make(map[string]*adapter)}
}

func (m *Manager) reserve(id string, a *adapter) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("protocols: manager closed")
	}
	if _, ok := m.adapters[id]; ok {
		return fmt.Errorf("protocols: %q is already registered", id)
	}
	m.adapters[id] = a
	return nil
}

func (m *Manager) release(id string) {
	m.mu.Lock()
	delete(m.adapters, id)
	m.mu.Unlock()
}

// AddServer starts s and makes its audio available to routes under its
// name. IDs are shared between servers and clients and must be unique.
func (m *Manager) AddServer(ctx context.Context, s Server) error {
	a := &adapter{kind: "server", server: s, done: make(chan struct{})}
	if err := m.reserve(s.ID(), a); err != nil {
		return err
	}
	if err := s.Start(ctx); err != nil {
		m.release(s.ID())
		return err
	}
	a.set(StateRunning, nil)

	pumpCtx, cancel := context.WithCancel(context.Background())
	stream := make(chan []float32, streamBuffer)
	m.router.RegisterSource(s.ID(), s.Name(), stream)
	go func() {
		defer close(a.done)
		defer close(stream)
		m.pumpServer(pumpCtx, s, stream)
	}()
	m.mu.Lock()
	a.cancel = cancel // from now on the server can be removed
	m.mu.Unlock()
	m.log.Info("server added", "id", s.ID(), "name", s.Name(), "protocol", s.Protocol())
	return nil
}

// pumpServer converts a server's audio to the internal format and passes
// it to the router without blocking.
func (m *Manager) pumpServer(ctx context.Context, s Server, out chan<- []float32) {
	var (
		conv   *audio.Converter
		format audio.Format
	)
	for {
		var p Packet
		select {
		case <-ctx.Done():
			return
		case p = <-s.Audio():
		}
		samples := p.Samples
		if p.Format != audio.Internal {
			if conv == nil || p.Format != format {
				c, err := audio.NewConverter(p.Format, audio.Internal)
				if err != nil {
					m.log.Warn("dropping audio in unsupported format", "server", s.ID(), "err", err)
					continue
				}
				conv, format = c, p.Format
			}
			var err error
			if samples, err = conv.Process(samples); err != nil {
				m.log.Warn("dropping audio that failed to convert", "server", s.ID(), "err", err)
				continue
			}
		}
		select {
		case out <- samples:
		default:
		}
	}
}

// RemoveServer stops a server and removes it from the router.
func (m *Manager) RemoveServer(id string) error {
	a, err := m.take(id, "server")
	if err != nil {
		return err
	}
	a.cancel()
	<-a.done
	m.router.UnregisterSource(id)
	err = a.server.Stop()
	m.log.Info("server removed", "id", id)
	return err
}

// AddClient registers c with the router and connects it in the background,
// retrying until it connects, fails permanently or is removed.
func (m *Manager) AddClient(c Client) error {
	ctx, cancel := context.WithCancel(context.Background())
	a := &adapter{kind: "client", client: c, cancel: cancel, done: make(chan struct{}), state: StateConnecting}
	if err := m.reserve(c.ID(), a); err != nil {
		cancel()
		return err
	}
	sink := make(chan []float32, streamBuffer)
	m.router.RegisterSink(c.ID(), c.Name(), sink)
	go func() {
		defer close(a.done)
		if m.connect(ctx, a) {
			m.pumpClient(ctx, c, sink)
		}
	}()
	m.log.Info("client added", "id", c.ID(), "name", c.Name(), "protocol", c.Protocol())
	return nil
}

// connect retries Connect with exponential backoff. It reports whether the
// client connected.
func (m *Manager) connect(ctx context.Context, a *adapter) bool {
	c := a.client
	backoff := minConnectBackoff
	for {
		attempt, cancel := context.WithTimeout(ctx, connectTimeout)
		err := c.Connect(attempt)
		cancel()
		if err == nil {
			a.set(StateConnected, nil)
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		if errors.Is(err, ErrUnsupportedDevice) {
			a.set(StateFailed, err)
			m.log.Error("client cannot be used", "id", c.ID(), "err", err)
			return false
		}
		a.set(StateConnecting, err)
		m.log.Warn("client connection failed, will retry", "id", c.ID(), "err", err, "retry_in", backoff)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxConnectBackoff)
	}
}

func (m *Manager) pumpClient(ctx context.Context, c Client, in <-chan []float32) {
	// Audio queued while connecting is stale; start from now.
	for len(in) > 0 {
		<-in
	}
	var dropped int
	for {
		select {
		case <-ctx.Done():
			return
		case buf := <-in:
			err := c.Send(Packet{Samples: buf, Format: audio.Internal})
			switch {
			case errors.Is(err, ErrBufferFull):
				if dropped++; dropped%100 == 1 {
					m.log.Debug("client is behind, dropping audio", "id", c.ID(), "dropped_buffers", dropped)
				}
			case err != nil:
				m.log.Warn("send failed", "id", c.ID(), "err", err)
			}
		}
	}
}

// RemoveClient disconnects a client and removes it from the router.
func (m *Manager) RemoveClient(id string) error {
	a, err := m.take(id, "client")
	if err != nil {
		return err
	}
	a.cancel()
	<-a.done
	m.router.UnregisterSink(id)
	err = a.client.Disconnect()
	m.log.Info("client removed", "id", id)
	return err
}

func (m *Manager) take(id, kind string) (*adapter, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.adapters[id]
	if !ok || a.kind != kind || a.cancel == nil {
		return nil, fmt.Errorf("protocols: %s %q: %w", kind, id, ErrUnknownAdapter)
	}
	delete(m.adapters, id)
	return a, nil
}

// Status returns every registered adapter, sorted by ID.
func (m *Manager) Status() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.adapters))
	for id, a := range m.adapters {
		st := Status{ID: id, Kind: a.kind}
		if a.server != nil {
			st.Name, st.Protocol = a.server.Name(), a.server.Protocol()
		} else {
			st.Name, st.Protocol = a.client.Name(), a.client.Protocol()
		}
		a.mu.Lock()
		st.State = a.state
		if a.err != nil {
			st.Error = a.err.Error()
		}
		a.mu.Unlock()
		if st.State == "" {
			continue // a server still starting
		}
		out = append(out, st)
	}
	slices.SortFunc(out, func(a, b Status) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// Close removes every adapter. The Manager cannot be used afterwards.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	var servers, clients []string
	for id, a := range m.adapters {
		if a.kind == "server" {
			servers = append(servers, id)
		} else {
			clients = append(clients, id)
		}
	}
	m.mu.Unlock()
	for _, id := range clients {
		if err := m.RemoveClient(id); err != nil && !errors.Is(err, ErrUnknownAdapter) {
			m.log.Warn("disconnect client", "id", id, "err", err)
		}
	}
	for _, id := range servers {
		if err := m.RemoveServer(id); err != nil && !errors.Is(err, ErrUnknownAdapter) {
			m.log.Warn("stop server", "id", id, "err", err)
		}
	}
}
