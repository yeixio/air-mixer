package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/yeixio/air-mixer/internal/config"
	"github.com/yeixio/air-mixer/internal/db"
	"github.com/yeixio/air-mixer/internal/protocols/airplay"
)

// listServers lists all servers
func (s *Server) listServers(w http.ResponseWriter, r *http.Request) {
	repo := db.NewServerRepository(s.db)
	servers, err := repo.List()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Ensure we return an empty array instead of null
	if servers == nil {
		servers = []*db.Server{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(servers)
}

// getServer gets a single server
func (s *Server) getServer(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	repo := db.NewServerRepository(s.db)
	server, err := repo.Get(id)
	if err != nil {
		s.writeError(w, http.StatusNotFound, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(server)
}

// createServer creates a new server
func (s *Server) createServer(w http.ResponseWriter, r *http.Request) {
	var server db.Server
	if err := json.NewDecoder(r.Body).Decode(&server); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if server.ID == "" {
		s.writeError(w, http.StatusBadRequest, "Server ID is required")
		return
	}

	// Default enabled to true for new servers if not explicitly set
	// Note: If the frontend doesn't send 'enabled', it defaults to false (Go zero value)
	// So we default to true for new servers to match expected behavior
	// The frontend should send enabled: true, but this is a safety net
	server.Enabled = true

	repo := db.NewServerRepository(s.db)
	if err := repo.Create(&server); err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Register with protocol manager if enabled
	if server.Enabled {
		if err := s.registerProtocolServer(&server); err != nil {
			log.Printf("Warning: failed to register server %s with protocol manager: %v", server.ID, err)
			// Don't fail the request, but log the error
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(server)
}

// updateServer updates an existing server
func (s *Server) updateServer(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	// Get existing server to check current enabled status
	repo := db.NewServerRepository(s.db)
	existingServer, err := repo.Get(id)
	if err != nil {
		s.writeError(w, http.StatusNotFound, err.Error())
		return
	}

	var server db.Server
	if err := json.NewDecoder(r.Body).Decode(&server); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	server.ID = id
	if err := repo.Update(&server); err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Handle protocol manager registration/unregistration
	if existingServer.Enabled && !server.Enabled {
		// Server was disabled, unregister it
		// It's OK if the server wasn't registered
		if err := s.protocolManager.UnregisterServer(id); err != nil {
			log.Printf("Warning: failed to unregister server %s: %v", id, err)
		}
	} else if !existingServer.Enabled && server.Enabled {
		// Server was enabled, register it
		if err := s.registerProtocolServer(&server); err != nil {
			log.Printf("Warning: failed to register server %s with protocol manager: %v", id, err)
		}
	} else if server.Enabled {
		// Server was already enabled, re-register it (in case config changed)
		// It's OK if the server wasn't registered before
		if err := s.protocolManager.UnregisterServer(id); err != nil {
			log.Printf("Warning: failed to unregister server %s during update: %v", id, err)
		}
		if err := s.registerProtocolServer(&server); err != nil {
			log.Printf("Warning: failed to re-register server %s with protocol manager: %v", id, err)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(server)
}

// deleteServer deletes a server
func (s *Server) deleteServer(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	// Unregister from protocol manager before deleting
	if err := s.protocolManager.UnregisterServer(id); err != nil {
		log.Printf("Warning: failed to unregister server %s: %v", id, err)
		// Continue with deletion even if unregister fails
	}

	repo := db.NewServerRepository(s.db)
	if err := repo.Delete(id); err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// registerProtocolServer creates and registers a protocol server based on database server model
func (s *Server) registerProtocolServer(server *db.Server) error {
	// Get config to access default settings
	cfg, err := config.Load()
	if err != nil {
		// If config load fails, use defaults
		cfg = &config.Config{
			Server: config.ServerConfig{
				AirPlayPort: 5000,
				Interface:   "",
			},
		}
	}

	// Parse server config
	var serverConfig map[string]string
	if server.Config != "" {
		if err := json.Unmarshal([]byte(server.Config), &serverConfig); err != nil {
			// If JSON parsing fails, try to treat as simple string
			log.Printf("Warning: failed to parse server config JSON for %s: %v", server.ID, err)
			serverConfig = make(map[string]string)
		}
	} else {
		serverConfig = make(map[string]string)
	}

	address := serverConfig["address"]
	if address == "" {
		address = fmt.Sprintf(":%d", cfg.Server.AirPlayPort)
	}

	iface := serverConfig["interface"]
	if iface == "" {
		iface = cfg.Server.Interface
	}

	// Create protocol server based on type
	switch server.Protocol {
	case "airplay":
		airplayServer := airplay.NewServer(server.ID, server.Name, address, iface)
		return s.protocolManager.RegisterServer(airplayServer)
	default:
		return fmt.Errorf("unknown protocol: %s", server.Protocol)
	}
}

