package alac

import (
	"errors"
)

// RiceDecoder decodes Rice-coded data
// Rice coding is a form of entropy coding used in ALAC
type RiceDecoder struct {
	bitstream *BitstreamReader
}

// NewRiceDecoder creates a new Rice decoder
func NewRiceDecoder(bitstream *BitstreamReader) *RiceDecoder {
	return &RiceDecoder{
		bitstream: bitstream,
	}
}

// DecodeRice decodes a Rice-coded value
// Rice coding: value = (quotient << k) + remainder
// where quotient is unary-coded and remainder is k-bit binary
func (rd *RiceDecoder) DecodeRice(k int) (int32, error) {
	if k < 0 || k > 16 {
		return 0, errors.New("invalid Rice parameter k (must be 0-16)")
	}

	// Decode quotient (unary coding: number of 1s followed by 0)
	quotient := 0
	for {
		bit, err := rd.bitstream.ReadBit()
		if err != nil {
			return 0, err
		}
		if !bit {
			break
		}
		quotient++
	}

	// Decode remainder (k-bit binary)
	var remainder uint32
	if k > 0 {
		var err error
		remainder, err = rd.bitstream.ReadBits(k)
		if err != nil {
			return 0, err
		}
	}

	// Calculate value: (quotient << k) + remainder
	value := (quotient << k) + int(remainder)

	// Convert to signed (Rice coding uses unsigned, but we need signed)
	// ALAC uses sign-magnitude: if value is odd, it's negative
	if value&1 != 0 {
		// Negative
		return -int32((value + 1) >> 1), nil
	}
	// Positive
	return int32(value >> 1), nil
}

// DecodeRiceSigned decodes a signed Rice-coded value
// This is a variant where the sign is encoded separately
func (rd *RiceDecoder) DecodeRiceSigned(k int) (int32, error) {
	// Decode absolute value
	absValue, err := rd.DecodeRice(k)
	if err != nil {
		return 0, err
	}

	// Decode sign bit
	signBit, err := rd.bitstream.ReadBit()
	if err != nil {
		return 0, err
	}

	if signBit {
		return -absValue, nil
	}
	return absValue, nil
}

