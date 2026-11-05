// Package httpstream serves an output's audio as a live WAV stream over
// HTTP, for players that pull audio rather than have it pushed to them:
// VLC, a browser, or a Matinee player (see package matinee).
//
// The stream is 16-bit little-endian stereo PCM at the router's rate, with
// a WAV header whose sizes are 0xFFFFFFFF ("as long as it goes"). Each
// listener gets audio in real time, with silence while nothing is routed,
// so players don't stall or give up between songs.
package httpstream

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/yeixio/air-mixer/internal/audio"
	"github.com/yeixio/air-mixer/internal/protocols"
)

const (
	// tick is how often a listener is sent audio.
	tick = 20 * time.Millisecond
	// maxQueued bounds the audio waiting for a listener, and so the delay
	// a slow listener adds: 1 s.
	maxQueued = audio.SampleRate * audio.Channels
	// preroll is silence sent first, so players fill their buffer and
	// start at once.
	preroll = audio.SampleRate * audio.Channels / 2
)

// Registry finds a running stream by its output's ID, for the HTTP
// handler. It is safe for concurrent use.
type Registry struct {
	mu      sync.Mutex
	streams map[string]*Stream
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{streams: make(map[string]*Stream)} }

func (r *Registry) add(s *Stream) {
	r.mu.Lock()
	r.streams[s.id] = s
	r.mu.Unlock()
}

func (r *Registry) remove(s *Stream) {
	r.mu.Lock()
	if r.streams[s.id] == s {
		delete(r.streams, s.id)
	}
	r.mu.Unlock()
}

// Lookup returns the connected stream for an output ID.
func (r *Registry) Lookup(id string) (*Stream, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.streams[id]
	return s, ok
}

// Stream is an output that listeners pull over HTTP. It implements
// protocols.Client; Connect makes it available in its Registry.
type Stream struct {
	id, name string
	registry *Registry
	log      *slog.Logger

	mu        sync.Mutex
	connected bool
	listeners map[*listener]struct{}
}

type listener struct {
	mu    sync.Mutex
	queue []float32
}

// New returns a stream output registered in r once connected.
func New(id, name string, r *Registry, logger *slog.Logger) *Stream {
	if logger == nil {
		logger = slog.Default()
	}
	return &Stream{
		id: id, name: name, registry: r,
		log:       logger.With("component", "stream-client", "client", id),
		listeners: make(map[*listener]struct{}),
	}
}

func (s *Stream) ID() string       { return s.id }
func (s *Stream) Name() string     { return s.name }
func (s *Stream) Protocol() string { return "stream" }

// Connected reports whether the stream is being served.
func (s *Stream) Connected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected
}

// Connect starts serving the stream.
func (s *Stream) Connect(context.Context) error {
	s.mu.Lock()
	s.connected = true
	s.mu.Unlock()
	if s.registry != nil {
		s.registry.add(s)
	}
	return nil
}

// Disconnect stops serving the stream; listeners are cut off.
func (s *Stream) Disconnect() error {
	if s.registry != nil {
		s.registry.remove(s)
	}
	s.mu.Lock()
	s.connected = false
	clear(s.listeners)
	s.mu.Unlock()
	return nil
}

// Listeners is how many players are pulling the stream.
func (s *Stream) Listeners() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.listeners)
}

// Send queues audio for every listener. A listener that is behind loses
// the oldest audio rather than holding the others up.
func (s *Stream) Send(p protocols.Packet) error {
	if p.Format != audio.Internal {
		return fmt.Errorf("stream: expected %+v audio, got %+v", audio.Internal, p.Format)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.connected {
		return protocols.ErrNotConnected
	}
	full := false
	for l := range s.listeners {
		l.mu.Lock()
		l.queue = append(l.queue, p.Samples...)
		if over := len(l.queue) - maxQueued; over > 0 {
			l.queue = l.queue[over:]
			full = true
		}
		l.mu.Unlock()
	}
	if full {
		return protocols.ErrBufferFull
	}
	return nil
}

// ServeHTTP sends the live stream until the listener leaves or the output
// is disconnected.
func (s *Stream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	l := &listener{}
	s.mu.Lock()
	if !s.connected {
		s.mu.Unlock()
		http.Error(w, "the output isn't connected", http.StatusServiceUnavailable)
		return
	}
	s.listeners[l] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.listeners, l)
		s.mu.Unlock()
	}()
	s.log.Info("listener joined", "remote", r.RemoteAddr)
	defer s.log.Info("listener left", "remote", r.RemoteAddr)

	h := w.Header()
	h.Set("Content-Type", "audio/wav")
	h.Set("Cache-Control", "no-store")
	h.Set("Icy-Name", s.name)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	flush := func() {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	if _, err := w.Write(WAVHeader(audio.SampleRate, audio.Channels)); err != nil {
		return
	}
	if _, err := w.Write(make([]byte, preroll*2)); err != nil {
		return
	}
	flush()

	t := time.NewTicker(tick)
	defer t.Stop()
	start, sent := time.Now(), 0 // samples sent after the preroll
	for {
		select {
		case <-r.Context().Done():
			return
		case now := <-t.C:
			if !s.member(l) {
				return
			}
			due := int(now.Sub(start).Seconds()*audio.SampleRate)*audio.Channels - sent
			if due <= 0 {
				continue
			}
			l.mu.Lock()
			k := min(due, len(l.queue))
			buf := encode(l.queue[:k], due)
			l.queue = l.queue[k:]
			l.mu.Unlock()
			sent += due
			if _, err := w.Write(buf); err != nil {
				return
			}
			flush()
		}
	}
}

func (s *Stream) member(l *listener) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.listeners[l]
	return ok
}

// encode converts samples to 16-bit little-endian, padded with silence to n
// samples.
func encode(samples []float32, n int) []byte {
	b := make([]byte, 2*n)
	for i, v := range samples {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(audio.FloatToInt16(v)))
	}
	return b
}

// WAVHeader is the header of a 16-bit PCM WAV of unknown length.
func WAVHeader(rate, channels int) []byte {
	const unknown = 0xFFFFFFFF
	b := make([]byte, 44)
	copy(b[0:], "RIFF")
	binary.LittleEndian.PutUint32(b[4:], unknown)
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1) // PCM
	binary.LittleEndian.PutUint16(b[22:], uint16(channels))
	binary.LittleEndian.PutUint32(b[24:], uint32(rate))
	binary.LittleEndian.PutUint32(b[28:], uint32(rate*channels*2))
	binary.LittleEndian.PutUint16(b[32:], uint16(channels*2))
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], unknown)
	return b
}
