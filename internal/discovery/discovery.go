// Package discovery finds speakers on the local network.
package discovery

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/zeroconf/v2"

	"github.com/yeixio/air-mixer/internal/protocols/upnp"
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

// Service runs discovery across protocols. It leaves out Air-Mixer's own
// receivers, from this and every other Air-Mixer instance, so its sources
// are never offered as speakers.
type Service struct {
	discoverers []Discoverer
	exclude     func(Device) bool
}

// Exclude also leaves out the devices f reports, such as inputs this host
// runs through other programs (shairport-sync) that carry no Air-Mixer
// marker.
func (s *Service) Exclude(f func(Device) bool) { s.exclude = f }

// keep reports whether d is shown: not one of Air-Mixer's own receivers and
// not excluded.
func (s *Service) keep(d Device) bool {
	return !IsAirMixer(d) && (s.exclude == nil || !s.exclude(d))
}

func (s *Service) filter(ds []Device) []Device {
	out := make([]Device, 0, len(ds))
	for _, d := range ds {
		if s.keep(d) {
			out = append(out, d)
		}
	}
	return out
}

// IsAirMixer reports whether d is a receiver Air-Mixer itself advertises:
// an AirPlay input (model AirMixer in its TXT record) or a DLNA renderer
// input (an Air-Mixer SSDP server header or manufacturer).
func IsAirMixer(d Device) bool {
	m := d.Metadata
	switch {
	case m["am"] == "AirMixer":
		return true
	case strings.Contains(m["server"], "Air-Mixer/"):
		return true
	case d.Protocol == "upnp" && strings.HasPrefix(d.Description, "UPnP/DLNA renderer: Air-Mixer"):
		return true
	}
	return false
}

// Host returns the IP address d answers on.
func (d Device) Host() string {
	if h := d.Metadata["host"]; h != "" {
		return h
	}
	if h, _, err := net.SplitHostPort(d.Address); err == nil {
		return h
	}
	return hostOf(d.Address)
}

