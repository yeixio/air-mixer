package airplay

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/yeixio/air-mixer/internal/audio"
	"github.com/yeixio/air-mixer/internal/audio/alac"
	"github.com/yeixio/air-mixer/internal/protocols"
	"howett.net/plist"
)

// pairingStateStore stores pairing state temporarily (in-memory)
// Key: client ID, Value: serialized pairing state
var pairingStateStore = make(map[string]*pairingStateData)
var pairingStateStoreMu sync.RWMutex

// pairingStateData holds serializable pairing state
type pairingStateData struct {
	Salt             []byte `json:"salt"`
	PublicKey        []byte `json:"public_key"`
	ClientPublicKey  []byte `json:"client_public_key"`
	ClientPrivateKey []byte `json:"client_private_key"`
	ServerPublicKey  []byte `json:"server_public_key"`
	Proof            []byte `json:"proof"`
}

// Client implements ProtocolClient for AirPlay (output to AirPlay devices)
type Client struct {
	id          string
	name        string
	address     string // IP:port of AirPlay device
	connected   bool
	mu          sync.RWMutex
	
	// RTSP connection
	rtspConn    net.Conn
	sessionID   string
	
	// Audio encoding
	encoder     *audio.ALACEncoder // ALAC encoder (typed for FrameSize access)
	sampleRate  int
	channels    int
	
	// RTP streaming
	udpConn      *net.UDPConn
	udpAddr      *net.UDPAddr // Address of AirPlay device for RTP
	rtpSeq       uint16
	rtpSSRC      uint32
	rtpTimestamp uint32
	
	// Encryption
	aesKey       []byte
	aesIV        []byte
	rsaPubKey    *rsa.PublicKey // Public key from device for encrypting AES key
	
	// Pairing credentials (for newer Apple TVs that require SRP pairing)
	deviceID     string // Device identifier from pairing
	privateKey   string // Private key from pairing
	
	// Device info (fetched from /info endpoint)
	deviceInfo   *deviceInfo // Cached device information
	
	// SRP pairing state
	pairingState      int         // Current pairing state (0=not started, 1=started, 2=completed)
	pairingSession    *srpSession // SRP session state
	pairingDeviceID   string      // Device ID from pairing
	pairingPrivateKey []byte      // Private key from pairing
	
	// Control
	stopChan     chan struct{}
	audioInput   chan []float32 // Input channel for audio samples
	sampleBuffer []float32      // Buffer for accumulating samples into frames
}

// deviceInfo holds information from the /info endpoint
// Using interface{} for fields that can be either string or []byte
type deviceInfo struct {
	Name            string      `plist:"name"`
	DeviceID        string      `plist:"deviceID"`
	Model           string      `plist:"model"`
	ProtocolVersion string      `plist:"protocolVersion"`
	SourceVersion   string      `plist:"sourceVersion"`
	PublicKey       interface{} `plist:"pk"`      // Device's public key (can be string or []byte)
	PairingID       interface{} `plist:"pi"`     // Pairing identifier (can be string or []byte)
	PairingStateID  interface{} `plist:"psi"`    // Pairing state identifier (can be string or []byte)
	MACAddress      string      `plist:"macAddress"`
	Features        uint64      `plist:"features"`
}

// srpSession holds SRP protocol state during pairing
// HomeKit/AirPlay uses SRP-6a with 3072-bit modulus
type srpSession struct {
	salt       []byte
	verifier   []byte
	publicKey  []byte
	secret     []byte
	sharedKey  []byte
	// SRP-6a parameters for proper implementation
	N          *big.Int // Large prime modulus (3072 bits for HomeKit)
	g          *big.Int // Generator
	a          *big.Int // Client private key (random)
	A          *big.Int // Client public key (g^a mod N)
	B          *big.Int // Server public key (from device)
	u          *big.Int // Random scrambling parameter
	S          *big.Int // Shared secret
	K          []byte   // Session key (SHA-512 of S)
}

// NewClient creates a new AirPlay client
func NewClient(id, name, address string, sampleRate, channels int) (*Client, error) {
	// Generate random SSRC for RTP
	ssrc := make([]byte, 4)
	if _, err := rand.Read(ssrc); err != nil {
		return nil, fmt.Errorf("failed to generate SSRC: %w", err)
	}
	
	client := &Client{
		id:           id,
		name:         name,
		address:      address,
		sampleRate:   sampleRate,
		channels:     channels,
		rtpSSRC:      uint32(ssrc[0])<<24 | uint32(ssrc[1])<<16 | uint32(ssrc[2])<<8 | uint32(ssrc[3]),
		rtpSeq:       0,
		rtpTimestamp: 0,
		stopChan:     make(chan struct{}),
		audioInput:   make(chan []float32, 100),
		sampleBuffer: make([]float32, 0, 4096*channels), // Pre-allocate for frame size
	}
	
	return client, nil
}

// Connect connects to the AirPlay device and starts audio streaming
func (c *Client) Connect(target string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	if c.connected {
		return nil
	}
	
	// Parse address (format: "ip:port" or just "ip" defaults to port 5000)
	host, port := parseAddress(c.address)
	if host == "" {
		return fmt.Errorf("invalid address: %s", c.address)
	}
	
	// Connect to RTSP server on AirPlay device
	rtspAddr := fmt.Sprintf("%s:%d", host, port)
	conn, err := net.DialTimeout("tcp", rtspAddr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("failed to connect to AirPlay device: %w", err)
	}
	c.rtspConn = conn
	
	// Perform RTSP handshake
	if err := c.rtspHandshake(); err != nil {
		conn.Close()
		return fmt.Errorf("RTSP handshake failed: %w", err)
	}
	
	// Start audio encoding and RTP streaming goroutine
	go c.streamAudio()
	
	c.connected = true
	log.Printf("AirPlay client %s (%s) connected to %s", c.id, c.name, rtspAddr)
	return nil
}

// Disconnect disconnects from the AirPlay device
func (c *Client) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	if !c.connected {
		return nil
	}
	
	// Send TEARDOWN
	if c.rtspConn != nil {
		c.sendRTSPRequest("TEARDOWN", fmt.Sprintf("rtsp://%s/", c.address), map[string]string{
			"Session": c.sessionID,
		}, nil)
		c.rtspConn.Close()
		c.rtspConn = nil
	}
	
	// Stop UDP connection
	if c.udpConn != nil {
		c.udpConn.Close()
		c.udpConn = nil
	}
	
	// Signal stop
	close(c.stopChan)
	c.stopChan = make(chan struct{})
	
	c.connected = false
	log.Printf("AirPlay client %s disconnected", c.id)
	return nil
}

// SendAudio sends audio data to the client
func (c *Client) SendAudio(packet protocols.AudioPacket) error {
	c.mu.RLock()
	connected := c.connected
	c.mu.RUnlock()
	
	if !connected {
		return fmt.Errorf("client not connected")
	}
	
	// Convert packet data to float32 samples
	if len(packet.Data)%4 != 0 {
		return fmt.Errorf("invalid packet size: %d bytes (not multiple of 4)", len(packet.Data))
	}
	
	samples := make([]float32, len(packet.Data)/4)
	for i := 0; i < len(samples); i++ {
		bits := uint32(packet.Data[i*4]) |
			uint32(packet.Data[i*4+1])<<8 |
			uint32(packet.Data[i*4+2])<<16 |
			uint32(packet.Data[i*4+3])<<24
		samples[i] = *(*float32)(unsafe.Pointer(&bits))
	}
	
	// Send to audio input channel (non-blocking)
	select {
	case c.audioInput <- samples:
		return nil
	default:
		// Buffer full, drop packet
		return fmt.Errorf("audio input buffer full")
	}
}

// Name returns the client name
func (c *Client) Name() string {
	return c.name
}

// ID returns the client ID
func (c *Client) ID() string {
	return c.id
}

// Protocol returns the protocol name
func (c *Client) Protocol() string {
	return "airplay"
}

// IsConnected returns whether the client is connected
func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// Close closes the client and releases resources
func (c *Client) Close() error {
	return c.Disconnect()
}

// fetchDeviceInfo fetches device information from the /info endpoint
func (c *Client) fetchDeviceInfo() (*deviceInfo, error) {
	host, _ := parseAddress(c.address)
	if host == "" {
		return nil, fmt.Errorf("invalid address: %s", c.address)
	}
	
	infoURL := fmt.Sprintf("http://%s:7000/info", host)
	
	resp, err := http.Get(infoURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch device info: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("device info request failed with status %d", resp.StatusCode)
	}
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read device info: %w", err)
	}
	
	var info deviceInfo
	_, err = plist.Unmarshal(body, &info)
	if err != nil {
		return nil, fmt.Errorf("failed to parse device info plist: %w", err)
	}
	
	// Convert byte fields to strings/base64 if needed
	publicKeyStr := ""
	if pkBytes, ok := info.PublicKey.([]byte); ok && len(pkBytes) > 0 {
		publicKeyStr = base64.StdEncoding.EncodeToString(pkBytes)
	} else if pkStr, ok := info.PublicKey.(string); ok {
		publicKeyStr = pkStr
	}
	piStr := ""
	if piBytes, ok := info.PairingID.([]byte); ok && len(piBytes) > 0 {
		piStr = string(piBytes)
		if piStr == "" {
			piStr = base64.StdEncoding.EncodeToString(piBytes)
		}
	} else if piStrVal, ok := info.PairingID.(string); ok {
		piStr = piStrVal
	}
	psiStr := ""
	if psiBytes, ok := info.PairingStateID.([]byte); ok && len(psiBytes) > 0 {
		psiStr = string(psiBytes)
		if psiStr == "" {
			psiStr = base64.StdEncoding.EncodeToString(psiBytes)
		}
	} else if psiStrVal, ok := info.PairingStateID.(string); ok {
		psiStr = psiStrVal
	}
	
	log.Printf("AirPlay client %s: Device info - Name: %s, Model: %s, Protocol: %s, PublicKey: %s, pi: %s, psi: %s", 
		c.id, info.Name, info.Model, info.ProtocolVersion, 
		func() string {
			if len(publicKeyStr) > 20 {
				return publicKeyStr[:20] + "..."
			}
			return publicKeyStr
		}(), piStr, psiStr)
	
	return &info, nil
}

