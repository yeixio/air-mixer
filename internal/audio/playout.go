package audio

import "sync"

// Playout is the queue between the router and an output device that pulls
// audio at its own pace. Push adds audio without blocking; Fill hands it
// to the device and pads with silence when the queue runs dry.
//
// A Playout can hold audio back by a fixed delay, so outputs with
// different latencies, such as several Bluetooth speakers in one room, can
// be lined up. The delay is applied as silence each time playback starts
// from an empty queue, so it holds after every gap in the audio.
type Playout struct {
	mu      sync.Mutex
	queue   []float32
	limit   int // samples Push may queue beyond the delay
	delay   int // samples of silence inserted when playback starts
	playing bool
}

// NewPlayout returns a Playout that queues at most limit samples on top of
// delay samples of silence.
func NewPlayout(limit, delay int) *Playout {
	delay -= delay % Channels // keep channels interleaved correctly
	return &Playout{limit: limit, delay: max(0, delay)}
}

// Push queues samples. It reports false, and queues nothing, when they do
// not fit; the caller drops them.
func (p *Playout) Push(samples []float32) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	lead := 0
	if !p.playing && len(p.queue) == 0 {
		lead = p.delay
	}
	if lead+len(p.queue)+len(samples) > p.limit+p.delay {
		return false
	}
	if lead > 0 {
		p.queue = make([]float32, lead, lead+len(samples))
	}
	p.queue = append(p.queue, samples...)
	p.playing = true
	return true
}

// Fill copies queued audio into dst and pads the rest with silence. It
// returns how many samples were real audio.
func (p *Playout) Fill(dst []float32) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := copy(dst, p.queue)
	p.queue = p.queue[n:]
	if len(p.queue) == 0 {
		p.queue = nil // release the backing array after a long queue
		if n < len(dst) {
			p.playing = false // ran dry: the next audio starts after the delay again
		}
	}
	clear(dst[n:])
	return n
}

// Reset empties the queue.
func (p *Playout) Reset() {
	p.mu.Lock()
	p.queue, p.playing = nil, false
	p.mu.Unlock()
}

// Len returns the number of queued samples, including any delay.
func (p *Playout) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.queue)
}
