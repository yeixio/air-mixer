package airplay

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
	"unsafe"

	"github.com/yeixio/air-mixer/internal/audio"
	"github.com/yeixio/air-mixer/internal/audio/alac"
	"github.com/yeixio/air-mixer/internal/protocols"
	"github.com/yeixio/air-mixer/apserver"
)

// Server implements ProtocolServer for AirPlay
type Server struct {
	id          string
	name        string
	address     string
	iface       string
	audioStream chan protocols.AudioPacket
	running     bool
	mu          sync.RWMutex
	decoder     audio.AudioDecoder
	udpConn     *net.UDPConn
	tcpListener net.Listener // Store TCP listener so we can close it
	stopChan    chan struct{}
	serverDone  chan struct{} // Channel to signal when server goroutine is done
	
	// RTP frame buffering
	frameBuffer      []byte    // Buffer for accumulating ALAC frame data across RTP packets
	lastPacketTime   time.Time // Time of last packet received
	lastRTPTimestamp uint32    // Last RTP timestamp to detect frame boundaries
	frameTimeout     time.Duration // Timeout for frame completion
	maxFrameBytes    uint32    // Maximum frame size from ALAC magic cookie
}

// NewServer creates a new AirPlay server
func NewServer(id, name, address, iface string) *Server {
	return &Server{
		id:            id,
		name:          name,
		address:       address,
		iface:         iface,
		audioStream:   make(chan protocols.AudioPacket, 100),
		stopChan:      make(chan struct{}),
		serverDone:    make(chan struct{}),
		frameTimeout:  500 * time.Millisecond, // 500ms timeout for frame completion (ALAC frames can be large)
	}
}

// GetAudioStreamChannel returns the audio stream channel for direct access
func (s *Server) GetAudioStreamChannel() chan<- protocols.AudioPacket {
	return s.audioStream
}

// getMainInterface auto-detects the main network interface
// When multiple interfaces are found, it picks the first non-virtual interface,
// or falls back to the first interface if all are virtual
func getMainInterface() (string, error) {
	var candidates []string
	var virtualCandidates []string

	interfaces, err := net.Interfaces()
	if err != nil {
		return "", fmt.Errorf("failed to get interfaces: %w", err)
	}

	for _, i := range interfaces {
		// Skip loopback interfaces
		if i.Flags&net.FlagLoopback != 0 {
			continue
		}

		// Check if interface has IPv4 address
		hasIPv4 := false
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addrs {
			if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if ipnet.IP.To4() != nil {
					hasIPv4 = true
					break
				}
			}
		}

		if !hasIPv4 {
			continue
		}

		// Prefer non-virtual interfaces (virtual interfaces often have "veth", "docker", "br-" prefixes)
		// or are marked as up but not running
		isVirtual := false
		ifaceName := i.Name
		if len(ifaceName) >= 4 {
			prefix := ifaceName[:4]
			if prefix == "veth" || prefix == "br-" || prefix == "dock" {
				isVirtual = true
			}
		}
		if len(ifaceName) >= 3 && ifaceName[:3] == "vmw" {
			isVirtual = true // VMware interfaces
		}
		if i.Flags&net.FlagUp != 0 && i.Flags&net.FlagRunning == 0 {
			isVirtual = true // Up but not running often indicates virtual
		}

		if isVirtual {
			virtualCandidates = append(virtualCandidates, ifaceName)
		} else {
			candidates = append(candidates, ifaceName)
		}
	}

	// Prefer non-virtual interfaces
	if len(candidates) > 0 {
		return candidates[0], nil
	}

	// Fall back to virtual interfaces if no non-virtual found
	if len(virtualCandidates) > 0 {
		return virtualCandidates[0], nil
	}

	return "", errors.New("no network interface with IPv4 address found")
}

// Start starts the AirPlay server
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("server is already running")
	}

	// Resolve interface name if "auto" or empty
	ifaceName := s.iface
	if ifaceName == "" || ifaceName == "auto" {
		var err error
		ifaceName, err = getMainInterface()
		if err != nil {
			return fmt.Errorf("failed to auto-detect interface: %w", err)
		}
		log.Printf("AirPlay server %s: auto-detected interface: %s", s.id, ifaceName)
	}

	// Create ANNOUNCE handler
	handler := func(sessionID string, fmtp []int, aesiv, aeskey []byte) error {
		return s.SetDecoder(fmtp, aesiv, aeskey)
	}

	// Start AirPlay server and capture the listener
	var err error
	s.tcpListener, err = apserver.ServeAirPlayWithHandler(s.name, ifaceName, s.address, handler)
	if err != nil {
		return fmt.Errorf("failed to start AirPlay server: %w", err)
	}

	// Monitor server in a goroutine
	go func() {
		defer close(s.serverDone)
		// Wait for stop signal
		<-s.stopChan
		log.Printf("AirPlay server %s received stop signal", s.id)
	}()

	s.running = true
	log.Printf("AirPlay server %s (%s) started on %s using interface %s", s.id, s.name, s.address, ifaceName)
	return nil
}

