package integration

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"
	"unsafe"

	"github.com/yeixio/air-mixer/internal/audio"
	"github.com/yeixio/air-mixer/internal/db"
	"github.com/yeixio/air-mixer/internal/protocols"
	"github.com/yeixio/air-mixer/internal/router"
)

// MockProtocolServer is a test implementation of ProtocolServer
type MockProtocolServer struct {
	id          string
	name        string
	protocol    string
	audioStream chan protocols.AudioPacket
	running     bool
}

func NewMockProtocolServer(id, name string) *MockProtocolServer {
	return &MockProtocolServer{
		id:          id,
		name:        name,
		protocol:    "mock",
		audioStream: make(chan protocols.AudioPacket, 100),
		running:     false,
	}
}

func (m *MockProtocolServer) Start() error {
	m.running = true
	return nil
}

func (m *MockProtocolServer) Stop() error {
	m.running = false
	close(m.audioStream)
	return nil
}

func (m *MockProtocolServer) AudioStream() <-chan protocols.AudioPacket {
	return m.audioStream
}

func (m *MockProtocolServer) Name() string {
	return m.name
}

func (m *MockProtocolServer) ID() string {
	return m.id
}

func (m *MockProtocolServer) Protocol() string {
	return m.protocol
}

func (m *MockProtocolServer) IsRunning() bool {
	return m.running
}

// SendTestAudio sends a test audio packet to the server's stream
func (m *MockProtocolServer) SendTestAudio(samples []float32) {
	// Convert float32 samples to bytes
	data := make([]byte, len(samples)*4)
	for i, sample := range samples {
		bits := *(*uint32)(unsafe.Pointer(&sample))
		data[i*4] = byte(bits)
		data[i*4+1] = byte(bits >> 8)
		data[i*4+2] = byte(bits >> 16)
		data[i*4+3] = byte(bits >> 24)
	}

	packet := protocols.AudioPacket{
		Data:       data,
		SampleRate: 44100,
		Channels:   2,
		Format:     audio.FormatPCM,
		Timestamp:  time.Now().UnixNano(),
	}

	select {
	case m.audioStream <- packet:
	default:
		// Channel full, skip
	}
}

// MockProtocolClient is a test implementation of ProtocolClient
type MockProtocolClient struct {
	id          string
	name        string
	protocol    string
	connected   bool
	received    []protocols.AudioPacket
	receiveChan chan protocols.AudioPacket
}

func NewMockProtocolClient(id, name string) *MockProtocolClient {
	return &MockProtocolClient{
		id:          id,
		name:        name,
		protocol:    "mock",
		connected:   true,
		received:    make([]protocols.AudioPacket, 0),
		receiveChan: make(chan protocols.AudioPacket, 100),
	}
}

func (m *MockProtocolClient) Connect(target string) error {
	m.connected = true
	return nil
}

func (m *MockProtocolClient) Disconnect() error {
	m.connected = false
	return nil
}

func (m *MockProtocolClient) SendAudio(packet protocols.AudioPacket) error {
	if !m.connected {
		return fmt.Errorf("client not connected")
	}
	m.received = append(m.received, packet)
	select {
	case m.receiveChan <- packet:
	default:
		// Channel full, skip
	}
	return nil
}

func (m *MockProtocolClient) Name() string {
	return m.name
}

func (m *MockProtocolClient) ID() string {
	return m.id
}

func (m *MockProtocolClient) Protocol() string {
	return m.protocol
}

func (m *MockProtocolClient) IsConnected() bool {
	return m.connected
}

// GetReceivedPackets returns all received audio packets
func (m *MockProtocolClient) GetReceivedPackets() []protocols.AudioPacket {
	return m.received
}

// WaitForPackets waits for at least n packets to be received
func (m *MockProtocolClient) WaitForPackets(n int, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for len(m.received) < n {
		select {
		case <-m.receiveChan:
			// Packet received, check again
		case <-ctx.Done():
			return false
		}
	}
	return true
}

