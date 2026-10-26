package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeixio/air-mixer/internal/db"
	"github.com/yeixio/air-mixer/internal/discovery"
	"github.com/yeixio/air-mixer/internal/protocols"
	"github.com/yeixio/air-mixer/internal/router"
	"github.com/yeixio/air-mixer/internal/service"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type nopServer struct{ id, name string }

func (s nopServer) ID() string                     { return s.id }
func (s nopServer) Name() string                   { return s.name }
func (s nopServer) Protocol() string               { return "fake" }
func (s nopServer) Start(context.Context) error    { return nil }
func (s nopServer) Stop() error                    { return nil }
func (s nopServer) Audio() <-chan protocols.Packet { return nil }

type nopClient struct{ id, name string }

func (c nopClient) ID() string                    { return c.id }
func (c nopClient) Name() string                  { return c.name }
func (c nopClient) Protocol() string              { return "fake" }
func (c nopClient) Connect(context.Context) error { return nil }
func (c nopClient) Disconnect() error             { return nil }
func (c nopClient) Connected() bool               { return true }
func (c nopClient) Send(protocols.Packet) error   { return nil }

type factory struct{}

func (factory) Server(v *db.Server) (protocols.Server, error) {
	if v.Protocol != "fake" {
		return nil, &service.InvalidError{Reason: "unsupported protocol"}
	}
	return nopServer{v.ID, v.Name}, nil
}

func (factory) Client(v *db.Client) (protocols.Client, error) {
	if v.Protocol != "fake" {
		return nil, &service.InvalidError{Reason: "unsupported protocol"}
	}
	return nopClient{v.ID, v.Name}, nil
}

type stubDiscovery struct {
	devices []discovery.Device
	err     error
}

func (s stubDiscovery) DiscoverAll(context.Context, time.Duration) ([]discovery.Device, error) {
	return s.devices, s.err
}

func (s stubDiscovery) Discover(_ context.Context, protocol string, _ time.Duration) ([]discovery.Device, error) {
	if protocol != "airplay" {
		return nil, discovery.ErrUnknownProtocol(protocol)
	}
	return s.devices, s.err
}

type testAPI struct {
	t       *testing.T
	handler http.Handler
	store   *db.DB
}

func newTestAPI(t *testing.T, disc Discoverer) *testAPI {
	t.Helper()
	store, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	r := router.New(quiet)
	r.Start()
	m := protocols.NewManager(r, quiet)
	t.Cleanup(func() { m.Close(); r.Stop(); store.Close() })
	if disc == nil {
		disc = stubDiscovery{}
	}
	h := New(Options{Service: service.New(store, r, m, factory{}, quiet), Discovery: disc, Logger: quiet, Version: "test"})
	return &testAPI{t: t, handler: h, store: store}
}

// do sends a request and decodes a JSON response into out, if given.
func (a *testAPI) do(method, path, body string, out any) *httptest.ResponseRecorder {
	a.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			a.t.Fatalf("%s %s: decode %q: %v", method, path, rec.Body.String(), err)
		}
	}
	return rec
}

func (a *testAPI) expect(method, path, body string, status int, out any) {
	a.t.Helper()
	if rec := a.do(method, path, body, out); rec.Code != status {
		a.t.Errorf("%s %s = %d %s, want %d", method, path, rec.Code, rec.Body.String(), status)
	}
}

func TestServers(t *testing.T) {
	a := newTestAPI(t, nil)
	var list []db.Server
	a.expect("GET", "/api/servers", "", 200, &list)
	if list == nil || len(list) != 0 {
		t.Errorf("empty list = %v; want []", list)
	}

	// The web UI sends config as a JSON string.
	var s db.Server
	a.expect("POST", "/api/servers", `{"id":"server-1","name":"Living room","protocol":"fake","config":"{\"interface\":\"en0\"}"}`, 201, &s)
	if s.ID != "server-1" || !s.Enabled || s.Config != `{"interface":"en0"}` || s.CreatedAt.IsZero() {
		t.Errorf("created %+v", s)
	}
	a.expect("POST", "/api/servers", `{"id":"server-1","name":"Again","protocol":"fake"}`, 409, nil)
	a.expect("POST", "/api/servers", `{"name":"x","protocol":"nope"}`, 400, nil)
	a.expect("POST", "/api/servers", `{"name":`, 400, nil)

	// An object config and a generated ID.
	var s2 db.Server
	a.expect("POST", "/api/servers", `{"name":"Den","protocol":"fake","enabled":false,"config":{"address": ":5001"}}`, 201, &s2)
	if !strings.HasPrefix(s2.ID, "server-") || s2.Enabled || s2.Config != `{"address":":5001"}` {
		t.Errorf("created %+v", s2)
	}

	// Partial update keeps omitted fields.
	a.expect("PUT", "/api/servers/server-1", `{"enabled":false}`, 200, &s)
	if s.Enabled || s.Name != "Living room" || s.Config != `{"interface":"en0"}` {
		t.Errorf("updated %+v", s)
	}
	a.expect("GET", "/api/servers/server-1", "", 200, &s)
	if s.Enabled {
		t.Error("update not saved")
	}
	a.expect("PUT", "/api/servers/missing", `{}`, 404, nil)
	a.expect("GET", "/api/servers/missing", "", 404, nil)
	a.expect("DELETE", "/api/servers/server-1", "", 204, nil)
	a.expect("DELETE", "/api/servers/server-1", "", 404, nil)
}

