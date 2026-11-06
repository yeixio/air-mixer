package discovery

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/zeroconf/v2"

	"github.com/yeixio/air-mixer/internal/protocols/upnp"
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
	return fakeBrowseService("_raop._tcp", entries, err)
}

func fakeBrowseService(want string, entries []*zeroconf.ServiceEntry, err error) browseFunc {
	return func(ctx context.Context, service, _ string, out chan<- *zeroconf.ServiceEntry, _ ...zeroconf.ClientOption) error {
		if service != want {
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

func TestSnapcastDiscover(t *testing.T) {
	s := &Snapcast{browse: fakeBrowseService("_snapcast._tcp", []*zeroconf.ServiceEntry{
		entry(`Snapcast`, "192.168.1.30", 1704),
	}, nil)}
	devices, err := s.Discover(context.Background(), 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].Address != "192.168.1.30:4953" || devices[0].Protocol != "snapcast" {
		t.Fatalf("devices %+v", devices)
	}
}

func TestCastDiscover(t *testing.T) {
	c := &Cast{browse: fakeBrowseService("_googlecast._tcp", []*zeroconf.ServiceEntry{
		entry("Google-Nest-Mini-0123abcd", "192.168.1.40", 8009, "fn=Kitchen speaker", "md=Google Nest Mini", "id=0123abcd"),
		entry("Google-Cast-Group-77", "192.168.1.40", 32187, "fn=Downstairs"),
	}, nil)}
	devices, err := c.Discover(context.Background(), 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 {
		t.Fatalf("devices %+v", devices)
	}
	d := devices[0]
	if d.Name != "Kitchen speaker" || d.Address != "192.168.1.40:8009" || d.Protocol != "cast" || d.Description != "Google Cast device: Google Nest Mini" {
		t.Errorf("device %+v", d)
	}
	if devices[1].Name != "Downstairs" || devices[1].Port != 32187 {
		t.Errorf("group %+v", devices[1])
	}
}

func TestUPnPDiscover(t *testing.T) {
	u := &UPnP{
		search: func(context.Context, string, time.Duration) ([]upnp.SearchResult, error) {
			return []upnp.SearchResult{
				{Location: "http://192.168.1.50:1400/xml/device_description.xml", USN: "uuid:RINCON_1::urn:schemas-upnp-org:device:MediaRenderer:1"},
				{Location: "http://192.168.1.60:49152/desc.xml"},
			}, nil
		},
		name: func(_ context.Context, loc string) (string, string, error) {
			if strings.Contains(loc, "1400") {
				return "Living Room", "Sonos One", nil
			}
			return "", "", errors.New("timeout")
		},
	}
	devices, err := u.Discover(context.Background(), time.Millisecond)
	if err != nil || len(devices) != 2 {
		t.Fatalf("%v %+v", err, devices)
	}
	if d := devices[0]; d.Name != "Living Room" || d.Description != "UPnP/DLNA renderer: Sonos One" || d.Port != 1400 || d.Protocol != "upnp" {
		t.Errorf("device %+v", d)
	}
	if devices[1].Name != "192.168.1.60:49152" {
		t.Errorf("unnamed device %+v", devices[1])
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
	if got := NewService().Protocols(); !slices.Equal(got, []string{"airplay", "airplay2", "cast", "snapcast", "upnp"}) {
		t.Errorf("default protocols = %v", got)
	}
}

func TestAirPlay2Discover(t *testing.T) {
	a := &AirPlay2{browse: fakeBrowseService("_airplay._tcp", []*zeroconf.ServiceEntry{
		entry("Bedroom HomePod", "192.168.1.30", 7000, "model=AudioAccessory5,1", "features=0x4A7FCA00,0xBC354BD0"),
	}, nil)}
	devices, err := a.Discover(context.Background(), 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 {
		t.Fatalf("got %d devices", len(devices))
	}
	d := devices[0]
	if d.Protocol != "airplay2" || d.Name != "Bedroom HomePod" || d.Address != "192.168.1.30:7000" || d.Metadata["model"] != "AudioAccessory5,1" {
		t.Errorf("device %+v", d)
	}
}
