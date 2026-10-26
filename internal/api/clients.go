package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/yeixio/air-mixer/internal/db"
	"github.com/yeixio/air-mixer/internal/protocols/airplay"
	"github.com/yeixio/air-mixer/internal/protocols/local"
)

// listClients lists all clients
func (s *Server) listClients(w http.ResponseWriter, r *http.Request) {
	repo := db.NewClientRepository(s.db)
	clients, err := repo.List()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Ensure we return an empty array instead of null
	if clients == nil {
		clients = []*db.Client{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(clients)
}

// getClient gets a single client
func (s *Server) getClient(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	repo := db.NewClientRepository(s.db)
	client, err := repo.Get(id)
	if err != nil {
		s.writeError(w, http.StatusNotFound, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(client)
}

// createClient creates a new client
func (s *Server) createClient(w http.ResponseWriter, r *http.Request) {
	var client db.Client
	if err := json.NewDecoder(r.Body).Decode(&client); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if client.ID == "" {
		s.writeError(w, http.StatusBadRequest, "Client ID is required")
		return
	}

	// Clients are enabled by default (output clients should be active)
	// Default to true for new clients - users can disable via update if needed
	client.Enabled = true

	// Try to register with protocol manager FIRST (before creating in database)
	// This way if registration fails, we don't leave orphaned records in the database
	if client.Enabled {
		if err := s.registerProtocolClient(&client); err != nil {
			// Check if error indicates pairing is required
			errStr := err.Error()
			log.Printf("[Create Client] Registration error for %s: %s", client.ID, errStr)
			if strings.Contains(errStr, "PAIRING_REQUIRED") {
				log.Printf("[Create Client] Detected PAIRING_REQUIRED for client %s, starting pairing flow", client.ID)
				// Pairing is required - DO NOT create client in DB yet
				// Start pairing process first
				airplayClient, pairErr := airplay.NewClient(client.ID, client.Name, client.Address, 44100, 2)
				if pairErr != nil {
					s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create AirPlay client for pairing: %v", pairErr))
					return
				}
				
				deviceID, pairErr := airplayClient.StartPairing()
				if pairErr != nil {
					s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to start pairing: %v", pairErr))
					return
				}
				
				// Store client data temporarily in the response (not in DB)
				// The pairing endpoint will create the client in DB after successful pairing
				response := map[string]interface{}{
					"id":              client.ID,
					"name":            client.Name,
					"protocol":        client.Protocol,
					"address":         client.Address,
					"pairing_device_id": deviceID,
					"pairing_required": true,
					"message":         "Enter the PIN code displayed on your Apple TV",
				}
				log.Printf("[Create Client] Returning 202 Accepted with pairing_required=true for client %s (client not yet in DB)", client.ID)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted) // 202 Accepted - pairing in progress
				if err := json.NewEncoder(w).Encode(response); err != nil {
					log.Printf("[Create Client] Error encoding pairing response: %v", err)
				}
				return
			}
			s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to register client %s with protocol manager: %v", client.ID, err))
			return
		}
	}

	// Only create in database if registration succeeded
	repo := db.NewClientRepository(s.db)
	if err := repo.Create(&client); err != nil {
		// Registration succeeded but database insert failed - unregister to clean up
		if client.Enabled {
			if unregErr := s.protocolManager.UnregisterClient(client.ID); unregErr != nil {
				log.Printf("Warning: failed to unregister client %s after database insert failure: %v", client.ID, unregErr)
			}
		}
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(client)
}

// updateClient updates an existing client
func (s *Server) updateClient(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	var client db.Client
	if err := json.NewDecoder(r.Body).Decode(&client); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	client.ID = id
	repo := db.NewClientRepository(s.db)
	if err := repo.Update(&client); err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Unregister old client if it exists
	if err := s.protocolManager.UnregisterClient(id); err != nil {
		// Log but don't fail - client might not be registered
		log.Printf("Note: failed to unregister client %s (may not have been registered): %v", id, err)
	}

	// Register with protocol manager if enabled
	if client.Enabled {
		if err := s.registerProtocolClient(&client); err != nil {
			s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to register client %s with protocol manager: %v", client.ID, err))
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(client)
}

// deleteClient deletes a client
func (s *Server) deleteClient(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	log.Printf("[Delete Client] Starting deletion of client: %s", id)

	// First, verify the client exists
	repo := db.NewClientRepository(s.db)
	client, err := repo.Get(id)
	if err != nil {
		log.Printf("[Delete Client] Client %s not found in database: %v", id, err)
		s.writeError(w, http.StatusNotFound, fmt.Sprintf("Client not found: %s", id))
		return
	}
	log.Printf("[Delete Client] Found client: %s (name: %s, protocol: %s, enabled: %v)", id, client.Name, client.Protocol, client.Enabled)

	// Unregister from protocol manager before deleting
	if err := s.protocolManager.UnregisterClient(id); err != nil {
		log.Printf("[Delete Client] Warning: failed to unregister client %s from protocol manager: %v (continuing with deletion)", id, err)
		// Continue with deletion even if unregister fails
	} else {
		log.Printf("[Delete Client] Successfully unregistered client %s from protocol manager", id)
	}

	// Delete from database
	if err := repo.Delete(id); err != nil {
		log.Printf("[Delete Client] Error deleting client %s from database: %v", id, err)
		// Check if it's a "not found" error
		if strings.Contains(err.Error(), "not found") {
			s.writeError(w, http.StatusNotFound, err.Error())
		} else {
			s.writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	// Verify deletion
	_, err = repo.Get(id)
	if err == nil {
		log.Printf("[Delete Client] ERROR: Client %s still exists after deletion!", id)
		s.writeError(w, http.StatusInternalServerError, "Client deletion failed - client still exists")
		return
	}

	log.Printf("[Delete Client] Successfully deleted client: %s (verified)", id)
	w.WriteHeader(http.StatusNoContent)
}

// registerProtocolClient creates and registers a protocol client based on database client model
func (s *Server) registerProtocolClient(client *db.Client) error {
	clientProtocol := client.Protocol
	
	// Create protocol client based on type
	switch clientProtocol {
	case "airplay":
		// Parse address from client config or use address field
		address := client.Address
		if address == "" {
			// Try to get from config
			var config map[string]interface{}
			if client.Config != "" {
				if err := json.Unmarshal([]byte(client.Config), &config); err == nil {
					if addr, ok := config["address"].(string); ok {
						address = addr
					}
				}
			}
			if address == "" {
				return fmt.Errorf("AirPlay client %s requires an address", client.ID)
			}
		}
		
		// Default to 44.1kHz stereo if not specified
		sampleRate := 44100
		channels := 2
		
		// Parse config if available
		var config map[string]interface{}
		if client.Config != "" {
			if err := json.Unmarshal([]byte(client.Config), &config); err == nil {
				if sr, ok := config["sample_rate"].(float64); ok {
					sampleRate = int(sr)
				}
				if ch, ok := config["channels"].(float64); ok {
					channels = int(ch)
				}
			}
		}
		
		airplayClient, err := airplay.NewClient(client.ID, client.Name, address, sampleRate, channels)
		if err != nil {
			return fmt.Errorf("failed to create AirPlay client: %w", err)
		}
		
		// Check if pairing credentials exist before attempting connection
		// Following pyatv's approach: check credentials first, pair if needed, then connect
		hasCredentials := false
		if client.Config != "" {
			var config map[string]interface{}
			if err := json.Unmarshal([]byte(client.Config), &config); err == nil {
				if deviceID, ok := config["device_id"].(string); ok && deviceID != "" {
					if privateKey, ok := config["private_key"].(string); ok && privateKey != "" {
						airplayClient.SetPairingCredentials(deviceID, privateKey)
						hasCredentials = true
						log.Printf("Loaded pairing credentials for AirPlay client %s", client.ID)
					}
				}
			}
		}
		
		// If no credentials exist, we should attempt pairing first (like pyatv does)
		// However, we can't know for sure if pairing is required until we try to connect
		// So we'll attempt connection and detect pairing requirement from the error
		// This matches pyatv's behavior where it tries to connect and handles pairing errors
		
		// Connect the client
		if err := airplayClient.Connect(""); err != nil {
			// Check if error indicates pairing is required
			errStr := err.Error()
			log.Printf("[Register Protocol Client] Connect error for AirPlay client %s: %s", client.ID, errStr)
			
			// Check for pairing requirement indicators
			// pyatv detects this when RTSP returns 403 without authentication headers
			if strings.Contains(errStr, "PAIRING_REQUIRED") {
				log.Printf("[Register Protocol Client] Detected PAIRING_REQUIRED for client %s", client.ID)
				return fmt.Errorf("PAIRING_REQUIRED: %w", err)
			}
			if strings.Contains(errStr, "requires pairing") || strings.Contains(errStr, "pairing first") {
				log.Printf("[Register Protocol Client] Detected pairing requirement for client %s", client.ID)
				return fmt.Errorf("PAIRING_REQUIRED: %w", err)
			}
			
			// If we have no credentials and got a 403/401 error, it likely needs pairing
			if !hasCredentials && (strings.Contains(errStr, "403") || strings.Contains(errStr, "401")) {
				log.Printf("[Register Protocol Client] No credentials and got auth error for client %s, likely needs pairing", client.ID)
				return fmt.Errorf("PAIRING_REQUIRED: %w", err)
			}
			
			return fmt.Errorf("failed to connect AirPlay client: %w", err)
		}
		
		return s.protocolManager.RegisterClient(airplayClient)
	case "local":
		// Default to 44.1kHz stereo if not specified
		sampleRate := 44100
		channels := 2
		
		// Parse config if available
		var config map[string]interface{}
		if client.Config != "" {
			if err := json.Unmarshal([]byte(client.Config), &config); err == nil {
				if sr, ok := config["sample_rate"].(float64); ok {
					sampleRate = int(sr)
				}
				if ch, ok := config["channels"].(float64); ok {
					channels = int(ch)
				}
			}
		}

		localClient, err := local.NewClient(client.ID, client.Name, sampleRate, channels)
		if err != nil {
			return fmt.Errorf("failed to create local client: %w", err)
		}

		// Connect the client
		if err := localClient.Connect(""); err != nil {
			return fmt.Errorf("failed to connect local client: %w", err)
		}

		return s.protocolManager.RegisterClient(localClient)
	default:
		return fmt.Errorf("unknown protocol: %s", client.Protocol)
	}
}

