package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/mux"
)

// discoverDevices discovers devices across all protocols
func (s *Server) discoverDevices(w http.ResponseWriter, r *http.Request) {
	// Get timeout from query parameter (default 5 seconds)
	timeout := 5 * time.Second
	if timeoutStr := r.URL.Query().Get("timeout"); timeoutStr != "" {
		if parsed, err := time.ParseDuration(timeoutStr); err == nil {
			timeout = parsed
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout+1*time.Second)
	defer cancel()

	devices, err := s.discovery.DiscoverAll(ctx, timeout)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"devices": devices,
		"count":   len(devices),
	})
}

// discoverDevicesByProtocol discovers devices for a specific protocol
func (s *Server) discoverDevicesByProtocol(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	protocol := vars["protocol"]

	// Get timeout from query parameter (default 5 seconds)
	timeout := 5 * time.Second
	if timeoutStr := r.URL.Query().Get("timeout"); timeoutStr != "" {
		if parsed, err := time.ParseDuration(timeoutStr); err == nil {
			timeout = parsed
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout+1*time.Second)
	defer cancel()

	devices, err := s.discovery.DiscoverByProtocol(ctx, protocol, timeout)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"protocol": protocol,
		"devices": devices,
		"count":   len(devices),
	})
}

