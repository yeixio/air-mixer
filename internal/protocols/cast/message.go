package cast

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// message is a CastMessage (cast_channel.proto), the frame every Cast
// request and reply travels in. Only text payloads are used.
type message struct {
	Source, Destination, Namespace, Payload string
}

const maxFrame = 64 << 10

// Protobuf field numbers of CastMessage.
const (
	fieldVersion     = 1
	fieldSource      = 2
	fieldDestination = 3
	fieldNamespace   = 4
	fieldPayloadType = 5
	fieldPayloadUTF8 = 6
)

func appendVarint(b []byte, v uint64) []byte { return binary.AppendUvarint(b, v) }

func appendString(b []byte, field int, s string) []byte {
	b = appendVarint(b, uint64(field<<3|2))
	b = appendVarint(b, uint64(len(s)))
	return append(b, s...)
}

// marshal encodes m with its 4-byte big-endian length prefix.
func (m message) marshal() []byte {
	b := make([]byte, 4, 64+len(m.Payload))
	b = appendVarint(b, fieldVersion<<3) // CASTV2_1_0 = 0
	b = appendVarint(b, 0)
	b = appendString(b, fieldSource, m.Source)
	b = appendString(b, fieldDestination, m.Destination)
	b = appendString(b, fieldNamespace, m.Namespace)
	b = appendVarint(b, fieldPayloadType<<3) // STRING = 0
	b = appendVarint(b, 0)
	b = appendString(b, fieldPayloadUTF8, m.Payload)
	binary.BigEndian.PutUint32(b, uint32(len(b)-4))
	return b
}

// readMessage reads one framed CastMessage.
func readMessage(r io.Reader) (message, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return message{}, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > maxFrame {
		return message{}, fmt.Errorf("cast: frame of %d bytes", n)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return message{}, err
	}
	return unmarshal(b)
}

var errBadMessage = errors.New("cast: malformed message")

func unmarshal(b []byte) (message, error) {
	var m message
	for len(b) > 0 {
		key, k := binary.Uvarint(b)
		if k <= 0 {
			return m, errBadMessage
		}
		b = b[k:]
		field, wire := int(key>>3), key&7
		switch wire {
		case 0:
			_, k := binary.Uvarint(b)
			if k <= 0 {
				return m, errBadMessage
			}
			b = b[k:]
		case 2:
			l, k := binary.Uvarint(b)
			if k <= 0 || uint64(len(b)-k) < l {
				return m, errBadMessage
			}
			v := string(b[k : k+int(l)])
			b = b[k+int(l):]
			switch field {
			case fieldSource:
				m.Source = v
			case fieldDestination:
				m.Destination = v
			case fieldNamespace:
				m.Namespace = v
			case fieldPayloadUTF8:
				m.Payload = v
			}
		default:
			return m, errBadMessage
		}
	}
	return m, nil
}
