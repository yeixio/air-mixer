// Package api serves the REST API and the web UI.
//
// The API is documented in docs/API.md. Responses are JSON; errors have the
// form {"error": "message"} with a matching status code.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/yeixio/air-mixer/internal/discovery"
	"github.com/yeixio/air-mixer/internal/service"
	"github.com/yeixio/air-mixer/webui"
)

// maxBody bounds request bodies.
const maxBody = 1 << 20

// Discoverer finds devices on the network.
type Discoverer interface {
	DiscoverAll(ctx context.Context, timeout time.Duration) ([]discovery.Device, error)
	Discover(ctx context.Context, protocol string, timeout time.Duration) ([]discovery.Device, error)
}

// API holds the HTTP handlers.
type API struct {
	svc       *service.Service
	discovery Discoverer
	log       *slog.Logger
	version   string
}

// Options configures the API.
type Options struct {
	Service   *service.Service
	Discovery Discoverer
	Logger    *slog.Logger
	Version   string
}

// New returns the HTTP handler for the API and web UI.
func New(opts Options) http.Handler {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	a := &API{svc: opts.Service, discovery: opts.Discovery, log: opts.Logger.With("component", "api"), version: opts.Version}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/servers", a.listServers)
	mux.HandleFunc("POST /api/servers", a.createServer)
	mux.HandleFunc("GET /api/servers/{id}", a.getServer)
	mux.HandleFunc("PUT /api/servers/{id}", a.updateServer)
	mux.HandleFunc("DELETE /api/servers/{id}", a.deleteServer)

	mux.HandleFunc("GET /api/clients", a.listClients)
	mux.HandleFunc("POST /api/clients", a.createClient)
	mux.HandleFunc("GET /api/clients/{id}", a.getClient)
	mux.HandleFunc("PUT /api/clients/{id}", a.updateClient)
	mux.HandleFunc("DELETE /api/clients/{id}", a.deleteClient)
	mux.HandleFunc("POST /api/clients/{id}/pair", a.pairClient)

	mux.HandleFunc("GET /api/routes", a.listRoutes)
	mux.HandleFunc("POST /api/routes", a.createRoute)
	mux.HandleFunc("GET /api/routes/{id}", a.getRoute)
	mux.HandleFunc("PUT /api/routes/{id}", a.updateRoute)
	mux.HandleFunc("DELETE /api/routes/{id}", a.deleteRoute)

	mux.HandleFunc("GET /api/status", a.status)
	mux.HandleFunc("GET /api/discovery", a.discoverAll)
	mux.HandleFunc("GET /api/discovery/{protocol}", a.discoverProtocol)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint")
	})

	mux.HandleFunc("GET /health", a.health)
	mux.Handle("/", webui.Handler())

	return a.middleware(mux)
}

// middleware adds panic recovery, request logging and common headers.
func (a *API) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			if v := recover(); v != nil {
				a.log.Error("panic in handler", "path", r.URL.Path, "panic", v, "stack", string(debug.Stack()))
				writeError(rec, http.StatusInternalServerError, "internal error")
			}
			a.log.Debug("request", "method", r.Method, "path", r.URL.Path, "status", rec.status, "duration", time.Since(start))
		}()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		next.ServeHTTP(rec, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

type errorResponse struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

// fail maps a service error to a status code.
func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	var invalid *service.InvalidError
	switch {
	case errors.As(err, &invalid):
		writeError(w, http.StatusBadRequest, invalid.Reason)
	case errors.Is(err, service.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, service.ErrConflict):
		writeError(w, http.StatusConflict, "an item with this id already exists")
	default:
		a.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// decode reads a JSON body. It reports false after writing an error.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		} else {
			writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		}
		return false
	}
	return true
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	if err := a.svc.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unhealthy", "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": a.version})
}

type statusResponse struct {
	service.Status
	Version   string `json:"version"`
	Timestamp int64  `json:"timestamp"`
}

func (a *API) status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, statusResponse{Status: a.svc.Status(), Version: a.version, Timestamp: time.Now().Unix()})
}

// pairClient answers the pairing endpoint the web UI calls for devices that
// need AirPlay 2 pairing, which is not implemented yet.
func (a *API) pairClient(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "AirPlay 2 pairing is not supported yet; see ROADMAP.md")
}

const (
	defaultDiscoveryTimeout = 5 * time.Second
	maxDiscoveryTimeout     = 30 * time.Second
)

func discoveryTimeout(r *http.Request) (time.Duration, bool) {
	s := r.URL.Query().Get("timeout")
	if s == "" {
		return defaultDiscoveryTimeout, true
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 || d > maxDiscoveryTimeout {
		return 0, false
	}
	return d, true
}

func (a *API) discoverAll(w http.ResponseWriter, r *http.Request) {
	timeout, ok := discoveryTimeout(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "timeout must be a duration between 0 and 30s, such as 5s")
		return
	}
	devices, err := a.discovery.DiscoverAll(r.Context(), timeout)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devices, "count": len(devices)})
}

func (a *API) discoverProtocol(w http.ResponseWriter, r *http.Request) {
	timeout, ok := discoveryTimeout(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "timeout must be a duration between 0 and 30s, such as 5s")
		return
	}
	protocol := r.PathValue("protocol")
	devices, err := a.discovery.Discover(r.Context(), protocol, timeout)
	var unknown discovery.ErrUnknownProtocol
	switch {
	case errors.As(err, &unknown):
		writeError(w, http.StatusNotFound, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if devices == nil {
		devices = []discovery.Device{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"protocol": protocol, "devices": devices, "count": len(devices)})
}
