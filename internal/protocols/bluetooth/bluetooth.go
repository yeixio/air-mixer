// Package bluetooth makes the host a Bluetooth speaker: phones find it by
// name in their Bluetooth settings, pair without a PIN, and play to it
// over A2DP, and what they play becomes an Air-Mixer input.
//
// BlueZ does the Bluetooth part, driven over the system D-Bus: the input
// names the adapter, makes it discoverable and pairable, and registers a
// pairing agent that accepts audio devices. PipeWire (or PulseAudio with
// its Bluetooth module) turns a connected phone into an audio source,
// which the input records through package pulse.
package bluetooth

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/godbus/dbus/v5"

	"github.com/yeixio/air-mixer/internal/protocols"
	"github.com/yeixio/air-mixer/internal/protocols/pulse"
)

const (
	bluez       = "org.bluez"
	ifAdapter   = "org.bluez.Adapter1"
	ifDevice    = "org.bluez.Device1"
	ifAgent     = "org.bluez.Agent1"
	ifAgentMgr  = "org.bluez.AgentManager1"
	ifProps     = "org.freedesktop.DBus.Properties"
	agentPrefix = "/io/yeix/airmixer/agent/"
)

// Audio profiles a paired phone may use: A2DP to send audio, AVRCP for
// play/pause.
var audioUUIDs = []string{
	"0000110a-0000-1000-8000-00805f9b34fb", // A2DP source
	"0000110b-0000-1000-8000-00805f9b34fb", // A2DP sink
	"0000110c-0000-1000-8000-00805f9b34fb", // AVRCP target
	"0000110e-0000-1000-8000-00805f9b34fb", // AVRCP controller
	"0000110d-0000-1000-8000-00805f9b34fb", // advanced audio
}

// Options configures a Bluetooth input.
type Options struct {
	ID, Name string
	// Adapter is the Bluetooth adapter, such as "hci0" (the default).
	Adapter string
	// Device, if set, is the one phone to record, by Bluetooth address;
	// otherwise whichever phone plays.
	Device string
	// Hidden keeps the adapter out of other devices' scans, for phones
	// already paired.
	Hidden bool
	// Server is the PulseAudio server; empty for the default.
	Server string
	Logger *slog.Logger
}

// Server is a Bluetooth speaker input.
type Server struct {
	o       Options
	log     *slog.Logger
	capture *pulse.Source

	mu         sync.Mutex
	err        error // why Bluetooth setup failed, if it did
	unregister func()
	closeBus   func() error
}

// New returns a Bluetooth input.
func New(o Options) *Server {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Adapter == "" {
		o.Adapter = "hci0"
	}
	return &Server{
		o:   o,
		log: o.Logger.With("component", "bluetooth", "server", o.ID),
		capture: pulse.NewSource(pulse.SourceOptions{
			ID: o.ID, Name: o.Name, Protocol: "bluetooth", Server: o.Server,
			Source: o.Device, Bluetooth: o.Device == "", Logger: o.Logger,
		}),
	}
}

func (s *Server) ID() string                     { return s.o.ID }
func (s *Server) Name() string                   { return s.o.Name }
func (s *Server) Protocol() string               { return "bluetooth" }
func (s *Server) Audio() <-chan protocols.Packet { return s.capture.Audio() }

// Err is why the adapter couldn't be set up, or nil.
func (s *Server) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// systemBus connects to BlueZ; tests replace it.
var systemBus = func() (busConn, error) {
	c, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, err
	}
	return c, nil
}

// busConn is the part of *dbus.Conn the input uses.
type busConn interface {
	Object(dest string, path dbus.ObjectPath) dbus.BusObject
	Export(v any, path dbus.ObjectPath, iface string) error
	Close() error
}

// Start sets up the adapter and starts recording. Without BlueZ it still
// records phones paired by other means, and logs why pairing is off.
func (s *Server) Start(ctx context.Context) error {
	if err := s.capture.Start(ctx); err != nil {
		return err
	}
	if err := s.setup(); err != nil {
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
		s.log.Warn("Bluetooth pairing is off; phones paired with the host by hand still play", "err", err)
	}
	return nil
}

