package audio

import (
	"fmt"
	"io"

	"github.com/carterpeel/alac"
)

// ALACDecoder decodes ALAC (Apple Lossless Audio Codec) audio data
type ALACDecoder struct {
	decoder    *alac.File
	sampleRate int
	channels   int
	bitDepth   int
}

// NewALACDecoder creates a new ALAC decoder
// magicCookie is the ALAC magic cookie data extracted from the fmtp header (24 bytes)
func NewALACDecoder(magicCookie []byte, sampleRate, channels, bitDepth int) (*ALACDecoder, error) {
	if len(magicCookie) < 24 {
		return nil, fmt.Errorf("magic cookie too short: need at least 24 bytes, got %d", len(magicCookie))
	}

	// Create ALAC decoder
	decoder := alac.New(bitDepth, channels)

	// Set the magic cookie (ALAC decoder needs this for initialization)
	decoder.SetInfo(magicCookie)
	
	// Allocate buffers
	decoder.AllocateBuffers()

	return &ALACDecoder{
		decoder:    &decoder,
		sampleRate: sampleRate,
		channels:   channels,
		bitDepth:   bitDepth,
	}, nil
}

// Decode decodes ALAC data into PCM float32 samples
func (d *ALACDecoder) Decode(data []byte) ([]float32, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty data")
	}

	// Decode ALAC frame to PCM bytes
	pcmBytes := d.decoder.DecodeFrame(data)
	
	// Convert PCM bytes to float32 samples
	// The decoder returns interleaved PCM samples
	bytesPerSample := d.bitDepth / 8
	if len(pcmBytes)%bytesPerSample != 0 {
		return nil, fmt.Errorf("decoded data length not aligned to sample size")
	}

	numSamples := len(pcmBytes) / bytesPerSample
	samples := make([]float32, numSamples)

	// Convert based on bit depth
	for i := 0; i < numSamples; i++ {
		offset := i * bytesPerSample
		var sample int32

		switch d.bitDepth {
		case 16:
			// Little-endian 16-bit PCM
			sample = int32(int16(uint16(pcmBytes[offset]) | uint16(pcmBytes[offset+1])<<8))
			samples[i] = float32(sample) / 32768.0

		case 24:
			// 24-bit PCM (little-endian)
			var b [4]byte
			copy(b[:3], pcmBytes[offset:offset+3])
			u32 := uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16
			// Sign extend
			if u32&0x800000 != 0 {
				u32 |= 0xFF000000
			}
			sample = int32(u32)
			samples[i] = float32(sample) / 8388608.0

		case 32:
			// 32-bit PCM (little-endian)
			sample = int32(uint32(pcmBytes[offset]) |
				uint32(pcmBytes[offset+1])<<8 |
				uint32(pcmBytes[offset+2])<<16 |
				uint32(pcmBytes[offset+3])<<24)
			samples[i] = float32(sample) / 2147483648.0

		default:
			return nil, fmt.Errorf("unsupported bit depth: %d", d.bitDepth)
		}

		// Clamp to [-1.0, 1.0]
		if samples[i] > 1.0 {
			samples[i] = 1.0
		} else if samples[i] < -1.0 {
			samples[i] = -1.0
		}
	}

	return samples, nil
}

// DecodeStream decodes ALAC data from a stream
func (d *ALACDecoder) DecodeStream(reader io.Reader) ([]float32, error) {
	// Read data from stream and decode
	buf := make([]byte, 4096)
	n, err := reader.Read(buf)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("failed to read from stream: %w", err)
	}
	if n == 0 {
		return nil, io.EOF
	}

	return d.Decode(buf[:n])
}

// SampleRate returns the sample rate in Hz
func (d *ALACDecoder) SampleRate() int {
	return d.sampleRate
}

// Channels returns the number of audio channels
func (d *ALACDecoder) Channels() int {
	return d.channels
}

// Format returns the audio format being decoded
func (d *ALACDecoder) Format() Format {
	return FormatALAC
}

// Close closes the decoder and releases resources
func (d *ALACDecoder) Close() error {
	if d.decoder != nil {
		d.decoder.Free()
	}
	return nil
}

