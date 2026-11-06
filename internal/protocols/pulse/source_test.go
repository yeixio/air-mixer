package pulse

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeRecorder struct {
	mu     sync.Mutex
	err    error
	closed bool
}

func (r *fakeRecorder) Check() error   { r.mu.Lock(); defer r.mu.Unlock(); return r.err }
func (r *fakeRecorder) Close()         { r.mu.Lock(); r.closed = true; r.mu.Unlock() }
func (r *fakeRecorder) Source() string { return "bluez_input.11_22_33_44_55_66.2" }

func (r *fakeRecorder) fail(err error) { r.mu.Lock(); r.err = err; r.mu.Unlock() }

func TestSourceWaitsRecordsAndRecovers(t *testing.T) {
	ri, ci := retryInterval, sourceCheckInterval
	retryInterval, sourceCheckInterval = 5*time.Millisecond, 5*time.Millisecond
	orig := dialRecord
	t.Cleanup(func() { retryInterval, sourceCheckInterval, dialRecord = ri, ci, orig })

	var (
		mu    sync.Mutex
		dials int
		write func([]float32)
		recs  []*fakeRecorder
	)
	dialRecord = func(o SourceOptions, w func([]float32)) (recorder, error) {
		mu.Lock()
		defer mu.Unlock()
		dials++
		if dials < 3 { // the phone isn't connected yet
			return nil, ErrNoSource
		}
		if o.Source != "11:22:33:44:55:66" {
			t.Errorf("source = %q", o.Source)
		}
		write = w
		r := &fakeRecorder{}
		recs = append(recs, r)
		return r, nil
	}

	s := NewSource(SourceOptions{ID: "bt", Name: "Phone", Protocol: "bluetooth", Source: "11:22:33:44:55:66", Logger: quiet})
	if s.Protocol() != "bluetooth" {
		t.Errorf("Protocol = %q", s.Protocol())
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return s.Recording() != "" })

	mu.Lock()
	w := write
	mu.Unlock()
	buf := []float32{0.5, -0.5}
	w(buf)
	buf[0] = 0 // the source must have copied it
	select {
	case p := <-s.Audio():
		if p.Samples[0] != 0.5 || p.Samples[1] != -0.5 {
			t.Errorf("samples = %v", p.Samples)
		}
	case <-time.After(time.Second):
		t.Fatal("no audio")
	}

	// The phone disconnects: the stream is closed and opened again.
	mu.Lock()
	first := recs[0]
	mu.Unlock()
	first.fail(errors.New("source went away"))
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(recs) == 2
	})
	if !first.closed {
		t.Error("lost stream not closed")
	}

	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !recs[1].closed {
		t.Error("Stop didn't close the stream")
	}
}

func TestMatchSource(t *testing.T) {
	sources := []sinkInfo{
		{id: "alsa_input.usb-Behringer.analog-stereo", description: "UMC202HD Analog Stereo"},
		{id: "bluez_output.AA_BB_CC_DD_EE_FF.1.monitor", description: "Monitor of Speaker"},
		{id: "bluez_input.11_22_33_44_55_66.2", description: "Mike's iPhone"},
	}
	for spec, want := range map[string]int{
		"alsa_input.usb-Behringer.analog-stereo":   0,
		"umc202hd analog stereo":                   0,
		"11:22:33:44:55:66":                        2,
		"bluez_output.AA_BB_CC_DD_EE_FF.1.monitor": 1,
	} {
		got, err := matchSource(sources, spec)
		if err != nil || got != want {
			t.Errorf("%q: got %d, %v; want %d", spec, got, err, want)
		}
	}
	// A speaker's address must not record its monitor.
	if _, err := matchSource(sources, "AA:BB:CC:DD:EE:FF"); !errors.Is(err, ErrNoSource) {
		t.Errorf("speaker address: err = %v, want ErrNoSource", err)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestIsBluetoothInput(t *testing.T) {
	for id, want := range map[string]bool{
		"bluez_input.11_22_33_44_55_66.2":            true,
		"bluez_source.11_22_33_44_55_66.a2dp_source": true,
		"bluez_output.AA_BB_CC_DD_EE_FF.1.monitor":   false,
		"bluez_input.11_22_33_44_55_66.2.monitor":    false,
		"alsa_input.pci-0000_00_1f.3.analog-stereo":  false,
	} {
		if got := isBluetoothInput(id); got != want {
			t.Errorf("%s: %v", id, got)
		}
	}
}
