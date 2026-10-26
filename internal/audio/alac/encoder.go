package alac

import "fmt"

const elementEnd = 7

// Encoder produces uncompressed ALAC frames ("escape" frames). These are
// valid ALAC that every receiver must accept; skipping compression keeps the
// encoder simple and cheap at the cost of bandwidth, which is irrelevant on a
// LAN. This is what most open-source AirPlay senders do.
type Encoder struct {
	cfg Config
}

// NewEncoder returns an encoder for cfg. Only 16-bit audio is supported.
func NewEncoder(cfg Config) (*Encoder, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.BitDepth != 16 {
		return nil, fmt.Errorf("alac: encoder supports 16-bit audio, got %d-bit", cfg.BitDepth)
	}
	return &Encoder{cfg: cfg}, nil
}

// Config returns the stream configuration to advertise to receivers.
func (e *Encoder) Config() Config { return e.cfg }

// Encode encodes interleaved 16-bit samples as one frame. len(pcm) must be a
// multiple of the channel count and hold at most FrameLength samples per
// channel; shorter frames carry an explicit sample count.
func (e *Encoder) Encode(pcm []int16) ([]byte, error) {
	channels := int(e.cfg.NumChannels)
	if len(pcm)%channels != 0 {
		return nil, fmt.Errorf("alac: %d samples is not a multiple of %d channels", len(pcm), channels)
	}
	n := len(pcm) / channels
	if n == 0 || n > int(e.cfg.FrameLength) {
		return nil, fmt.Errorf("alac: frame of %d samples, want 1..%d", n, e.cfg.FrameLength)
	}
	hasSize := n != int(e.cfg.FrameLength)

	w := &bitWriter{buf: make([]byte, 0, 8+len(pcm)*2)}
	element := uint32(elementMono)
	if channels == 2 {
		element = elementStereo
	}
	w.write(element, 3)
	w.write(0, 4)  // element instance tag
	w.write(0, 12) // unused header bits
	if hasSize {
		w.write(1, 1)
	} else {
		w.write(0, 1)
	}
	w.write(0, 2) // uncompressed bytes
	w.write(1, 1) // not compressed
	if hasSize {
		w.write(uint32(n), 32)
	}
	for _, s := range pcm {
		w.write(uint32(uint16(s)), 16)
	}
	w.write(elementEnd, 3)
	return w.buf, nil
}
