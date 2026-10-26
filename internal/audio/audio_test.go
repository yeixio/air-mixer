package audio

import (
	"errors"
	"math"
	"testing"
	"time"
)

func recv(t *testing.T, ch <-chan []float32) []float32 {
	t.Helper()
	select {
	case b, ok := <-ch:
		if !ok {
			t.Fatal("channel closed")
		}
		return b
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for samples")
	}
	return nil
}

func expectNothing(t *testing.T, ch <-chan []float32) {
	t.Helper()
	select {
	case b := <-ch:
		t.Fatalf("unexpected buffer %v", b)
	case <-time.After(50 * time.Millisecond):
	}
}

func equal(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if math.Abs(float64(a[i]-b[i])) > 1e-6 {
			return false
		}
	}
	return true
}

func TestMixerSingleInputPassesThrough(t *testing.T) {
	m := NewMixer(1000)
	defer m.Close()
	in := make(chan []float32, 1)
	if err := m.AddInput("a", in, 0.5); err != nil {
		t.Fatal(err)
	}
	in <- []float32{0.2, -0.4, 1, -1}
	if got, want := recv(t, m.Output()), []float32{0.1, -0.2, 0.5, -0.5}; !equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestMixerSumsAlignedInputs(t *testing.T) {
	m := NewMixer(1000)
	defer m.Close()
	a, b := make(chan []float32, 1), make(chan []float32, 1)
	m.AddInput("a", a, 1)
	m.AddInput("b", b, 0.5)

	a <- []float32{0.1, 0.2, 0.3, 0.4}
	expectNothing(t, m.Output()) // waits for b

	b <- []float32{0.2, 0.2}
	if got, want := recv(t, m.Output()), []float32{0.2, 0.3}; !equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	b <- []float32{0.4, 0.4}
	if got, want := recv(t, m.Output()), []float32{0.5, 0.6}; !equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestMixerClipsSum(t *testing.T) {
	m := NewMixer(1000)
	defer m.Close()
	a, b := make(chan []float32, 1), make(chan []float32, 1)
	m.AddInput("a", a, 1)
	m.AddInput("b", b, 1)
	a <- []float32{0.8, -0.8}
	b <- []float32{0.8, -0.8}
	if got, want := recv(t, m.Output()), []float32{1, -1}; !equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestMixerDoesNotWaitForStalledInput(t *testing.T) {
	m := NewMixer(4)
	defer m.Close()
	a := make(chan []float32, 2)
	m.AddInput("a", a, 1)
	m.AddInput("stalled", make(chan []float32), 1)

	a <- []float32{0.1, 0.1}
	expectNothing(t, m.Output())
	a <- []float32{0.2, 0.2}
	if got, want := recv(t, m.Output()), []float32{0.1, 0.1, 0.2, 0.2}; !equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestMixerRemoveAndEOF(t *testing.T) {
	m := NewMixer(1000)
	defer m.Close()
	a, b := make(chan []float32, 1), make(chan []float32, 1)
	m.AddInput("a", a, 1)
	m.AddInput("b", b, 1)

	if err := m.AddInput("a", a, 1); !errors.Is(err, ErrInputExists) {
		t.Errorf("duplicate AddInput error = %v", err)
	}
	if err := m.RemoveInput("b"); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveInput("b"); !errors.Is(err, ErrInputNotFound) {
		t.Errorf("second RemoveInput error = %v", err)
	}
	a <- []float32{0.3, 0.3}
	if got := recv(t, m.Output()); !equal(got, []float32{0.3, 0.3}) {
		t.Errorf("got %v after removing b", got)
	}

	close(a)
	deadline := time.Now().Add(2 * time.Second)
	for m.Len() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("input not removed after its channel closed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestMixerVolume(t *testing.T) {
	m := NewMixer(1000)
	defer m.Close()
	a := make(chan []float32, 1)
	if err := m.AddInput("a", a, 2); !errors.Is(err, ErrInvalidVolume) {
		t.Errorf("AddInput volume 2 error = %v", err)
	}
	m.AddInput("a", a, 1)
	if err := m.SetVolume("a", 0.25); err != nil {
		t.Fatal(err)
	}
	if err := m.SetVolume("a", -1); !errors.Is(err, ErrInvalidVolume) {
		t.Errorf("SetVolume -1 error = %v", err)
	}
	if err := m.SetVolume("missing", 1); !errors.Is(err, ErrInputNotFound) {
		t.Errorf("SetVolume missing error = %v", err)
	}
	a <- []float32{1, 1}
	if got := recv(t, m.Output()); !equal(got, []float32{0.25, 0.25}) {
		t.Errorf("got %v", got)
	}
}

func TestMixerCloseClosesOutput(t *testing.T) {
	m := NewMixer(1000)
	m.AddInput("a", make(chan []float32), 1)
	m.Close()
	m.Close()
	if _, ok := <-m.Output(); ok {
		t.Error("output still open after Close")
	}
	if err := m.AddInput("b", make(chan []float32), 1); err == nil {
		t.Error("AddInput succeeded after Close")
	}
}

func TestGainDoesNotModifyInput(t *testing.T) {
	in := []float32{0.5, -0.5}
	out := Gain(in, 0.5)
	if !equal(in, []float32{0.5, -0.5}) || !equal(out, []float32{0.25, -0.25}) {
		t.Errorf("in=%v out=%v", in, out)
	}
}

func TestSampleConversions(t *testing.T) {
	for _, tc := range []struct {
		in   float32
		want int16
	}{{0, 0}, {0.5, 16384}, {-1, -32768}, {1, 32767}, {2, 32767}, {-2, -32768}} {
		if got := FloatToInt16(tc.in); got != tc.want {
			t.Errorf("FloatToInt16(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}
	if got := Int16ToFloat(-32768); got != -1 {
		t.Errorf("Int16ToFloat(-32768) = %v", got)
	}
	if Clamp(1.5) != 1 || Clamp(-1.5) != -1 || Clamp(0.25) != 0.25 {
		t.Error("Clamp")
	}
}

func TestRemixChannels(t *testing.T) {
	cases := []struct {
		in       []float32
		from, to int
		want     []float32
	}{
		{[]float32{0.1, 0.2}, 1, 2, []float32{0.1, 0.1, 0.2, 0.2}},
		{[]float32{0.2, 0.4, -0.2, 0}, 2, 1, []float32{0.3, -0.1}},
		{[]float32{0.1, 0.2}, 2, 2, []float32{0.1, 0.2}},
		{[]float32{1, 2, 3, 4, 5, 6}, 3, 2, []float32{1, 2, 4, 5}},
		{[]float32{1, 2}, 2, 3, []float32{1, 2, 0}},
	}
	for _, tc := range cases {
		got, err := RemixChannels(tc.in, tc.from, tc.to)
		if err != nil || !equal(got, tc.want) {
			t.Errorf("RemixChannels(%v, %d, %d) = %v, %v; want %v", tc.in, tc.from, tc.to, got, err, tc.want)
		}
	}
	if _, err := RemixChannels([]float32{1, 2, 3}, 2, 1); err == nil {
		t.Error("expected misaligned input to fail")
	}
	if _, err := RemixChannels(nil, 0, 1); err == nil {
		t.Error("expected zero channels to fail")
	}
}

func TestResamplerUpsampleIsContinuousAcrossChunks(t *testing.T) {
	r, err := NewResampler(1, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	var got []float32
	for _, chunk := range [][]float32{{0, 1}, {2, 3}, {4}} {
		out, err := r.Process(chunk)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, out...)
	}
	// A ramp stays a ramp, including across chunk boundaries.
	want := []float32{0, 0.5, 1, 1.5, 2, 2.5, 3, 3.5}
	if !equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestResamplerDownsampleStereo(t *testing.T) {
	r, _ := NewResampler(4, 2, 2)
	out, err := r.Process([]float32{0, 10, 1, 11, 2, 12, 3, 13, 4, 14})
	if err != nil {
		t.Fatal(err)
	}
	if want := []float32{0, 10, 2, 12}; !equal(out, want) {
		t.Errorf("got %v, want %v", out, want)
	}
	if _, err := r.Process([]float32{1}); err == nil {
		t.Error("expected misaligned input to fail")
	}
	if _, err := NewResampler(0, 1, 1); err == nil {
		t.Error("expected invalid rate to fail")
	}
}

func TestResamplerLength(t *testing.T) {
	r, _ := NewResampler(48000, 44100, 2)
	total := 0
	for range 100 {
		out, _ := r.Process(make([]float32, 2*480))
		total += len(out) / 2
	}
	// 48000 input frames should become about 44100 output frames.
	if total < 44090 || total > 44110 {
		t.Errorf("resampled 48000 frames to %d, want about 44100", total)
	}
}

func TestConverter(t *testing.T) {
	c, err := NewConverter(Format{48000, 1}, Internal)
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.Process(make([]float32, 480))
	if err != nil {
		t.Fatal(err)
	}
	if len(out)%2 != 0 || len(out) == 0 {
		t.Errorf("converted to %d samples", len(out))
	}
	same, _ := NewConverter(Internal, Internal)
	in := []float32{0.1, 0.2}
	if out, _ := same.Process(in); !equal(out, in) {
		t.Errorf("identity conversion changed samples: %v", out)
	}
	if _, err := NewConverter(Format{}, Internal); err == nil {
		t.Error("expected invalid format to fail")
	}
}
