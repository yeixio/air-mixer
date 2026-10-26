package protocols

import (
	"fmt"
	"log"
	"sync"
	"time"
	"unsafe"

	"github.com/yeixio/air-mixer/internal/audio"
	"github.com/yeixio/air-mixer/internal/router"
)

// Manager manages protocol adapters and connects them to the router
type Manager struct {
	servers map[string]ProtocolServer
	clients map[string]ProtocolClient
	router  *router.AudioRouter
	mu      sync.RWMutex
	
	// Stream converters: convert AudioPacket to float32 streams
	serverStreams map[string]chan []float32
}

// NewManager creates a new protocol adapter manager
func NewManager(audioRouter *router.AudioRouter) *Manager {
	return &Manager{
		servers:       make(map[string]ProtocolServer),
		clients:       make(map[string]ProtocolClient),
		router:        audioRouter,
		serverStreams: make(map[string]chan []float32),
	}
}

// RegisterServer registers a protocol server (input)
func (m *Manager) RegisterServer(server ProtocolServer) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if server.ID() == "" {
		return fmt.Errorf("server ID cannot be empty")
	}

	if _, exists := m.servers[server.ID()]; exists {
		return fmt.Errorf("server %s already registered", server.ID())
	}

	m.servers[server.ID()] = server

	// Create stream converter channel
	streamChan := make(chan []float32, 100)
	m.serverStreams[server.ID()] = streamChan

	// Register with router (pass both ID and name for name-based routing)
	m.router.RegisterSourceStream(server.ID(), server.Name(), streamChan)

	// Start server if not already running
	if !server.IsRunning() {
		if err := server.Start(); err != nil {
			return fmt.Errorf("failed to start server %s: %w", server.ID(), err)
		}
	}

	// Start stream converter goroutine
	go m.convertServerStream(server, streamChan)

	log.Printf("Registered server: %s (%s)", server.ID(), server.Protocol())
	return nil
}

// UnregisterServer unregisters a protocol server
// Returns nil if server is not registered (idempotent operation)
func (m *Manager) UnregisterServer(serverID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	server, exists := m.servers[serverID]
	if !exists {
		// Server not registered - this is OK (idempotent operation)
		// Return nil to indicate success (nothing to unregister)
		return nil
	}

	// Stop server
	if server.IsRunning() {
		if err := server.Stop(); err != nil {
			log.Printf("Error stopping server %s: %v", serverID, err)
		}
	}

	// Unregister from router (name will be removed automatically)
	m.router.UnregisterSourceStream(serverID)

	// Close stream channel
	if streamChan, ok := m.serverStreams[serverID]; ok {
		close(streamChan)
		delete(m.serverStreams, serverID)
	}

	delete(m.servers, serverID)
	log.Printf("Unregistered server: %s", serverID)
	return nil
}

// RegisterClient registers a protocol client (output)
func (m *Manager) RegisterClient(client ProtocolClient) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if client.ID() == "" {
		return fmt.Errorf("client ID cannot be empty")
	}

	if _, exists := m.clients[client.ID()]; exists {
		return fmt.Errorf("client %s already registered", client.ID())
	}

	m.clients[client.ID()] = client

	// Create sink channel for router
	sinkChan := make(chan []float32, 100)
	m.router.RegisterClientSink(client.ID(), client.Name(), sinkChan)
	log.Printf("Protocol manager: created sink channel for client %s (%s) and registered with router", client.ID(), client.Name())

	// Start client output goroutine
	go m.handleClientOutput(client, sinkChan)
	log.Printf("Protocol manager: started client output handler goroutine for client %s", client.ID())

	log.Printf("Protocol manager: ✓ registered client: %s (%s)", client.ID(), client.Protocol())
	return nil
}

// UnregisterClient unregisters a protocol client
// Returns nil if client is not registered (idempotent operation)
func (m *Manager) UnregisterClient(clientID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	client, exists := m.clients[clientID]
	if !exists {
		// Client not registered - this is OK (idempotent operation)
		// Return nil to indicate success (nothing to unregister)
		return nil
	}

	// Disconnect client
	if client.IsConnected() {
		if err := client.Disconnect(); err != nil {
			log.Printf("Error disconnecting client %s: %v", clientID, err)
		}
	}

	// Unregister from router
	m.router.UnregisterClientSink(clientID)

	delete(m.clients, clientID)
	log.Printf("Unregistered client: %s", clientID)
	return nil
}

// GetServer gets a server by ID
func (m *Manager) GetServer(serverID string) (ProtocolServer, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	server, exists := m.servers[serverID]
	if !exists {
		return nil, fmt.Errorf("server %s not found", serverID)
	}

	return server, nil
}

// GetClient gets a client by ID
func (m *Manager) GetClient(clientID string) (ProtocolClient, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	client, exists := m.clients[clientID]
	if !exists {
		return nil, fmt.Errorf("client %s not found", clientID)
	}

	return client, nil
}