func (s *Server) setup() error {
	conn, err := systemBus()
	if err != nil {
		return fmt.Errorf("bluetooth: system D-Bus: %w", err)
	}
	adapter := conn.Object(bluez, dbus.ObjectPath("/org/bluez/"+s.o.Adapter))
	set := func(prop string, v any) error {
		return adapter.Call(ifProps+".Set", 0, ifAdapter, prop, dbus.MakeVariant(v)).Err
	}
	if err := set("Powered", true); err != nil {
		conn.Close()
		return fmt.Errorf("bluetooth: adapter %s: %w", s.o.Adapter, err)
	}
	steps := []struct {
		prop string
		v    any
	}{
		{"Alias", s.o.Name},
		{"Pairable", true},
		{"PairableTimeout", uint32(0)},
		{"DiscoverableTimeout", uint32(0)},
		{"Discoverable", !s.o.Hidden},
	}
	for _, st := range steps {
		if err := set(st.prop, st.v); err != nil {
			s.log.Warn("set adapter property", "property", st.prop, "err", err)
		}
	}

	path := dbus.ObjectPath(agentPrefix + sanitize(s.o.ID))
	if err := conn.Export(&agent{s: s, conn: conn}, path, ifAgent); err != nil {
		conn.Close()
		return fmt.Errorf("bluetooth: export agent: %w", err)
	}
	mgr := conn.Object(bluez, "/org/bluez")
	if err := mgr.Call(ifAgentMgr+".RegisterAgent", 0, path, "NoInputNoOutput").Err; err != nil {
		conn.Close()
		return fmt.Errorf("bluetooth: register pairing agent: %w", err)
	}
	if err := mgr.Call(ifAgentMgr+".RequestDefaultAgent", 0, path).Err; err != nil {
		s.log.Warn("another pairing agent stays the default", "err", err)
	}
	s.mu.Lock()
	s.closeBus = conn.Close
	s.unregister = func() {
		mgr.Call(ifAgentMgr+".UnregisterAgent", 0, path) //nolint:errcheck // shutting down
		set("Discoverable", false)                       //nolint:errcheck // shutting down
	}
	s.mu.Unlock()
	s.log.Info("Bluetooth speaker ready to pair", "adapter", s.o.Adapter, "name", s.o.Name, "discoverable", !s.o.Hidden)
	return nil
}

func sanitize(id string) string {
	var b strings.Builder
	for _, r := range id {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// Stop stops recording, unregisters the agent and hides the adapter.
func (s *Server) Stop() error {
	err := s.capture.Stop()
	s.mu.Lock()
	unregister, closeBus := s.unregister, s.closeBus
	s.unregister, s.closeBus = nil, nil
	s.mu.Unlock()
	if unregister != nil {
		unregister()
	}
	if closeBus != nil {
		closeBus() //nolint:errcheck // shutting down
	}
	return err
}

// agent is a BlueZ pairing agent with no display or keyboard: it accepts
// pairing and audio connections, so a phone pairs with one tap.
type agent struct {
	s    *Server
	conn busConn
}

var errRejected = dbus.NewError("org.bluez.Error.Rejected", []any{"rejected by Air-Mixer"})

func (a *agent) trust(device dbus.ObjectPath) {
	err := a.conn.Object(bluez, device).Call(ifProps+".Set", 0, ifDevice, "Trusted", dbus.MakeVariant(true)).Err
	if err != nil {
		a.s.log.Warn("trust device", "device", device, "err", err)
	}
}

// Release is called when BlueZ drops the agent.
func (a *agent) Release() *dbus.Error { return nil }

// RequestPinCode answers legacy pairing with the usual 0000.
func (a *agent) RequestPinCode(device dbus.ObjectPath) (string, *dbus.Error) {
	a.s.log.Info("pairing (PIN)", "device", device)
	a.trust(device)
	return "0000", nil
}

// DisplayPinCode would show a PIN; there is no display.
func (a *agent) DisplayPinCode(dbus.ObjectPath, string) *dbus.Error { return nil }

// RequestPasskey answers with 0.
func (a *agent) RequestPasskey(device dbus.ObjectPath) (uint32, *dbus.Error) {
	a.trust(device)
	return 0, nil
}

// DisplayPasskey would show a passkey; there is no display.
func (a *agent) DisplayPasskey(dbus.ObjectPath, uint32, uint16) *dbus.Error { return nil }

// RequestConfirmation accepts "just works" pairing.
func (a *agent) RequestConfirmation(device dbus.ObjectPath, _ uint32) *dbus.Error {
	a.s.log.Info("pairing", "device", device)
	a.trust(device)
	return nil
}

// RequestAuthorization accepts pairing a phone starts.
func (a *agent) RequestAuthorization(device dbus.ObjectPath) *dbus.Error {
	a.trust(device)
	return nil
}

// AuthorizeService lets paired devices use audio profiles only.
func (a *agent) AuthorizeService(device dbus.ObjectPath, uuid string) *dbus.Error {
	for _, u := range audioUUIDs {
		if strings.EqualFold(u, uuid) {
			return nil
		}
	}
	a.s.log.Info("refused a non-audio Bluetooth service", "device", device, "uuid", uuid)
	return errRejected
}

// Cancel is called when pairing is cancelled.
func (a *agent) Cancel() *dbus.Error { return nil }