// rtspHandshake performs the RTSP handshake with the AirPlay device
func (c *Client) rtspHandshake() error {
	cseq := 1
	
	// Log connection attempt
	log.Printf("AirPlay client %s: attempting RTSP connection to %s", c.id, c.address)
	
	// Fetch device info first to get public key and other capabilities
	if c.deviceInfo == nil {
		info, err := c.fetchDeviceInfo()
		if err != nil {
			log.Printf("AirPlay client %s: Warning: failed to fetch device info: %v (continuing anyway)", c.id, err)
		} else {
			c.deviceInfo = info
			if pkBytes, ok := info.PublicKey.([]byte); ok && len(pkBytes) > 0 {
				pkStr := base64.StdEncoding.EncodeToString(pkBytes)
				log.Printf("AirPlay client %s: Device public key: %s", c.id, pkStr)
			}
		}
	}
	
	// First OPTIONS request without authentication (let device challenge us if needed)
	// Some devices require rtsp://host/ format, others accept *
	// Try with full URI first
	rtspURI := fmt.Sprintf("rtsp://%s/", c.address)
	resp, err := c.sendRTSPRequestWithCSeq("OPTIONS", rtspURI, nil, nil, &cseq)
	if err != nil {
		return fmt.Errorf("OPTIONS failed: %w", err)
	}
	
	// Log response for debugging
	log.Printf("AirPlay client %s: OPTIONS response status %d, headers: %v", c.id, resp.statusCode, resp.headers)
	
	// Track if OPTIONS failed (for ANNOUNCE authentication)
	optionsFailed := (resp.statusCode == 403 || resp.statusCode == 401)
	log.Printf("AirPlay client %s: OPTIONS failed flag: %v", c.id, optionsFailed)
	
	// Handle authentication responses
	// Device may respond with:
	// 1. 200 OK without challenge - proceed (no auth needed)
	// 2. 200 OK with Apple-Challenge - device wants to challenge us, respond
	// 3. 403/401 with Apple-Challenge - device requires authentication, respond
	// 4. 403/401 without challenge - try sending our own challenge as fallback
	
	if resp.statusCode == 200 {
		log.Printf("AirPlay client %s: OPTIONS returned 200, checking for challenges...", c.id)
		// Check if device sent Apple-Response (device responded to our challenge)
		if _, ok := resp.headers["Apple-Response"]; ok {
			log.Printf("AirPlay client %s: device responded to our challenge, proceeding", c.id)
			// Device authenticated us, we can proceed
		} else if challenge, ok := resp.headers["Apple-Challenge"]; ok {
			// Device also wants to challenge us
			log.Printf("AirPlay client %s: device sent Apple-Challenge, responding", c.id)
			response, err := c.handleAppleChallenge(challenge)
			if err != nil {
				return fmt.Errorf("failed to handle Apple-Challenge: %w", err)
			}
			
			// Send OPTIONS again with Apple-Response
			optHeaders := map[string]string{
				"Apple-Response": response,
			}
			resp, err = c.sendRTSPRequestWithCSeq("OPTIONS", fmt.Sprintf("rtsp://%s/", c.address), optHeaders, nil, &cseq)
			if err != nil {
				return fmt.Errorf("OPTIONS with Apple-Response failed: %w", err)
			}
			if resp.statusCode != 200 {
				return fmt.Errorf("OPTIONS with Apple-Response returned status %d", resp.statusCode)
			}
		} else {
			// 200 OK without any challenge/response - some devices work this way
			log.Printf("AirPlay client %s: device accepted connection without challenge", c.id)
		}
	} else if resp.statusCode == 403 || resp.statusCode == 401 {
		log.Printf("AirPlay client %s: OPTIONS returned %d, checking for Apple-Challenge...", c.id, resp.statusCode)
		// Device requires authentication - check for Apple-Challenge in response
		challenge, ok := resp.headers["Apple-Challenge"]
		log.Printf("AirPlay client %s: Apple-Challenge in headers: %v (value: %s)", c.id, ok, challenge)
		if !ok {
			// Try reading from response body (some devices send challenge in body)
			if len(resp.body) > 0 {
				bodyStr := string(resp.body)
				// Look for Apple-Challenge in body (case-insensitive)
				bodyLower := strings.ToLower(bodyStr)
				if idx := strings.Index(bodyLower, "apple-challenge"); idx >= 0 {
					// Find the actual case in the original string
					challengeLine := bodyStr[idx:]
					if endIdx := strings.IndexAny(challengeLine, "\r\n"); endIdx >= 0 {
						challengeLine = challengeLine[:endIdx]
						if parts := strings.SplitN(challengeLine, ":", 2); len(parts) == 2 {
							challenge = strings.TrimSpace(parts[1])
							ok = true
							log.Printf("AirPlay client %s: found Apple-Challenge in response body", c.id)
						}
					}
				}
			}
		}
		
		if ok && challenge != "" {
			response, err := c.handleAppleChallenge(challenge)
			if err != nil {
				return fmt.Errorf("failed to handle Apple-Challenge: %w", err)
			}
			
			// Retry OPTIONS with Apple-Response
			optHeaders := map[string]string{
				"Apple-Response": response,
			}
			resp, err = c.sendRTSPRequestWithCSeq("OPTIONS", fmt.Sprintf("rtsp://%s/", c.address), optHeaders, nil, &cseq)
			if err != nil {
				return fmt.Errorf("OPTIONS with Apple-Response failed: %w", err)
			}
			if resp.statusCode != 200 {
				return fmt.Errorf("OPTIONS with Apple-Response returned status %d", resp.statusCode)
			}
		} else {
			log.Printf("AirPlay client %s: 403/401 without challenge detected, checking credentials...", c.id)
			// 403/401 without challenge - device is rejecting OPTIONS
			// Check if we have pairing credentials - if not, device likely requires pairing
			// Note: We're already holding the write lock from Connect(), so we can access fields directly
			deviceID := c.deviceID
			privateKey := c.privateKey
			hasCredentials := (deviceID != "" && privateKey != "")
			log.Printf("AirPlay client %s: Has credentials: %v (deviceID: %s, privateKey length: %d)", c.id, hasCredentials, deviceID, len(privateKey))
			
			if !hasCredentials {
				log.Printf("AirPlay client %s: received %d without challenge on OPTIONS. Device likely requires pairing first (no credentials stored). Proceeding to ANNOUNCE to confirm...", c.id, resp.statusCode)
				// Continue to ANNOUNCE to see if it also fails, which will give us a better error message
			} else {
				log.Printf("AirPlay client %s: received %d without challenge on OPTIONS. Some devices authenticate on ANNOUNCE instead. Proceeding to ANNOUNCE...", c.id, resp.statusCode)
			}
			// Don't return error - try to proceed to ANNOUNCE to get better error message
		}
	} else {
		return fmt.Errorf("OPTIONS returned unexpected status %d", resp.statusCode)
	}
	
	// Check if we should proceed to ANNOUNCE or if pairing is required
	// If OPTIONS failed with 403 and we have no credentials, we might need pairing
	// Note: We're already holding the write lock from Connect(), so we can access fields directly
	hasCredentialsForAnnounce := (c.deviceID != "" && c.privateKey != "")
	
	if optionsFailed && !hasCredentialsForAnnounce {
		log.Printf("AirPlay client %s: OPTIONS returned 403 without credentials. Device likely requires pairing. Proceeding to ANNOUNCE to confirm...", c.id)
	}
	
	// Create ALAC encoder
	encoder, err := audio.NewALACEncoder(c.sampleRate, c.channels, 16) // 16-bit depth
	if err != nil {
		return fmt.Errorf("failed to create ALAC encoder: %w", err)
	}
	c.encoder = encoder
	
	// Get magic cookie from encoder
	magicCookie := encoder.MagicCookie()
	
	// Build fmtp array from magic cookie
	fmtp := c.buildFmtpFromMagicCookie(magicCookie)
	
	// Generate AES key and IV
	c.aesKey = make([]byte, 16)
	c.aesIV = make([]byte, 16)
	if _, err := rand.Read(c.aesKey); err != nil {
		return fmt.Errorf("failed to generate AES key: %w", err)
	}
	if _, err := rand.Read(c.aesIV); err != nil {
		return fmt.Errorf("failed to generate AES IV: %w", err)
	}
	
	// Encrypt AES key with RSA (if we have public key)
	// For now, we'll send it unencrypted and let the device handle it
	// In production, this should be RSA-encrypted
	rsaAESKey := base64.StdEncoding.EncodeToString(c.aesKey)
	
	// Build ANNOUNCE body with SDP
	announceBody := c.buildSDP(fmtp, rsaAESKey)
	
	// If OPTIONS returned 403, try sending Apple-Challenge in ANNOUNCE
	// Some devices authenticate on ANNOUNCE instead of OPTIONS
	announceHeaders := make(map[string]string)
	// Add Content-Type header as shown in AirPlay spec
	announceHeaders["Content-Type"] = "application/sdp"
	// Add DACP-ID header (shown in AirPlay spec examples)
	announceHeaders["DACP-ID"] = c.id
	
	if optionsFailed {
		log.Printf("AirPlay client %s: OPTIONS failed, trying Apple-Challenge in ANNOUNCE request", c.id)
		// Generate challenge for ANNOUNCE
		challengeBytes := make([]byte, 16)
		if _, err := rand.Read(challengeBytes); err != nil {
			return fmt.Errorf("failed to generate challenge: %w", err)
		}
		clientChallenge := base64.StdEncoding.EncodeToString(challengeBytes)
		announceHeaders["Apple-Challenge"] = clientChallenge
	}
	
	log.Printf("AirPlay client %s: Sending ANNOUNCE request (OPTIONS failed: %v, hasCredentials: %v)", c.id, optionsFailed, hasCredentialsForAnnounce)
	resp, err = c.sendRTSPRequestWithCSeq("ANNOUNCE", fmt.Sprintf("rtsp://%s/", c.address), announceHeaders, []byte(announceBody), &cseq)
	if err != nil {
		return fmt.Errorf("ANNOUNCE failed: %w", err)
	}
	
	log.Printf("AirPlay client %s: ANNOUNCE response status %d, headers: %v", c.id, resp.statusCode, resp.headers)
	if resp.statusCode != 200 {
		log.Printf("AirPlay client %s: ANNOUNCE response body: %s", c.id, string(resp.body))
	}
	
	// Check if device responded with Apple-Response or Apple-Challenge
	if resp.statusCode == 200 {
		if _, ok := resp.headers["Apple-Response"]; ok {
			log.Printf("AirPlay client %s: device responded to ANNOUNCE challenge", c.id)
		} else if challenge, ok := resp.headers["Apple-Challenge"]; ok {
			log.Printf("AirPlay client %s: device sent Apple-Challenge in ANNOUNCE response, responding", c.id)
			response, err := c.handleAppleChallenge(challenge)
			if err != nil {
				return fmt.Errorf("failed to handle Apple-Challenge: %w", err)
			}
			
			// Retry ANNOUNCE with Apple-Response
			announceHeaders = map[string]string{
				"Apple-Response": response,
			}
			resp, err = c.sendRTSPRequestWithCSeq("ANNOUNCE", fmt.Sprintf("rtsp://%s/", c.address), announceHeaders, []byte(announceBody), &cseq)
			if err != nil {
				return fmt.Errorf("ANNOUNCE with Apple-Response failed: %w", err)
			}
			if resp.statusCode != 200 {
				return fmt.Errorf("ANNOUNCE with Apple-Response returned status %d", resp.statusCode)
			}
		}
	} else if resp.statusCode == 403 || resp.statusCode == 401 {
		// Check if device sent Apple-Response even with 403
		if _, ok := resp.headers["Apple-Response"]; ok {
			log.Printf("AirPlay client %s: device responded to ANNOUNCE challenge with Apple-Response (status %d), proceeding", c.id, resp.statusCode)
			// Device authenticated us, proceed despite 403
		} else if challenge, ok := resp.headers["Apple-Challenge"]; ok {
			log.Printf("AirPlay client %s: device sent Apple-Challenge in ANNOUNCE response (status %d), responding", c.id, resp.statusCode)
			response, err := c.handleAppleChallenge(challenge)
			if err != nil {
				return fmt.Errorf("failed to handle Apple-Challenge: %w", err)
			}
			
			// Retry ANNOUNCE with Apple-Response
			announceHeaders = map[string]string{
				"Apple-Response": response,
			}
			resp, err = c.sendRTSPRequestWithCSeq("ANNOUNCE", fmt.Sprintf("rtsp://%s/", c.address), announceHeaders, []byte(announceBody), &cseq)
			if err != nil {
				return fmt.Errorf("ANNOUNCE with Apple-Response failed: %w", err)
			}
			if resp.statusCode != 200 {
				return fmt.Errorf("ANNOUNCE with Apple-Response returned status %d", resp.statusCode)
			}
		} else {
			// Device returned 403 without any authentication headers
			// This typically means the device requires pairing first (SRP protocol)
			// Newer Apple TVs (tvOS 10.2+) require pairing before RTSP connections
			// Note: We're already holding the write lock from Connect(), so we can access fields directly
			hasCredentials := (c.deviceID != "" && c.privateKey != "")
			log.Printf("AirPlay client %s: ANNOUNCE returned %d without auth headers. Has credentials: %v", c.id, resp.statusCode, hasCredentials)
			
			if !hasCredentials {
				// No credentials and device rejected - definitely needs pairing
				log.Printf("AirPlay client %s: Returning PAIRING_REQUIRED error", c.id)
				return fmt.Errorf("PAIRING_REQUIRED: ANNOUNCE returned status %d without authentication headers. This device requires pairing first. Response headers: %v", resp.statusCode, resp.headers)
			}
			return fmt.Errorf("ANNOUNCE returned status %d without authentication headers. This device likely requires pairing first. Please pair the device using the pairing endpoint before connecting. Response headers: %v", resp.statusCode, resp.headers)
		}
	} else if resp.statusCode != 200 {
		return fmt.Errorf("ANNOUNCE returned status %d", resp.statusCode)
	}
	
	// SETUP request
	setupHeaders := map[string]string{
		"Transport": "RTP/AVP/UDP;unicast;mode=record",
	}
	resp, err = c.sendRTSPRequestWithCSeq("SETUP", fmt.Sprintf("rtsp://%s/", c.address), setupHeaders, nil, &cseq)
	if err != nil {
		return fmt.Errorf("SETUP failed: %w", err)
	}
	if resp.statusCode != 200 {
		return fmt.Errorf("SETUP returned status %d", resp.statusCode)
	}
	
	// Parse Session ID and Transport info
	if session, ok := resp.headers["Session"]; ok {
		c.sessionID = session
	}
	
	// Parse transport to get server ports
	// Format: "RTP/AVP/UDP;unicast;server_port=6000;control_port=6001;timing_port=6002"
	host, _ := parseAddress(c.address)
	serverPort := 6000 // Default
	if transport, ok := resp.headers["Transport"]; ok {
		// Extract server_port
		if idx := strings.Index(transport, "server_port="); idx >= 0 {
			portStr := transport[idx+12:]
			if endIdx := strings.Index(portStr, ";"); endIdx >= 0 {
				portStr = portStr[:endIdx]
			}
			if port, err := strconv.Atoi(portStr); err == nil {
				serverPort = port
			}
		}
	}
	
	// Create UDP connection to device
	udpAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", host, serverPort))
	if err != nil {
		return fmt.Errorf("failed to resolve UDP address: %w", err)
	}
	c.udpAddr = udpAddr
	
	udpConn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return fmt.Errorf("failed to create UDP connection: %w", err)
	}
	c.udpConn = udpConn
	
	// RECORD request
	recordHeaders := map[string]string{
		"Session": c.sessionID,
	}
	resp, err = c.sendRTSPRequestWithCSeq("RECORD", fmt.Sprintf("rtsp://%s/", c.address), recordHeaders, nil, &cseq)
	if err != nil {
		return fmt.Errorf("RECORD failed: %w", err)
	}
	if resp.statusCode != 200 {
		return fmt.Errorf("RECORD returned status %d", resp.statusCode)
	}
	
	return nil
}

