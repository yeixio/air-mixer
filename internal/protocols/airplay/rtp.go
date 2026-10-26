package airplay

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"time"
)

// RTP payload types used by RAOP. Packets sent over the control port set
// the marker bit, so the type byte reads 0xd4, 0xd5 or 0xd6 on the wire.
const (
	ptAudio            = 0x60
	ptSync             = 0x54
	ptTimingRequest    = 0x52
	ptTimingResponse   = 0x53
	ptRetransmitReq    = 0x55
	ptRetransmitAnswer = 0x56

	rtpHeaderLen = 12
)

type rtpHeader struct {
	Marker      bool
	PayloadType byte
	Seq         uint16
	Timestamp   uint32
	SSRC        uint32
}

func parseRTP(b []byte) (rtpHeader, []byte, error) {
	if len(b) < rtpHeaderLen {
		return rtpHeader{}, nil, errors.New("short RTP packet")
	}
	h := rtpHeader{
		Marker:      b[1]&0x80 != 0,
		PayloadType: b[1] & 0x7f,
		Seq:         binary.BigEndian.Uint16(b[2:4]),
		Timestamp:   binary.BigEndian.Uint32(b[4:8]),
		SSRC:        binary.BigEndian.Uint32(b[8:12]),
	}
	return h, b[rtpHeaderLen:], nil
}

func (h rtpHeader) append(b []byte) []byte {
	pt := h.PayloadType
	if h.Marker {
		pt |= 0x80
	}
	b = append(b, 0x80, pt)
	b = binary.BigEndian.AppendUint16(b, h.Seq)
	b = binary.BigEndian.AppendUint32(b, h.Timestamp)
	return binary.BigEndian.AppendUint32(b, h.SSRC)
}

// aesCipher implements RAOP's audio encryption: every packet is encrypted
// independently with AES-128-CBC using the session IV, and a trailing
// partial block is sent in the clear.
type aesCipher struct {
	block cipher.Block
	iv    []byte
}

func newAESCipher(key, iv []byte) (*aesCipher, error) {
	if len(iv) != aes.BlockSize {
		return nil, errors.New("AES IV must be 16 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return &aesCipher{block: block, iv: iv}, nil
}

func (c *aesCipher) encrypt(dst, src []byte) {
	n := len(src) &^ (aes.BlockSize - 1)
	cipher.NewCBCEncrypter(c.block, c.iv).CryptBlocks(dst[:n], src[:n])
	copy(dst[n:], src[n:])
}

func (c *aesCipher) decrypt(dst, src []byte) {
	n := len(src) &^ (aes.BlockSize - 1)
	cipher.NewCBCDecrypter(c.block, c.iv).CryptBlocks(dst[:n], src[:n])
	copy(dst[n:], src[n:])
}

// NTP timestamps count from 1900; Unix time from 1970.
const ntpEpochOffset = 2208988800

func ntpTime(t time.Time) uint64 {
	secs := uint64(t.Unix() + ntpEpochOffset)
	frac := uint64(t.Nanosecond()) << 32 / 1e9
	return secs<<32 | frac
}
