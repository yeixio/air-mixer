package audio

import (
	"encoding/binary"
	"fmt"
)

// PCMDecoder decodes PCM audio data
type PCMDecoder struct {
	sampleRate int
	channels   int
	bitDepth   int // 16 or 24 or 32
	endianness binary.ByteOrder
}

// NewPCMDecoder creates a new PCM decoder
func NewPCMDecoder(sampleRate, channels, bitDepth int, littleEndian bool) *PCMDecoder {
	var endianness binary.ByteOrder = binary.BigEndian
	if littleEndian {
		endianness = binary.LittleEndian
	}

	return &PCMDecoder{
		sampleRate: sampleRate,
		channels:   channels,
		bitDepth:   bitDepth,
		endianness: endianness,
	}
}

// Decode decodes PCM data into float32 samples
func (d *PCMDecoder) Decode(data []byte) ([]float32, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty data")
	}

	var samples []float32
	bytesPerSample := d.bitDepth / 8

	if len(data)%bytesPerSample != 0 {
		return nil, fmt.Errorf("data length not aligned to sample size")
	}

	numSamples := len(data) / bytesPerSample
	samples = make([]float32, numSamples)

	for i := 0; i < numSamples; i++ {
		offset := i * bytesPerSample
		var sample int32

		switch d.bitDepth {
		case 16:
			sample = int32(d.endianness.Uint16(data[offset : offset+2]))
			// Normalize to [-1.0, 1.0]
			samples[i] = float32(sample) / 32768.0

		case 24:
			// 24-bit samples are tricky - read as 32-bit and shift
			var b [4]byte
			if d.endianness == binary.LittleEndian {
				copy(b[:3], data[offset:offset+3])
			} else {
				copy(b[1:], data[offset:offset+3])
			}
			u32 := d.endianness.Uint32(b[:])
			// Sign extend 24-bit to 32-bit
			if u32&0x800000 != 0 {
				u32 |= 0xFF000000
			}
			sample = int32(u32)
			// Normalize to [-1.0, 1.0]
			samples[i] = float32(sample) / 8388608.0

		case 32:
			sample = int32(d.endianness.Uint32(data[offset : offset+4]))
			// Normalize to [-1.0, 1.0]
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

// SampleRate returns the sample rate in Hz
func (d *PCMDecoder) SampleRate() int {
	return d.sampleRate
}

// Channels returns the number of audio channels
func (d *PCMDecoder) Channels() int {
	return d.channels
}

// Format returns the audio format being decoded
func (d *PCMDecoder) Format() Format {
	return FormatPCM
}

// Close closes the decoder and releases resources
func (d *PCMDecoder) Close() error {
	// PCM decoder doesn't need cleanup
	return nil
}

