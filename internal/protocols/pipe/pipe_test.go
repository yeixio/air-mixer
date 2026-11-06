package pipe

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeixio/air-mixer/internal/audio"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestRunsReadsAndRestarts(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	lo, hi := minBackoff, maxBackoff
	minBackoff, maxBackoff = 10*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { minBackoff, maxBackoff = lo, hi })

	// 0.1 s of a constant 0x2000 (0.25) in s16le stereo, then exit.
	script := `i=0; while [ $i -lt 4410 ]; do printf '\000\040\000\040'; i=$((i+1)); done`
	s := New(Options{ID: "x", Name: "X", Protocol: "spotify", Command: []string{"sh", "-c", script}, Logger: quiet})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	var n int
	deadline := time.After(10 * time.Second)
	for n < 4410*2*2 { // two runs' worth: it was restarted
		select {
		case p := <-s.Audio():
			if p.Format != audio.Internal || p.Samples[0] != 0.25 {
				t.Fatalf("packet %+v %v", p.Format, p.Samples[0])
			}
			n += len(p.Samples)
		case <-deadline:
			t.Fatalf("got %d samples", n)
		}
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	if s.Running() {
		t.Error("still running")
	}
}

func TestMissingProgram(t *testing.T) {
	s := New(Options{Protocol: "spotify", Command: []string{"no-such-receiver-xyz"}})
	if err := s.Start(context.Background()); err == nil {
		t.Error("started a missing program")
	}
}

func TestShairportAirPlay2(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	if v, ok := shairportAirPlay2(); v != "" || ok {
		t.Errorf("missing program = %q, %v", v, ok)
	}
	write := func(version string) {
		script := "#!/bin/sh\necho '" + version + "'\n"
		if err := os.WriteFile(filepath.Join(bin, ShairportSync), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("4.3.7-AirPlay2-smi10-OpenSSL-Avahi-stdout")
	if _, ok := shairportAirPlay2(); !ok {
		t.Error("AirPlay 2 build not recognised")
	}
	write("3.3.8-OpenSSL-Avahi-ALSA-stdout")
	if v, ok := shairportAirPlay2(); ok || v == "" {
		t.Errorf("classic build = %q, %v", v, ok)
	}
}
