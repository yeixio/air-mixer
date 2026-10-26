package local

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"math"
	"testing"

	"github.com/yeixio/air-mixer/internal/audio"
	"github.com/yeixio/air-mixer/internal/protocols"
)

type fakePlayer struct {
	r       io.Reader
	playing bool
	closed  bool
}

func (p *fakePlayer) Play()        { p.playing = true }
func (p *fakePlayer) Close() error { p.closed = true; return nil }

func withFakePlayer(t *testing.T) *fakePlayer {
	t.Helper()
	fp := &fakePlayer{}
	orig := newPlayer
	newPlayer = func(r io.Reader) (player, error) { fp.r = r; return fp, nil }
	t.Cleanup(func() { newPlayer = orig })
	return fp
}

func read(t *testing.T, r io.Reader, n int) []float32 {
	t.Helper()
	b := make([]byte, 4*n+3) // a trailing partial sample is left unread
	got, err := r.Read(b)
	if err != nil || got != 4*n {
		t.Fatalf("Read = %d, %v", got, err)
	}
	out := make([]float32, n)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return out
}

func TestClient(t *testing.T) {
	fp := withFakePlayer(t)
	c := NewClient("l1", "Speakers", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if c.ID() != "l1" || c.Name() != "Speakers" || c.Protocol() != "local" {
		t.Error("identity")
	}
	if err := c.Send(protocols.Packet{Format: audio.Internal}); !errors.Is(err, protocols.ErrNotConnected) {
		t.Errorf("Send before Connect = %v", err)
	}
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !c.Connected() || !fp.playing {
		t.Fatal("not playing after Connect")
	}
	if err := c.Connect(context.Background()); err != nil {
		t.Errorf("second Connect = %v", err)
	}

	// Silence until audio arrives.
	for _, s := range read(t, fp.r, 4) {
		if s != 0 {
			t.Fatalf("expected silence, got %v", s)
		}
	}

	if err := c.Send(protocols.Packet{Samples: []float32{0.5, -0.5, 0.25}, Format: audio.Internal}); err != nil {
		t.Fatal(err)
	}
	got := read(t, fp.r, 2)
	if got[0] != 0.5 || got[1] != -0.5 {
		t.Errorf("first read %v", got)
	}
	got = read(t, fp.r, 3)
	if got[0] != 0.25 || got[1] != 0 || got[2] != 0 {
		t.Errorf("second read %v, want the remainder then silence", got)
	}
	if n, _ := fp.r.Read(make([]byte, 3)); n != 0 {
		t.Errorf("read of less than one sample returned %d", n)
	}

	if err := c.Send(protocols.Packet{Format: audio.Format{SampleRate: 48000, Channels: 2}}); err == nil {
		t.Error("wrong format accepted")
	}
	big := make([]float32, maxBuffered)
	if err := c.Send(protocols.Packet{Samples: big, Format: audio.Internal}); err != nil {
		t.Fatal(err)
	}
	if err := c.Send(protocols.Packet{Samples: []float32{1}, Format: audio.Internal}); !errors.Is(err, protocols.ErrBufferFull) {
		t.Errorf("Send past the limit = %v", err)
	}

	if err := c.Disconnect(); err != nil || !fp.closed || c.Connected() {
		t.Errorf("Disconnect = %v, closed %v", err, fp.closed)
	}
	if err := c.Disconnect(); err != nil {
		t.Errorf("second Disconnect = %v", err)
	}
}

func TestConnectError(t *testing.T) {
	orig := newPlayer
	newPlayer = func(io.Reader) (player, error) { return nil, errors.New("no device") }
	defer func() { newPlayer = orig }()
	c := NewClient("l1", "Speakers", nil)
	if err := c.Connect(context.Background()); err == nil || c.Connected() {
		t.Errorf("Connect = %v", err)
	}
}