// Stop stops the AirPlay server
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

	log.Printf("Stopping AirPlay server %s", s.id)
	s.running = false

	// Close TCP listener to stop Accept() loop and free the port
	if s.tcpListener != nil {
		if err := s.tcpListener.Close(); err != nil {
			log.Printf("Error closing TCP listener for server %s: %v", s.id, err)
		} else {
			log.Printf("TCP listener closed for server %s", s.id)
		}
		s.tcpListener = nil
	}

	// Close UDP connection
	if s.udpConn != nil {
		s.udpConn.Close()
		s.udpConn = nil
	}

	// Close decoder
	if s.decoder != nil {
		s.decoder.Close()
		s.decoder = nil
	}

	// Signal stop to goroutines
	close(s.stopChan)

	// Wait for server goroutine to finish (with timeout)
	select {
	case <-s.serverDone:
		log.Printf("AirPlay server %s goroutine stopped", s.id)
	case <-time.After(2 * time.Second):
		log.Printf("Warning: AirPlay server %s goroutine did not stop within timeout", s.id)
	}

	// Don't close audioStream here - it might still be in use
	// The channel will be garbage collected when server is destroyed

	log.Printf("AirPlay server %s stopped", s.id)
	return nil
}

// AudioStream returns the audio stream channel
func (s *Server) AudioStream() <-chan protocols.AudioPacket {
	return s.audioStream
}

// Name returns the server name
func (s *Server) Name() string {
	return s.name
}

// ID returns the server ID
func (s *Server) ID() string {
	return s.id
}

// Protocol returns the protocol name
func (s *Server) Protocol() string {
	return "airplay"
}

// IsRunning returns whether the server is running
func (s *Server) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// SetDecoder sets the audio decoder for this server
// This is called when we receive the fmtp header with ALAC parameters
func (s *Server) SetDecoder(fmtp []int, aesiv, aeskey []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Parse magic cookie to get MaxFrameBytes for frame boundary detection
	cookie, err := alac.ParseMagicCookieFromFmtp(fmtp)
	if err != nil {
		return fmt.Errorf("failed to parse magic cookie: %w", err)
	}
	
	// Store MaxFrameBytes for frame boundary detection
	s.maxFrameBytes = cookie.MaxFrameBytes
	if s.maxFrameBytes == 0 {
		// Fallback if MaxFrameBytes is 0 (shouldn't happen, but be safe)
		s.maxFrameBytes = 100 * 1024 // 100KB default
	}
	log.Printf("AirPlay server %s: ALAC MaxFrameBytes: %d bytes", s.id, s.maxFrameBytes)

	// Create pure Go ALAC decoder
	decoder, err := audio.NewPureALACDecoderFromFmtp(fmtp)
	if err != nil {
		return fmt.Errorf("failed to create ALAC decoder: %w", err)
	}

	s.decoder = decoder

	// Start UDP listener for RTP packets
	go s.startUDPListener(aesiv, aeskey, fmtp)

	return nil
}

