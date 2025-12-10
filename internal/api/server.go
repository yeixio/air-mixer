package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/gopherstein/air-mixer/internal/db"
	"github.com/gopherstein/air-mixer/internal/discovery"
	"github.com/gopherstein/air-mixer/internal/logger"
	"github.com/gopherstein/air-mixer/internal/protocols"
	"github.com/gopherstein/air-mixer/internal/router"
	"github.com/gopherstein/air-mixer/webui"
)

// Server represents the REST API server
type Server struct {
	router          *mux.Router
	db              *db.DB
	audioRouter     *router.AudioRouter
	protocolManager *protocols.Manager
	httpServer      *http.Server
	discovery       *discovery.Service
}

// NewServer creates a new API server
func NewServer(db *db.DB, audioRouter *router.AudioRouter, protocolManager *protocols.Manager, port int) *Server {
	r := mux.NewRouter()
	
	s := &Server{
		router:          r,
		db:              db,
		audioRouter:     audioRouter,
		protocolManager: protocolManager,
		discovery:       discovery.NewService(),
		httpServer: &http.Server{
			Addr:         fmt.Sprintf(":%d", port),
			Handler:      r,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 15 * time.Second,
			IdleTimeout:  60 * time.Second,
		},
	}

	s.setupRoutes()
	return s
}

// setupRoutes sets up all API routes
func (s *Server) setupRoutes() {
	api := s.router.PathPrefix("/api").Subrouter()
	
	// Servers (input)
	api.HandleFunc("/servers", s.listServers).Methods("GET")
	api.HandleFunc("/servers", s.createServer).Methods("POST")
	api.HandleFunc("/servers/{id}", s.getServer).Methods("GET")
	api.HandleFunc("/servers/{id}", s.updateServer).Methods("PUT")
	api.HandleFunc("/servers/{id}", s.deleteServer).Methods("DELETE")

	// Clients (output)
	api.HandleFunc("/clients", s.listClients).Methods("GET")
	api.HandleFunc("/clients", s.createClient).Methods("POST")
	api.HandleFunc("/clients/{id}", s.getClient).Methods("GET")
	api.HandleFunc("/clients/{id}", s.updateClient).Methods("PUT")
	api.HandleFunc("/clients/{id}", s.deleteClient).Methods("DELETE")
	api.HandleFunc("/clients/{id}/pair", s.pairClient).Methods("POST") // Pairing endpoint

	// Routes
	api.HandleFunc("/routes", s.listRoutes).Methods("GET")
	api.HandleFunc("/routes", s.createRoute).Methods("POST")
	api.HandleFunc("/routes/{id}", s.getRoute).Methods("GET")
	api.HandleFunc("/routes/{id}", s.updateRoute).Methods("PUT")
	api.HandleFunc("/routes/{id}", s.deleteRoute).Methods("DELETE")

	// Status
	api.HandleFunc("/status", s.getStatus).Methods("GET")

	// Discovery
	api.HandleFunc("/discovery", s.discoverDevices).Methods("GET")
	api.HandleFunc("/discovery/{protocol}", s.discoverDevicesByProtocol).Methods("GET")

	// Health check
	s.router.HandleFunc("/health", s.healthCheck).Methods("GET")
	
	// Web UI (serve at root, but after API routes)
	// Serve web UI for all non-API routes
	s.router.PathPrefix("/").HandlerFunc(webui.RootHandler)
}

// Start starts the HTTP server
func (s *Server) Start() error {
	log := logger.GetLogger()
	log.Infof("API server listening on %s", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

// Stop stops the HTTP server
func (s *Server) Stop() error {
	return s.httpServer.Close()
}

// GetAddr returns the server address (for logging)
func (s *Server) GetAddr() string {
	return s.httpServer.Addr
}

// healthCheck returns a simple health check response
func (s *Server) healthCheck(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// getStatus returns system status
func (s *Server) getStatus(w http.ResponseWriter, r *http.Request) {
	status := map[string]interface{}{
		"router_running": s.audioRouter.IsRunning(),
		"routes_active":  len(s.audioRouter.ListRouteStatus()),
		"timestamp":      time.Now().Unix(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// ErrorResponse represents an error response
type ErrorResponse struct {
	Error string `json:"error"`
}

// writeError writes an error response
func (s *Server) writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(ErrorResponse{Error: message})
}

