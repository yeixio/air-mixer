package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/gopherstein/air-mixer/internal/db"
	"github.com/gopherstein/air-mixer/internal/router"
)

// listRoutes lists all routes
func (s *Server) listRoutes(w http.ResponseWriter, r *http.Request) {
	// Get route statuses (includes active status)
	statuses := s.audioRouter.ListRouteStatus()

	// Ensure we return an empty array instead of null
	if statuses == nil {
		statuses = []router.RouteStatus{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(statuses)
}

// getRoute gets a single route
func (s *Server) getRoute(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	route, err := s.audioRouter.GetRoute(id)
	if err != nil {
		s.writeError(w, http.StatusNotFound, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(route)
}

// createRouteRequest represents a route creation request
type createRouteRequest struct {
	Name      string   `json:"name"`
	SourceName string   `json:"source_name"`
	DestNames  []string `json:"dest_names"`
	MixMode   string   `json:"mix_mode"`
	Volume    float32  `json:"volume"`
	Enabled   bool     `json:"enabled"`
	Priority  int      `json:"priority"`
}

// createRoute creates a new route
func (s *Server) createRoute(w http.ResponseWriter, r *http.Request) {
	var req createRouteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Generate ID if not provided (in real app, use UUID)
	routeID := generateID()

	route := router.Route{
		ID:         routeID,
		Name:       req.Name,
		SourceName: req.SourceName,
		DestNames:  req.DestNames,
		MixMode:    router.MixMode(req.MixMode),
		Volume:     req.Volume,
		Enabled:    req.Enabled,
		Priority:   req.Priority,
	}

	if route.MixMode == "" {
		route.MixMode = router.MixModeReplace
	}
	if route.Volume == 0 {
		route.Volume = 1.0
	}

	// Save to database first
	routeRepo := db.NewRouteRepository(s.db)
	dbRoute := &db.Route{
		ID:         route.ID,
		Name:       route.Name,
		SourceName: route.SourceName,
		DestNames:  strings.Join(route.DestNames, ","), // Convert []string to comma-separated string
		MixMode:    string(route.MixMode),
		Volume:     float64(route.Volume),
		Enabled:    route.Enabled,
		Priority:   route.Priority,
	}
	if err := routeRepo.Create(dbRoute); err != nil {
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to save route to database: %v", err))
		return
	}

	// Add to router (will activate if enabled and source stream is available)
	if err := s.audioRouter.AddRoute(route); err != nil {
		// If router add fails, try to delete from database to keep consistency
		routeRepo.Delete(route.ID)
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(route)
}

// updateRoute updates an existing route
func (s *Server) updateRoute(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	var req createRouteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	route := router.Route{
		ID:         id,
		Name:       req.Name,
		SourceName: req.SourceName,
		DestNames:  req.DestNames,
		MixMode:    router.MixMode(req.MixMode),
		Volume:     req.Volume,
		Enabled:    req.Enabled,
		Priority:   req.Priority,
	}

	if route.MixMode == "" {
		route.MixMode = router.MixModeReplace
	}

	// Update in database
	routeRepo := db.NewRouteRepository(s.db)
	dbRoute := &db.Route{
		ID:         route.ID,
		Name:       route.Name,
		SourceName: route.SourceName,
		DestNames:  strings.Join(route.DestNames, ","), // Convert []string to comma-separated string
		MixMode:    string(route.MixMode),
		Volume:     float64(route.Volume),
		Enabled:    route.Enabled,
		Priority:   route.Priority,
	}
	if err := routeRepo.Update(dbRoute); err != nil {
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to update route in database: %v", err))
		return
	}

	// Update in router
	if err := s.audioRouter.UpdateRoute(id, route); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(route)
}

// deleteRoute deletes a route
func (s *Server) deleteRoute(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	// Remove from router first
	if err := s.audioRouter.RemoveRoute(id); err != nil {
		s.writeError(w, http.StatusNotFound, err.Error())
		return
	}

	// Delete from database
	routeRepo := db.NewRouteRepository(s.db)
	if err := routeRepo.Delete(id); err != nil {
		// Log error but don't fail the request since router removal succeeded
		fmt.Printf("Warning: failed to delete route %s from database: %v\n", id, err)
	}

	w.WriteHeader(http.StatusNoContent)
}

// generateID generates a simple ID (in production, use UUID)
func generateID() string {
	// Simple implementation - in production use github.com/google/uuid
	return strings.ReplaceAll(fmt.Sprintf("%d", time.Now().UnixNano()), "-", "")
}

