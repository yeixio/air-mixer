package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/gopherstein/air-mixer/internal/db"
	"github.com/gopherstein/air-mixer/internal/protocols/airplay"
)

// pairClientRequest represents a pairing request
type pairClientRequest struct {
	PIN        string                 `json:"pin"`         // PIN code displayed on Apple TV
	ClientData *pairClientDataRequest `json:"client_data,omitempty"` // Client data if client not yet in DB
}

// pairClientDataRequest represents client data sent during pairing when client not yet in DB
type pairClientDataRequest struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
}

// pairClientResponse represents a pairing response
type pairClientResponse struct {
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	RequiresPIN bool `json:"requires_pin,omitempty"` // True if device is waiting for PIN
}

// pairClient initiates or completes pairing with an AirPlay client
func (s *Server) pairClient(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	repo := db.NewClientRepository(s.db)
	client, err := repo.Get(id)
	
	// Decode request body
	var req pairClientRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	
	// If client doesn't exist in DB, check if client data is in request body
	// This happens when pairing is required during client creation
	clientExistsInDB := (err == nil)
	if !clientExistsInDB {
		// If client not in DB and no client data provided, return error
		if req.ClientData == nil {
			s.writeError(w, http.StatusNotFound, fmt.Sprintf("Client %s not found and no client data provided", id))
			return
		}
		
		// Create temporary client object from request data
		client = &db.Client{
			ID:       req.ClientData.ID,
			Name:     req.ClientData.Name,
			Protocol: req.ClientData.Protocol,
			Address:  req.ClientData.Address,
			Enabled:  true,
		}
	}

	// Only AirPlay clients support pairing
	if client.Protocol != "airplay" {
		s.writeError(w, http.StatusBadRequest, "Pairing is only supported for AirPlay clients")
		return
	}

	// Parse address
	address := client.Address
	if address == "" {
		s.writeError(w, http.StatusBadRequest, "Client address is required for pairing")
		return
	}

	// Check if client already has credentials (only if client exists in DB)
	if clientExistsInDB {
		var existingConfig map[string]interface{}
		if client.Config != "" {
			if err := json.Unmarshal([]byte(client.Config), &existingConfig); err != nil {
				existingConfig = make(map[string]interface{})
			}
		} else {
			existingConfig = make(map[string]interface{})
		}

		// Check if already paired
		if deviceID, ok := existingConfig["device_id"].(string); ok && deviceID != "" {
			if privateKey, ok := existingConfig["private_key"].(string); ok && privateKey != "" {
				// Already paired
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(pairClientResponse{
					Success: true,
					Message: "Client is already paired",
				})
				return
			}
		}
	}

	// PIN is required for completing pairing
	if req.PIN == "" {
		s.writeError(w, http.StatusBadRequest, "PIN is required to complete pairing")
		return
	}

	// Perform pairing
	log.Printf("Completing pairing for AirPlay client %s (%s) at %s", client.ID, client.Name, address)
	
	// Create temporary client for pairing
	airplayClient, err := airplay.NewClient(client.ID, client.Name, address, 44100, 2)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create AirPlay client: %v", err))
		return
	}

	// Complete pairing with PIN
	deviceID, privateKey, err := airplayClient.CompletePairing(req.PIN)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Pairing failed: %v", err))
		return
	}

	// Store credentials in client config
	config := make(map[string]interface{})
	if client.Config != "" {
		if err := json.Unmarshal([]byte(client.Config), &config); err != nil {
			config = make(map[string]interface{})
		}
	}
	config["device_id"] = deviceID
	config["private_key"] = privateKey
	
	configJSON, err := json.Marshal(config)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to save credentials: %v", err))
		return
	}

	client.Config = string(configJSON)
	
	// If client doesn't exist in DB, create it now (after successful pairing)
	if !clientExistsInDB {
		log.Printf("Creating client %s in database after successful pairing", client.ID)
		if err := repo.Create(client); err != nil {
			s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create client after pairing: %v", err))
			return
		}
	} else {
		// Update existing client
		if err := repo.Update(client); err != nil {
			s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to update client: %v", err))
			return
		}
	}

	// Register the client with the protocol manager now that pairing is complete
	if err := s.registerProtocolClient(client); err != nil {
		log.Printf("Warning: failed to register client %s after pairing: %v", client.ID, err)
		// Don't fail the request - pairing succeeded, registration can be retried
	}

	log.Printf("Successfully paired AirPlay client %s (%s)", client.ID, client.Name)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pairClientResponse{
		Success: true,
		Message: "Pairing successful",
	})
}

