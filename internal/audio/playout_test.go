package audio

import (
	"testing"
	"time"
)

func TestPlayout(t *testing.T) {
	p := NewPlayout(4, 0)
	dst := make([]float32, 3)
	if n := p.Fill(dst); n != 0 || dst[0] != 0 {
		t.Fatalf("empty Fill = %d %v", n, dst)
	}
	if !p.Push([]float32{1, 2}) || !p.Push([]float32{3, 4}) {
		t.Fatal("Push within the limit refused")
	}
	if p.Push([]float32{5}) {
		t.Error("Push past the limit accepted")
	}
	if n := p.Fill(dst); n != 3 || dst[2] != 3 {
		t.Errorf("Fill = %d %v", n, dst)
	}
	if n := p.Fill(dst); n != 1 || dst[0] != 4 || dst[1] != 0 {
		t.Errorf("Fill = %d %v, want the remainder then silence", n, dst)
	}
	p.Push([]float32{1, 1})
	p.Reset()
	if p.Len() != 0 {
		t.Error("Reset left audio")
	}
}

func TestPlayoutDelay(t *testing.T) {
	p := NewPlayout(4, 3) // rounded down to whole frames: 2 samples
	if !p.Push([]float32{1, 1, 1, 1}) {
		t.Fatal("Push refused")
	}
	if p.Len() != 6 {
		t.Fatalf("Len = %d, want the delay plus the audio", p.Len())
	}
	dst := make([]float32, 6)
	p.Fill(dst)
	if dst[0] != 0 || dst[1] != 0 || dst[2] != 1 {
		t.Errorf("Fill = %v, want silence first", dst)
	}

	// Playing on without a gap adds no more delay.
	p.Push([]float32{2, 2})
	if p.Len() != 2 {
		t.Errorf("Len while playing = %d, want no extra delay", p.Len())
	}
	// After running dry, the delay applies again.
	p.Fill(make([]float32, 4))
	p.Push([]float32{3, 3})
	if p.Len() != 4 {
		t.Errorf("Len after a gap = %d, want the delay again", p.Len())
	}

	// A Push that does not fit does not leave the delay behind.
	q := NewPlayout(2, 2)
	if q.Push([]float32{1, 1, 1}) || q.Len() != 0 {
		t.Errorf("oversized Push: Len = %d", q.Len())
	}
}

func TestSamples(t *testing.T) {
	if got := Samples(time.Second); got != SampleRate*Channels {
		t.Errorf("Samples(1s) = %d", got)
	}
	if got := Samples(0); got != 0 {
		t.Errorf("Samples(0) = %d", got)
	}
}
