// Package audio holds the sample format shared by every part of Air-Mixer and
// the DSP building blocks that operate on it: mixing, resampling and channel
// conversion.
//
// Audio moves between components as []float32 buffers of interleaved samples
// in [-1, 1]. Inside the router all audio uses SampleRate and Channels;
// protocol adapters convert at the edges.
package audio

import "math"

const (
	// SampleRate is the router's internal sample rate in Hz. It matches
	// AirPlay, the most common source.
	SampleRate = 44100
	// Channels is the router's internal channel count (stereo).
	Channels = 2
)

// Format describes a stream of interleaved float32 samples.
type Format struct {
	SampleRate int
	Channels   int
}

// Internal is the format audio uses inside the router.
var Internal = Format{SampleRate: SampleRate, Channels: Channels}

// Valid reports whether f has a usable rate and channel count.
func (f Format) Valid() bool { return f.SampleRate > 0 && f.Channels > 0 }

// Clamp limits s to [-1, 1].
func Clamp(s float32) float32 {
	return max(-1, min(1, s))
}

// Int16ToFloat converts a 16-bit sample to [-1, 1).
func Int16ToFloat(s int16) float32 { return float32(s) / 32768 }

// FloatToInt16 converts a sample in [-1, 1] to 16-bit, clipping values
// outside that range.
func FloatToInt16(s float32) int16 {
	v := math.Round(float64(s) * 32768)
	return int16(max(math.MinInt16, min(math.MaxInt16, v)))
}

// Gain returns a copy of samples scaled by g. It never modifies its input,
// which may be shared with other consumers.
func Gain(samples []float32, g float32) []float32 {
	out := make([]float32, len(samples))
	for i, s := range samples {
		out[i] = s * g
	}
	return out
}