func TestClients(t *testing.T) {
	a := newTestAPI(t, nil)
	var c db.Client
	a.expect("POST", "/api/clients", `{"id":"client-1","name":"Kitchen","protocol":"fake","address":"10.0.0.5:7000"}`, 201, &c)
	if !c.Enabled || c.Address != "10.0.0.5:7000" {
		t.Errorf("created %+v", c)
	}
	var list []db.Client
	a.expect("GET", "/api/clients", "", 200, &list)
	if len(list) != 1 {
		t.Errorf("list %v", list)
	}
	a.expect("PUT", "/api/clients/client-1", `{"name":"Kitchen 2","config":null}`, 200, &c)
	if c.Name != "Kitchen 2" || c.Address != "10.0.0.5:7000" {
		t.Errorf("updated %+v", c)
	}
	a.expect("GET", "/api/clients/client-1", "", 200, &c)
	a.expect("POST", "/api/clients/client-1/pair", `{"pin":"1234"}`, 501, nil)
	a.expect("PUT", "/api/clients/missing", `{}`, 404, nil)
	a.expect("GET", "/api/clients/missing", "", 404, nil)
	a.expect("DELETE", "/api/clients/client-1", "", 204, nil)
	a.expect("DELETE", "/api/clients/client-1", "", 404, nil)
}

func TestRoutes(t *testing.T) {
	a := newTestAPI(t, nil)
	var list []router.RouteStatus
	a.expect("GET", "/api/routes", "", 200, &list)
	if list == nil {
		t.Error("routes list is null")
	}

	// The body the web UI sends.
	var r router.Route
	a.expect("POST", "/api/routes", `{"name":"Party","source_name":"Phone","dest_names":["Kitchen","Den"],"mix_mode":"replace","volume":0.8,"enabled":true,"priority":0}`, 201, &r)
	if r.ID == "" || r.Volume != 0.8 || len(r.DestNames) != 2 {
		t.Errorf("created %+v", r)
	}
	var defaults router.Route
	a.expect("POST", "/api/routes", `{"name":"Defaults","source_name":"Phone","dest_names":["Den"]}`, 201, &defaults)
	if defaults.Volume != 1 || !defaults.Enabled || defaults.MixMode != router.MixModeReplace {
		t.Errorf("defaults %+v", defaults)
	}
	a.expect("POST", "/api/routes", `{"name":"Bad","source_name":"Phone","dest_names":[]}`, 400, nil)

	var st router.RouteStatus
	a.expect("GET", "/api/routes/"+r.ID, "", 200, &st)
	if st.Name != "Party" || st.SourceAvailable {
		t.Errorf("status %+v", st)
	}
	a.expect("PUT", "/api/routes/"+r.ID, `{"enabled":false}`, 200, &r)
	if r.Enabled || r.Name != "Party" || r.Volume != 0.8 {
		t.Errorf("updated %+v", r)
	}
	a.expect("PUT", "/api/routes/"+r.ID, `{"volume":3}`, 400, nil)
	a.expect("PUT", "/api/routes/missing", `{}`, 404, nil)
	a.expect("GET", "/api/routes/missing", "", 404, nil)
	a.expect("DELETE", "/api/routes/"+r.ID, "", 204, nil)
	a.expect("DELETE", "/api/routes/"+r.ID, "", 404, nil)
}

func TestStatusAndHealth(t *testing.T) {
	a := newTestAPI(t, nil)
	a.expect("POST", "/api/servers", `{"id":"s","name":"S","protocol":"fake"}`, 201, nil)
	var st struct {
		RouterRunning bool               `json:"router_running"`
		RoutesActive  int                `json:"routes_active"`
		Adapters      []protocols.Status `json:"adapters"`
		Version       string             `json:"version"`
		Timestamp     int64              `json:"timestamp"`
	}
	a.expect("GET", "/api/status", "", 200, &st)
	if !st.RouterRunning || st.Version != "test" || st.Timestamp == 0 || len(st.Adapters) != 1 {
		t.Errorf("status %+v", st)
	}

	var h map[string]string
	a.expect("GET", "/health", "", 200, &h)
	if h["status"] != "ok" {
		t.Errorf("health %v", h)
	}
	a.store.Close()
	a.expect("GET", "/health", "", 503, &h)
}

func TestDiscovery(t *testing.T) {
	a := newTestAPI(t, stubDiscovery{devices: []discovery.Device{{Name: "Kitchen", Protocol: "airplay", Address: "10.0.0.5:7000"}}})
	var resp struct {
		Devices []discovery.Device `json:"devices"`
		Count   int                `json:"count"`
	}
	a.expect("GET", "/api/discovery?timeout=1s", "", 200, &resp)
	if resp.Count != 1 || resp.Devices[0].Name != "Kitchen" {
		t.Errorf("discovery %+v", resp)
	}
	a.expect("GET", "/api/discovery/airplay", "", 200, &resp)
	a.expect("GET", "/api/discovery/cast", "", 404, nil)
	a.expect("GET", "/api/discovery?timeout=forever", "", 400, nil)
	a.expect("GET", "/api/discovery/airplay?timeout=1h", "", 400, nil)

	failing := newTestAPI(t, stubDiscovery{err: errors.New("no multicast")})
	failing.expect("GET", "/api/discovery", "", 502, nil)
	failing.expect("GET", "/api/discovery/airplay", "", 502, nil)
}

func TestMisc(t *testing.T) {
	a := newTestAPI(t, nil)
	rec := a.do("GET", "/", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("web UI: %d %v", rec.Code, rec.Header())
	}
	a.expect("GET", "/api/nope", "", 404, nil)

	big := `{"name":"` + string(bytes.Repeat([]byte("a"), maxBody)) + `"}`
	a.expect("POST", "/api/servers", big, 413, nil)
}

func TestPanicRecovery(t *testing.T) {
	a := &API{log: quiet}
	h := a.middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 500 {
		t.Errorf("panic gave %d", rec.Code)
	}
}
