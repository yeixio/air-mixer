package alac

import (
	"fmt"
	"math/bits"
)

// The decoder is a Go port of David Hammerton's ALAC decoder (MIT licensed,
// http://crazney.net/programs/itunes/alac.html), which shairport and most
// open-source AirPlay receivers build on. Variable names follow the original
// where that helps when comparing the two.

const (
	elementMono   = 0 // single channel element
	elementStereo = 1 // channel pair element
	riceThreshold = 8 // longest unary prefix before a raw value follows
)

// Decoder decodes ALAC frames for one stream. It is not safe for concurrent
// use.
type Decoder struct {
	cfg Config

	predictError [2][]int32
	output       [2][]int32
	uncompressed [2][]int32
}

// NewDecoder returns a decoder for streams described by cfg.
func NewDecoder(cfg Config) (*Decoder, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	d := &Decoder{cfg: cfg}
	for ch := range 2 {
		d.predictError[ch] = make([]int32, cfg.FrameLength)
		d.output[ch] = make([]int32, cfg.FrameLength)
		d.uncompressed[ch] = make([]int32, cfg.FrameLength)
	}
	return d, nil
}

// Config returns the stream configuration.
func (d *Decoder) Config() Config { return d.cfg }

// Decode decodes one frame into interleaved samples at the stream's bit
// depth. The result has Config().NumChannels channels; a mono element in a
// stereo stream is copied to both channels.
func (d *Decoder) Decode(frame []byte) ([]int32, error) {
	r := &bitReader{buf: frame}
	element := r.read(3)
	var (
		n   int
		err error
	)
	switch element {
	case elementMono:
		n, err = d.decodeElement(r, 1)
	case elementStereo:
		n, err = d.decodeElement(r, 2)
	default:
		return nil, fmt.Errorf("alac: unsupported element type %d", element)
	}
	if err != nil {
		return nil, err
	}
	if r.err != nil {
		return nil, r.err
	}

	channels := int(d.cfg.NumChannels)
	out := make([]int32, n*channels)
	for i := range n {
		for ch := range channels {
			src := ch
			if element == elementMono {
				src = 0
			}
			out[i*channels+ch] = d.output[src][i]
		}
	}
	return out, nil
}

// DecodeFloat decodes one frame into interleaved samples scaled to [-1, 1).
func (d *Decoder) DecodeFloat(frame []byte) ([]float32, error) {
	pcm, err := d.Decode(frame)
	if err != nil {
		return nil, err
	}
	scale := 1 / float32(int32(1)<<(d.cfg.BitDepth-1))
	out := make([]float32, len(pcm))
	for i, s := range pcm {
		out[i] = float32(s) * scale
	}
	return out, nil
}

// decodeElement decodes a mono or stereo element into d.output and returns
// the number of samples per channel.
func (d *Decoder) decodeElement(r *bitReader, channels int) (int, error) {
	r.read(4)  // element instance tag
	r.read(12) // unused header bits
	hasSize := r.read(1) == 1
	uncompressedBytes := int(r.read(2))
	isNotCompressed := r.read(1) == 1

	n := int(d.cfg.FrameLength)
	if hasSize {
		size := r.read(32)
		if size > d.cfg.FrameLength {
			return 0, fmt.Errorf("alac: frame has %d samples, configured maximum is %d", size, d.cfg.FrameLength)
		}
		n = int(size)
	}

	bitDepth := int(d.cfg.BitDepth)
	if isNotCompressed {
		for i := range n {
			for ch := range channels {
				d.output[ch][i] = readRawSample(r, bitDepth)
			}
		}
		if channels == 2 {
			d.combineStereo(n, 0, 0, 0)
		}
		return n, r.err
	}

	readSampleSize := bitDepth - uncompressedBytes*8 + channels - 1
	interlacingShift := uint8(r.read(8))
	interlacingLeftWeight := uint8(r.read(8))

	type channelParams struct {
		predictionType  int
		quantization    int
		riceModifier    int
		coefficients    [32]int16
		numCoefficients int
	}
	var params [2]channelParams
	for ch := range channels {
		p := &params[ch]
		p.predictionType = int(r.read(4))
		p.quantization = int(r.read(4))
		p.riceModifier = int(r.read(3))
		p.numCoefficients = int(r.read(5))
		for i := range p.numCoefficients {
			p.coefficients[i] = int16(r.read(16))
		}
	}

	if uncompressedBytes > 0 {
		for i := range n {
			for ch := range channels {
				d.uncompressed[ch][i] = int32(r.read(uncompressedBytes * 8))
			}
		}
	}

	for ch := range channels {
		p := &params[ch]
		if p.predictionType != 0 {
			return 0, fmt.Errorf("alac: unsupported prediction type %d", p.predictionType)
		}
		err := entropyRiceDecode(r, d.predictError[ch][:n], readSampleSize,
			int(d.cfg.MB), int(d.cfg.KB), p.riceModifier*int(d.cfg.PB)/4, int32(1)<<d.cfg.KB-1)
		if err != nil {
			return 0, err
		}
		predictorDecompressFIRAdapt(d.predictError[ch][:n], d.output[ch][:n], readSampleSize,
			p.coefficients[:p.numCoefficients], p.quantization)
	}

	if channels == 2 {
		d.combineStereo(n, uncompressedBytes, interlacingShift, interlacingLeftWeight)
	} else if uncompressedBytes > 0 {
		mergeUncompressed(d.output[0][:n], d.uncompressed[0][:n], uncompressedBytes)
	}
	// Output is truncated to the stream's bit depth, as in the reference
	// decoder.
	for ch := range channels {
		for i, v := range d.output[ch][:n] {
			d.output[ch][i] = signExtend(v, bitDepth)
		}
	}
	return n, r.err
}

