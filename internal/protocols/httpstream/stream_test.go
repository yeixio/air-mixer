package httpstream

import (
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yeixio/air-mixer/internal/audio"
	"github.com/yeixio/air-mixer/internal/protocols"
)

// A listener gets a WAV header, silence while nothing plays, then the
// audio, in real time.
func TestStream(t *testing.T) {
	reg := NewRegistry()
	s := New("tap", "Tap", reg, nil)
	if err := s.Send(protocols.Packet{Samples: []float32{0, 0}, Format: audio.Internal}); err != protocols.ErrNotConnected {
		t.Fatalf("before connect: %v", err)
	}
	if _, ok := reg.Lookup("tap"); ok {
		t.Fatal("registered before connect")
	}
	if err := s.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, ok := reg.Lookup("tap")
	if !ok || got != s {
		t.Fatal("not registered")
	}
	srv := httptest.NewServer(s)
	defer srv.Close()
	res, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	hdr := make([]byte, 44)
	if _, err := io.ReadFull(res.Body, hdr); err != nil {
		t.Fatal(err)
	}
	if string(hdr[:4]) != "RIFF" || binary.LittleEndian.Uint32(hdr[24:]) != audio.SampleRate || binary.LittleEndian.Uint16(hdr[22:]) != 2 {
		t.Fatalf("header % x", hdr)
	}
	for s.Listeners() == 0 {
		time.Sleep(time.Millisecond)
	}

	start := time.Now()
	// The preroll and a little more is silence.
	silence := make([]byte, preroll*2+audio.SampleRate/10*4)
	if _, err := io.ReadFull(res.Body, silence); err != nil {
		t.Fatal(err)
	}
	for i, b := range silence {
		if b != 0 {
			t.Fatalf("byte %d of the silence is %d", i, b)
		}
	}
	if took := time.Since(start); took < 50*time.Millisecond {
		t.Fatalf("100 ms after the preroll came in %v: faster than real time", took)
	}
	if err := s.Send(protocols.Packet{Samples: []float32{0.25, -0.25}, Format: audio.Internal}); err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, 4)
	for deadline := time.Now().Add(2 * time.Second); ; {
		if _, err := io.ReadFull(res.Body, frame); err != nil {
			t.Fatal(err)
		}
		if l, r := int16(binary.LittleEndian.Uint16(frame)), int16(binary.LittleEndian.Uint16(frame[2:])); l != 0 {
			if l != audio.FloatToInt16(0.25) || r != audio.FloatToInt16(-0.25) {
				t.Fatalf("frame %d %d", l, r)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the audio never came")
		}
	}

	// Disconnecting ends the stream.
	s.Disconnect()
	if _, ok := reg.Lookup("tap"); ok {
		t.Fatal("still registered")
	}
	done := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, res.Body); done <- err }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the stream didn't end")
	}
}
