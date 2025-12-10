package discovery

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/grandcat/zeroconf"
)

// AirPlayDiscoverer discovers AirPlay devices via mDNS/Bonjour
type AirPlayDiscoverer struct{}

// NewAirPlayDiscoverer creates a new AirPlay discoverer
func NewAirPlayDiscoverer() *AirPlayDiscoverer {
	return &AirPlayDiscoverer{}
}

// Protocol returns the protocol name
func (d *AirPlayDiscoverer) Protocol() string {
	return "airplay"
}

// Discover discovers AirPlay devices on the network using mDNS
func (d *AirPlayDiscoverer) Discover(ctx context.Context, timeout time.Duration) ([]DiscoveredDevice, error) {
	// Discover AirPlay services (_raop._tcp)
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create mDNS resolver: %w", err)
	}

	entries := make(chan *zeroconf.ServiceEntry)
	devices := []DiscoveredDevice{}

	// Start discovery
	go func() {
		err := resolver.Browse(ctx, "_raop._tcp", "local.", entries)
		if err != nil {
			close(entries)
		}
	}()

	// Collect discovered devices
	done := make(chan bool)
	go func() {
		seen := make(map[string]bool) // Track seen devices by address:port
		for entry := range entries {
			if entry == nil {
				continue
			}

			// Get IP address
			var ip string
			if len(entry.AddrIPv4) > 0 {
				ip = entry.AddrIPv4[0].String()
			} else if len(entry.AddrIPv6) > 0 {
				ip = entry.AddrIPv6[0].String()
			} else if entry.HostName != "" {
				// Try to resolve hostname
				if resolved, err := resolveAddress(entry.HostName); err == nil {
					ip = resolved
				} else {
					continue // Skip if we can't resolve
				}
			} else {
				continue // Skip if no address
			}

			// Create unique key
			key := fmt.Sprintf("%s:%d", ip, entry.Port)
			if seen[key] {
				continue // Skip duplicates
			}
			seen[key] = true

			// Parse device name from service name
			deviceName := parseAirPlayName(entry.Instance)

			device := DiscoveredDevice{
				Name:        deviceName,
				Protocol:    "airplay",
				Address:     fmt.Sprintf("%s:%d", ip, entry.Port),
				Port:        entry.Port,
				Description: fmt.Sprintf("AirPlay device: %s", deviceName),
				Metadata: map[string]string{
					"service_name": entry.Instance,
					"hostname":     entry.HostName,
					"domain":       entry.Domain,
				},
			}

			// Parse TXT record metadata if available
			if len(entry.Text) > 0 {
				for _, txt := range entry.Text {
					parts := strings.SplitN(txt, "=", 2)
					if len(parts) == 2 {
						device.Metadata[parts[0]] = parts[1]
					}
				}
			}

			devices = append(devices, device)
		}
		done <- true
	}()

	// Wait for timeout or completion
	select {
	case <-time.After(timeout):
		// Timeout reached, return what we found
	case <-done:
		// Discovery completed
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return devices, nil
}

// parseAirPlayName extracts device name from AirPlay service name
// Format is typically: MAC@DeviceName or just DeviceName
func parseAirPlayName(serviceName string) string {
	// AirPlay service names often have format: MAC@DeviceName
	var deviceName string
	if idx := strings.Index(serviceName, "@"); idx > 0 && idx < len(serviceName)-1 {
		deviceName = serviceName[idx+1:]
	} else {
		deviceName = serviceName
	}
	
	// Clean up escape characters - remove backslashes before spaces
	// AirPlay device names sometimes have "\ " (backslash-space) which should just be a space
	deviceName = strings.ReplaceAll(deviceName, "\\ ", " ")
	// Remove any remaining standalone backslashes that aren't escaping anything meaningful
	deviceName = strings.ReplaceAll(deviceName, "\\", "")
	
	return deviceName
}

// resolveAddress resolves a hostname to IP address
func resolveAddress(hostname string) (string, error) {
	ips, err := net.LookupIP(hostname)
	if err != nil {
		return "", err
	}

	for _, ip := range ips {
		if ip.To4() != nil {
			return ip.String(), nil
		}
	}

	if len(ips) > 0 {
		return ips[0].String(), nil
	}

	return "", fmt.Errorf("no IP address found for %s", hostname)
}

