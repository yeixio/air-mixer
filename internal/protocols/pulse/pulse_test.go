package pulse

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/yeixio/air-mixer/internal/audio"
	"github.com/yeixio/air-mixer/internal/protocols"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type fakeStream struct {
	fill func([]float32) int

	mu     sync.Mutex
	err    error
	closed int
}

func (s *fakeStream) Check() error { s.mu.Lock(); defer s.mu.Unlock(); return s.err }
func (s *fakeStream) Close()       { s.mu.Lock(); s.closed++; s.mu.Unlock() }
func (s *fakeStream) Sink() string { return "bluez_output.AA_BB_CC_DD_EE_FF.1" }

func (s *fakeStream) isClosed() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.closed > 0 }

func withFakeDial(t *testing.T, err error) *fakeStream {
	t.Helper()
	fs := &fakeStream{}
	orig := dial
	dial = func(_ Options, fill func([]float32) int) (stream, error) {
		if err != nil {
			return nil, err
		}
		fs.fill = fill
		return fs, nil
	}
	t.Cleanup(func() { dial = orig })
	return fs
}

func send(c *Client, s ...float32) error {
	return c.Send(protocols.Packet{Samples: s, Format: audio.Internal})
}

func TestClient(t *testing.T) {
	fs := withFakeDial(t, nil)
	c := NewClient(Options{ID: "p1", Name: "Kitchen", Delay: time.Second, Logger: quiet})
	if c.ID() != "p1" || c.Name() != "Kitchen" || c.Protocol() != "pulse" {
		t.Error("identity")
	}
	if err := send(c, 1); !errors.Is(err, protocols.ErrNotConnected) {
		t.Errorf("Send before Connect = %v", err)
	}
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !c.Connected() {
		t.Fatal("not connected")
	}
	if err := c.Connect(context.Background()); err != nil {
		t.Errorf("second Connect = %v", err)
	}
	if err := c.Send(protocols.Packet{Format: audio.Format{SampleRate: 48000, Channels: 2}}); err == nil {
		t.Error("wrong format accepted")
	}

	// The delay plays as silence before the audio.
	if err := send(c, 0.5, 0.5); err != nil {
		t.Fatal(err)
	}
	delay := audio.Samples(time.Second)
	buf := make([]float32, delay+2)
	fs.fill(buf)
	if buf[delay-1] != 0 || buf[delay] != 0.5 {
		t.Errorf("audio at %v, want it after %d samples of silence", buf[delay-1:], delay)
	}

	if err := send(c, make([]float32, maxBuffered+delay+1)...); !errors.Is(err, protocols.ErrBufferFull) {
		t.Errorf("oversized Send = %v", err)
	}

	if err := c.Disconnect(); err != nil || !fs.isClosed() || c.Connected() {
		t.Errorf("Disconnect = %v, closed %v", err, fs.isClosed())
	}
	if err := c.Disconnect(); err != nil {
		t.Errorf("second Disconnect = %v", err)
	}
}

func TestConnectError(t *testing.T) {
	withFakeDial(t, ErrNoSink)
	c := NewClient(Options{ID: "p1", Name: "Kitchen"})
	if err := c.Connect(context.Background()); !errors.Is(err, ErrNoSink) || c.Connected() {
		t.Errorf("Connect = %v", err)
	}
}

func TestConnectCancelled(t *testing.T) {
	release := make(chan struct{})
	fs := &fakeStream{}
	orig := dial
	dial = func(Options, func([]float32) int) (stream, error) { <-release; return fs, nil }
	defer func() { dial = orig }()

	c := NewClient(Options{ID: "p1", Name: "Kitchen"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := c.Connect(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Connect = %v", err)
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for !fs.isClosed() {
		if time.Now().After(deadline) {
			t.Fatal("stream opened after the deadline was not closed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestLostSink(t *testing.T) {
	fs := withFakeDial(t, nil)
	c := NewClient(Options{ID: "p1", Name: "Kitchen", Logger: quiet})
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer c.Disconnect()
	fs.mu.Lock()
	fs.err = errors.New("sink went away")
	fs.mu.Unlock()

	deadline := time.Now().Add(3 * checkInterval)
	for c.Connected() {
		if time.Now().After(deadline) {
			t.Fatal("lost sink not noticed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !fs.isClosed() {
		t.Error("stream left playing on another sink")
	}
	if err := send(c, 1, 1); !errors.Is(err, protocols.ErrConnectionLost) {
		t.Errorf("Send after loss = %v", err)
	}
}

func TestMatchSink(t *testing.T) {
	sinks := []sinkInfo{
		{id: "alsa_output.platform-bcm2835_audio.stereo-fallback", description: "Built-in Audio Stereo"},
		{id: "bluez_sink.AA_BB_CC_DD_EE_01.a2dp_sink", description: "JBL Flip 5"},
		{id: "bluez_output.AA_BB_CC_DD_EE_02.1", description: "Kitchen Speaker"},
	}
	for spec, want := range map[string]int{
		"bluez_output.AA_BB_CC_DD_EE_02.1": 2,
		"jbl flip 5":                       1,
		"aa:bb:cc:dd:ee:02":                2,
		"AA-BB-CC-DD-EE-01":                1,
	} {
		got, err := matchSink(sinks, spec)
		if err != nil || got != want {
			t.Errorf("matchSink(%q) = %d, %v; want %d", spec, got, err, want)
		}
	}
	if _, err := matchSink(sinks, "AA:BB:CC:DD:EE:03"); !errors.Is(err, ErrNoSink) {
		t.Errorf("missing sink: %v", err)
	}
}