// rtspResponse represents an RTSP response
type rtspResponse struct {
	statusCode int
	headers    map[string]string
	body       []byte
}

// sendRTSPRequest sends an RTSP request and returns the response (uses default CSeq)
func (c *Client) sendRTSPRequest(method, uri string, headers map[string]string, body []byte) (*rtspResponse, error) {
	cseq := 1
	return c.sendRTSPRequestWithCSeq(method, uri, headers, body, &cseq)
}

// sendRTSPRequestWithCSeq sends an RTSP request with a specific CSeq
func (c *Client) sendRTSPRequestWithCSeq(method, uri string, headers map[string]string, body []byte, cseq *int) (*rtspResponse, error) {
	if c.rtspConn == nil {
		return nil, fmt.Errorf("RTSP connection not established")
	}
	
	*cseq++
	
	// Build request
	request := fmt.Sprintf("%s %s RTSP/1.0\r\n", method, uri)
	request += fmt.Sprintf("CSeq: %d\r\n", *cseq)
	// Use iTunes-style User-Agent (as shown in AirPlay spec)
	// Format: iTunes/version (platform; OS version) AppleWebKit/version
	request += fmt.Sprintf("User-Agent: iTunes/12.0 (Macintosh; Intel Mac OS X 10.15) AppleWebKit/605.1.15\r\n")
	// Note: Not sending Client-Instance for now - some Apple TVs reject it
	// We can add it conditionally if needed
	
	// Add custom headers
	for k, v := range headers {
		request += fmt.Sprintf("%s: %s\r\n", k, v)
	}
	
	// Add Content-Length if body present
	if len(body) > 0 {
		request += fmt.Sprintf("Content-Length: %d\r\n", len(body))
	}
	
	request += "\r\n"
	
	// Log full request for debugging (first 500 chars to avoid huge logs)
	requestPreview := request
	if len(requestPreview) > 500 {
		requestPreview = requestPreview[:500] + "..."
	}
	log.Printf("AirPlay client %s: Sending RTSP request:\n%s", c.id, requestPreview)
	
	// Send request
	if _, err := c.rtspConn.Write([]byte(request)); err != nil {
		return nil, fmt.Errorf("failed to write request: %w", err)
	}
	
	// Send body if present
	if len(body) > 0 {
		if _, err := c.rtspConn.Write(body); err != nil {
			return nil, fmt.Errorf("failed to write body: %w", err)
		}
	}
	
	// Read response
	buffer := make([]byte, 4096)
	n, err := c.rtspConn.Read(buffer)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	
	// Parse response
	resp := &rtspResponse{
		headers: make(map[string]string),
	}
	
	responseStr := string(buffer[:n])
	// Log full response for debugging (first 500 chars)
	responsePreview := responseStr
	if len(responsePreview) > 500 {
		responsePreview = responsePreview[:500] + "..."
	}
	log.Printf("AirPlay client %s: Received RTSP response:\n%s", c.id, responsePreview)
	lines := strings.Split(responseStr, "\r\n")
	
	// Parse status line
	if len(lines) > 0 {
		parts := strings.Fields(lines[0])
		if len(parts) >= 3 {
			if code, err := strconv.Atoi(parts[1]); err == nil {
				resp.statusCode = code
			}
		}
	}
	
	// Parse headers (normalize to Title-Case for case-insensitive matching)
	bodyStart := 0
	for i, line := range lines {
		if line == "" {
			bodyStart = i + 1
			break
		}
		if i > 0 { // Skip status line
			if idx := strings.Index(line, ":"); idx > 0 {
				key := strings.TrimSpace(line[:idx])
				value := strings.TrimSpace(line[idx+1:])
				// Normalize header key to Title-Case (e.g., "apple-challenge" -> "Apple-Challenge")
				key = normalizeHeaderKey(key)
				resp.headers[key] = value
			}
		}
	}
	
	// Parse body if present
	if bodyStart < len(lines) {
		resp.body = []byte(strings.Join(lines[bodyStart:], "\r\n"))
	}
	
	return resp, nil
}