// startUDPListener listens for RTP audio packets on UDP port 6000
func (s *Server) startUDPListener(aesiv, aeskey []byte, fmtp []int) {
	udpaddr, err := net.ResolveUDPAddr("udp", ":6000")
	if err != nil {
		log.Printf("AirPlay server %s: failed to resolve UDP address: %v", s.id, err)
		return
	}

	udpconn, err := net.ListenUDP("udp", udpaddr)
	if err != nil {
		log.Printf("AirPlay server %s: failed to listen on UDP: %v", s.id, err)
		return
	}
	defer udpconn.Close()

	s.mu.Lock()
	s.udpConn = udpconn
	s.mu.Unlock()

	log.Printf("AirPlay server %s: listening for RTP packets on UDP :6000", s.id)

	buf := make([]byte, 1024*16)
	for {
		select {
		case <-s.stopChan:
			return
		default:
			udpconn.SetReadDeadline(time.Now().Add(1 * time.Second))
			n, _, err := udpconn.ReadFromUDP(buf)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				log.Printf("AirPlay server %s: UDP read error: %v", s.id, err)
				return
			}
			// Only log packet reception at debug level (too verbose for normal operation)
			// log.Printf("AirPlay server %s: received UDP packet from %s (%d bytes)", s.id, addr, n)

			if n < 12 {
				continue // Not a valid RTP packet
			}

			// Parse RTP header
			// RTP header format (first 12 bytes):
			// Byte 1: V(2) P X CC(4) - Version, Padding, Extension, CSRC count
			// Byte 2: M PT(7) - Marker bit, Payload Type
			// Bytes 3-4: Sequence number
			// Bytes 5-8: Timestamp
			// Bytes 9-12: SSRC
			rtpHeader := buf[0:12]
			markerBit := (rtpHeader[1] & 0x80) != 0 // M bit is bit 7 of byte 1 (0x80 = 10000000)
			rtpTimestamp := uint32(rtpHeader[4])<<24 | uint32(rtpHeader[5])<<16 | uint32(rtpHeader[6])<<8 | uint32(rtpHeader[7])
			
			// Extract audio data (skip 12-byte RTP header)
			encryptedAudio := buf[12:n]

			// Decrypt if we have keys
			var audioData []byte
			if len(aeskey) > 0 && len(aesiv) > 0 {
				// CBC decryption - decrypt all full blocks
				block, err := aes.NewCipher(aeskey)
				if err != nil {
					log.Printf("AirPlay server %s: failed to create cipher: %v", s.id, err)
					continue
				}

				audioData = make([]byte, len(encryptedAudio))
				copy(audioData, encryptedAudio)
				
				AESDec := cipher.NewCBCDecrypter(block, aesiv)
				blockSize := aes.BlockSize
				fullBlocks := len(audioData) / blockSize
				if fullBlocks > 0 {
					AESDec.CryptBlocks(audioData[:fullBlocks*blockSize], audioData[:fullBlocks*blockSize])
				}
				// Keep remaining bytes (might be padding or need to be accumulated with next packet)
			} else {
				audioData = encryptedAudio
				log.Printf("AirPlay server %s: WARNING - no encryption keys, using raw audio: %d bytes", s.id, len(audioData))
			}

			now := time.Now()
			
			// Accumulate frame data across RTP packets
			// Use RTP timestamp changes to detect frame boundaries (most reliable)
			// Fall back to marker bit and timeout if needed
			s.mu.Lock()
			prevBufferSize := len(s.frameBuffer)
			
			// Check if RTP timestamp changed significantly (indicates new frame)
			// RTP timestamps increment by samples per packet, so a large jump indicates a new frame
			timestampChanged := false
			if s.lastRTPTimestamp != 0 && prevBufferSize > 0 {
				// Calculate timestamp difference (handle wrap-around)
				var timestampDiff uint32
				if rtpTimestamp >= s.lastRTPTimestamp {
					timestampDiff = rtpTimestamp - s.lastRTPTimestamp
				} else {
					// Timestamp wrapped around (32-bit unsigned)
					timestampDiff = (^uint32(0) - s.lastRTPTimestamp) + rtpTimestamp + 1
				}
				// At 44.1kHz, a typical packet is ~352 samples (8ms)
				// Within a frame, timestamps increment by ~352 samples per packet
				// A new frame starts when timestamp jumps by much more (typically thousands of samples)
				// Use a high threshold to avoid false positives from normal packet-to-packet increments
				if timestampDiff > 5000 {
					timestampChanged = true
					log.Printf("AirPlay server %s: detected new frame by RTP timestamp change (diff: %d samples, prev buffer: %d bytes)", s.id, timestampDiff, prevBufferSize)
				}
			}
			
			// If timestamp changed, complete the previous frame before starting a new one
			if timestampChanged && prevBufferSize > 0 {
				frameData := make([]byte, len(s.frameBuffer))
				copy(frameData, s.frameBuffer)
				s.frameBuffer = s.frameBuffer[:0] // Clear buffer for new frame
				s.lastRTPTimestamp = rtpTimestamp
				s.lastPacketTime = now
				s.mu.Unlock()
				
				// Decode the completed frame
				if s.decoder != nil && len(frameData) > 0 {
					s.decodeAndSendFrame(frameData)
				}
				
				// Start accumulating new frame
				s.mu.Lock()
				s.frameBuffer = append(s.frameBuffer, audioData...)
				s.lastRTPTimestamp = rtpTimestamp
				s.lastPacketTime = now
				s.mu.Unlock()
				continue
			}
			
			// Continue accumulating current frame
			s.frameBuffer = append(s.frameBuffer, audioData...)
			timeSinceLastPacket := now.Sub(s.lastPacketTime)
			s.lastPacketTime = now
			s.lastRTPTimestamp = rtpTimestamp
			
			// Check if frame is complete:
			// 1. Marker bit is set (explicit frame end)
			// 2. Buffer reached MaxFrameBytes (from ALAC magic cookie) - most reliable
			// 3. Timeout since last packet (fallback)
			// 4. Safety limit (prevent unbounded growth)
			frameComplete := markerBit
			maxFrameSize := 500 * 1024 // 500KB safety limit
			
			if !frameComplete && prevBufferSize > 0 {
				// Check if we've reached the expected frame size from magic cookie
				if s.maxFrameBytes > 0 && uint32(len(s.frameBuffer)) >= s.maxFrameBytes {
					frameComplete = true
					log.Printf("AirPlay server %s: frame completion by MaxFrameBytes (buffer: %d bytes >= %d bytes)", s.id, len(s.frameBuffer), s.maxFrameBytes)
				} else if timeSinceLastPacket > s.frameTimeout {
					frameComplete = true
					estimatedPackets := len(s.frameBuffer) / 32
					log.Printf("AirPlay server %s: frame completion by timeout (%v since last packet, buffer: %d bytes, ~%d packets)", s.id, timeSinceLastPacket, len(s.frameBuffer), estimatedPackets)
				} else if len(s.frameBuffer) > maxFrameSize {
					// Safety: force completion if buffer gets too large
					frameComplete = true
					log.Printf("AirPlay server %s: frame completion by size limit (buffer: %d bytes exceeded %d bytes limit)", s.id, len(s.frameBuffer), maxFrameSize)
				}
			}
			
			frameData := make([]byte, len(s.frameBuffer))
			copy(frameData, s.frameBuffer)
			if frameComplete {
				s.frameBuffer = s.frameBuffer[:0] // Clear buffer for next frame
				s.lastPacketTime = time.Time{} // Reset timer
				s.lastRTPTimestamp = 0 // Reset timestamp
			}
			s.mu.Unlock()

			// Only decode when we have a complete frame
			if !frameComplete {
				// Only log periodically to avoid spam (every 10KB)
				if len(frameData) > 0 && len(frameData)%10240 == 0 && len(frameData) != prevBufferSize {
					log.Printf("AirPlay server %s: accumulating frame, buffer: %d bytes (~%d packets), waiting for frame boundary...", s.id, len(frameData), len(frameData)/32)
				}
				continue // Wait for more packets
			}

			// Decode the completed frame
			if s.decoder != nil && len(frameData) > 0 {
				s.decodeAndSendFrame(frameData)
			}
		}
	}
}

