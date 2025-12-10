package discovery

import (
	"context"
	"fmt"
	"time"
)

// DiscoveredDevice represents a discovered audio device
type DiscoveredDevice struct {
	Name        string            `json:"name"`
	Protocol    string            `json:"protocol"`
	Address     string            `json:"address"`
	Port        int               `json:"port"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Description string            `json:"description,omitempty"`
}

// Discoverer interface for protocol-specific discovery
type Discoverer interface {
	Discover(ctx context.Context, timeout time.Duration) ([]DiscoveredDevice, error)
	Protocol() string
}

// Service manages device discovery across multiple protocols
type Service struct {
	discoverers []Discoverer
}

// NewService creates a new discovery service
func NewService() *Service {
	return &Service{
		discoverers: []Discoverer{
			NewAirPlayDiscoverer(),
			// Add other discoverers here as they're implemented
		},
	}
}

// DiscoverAll discovers devices across all supported protocols
func (s *Service) DiscoverAll(ctx context.Context, timeout time.Duration) ([]DiscoveredDevice, error) {
	var allDevices []DiscoveredDevice
	
	for _, discoverer := range s.discoverers {
		devices, err := discoverer.Discover(ctx, timeout)
		if err != nil {
			// Log error but continue with other protocols
			continue
		}
		allDevices = append(allDevices, devices...)
	}
	
	return allDevices, nil
}

// DiscoverByProtocol discovers devices for a specific protocol
func (s *Service) DiscoverByProtocol(ctx context.Context, protocol string, timeout time.Duration) ([]DiscoveredDevice, error) {
	for _, discoverer := range s.discoverers {
		if discoverer.Protocol() == protocol {
			return discoverer.Discover(ctx, timeout)
		}
	}
	
	return nil, fmt.Errorf("no discoverer found for protocol: %s", protocol)
}

// GetSupportedProtocols returns list of protocols that support discovery
func (s *Service) GetSupportedProtocols() []string {
	protocols := make([]string, 0, len(s.discoverers))
	for _, d := range s.discoverers {
		protocols = append(protocols, d.Protocol())
	}
	return protocols
}

