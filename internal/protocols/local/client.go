package local

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/yeixio/air-mixer/internal/audio"
	"github.com/yeixio/air-mixer/internal/protocols"
)

// Client implements ProtocolClient for local audio output (using Oto)
type Client struct {
	id          string
	name        string
	connected   bool
	output      *audio.OtoOutput
	sampleRate  int
	channels    int
	mu          sync.RWMutex
	audioStream chan []float32
}

// NewClient creates a new local audio output client
func NewClient(id, name string, sampleRate, channels int) (*Client, error) {
	output, err := audio.NewOtoOutput(sampleRate, channels)
	if err != nil {
		return nil, fmt.Errorf("failed to create audio output: %w", err)
	}

	client := &Client{
		id:          id,
		name:        name,
		sampleRate:  sampleRate,
		channels:    channels,
		output:      output,
		audioStream: make(chan []float32, 100),
	}

	return client, nil
}

// Connect connects the client (starts audio playback)
func (c *Client) Connect(target string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return nil
	}

	if err := c.output.Play(c.audioStream, c.sampleRate, c.channels); err != nil {
		return fmt.Errorf("failed to start playback: %w", err)
	}

	c.connected = true
	return nil
}

// Disconnect disconnects the client (stops audio playback)
func (c *Client) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected {
		return nil
	}

	c.output.Stop()
	c.connected = false
	return nil
}

// SendAudio sends audio data to the client
func (c *Client) SendAudio(packet protocols.AudioPacket) error {
	c.mu.RLock()
	connected := c.connected
	c.mu.RUnlock()

	if !connected {
		return fmt.Errorf("client not connected")
	}

	// Convert packet data back to float32 samples
	if len(packet.Data)%4 != 0 {
		return fmt.Errorf("invalid packet size: %d bytes (not multiple of 4)", len(packet.Data))
	}

	samples := make([]float32, len(packet.Data)/4)
	for i := 0; i < len(samples); i++ {
		bits := uint32(packet.Data[i*4]) |
			uint32(packet.Data[i*4+1])<<8 |
			uint32(packet.Data[i*4+2])<<16 |
			uint32(packet.Data[i*4+3])<<24
		samples[i] = *(*float32)(unsafe.Pointer(&bits))
	}

	// Send to audio stream
	select {
	case c.audioStream <- samples:
		return nil
	default:
		// Buffer full, drop packet
		return fmt.Errorf("audio stream buffer full")
	}
}

// Name returns the client name
func (c *Client) Name() string {
	return c.name
}

// ID returns the client ID
func (c *Client) ID() string {
	return c.id
}

// Protocol returns the protocol name
func (c *Client) Protocol() string {
	return "local"
}

// IsConnected returns whether the client is connected
func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// Close closes the client and releases resources
func (c *Client) Close() error {
	c.Disconnect()
	if c.output != nil {
		return c.output.Close()
	}
	return nil
}

