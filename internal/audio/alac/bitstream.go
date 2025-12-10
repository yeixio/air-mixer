package alac

import (
	"errors"
)

// BitstreamReader reads bits from a byte stream
// ALAC uses variable-length bit reading for entropy coding
type BitstreamReader struct {
	data   []byte
	pos    int  // Current byte position
	bitPos int  // Current bit position within byte (0-7)
}

// NewBitstreamReader creates a new bitstream reader
func NewBitstreamReader(data []byte) *BitstreamReader {
	return &BitstreamReader{
		data:   data,
		pos:    0,
		bitPos: 0,
	}
}

// ReadBits reads n bits from the stream
// Returns the bits as an unsigned integer
func (br *BitstreamReader) ReadBits(n int) (uint32, error) {
	if n < 1 || n > 32 {
		return 0, errors.New("invalid bit count (must be 1-32)")
	}

	var result uint32
	bitsRead := 0

	for bitsRead < n {
		if br.pos >= len(br.data) {
			return 0, errors.New("end of stream")
		}

		// Calculate how many bits we can read from current byte
		bitsAvailable := 8 - br.bitPos
		bitsNeeded := n - bitsRead
		bitsToRead := bitsAvailable
		if bitsToRead > bitsNeeded {
			bitsToRead = bitsNeeded
		}

		// Extract bits from current byte
		mask := uint8((1 << bitsToRead) - 1)
		bits := (br.data[br.pos] >> br.bitPos) & mask
		result |= uint32(bits) << bitsRead

		// Update positions
		br.bitPos += bitsToRead
		bitsRead += bitsToRead

		// Move to next byte if needed
		if br.bitPos >= 8 {
			br.bitPos = 0
			br.pos++
		}
	}

	return result, nil
}

// ReadBit reads a single bit from the stream
func (br *BitstreamReader) ReadBit() (bool, error) {
	bit, err := br.ReadBits(1)
	if err != nil {
		return false, err
	}
	return bit == 1, nil
}

// ReadSignedBits reads n bits as a signed integer (two's complement)
func (br *BitstreamReader) ReadSignedBits(n int) (int32, error) {
	bits, err := br.ReadBits(n)
	if err != nil {
		return 0, err
	}

	// Sign extend
	if n < 32 {
		signBit := uint32(1 << (n - 1))
		if bits&signBit != 0 {
			// Negative number - sign extend
			mask := uint32((1 << n) - 1)
			bits |= ^mask
		}
	}

	return int32(bits), nil
}

// Align aligns the bitstream to byte boundary
func (br *BitstreamReader) Align() {
	if br.bitPos != 0 {
		br.bitPos = 0
		br.pos++
	}
}

// Position returns the current byte position
func (br *BitstreamReader) Position() int {
	return br.pos
}

// RemainingBytes returns the number of remaining bytes
func (br *BitstreamReader) RemainingBytes() int {
	if br.pos >= len(br.data) {
		return 0
	}
	return len(br.data) - br.pos
}

// Reset resets the reader to the beginning
func (br *BitstreamReader) Reset() {
	br.pos = 0
	br.bitPos = 0
}

