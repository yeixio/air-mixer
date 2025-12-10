package audio

import (
	"fmt"
)

// LinearSampleRateConverter implements sample rate conversion using linear interpolation
type LinearSampleRateConverter struct{}

// NewLinearSampleRateConverter creates a new linear sample rate converter
func NewLinearSampleRateConverter() *LinearSampleRateConverter {
	return &LinearSampleRateConverter{}
}

// Convert converts audio samples from one sample rate to another using linear interpolation
func (c *LinearSampleRateConverter) Convert(samples []float32, fromRate, toRate int) ([]float32, error) {
	if fromRate == toRate {
		return samples, nil
	}

	if fromRate <= 0 || toRate <= 0 {
		return nil, fmt.Errorf("invalid sample rate: from=%d, to=%d", fromRate, toRate)
	}

	if len(samples) == 0 {
		return samples, nil
	}

	ratio := float64(toRate) / float64(fromRate)
	outputLen := int(float64(len(samples)) * ratio)
	result := make([]float32, outputLen)

	for i := 0; i < outputLen; i++ {
		// Calculate source position
		srcPos := float64(i) / ratio

		// Get the two nearest samples
		idx1 := int(srcPos)
		idx2 := idx1 + 1

		// Handle edge cases
		if idx1 >= len(samples) {
			result[i] = samples[len(samples)-1]
			continue
		}
		if idx2 >= len(samples) {
			result[i] = samples[len(samples)-1]
			continue
		}

		// Linear interpolation
		fraction := srcPos - float64(idx1)
		result[i] = samples[idx1]*(1.0-float32(fraction)) + samples[idx2]*float32(fraction)
	}

	return result, nil
}

// Close closes the converter and releases resources
func (c *LinearSampleRateConverter) Close() error {
	return nil
}

// ChannelConverter implements channel conversion
type ChannelConverter struct{}

// NewChannelConverter creates a new channel converter
func NewChannelConverter() *ChannelConverter {
	return &ChannelConverter{}
}

// Convert converts audio samples between channel configurations
func (c *ChannelConverter) Convert(samples []float32, fromChannels, toChannels int) ([]float32, error) {
	if fromChannels == toChannels {
		return samples, nil
	}

	if fromChannels <= 0 || toChannels <= 0 {
		return nil, fmt.Errorf("invalid channel count: from=%d, to=%d", fromChannels, toChannels)
	}

	if len(samples) == 0 {
		return samples, nil
	}

	if len(samples)%fromChannels != 0 {
		return nil, fmt.Errorf("sample count not aligned to channel count")
	}

	frames := len(samples) / fromChannels
	result := make([]float32, frames*toChannels)

	for i := 0; i < frames; i++ {
		srcOffset := i * fromChannels
		dstOffset := i * toChannels

		switch {
		case fromChannels == 1 && toChannels == 2:
			// Mono to Stereo: duplicate channel
			result[dstOffset] = samples[srcOffset]
			result[dstOffset+1] = samples[srcOffset]

		case fromChannels == 2 && toChannels == 1:
			// Stereo to Mono: average channels
			result[dstOffset] = (samples[srcOffset] + samples[srcOffset+1]) / 2.0

		case fromChannels == 1 && toChannels > 2:
			// Mono to multi-channel: duplicate to all channels
			for j := 0; j < toChannels; j++ {
				result[dstOffset+j] = samples[srcOffset]
			}

		case fromChannels > 2 && toChannels == 1:
			// Multi-channel to Mono: average all channels
			sum := float32(0.0)
			for j := 0; j < fromChannels; j++ {
				sum += samples[srcOffset+j]
			}
			result[dstOffset] = sum / float32(fromChannels)

		case fromChannels == 2 && toChannels > 2:
			// Stereo to multi-channel: duplicate L/R appropriately
			left := samples[srcOffset]
			right := samples[srcOffset+1]
			for j := 0; j < toChannels; j++ {
				if j%2 == 0 {
					result[dstOffset+j] = left
				} else {
					result[dstOffset+j] = right
				}
			}

		case fromChannels > 2 && toChannels == 2:
			// Multi-channel to Stereo: take first two channels
			result[dstOffset] = samples[srcOffset]
			if fromChannels > 1 {
				result[dstOffset+1] = samples[srcOffset+1]
			} else {
				result[dstOffset+1] = samples[srcOffset]
			}

		default:
			// Generic case: take first N channels or pad with zeros
			for j := 0; j < toChannels; j++ {
				if j < fromChannels {
					result[dstOffset+j] = samples[srcOffset+j]
				} else {
					result[dstOffset+j] = 0.0
				}
			}
		}
	}

	return result, nil
}

// Close closes the converter and releases resources
func (c *ChannelConverter) Close() error {
	return nil
}

