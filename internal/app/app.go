package app

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gopherstein/air-mixer/internal/api"
	"github.com/gopherstein/air-mixer/internal/config"
	"github.com/gopherstein/air-mixer/internal/db"
	"github.com/gopherstein/air-mixer/internal/logger"
	"github.com/gopherstein/air-mixer/internal/protocols"
	"github.com/gopherstein/air-mixer/internal/protocols/airplay"
	"github.com/gopherstein/air-mixer/internal/protocols/local"
	"github.com/gopherstein/air-mixer/internal/router"
)

// App represents the main application
type App struct {
	config          *config.Config
	database        *db.DB
	audioRouter     *router.AudioRouter
	protocolManager *protocols.Manager
	apiServer       *api.Server
}

// New creates a new application instance
func New() (*App, error) {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	// Initialize database
	database, err := db.New(cfg.Database.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	// Initialize logger
	logger.Init(&cfg.Logging)

	// Run migrations
	if err := database.Migrate(); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	// Create audio router
	audioRouter := router.NewRouter(database)

	// Create protocol manager
	protocolManager := protocols.NewManager(audioRouter)

	// Create API server
	apiServer := api.NewServer(database, audioRouter, protocolManager, cfg.Server.WebPort)

	return &App{
		config:          cfg,
		database:        database,
		audioRouter:     audioRouter,
		protocolManager: protocolManager,
		apiServer:       apiServer,
	}, nil
}

// Start starts the application
func (a *App) Start() error {
	log := logger.GetLogger()
	log.Info("Starting Air-Mixer...")

	// Load and start protocol servers from database
	if err := a.loadProtocolServers(); err != nil {
		log.Warnf("Failed to load protocol servers: %v", err)
	}

	// Load and register protocol clients from database
	if err := a.loadProtocolClients(); err != nil {
		log.Warnf("Failed to load protocol clients: %v", err)
	}

	// Start audio router
	if err := a.audioRouter.Start(); err != nil {
		return fmt.Errorf("failed to start audio router: %w", err)
	}
	log.Info("Audio router started")

	// Load routes from database
	log.Info("Loading routes from database...")
	if err := a.loadRoutes(); err != nil {
		log.Warnf("Failed to load routes: %v", err)
	} else {
		log.Info("Routes loaded successfully")
	}

	// Start API server in goroutine
	log.Info("Starting API server...")
	go func() {
		log.Infof("Starting API server on %s", a.apiServer.GetAddr())
		if err := a.apiServer.Start(); err != nil {
			log.Errorf("API server error: %v", err)
		}
	}()
	
	// Give API server a moment to start
	time.Sleep(200 * time.Millisecond)

	log.Info("Air-Mixer started successfully")
	log.Infof("Web UI available at http://localhost:%d", a.config.Server.WebPort)
	log.Infof("API available at http://localhost:%d/api", a.config.Server.WebPort)
	log.Info("Server is ready - waiting for connections...")

	return nil
}

// Stop stops the application
func (a *App) Stop() error {
	log := logger.GetLogger()
	log.Info("Stopping Air-Mixer...")

	// Stop API server
	if err := a.apiServer.Stop(); err != nil {
		log.Errorf("Error stopping API server: %v", err)
	}

	// Stop audio router
	if err := a.audioRouter.Stop(); err != nil {
		log.Errorf("Error stopping audio router: %v", err)
	}

	// Close database
	if err := a.database.Close(); err != nil {
		log.Errorf("Error closing database: %v", err)
	}

	log.Info("Air-Mixer stopped")
	return nil
}

// loadProtocolServers loads protocol servers from database and registers them
func (a *App) loadProtocolServers() error {
	log := logger.GetLogger()
	serverRepo := db.NewServerRepository(a.database)
	servers, err := serverRepo.List()
	if err != nil {
		return fmt.Errorf("failed to list servers: %w", err)
	}

	log.Debugf("Found %d servers in database", len(servers))

	for _, server := range servers {
		if !server.Enabled {
			log.Debugf("Skipping disabled server: %s", server.ID)
			continue
		}

		log.Debugf("Loading server: %s (protocol: %s, name: %s)", server.ID, server.Protocol, server.Name)

		// Create protocol server based on type
		switch server.Protocol {
		case "airplay":
			// Parse config
			var config map[string]string
			if server.Config != "" {
				// TODO: Parse JSON config
				config = make(map[string]string)
			}

			address := config["address"]
			if address == "" {
				address = fmt.Sprintf(":%d", a.config.Server.AirPlayPort)
			}

			iface := config["interface"]
			if iface == "" {
				iface = a.config.Server.Interface
			}

			log.Debugf("AirPlay server config - ID: %s, Name: %s, Address: %s, Interface: %s", server.ID, server.Name, address, iface)

			// Create AirPlay server
			airplayServer := airplay.NewServer(server.ID, server.Name, address, iface)
			
			// Register with protocol manager
			if err := a.protocolManager.RegisterServer(airplayServer); err != nil {
				log.Errorf("Failed to register AirPlay server %s: %v", server.ID, err)
				continue
			}

			log.Infof("Loaded and registered AirPlay server: %s (%s)", server.ID, server.Name)
		default:
			log.Warnf("Unknown protocol: %s for server %s", server.Protocol, server.ID)
		}
	}

	return nil
}

// loadProtocolClients loads protocol clients from database and registers them
func (a *App) loadProtocolClients() error {
	log := logger.GetLogger()
	clientRepo := db.NewClientRepository(a.database)
	clients, err := clientRepo.List()
	if err != nil {
		return fmt.Errorf("failed to list clients: %w", err)
	}

	log.Infof("Found %d clients in database", len(clients))
	
	// Log all clients found (for debugging)
	for _, client := range clients {
		status := "enabled"
		if !client.Enabled {
			status = "disabled"
		}
		log.Infof("Client in database: %s (protocol: %s, name: %s, status: %s, address: %s)", 
			client.ID, client.Protocol, client.Name, status, client.Address)
	}

	enabledCount := 0
	for _, client := range clients {
		if !client.Enabled {
			log.Debugf("Skipping disabled client: %s (%s)", client.ID, client.Name)
			continue
		}
		enabledCount++

		log.Infof("Loading enabled client: %s (protocol: %s, name: %s)", client.ID, client.Protocol, client.Name)

		// Create protocol client based on type
		clientProtocol := client.Protocol
		
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
					log.Warnf("AirPlay client %s requires an address, skipping", client.ID)
					continue
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
				log.Errorf("Failed to create AirPlay client %s: %v", client.ID, err)
				continue
			}
			
			// Connect the client
			if err := airplayClient.Connect(""); err != nil {
				log.Errorf("Failed to connect AirPlay client %s (%s) to %s: %v", client.ID, client.Name, address, err)
				log.Warnf("Client %s (%s) will not be available until connection succeeds. You may want to delete or disable this client if it's no longer needed.", client.ID, client.Name)
				continue
			}
			
			// Register with protocol manager
			if err := a.protocolManager.RegisterClient(airplayClient); err != nil {
				log.Errorf("Failed to register AirPlay client %s: %v", client.ID, err)
				continue
			}
			
			log.Infof("Successfully loaded and registered AirPlay client: %s (%s)", client.ID, client.Name)
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
				log.Errorf("Failed to create local client %s: %v", client.ID, err)
				continue
			}

			// Connect the client
			if err := localClient.Connect(""); err != nil {
				log.Errorf("Failed to connect local client %s: %v", client.ID, err)
				continue
			}

			// Register with protocol manager
			if err := a.protocolManager.RegisterClient(localClient); err != nil {
				log.Errorf("Failed to register local client %s: %v", client.ID, err)
				continue
			}

			log.Infof("Loaded and registered local client: %s (%s)", client.ID, client.Name)
		default:
			log.Warnf("Unknown protocol: %s for client %s (%s)", client.Protocol, client.ID, client.Name)
		}
	}

	if enabledCount > 0 {
		log.Infof("Finished loading clients: %d enabled clients processed", enabledCount)
	} else {
		log.Info("No enabled clients found in database")
	}

	return nil
}