// NewService returns a Service with the given discoverers, or every
// built-in one if none are given.
func NewService(ds ...Discoverer) *Service {
	if len(ds) == 0 {
		ds = []Discoverer{NewAirPlay(), NewAirPlay2(), NewCast(), NewSnapcast(), NewUPnP()}
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
		out = append(out, s.filter(r.devices)...)
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
			devices = s.filter(devices)
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
	return browseMDNS(ctx, a.browse, "_raop._tcp", timeout, func(e *zeroconf.ServiceEntry, _ net.IP) Device {
		d, _ := airplayDevice(e)
		return d
	})
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

// AirPlay2 discovers AirPlay 2 receivers (_airplay._tcp): HomePods,
// Apple TVs and other speakers that need AirPlay 2.
type AirPlay2 struct {
	browse browseFunc
}

// NewAirPlay2 returns an AirPlay 2 discoverer.
func NewAirPlay2() *AirPlay2 { return &AirPlay2{browse: zeroconf.Browse} }

// Protocol returns "airplay2".
func (*AirPlay2) Protocol() string { return "airplay2" }

// Discover browses _airplay._tcp for timeout.
func (a *AirPlay2) Discover(ctx context.Context, timeout time.Duration) ([]Device, error) {
	return browseMDNS(ctx, a.browse, "_airplay._tcp", timeout, func(e *zeroconf.ServiceEntry, _ net.IP) Device {
		d, ok := airplayDevice(e)
		if ok {
			d.Protocol = "airplay2"
			d.Description = "AirPlay 2 device: " + d.Name
		}
		return d
	})
}

// airplayName turns a RAOP instance name, "<MAC>@<name>", into the name.
func airplayName(instance string) string {
	if _, name, ok := strings.Cut(instance, "@"); ok && name != "" {
		instance = name
	}
	return strings.ReplaceAll(instance, `\`, "")
}

// Snapcast discovers Snapcast servers over mDNS (_snapcast._tcp). Their
// address points at the TCP source port Air-Mixer writes to (4953 by
// convention, see package snapcast), not the advertised client port.
type Snapcast struct {
	browse browseFunc
}

// NewSnapcast returns a Snapcast discoverer.
func NewSnapcast() *Snapcast { return &Snapcast{browse: zeroconf.Browse} }

// Protocol returns "snapcast".
func (*Snapcast) Protocol() string { return "snapcast" }

// Discover browses _snapcast._tcp for timeout.
func (s *Snapcast) Discover(ctx context.Context, timeout time.Duration) ([]Device, error) {
	return browseMDNS(ctx, s.browse, "_snapcast._tcp", timeout, func(e *zeroconf.ServiceEntry, ip net.IP) Device {
		name := strings.ReplaceAll(e.Instance, `\`, "")
		return Device{
			Name:        name,
			Protocol:    "snapcast",
			Address:     net.JoinHostPort(ip.String(), "4953"),
			Port:        4953,
			Description: "Snapcast server: " + name,
			Metadata:    map[string]string{"hostname": e.HostName, "client_port": strconv.Itoa(e.Port)},
		}
	})
}

// browseMDNS browses one mDNS service for timeout and turns each answer
// with an address into a Device, once per address.
func browseMDNS(ctx context.Context, browse browseFunc, service string, timeout time.Duration, device func(*zeroconf.ServiceEntry, net.IP) Device) ([]Device, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	entries := make(chan *zeroconf.ServiceEntry, 16)
	errc := make(chan error, 1)
	go func() {
		err := browse(ctx, service, "local.", entries)
		if err != nil && ctx.Err() == nil {
			// Hosts and pods without IPv6 can't open the IPv6 socket;
			// browse over IPv4 alone instead.
			err = browse(ctx, service, "local.", entries, zeroconf.SelectIPTraffic(zeroconf.IPv4))
		}
		errc <- err
	}()

	devices := []Device{}
	seen := make(map[string]bool)
	for {
		select {
		case e, ok := <-entries:
			if !ok {
				return devices, nil
			}
			ip := entryIP(e)
			if ip == nil {
				continue
			}
			d := device(e, ip)
			if !seen[d.Address] {
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

func entryIP(e *zeroconf.ServiceEntry) net.IP {
	switch {
	case e == nil:
		return nil
	case len(e.AddrIPv4) > 0:
		return e.AddrIPv4[0]
	case len(e.AddrIPv6) > 0:
		return e.AddrIPv6[0]
	}
	return nil
}

// Cast discovers Google Cast devices and speaker groups over mDNS
// (_googlecast._tcp).
type Cast struct {
	browse browseFunc
}

// NewCast returns a Google Cast discoverer.
func NewCast() *Cast { return &Cast{browse: zeroconf.Browse} }

// Protocol returns "cast".
func (*Cast) Protocol() string { return "cast" }

// Discover browses _googlecast._tcp for timeout.
func (c *Cast) Discover(ctx context.Context, timeout time.Duration) ([]Device, error) {
	return browseMDNS(ctx, c.browse, "_googlecast._tcp", timeout, func(e *zeroconf.ServiceEntry, ip net.IP) Device {
		meta := map[string]string{"service_name": e.Instance, "hostname": e.HostName}
		for _, txt := range e.Text {
			if k, v, ok := strings.Cut(txt, "="); ok {
				meta[k] = v
			}
		}
		name := meta["fn"] // the name people gave it in Google Home
		if name == "" {
			name = strings.ReplaceAll(e.Instance, `\`, "")
		}
		desc := "Google Cast device"
		if md := meta["md"]; md != "" {
			desc += ": " + md
		}
		return Device{
			Name:        name,
			Protocol:    "cast",
			Address:     net.JoinHostPort(ip.String(), strconv.Itoa(e.Port)),
			Port:        e.Port,
			Description: desc,
			Metadata:    meta,
		}
	})
}

// UPnP discovers UPnP/DLNA media renderers, Sonos players among them,
// with an SSDP search, and reads each one's name from its description.
type UPnP struct {
	search func(ctx context.Context, target string, timeout time.Duration) ([]upnp.SearchResult, error)
	name   func(ctx context.Context, location string) (name, model string, err error)
}

// NewUPnP returns a UPnP discoverer.
func NewUPnP() *UPnP { return &UPnP{search: upnp.Search, name: upnp.Describe} }

// Protocol returns "upnp".
func (*UPnP) Protocol() string { return "upnp" }

// Discover searches for media renderers for timeout.
func (u *UPnP) Discover(ctx context.Context, timeout time.Duration) ([]Device, error) {
	found, err := u.search(ctx, "urn:schemas-upnp-org:device:MediaRenderer:1", timeout)
	if err != nil {
		return nil, err
	}
	devices := make([]Device, len(found))
	var wg sync.WaitGroup
	for i, r := range found {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d := Device{Protocol: "upnp", Address: r.Location, Metadata: map[string]string{"usn": r.USN, "server": r.Server}}
			if host, port, err := net.SplitHostPort(hostOf(r.Location)); err == nil {
				d.Port, _ = strconv.Atoi(port)
				d.Metadata["host"] = host
			}
			ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			name, model, err := u.name(ctx, r.Location)
			cancel()
			if err != nil || name == "" {
				name = hostOf(r.Location)
			}
			d.Name = name
			d.Description = "UPnP/DLNA renderer"
			if model != "" {
				d.Description += ": " + model
			}
			devices[i] = d
		}()
	}
	wg.Wait()
	return devices, nil
}

func hostOf(location string) string {
	rest := strings.TrimPrefix(strings.TrimPrefix(location, "http://"), "https://")
	host, _, _ := strings.Cut(rest, "/")
	return host
}