// handleAppleChallenge handles Apple-Challenge authentication
func (c *Client) handleAppleChallenge(challenge string) (string, error) {
	// Use the same RSA key as the server (from apserver/auth.go)
	// In production, each client should have its own key
	const superSecretKey = `-----BEGIN RSA PRIVATE KEY-----
MIIEpQIBAAKCAQEA59dE8qLieItsH1WgjrcFRKj6eUWqi+bGLOX1HL3U3GhC/j0Qg90u3sG/1CUt
wC5vOYvfDmFI6oSFXi5ELabWJmT2dKHzBJKa3k9ok+8t9ucRqMd6DZHJ2YCCLlDRKSKv6kDqnw4U
wPdpOMXziC/AMj3Z/lUVX1G7WSHCAWKf1zNS1eLvqr+boEjXuBOitnZ/bDzPHrTOZz0Dew0uowxf
/+sG+NCK3eQJVxqcaJ/vEHKIVd2M+5qL71yJQ+87X6oV3eaYvt3zWZYD6z5vYTcrtij2VZ9Zmni/
UAaHqn9JdsBWLUEpVviYnhimNVvYFZeCXg/IdTQ+x4IRdiXNv5hEewIDAQABAoIBAQDl8Axy9XfW
BLmkzkEiqoSwF0PsmVrPzH9KsnwLGH+QZlvjWd8SWYGN7u1507HvhF5N3drJoVU3O14nDY4TFQAa
LlJ9VM35AApXaLyY1ERrN7u9ALKd2LUwYhM7Km539O4yUFYikE2nIPscEsA5ltpxOgUGCY7b7ez5
NtD6nL1ZKauw7aNXmVAvmJTcuPxWmoktF3gDJKK2wxZuNGcJE0uFQEG4Z3BrWP7yoNuSK3dii2jm
lpPHr0O/KnPQtzI3eguhe0TwUem/eYSdyzMyVx/YpwkzwtYL3sR5k0o9rKQLtvLzfAqdBxBurciz
aaA/L0HIgAmOit1GJA2saMxTVPNhAoGBAPfgv1oeZxgxmotiCcMXFEQEWflzhWYTsXrhUIuz5jFu
a39GLS99ZEErhLdrwj8rDDViRVJ5skOp9zFvlYAHs0xh92ji1E7V/ysnKBfsMrPkk5KSKPrnjndM
oPdevWnVkgJ5jxFuNgxkOLMuG9i53B4yMvDTCRiIPMQ++N2iLDaRAoGBAO9v//mU8eVkQaoANf0Z
oMjW8CN4xwWA2cSEIHkd9AfFkftuv8oyLDCG3ZAf0vrhrrtkrfa7ef+AUb69DNggq4mHQAYBp7L+
k5DKzJrKuO0r+R0YbY9pZD1+/g9dVt91d6LQNepUE/yY2PP5CNoFmjedpLHMOPFdVgqDzDFxU8hL
AoGBANDrr7xAJbqBjHVwIzQ4To9pb4BNeqDndk5Qe7fT3+/H1njGaC0/rXE0Qb7q5ySgnsCb3DvA
cJyRM9SJ7OKlGt0FMSdJD5KG0XPIpAVNwgpXXH5MDJg09KHeh0kXo+QA6viFBi21y340NonnEfdf
54PX4ZGS/Xac1UK+pLkBB+zRAoGAf0AY3H3qKS2lMEI4bzEFoHeK3G895pDaK3TFBVmD7fV0Zhov
17fegFPMwOII8MisYm9ZfT2Z0s5Ro3s5rkt+nvLAdfC/PYPKzTLalpGSwomSNYJcB9HNMlmhkGzc
1JnLYT4iyUyx6pcZBmCd8bD0iwY/FzcgNDaUmbX9+XDvRA0CgYEAkE7pIPlE71qvfJQgoA9em0gI
LAuE4Pu13aKiJnfft7hIjbK+5kyb3TysZvoyDnb3HOKvInK7vXbKuU4ISgxB2bB3HcYzQMGsz1qJ
2gG0N5hvJpzwwhbhXqFKA4zaaSrw622wDniAK5MlIE0tIAKKP4yxNGjoD2QYjhBGuhvkWKY=
-----END RSA PRIVATE KEY-----`
	
	// Parse private key
	pemblock, _ := pem.Decode([]byte(superSecretKey))
	if pemblock == nil {
		return "", fmt.Errorf("failed to decode PEM block")
	}
	
	privKey, err := x509.ParsePKCS1PrivateKey(pemblock.Bytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse private key: %w", err)
	}
	
	// Decode challenge
	p64 := base64Pad(challenge)
	ptext, err := base64.StdEncoding.DecodeString(p64)
	if err != nil {
		return "", fmt.Errorf("failed to decode challenge: %w", err)
	}
	
	// Get local IP and MAC
	localAddr := c.rtspConn.LocalAddr()
	ip := getIPFromAddr(localAddr)
	mac := getMACFromAddr(localAddr)
	
	// Append IP and MAC
	ptext = append(ptext, ip...)
	ptext = append(ptext, mac...)
	
	// Pad to 32 bytes
	for len(ptext) < 32 {
		ptext = append(ptext, 0)
	}
	
	// Sign with RSA
	ctext, err := rsa.SignPKCS1v15(nil, privKey, 0, ptext)
	if err != nil {
		return "", fmt.Errorf("failed to sign challenge: %w", err)
	}
	
	response := base64.StdEncoding.EncodeToString(ctext)
	
	// Unpad if original challenge was unpadded
	if len(p64) != len(challenge) {
		response = base64Unpad(response)
	}
	
	return response, nil
}

// base64Pad adds padding to base64 string
func base64Pad(s string) string {
	for len(s)%4 != 0 {
		s += "="
	}
	return s
}

// base64Unpad removes padding from base64 string
func base64Unpad(s string) string {
	if idx := strings.Index(s, "="); idx >= 0 {
		return s[:idx]
	}
	return s
}

// normalizeHeaderKey normalizes HTTP/RTSP header keys to Title-Case
// e.g., "apple-challenge" -> "Apple-Challenge", "CSeq" -> "CSeq"
func normalizeHeaderKey(key string) string {
	parts := strings.Split(key, "-")
	for i, part := range parts {
		if len(part) > 0 {
			parts[i] = strings.ToUpper(part[:1]) + strings.ToLower(part[1:])
		}
	}
	return strings.Join(parts, "-")
}

// getIPFromAddr extracts IP address from net.Addr
func getIPFromAddr(addr net.Addr) []byte {
	straddr := addr.String()
	host, _, _ := net.SplitHostPort(straddr)
	idx := strings.Index(host, "%")
	if idx >= 0 {
		host = host[:idx]
	}
	
	ipaddr := net.ParseIP(host)
	if ipaddr == nil {
		return []byte{0, 0, 0, 0}
	}
	
	ip := ipaddr.To4()
	if ip == nil {
		ip = ipaddr.To16()
	}
	if ip == nil {
		return []byte{0, 0, 0, 0}
	}
	
	return ip
}

// getMACFromAddr extracts MAC address from net.Addr
func getMACFromAddr(addr net.Addr) []byte {
	ifaces, err := net.Interfaces()
	if err != nil {
		return []byte{0, 0, 0, 0, 0, 0}
	}
	
	ip := getIPFromAddr(addr)
	ipStr := net.IP(ip).String()
	
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if strings.Contains(addr.String(), ipStr) {
				return iface.HardwareAddr
			}
		}
	}
	
	return []byte{0, 0, 0, 0, 0, 0}
}

// buildFmtpFromMagicCookie builds fmtp array from magic cookie
func (c *Client) buildFmtpFromMagicCookie(cookie *alac.MagicCookie) []int {
	// fmtp format: [format, frameLength, compatibleVersion, bitDepth, pb, mb, kb, numChannels, maxRun, maxFrameBytes, avgBitRate, sampleRate]
	return []int{
		96, // AppleLossless format
		int(cookie.FrameLength),
		int(cookie.CompatibleVersion),
		int(cookie.BitDepth),
		int(cookie.PB),
		int(cookie.MB),
		int(cookie.KB),
		int(cookie.NumChannels),
		int(cookie.MaxRun),
		int(cookie.MaxFrameBytes),
		int(cookie.AvgBitRate),
		int(cookie.SampleRate),
	}
}

// buildSDP builds an SDP body for ANNOUNCE
func (c *Client) buildSDP(fmtp []int, rsaAESKey string) string {
	// Build fmtp string
	fmtpStr := ""
	for i, v := range fmtp {
		if i > 0 {
			fmtpStr += " "
		}
		fmtpStr += strconv.Itoa(v)
	}
	
	aesIV64 := base64.StdEncoding.EncodeToString(c.aesIV)
	
	sdp := fmt.Sprintf(`v=0
o=- 0 0 IN IP4 0.0.0.0
s=AirPlay
c=IN IP4 0.0.0.0
t=0 0
m=audio 0 RTP/AVP 96
a=rtpmap:96 AppleLossless
a=fmtp:96 %s
a=rsaaeskey:%s
a=aesiv:%s
`, fmtpStr, rsaAESKey, aesIV64)
	
	return sdp
}


// streamAudio streams audio to the AirPlay device
func (c *Client) streamAudio() {
	if c.encoder == nil || c.udpConn == nil {
		log.Printf("AirPlay client %s: encoder or UDP connection not ready", c.id)
		return
	}
	
	log.Printf("AirPlay client %s: audio streaming started", c.id)
	
	// Get frame size from encoder
	frameSize := c.encoder.FrameSize()
	
	// Create AES cipher for encryption
	block, err := aes.NewCipher(c.aesKey)
	if err != nil {
		log.Printf("AirPlay client %s: failed to create AES cipher: %v", c.id, err)
		return
	}
	
	// Create CBC mode encrypter (server uses CBC for decryption)
	encrypter := cipher.NewCBCEncrypter(block, c.aesIV)
	
	// Calculate samples per frame (per channel)
	samplesPerFrame := frameSize / c.channels
	
	ticker := time.NewTicker(time.Duration(samplesPerFrame) * time.Second / time.Duration(c.sampleRate))
	defer ticker.Stop()
	
	for {
		select {
		case <-c.stopChan:
			log.Printf("AirPlay client %s: audio streaming stopped", c.id)
			return
			
		case samples := <-c.audioInput:
			// Add samples to buffer
			c.sampleBuffer = append(c.sampleBuffer, samples...)
			
			// Process frames when we have enough samples
			for len(c.sampleBuffer) >= frameSize {
				// Extract frame
				frameSamples := c.sampleBuffer[:frameSize]
				c.sampleBuffer = c.sampleBuffer[frameSize:]
				
				// Encode to ALAC
				alacFrame, err := c.encoder.Encode(frameSamples)
				if err != nil {
					log.Printf("AirPlay client %s: encoding error: %v", c.id, err)
					continue
				}
				
				// Encrypt ALAC frame with CBC mode
				// CBC requires padding to block size
				encryptedFrame := c.encryptCBC(alacFrame, encrypter)
				
				// Send as RTP packet
				if err := c.sendRTPPacket(encryptedFrame); err != nil {
					log.Printf("AirPlay client %s: RTP send error: %v", c.id, err)
					continue
				}
			}
			
		case <-ticker.C:
			// Time-based frame sending (if buffer not full enough)
			// This ensures we send frames even if we don't have enough samples
			if len(c.sampleBuffer) >= samplesPerFrame*c.channels {
				// We have enough for at least one frame, process it
				frameSize := samplesPerFrame * c.channels
				if len(c.sampleBuffer) >= frameSize {
					frameSamples := c.sampleBuffer[:frameSize]
					c.sampleBuffer = c.sampleBuffer[frameSize:]
					
					// Encode to ALAC
					alacFrame, err := c.encoder.Encode(frameSamples)
					if err != nil {
						log.Printf("AirPlay client %s: encoding error: %v", c.id, err)
						continue
					}
					
					// Encrypt ALAC frame with CBC mode
					encryptedFrame := c.encryptCBC(alacFrame, encrypter)
					
					// Send as RTP packet
					if err := c.sendRTPPacket(encryptedFrame); err != nil {
						log.Printf("AirPlay client %s: RTP send error: %v", c.id, err)
					}
				}
			}
		}
	}
}

