// Package api serves the REST API and the web UI.
//
// The API is documented in docs/API.md. Responses are JSON; errors have the
// form {"error": "message"} with a matching status code, plus a stable
// "code" and its "params" for errors in the i18n catalog.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/yeixio/air-mixer/i18n"
	"github.com/yeixio/air-mixer/internal/discovery"
	"github.com/yeixio/air-mixer/internal/protocols/httpstream"
	"github.com/yeixio/air-mixer/internal/protocols/matinee"
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
	player    Player
	log       *slog.Logger
	version   string
	streams   *httpstream.Registry
	matinee   *matinee.Account
}

// Options configures the API.
type Options struct {
	Service   *service.Service
	Discovery Discoverer
	// Player plays audio from URLs. Nil disables the playback endpoints.
	Player  Player
	Logger  *slog.Logger
	Version string
	// Streams serves stream and Matinee outputs' audio. Nil for none.
	Streams *httpstream.Registry
	// Matinee is the sign-in to Matinee. Nil turns /api/matinee off.
	Matinee *matinee.Account
}

// New returns the HTTP handler for the API and web UI.
func New(opts Options) http.Handler {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	a := &API{
		svc: opts.Service, discovery: opts.Discovery, player: opts.Player, log: opts.Logger.With("component", "api"), version: opts.Version,
		streams: opts.Streams, matinee: opts.Matinee,
	}

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

	a.registerPlayback(mux)
	// Matinee (matinee.go) and live streams of outputs.
	mux.HandleFunc("GET /api/clients/{id}/stream", a.clientStream)
	mux.HandleFunc("HEAD /api/clients/{id}/stream", a.clientStream)
	mux.HandleFunc("GET /api/matinee", a.matineeState)
	mux.HandleFunc("PUT /api/matinee", a.setMatinee)
	mux.HandleFunc("POST /api/matinee/link", a.linkMatinee)
	mux.HandleFunc("DELETE /api/matinee/link", a.unlinkMatinee)
	mux.HandleFunc("GET /api/matinee/players", a.matineePlayers)

	mux.HandleFunc("GET /api/status", a.status)
	mux.HandleFunc("GET /api/discovery", a.discoverAll)
	mux.HandleFunc("GET /api/discovery/{protocol}", a.discoverProtocol)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeCode(w, http.StatusNotFound, "no_such_endpoint", nil)
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
				writeCode(rec, http.StatusInternalServerError, "internal", nil)
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

// Flush lets streams send audio as it comes.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap gives http.ResponseController the real writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

type errorResponse struct {
	Error  string         `json:"error"`
	Code   string         `json:"code,omitempty"`
	Params map[string]any `json:"params,omitempty"`
}

// writeError sends an English message that has no code in the catalog.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

// writeCode sends an error from the i18n catalog: its code and params, so
// the web UI can show it in the reader's language, and its English text.
func writeCode(w http.ResponseWriter, status int, code string, params map[string]any) {
	writeJSON(w, status, errorResponse{Error: i18n.Message(code, params), Code: code, Params: params})
}

// fail maps a service error to a status code.
func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	var invalid *service.InvalidError
	switch {
	case errors.As(err, &invalid):
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: invalid.Reason, Code: invalid.Code, Params: invalid.Params})
	case errors.Is(err, service.ErrNotFound):
		writeCode(w, http.StatusNotFound, "not_found", nil)
	case errors.Is(err, service.ErrConflict):
		writeCode(w, http.StatusConflict, "conflict", nil)
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
			writeCode(w, http.StatusRequestEntityTooLarge, "body_too_large", nil)
		} else {
			writeCode(w, http.StatusBadRequest, "invalid_json", map[string]any{"detail": err.Error()})
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
	writeCode(w, http.StatusNotImplemented, "pairing_unsupported", nil)
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
		writeCode(w, http.StatusBadRequest, "bad_timeout", nil)
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
		writeCode(w, http.StatusBadRequest, "bad_timeout", nil)
		return
	}
	protocol := r.PathValue("protocol")
	devices, err := a.discovery.Discover(r.Context(), protocol, timeout)
	var unknown discovery.ErrUnknownProtocol
	switch {
	case errors.As(err, &unknown):
		writeCode(w, http.StatusNotFound, "discovery_unsupported", map[string]any{"protocol": string(unknown)})
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
