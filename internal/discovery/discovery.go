// Package discovery finds speakers on the local network.
package discovery

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/libp2p/zeroconf/v2"
)

// Device is a speaker found on the network.
type Device struct {
	Name        string            `json:"name"`
	Protocol    string            `json:"protocol"`
	Address     string            `json:"address"` // host:port
	Port        int               `json:"port"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Description string            `json:"description,omitempty"`
}

// Discoverer finds devices for one protocol.
type Discoverer interface {
	Protocol() string
	// Discover browses for timeout and returns what it found.
	Discover(ctx context.Context, timeout time.Duration) ([]Device, error)
}

// Service runs discovery across protocols.
type Service struct {
	discoverers []Discoverer
}

// NewService returns a Service with the given discoverers, or every
// built-in one if none are given.
func NewService(ds ...Discoverer) *Service {
	if len(ds) == 0 {
		ds = []Discoverer{NewAirPlay()}
	}
	return &Service{discoverers: ds}
}

// Protocols lists the protocols that support discovery.
func (s *Service) Protocols() []string {
	out := make([]string, len(s.discoverers))
	for i, d := range s.discoverers {
		out[i] = d.Protocol()
	}
	return out
}

// DiscoverAll browses every protocol at once. A protocol that fails is
// skipped; the error is returned only if every protocol fails.
func (s *Service) DiscoverAll(ctx context.Context, timeout time.Duration) ([]Device, error) {
	type result struct {
		devices []Device
		err     error
	}
	results := make(chan result, len(s.discoverers))
	for _, d := range s.discoverers {
		go func() {
			devices, err := d.Discover(ctx, timeout)
			results <- result{devices, err}
		}()
	}
	out := []Device{}
	var lastErr error
	failed := 0
	for range s.discoverers {
		r := <-results
		if r.err != nil {
			failed++
			lastErr = r.err
			continue
		}
		out = append(out, r.devices...)
	}
	if failed > 0 && failed == len(s.discoverers) {
		return nil, lastErr
	}
	sortDevices(out)
	return out, nil
}

// ErrUnknownProtocol is returned for a protocol without discovery.
type ErrUnknownProtocol string

func (e ErrUnknownProtocol) Error() string {
	return fmt.Sprintf("discovery is not supported for protocol %q", string(e))
}

// Discover browses one protocol.
func (s *Service) Discover(ctx context.Context, protocol string, timeout time.Duration) ([]Device, error) {
	for _, d := range s.discoverers {
		if d.Protocol() == protocol {
			devices, err := d.Discover(ctx, timeout)
			sortDevices(devices)
			return devices, err
		}
	}
	return nil, ErrUnknownProtocol(protocol)
}

func sortDevices(ds []Device) {
	slices.SortFunc(ds, func(a, b Device) int {
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.Address, b.Address)
	})
}

// browseFunc matches zeroconf.Browse; tests replace it.
type browseFunc func(ctx context.Context, service, domain string, entries chan<- *zeroconf.ServiceEntry, opts ...zeroconf.ClientOption) error

// AirPlay discovers AirPlay (RAOP) speakers over mDNS.
type AirPlay struct {
	browse browseFunc
}

// NewAirPlay returns an AirPlay discoverer.
func NewAirPlay() *AirPlay { return &AirPlay{browse: zeroconf.Browse} }

// Protocol returns "airplay".
func (*AirPlay) Protocol() string { return "airplay" }

// Discover browses _raop._tcp for timeout.
func (a *AirPlay) Discover(ctx context.Context, timeout time.Duration) ([]Device, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	entries := make(chan *zeroconf.ServiceEntry, 16)
	errc := make(chan error, 1)
	go func() { errc <- a.browse(ctx, "_raop._tcp", "local.", entries) }()

	devices := []Device{}
	seen := make(map[string]bool)
	for {
		select {
		case e, ok := <-entries:
			if !ok {
				return devices, nil
			}
			d, ok := airplayDevice(e)
			if ok && !seen[d.Address] {
				seen[d.Address] = true
				devices = append(devices, d)
			}
		case err := <-errc:
			if err != nil {
				return nil, fmt.Errorf("mDNS browse: %w", err)
			}
			errc = nil // browsing ended; keep reading until the timeout
		case <-ctx.Done():
			return devices, nil
		}
	}
}

func airplayDevice(e *zeroconf.ServiceEntry) (Device, bool) {
	if e == nil {
		return Device{}, false
	}
	var ip net.IP
	switch {
	case len(e.AddrIPv4) > 0:
		ip = e.AddrIPv4[0]
	case len(e.AddrIPv6) > 0:
		ip = e.AddrIPv6[0]
	default:
		return Device{}, false
	}
	name := airplayName(e.Instance)
	d := Device{
		Name:        name,
		Protocol:    "airplay",
		Address:     net.JoinHostPort(ip.String(), strconv.Itoa(e.Port)),
		Port:        e.Port,
		Description: "AirPlay device: " + name,
		Metadata: map[string]string{
			"service_name": e.Instance,
			"hostname":     e.HostName,
			"domain":       e.Domain,
		},
	}
	for _, txt := range e.Text {
		if k, v, ok := strings.Cut(txt, "="); ok {
			d.Metadata[k] = v
		}
	}
	return d, true
}

// airplayName turns a RAOP instance name, "<MAC>@<name>", into the name.
func airplayName(instance string) string {
	if _, name, ok := strings.Cut(instance, "@"); ok && name != "" {
		instance = name
	}
	return strings.ReplaceAll(instance, `\`, "")
}
