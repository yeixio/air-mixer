package audio

import (
	"fmt"

	"github.com/gopherstein/air-mixer/internal/audio/alac"
)

// PureALACDecoder is a pure Go ALAC decoder implementation
type PureALACDecoder struct {
	decoder    *alac.Decoder
	sampleRate int
	channels   int
	bitDepth   int
}

// NewPureALACDecoder creates a new pure Go ALAC decoder
// magicCookie is the ALAC magic cookie data extracted from the fmtp header (24 bytes)
// or can be parsed from fmtp array
func NewPureALACDecoder(magicCookie []byte, sampleRate, channels, bitDepth int) (*PureALACDecoder, error) {
	// Parse magic cookie
	cookie, err := alac.ParseMagicCookie(magicCookie)
	if err != nil {
		return nil, fmt.Errorf("failed to parse magic cookie: %w", err)
	}

	// Override with provided values if they differ (for compatibility)
	if sampleRate > 0 {
		cookie.SampleRate = uint32(sampleRate)
	}
	if channels > 0 {
		cookie.NumChannels = uint8(channels)
	}
	if bitDepth > 0 {
		cookie.BitDepth = uint8(bitDepth)
	}

	// Create decoder
	decoder := alac.NewDecoder(cookie)

	return &PureALACDecoder{
		decoder:    decoder,
		sampleRate: int(cookie.SampleRate),
		channels:   int(cookie.NumChannels),
		bitDepth:   int(cookie.BitDepth),
	}, nil
}

// NewPureALACDecoderFromFmtp creates a new pure Go ALAC decoder from fmtp array
// This is convenient for AirPlay integration where fmtp comes from RTSP
func NewPureALACDecoderFromFmtp(fmtp []int) (*PureALACDecoder, error) {
	// Parse magic cookie from fmtp
	cookie, err := alac.ParseMagicCookieFromFmtp(fmtp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse magic cookie from fmtp: %w", err)
	}

	// Create decoder
	decoder := alac.NewDecoder(cookie)

	return &PureALACDecoder{
		decoder:    decoder,
		sampleRate: int(cookie.SampleRate),
		channels:   int(cookie.NumChannels),
		bitDepth:   int(cookie.BitDepth),
	}, nil
}

// Decode decodes ALAC data into PCM float32 samples
func (d *PureALACDecoder) Decode(data []byte) ([]float32, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty data")
	}

	// Decode frame
	samples, err := d.decoder.DecodeFrameToFloat32(data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode ALAC frame: %w", err)
	}

	return samples, nil
}

// SampleRate returns the sample rate in Hz
func (d *PureALACDecoder) SampleRate() int {
	return d.sampleRate
}

// Channels returns the number of audio channels
func (d *PureALACDecoder) Channels() int {
	return d.channels
}

// Format returns the audio format being decoded
func (d *PureALACDecoder) Format() Format {
	return FormatALAC
}

// Close closes the decoder and releases resources
func (d *PureALACDecoder) Close() error {
	// Pure Go implementation - no cleanup needed
	return nil
}

