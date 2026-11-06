package bluetooth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/godbus/dbus/v5"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type fakeBus struct {
	mu       sync.Mutex
	calls    []string
	props    map[string]any
	exported any
	closed   bool
	fail     error
}

type fakeObject struct {
	dbus.BusObject
	bus  *fakeBus
	path dbus.ObjectPath
}

func (o fakeObject) Call(method string, _ dbus.Flags, args ...any) *dbus.Call {
	o.bus.mu.Lock()
	defer o.bus.mu.Unlock()
	o.bus.calls = append(o.bus.calls, method)
	if o.bus.fail != nil {
		return &dbus.Call{Err: o.bus.fail}
	}
	if method == ifProps+".Set" {
		o.bus.props[string(o.path)+" "+args[1].(string)] = args[2].(dbus.Variant).Value()
	}
	return &dbus.Call{}
}

func (b *fakeBus) Object(_ string, path dbus.ObjectPath) dbus.BusObject {
	return fakeObject{bus: b, path: path}
}

func (b *fakeBus) Export(v any, _ dbus.ObjectPath, _ string) error {
	b.mu.Lock()
	b.exported = v
	b.mu.Unlock()
	return nil
}

func (b *fakeBus) Close() error { b.mu.Lock(); b.closed = true; b.mu.Unlock(); return nil }

func withBus(t *testing.T, b *fakeBus, err error) {
	orig := systemBus
	systemBus = func() (busConn, error) {
		if err != nil {
			return nil, err
		}
		return b, nil
	}
	t.Cleanup(func() { systemBus = orig })
}

func TestSetsUpAdapterAndAgent(t *testing.T) {
	b := &fakeBus{props: map[string]any{}}
	withBus(t, b, nil)
	s := New(Options{ID: "bt-1", Name: "Kitchen speaker", Logger: quiet})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
	for prop, want := range map[string]any{
		"Powered": true, "Alias": "Kitchen speaker", "Pairable": true, "Discoverable": true, "DiscoverableTimeout": uint32(0),
	} {
		if got := b.props["/org/bluez/hci0 "+prop]; got != want {
			t.Errorf("%s = %v, want %v", prop, got, want)
		}
	}
	a, ok := b.exported.(*agent)
	if !ok {
		t.Fatalf("exported %T", b.exported)
	}
	// Pairing is accepted and the phone is trusted; only audio services
	// are allowed.
	if err := a.RequestConfirmation("/org/bluez/hci0/dev_11_22_33_44_55_66", 123456); err != nil {
		t.Error(err)
	}
	if b.props["/org/bluez/hci0/dev_11_22_33_44_55_66 Trusted"] != true {
		t.Error("phone not trusted")
	}
	if err := a.AuthorizeService("/d", "0000110D-0000-1000-8000-00805F9B34FB"); err != nil {
		t.Error("A2DP refused")
	}
	if err := a.AuthorizeService("/d", "00001105-0000-1000-8000-00805f9b34fb"); err == nil {
		t.Error("OBEX file transfer allowed")
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	if !b.closed || b.props["/org/bluez/hci0 Discoverable"] != false {
		t.Error("Stop didn't hide the adapter and close the bus")
	}
	found := false
	for _, c := range b.calls {
		found = found || c == ifAgentMgr+".UnregisterAgent"
	}
	if !found {
		t.Error("agent not unregistered")
	}
}

func TestWorksWithoutBlueZ(t *testing.T) {
	withBus(t, nil, errors.New("no system bus"))
	s := New(Options{ID: "bt", Name: "BT", Logger: quiet})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	if s.Err() == nil {
		t.Error("no setup error reported")
	}
	if s.Protocol() != "bluetooth" {
		t.Error(s.Protocol())
	}
}