// TestAudioPipeline tests the complete audio pipeline:
// 1. Create server, client, and route
// 2. Register them with the protocol manager
// 3. Send test audio through the server
// 4. Verify audio reaches the client
func TestAudioPipeline(t *testing.T) {
	// Setup database
	database, err := db.New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer database.Close()
	
	// Run migrations
	if err := database.Migrate(); err != nil {
		t.Fatalf("Failed to migrate database: %v", err)
	}

	// Create router
	audioRouter := router.NewRouter(database)
	if err := audioRouter.Start(); err != nil {
		t.Fatalf("Failed to start router: %v", err)
	}
	defer audioRouter.Stop()

	// Create protocol manager
	protocolManager := protocols.NewManager(audioRouter)

	// Create mock server
	serverID := "test-server-1"
	serverName := "Test Server"
	server := NewMockProtocolServer(serverID, serverName)

	// Create mock client
	clientID := "test-client-1"
	clientName := "Test Client"
	client := NewMockProtocolClient(clientID, clientName)

	// Register server
	if err := protocolManager.RegisterServer(server); err != nil {
		t.Fatalf("Failed to register server: %v", err)
	}

	// Start server
	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop()

	// Register client
	if err := protocolManager.RegisterClient(client); err != nil {
		t.Fatalf("Failed to register client: %v", err)
	}

	// Wait a bit for registration to complete
	time.Sleep(100 * time.Millisecond)

	// Create route
	route := router.Route{
		ID:       "test-route-1",
		Name:     "Test Route",
		SourceID: serverID,
		DestIDs:  []string{clientID},
		MixMode:  router.MixModeReplace,
		Volume:   1.0,
		Enabled:  true,
		Priority: 1,
	}

	// Add route to router
	if err := audioRouter.AddRoute(route); err != nil {
		t.Fatalf("Failed to add route: %v", err)
	}

	// Wait a bit for route activation
	time.Sleep(200 * time.Millisecond)

	// Generate test audio samples (sine wave)
	sampleRate := 44100
	duration := 1.0 // 1 second
	numSamples := int(float64(sampleRate) * duration)
	samples := make([]float32, numSamples*2) // Stereo

	frequency := 440.0 // A4 note
	for i := 0; i < numSamples; i++ {
		t := float64(i) / float64(sampleRate)
		value := float32(0.5 * math.Sin(2*math.Pi*frequency*t))
		samples[i*2] = value     // Left channel
		samples[i*2+1] = value    // Right channel
	}

	// Send test audio in chunks
	chunkSize := 1024
	numChunks := len(samples) / chunkSize
	t.Logf("Sending %d chunks of %d samples each", numChunks, chunkSize)

	for i := 0; i < numChunks; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(samples) {
			end = len(samples)
		}
		chunk := samples[start:end]
		server.SendTestAudio(chunk)
		time.Sleep(10 * time.Millisecond) // Small delay between chunks
	}

	// Wait for audio to propagate through the pipeline
	time.Sleep(500 * time.Millisecond)

	// Verify client received audio packets
	receivedPackets := client.GetReceivedPackets()
	if len(receivedPackets) == 0 {
		t.Error("Client did not receive any audio packets")
		t.Logf("Router status: running=%v", audioRouter.IsRunning())
		t.Logf("Server status: running=%v", server.IsRunning())
		t.Logf("Client status: connected=%v", client.IsConnected())
	} else {
		t.Logf("✓ Client received %d audio packets", len(receivedPackets))

		// Verify packet contents
		totalBytes := 0
		for i, packet := range receivedPackets {
			if i < 3 || i == len(receivedPackets)-1 {
				t.Logf("Packet %d: %d bytes, sample rate: %d, channels: %d, format: %s",
					i, len(packet.Data), packet.SampleRate, packet.Channels, packet.Format)
			}
			totalBytes += len(packet.Data)
		}
		t.Logf("Total bytes received: %d", totalBytes)

		// Verify we received a reasonable amount of data
		expectedMinBytes := numSamples * 2 * 4 // samples * channels * bytes per float32
		if totalBytes < expectedMinBytes/2 { // Allow for some loss/chunking
			t.Errorf("Received too little data: %d bytes, expected at least %d bytes", totalBytes, expectedMinBytes/2)
		}
	}
}