// readRawSample reads one verbatim sample from an uncompressed frame.
func readRawSample(r *bitReader, bitDepth int) int32 {
	if bitDepth <= 16 {
		return signExtend(int32(r.read(bitDepth)), bitDepth)
	}
	v := r.read(16) << (bitDepth - 16)
	v |= r.read(bitDepth - 16)
	return signExtend(int32(v), bitDepth)
}

// combineStereo undoes ALAC's mid/side decorrelation in place, turning
// d.output[0] and d.output[1] into left and right.
func (d *Decoder) combineStereo(n, uncompressedBytes int, shift, leftWeight uint8) {
	a, b := d.output[0][:n], d.output[1][:n]
	if leftWeight != 0 {
		for i := range n {
			mid, diff := a[i], b[i]
			right := mid - (diff*int32(leftWeight))>>shift
			a[i], b[i] = right+diff, right
		}
	}
	if uncompressedBytes > 0 {
		mergeUncompressed(a, d.uncompressed[0][:n], uncompressedBytes)
		mergeUncompressed(b, d.uncompressed[1][:n], uncompressedBytes)
	}
}

// mergeUncompressed appends the low-order bytes that 24-bit streams carry
// outside the entropy coder.
func mergeUncompressed(samples, low []int32, uncompressedBytes int) {
	shift := uncompressedBytes * 8
	mask := int32(1)<<shift - 1
	for i := range samples {
		samples[i] = samples[i]<<shift | low[i]&mask
	}
}

// entropyRiceDecode decodes adaptive Golomb-Rice coded prediction residuals.
func entropyRiceDecode(r *bitReader, out []int32, readSampleSize, initialHistory, kModifier, historyMult int, kModifierMask int32) error {
	history := int32(initialHistory)
	var signModifier int32
	for i := 0; i < len(out); i++ {
		k := 31 - bits.LeadingZeros32(uint32(history>>9+3))
		k = min(k, kModifier)

		decoded := decodeValue(r, readSampleSize, k, -1) + signModifier
		final := (decoded + 1) / 2
		if decoded&1 != 0 {
			final = -final
		}
		out[i] = final
		signModifier = 0

		history += decoded*int32(historyMult) - (history*int32(historyMult))>>9
		if decoded > 0xFFFF {
			history = 0xFFFF
		}

		// A low history signals a run of zero residuals.
		if history < 128 && i+1 < len(out) {
			signModifier = 1
			k := bits.LeadingZeros32(uint32(history)) + int((history+16)/64) - 24
			blockSize := decodeValue(r, 16, k, kModifierMask)
			if blockSize > 0 {
				if i+1+int(blockSize) > len(out) {
					return fmt.Errorf("alac: zero run of %d overflows frame", blockSize)
				}
				clear(out[i+1 : i+1+int(blockSize)])
				i += int(blockSize)
			}
			if blockSize > 0xFFFF {
				signModifier = 0
			}
			history = 0
		}
		if r.err != nil {
			return r.err
		}
	}
	return nil
}

// decodeValue reads one Rice-coded value with parameter k. Prefixes longer
// than riceThreshold escape to a raw readSampleSize-bit value.
func decodeValue(r *bitReader, readSampleSize, k int, mask int32) int32 {
	var x int32
	for x <= riceThreshold && r.read(1) == 1 {
		x++
	}
	if x > riceThreshold {
		return int32(r.read(readSampleSize) & (0xFFFFFFFF >> (32 - readSampleSize)))
	}
	if k != 1 {
		extra := int32(r.read(k))
		x *= (int32(1)<<k - 1) & mask
		if extra > 1 {
			x += extra - 1
		} else {
			r.unread(1)
		}
	}
	return x
}

func signOnly(v int32) int32 {
	switch {
	case v < 0:
		return -1
	case v > 0:
		return 1
	}
	return 0
}

// predictorDecompressFIRAdapt reconstructs samples from residuals with
// ALAC's sign-adaptive FIR predictor, updating coefs as it goes.
func predictorDecompressFIRAdapt(errs, out []int32, readSampleSize int, coefs []int16, quantization int) {
	n := len(out)
	if n == 0 {
		return
	}
	out[0] = errs[0]
	numCoefs := len(coefs)

	if numCoefs == 0 {
		copy(out[1:], errs[1:])
		return
	}
	if numCoefs == 31 {
		// Each residual is the difference from the previous sample.
		for i := 1; i < n; i++ {
			out[i] = signExtend(out[i-1]+errs[i], readSampleSize)
		}
		return
	}

	// Warm-up samples.
	for i := 0; i < numCoefs && i+1 < n; i++ {
		out[i+1] = signExtend(out[i]+errs[i+1], readSampleSize)
	}

	var round int32
	if quantization > 0 {
		round = 1 << (quantization - 1)
	}
	for i := numCoefs + 1; i < n; i++ {
		base := i - numCoefs - 1
		window := out[base : i+1]
		errVal := errs[i]

		var sum int32
		for j := range numCoefs {
			sum += (window[numCoefs-j] - window[0]) * int32(coefs[j])
		}
		v := (round + sum) >> quantization
		window[numCoefs+1] = signExtend(v+window[0]+errVal, readSampleSize)

		// Nudge the coefficients towards the sign of the error.
		dir := signOnly(errVal)
		for p := numCoefs - 1; p >= 0 && errVal*dir > 0; p-- {
			val := window[0] - window[numCoefs-p]
			sign := signOnly(val) * dir
			coefs[p] -= int16(sign)
			val *= sign
			errVal -= (val >> quantization) * int32(numCoefs-p)
		}
	}
}
