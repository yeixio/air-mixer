package alac

import (
	"fmt"
)

// Decoder decodes ALAC (Apple Lossless Audio Codec) data
type Decoder struct {
	magicCookie *MagicCookie
	bitstream   *BitstreamReader
	riceDecoder *RiceDecoder
	predictor   *Predictor
}

// NewDecoder creates a new ALAC decoder
func NewDecoder(magicCookie *MagicCookie) *Decoder {
	return &Decoder{
		magicCookie: magicCookie,
		predictor:   NewPredictor(),
	}
}

// DecodeFrame decodes a single ALAC frame
// Returns PCM samples as int32 (will be converted to float32 later)
func (d *Decoder) DecodeFrame(data []byte) ([]int32, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty frame data")
	}

	// Initialize bitstream reader
	d.bitstream = NewBitstreamReader(data)
	d.riceDecoder = NewRiceDecoder(d.bitstream)

	// Reset predictor for new frame
	d.predictor.Reset()

	// Calculate number of samples per channel
	samplesPerChannel := int(d.magicCookie.FrameLength)
	totalSamples := samplesPerChannel * int(d.magicCookie.NumChannels)

	// Decode samples
	samples := make([]int32, totalSamples)

	// ALAC decodes channel-interleaved samples
	for i := 0; i < samplesPerChannel; i++ {
		for ch := 0; ch < int(d.magicCookie.NumChannels); ch++ {
			sampleIdx := i*int(d.magicCookie.NumChannels) + ch

			// Decode Rice-coded residual
			// The Rice parameter k is typically derived from the magic cookie
			k := int(d.magicCookie.KB)
			residual, err := d.riceDecoder.DecodeRice(k)
			if err != nil {
				return nil, fmt.Errorf("failed to decode Rice code at sample %d: %w", sampleIdx, err)
			}

			// Reconstruct sample using predictor
			sample := d.predictor.Reconstruct(residual)
			samples[sampleIdx] = sample
		}
	}

	return samples, nil
}

// DecodeFrameToFloat32 decodes a frame and converts to float32 samples
// Returns samples normalized to [-1.0, 1.0] range
func (d *Decoder) DecodeFrameToFloat32(data []byte) ([]float32, error) {
	intSamples, err := d.DecodeFrame(data)
	if err != nil {
		return nil, err
	}

	// Convert to float32 and normalize
	bitDepth := int(d.magicCookie.BitDepth)
	maxValue := float32(uint32(1) << (bitDepth - 1))

	samples := make([]float32, len(intSamples))
	for i, sample := range intSamples {
		samples[i] = float32(sample) / maxValue

		// Clamp to [-1.0, 1.0]
		if samples[i] > 1.0 {
			samples[i] = 1.0
		} else if samples[i] < -1.0 {
			samples[i] = -1.0
		}
	}

	return samples, nil
}

// SampleRate returns the sample rate from the magic cookie
func (d *Decoder) SampleRate() int {
	return int(d.magicCookie.SampleRate)
}

// Channels returns the number of channels from the magic cookie
func (d *Decoder) Channels() int {
	return int(d.magicCookie.NumChannels)
}

// BitDepth returns the bit depth from the magic cookie
func (d *Decoder) BitDepth() int {
	return int(d.magicCookie.BitDepth)
}