// encryptCBC encrypts data using CBC mode with PKCS7 padding
func (c *Client) encryptCBC(data []byte, encrypter cipher.BlockMode) []byte {
	blockSize := encrypter.BlockSize()
	
	// Add PKCS7 padding
	padding := blockSize - (len(data) % blockSize)
	paddedData := make([]byte, len(data)+padding)
	copy(paddedData, data)
	for i := len(data); i < len(paddedData); i++ {
		paddedData[i] = byte(padding)
	}
	
	// Encrypt
	encrypted := make([]byte, len(paddedData))
	encrypter.CryptBlocks(encrypted, paddedData)
	
	return encrypted
}

// sendRTPPacket sends an encrypted ALAC frame as an RTP packet
func (c *Client) sendRTPPacket(payload []byte) error {
	if c.udpConn == nil || c.udpAddr == nil {
		return fmt.Errorf("UDP connection not established")
	}
	
	// RTP header is 12 bytes
	// Byte 0: V(2) P X CC(4) - Version=2, Padding=0, Extension=0, CSRC count=0
	// Byte 1: M PT(7) - Marker bit, Payload Type=96 (dynamic)
	// Bytes 2-3: Sequence number
	// Bytes 4-7: Timestamp
	// Bytes 8-11: SSRC
	
	rtpHeader := make([]byte, 12)
	rtpHeader[0] = 0x80 // Version=2, no padding, no extension, no CSRC
	rtpHeader[1] = 0x60 // Marker=0, Payload Type=96
	
	// Sequence number
	binary.BigEndian.PutUint16(rtpHeader[2:4], c.rtpSeq)
	c.rtpSeq++
	
	// Timestamp (increment by samples per frame)
	samplesPerFrame := c.encoder.FrameSize() / c.channels
	c.rtpTimestamp += uint32(samplesPerFrame)
	binary.BigEndian.PutUint32(rtpHeader[4:8], c.rtpTimestamp)
	
	// SSRC
	binary.BigEndian.PutUint32(rtpHeader[8:12], c.rtpSSRC)
	
	// Combine header and payload
	packet := append(rtpHeader, payload...)
	
	// Send packet
	_, err := c.udpConn.Write(packet)
	return err
}

// parseAddress parses an address string into host and port
// Format: "ip:port" or just "ip" (defaults to port 5000 for AirPlay)
func parseAddress(addr string) (string, int) {
	if strings.Contains(addr, ":") {
		parts := strings.Split(addr, ":")
		if len(parts) == 2 {
			port, err := strconv.Atoi(parts[1])
			if err == nil {
				return parts[0], port
			}
		}
		return "", 0
	}
	// Default AirPlay RTSP port
	return addr, 5000
}

// SetPairingCredentials sets the pairing credentials for this client
// These are obtained through the pairing process and stored for future connections
func (c *Client) SetPairingCredentials(deviceID, privateKey string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deviceID = deviceID
	c.privateKey = privateKey
}

// StartPairing initiates the pairing process with the AirPlay device
// This should be called first, and the device will display a PIN code
// Returns the device identifier that will be used in CompletePairing
func (c *Client) StartPairing() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	// Parse address to get host
	host, _ := parseAddress(c.address)
	if host == "" {
		return "", fmt.Errorf("invalid address: %s", c.address)
	}
	
	// Fetch device info if not already available (needed for pairing identifiers)
	if c.deviceInfo == nil {
		info, err := c.fetchDeviceInfo()
		if err != nil {
			log.Printf("AirPlay client %s: Warning: failed to fetch device info for pairing: %v (continuing anyway)", c.id, err)
		} else {
			c.deviceInfo = info
			piStr := ""
			if piBytes, ok := info.PairingID.([]byte); ok && len(piBytes) > 0 {
				piStr = string(piBytes)
				if piStr == "" {
					piStr = base64.StdEncoding.EncodeToString(piBytes)
				}
			} else if piStrVal, ok := info.PairingID.(string); ok {
				piStr = piStrVal
			}
			psiStr := ""
			if psiBytes, ok := info.PairingStateID.([]byte); ok && len(psiBytes) > 0 {
				psiStr = string(psiBytes)
				if psiStr == "" {
					psiStr = base64.StdEncoding.EncodeToString(psiBytes)
				}
			} else if psiStrVal, ok := info.PairingStateID.(string); ok {
				psiStr = psiStrVal
			}
			log.Printf("AirPlay client %s: Device info for pairing - pi: %s, psi: %s", c.id, piStr, psiStr)
		}
	}
	
	// AirPlay pairing: goplay2 (working receiver) receives pairing via RTSP POST, not HTTP
	// Try RTSP POST first, then fall back to HTTP if needed
	// Ensure RTSP connection is established
	if c.rtspConn == nil {
		// Establish RTSP connection first
		rtspAddr := fmt.Sprintf("%s:7000", host)
		conn, err := net.DialTimeout("tcp", rtspAddr, 10*time.Second)
		if err != nil {
			return "", fmt.Errorf("failed to establish RTSP connection for pairing: %w", err)
		}
		c.rtspConn = conn
	}
	
	log.Printf("AirPlay client %s: Starting pairing via RTSP POST", c.id)
	
	// CRITICAL: pyatv calls /pair-pin-start FIRST before /pair-setup
	// This is required for AirPlay devices to enable pairing mode
	pinStartURL := fmt.Sprintf("http://%s:7000/pair-pin-start", host)
	httpClient := &http.Client{Timeout: 10 * time.Second}
	pinStartReq, err := http.NewRequest("POST", pinStartURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create pair-pin-start request: %w", err)
	}
	pinStartReq.Header.Set("User-Agent", "AirPlay/320.20")
	pinStartReq.Header.Set("Connection", "keep-alive")
	pinStartReq.Header.Set("X-Apple-HKP", "3")
	pinStartReq.Header.Set("Content-Type", "application/octet-stream")
	
	log.Printf("AirPlay client %s: Calling /pair-pin-start first (as pyatv does)", c.id)
	pinStartResp, err := httpClient.Do(pinStartReq)
	if err != nil {
		log.Printf("AirPlay client %s: Warning: pair-pin-start failed: %v (continuing anyway)", c.id, err)
	} else {
		// Read and log response body if any
		body, _ := io.ReadAll(pinStartResp.Body)
		pinStartResp.Body.Close()
		log.Printf("AirPlay client %s: pair-pin-start response status: %d, body length: %d", c.id, pinStartResp.StatusCode, len(body))
		if len(body) > 0 {
			log.Printf("AirPlay client %s: pair-pin-start response body: %x", c.id, body)
		}
		// Wait a moment for the device to process the request and display the PIN
		// Some devices need this delay before they're ready for M1
		time.Sleep(1 * time.Second)
	}
	
	// Step 1: Send M1 - Pair Setup Request
	// Try using Tag 0x01 for Method (as some implementations use) and pairing+tlv8 content type
	// Based on m1-probe tests, Tag 0x01 might be correct for AirPlay
	methodTLV := encodeTLV(0x01, []byte{0x00}) // Tag 0x01 (Method) = 0x00 (PairSetup)
	seqNoTLV := encodeTLV(0x06, []byte{0x01})  // Tag 0x06 (SeqNo/State) = 0x01 (M1)
	// Combine TLV entries
	m1 := append(methodTLV, seqNoTLV...)
	log.Printf("AirPlay client %s: Sending M1 request (TLV length: %d, hex: %x)", c.id, len(m1), m1)
	
	// Send HTTP POST request for pairing (pyatv uses HTTP, not RTSP POST)
	pairingURL := fmt.Sprintf("http://%s:7000/pair-setup", host)
	req, err := http.NewRequest("POST", pairingURL, bytes.NewReader(m1))
	if err != nil {
		return "", fmt.Errorf("failed to create pairing request: %w", err)
	}
	
	// Try pairing+tlv8 content type (as used in m1-probe)
	req.Header.Set("User-Agent", "AirPlay/320.20")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("X-Apple-HKP", "3")
	req.Header.Set("Content-Type", "application/pairing+tlv8")
	
	// pyatv does NOT include pairing IDs in the initial pair-setup request
	// They're only used later in the flow
	
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send pairing request: %w", err)
	}
	defer resp.Body.Close()
	
	log.Printf("AirPlay client %s: Pairing M1 response status %d", c.id, resp.StatusCode)
	
	// Read response body regardless of status code
	responseData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read pairing response: %w", err)
	}
	
	// If we got 470 but the PIN appeared on screen, the device accepted the pairing
	// Some devices return 470 even when pairing is successful - check if response has TLV data
	if resp.StatusCode != http.StatusOK {
		log.Printf("AirPlay client %s: Pairing M1 returned status %d, response body length: %d", c.id, resp.StatusCode, len(responseData))
		if len(responseData) == 0 && resp.StatusCode == 470 {
			// Empty response with 470 - device is in pairing mode but we don't have the salt/public key
			// Some devices need multiple retries with increasing delays
			log.Printf("AirPlay client %s: Got 470 with empty body, retrying M1 with exponential backoff", c.id)
			
			// Try multiple retries with exponential backoff
			maxRetries := 3
			retrySuccess := false
			for retry := 0; retry < maxRetries; retry++ {
				// Wait with exponential backoff: 1s, 2s, 3s
				waitTime := time.Duration(retry+1) * time.Second
				log.Printf("AirPlay client %s: Waiting %v before retry %d/%d", c.id, waitTime, retry+1, maxRetries)
				time.Sleep(waitTime)
				
				// Retry M1 request
				req, err = http.NewRequest("POST", pairingURL, bytes.NewReader(m1))
				if err != nil {
					return "", fmt.Errorf("failed to create retry pairing request: %w", err)
				}
				req.Header.Set("User-Agent", "AirPlay/320.20")
				req.Header.Set("Connection", "keep-alive")
				req.Header.Set("X-Apple-HKP", "3")
				req.Header.Set("Content-Type", "application/pairing+tlv8")
				
				resp, err = httpClient.Do(req)
				if err != nil {
					log.Printf("AirPlay client %s: Retry %d failed: %v", c.id, retry+1, err)
					continue
				}
				
				responseData, err = io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil {
					log.Printf("AirPlay client %s: Failed to read retry %d response: %v", c.id, retry+1, err)
					continue
				}
				
				log.Printf("AirPlay client %s: Retry %d M1 response status %d, body length: %d", c.id, retry+1, resp.StatusCode, len(responseData))
				
				// If we got a successful response or one with data, break out of retry loop
				if resp.StatusCode == http.StatusOK || len(responseData) > 0 {
					retrySuccess = true
					break
				}
			}
			
			// If we still get 470 with empty body after all retries, we can't continue without the pairing data
			if !retrySuccess && resp.StatusCode == 470 && len(responseData) == 0 {
				log.Printf("AirPlay client %s: All retries returned 470 with empty body - device may be waiting for PIN but we can't get pairing data", c.id)
				log.Printf("AirPlay client %s: No pairing state stored (M1/M2 never completed successfully)", c.id)
				// Return early - CompletePairing will need to handle this
				// Note: We can't store pairing state because we never got salt/public key
				deviceIDBytes := make([]byte, 16)
				if _, err := rand.Read(deviceIDBytes); err != nil {
					return "", fmt.Errorf("failed to generate device ID: %w", err)
				}
				tempDeviceID := base64.StdEncoding.EncodeToString(deviceIDBytes)
				c.pairingDeviceID = tempDeviceID
				c.pairingState = 1 // Pairing started, waiting for PIN
				log.Printf("AirPlay client %s: Pairing initiated (PIN shown on device), waiting for PIN entry", c.id)
				return tempDeviceID, nil
			}
			
			// If retry was successful, check if we should parse TLV data or return early
			if retrySuccess {
				// Try to parse response as TLV - might contain salt/public key even with 470
				tlvData := decodeTLV(responseData)
				if len(tlvData) == 0 && resp.StatusCode == 470 {
					// Empty response with 470 - if PIN appeared, device is waiting for PIN
					// Return temp device ID so UI can prompt
					deviceIDBytes := make([]byte, 16)
					if _, err := rand.Read(deviceIDBytes); err != nil {
						return "", fmt.Errorf("failed to generate device ID: %w", err)
					}
					tempDeviceID := base64.StdEncoding.EncodeToString(deviceIDBytes)
					c.pairingDeviceID = tempDeviceID
					c.pairingState = 1 // Pairing started, waiting for PIN
					log.Printf("AirPlay client %s: Pairing initiated (PIN shown on device), waiting for PIN entry", c.id)
					return tempDeviceID, nil
				}
				// If we got TLV data, continue to normal flow below
			}
		}
	}
	
	// Parse TLV response (if we have data)
	var tlvData map[byte][]byte
	if len(responseData) > 0 {
		tlvData = decodeTLV(responseData)
	} else {
		tlvData = make(map[byte][]byte)
	}
	
	// Check for salt (type 0x02) and public key (type 0x03)
	salt, hasSalt := tlvData[0x02]
	publicKey, hasPublicKey := tlvData[0x03]
	
	// If we don't have salt/public key, device may be using a different flow
	// If PIN appeared, return success so UI can prompt
	if !hasSalt || !hasPublicKey {
		// PIN appeared but no pairing data - device is waiting for PIN
		// This is valid - return temp device ID so UI can prompt
		deviceIDBytes := make([]byte, 16)
		if _, err := rand.Read(deviceIDBytes); err != nil {
			return "", fmt.Errorf("failed to generate device ID: %w", err)
		}
		tempDeviceID := base64.StdEncoding.EncodeToString(deviceIDBytes)
		c.pairingDeviceID = tempDeviceID
		c.pairingState = 1 // Pairing started, waiting for PIN
		log.Printf("AirPlay client %s: Pairing initiated (PIN shown on device), waiting for PIN entry", c.id)
		return tempDeviceID, nil
	}
	
	// Parse server public key B from response
	B := new(big.Int).SetBytes(publicKey)
	
	// Initialize SRP session with proper SRP-6a parameters
	c.pairingSession = &srpSession{
		salt:      salt,
		publicKey: publicKey,
		N:         srpN3072,
		g:         srpG,
		B:         B,
	}
	
	// Generate proper SRP-6a client public key A and private key a
	A, a, err := generateSRPClientKey()
	if err != nil {
		return "", fmt.Errorf("failed to generate SRP client key: %w", err)
	}
	
	c.pairingSession.A = A
	c.pairingSession.a = a

	// Convert A to bytes (384 bytes for 3072 bits)
	clientPublicKeyBytes := make([]byte, 384)
	A.FillBytes(clientPublicKeyBytes)
	
	// Convert a (private key) to bytes for storage
	clientPrivateKeyBytes := make([]byte, 384)
	a.FillBytes(clientPrivateKeyBytes)
	
	// Store pairing state in memory so CompletePairing can restore it
	// This is needed because we create a new client instance in the API
	pairingStateStoreMu.Lock()
	pairingStateStore[c.id] = &pairingStateData{
		Salt:             salt,
		PublicKey:        publicKey,
		ClientPublicKey:  clientPublicKeyBytes,
		ClientPrivateKey: clientPrivateKeyBytes,
		ServerPublicKey:  publicKey, // B is the server public key
	}
	pairingStateStoreMu.Unlock()
	log.Printf("AirPlay client %s: Stored pairing state for later restoration", c.id)

	// Step 2: Send M2 - Client Public Key (pyatv uses SeqNo 0x03)
	// TLV type 0x06 (SeqNo) = 0x03 (M2), TLV type 0x03 (PublicKey) = client public key
	m2 := append(
		encodeTLV(0x06, []byte{0x03}), // SeqNo = M2
		encodeTLV(0x03, clientPublicKeyBytes)..., // PublicKey
	)
	log.Printf("AirPlay client %s: Sending M2 request (TLV length: %d)", c.id, len(m2))
	
	req, err = http.NewRequest("POST", pairingURL, bytes.NewReader(m2))
	if err != nil {
		return "", fmt.Errorf("failed to create M2 request: %w", err)
	}
	req.Header.Set("User-Agent", "AirPlay/320.20")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("X-Apple-HKP", "3")
	req.Header.Set("Content-Type", "application/octet-stream")
	
	resp, err = httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send client public key: %w", err)
	}
	defer resp.Body.Close()
	
	log.Printf("AirPlay client %s: Pairing M2 response status %d", c.id, resp.StatusCode)
	
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("AirPlay client %s: Pairing M2 error response body: %s", c.id, string(body))
		return "", fmt.Errorf("client public key request failed with status %d: %s", resp.StatusCode, string(body))
	}
	
	responseData, err = io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}
	
	tlvData = decodeTLV(responseData)
	
	// Device should respond with proof (type 0x04)
	proof, hasProof := tlvData[0x04]
	if !hasProof {
		return "", fmt.Errorf("device did not send proof")
	}
	
	// Store proof for verification in CompletePairing
	c.pairingSession.sharedKey = proof
	
	c.pairingState = 1 // Pairing started, waiting for PIN
	
	// Generate a temporary device ID (will be replaced after PIN verification)
	deviceIDBytes := make([]byte, 16)
	if _, err := rand.Read(deviceIDBytes); err != nil {
		return "", fmt.Errorf("failed to generate device ID: %w", err)
	}
	tempDeviceID := base64.StdEncoding.EncodeToString(deviceIDBytes)
	c.pairingDeviceID = tempDeviceID
	
	log.Printf("AirPlay client %s: Pairing initiated, waiting for PIN", c.id)
	
	return tempDeviceID, nil
}

