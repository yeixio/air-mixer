package cast

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yeixio/air-mixer/internal/audio"
	"github.com/yeixio/air-mixer/internal/protocols"
	"github.com/yeixio/air-mixer/internal/protocols/httpstream"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestMessageRoundTrip(t *testing.T) {
	m := message{Source: "a", Destination: "b", Namespace: nsMedia, Payload: `{"type":"LOAD"}`}
	b := m.marshal()
	got, err := readMessage(strings.NewReader(string(b)))
	if err != nil || got != m {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err := unmarshal([]byte{0x12, 0x05, 'a'}); err == nil {
		t.Error("truncated message accepted")
	}
}

// device is a fake Cast device on the other end of a pipe.
type device struct {
	t    *testing.T
	conn net.Conn

	mu       sync.Mutex
	received []map[string]any
	loads    []string
	stops    int
}

func (d *device) send(ns, src string, v any) {
	b, _ := json.Marshal(v)
	d.conn.Write(message{Source: src, Destination: senderID, Namespace: ns, Payload: string(b)}.marshal())
}

func (d *device) status(apps ...map[string]any) {
	d.send(nsReceiver, receiverID, map[string]any{"type": "RECEIVER_STATUS", "status": map[string]any{"applications": apps}})
}

func (d *device) run() {
	for {
		m, err := readMessage(d.conn)
		if err != nil {
			return
		}
		var p map[string]any
		json.Unmarshal([]byte(m.Payload), &p)
		d.mu.Lock()
		d.received = append(d.received, p)
		d.mu.Unlock()
		switch p["type"] {
		case "LAUNCH":
			d.status(map[string]any{"appId": mediaReceiver, "sessionId": "s1", "transportId": "t1"})
		case "LOAD":
			media := p["media"].(map[string]any)
			d.mu.Lock()
			d.loads = append(d.loads, media["contentId"].(string))
			d.mu.Unlock()
		case "STOP":
			d.mu.Lock()
			d.stops++
			d.mu.Unlock()
			d.status()
		}
	}
}

func (d *device) count(f func() int) int { d.mu.Lock(); defer d.mu.Unlock(); return f() }

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func withDevice(t *testing.T) *device {
	t.Helper()
	hb, ids, rg := heartbeat, idleStop, resumeGap
	heartbeat, idleStop, resumeGap = 20*time.Millisecond, 150*time.Millisecond, 50*time.Millisecond
	orig := dialer
	d := &device{t: t}
	dialer = func(_ context.Context, addr string) (net.Conn, error) {
		if addr != "192.0.2.10:8009" {
			t.Errorf("dialed %s", addr)
		}
		a, b := net.Pipe()
		d.conn = b
		go d.run()
		return a, nil
	}
	t.Cleanup(func() { heartbeat, idleStop, resumeGap, dialer = hb, ids, rg, orig })
	return d
}

func TestCastLifecycle(t *testing.T) {
	d := withDevice(t)
	streams := httpstream.NewRegistry()
	c := New(Options{ID: "kitchen", Name: "Kitchen", Address: "192.0.2.10", Streams: streams, PublicURL: "http://airmixer.lan:9001/", Logger: quiet})
	sound := protocols.Packet{Samples: []float32{0.3, 0.3}, Format: audio.Internal}
	if err := c.Send(sound); err == nil {
		t.Error("Send before Connect worked")
	}
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := streams.Lookup("kitchen"); !ok {
		t.Error("stream not served")
	}
	// Silence doesn't launch anything.
	if err := c.Send(protocols.Packet{Samples: make([]float32, 8), Format: audio.Internal}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if c.Playing() {
		t.Fatal("launched on silence")
	}

	// Sound launches the receiver and loads the stream.
	c.Send(sound)
	waitFor(t, "LOAD", func() bool { return d.count(func() int { return len(d.loads) }) == 1 })
	if got := d.loads[0]; got != "http://airmixer.lan:9001/api/clients/kitchen/stream" {
		t.Errorf("LOAD %q", got)
	}
	if !c.Playing() {
		t.Error("not playing after LOAD")
	}

	// Another app takes over: Air-Mixer leaves it alone while its audio
	// keeps going...
	d.status(map[string]any{"appId": "YouTube", "sessionId": "y", "transportId": "ty"})
	waitFor(t, "displacement", func() bool { return !c.Playing() })
	c.Send(sound)
	time.Sleep(20 * time.Millisecond)
	if d.count(func() int { return len(d.loads) }) != 1 {
		t.Fatal("took the device back while audio continued")
	}
	// ...and casts again when audio starts after a pause.
	time.Sleep(80 * time.Millisecond)
	c.Send(sound)
	waitFor(t, "second LOAD", func() bool { return d.count(func() int { return len(d.loads) }) == 2 })

	// Silence stops the receiver app.
	waitFor(t, "idle STOP", func() bool { return d.count(func() int { return d.stops }) == 1 })
	if c.Playing() {
		t.Error("still playing after idle stop")
	}

	// Heartbeats were sent.
	pings := d.count(func() int {
		n := 0
		for _, p := range d.received {
			if p["type"] == "PING" {
				n++
			}
		}
		return n
	})
	if pings == 0 {
		t.Error("no heartbeats")
	}

	// The device goes away: Send reports it, so the Manager reconnects.
	d.conn.Close()
	waitFor(t, "loss", func() bool { return !c.Connected() })
	if err := c.Send(sound); err == nil || !strings.Contains(err.Error(), "connection lost") {
		t.Errorf("Send after loss = %v", err)
	}
	if err := c.Disconnect(); err != nil {
		t.Error(err)
	}
	if _, ok := streams.Lookup("kitchen"); ok {
		t.Error("stream still served")
	}
}
