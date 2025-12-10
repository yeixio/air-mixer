package protocols

import (
	"github.com/gopherstein/air-mixer/internal/audio"
)

// AudioPacket represents a packet of audio data from a protocol
type AudioPacket struct {
	Data       []byte
	SampleRate int
	Channels   int
	Format     audio.Format
	Timestamp  int64 // Unix timestamp in nanoseconds
}

// ProtocolServer represents an input audio server (receives audio)
type ProtocolServer interface {
	// Start starts the server
	Start() error
	
	// Stop stops the server
	Stop() error
	
	// AudioStream returns the audio stream channel
	AudioStream() <-chan AudioPacket
	
	// Name returns the server name
	Name() string
	
	// ID returns the server ID
	ID() string
	
	// Protocol returns the protocol name (e.g., "airplay", "bluetooth")
	Protocol() string
	
	// IsRunning returns whether the server is running
	IsRunning() bool
}

// ProtocolClient represents an output audio client (sends audio)
type ProtocolClient interface {
	// Connect connects to the target
	Connect(target string) error
	
	// Disconnect disconnects from the target
	Disconnect() error
	
	// SendAudio sends audio data to the target
	SendAudio(packet AudioPacket) error
	
	// Name returns the client name
	Name() string
	
	// ID returns the client ID
	ID() string
	
	// Protocol returns the protocol name
	Protocol() string
	
	// IsConnected returns whether the client is connected
	IsConnected() bool
}