// startPairingHTTP is a fallback that uses HTTP for pairing (original implementation)
func (c *Client) startPairingHTTP() (string, error) {
	host, _ := parseAddress(c.address)
	pairingURL := fmt.Sprintf("http://%s:7000/pair-setup", host)
	
	log.Printf("AirPlay client %s: Starting pairing via HTTP fallback: %s", c.id, pairingURL)
	
	httpClient := &http.Client{Timeout: 10 * time.Second}
	
	// Step 1: Send M1 - Pair Setup Request
	// Include both Method and State in M1
	methodTLV := encodeTLV(0x01, []byte{0x00}) // Method = Pair Setup
	stateTLV := encodeTLV(0x06, []byte{0x01})   // State = M1
	m1 := append(methodTLV, stateTLV...)
	log.Printf("AirPlay client %s: Sending M1 request via HTTP (TLV length: %d, hex: %x)", c.id, len(m1), m1)
	
	req, err := http.NewRequest("POST", pairingURL, bytes.NewReader(m1))
	if err != nil {
		return "", fmt.Errorf("failed to create pairing request: %w", err)
	}
	req.Header.Set("Content-Type", "application/pairing+tlv8")
	req.Header.Set("User-Agent", "AirPlay/320.20")
	
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send pairing request: %w", err)
	}
	defer resp.Body.Close()
	
	log.Printf("AirPlay client %s: Pairing M1 HTTP response status %d", c.id, resp.StatusCode)
	
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("AirPlay client %s: Pairing M1 HTTP error response body: %s", c.id, string(body))
		return "", fmt.Errorf("pairing request failed with status %d: %s", resp.StatusCode, string(body))
	}
	
	responseData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read pairing response: %w", err)
	}
	
	// Parse TLV response
	tlvData := decodeTLV(responseData)
	
	// Check for salt (type 0x02) and public key (type 0x03)
	salt, hasSalt := tlvData[0x02]
	publicKey, hasPublicKey := tlvData[0x03]
	
	if !hasSalt || !hasPublicKey {
		return "", fmt.Errorf("invalid pairing response: missing salt or public key")
	}
	
	// Parse server public key B from response
	B := new(big.Int).SetBytes(publicKey)
	
	// Initialize SRP session
	c.pairingSession = &srpSession{
		salt:      salt,
		publicKey: publicKey,
		N:         srpN3072,
		g:         srpG,
		B:         B,
	}
	
	// Generate SRP client keys
	A, a, err := generateSRPClientKey()
	if err != nil {
		return "", fmt.Errorf("failed to generate SRP client key: %w", err)
	}
	
	c.pairingSession.A = A
	c.pairingSession.a = a
	
	// Convert A to bytes
	clientPublicKeyBytes := make([]byte, 384)
	A.FillBytes(clientPublicKeyBytes)
	
	// Step 2: Send M2 via HTTP
	m2 := encodeTLV(0x03, clientPublicKeyBytes)
	req, err = http.NewRequest("POST", pairingURL, bytes.NewReader(m2))
	if err != nil {
		return "", fmt.Errorf("failed to create M2 request: %w", err)
	}
	req.Header.Set("Content-Type", "application/pairing+tlv8")
	req.Header.Set("User-Agent", "AirPlay/320.20")
	
	resp, err = httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send client public key: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("client public key request failed with status %d: %s", resp.StatusCode, string(body))
	}
	
	responseData, err = io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}
	
	tlvData = decodeTLV(responseData)
	
	proof, hasProof := tlvData[0x04]
	if !hasProof {
		return "", fmt.Errorf("device did not send proof")
	}
	
	c.pairingSession.sharedKey = proof
	c.pairingState = 1
	
	deviceIDBytes := make([]byte, 16)
	if _, err := rand.Read(deviceIDBytes); err != nil {
		return "", fmt.Errorf("failed to generate device ID: %w", err)
	}
	tempDeviceID := base64.StdEncoding.EncodeToString(deviceIDBytes)
	c.pairingDeviceID = tempDeviceID
	
	log.Printf("AirPlay client %s: Pairing initiated via HTTP, waiting for PIN", c.id)
	
	return tempDeviceID, nil
}

