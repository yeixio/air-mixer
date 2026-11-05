package api

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeixio/air-mixer/internal/db"
	"github.com/yeixio/air-mixer/internal/protocols"
	"github.com/yeixio/air-mixer/internal/protocols/httpstream"
	"github.com/yeixio/air-mixer/internal/protocols/matinee"
	"github.com/yeixio/air-mixer/internal/router"
	"github.com/yeixio/air-mixer/internal/service"
)

// newMatineeAPI is an API with the real adapters for streams and Matinee.
func newMatineeAPI(t *testing.T) *testAPI {
	t.Helper()
	store, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	r := router.New(quiet)
	r.Start()
	m := protocols.NewManager(r, quiet)
	streams := httpstream.NewRegistry()
	account := matinee.NewAccount(context.Background(), store, "", quiet)
	t.Cleanup(func() { m.Close(); r.Stop(); account.Close(); store.Close() })
	f := service.NewFactory(service.Defaults{AirPlayPort: 5000, WebPort: 9001, Streams: streams, Matinee: account, Settings: store}, quiet)
	h := New(Options{Service: service.New(store, r, m, f, quiet), Discovery: stubDiscovery{}, Logger: quiet, Version: "test", Streams: streams, Matinee: account})
	return &testAPI{t: t, handler: h, store: store}
}

func TestMatineeSettings(t *testing.T) {
	a := newMatineeAPI(t)
	var st matinee.State
	a.expect("GET", "/api/matinee", "", http.StatusOK, &st)
	if st.URL != "" || st.SignedIn {
		t.Fatalf("new: %+v", st)
	}
	a.expect("PUT", "/api/matinee", `{"url":"ftp://nope"}`, http.StatusBadRequest, nil)
	a.expect("PUT", "/api/matinee", `{"url":"192.168.1.20:7171/"}`, http.StatusOK, &st)
	if st.URL != "http://192.168.1.20:7171" {
		t.Fatalf("url: %+v", st)
	}
	if v, _ := a.store.Setting(context.Background(), "matinee.url"); v != st.URL {
		t.Fatalf("saved url %q", v)
	}
	a.expect("GET", "/api/matinee/players", "", http.StatusConflict, nil)
	a.expect("DELETE", "/api/matinee/link", "", http.StatusNoContent, nil)

	// A Matinee input starts without a sign-in (it keeps trying), and a
	// Matinee output needs a player.
	a.expect("POST", "/api/servers", `{"name":"Kitchen","protocol":"matinee"}`, http.StatusCreated, nil)
	a.expect("POST", "/api/clients", `{"name":"TV","protocol":"matinee"}`, http.StatusBadRequest, nil)
	a.expect("POST", "/api/clients", `{"name":"TV","protocol":"matinee","address":"tv-0001"}`, http.StatusCreated, nil)
}

func TestMatineeOff(t *testing.T) {
	a := newTestAPI(t, nil)
	a.expect("GET", "/api/matinee", "", http.StatusNotFound, nil)
	a.expect("GET", "/api/clients/x/stream", "", http.StatusNotFound, nil)
}

// A stream output is served at /api/clients/{id}/stream once connected.
func TestClientStream(t *testing.T) {
	a := newMatineeAPI(t)
	var c db.Client
	a.expect("POST", "/api/clients", `{"id":"tap","name":"Tap","protocol":"stream"}`, http.StatusCreated, &c)
	var rec interface{ Header() http.Header }
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		r := a.do("HEAD", "/api/clients/tap/stream", "", nil)
		if r.Code == http.StatusOK {
			rec = r
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stream: %d %s", r.Code, r.Body.String())
		}
	}
	if ct := rec.Header().Get("Content-Type"); ct != "audio/wav" {
		t.Fatalf("content type %q", ct)
	}
	a.expect("GET", "/api/clients/nope/stream", "", http.StatusNotFound, nil)
}