// ListServers returns all registered servers
func (m *Manager) ListServers() []ProtocolServer {
	m.mu.RLock()
	defer m.mu.RUnlock()

	servers := make([]ProtocolServer, 0, len(m.servers))
	for _, server := range m.servers {
		servers = append(servers, server)
	}

	return servers
}

// ListClients returns all registered clients
func (m *Manager) ListClients() []ProtocolClient {
	m.mu.RLock()
	defer m.mu.RUnlock()

	clients := make([]ProtocolClient, 0, len(m.clients))
	for _, client := range m.clients {
		clients = append(clients, client)
	}

	return clients
}

// convertServerStream converts AudioPacket to float32 stream
func (m *Manager) convertServerStream(server ProtocolServer, streamChan chan<- []float32) {
	audioStream := server.AudioStream()
	
	// Track decoders per format (in case format changes)
	decoders := make(map[audio.Format]audio.AudioDecoder)
	
	for packet := range audioStream {
		log.Printf("Protocol manager: received audio packet from server %s (format: %s, %d bytes, sample rate: %d, channels: %d)", 
			server.ID(), packet.Format, len(packet.Data), packet.SampleRate, packet.Channels)
		
		// Handle different formats
		var samples []float32

		switch packet.Format {
		case audio.FormatALAC:
			// ALAC packets should be decoded by the server before sending
			// If we receive ALAC format, it means decoder isn't set up yet
			// Skip these packets - they'll be handled once decoder is ready
			log.Printf("Protocol manager: skipping ALAC packet (should be decoded by server)")
			continue
		case audio.FormatPCM:
			// The packet data contains float32 samples converted to bytes
			// We need to convert them back to float32
			if len(packet.Data)%4 != 0 {
				log.Printf("Protocol manager: invalid PCM packet size (not multiple of 4): %d bytes", len(packet.Data))
				continue
			}
			sampleCount := len(packet.Data) / 4
			samples = make([]float32, sampleCount)
			for i := 0; i < sampleCount; i++ {
				bits := uint32(packet.Data[i*4]) | 
					uint32(packet.Data[i*4+1])<<8 | 
					uint32(packet.Data[i*4+2])<<16 | 
					uint32(packet.Data[i*4+3])<<24
				samples[i] = *(*float32)(unsafe.Pointer(&bits))
			}
			log.Printf("Protocol manager: converted %d bytes to %d float32 samples", len(packet.Data), len(samples))
		default:
			log.Printf("Protocol manager: unsupported format: %s", packet.Format)
			continue
		}

		// Send to router stream
		select {
		case streamChan <- samples:
			log.Printf("Protocol manager: sent %d samples to router stream for server %s", len(samples), server.ID())
		default:
			log.Printf("Protocol manager: router stream buffer full, dropping packet from server %s", server.ID())
		}
	}

	// Cleanup decoders
	for _, decoder := range decoders {
		decoder.Close()
	}
}

// handleClientOutput handles audio output for a client
func (m *Manager) handleClientOutput(client ProtocolClient, sinkChan <-chan []float32) {
	log.Printf("Protocol manager: started client output handler for client %s (%s)", client.ID(), client.Protocol())
	
	// Ensure client is connected
	if !client.IsConnected() {
		log.Printf("Protocol manager: client %s is not connected, attempting to connect...", client.ID())
		// TODO: Get target address from client config
		// For now, we'll assume client is already connected or will connect automatically
	}
	
	sampleCount := 0
	var sampleRate int = 44100 // Default, should get from router/stream
	var channels int = 2        // Default, should get from router/stream
	
	for samples := range sinkChan {
		sampleCount++
		
		// Log first sample and periodically
		if sampleCount == 1 || sampleCount%1000 == 0 {
			log.Printf("Protocol manager: client %s received %d samples (batch #%d)", client.ID(), len(samples), sampleCount)
		}
		
		// Convert float32 samples to bytes (PCM format)
		// Each float32 is 4 bytes
		data := make([]byte, len(samples)*4)
		for i, sample := range samples {
			// Convert float32 to bytes (little-endian)
			bits := *(*uint32)(unsafe.Pointer(&sample))
			data[i*4] = byte(bits)
			data[i*4+1] = byte(bits >> 8)
			data[i*4+2] = byte(bits >> 16)
			data[i*4+3] = byte(bits >> 24)
		}
		
		// Create AudioPacket
		packet := AudioPacket{
			Data:       data,
			SampleRate: sampleRate,
			Channels:   channels,
			Format:     audio.FormatPCM,
			Timestamp:  time.Now().UnixNano(),
		}
		
		// Send audio to client
		if err := client.SendAudio(packet); err != nil {
			log.Printf("Protocol manager: error sending audio to client %s: %v", client.ID(), err)
			// Continue processing - don't stop on single error
		} else if sampleCount == 1 || sampleCount%1000 == 0 {
			log.Printf("Protocol manager: successfully sent %d bytes to client %s", len(data), client.ID())
		}
	}
	
	log.Printf("Protocol manager: client output handler stopped for client %s", client.ID())
}

