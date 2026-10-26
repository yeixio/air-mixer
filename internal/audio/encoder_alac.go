package audio

import (
	"fmt"

	"github.com/yeixio/air-mixer/internal/audio/alac"
)

// ALACEncoder encodes PCM samples into ALAC format
type ALACEncoder struct {
	encoder    *alac.Encoder
	magicCookie *alac.MagicCookie
	sampleRate int
	channels   int
	bitDepth   int
	frameSize  int // Samples per frame
}

// NewALACEncoder creates a new ALAC encoder
func NewALACEncoder(sampleRate, channels, bitDepth int) (*ALACEncoder, error) {
	// Create default magic cookie for encoding
	// Frame length: typically 4096 samples for 44.1kHz
	frameLength := 4096
	if sampleRate != 44100 {
		// Adjust frame length proportionally
		frameLength = (4096 * sampleRate) / 44100
	}
	
	magicCookie := &alac.MagicCookie{
		FrameLength:       uint32(frameLength),
		CompatibleVersion: 0,
		BitDepth:         uint8(bitDepth),
		PB:               40,  // Default predictor parameter
		MB:               10,  // Default multiplier parameter
		KB:               14,  // Default K parameter for Rice coding
		NumChannels:      uint8(channels),
		MaxRun:           16,
		MaxFrameBytes:    uint32(frameLength * channels * (bitDepth / 8) * 2), // Estimate
		AvgBitRate:       uint32(sampleRate * channels * bitDepth),
		SampleRate:       uint32(sampleRate),
	}
	
	encoder := alac.NewEncoder(magicCookie)
	
	return &ALACEncoder{
		encoder:     encoder,
		magicCookie: magicCookie,
		sampleRate:  sampleRate,
		channels:    channels,
		bitDepth:   bitDepth,
		frameSize:  frameLength * channels,
	}, nil
}

// Encode encodes PCM float32 samples into ALAC format
func (e *ALACEncoder) Encode(samples []float32) ([]byte, error) {
	if len(samples) == 0 {
		return nil, fmt.Errorf("empty samples")
	}
	
	// Encode frame
	return e.encoder.EncodeFrameFromFloat32(samples)
}

// SampleRate returns the sample rate
func (e *ALACEncoder) SampleRate() int {
	return e.sampleRate
}

// Channels returns the number of channels
func (e *ALACEncoder) Channels() int {
	return e.channels
}

// Format returns the audio format
func (e *ALACEncoder) Format() Format {
	return FormatALAC
}

// Close closes the encoder
func (e *ALACEncoder) Close() error {
	return nil
}

// MagicCookie returns the ALAC magic cookie for use in RTSP
func (e *ALACEncoder) MagicCookie() *alac.MagicCookie {
	return e.magicCookie
}

// FrameSize returns the number of samples per frame
func (e *ALACEncoder) FrameSize() int {
	return e.frameSize
}