// loadRoutes loads routes from database and adds them to the router
func (a *App) loadRoutes() error {
	routeRepo := db.NewRouteRepository(a.database)
	dbRoutes, err := routeRepo.List()
	if err != nil {
		return fmt.Errorf("failed to list routes: %w", err)
	}

	log := logger.GetLogger()
	log.Infof("Loading %d routes from database", len(dbRoutes))

	for _, dbRoute := range dbRoutes {
		// Convert db.Route to router.Route
		// db.Route has DestNames as comma-separated string, router.Route has []string
		destNames := []string{}
		if dbRoute.DestNames != "" {
			parts := strings.Split(dbRoute.DestNames, ",")
			for _, part := range parts {
				trimmed := strings.TrimSpace(part)
				if trimmed != "" {
					destNames = append(destNames, trimmed)
				}
			}
		}

		route := router.Route{
			ID:         dbRoute.ID,
			Name:       dbRoute.Name,
			SourceName: dbRoute.SourceName,
			DestNames:  destNames,
			MixMode:    router.MixMode(dbRoute.MixMode),
			Volume:     float32(dbRoute.Volume),
			Enabled:    dbRoute.Enabled,
			Priority:   dbRoute.Priority,
		}

		// Add to router (will activate if enabled and source stream is available)
		if err := a.audioRouter.AddRoute(route); err != nil {
			log.Warnf("Failed to load route %s (%s): %v", route.ID, route.Name, err)
			continue
		}

		log.Debugf("Loaded route: %s (%s) - Source: %s, Destinations: %v", route.ID, route.Name, route.SourceName, route.DestNames)
	}

	log.Infof("Successfully loaded %d routes", len(dbRoutes))
	return nil
}

// Run runs the application with signal handling
func (a *App) Run() error {
	// Start the application
	if err := a.Start(); err != nil {
		return err
	}

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Block until signal received
	<-sigChan

	// Stop the application
	return a.Stop()
}