// decodeAndSendFrame decodes an ALAC frame and sends it to the audio stream
func (s *Server) decodeAndSendFrame(frameData []byte) {
	// Check if we have enough data for a reasonable frame
	// ALAC frames are typically at least a few hundred bytes
	if len(frameData) < 100 {
		log.Printf("AirPlay server %s: skipping very small frame (%d bytes) - likely incomplete", s.id, len(frameData))
		return
	}
	
	log.Printf("AirPlay server %s: decoding frame (%d bytes)...", s.id, len(frameData))
	
	// Decode ALAC to PCM samples
	samples, err := s.decoder.Decode(frameData)
	if err != nil {
		log.Printf("AirPlay server %s: decode error: %v (frame size: %d bytes)", s.id, err, len(frameData))
		// Clear buffer on decode error to avoid accumulating bad data
		s.mu.Lock()
		s.frameBuffer = s.frameBuffer[:0]
		s.mu.Unlock()
		return
	}

	// Convert float32 samples to bytes for the packet
	// We'll send as PCM format since it's already decoded
	pcmData := make([]byte, len(samples)*4) // float32 = 4 bytes
	for i, sample := range samples {
		// Convert float32 to bytes (little-endian)
		bits := uint32(*(*uint32)(unsafe.Pointer(&sample)))
		pcmData[i*4] = byte(bits)
		pcmData[i*4+1] = byte(bits >> 8)
		pcmData[i*4+2] = byte(bits >> 16)
		pcmData[i*4+3] = byte(bits >> 24)
	}

	// Create audio packet with decoded PCM data
	packet := protocols.AudioPacket{
		Data:       pcmData,
		SampleRate: s.decoder.SampleRate(),
		Channels:   s.decoder.Channels(),
		Format:     audio.FormatPCM, // Mark as PCM since it's decoded
		Timestamp:  time.Now().UnixNano(),
	}

	// Send to stream (non-blocking)
	select {
	case s.audioStream <- packet:
		// Successfully sent
	default:
		log.Printf("AirPlay server %s: WARNING - audio stream buffer full, dropping packet", s.id)
	}
}

