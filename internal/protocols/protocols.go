// Package protocols defines the interfaces protocol adapters implement and
// the Manager that connects running adapters to the router.
//
// A Server is an audio input: a receiver other devices play to, such as an
// AirPlay receiver. A Client is an audio output: a speaker Air-Mixer plays
// to. Adapters live in subpackages, one per protocol.
package protocols

import (
	"context"
	"errors"

	"github.com/yeixio/air-mixer/internal/audio"
)

// Packet is a buffer of interleaved float32 samples in [-1, 1].
type Packet struct {
	Samples []float32
	Format  audio.Format
}

// Server is an audio input.
type Server interface {
	ID() string
	Name() string
	Protocol() string
	// Start begins accepting connections. ctx bounds startup only.
	Start(ctx context.Context) error
	// Stop releases all resources. The server may not be restarted.
	Stop() error
	// Audio delivers decoded audio. It is never closed while the server is
	// registered; consumers stop reading when they unregister it.
	Audio() <-chan Packet
}

// Client is an audio output.
type Client interface {
	ID() string
	Name() string
	Protocol() string
	// Connect opens the output. ctx bounds the connection attempt.
	Connect(ctx context.Context) error
	// Disconnect closes the output. It is safe to call more than once.
	Disconnect() error
	// Send queues audio for playback without blocking. When the output is
	// behind, it drops audio and returns ErrBufferFull.
	Send(Packet) error
	Connected() bool
}

var (
	// ErrBufferFull is returned by Client.Send when audio was dropped.
	ErrBufferFull = errors.New("output buffer full")
	// ErrNotConnected is returned by Client.Send before Connect.
	ErrNotConnected = errors.New("not connected")
	// ErrUnsupportedDevice is wrapped by Connect errors that retrying will
	// not fix, such as a device that needs a protocol version Air-Mixer
	// does not speak.
	ErrUnsupportedDevice = errors.New("unsupported device")
)