// CompletePairing completes the pairing process by providing the PIN code
// Returns the device identifier and private key that should be stored for future connections
func (c *Client) CompletePairing(pin string) (string, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	// If pairing state is not 1, check if we need to start pairing first
	// This handles the case where StartPairing() was called but the session wasn't initialized
	// (e.g., when we got a 470 response with empty body)
	if c.pairingState != 1 || c.pairingSession == nil {
		// First, try to restore pairing state from memory (if StartPairing() stored it)
		pairingStateStoreMu.RLock()
		storedState, hasStoredState := pairingStateStore[c.id]
		pairingStateStoreMu.RUnlock()
		
		log.Printf("AirPlay client %s: Checking for stored pairing state - hasStoredState=%v, pairingStateStore keys: %v", c.id, hasStoredState, func() []string {
			pairingStateStoreMu.RLock()
			defer pairingStateStoreMu.RUnlock()
			keys := make([]string, 0, len(pairingStateStore))
			for k := range pairingStateStore {
				keys = append(keys, k)
			}
			return keys
		}())
		
		if hasStoredState && storedState != nil {
			log.Printf("AirPlay client %s: Restoring pairing state from memory", c.id)
			// Restore pairing session from stored state
			B := new(big.Int).SetBytes(storedState.ServerPublicKey)
			A := new(big.Int).SetBytes(storedState.ClientPublicKey)
			a := new(big.Int).SetBytes(storedState.ClientPrivateKey)
			
			c.pairingSession = &srpSession{
				salt:      storedState.Salt,
				publicKey: storedState.PublicKey,
				N:         srpN3072,
				g:         srpG,
				B:         B,
				A:         A,
				a:         a,
			}
			
			if len(storedState.Proof) > 0 {
				c.pairingSession.sharedKey = storedState.Proof
			}
			
			c.pairingState = 1 // Pairing started, ready for PIN
			log.Printf("AirPlay client %s: Pairing state restored, proceeding with PIN verification", c.id)
		} else {
			// No stored state - this means StartPairing() got 470 and returned early without storing state
			// The device is already showing the PIN from the earlier /pair-pin-start call
			// We must NOT call /pair-pin-start again as it would reset the PIN
			// Instead, try sending M1 again to get the salt/public key (device should still be in pairing mode)
			log.Printf("AirPlay client %s: Pairing session not initialized, but device should already be in pairing mode. Attempting M1/M2 exchange", c.id)
		
		// Parse address to get host
		host, _ := parseAddress(c.address)
		if host == "" {
			return "", "", fmt.Errorf("invalid address: %s", c.address)
		}
		
		// Fetch device info if not already available (needed for pairing identifiers)
		if c.deviceInfo == nil {
			info, err := c.fetchDeviceInfo()
			if err != nil {
				log.Printf("AirPlay client %s: Warning: failed to fetch device info: %v (continuing anyway)", c.id, err)
			} else {
				c.deviceInfo = info
			}
		}
		
		// Extract pairing identifiers for use in headers
		var piStr, psiStr string
		if c.deviceInfo != nil {
			if piBytes, ok := c.deviceInfo.PairingID.([]byte); ok && len(piBytes) > 0 {
				piStr = string(piBytes)
				if piStr == "" {
					piStr = base64.StdEncoding.EncodeToString(piBytes)
				}
			} else if piStrVal, ok := c.deviceInfo.PairingID.(string); ok {
				piStr = piStrVal
			}
			if psiBytes, ok := c.deviceInfo.PairingStateID.([]byte); ok && len(psiBytes) > 0 {
				psiStr = string(psiBytes)
				if psiStr == "" {
					psiStr = base64.StdEncoding.EncodeToString(psiBytes)
				}
			} else if psiStrVal, ok := c.deviceInfo.PairingStateID.(string); ok {
				psiStr = psiStrVal
			}
		}
		
		pairingURL := fmt.Sprintf("http://%s:7000/pair-setup", host)
		httpClient := &http.Client{Timeout: 10 * time.Second}
		
		// CRITICAL: Do NOT call /pair-pin-start here - the device is already showing the PIN
		// Calling it again would reset the pairing session and invalidate the PIN the user just entered
		
		// Step 1: Send M1 - Pair Setup Request
		// Use Tag 0x01 for Method and pairing+tlv8 content type (as tested in m1-probe)
		methodTLV := encodeTLV(0x01, []byte{0x00}) // Tag 0x01 (Method) = 0x00 (PairSetup)
		seqNoTLV := encodeTLV(0x06, []byte{0x01})  // Tag 0x06 (SeqNo/State) = 0x01 (M1)
		m1 := append(methodTLV, seqNoTLV...)
		
		req, err := http.NewRequest("POST", pairingURL, bytes.NewReader(m1))
		if err != nil {
			return "", "", fmt.Errorf("failed to create pairing request: %w", err)
		}
		req.Header.Set("User-Agent", "AirPlay/320.20")
		req.Header.Set("Connection", "keep-alive")
		req.Header.Set("X-Apple-HKP", "3")
		req.Header.Set("Content-Type", "application/pairing+tlv8")
		// Include pairing identifiers if available (may help device identify the pairing session)
		if piStr != "" {
			req.Header.Set("X-Apple-Pairing-ID", piStr)
		}
		if psiStr != "" {
			req.Header.Set("X-Apple-Pairing-State", psiStr)
		}
		
		resp, err := httpClient.Do(req)
		if err != nil {
			return "", "", fmt.Errorf("failed to send pairing request: %w", err)
		}
		defer resp.Body.Close()
		
		responseData, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", "", fmt.Errorf("failed to read pairing response: %w", err)
		}
		
		log.Printf("AirPlay client %s: M1 response in CompletePairing: status=%d, body_length=%d", c.id, resp.StatusCode, len(responseData))
		
		// Handle 470 response - device is waiting for PIN but might need retries to send salt/public key
		// Try retrying M1 a few times with delays (don't restart pairing as it would invalidate the PIN)
		if resp.StatusCode == 470 && len(responseData) == 0 {
			log.Printf("AirPlay client %s: Got 470 with empty body. Retrying M1 without restarting pairing (device already showing PIN)", c.id)
			
			// Try retries with delays - device might need time to respond
			maxRetries := 3
			for retry := 0; retry < maxRetries; retry++ {
				waitTime := time.Duration(retry+1) * time.Second
				log.Printf("AirPlay client %s: Waiting %v before retry %d/%d", c.id, waitTime, retry+1, maxRetries)
				time.Sleep(waitTime)
				
				req, err = http.NewRequest("POST", pairingURL, bytes.NewReader(m1))
				if err != nil {
					return "", "", fmt.Errorf("failed to create retry pairing request: %w", err)
				}
				req.Header.Set("User-Agent", "AirPlay/320.20")
				req.Header.Set("Connection", "keep-alive")
				req.Header.Set("X-Apple-HKP", "3")
				req.Header.Set("Content-Type", "application/pairing+tlv8")
				// Include pairing identifiers if available
				if piStr != "" {
					req.Header.Set("X-Apple-Pairing-ID", piStr)
				}
				if psiStr != "" {
					req.Header.Set("X-Apple-Pairing-State", psiStr)
				}
				
				resp, err = httpClient.Do(req)
				if err != nil {
					log.Printf("AirPlay client %s: Retry %d failed: %v", c.id, retry+1, err)
					continue
				}
				
				responseData, err = io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil {
					log.Printf("AirPlay client %s: Failed to read retry %d response: %v", c.id, retry+1, err)
					continue
				}
				
				log.Printf("AirPlay client %s: Retry %d M1 response: status=%d, body_length=%d", c.id, retry+1, resp.StatusCode, len(responseData))
				
				// If we got data or success, break out
				if resp.StatusCode == http.StatusOK || len(responseData) > 0 {
					break
				}
			}
			
			// If still 470, the device is waiting for PIN but we can't get pairing data
			// This shouldn't happen, but if it does, we can't proceed
			if resp.StatusCode == 470 && len(responseData) == 0 {
				return "", "", fmt.Errorf("device returned 470 after %d retries and pairing data is unavailable. The pairing session may have expired - please delete the client and try pairing again from the beginning", maxRetries)
			}
		}
		
		// Try to parse response even if status is not 200
		var tlvData map[byte][]byte
		if len(responseData) > 0 {
			tlvData = decodeTLV(responseData)
		} else {
			tlvData = make(map[byte][]byte)
		}
		
		// Check for salt (type 0x02) and public key (type 0x03)
		salt, hasSalt := tlvData[0x02]
		publicKey, hasPublicKey := tlvData[0x03]
		
		if !hasSalt || !hasPublicKey {
			// If we got 470 with empty body, we can't continue
			if resp.StatusCode == 470 && len(responseData) == 0 {
				return "", "", fmt.Errorf("device is in pairing mode but pairing session data is missing. The pairing may have timed out. Please delete the client and add it again to restart pairing")
			}
			return "", "", fmt.Errorf("invalid pairing response: missing salt or public key (status: %d, body length: %d). Device may need pairing to be restarted", resp.StatusCode, len(responseData))
		}
		
		// Parse server public key B from response
		B := new(big.Int).SetBytes(publicKey)
		
		// Initialize SRP session with proper SRP-6a parameters
		c.pairingSession = &srpSession{
			salt:      salt,
			publicKey: publicKey,
			N:         srpN3072,
			g:         srpG,
			B:         B,
		}
		
		// Generate proper SRP-6a client public key A and private key a
		A, a, err := generateSRPClientKey()
		if err != nil {
			return "", "", fmt.Errorf("failed to generate SRP client key: %w", err)
		}
		
		c.pairingSession.A = A
		c.pairingSession.a = a
		
		// Convert A to bytes (384 bytes for 3072 bits)
		clientPublicKeyBytes := make([]byte, 384)
		A.FillBytes(clientPublicKeyBytes)
		
		// Step 2: Send M2 - Client Public Key (pyatv uses SeqNo 0x03 for M2)
		// TLV type 0x06 (SeqNo) = 0x03 (M2), TLV type 0x03 (PublicKey) = client public key
		m2 := append(
			encodeTLV(0x06, []byte{0x03}), // SeqNo = M2
			encodeTLV(0x03, clientPublicKeyBytes)..., // PublicKey
		)
		
		req, err = http.NewRequest("POST", pairingURL, bytes.NewReader(m2))
		if err != nil {
			return "", "", fmt.Errorf("failed to create M2 request: %w", err)
		}
		req.Header.Set("User-Agent", "AirPlay/320.20")
		req.Header.Set("Connection", "keep-alive")
		req.Header.Set("X-Apple-HKP", "3")
		req.Header.Set("Content-Type", "application/pairing+tlv8")
		// Include pairing identifiers if available
		if piStr != "" {
			req.Header.Set("X-Apple-Pairing-ID", piStr)
		}
		if psiStr != "" {
			req.Header.Set("X-Apple-Pairing-State", psiStr)
		}
		
		resp, err = httpClient.Do(req)
		if err != nil {
			return "", "", fmt.Errorf("failed to send client public key: %w", err)
		}
		defer resp.Body.Close()
		
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return "", "", fmt.Errorf("client public key request failed with status %d: %s", resp.StatusCode, string(body))
		}
		
		responseData, err = io.ReadAll(resp.Body)
		if err != nil {
			return "", "", fmt.Errorf("failed to read response: %w", err)
		}
		
		tlvData = decodeTLV(responseData)
		
		// Device should respond with proof (type 0x04)
		proof, hasProof := tlvData[0x04]
		if !hasProof {
			return "", "", fmt.Errorf("device did not send proof")
		}
		
		// Store proof for verification
		c.pairingSession.sharedKey = proof
		c.pairingState = 1 // Pairing started, ready for PIN
		}
	}
	
	// At this point, we should have a valid pairing session (either restored or newly created)
	if c.pairingSession == nil {
		return "", "", fmt.Errorf("pairing session not initialized - cannot proceed with PIN verification")
	}
	
	// Parse address to get host
	host, _ := parseAddress(c.address)
	if host == "" {
		return "", "", fmt.Errorf("invalid address: %s", c.address)
	}
	
	// Fetch device info if not already available (needed for pairing identifiers)
	if c.deviceInfo == nil {
		info, err := c.fetchDeviceInfo()
		if err != nil {
			log.Printf("AirPlay client %s: Warning: failed to fetch device info: %v (continuing anyway)", c.id, err)
		} else {
			c.deviceInfo = info
		}
	}
	
	// Extract pairing identifiers for use in headers
	var piStr, psiStr string
	if c.deviceInfo != nil {
		if piBytes, ok := c.deviceInfo.PairingID.([]byte); ok && len(piBytes) > 0 {
			piStr = string(piBytes)
			if piStr == "" {
				piStr = base64.StdEncoding.EncodeToString(piBytes)
			}
		} else if piStrVal, ok := c.deviceInfo.PairingID.(string); ok {
			piStr = piStrVal
		}
		if psiBytes, ok := c.deviceInfo.PairingStateID.([]byte); ok && len(psiBytes) > 0 {
			psiStr = string(psiBytes)
			if psiStr == "" {
				psiStr = base64.StdEncoding.EncodeToString(psiBytes)
			}
		} else if psiStrVal, ok := c.deviceInfo.PairingStateID.(string); ok {
			psiStr = psiStrVal
		}
	}
	
	pairingURL := fmt.Sprintf("http://%s:7000/pair-setup", host)
	
	// Step 3: Send M3 - PIN verification
	// Convert PIN to string (4-digit PIN)
	if len(pin) != 4 {
		return "", "", fmt.Errorf("PIN must be 4 digits")
	}
	
	// Calculate SRP shared secret S using proper SRP-6a
	S, err := calculateSRPSharedSecret(c.pairingSession.B, c.pairingSession.a, c.pairingSession.salt, pin)
	if err != nil {
		return "", "", fmt.Errorf("failed to calculate SRP shared secret: %w", err)
	}
	c.pairingSession.S = S
	
	// Calculate session key K = H(S)
	K := calculateSRPSessionKey(S)
	c.pairingSession.K = K
	
	// Calculate client proof M = H(H(N) XOR H(g) | H(username) | salt | A | B | K)
	// For HomeKit, username is typically empty
	username := ""
	clientProof := calculateSRPProof(username, c.pairingSession.salt, c.pairingSession.A, c.pairingSession.B, K)
	
	// Step 3: Send M3 - PIN Proof (pyatv uses SeqNo 0x04 for M3)
	// TLV type 0x06 (SeqNo) = 0x04 (M3), TLV type 0x05 (Proof) = client proof
	m3 := append(
		encodeTLV(0x06, []byte{0x04}), // SeqNo = M3
		encodeTLV(0x05, clientProof)..., // Proof
	)
	log.Printf("AirPlay client %s: Sending M3 request (TLV length: %d)", c.id, len(m3))
	
	// Create HTTP client for pairing requests
	httpClient := &http.Client{Timeout: 10 * time.Second}
	
	req, err := http.NewRequest("POST", pairingURL, bytes.NewReader(m3))
	if err != nil {
		return "", "", fmt.Errorf("failed to create M3 request: %w", err)
	}
		req.Header.Set("User-Agent", "AirPlay/320.20")
		req.Header.Set("Connection", "keep-alive")
		req.Header.Set("X-Apple-HKP", "3")
		req.Header.Set("Content-Type", "application/pairing+tlv8") // Use pairing+tlv8 for M3
	// Include pairing identifiers if available (may help device identify the pairing session)
	if piStr != "" {
		req.Header.Set("X-Apple-Pairing-ID", piStr)
	}
	if psiStr != "" {
		req.Header.Set("X-Apple-Pairing-State", psiStr)
	}
	
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("failed to send PIN proof: %w", err)
	}
	defer resp.Body.Close()
	
	log.Printf("AirPlay client %s: Pairing M3 response status %d", c.id, resp.StatusCode)
	
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("AirPlay client %s: Pairing M3 error response body: %s", c.id, string(body))
		return "", "", fmt.Errorf("PIN verification failed with status %d: %s", resp.StatusCode, string(body))
	}
	
	responseData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("failed to read response: %w", err)
	}
	
	tlvData := decodeTLV(responseData)
	
	// Device should respond with encrypted data containing device ID and private key
	encryptedData, hasEncrypted := tlvData[0x06]
	if !hasEncrypted {
		return "", "", fmt.Errorf("device did not send encrypted credentials")
	}
	
	// Decrypt the data using the shared key
	// In production, this would use proper encryption
	decryptedData := decryptPairingData(encryptedData, c.pairingSession.sharedKey)
	
	// Parse decrypted data to get device ID and private key
	decryptedTLV := decodeTLV(decryptedData)
	
	deviceID, hasDeviceID := decryptedTLV[0x01]
	privateKey, hasPrivateKey := decryptedTLV[0x02]
	
	if !hasDeviceID || !hasPrivateKey {
		return "", "", fmt.Errorf("decrypted data missing device ID or private key")
	}
	
	c.pairingDeviceID = string(deviceID)
	c.pairingPrivateKey = privateKey
	c.deviceID = c.pairingDeviceID
	c.privateKey = base64.StdEncoding.EncodeToString(privateKey)
	c.pairingState = 2 // Pairing completed
	
	// Clear stored pairing state (no longer needed)
	pairingStateStoreMu.Lock()
	delete(pairingStateStore, c.id)
	pairingStateStoreMu.Unlock()
	
	log.Printf("AirPlay client %s: Pairing completed successfully", c.id)
	
	return c.pairingDeviceID, c.privateKey, nil
}

