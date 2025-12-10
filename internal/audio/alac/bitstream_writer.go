package alac

import (
	"bytes"
)

// BitstreamWriter writes bits to a byte stream
// ALAC uses variable-length bit writing for entropy coding
type BitstreamWriter struct {
	buffer *bytes.Buffer
	bitPos int  // Current bit position within byte (0-7)
	current byte // Current byte being written
}

// NewBitstreamWriter creates a new bitstream writer
func NewBitstreamWriter() *BitstreamWriter {
	return &BitstreamWriter{
		buffer: bytes.NewBuffer(nil),
		bitPos: 0,
		current: 0,
	}
}

// WriteBits writes n bits to the stream
func (bw *BitstreamWriter) WriteBits(value uint32, n int) {
	if n < 1 || n > 32 {
		return
	}

	bitsWritten := 0
	for bitsWritten < n {
		// Calculate how many bits we can write to current byte
		bitsAvailable := 8 - bw.bitPos
		bitsNeeded := n - bitsWritten
		bitsToWrite := bitsAvailable
		if bitsToWrite > bitsNeeded {
			bitsToWrite = bitsNeeded
		}

		// Extract bits to write
		mask := uint32((1 << bitsToWrite) - 1)
		bits := (value >> bitsWritten) & mask

		// Write bits to current byte
		bw.current |= byte(bits << bw.bitPos)
		bw.bitPos += bitsToWrite
		bitsWritten += bitsToWrite

		// Flush byte if full
		if bw.bitPos >= 8 {
			bw.buffer.WriteByte(bw.current)
			bw.current = 0
			bw.bitPos = 0
		}
	}
}

// WriteBit writes a single bit to the stream
func (bw *BitstreamWriter) WriteBit(bit bool) {
	if bit {
		bw.current |= (1 << bw.bitPos)
	}
	bw.bitPos++
	
	if bw.bitPos >= 8 {
		bw.buffer.WriteByte(bw.current)
		bw.current = 0
		bw.bitPos = 0
	}
}

// Align aligns the bitstream to byte boundary
func (bw *BitstreamWriter) Align() {
	if bw.bitPos != 0 {
		bw.buffer.WriteByte(bw.current)
		bw.current = 0
		bw.bitPos = 0
	}
}

// Bytes returns the written bytes
func (bw *BitstreamWriter) Bytes() []byte {
	// Make a copy and flush any remaining bits
	result := make([]byte, bw.buffer.Len())
	copy(result, bw.buffer.Bytes())
	if bw.bitPos > 0 {
		result = append(result, bw.current)
	}
	return result
}

// Reset resets the writer
func (bw *BitstreamWriter) Reset() {
	bw.buffer.Reset()
	bw.bitPos = 0
	bw.current = 0
}

