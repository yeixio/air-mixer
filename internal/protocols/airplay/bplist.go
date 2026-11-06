package airplay

// A minimal binary property list ("bplist00") encoder and decoder, for the
// bodies of AirPlay 2 SETUP requests and responses. It handles the types
// those use: dictionaries, arrays, strings, integers, reals, booleans and
// data.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
	"unicode/utf16"
)

// encodePlist encodes v, built from map[string]any, []any, string, bool,
// []byte, float64 and integer types, as a binary property list.
func encodePlist(v any) ([]byte, error) {
	e := &plistEncoder{}
	if _, err := e.add(v); err != nil {
		return nil, err
	}
	refSize := byteWidth(uint64(len(e.objects)))
	// Objects are encoded after the references are known.
	out := []byte("bplist00")
	offsets := make([]uint64, len(e.objects))
	for i, o := range e.objects {
		offsets[i] = uint64(len(out))
		out = o.encode(out, refSize)
	}
	tableOffset := uint64(len(out))
	offSize := byteWidth(tableOffset)
	for _, off := range offsets {
		out = appendSized(out, off, offSize)
	}
	trailer := make([]byte, 32)
	trailer[6] = byte(offSize)
	trailer[7] = byte(refSize)
	binary.BigEndian.PutUint64(trailer[8:], uint64(len(e.objects)))
	binary.BigEndian.PutUint64(trailer[16:], 0) // top object
	binary.BigEndian.PutUint64(trailer[24:], tableOffset)
	return append(out, trailer...), nil
}

type plistObject struct {
	kind byte // high nibble of the marker
	raw  []byte
	refs []int
	n    int // count for strings, data, arrays, dicts
}

func (o plistObject) encode(out []byte, refSize int) []byte {
	switch o.kind {
	case 0x0, 0x1, 0x2: // bool, int, real: raw carries the full encoding
		return append(out, o.raw...)
	}
	if o.n < 15 {
		out = append(out, o.kind<<4|byte(o.n))
	} else {
		out = append(out, o.kind<<4|0xf)
		out = appendPlistInt(out, uint64(o.n))
	}
	out = append(out, o.raw...)
	for _, r := range o.refs {
		out = appendSized(out, uint64(r), refSize)
	}
	return out
}

type plistEncoder struct{ objects []plistObject }

func (e *plistEncoder) push(o plistObject) int {
	e.objects = append(e.objects, o)
	return len(e.objects) - 1
}

func (e *plistEncoder) add(v any) (int, error) {
	switch v := v.(type) {
	case bool:
		b := byte(0x08)
		if v {
			b = 0x09
		}
		return e.push(plistObject{kind: 0, raw: []byte{b}}), nil
	case int:
		return e.addInt(int64(v)), nil
	case int64:
		return e.addInt(v), nil
	case uint32:
		return e.addInt(int64(v)), nil
	case uint64:
		if v > math.MaxInt64 {
			return 0, errors.New("plist: integer out of range")
		}
		return e.addInt(int64(v)), nil
	case float64:
		raw := []byte{0x23}
		raw = binary.BigEndian.AppendUint64(raw, math.Float64bits(v))
		return e.push(plistObject{kind: 2, raw: raw}), nil
	case string:
		for _, r := range v {
			if r > 0x7f {
				u := utf16.Encode([]rune(v))
				raw := make([]byte, 0, 2*len(u))
				for _, c := range u {
					raw = binary.BigEndian.AppendUint16(raw, c)
				}
				return e.push(plistObject{kind: 6, raw: raw, n: len(u)}), nil
			}
		}
		return e.push(plistObject{kind: 5, raw: []byte(v), n: len(v)}), nil
	case []byte:
		return e.push(plistObject{kind: 4, raw: v, n: len(v)}), nil
	case []any:
		i := e.push(plistObject{kind: 0xa, n: len(v)})
		refs := make([]int, len(v))
		for j, item := range v {
			r, err := e.add(item)
			if err != nil {
				return 0, err
			}
			refs[j] = r
		}
		e.objects[i].refs = refs
		return i, nil
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		i := e.push(plistObject{kind: 0xd, n: len(v)})
		refs := make([]int, 2*len(keys))
		for j, k := range keys {
			r, err := e.add(k)
			if err != nil {
				return 0, err
			}
			refs[j] = r
		}
		for j, k := range keys {
			r, err := e.add(v[k])
			if err != nil {
				return 0, err
			}
			refs[len(keys)+j] = r
		}
		e.objects[i].refs = refs
		return i, nil
	}
	return 0, fmt.Errorf("plist: cannot encode %T", v)
}

func (e *plistEncoder) addInt(v int64) int {
	var raw []byte
	switch {
	case v >= 0 && v <= math.MaxUint8:
		raw = []byte{0x10, byte(v)}
	case v >= 0 && v <= math.MaxUint16:
		raw = binary.BigEndian.AppendUint16([]byte{0x11}, uint16(v))
	case v >= 0 && v <= math.MaxUint32:
		raw = binary.BigEndian.AppendUint32([]byte{0x12}, uint32(v))
	default: // negative numbers are always 8 bytes
		raw = binary.BigEndian.AppendUint64([]byte{0x13}, uint64(v))
	}
	return e.push(plistObject{kind: 1, raw: raw})
}

func appendPlistInt(out []byte, v uint64) []byte {
	switch {
	case v <= math.MaxUint8:
		return append(out, 0x10, byte(v))
	case v <= math.MaxUint16:
		return binary.BigEndian.AppendUint16(append(out, 0x11), uint16(v))
	case v <= math.MaxUint32:
		return binary.BigEndian.AppendUint32(append(out, 0x12), uint32(v))
	}
	return binary.BigEndian.AppendUint64(append(out, 0x13), v)
}