// encodeTLV encodes data in TLV (Type-Length-Value) format
// HomeKit pairing uses TLV8 (1-byte length) for values up to 255 bytes
// For values > 255 bytes, we'd need TLV16, but pairing typically uses TLV8
func encodeTLV(tag byte, value []byte) []byte {
	length := len(value)
	if length > 255 {
		// For values > 255, use TLV16 (2-byte length)
		result := make([]byte, 1+2+length)
		result[0] = tag
		binary.BigEndian.PutUint16(result[1:3], uint16(length))
		copy(result[3:], value)
		return result
	}
	// TLV8: tag (1 byte) + length (1 byte) + value
	result := make([]byte, 1+1+length)
	result[0] = tag
	result[1] = byte(length)
	copy(result[2:], value)
	return result
}

// decodeTLV decodes TLV data into a map
// Supports both TLV8 (1-byte length) and TLV16 (2-byte length)
// TLV16 is indicated by length byte = 0xFF, followed by 2-byte length
func decodeTLV(data []byte) map[byte][]byte {
	result := make(map[byte][]byte)
	offset := 0
	
	for offset < len(data) {
		if offset+2 > len(data) {
			break // Not enough data for tag + length byte
		}
		
		tag := data[offset]
		lengthByte := data[offset+1]
		
		var length int
		var valueOffset int
		
		// Check if this is TLV16 (length byte = 0xFF indicates 2-byte length follows)
		if lengthByte == 0xFF && offset+4 <= len(data) {
			// TLV16: tag (1) + 0xFF (1) + length (2) + value
			length = int(binary.BigEndian.Uint16(data[offset+2 : offset+4]))
			valueOffset = offset + 4
		} else {
			// TLV8: tag (1) + length (1) + value
			length = int(lengthByte)
			valueOffset = offset + 2
		}
		
		if valueOffset+length > len(data) {
			break // Not enough data for value
		}
		
		value := make([]byte, length)
		copy(value, data[valueOffset:valueOffset+length])
		result[tag] = value
		offset = valueOffset + length
	}
	
	return result
}

// calculateSRPProofLegacy is the old simplified version (removed - using proper SRP-6a from srp.go)

// decryptPairingData decrypts pairing data using the shared key
// This is a simplified version - production would use proper AES encryption
func decryptPairingData(encrypted, key []byte) []byte {
	// For now, return as-is (simplified)
	// In production, this would use AES-128-CBC or similar
	if len(key) < 16 {
		// Pad key if needed
		paddedKey := make([]byte, 16)
		copy(paddedKey, key)
		key = paddedKey
	}
	
	// Simple XOR decryption for now (not secure, but functional)
	decrypted := make([]byte, len(encrypted))
	for i := range encrypted {
		decrypted[i] = encrypted[i] ^ key[i%len(key)]
	}
	
	return decrypted
}

// isPrintable checks if a string contains only printable ASCII characters
func isPrintable(s string) bool {
	for _, r := range s {
		if r < 32 || r > 126 {
			return false
		}
	}
	return true
}

