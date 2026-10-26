package audio

import (
	"errors"
	"sync"
)

// ErrInputExists is returned when adding an input whose ID is already in use.
var ErrInputExists = errors.New("audio: mixer input already exists")

// ErrInputNotFound is returned when an input ID is unknown.
var ErrInputNotFound = errors.New("audio: mixer input not found")

// ErrInvalidVolume is returned for volumes outside [0, 1].
var ErrInvalidVolume = errors.New("audio: volume must be between 0 and 1")

// Mixer sums any number of input streams into one output stream.
//
// Inputs rarely deliver buffers in lockstep, so the mixer queues samples per
// input and emits as many samples as every input has ready. If an input falls
// more than maxLag samples behind the others it is treated as silent for the
// gap, so one stalled source never blocks the rest. With a single input the
// mixer passes buffers through unchanged apart from volume.
//
// All inputs must share one sample format, and every buffer must hold whole
// frames.
type Mixer struct {
	maxLag int
	out    chan []float32
	events chan mixerEvent
	done   chan struct{}
	exited chan struct{}

	mu     sync.Mutex
	inputs map[string]*mixerInput
	closed bool
}

type mixerInput struct {
	volume  float32
	pending []float32
	stop    chan struct{}
}

type mixerEvent struct {
	id      string
	in      *mixerInput
	samples []float32
	eof     bool
}

// NewMixer starts a mixer. maxLag is in samples (not frames); a quarter
// second of internal audio is a reasonable value.
func NewMixer(maxLag int) *Mixer {
	m := &Mixer{
		maxLag: maxLag,
		out:    make(chan []float32, 16),
		events: make(chan mixerEvent),
		done:   make(chan struct{}),
		exited: make(chan struct{}),
		inputs: make(map[string]*mixerInput),
	}
	go m.loop()
	return m
}

// Output returns the mixed stream. It is closed after Close.
func (m *Mixer) Output() <-chan []float32 { return m.out }

// AddInput starts mixing samples from ch at the given volume. The input is
// removed automatically when ch is closed.
func (m *Mixer) AddInput(id string, ch <-chan []float32, volume float32) error {
	if volume < 0 || volume > 1 {
		return ErrInvalidVolume
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("audio: mixer is closed")
	}
	if _, ok := m.inputs[id]; ok {
		return ErrInputExists
	}
	in := &mixerInput{volume: volume, stop: make(chan struct{})}
	m.inputs[id] = in
	go m.read(id, in, ch)
	return nil
}

// RemoveInput stops mixing an input and discards its queued samples.
func (m *Mixer) RemoveInput(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	in, ok := m.inputs[id]
	if !ok {
		return ErrInputNotFound
	}
	delete(m.inputs, id)
	close(in.stop)
	return nil
}

// SetVolume changes an input's volume.
func (m *Mixer) SetVolume(id string, volume float32) error {
	if volume < 0 || volume > 1 {
		return ErrInvalidVolume
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	in, ok := m.inputs[id]
	if !ok {
		return ErrInputNotFound
	}
	in.volume = volume
	return nil
}

// Len returns the number of inputs.
func (m *Mixer) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.inputs)
}

// Close stops the mixer and closes the output channel. It is safe to call
// more than once.
func (m *Mixer) Close() {
	m.mu.Lock()
	if !m.closed {
		m.closed = true
		close(m.done)
	}
	m.mu.Unlock()
	<-m.exited
}

func (m *Mixer) read(id string, in *mixerInput, ch <-chan []float32) {
	for {
		var ev mixerEvent
		select {
		case <-in.stop:
			return
		case <-m.done:
			return
		case s, ok := <-ch:
			ev = mixerEvent{id: id, in: in, samples: s, eof: !ok}
		}
		select {
		case m.events <- ev:
		case <-in.stop:
			return
		case <-m.done:
			return
		}
		if ev.eof {
			return
		}
	}
}

func (m *Mixer) loop() {
	defer close(m.exited)
	defer close(m.out)
	for {
		select {
		case <-m.done:
			return
		case ev := <-m.events:
			buf := m.handle(ev)
			if buf == nil {
				continue
			}
			select {
			case m.out <- buf:
			case <-m.done:
				return
			}
		}
	}
}

// handle queues an event's samples and returns the next mixed buffer, if
// one is ready.
func (m *Mixer) handle(ev mixerEvent) []float32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.inputs[ev.id] != ev.in {
		return nil // removed or replaced since the event was read
	}
	if ev.eof {
		delete(m.inputs, ev.id)
	} else {
		ev.in.pending = append(ev.in.pending, ev.samples...)
	}
	if len(m.inputs) == 0 {
		return nil
	}

	ready, most := -1, 0
	for _, in := range m.inputs {
		if ready < 0 || len(in.pending) < ready {
			ready = len(in.pending)
		}
		most = max(most, len(in.pending))
	}
	n := ready
	if n == 0 {
		if most < m.maxLag {
			return nil
		}
		n = most // a lagging input is silent for this stretch
	}

	out := make([]float32, n)
	for _, in := range m.inputs {
		k := min(n, len(in.pending))
		for i, s := range in.pending[:k] {
			out[i] += s * in.volume
		}
		in.pending = append(in.pending[:0], in.pending[k:]...)
	}
	if len(m.inputs) > 1 {
		for i, s := range out {
			out[i] = Clamp(s)
		}
	}
	return out
}
