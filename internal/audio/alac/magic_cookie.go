package alac

import (
	"encoding/binary"
	"fmt"
)

// MagicCookie represents the ALAC magic cookie structure
// This is extracted from the fmtp header in AirPlay RTSP
type MagicCookie struct {
	FrameLength      uint32
	CompatibleVersion uint8
	BitDepth         uint8
	PB               uint8  // Predictor parameter
	MB               uint8  // Multiplier parameter
	KB               uint8  // K parameter
	NumChannels      uint8
	MaxRun           uint16
	MaxFrameBytes    uint32
	AvgBitRate       uint32
	SampleRate       uint32
}

// ParseMagicCookie parses ALAC magic cookie from bytes
// The magic cookie is 24 bytes in big-endian format
func ParseMagicCookie(data []byte) (*MagicCookie, error) {
	if len(data) < 24 {
		return nil, fmt.Errorf("magic cookie too short: need 24 bytes, got %d", len(data))
	}

	cookie := &MagicCookie{
		FrameLength:       binary.BigEndian.Uint32(data[0:4]),
		CompatibleVersion: data[4],
		BitDepth:         data[5],
		PB:               data[6],
		MB:               data[7],
		KB:               data[8],
		NumChannels:      data[9],
		MaxRun:           binary.BigEndian.Uint16(data[10:12]),
		MaxFrameBytes:    binary.BigEndian.Uint32(data[12:16]),
		AvgBitRate:       binary.BigEndian.Uint32(data[16:20]),
		SampleRate:       binary.BigEndian.Uint32(data[20:24]),
	}

	return cookie, nil
}

// ParseMagicCookieFromFmtp parses magic cookie from fmtp array
// fmtp is the array of integers from the RTSP ANNOUNCE message
func ParseMagicCookieFromFmtp(fmtp []int) (*MagicCookie, error) {
	if len(fmtp) < 12 {
		return nil, fmt.Errorf("fmtp array too short: need at least 12 elements, got %d", len(fmtp))
	}

	// Convert fmtp array to bytes
	data := make([]byte, 24)
	binary.BigEndian.PutUint32(data[0:4], uint32(fmtp[1]))
	data[4] = byte(fmtp[2])
	data[5] = byte(fmtp[3])
	data[6] = byte(fmtp[4])
	data[7] = byte(fmtp[5])
	data[8] = byte(fmtp[6])
	data[9] = byte(fmtp[7])
	binary.BigEndian.PutUint16(data[10:12], uint16(fmtp[8]))
	binary.BigEndian.PutUint32(data[12:16], uint32(fmtp[9]))
	binary.BigEndian.PutUint32(data[16:20], uint32(fmtp[10]))
	binary.BigEndian.PutUint32(data[20:24], uint32(fmtp[11]))

	return ParseMagicCookie(data)
}

