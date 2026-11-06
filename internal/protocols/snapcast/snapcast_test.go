package snapcast

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/yeixio/air-mixer/internal/audio"
	"github.com/yeixio/air-mixer/internal/protocols"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestWritesPCMAndReportsLoss(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	c, err := New(Options{ID: "snap", Name: "Snapcast", Address: ln.Addr().String(), SampleRate: audio.SampleRate, Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Send(protocols.Packet{Samples: []float32{0}, Format: audio.Internal}); !errors.Is(err, protocols.ErrNotConnected) {
		t.Errorf("Send before Connect = %v", err)
	}
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	conn, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Send(protocols.Packet{Samples: []float32{0.5, -0.5}, Format: audio.Internal}); err != nil {
		t.Fatal(err)
	}
	var got [4]byte
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(conn, got[:]); err != nil {
		t.Fatal(err)
	}
	if l, r := int16(binary.LittleEndian.Uint16(got[0:])), int16(binary.LittleEndian.Uint16(got[2:])); l != 16384 || r != -16384 {
		t.Errorf("PCM = %d, %d", l, r)
	}

	// snapserver goes away: Send reports the loss so the Manager
	// reconnects.
	conn.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		err := c.Send(protocols.Packet{Samples: make([]float32, 4096), Format: audio.Internal})
		if errors.Is(err, protocols.ErrConnectionLost) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no ErrConnectionLost, last err %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := c.Disconnect(); err != nil {
		t.Logf("Disconnect: %v", err)
	}
	if c.Connected() {
		t.Error("still connected")
	}
}

func TestDefaults(t *testing.T) {
	c, err := New(Options{Address: "snapserver.local"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Address() != "snapserver.local:4953" || c.opts.SampleRate != 48000 {
		t.Errorf("address %q, rate %d", c.Address(), c.opts.SampleRate)
	}
}
