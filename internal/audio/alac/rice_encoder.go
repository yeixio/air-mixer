package alac

// RiceEncoder encodes data using Rice coding
// Rice coding is a form of entropy coding used in ALAC
type RiceEncoder struct {
	bitstream *BitstreamWriter
}

// NewRiceEncoder creates a new Rice encoder
func NewRiceEncoder(bitstream *BitstreamWriter) *RiceEncoder {
	return &RiceEncoder{
		bitstream: bitstream,
	}
}

// EncodeRice encodes a value using Rice coding
// Rice coding: value = (quotient << k) + remainder
// where quotient is unary-coded and remainder is k-bit binary
func (re *RiceEncoder) EncodeRice(value int32, k int) {
	if k < 0 || k > 16 {
		return
	}

	// Convert signed to unsigned using sign-magnitude
	// ALAC uses sign-magnitude: if value is negative, make it odd
	var unsignedValue uint32
	if value < 0 {
		unsignedValue = uint32((-value << 1) - 1)
	} else {
		unsignedValue = uint32(value << 1)
	}

	// Calculate quotient and remainder
	quotient := unsignedValue >> k
	remainder := unsignedValue & ((1 << k) - 1)

	// Encode quotient (unary coding: number of 1s followed by 0)
	for i := uint32(0); i < quotient; i++ {
		re.bitstream.WriteBit(true)
	}
	re.bitstream.WriteBit(false)

	// Encode remainder (k-bit binary)
	if k > 0 {
		re.bitstream.WriteBits(remainder, k)
	}
}