func byteWidth(v uint64) int {
	switch {
	case v <= math.MaxUint8:
		return 1
	case v <= math.MaxUint16:
		return 2
	case v <= math.MaxUint32:
		return 4
	}
	return 8
}

func appendSized(out []byte, v uint64, size int) []byte {
	for i := size - 1; i >= 0; i-- {
		out = append(out, byte(v>>(8*i)))
	}
	return out
}

// decodePlist decodes a binary property list into map[string]any, []any,
// string, bool, []byte, float64 and int64 (uint64 for integers above the
// int64 range).
func decodePlist(b []byte) (any, error) {
	if len(b) < 40 || string(b[:8]) != "bplist00" {
		return nil, errors.New("plist: not a binary property list")
	}
	t := b[len(b)-32:]
	d := &plistDecoder{
		b:       b,
		offSize: int(t[6]),
		refSize: int(t[7]),
		count:   binary.BigEndian.Uint64(t[8:16]),
		table:   binary.BigEndian.Uint64(t[24:32]),
	}
	top := binary.BigEndian.Uint64(t[16:24])
	if d.offSize < 1 || d.offSize > 8 || d.refSize < 1 || d.refSize > 8 ||
		d.count > uint64(len(b)) || d.table > uint64(len(b))-32 ||
		d.count*uint64(d.offSize) > uint64(len(b))-32-d.table {
		return nil, errors.New("plist: bad trailer")
	}
	return d.object(top, 0)
}

type plistDecoder struct {
	b                []byte
	offSize, refSize int
	count, table     uint64
}

func (d *plistDecoder) sized(at uint64, size int) (uint64, error) {
	if at+uint64(size) > uint64(len(d.b)) {
		return 0, errors.New("plist: truncated")
	}
	var v uint64
	for _, c := range d.b[at : at+uint64(size)] {
		v = v<<8 | uint64(c)
	}
	return v, nil
}

func (d *plistDecoder) object(ref uint64, depth int) (any, error) {
	if ref >= d.count || depth > 32 {
		return nil, errors.New("plist: bad object reference")
	}
	off, err := d.sized(d.table+ref*uint64(d.offSize), d.offSize)
	if err != nil {
		return nil, err
	}
	if off >= d.table {
		return nil, errors.New("plist: bad object offset")
	}
	marker := d.b[off]
	kind, info := marker>>4, marker&0xf
	pos := off + 1
	length := func() (uint64, error) {
		if info != 0xf {
			return uint64(info), nil
		}
		if pos >= uint64(len(d.b)) || d.b[pos]>>4 != 1 {
			return 0, errors.New("plist: bad length")
		}
		size := 1 << (d.b[pos] & 0xf)
		n, err := d.sized(pos+1, size)
		pos += 1 + uint64(size)
		return n, err
	}
	switch kind {
	case 0x0:
		switch marker {
		case 0x08:
			return false, nil
		case 0x09:
			return true, nil
		}
		return nil, nil
	case 0x1:
		size := 1 << info
		v, err := d.sized(pos, size)
		if err != nil {
			return nil, err
		}
		if size == 8 || v <= math.MaxInt64 {
			return int64(v), nil
		}
		return v, nil
	case 0x2:
		size := 1 << info
		v, err := d.sized(pos, size)
		if err != nil {
			return nil, err
		}
		if size == 4 {
			return float64(math.Float32frombits(uint32(v))), nil
		}
		return math.Float64frombits(v), nil
	case 0x4, 0x5, 0x6:
		n, err := length()
		if err != nil {
			return nil, err
		}
		width := uint64(1)
		if kind == 0x6 {
			width = 2
		}
		if n > uint64(len(d.b)) || pos+n*width > uint64(len(d.b)) {
			return nil, errors.New("plist: truncated")
		}
		raw := d.b[pos : pos+n*width]
		switch kind {
		case 0x4:
			return append([]byte(nil), raw...), nil
		case 0x5:
			return string(raw), nil
		}
		u := make([]uint16, n)
		for i := range u {
			u[i] = binary.BigEndian.Uint16(raw[2*i:])
		}
		return string(utf16.Decode(u)), nil
	case 0xa, 0xd:
		n, err := length()
		if err != nil {
			return nil, err
		}
		items := n
		if kind == 0xd {
			items *= 2
		}
		if items > d.count {
			return nil, errors.New("plist: too many items")
		}
		refs := make([]uint64, items)
		for i := range refs {
			if refs[i], err = d.sized(pos+uint64(i*d.refSize), d.refSize); err != nil {
				return nil, err
			}
		}
		if kind == 0xa {
			out := make([]any, n)
			for i, r := range refs {
				if out[i], err = d.object(r, depth+1); err != nil {
					return nil, err
				}
			}
			return out, nil
		}
		out := make(map[string]any, n)
		for i := range n {
			k, err := d.object(refs[i], depth+1)
			if err != nil {
				return nil, err
			}
			ks, ok := k.(string)
			if !ok {
				return nil, errors.New("plist: dictionary key is not a string")
			}
			if out[ks], err = d.object(refs[n+i], depth+1); err != nil {
				return nil, err
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("plist: unsupported object type 0x%x", marker)
}

// plistInt reads an integer from a decoded dictionary.
func plistInt(m map[string]any, key string) (int64, bool) {
	switch v := m[key].(type) {
	case int64:
		return v, true
	case uint64:
		return int64(v), true
	}
	return 0, false
}
