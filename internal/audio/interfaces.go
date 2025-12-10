package audio

// AudioPacket represents a packet of audio data
type AudioPacket struct {
	Data       []byte
	SampleRate int
	Channels   int
	Format     Format
	Timestamp  int64 // Unix timestamp in nanoseconds
}

// Format represents the audio format
type Format string

const (
	FormatALAC Format = "alac" // Apple Lossless
	FormatPCM  Format = "pcm"  // Pulse Code Modulation
	FormatAAC  Format = "aac"  // Advanced Audio Coding
	FormatMP3  Format = "mp3"  // MPEG Audio Layer III
)

// AudioDecoder decodes audio packets from various formats
type AudioDecoder interface {
	// Decode decodes audio data into PCM samples
	Decode(data []byte) ([]float32, error)
	
	// SampleRate returns the sample rate in Hz
	SampleRate() int
	
	// Channels returns the number of audio channels
	Channels() int
	
	// Format returns the audio format being decoded
	Format() Format
	
	// Close closes the decoder and releases resources
	Close() error
}

// AudioEncoder encodes PCM samples into various formats
type AudioEncoder interface {
	// Encode encodes PCM samples into the target format
	Encode(samples []float32) ([]byte, error)
	
	// SampleRate returns the sample rate in Hz
	SampleRate() int
	
	// Channels returns the number of audio channels
	Channels() int
	
	// Format returns the audio format being encoded
	Format() Format
	
	// Close closes the encoder and releases resources
	Close() error
}

// AudioMixer mixes multiple audio streams
type AudioMixer interface {
	// AddSource adds an audio source to the mixer
	AddSource(id string, source <-chan []float32) error
	
	// RemoveSource removes an audio source from the mixer
	RemoveSource(id string) error
	
	// SetSourceVolume sets the volume for a specific source (0.0 to 1.0)
	SetSourceVolume(id string, volume float32) error
	
	// Output returns the mixed audio stream
	Output() <-chan []float32
	
	// Start starts the mixer
	Start() error
	
	// Stop stops the mixer
	Stop() error
	
	// Close closes the mixer and releases resources
	Close() error
}

// AudioOutput handles audio playback
type AudioOutput interface {
	// Play starts playing audio from the stream
	Play(stream <-chan []float32, sampleRate, channels int) error
	
	// Stop stops playback
	Stop() error
	
	// SetVolume sets the output volume (0.0 to 1.0)
	SetVolume(volume float32) error
	
	// GetVolume returns the current volume
	GetVolume() float32
	
	// IsPlaying returns whether audio is currently playing
	IsPlaying() bool
	
	// Close closes the output and releases resources
	Close() error
}

// SampleRateConverter converts audio between different sample rates
type SampleRateConverter interface {
	// Convert converts audio samples from one sample rate to another
	Convert(samples []float32, fromRate, toRate int) ([]float32, error)
	
	// Close closes the converter and releases resources
	Close() error
}

// ChannelConverterFunc is a function type for channel conversion
type ChannelConverterFunc func(samples []float32, fromChannels, toChannels int) ([]float32, error)

