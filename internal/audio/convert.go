package audio

import "fmt"

// RemixChannels converts interleaved samples between channel counts. Mono is
// copied to every output channel; downmixing to mono averages all channels;
// other conversions keep the first channels and pad the rest with silence.
func RemixChannels(samples []float32, from, to int) ([]float32, error) {
	if from <= 0 || to <= 0 {
		return nil, fmt.Errorf("audio: invalid channel counts %d -> %d", from, to)
	}
	if len(samples)%from != 0 {
		return nil, fmt.Errorf("audio: %d samples is not a multiple of %d channels", len(samples), from)
	}
	if from == to {
		return samples, nil
	}
	frames := len(samples) / from
	out := make([]float32, frames*to)
	for i := range frames {
		in := samples[i*from : (i+1)*from]
		dst := out[i*to : (i+1)*to]
		switch {
		case from == 1:
			for c := range dst {
				dst[c] = in[0]
			}
		case to == 1:
			var sum float32
			for _, s := range in {
				sum += s
			}
			dst[0] = sum / float32(from)
		default:
			copy(dst, in)
		}
	}
	return out, nil
}

// Resampler converts interleaved audio between sample rates with linear
// interpolation. It keeps state between calls so that a stream processed in
// chunks has no discontinuities at the chunk boundaries. It is not safe for
// concurrent use.
type Resampler struct {
	from, to, channels int
	pos                float64   // read position relative to the start of the next input chunk
	last               []float32 // final frame of the previous chunk
}

// NewResampler returns a resampler from one rate to another.
func NewResampler(from, to, channels int) (*Resampler, error) {
	if from <= 0 || to <= 0 || channels <= 0 {
		return nil, fmt.Errorf("audio: invalid resampler parameters %d Hz -> %d Hz, %d channels", from, to, channels)
	}
	return &Resampler{from: from, to: to, channels: channels}, nil
}

// Process resamples one chunk of interleaved samples.
func (r *Resampler) Process(samples []float32) ([]float32, error) {
	if len(samples)%r.channels != 0 {
		return nil, fmt.Errorf("audio: %d samples is not a multiple of %d channels", len(samples), r.channels)
	}
	if r.from == r.to {
		return samples, nil
	}
	frames := len(samples) / r.channels
	if frames == 0 {
		return nil, nil
	}

	// Treat the previous chunk's last frame as frame -1 so interpolation
	// can span the boundary.
	frame := func(i int) []float32 {
		if i < 0 {
			return r.last
		}
		return samples[i*r.channels : (i+1)*r.channels]
	}
	if r.last == nil {
		r.last = make([]float32, r.channels)
		copy(r.last, frame(0))
	}

	step := float64(r.from) / float64(r.to)
	out := make([]float32, 0, int(float64(frames)/step+1)*r.channels)
	pos := r.pos
	for pos < float64(frames-1) {
		i := int(pos)
		if pos < 0 {
			i = -1
		}
		frac := float32(pos - float64(i))
		a, b := frame(i), frame(i+1)
		for c := range r.channels {
			out = append(out, a[c]+(b[c]-a[c])*frac)
		}
		pos += step
	}
	r.pos = pos - float64(frames)
	copy(r.last, frame(frames-1))
	return out, nil
}

// Converter adapts a stream from one Format to another.
type Converter struct {
	from, to  Format
	resampler *Resampler
}

// NewConverter returns a converter from one format to another.
func NewConverter(from, to Format) (*Converter, error) {
	if !from.Valid() || !to.Valid() {
		return nil, fmt.Errorf("audio: invalid conversion %+v -> %+v", from, to)
	}
	c := &Converter{from: from, to: to}
	if from.SampleRate != to.SampleRate {
		var err error
		c.resampler, err = NewResampler(from.SampleRate, to.SampleRate, to.Channels)
		if err != nil {
			return nil, err
		}
	}
	return c, nil
}

// Process converts one chunk of samples.
func (c *Converter) Process(samples []float32) ([]float32, error) {
	out, err := RemixChannels(samples, c.from.Channels, c.to.Channels)
	if err != nil || c.resampler == nil {
		return out, err
	}
	return c.resampler.Process(out)
}
