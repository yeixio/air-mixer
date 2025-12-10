package audio

import (
	"context"
	"fmt"
	"io"
	"sync"
	"unsafe"

	"github.com/ebitengine/oto/v3"
)

// OtoOutput implements AudioOutput using the Oto library
type OtoOutput struct {
	ctx        context.Context
	cancel     context.CancelFunc
	player     *oto.Player
	mu         sync.RWMutex
	volume     float32
	isPlaying  bool
	sampleRate int
	channels   int
	stream     chan []float32
}

// NewOtoOutput creates a new Oto-based audio output
func NewOtoOutput(sampleRate, channels int) (*OtoOutput, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Create Oto context
	op := &oto.NewContextOptions{
		SampleRate:   sampleRate,
		ChannelCount: channels,
		Format:       oto.FormatFloat32LE,
	}

	c, ready, err := oto.NewContext(op)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create oto context: %w", err)
	}

	// Wait for context to be ready
	<-ready

	output := &OtoOutput{
		ctx:        ctx,
		cancel:     cancel,
		volume:     1.0,
		sampleRate: sampleRate,
		channels:   channels,
		stream:     make(chan []float32, 100), // Buffer some samples
	}

	// Create player with a reader
	reader := &audioReader{output: output}
	player := c.NewPlayer(reader)
	output.player = player

	return output, nil
}

// audioReader implements io.Reader for Oto
type audioReader struct {
	output *OtoOutput
	buffer []byte
	pos    int
}

func (r *audioReader) Read(p []byte) (n int, err error) {
	output := r.output

	// If we have leftover data, use it first
	if r.pos < len(r.buffer) {
		n = copy(p, r.buffer[r.pos:])
		r.pos += n
		if r.pos >= len(r.buffer) {
			r.buffer = r.buffer[:0]
			r.pos = 0
		}
		return n, nil
	}

	// Read from stream
	select {
	case <-output.ctx.Done():
		return 0, io.EOF
	case samples, ok := <-output.stream:
		if !ok {
			return 0, io.EOF
		}

		// Apply volume
		output.mu.RLock()
		vol := output.volume
		output.mu.RUnlock()

		if vol != 1.0 {
			for i := range samples {
				samples[i] *= vol
			}
		}

		// Convert float32 samples to bytes
		data := float32ToBytes(samples)

		// Copy to output buffer
		n = copy(p, data)
		if n < len(data) {
			// Store remainder for next read
			r.buffer = data[n:]
			r.pos = 0
		}

		return n, nil
	}
}

// Play starts playing audio from the stream
func (o *OtoOutput) Play(stream <-chan []float32, sampleRate, channels int) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.isPlaying {
		return ErrAlreadyPlaying
	}

	// Check if sample rate or channels match
	if sampleRate != o.sampleRate {
		return fmt.Errorf("sample rate mismatch: expected %d, got %d", o.sampleRate, sampleRate)
	}
	if channels != o.channels {
		return fmt.Errorf("channel count mismatch: expected %d, got %d", o.channels, channels)
	}

	o.isPlaying = true

	// Start goroutine to forward samples from stream to internal stream
	go o.forwardSamples(stream)

	// Start player
	o.player.Play()

	return nil
}

// forwardSamples forwards samples from the input stream to the internal stream
func (o *OtoOutput) forwardSamples(stream <-chan []float32) {
	defer close(o.stream)

	for {
		select {
		case <-o.ctx.Done():
			return
		case samples, ok := <-stream:
			if !ok {
				return
			}

			select {
			case o.stream <- samples:
			case <-o.ctx.Done():
				return
			}
		}
	}
}

// Stop stops playback
func (o *OtoOutput) Stop() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if !o.isPlaying {
		return nil
	}

	o.cancel()
	o.player.Pause()
	o.isPlaying = false

	return nil
}

// SetVolume sets the output volume (0.0 to 1.0)
func (o *OtoOutput) SetVolume(volume float32) error {
	if volume < 0.0 || volume > 1.0 {
		return ErrInvalidVolume
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	o.volume = volume
	return nil
}

// GetVolume returns the current volume
func (o *OtoOutput) GetVolume() float32 {
	o.mu.RLock()
	defer o.mu.RUnlock()

	return o.volume
}

// IsPlaying returns whether audio is currently playing
func (o *OtoOutput) IsPlaying() bool {
	o.mu.RLock()
	defer o.mu.RUnlock()

	return o.isPlaying
}

// Close closes the output and releases resources
func (o *OtoOutput) Close() error {
	if err := o.Stop(); err != nil {
		return err
	}

	if o.player != nil {
		if err := o.player.Close(); err != nil {
			return err
		}
	}

	return nil
}

// float32ToBytes converts float32 samples to byte slice
// Oto expects little-endian float32 format
func float32ToBytes(samples []float32) []byte {
	data := make([]byte, len(samples)*4)
	for i, sample := range samples {
		// Convert float32 to bytes (little-endian)
		bits := uint32(*(*uint32)(unsafe.Pointer(&sample)))
		data[i*4] = byte(bits)
		data[i*4+1] = byte(bits >> 8)
		data[i*4+2] = byte(bits >> 16)
		data[i*4+3] = byte(bits >> 24)
	}
	return data
}

var (
	ErrAlreadyPlaying = &Error{Message: "audio is already playing"}
)
