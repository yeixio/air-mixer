package alac

import (
	"fmt"
)

// Encoder encodes PCM samples into ALAC format
type Encoder struct {
	magicCookie *MagicCookie
	bitstream   *BitstreamWriter
	riceEncoder *RiceEncoder
	predictor   *Predictor
}

// NewEncoder creates a new ALAC encoder
func NewEncoder(magicCookie *MagicCookie) *Encoder {
	return &Encoder{
		magicCookie: magicCookie,
		predictor:   NewPredictor(),
	}
}

// EncodeFrame encodes a single frame of PCM samples into ALAC format
// samples should be int32 PCM samples, channel-interleaved
func (e *Encoder) EncodeFrame(samples []int32) ([]byte, error) {
	if len(samples) == 0 {
		return nil, fmt.Errorf("empty samples")
	}

	// Initialize bitstream writer
	e.bitstream = NewBitstreamWriter()
	e.riceEncoder = NewRiceEncoder(e.bitstream)

	// Reset predictor for new frame
	e.predictor.Reset()

	// Calculate number of samples per channel
	samplesPerChannel := len(samples) / int(e.magicCookie.NumChannels)
	if samplesPerChannel == 0 {
		return nil, fmt.Errorf("invalid sample count")
	}

	// Encode samples
	// ALAC encodes channel-interleaved samples
	for i := 0; i < samplesPerChannel; i++ {
		for ch := 0; ch < int(e.magicCookie.NumChannels); ch++ {
			sampleIdx := i*int(e.magicCookie.NumChannels) + ch
			if sampleIdx >= len(samples) {
				break
			}
			
			sample := samples[sampleIdx]

			// Predict next sample
			predicted := e.predictor.Predict()
			
			// Calculate residual (prediction error)
			residual := sample - predicted
			
			// Update predictor with actual sample
			e.predictor.Update(sample)

			// Encode residual using Rice coding
			k := int(e.magicCookie.KB)
			e.riceEncoder.EncodeRice(residual, k)
		}
	}

	// Align to byte boundary
	e.bitstream.Align()

	return e.bitstream.Bytes(), nil
}

// EncodeFrameFromFloat32 encodes float32 samples to ALAC
// Converts float32 [-1.0, 1.0] to int32 PCM first
func (e *Encoder) EncodeFrameFromFloat32(samples []float32) ([]byte, error) {
	// Convert float32 to int32
	bitDepth := int(e.magicCookie.BitDepth)
	maxValue := int32((1 << (bitDepth - 1)) - 1)
	
	intSamples := make([]int32, len(samples))
	for i, sample := range samples {
		// Clamp to [-1.0, 1.0]
		if sample > 1.0 {
			sample = 1.0
		} else if sample < -1.0 {
			sample = -1.0
		}
		
		// Convert to int32
		intSamples[i] = int32(sample * float32(maxValue))
	}
	
	return e.EncodeFrame(intSamples)
}

