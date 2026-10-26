// Package alac implements the Apple Lossless Audio Codec as used by AirPlay
// (RAOP): a decoder for compressed and uncompressed frames, and an encoder
// that produces spec-valid uncompressed frames.
package alac

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Config is the ALAC decoder configuration ("magic cookie"). AirPlay senders
// transmit it in the SDP fmtp attribute; MP4 files store it in the 'alac'
// atom. Field names follow Apple's ALACSpecificConfig.
type Config struct {
	FrameLength       uint32 // samples per channel in a full frame
	CompatibleVersion uint8
	BitDepth          uint8
	PB                uint8 // rice history multiplier
	MB                uint8 // rice initial history
	KB                uint8 // rice k modifier
	NumChannels       uint8
	MaxRun            uint16
	MaxFrameBytes     uint32
	AvgBitRate        uint32
	SampleRate        uint32
}

// DefaultConfig returns the configuration AirPlay uses for CD-quality audio:
// 352 samples per frame, 16-bit stereo at 44.1 kHz.
func DefaultConfig() Config {
	return Config{
		FrameLength: 352,
		BitDepth:    16,
		PB:          40,
		MB:          10,
		KB:          14,
		NumChannels: 2,
		MaxRun:      255,
		SampleRate:  44100,
	}
}

const cookieSize = 24

// ParseMagicCookie parses a 24-byte ALACSpecificConfig. A 36-byte cookie
// that still carries its 'alac' atom header, as found in MP4 files, is also
// accepted.
func ParseMagicCookie(b []byte) (Config, error) {
	if len(b) == cookieSize+12 && string(b[4:8]) == "alac" {
		b = b[12:]
	}
	if len(b) < cookieSize {
		return Config{}, fmt.Errorf("alac: magic cookie is %d bytes, need %d", len(b), cookieSize)
	}
	c := Config{
		FrameLength:       binary.BigEndian.Uint32(b[0:4]),
		CompatibleVersion: b[4],
		BitDepth:          b[5],
		PB:                b[6],
		MB:                b[7],
		KB:                b[8],
		NumChannels:       b[9],
		MaxRun:            binary.BigEndian.Uint16(b[10:12]),
		MaxFrameBytes:     binary.BigEndian.Uint32(b[12:16]),
		AvgBitRate:        binary.BigEndian.Uint32(b[16:20]),
		SampleRate:        binary.BigEndian.Uint32(b[20:24]),
	}
	return c, c.Validate()
}

// MagicCookie encodes c as a 24-byte ALACSpecificConfig.
func (c Config) MagicCookie() []byte {
	b := make([]byte, cookieSize)
	binary.BigEndian.PutUint32(b[0:4], c.FrameLength)
	b[4] = c.CompatibleVersion
	b[5] = c.BitDepth
	b[6] = c.PB
	b[7] = c.MB
	b[8] = c.KB
	b[9] = c.NumChannels
	binary.BigEndian.PutUint16(b[10:12], c.MaxRun)
	binary.BigEndian.PutUint32(b[12:16], c.MaxFrameBytes)
	binary.BigEndian.PutUint32(b[16:20], c.AvgBitRate)
	binary.BigEndian.PutUint32(b[20:24], c.SampleRate)
	return b
}

// ParseFmtp parses the value of an SDP "a=fmtp:" attribute, for example
// "96 352 0 16 40 10 14 2 255 0 0 44100". The leading payload type is
// required and ignored.
func ParseFmtp(s string) (Config, error) {
	fields := strings.Fields(s)
	if len(fields) != 12 {
		return Config{}, fmt.Errorf("alac: fmtp has %d fields, want 12", len(fields))
	}
	v := make([]uint64, len(fields))
	for i, f := range fields {
		n, err := strconv.ParseUint(f, 10, 32)
		if err != nil {
			return Config{}, fmt.Errorf("alac: fmtp field %d: %w", i, err)
		}
		v[i] = n
	}
	c := Config{
		FrameLength:       uint32(v[1]),
		CompatibleVersion: uint8(v[2]),
		BitDepth:          uint8(v[3]),
		PB:                uint8(v[4]),
		MB:                uint8(v[5]),
		KB:                uint8(v[6]),
		NumChannels:       uint8(v[7]),
		MaxRun:            uint16(v[8]),
		MaxFrameBytes:     uint32(v[9]),
		AvgBitRate:        uint32(v[10]),
		SampleRate:        uint32(v[11]),
	}
	return c, c.Validate()
}

// Fmtp formats c as an SDP fmtp value with payload type 96.
func (c Config) Fmtp() string {
	return fmt.Sprintf("96 %d %d %d %d %d %d %d %d %d %d %d",
		c.FrameLength, c.CompatibleVersion, c.BitDepth, c.PB, c.MB, c.KB,
		c.NumChannels, c.MaxRun, c.MaxFrameBytes, c.AvgBitRate, c.SampleRate)
}

// maxFrameLength bounds per-frame allocations when a sender advertises a
// nonsensical frame length. Apple's encoder never exceeds 4096.
const maxFrameLength = 1 << 16

// Validate reports whether c describes a stream this package can decode.
func (c Config) Validate() error {
	switch {
	case c.FrameLength == 0 || c.FrameLength > maxFrameLength:
		return fmt.Errorf("alac: unsupported frame length %d", c.FrameLength)
	case c.BitDepth != 16 && c.BitDepth != 24:
		return fmt.Errorf("alac: unsupported bit depth %d", c.BitDepth)
	case c.NumChannels != 1 && c.NumChannels != 2:
		return fmt.Errorf("alac: unsupported channel count %d", c.NumChannels)
	case c.KB == 0 || c.KB > 31:
		return fmt.Errorf("alac: invalid rice k modifier %d", c.KB)
	case c.SampleRate == 0:
		return errors.New("alac: sample rate is zero")
	}
	return nil
}
