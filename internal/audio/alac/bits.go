package alac

import "errors"

var errShortFrame = errors.New("alac: frame truncated")

// bitReader reads big-endian bit fields. Reading past the end yields zero
// bits and records errShortFrame instead of panicking.
type bitReader struct {
	buf []byte
	pos int // in bits
	err error
}

func (r *bitReader) read(n int) uint32 {
	var v uint32
	for n > 0 {
		i := r.pos >> 3
		if i >= len(r.buf) {
			r.err = errShortFrame
			return v << n
		}
		avail := 8 - r.pos&7
		take := min(avail, n)
		b := uint32(r.buf[i]>>(avail-take)) & (1<<take - 1)
		v = v<<take | b
		r.pos += take
		n -= take
	}
	return v
}

func (r *bitReader) unread(n int) { r.pos -= n }

// bitWriter appends big-endian bit fields to a byte slice.
type bitWriter struct {
	buf []byte
	pos int // in bits
}

func (w *bitWriter) write(v uint32, n int) {
	for n > 0 {
		if w.pos&7 == 0 {
			w.buf = append(w.buf, 0)
		}
		avail := 8 - w.pos&7
		take := min(avail, n)
		bits := byte(v>>(n-take)) & (1<<take - 1)
		w.buf[len(w.buf)-1] |= bits << (avail - take)
		w.pos += take
		n -= take
	}
}

// signExtend interprets the low bits of v as a two's complement number.
func signExtend(v int32, bits int) int32 {
	shift := 32 - bits
	return v << shift >> shift
}
