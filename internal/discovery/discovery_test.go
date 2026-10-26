package discovery

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/libp2p/zeroconf/v2"
)

func entry(instance string, ip string, port int, txt ...string) *zeroconf.ServiceEntry {
	e := &zeroconf.ServiceEntry{HostName: "host.local.", Port: port, Text: txt}
	e.Instance, e.Domain = instance, "local."
	if parsed := net.ParseIP(ip); parsed != nil {
		if parsed.To4() != nil {
			e.AddrIPv4 = []net.IP{parsed}
		} else {
			e.AddrIPv6 = []net.IP{parsed}
		}
	}
	return e
}

func fakeBrowse(entries []*zeroconf.ServiceEntry, err error) browseFunc {
	return func(ctx context.Context, service, _ string, out chan<- *zeroconf.ServiceEntry, _ ...zeroconf.ClientOption) error {
		if service != "_raop._tcp" {
			return errors.New("wrong service " + service)
		}
		if err != nil {
			return err
		}
		for _, e := range entries {
			out <- e
		}
		return nil
	}
}

func TestAirPlayDiscover(t *testing.T) {
	a := &AirPlay{browse: fakeBrowse([]*zeroconf.ServiceEntry{
		entry(`AABBCCDDEEFF@Living\ Room`, "192.168.1.20", 7000, "am=AudioAccessory5,1", "et=0,4", "bogus"),
		entry("AABBCCDDEEFF@Living Room", "192.168.1.20", 7000), // duplicate
		entry("Kitchen", "fe80::1", 5000),
		entry("NoAddress", "", 5000),
		nil,
	}, nil)}
	devices, err := a.Discover(context.Background(), 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 {
		t.Fatalf("got %d devices: %+v", len(devices), devices)
	}
	d := devices[0]
	if d.Name != "Living Room" || d.Address != "192.168.1.20:7000" || d.Port != 7000 || d.Protocol != "airplay" {
		t.Errorf("device %+v", d)
	}
	if d.Metadata["am"] != "AudioAccessory5,1" || d.Metadata["et"] != "0,4" || d.Metadata["service_name"] == "" {
		t.Errorf("metadata %v", d.Metadata)
	}
	if devices[1].Address != "[fe80::1]:5000" {
		t.Errorf("IPv6 address %q", devices[1].Address)
	}

	a = &AirPlay{browse: fakeBrowse(nil, errors.New("no multicast"))}
	if _, err := a.Discover(context.Background(), 50*time.Millisecond); err == nil {
		t.Error("browse error swallowed")
	}
}

type stubDiscoverer struct {
	protocol string
	devices  []Device
	err      error
}

func (s stubDiscoverer) Protocol() string { return s.protocol }
func (s stubDiscoverer) Discover(context.Context, time.Duration) ([]Device, error) {
	return s.devices, s.err
}

func TestService(t *testing.T) {
	ctx := context.Background()
	s := NewService(
		stubDiscoverer{protocol: "a", devices: []Device{{Name: "Zed"}, {Name: "Amy"}}},
		stubDiscoverer{protocol: "b", err: errors.New("down")},
	)
	if got := s.Protocols(); len(got) != 2 || got[0] != "a" {
		t.Errorf("Protocols = %v", got)
	}
	all, err := s.DiscoverAll(ctx, time.Millisecond)
	if err != nil || len(all) != 2 || all[0].Name != "Amy" {
		t.Errorf("DiscoverAll = %v, %v", all, err)
	}
	if _, err := s.Discover(ctx, "b", time.Millisecond); err == nil {
		t.Error("protocol error swallowed")
	}
	var unknown ErrUnknownProtocol
	if _, err := s.Discover(ctx, "c", time.Millisecond); !errors.As(err, &unknown) {
		t.Errorf("unknown protocol = %v", err)
	}

	failing := NewService(stubDiscoverer{protocol: "b", err: errors.New("down")})
	if _, err := failing.DiscoverAll(ctx, time.Millisecond); err == nil {
		t.Error("total failure swallowed")
	}
	if got := NewService().Protocols(); len(got) != 1 || got[0] != "airplay" {
		t.Errorf("default protocols = %v", got)
	}
}
