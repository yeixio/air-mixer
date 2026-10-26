package router

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/yeixio/air-mixer/internal/audio"
	"github.com/yeixio/air-mixer/internal/db"
)

// AudioRouter implements the Router interface
type AudioRouter struct {
	db            *db.DB
	routes        map[string]*Route
	activeRoutes  map[string]*activeRoute
	mu            sync.RWMutex
	running       bool
	ctx           context.Context
	cancel        context.CancelFunc
	serverStreams map[string]<-chan []float32 // Source streams (keyed by server ID)
	clientSinks   map[string]chan<- []float32 // Destination sinks (keyed by client ID)
	serverNames   map[string]string // Map server name -> server ID
	clientNames   map[string]string // Map client name -> client ID
}

// activeRoute represents a route that's currently processing audio
type activeRoute struct {
	route  Route
	mixer  audio.AudioMixer
	cancel context.CancelFunc
}

// NewRouter creates a new audio router
func NewRouter(database *db.DB) *AudioRouter {
	ctx, cancel := context.WithCancel(context.Background())
	return &AudioRouter{
		db:            database,
		routes:        make(map[string]*Route),
		activeRoutes:  make(map[string]*activeRoute),
		ctx:           ctx,
		cancel:        cancel,
		serverStreams: make(map[string]<-chan []float32),
		clientSinks:   make(map[string]chan<- []float32),
		serverNames:   make(map[string]string),
		clientNames:   make(map[string]string),
	}
}

// AddRoute adds a new route
func (r *AudioRouter) AddRoute(route Route) error {
	r.mu.Lock()

	if route.ID == "" {
		r.mu.Unlock()
		return fmt.Errorf("route ID cannot be empty")
	}

	if _, exists := r.routes[route.ID]; exists {
		r.mu.Unlock()
		return fmt.Errorf("route with ID %s already exists", route.ID)
	}

	// Validate route
	if err := r.validateRoute(route); err != nil {
		r.mu.Unlock()
		return err
	}

	r.routes[route.ID] = &route
	routeCopy := route // Copy for use outside the lock
	running := r.running
	enabled := route.Enabled
	r.mu.Unlock() // Release lock before potentially blocking operations

	// If router is running and route is enabled, try to activate it
	// Note: If source stream doesn't exist yet, route will be activated when stream becomes available
	if running && enabled {
		if err := r.activateRoute(&routeCopy); err != nil {
			// Check if error is due to source not being available (OK - will activate later)
			// vs destination/client errors (should be reported)
			if strings.Contains(err.Error(), "source") && strings.Contains(err.Error(), "not available") {
				// Source stream not available yet - this is OK, route will activate when stream becomes available
				log.Printf("Route %s created but not yet active (source '%s' not available): %v", routeCopy.ID, routeCopy.SourceName, err)
				// Don't return error - allow route to be created and activated later
			} else {
				// Real error (e.g., destinations not found) - return it
				return fmt.Errorf("failed to activate route: %w", err)
			}
		}
	}

	return nil
}

// RemoveRoute removes a route by ID
func (r *AudioRouter) RemoveRoute(routeID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.routes[routeID]; !exists {
		return fmt.Errorf("route %s not found", routeID)
	}

	// Deactivate if active
	if active, ok := r.activeRoutes[routeID]; ok {
		r.deactivateRoute(active)
		delete(r.activeRoutes, routeID)
	}

	delete(r.routes, routeID)
	return nil
}

// UpdateRoute updates an existing route
func (r *AudioRouter) UpdateRoute(routeID string, route Route) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.routes[routeID]; !exists {
		return fmt.Errorf("route %s not found", routeID)
	}

	// Validate route
	if err := r.validateRoute(route); err != nil {
		return err
	}

	route.ID = routeID // Ensure ID matches
	r.routes[routeID] = &route

	// If router is running, update active route
	if r.running {
		// Deactivate old route if it was active
		if active, ok := r.activeRoutes[routeID]; ok {
			r.deactivateRoute(active)
			delete(r.activeRoutes, routeID)
		}

		// Activate new route if enabled
		// Note: If source stream doesn't exist yet, route will be activated when stream becomes available
		if route.Enabled {
			if err := r.activateRoute(&route); err != nil {
				// Source stream not available yet - this is OK, route will activate when stream becomes available
				log.Printf("Route %s updated but not yet active (source '%s' not available): %v", routeID, route.SourceName, err)
				// Don't return error - allow route to be updated and activated later
			}
		}
	}

	return nil
}

// GetRoute retrieves a route by ID
func (r *AudioRouter) GetRoute(routeID string) (*Route, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	route, exists := r.routes[routeID]
	if !exists {
		return nil, fmt.Errorf("route %s not found", routeID)
	}

	// Return a copy
	routeCopy := *route
	return &routeCopy, nil
}

// ListRoutes returns all routes
func (r *AudioRouter) ListRoutes() []Route {
	r.mu.RLock()
	defer r.mu.RUnlock()

	routes := make([]Route, 0, len(r.routes))
	for _, route := range r.routes {
		routes = append(routes, *route)
	}

	return routes
}

// ListRouteStatus returns all routes with status information
func (r *AudioRouter) ListRouteStatus() []RouteStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()

	statuses := make([]RouteStatus, 0, len(r.routes))
	for _, route := range r.routes {
		status := RouteStatus{
			Route:  *route,
			Active: r.activeRoutes[route.ID] != nil,
		}
		statuses = append(statuses, status)
	}

	return statuses
}

// Start starts the router
func (r *AudioRouter) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		return fmt.Errorf("router is already running")
	}

	r.running = true

	// Activate all enabled routes that have available source streams
	for _, route := range r.routes {
		if route.Enabled {
			if err := r.activateRoute(route); err != nil {
				// Source stream not available yet - this is OK, route will activate when stream becomes available
				log.Printf("Route %s enabled but not yet active (source '%s' not available): %v", route.ID, route.SourceName, err)
				// Continue - route will be activated when source stream becomes available
			}
		}
	}

	return nil
}

// Stop stops the router
func (r *AudioRouter) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.running {
		return nil
	}

	r.running = false

	// Deactivate all active routes
	for _, active := range r.activeRoutes {
		r.deactivateRoute(active)
	}
	r.activeRoutes = make(map[string]*activeRoute)

	return nil
}

// IsRunning returns whether the router is running
func (r *AudioRouter) IsRunning() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.running
}

// RegisterSourceStream registers an audio source stream
// serverID is the server's ID, serverName is the server's name (for name-based routing)
func (r *AudioRouter) RegisterSourceStream(serverID, serverName string, stream <-chan []float32) {
	r.mu.Lock()
	r.serverStreams[serverID] = stream
	if serverName != "" {
		r.serverNames[serverName] = serverID
		log.Printf("Router: registered server name mapping: %s -> %s", serverName, serverID)
	}
	
	// Collect routes that might be waiting for this source
	var waitingRoutes []*Route
	if r.running {
		for _, route := range r.routes {
			// Skip if route is not enabled or already active
			if !route.Enabled {
				continue
			}
			if _, isActive := r.activeRoutes[route.ID]; isActive {
				continue
			}
			
			// If this route uses this source (by name), mark it for activation
			if route.SourceName == serverName {
				waitingRoutes = append(waitingRoutes, route)
			}
		}
	}
	r.mu.Unlock()
	
	// Activate waiting routes (without holding the lock)
	for _, route := range waitingRoutes {
		if err := r.activateRoute(route); err != nil {
			log.Printf("Failed to activate waiting route %s when source %s (%s) became available: %v", route.ID, serverName, serverID, err)
		} else {
			log.Printf("Activated waiting route %s when source %s (%s) became available", route.ID, serverName, serverID)
		}
	}
}

// UnregisterSourceStream unregisters an audio source stream
func (r *AudioRouter) UnregisterSourceStream(sourceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.serverStreams, sourceID)
}

// RegisterClientSink registers an audio destination sink
// clientID is the client's ID, clientName is the client's name (for name-based routing)
func (r *AudioRouter) RegisterClientSink(clientID, clientName string, sink chan<- []float32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clientSinks[clientID] = sink
	if clientName != "" {
		r.clientNames[clientName] = clientID
		log.Printf("Router: registered client name mapping: %s -> %s", clientName, clientID)
	}
	log.Printf("Router: registered client sink %s (name: %s, total sinks: %d)", clientID, clientName, len(r.clientSinks))
}

// UnregisterClientSink unregisters an audio destination sink
func (r *AudioRouter) UnregisterClientSink(clientID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.clientSinks, clientID)
}

// getClientSinkIDs returns a list of registered client sink IDs (for logging)
func (r *AudioRouter) getClientSinkIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.clientSinks))
	for id := range r.clientSinks {
		ids = append(ids, id)
	}
	return ids
}

// validateRoute validates a route configuration
func (r *AudioRouter) validateRoute(route Route) error {
	if route.SourceName == "" {
		return fmt.Errorf("source name cannot be empty")
	}
	if len(route.DestNames) == 0 {
		return fmt.Errorf("at least one destination name required")
	}
	if route.Volume < 0.0 || route.Volume > 1.0 {
		return fmt.Errorf("volume must be between 0.0 and 1.0")
	}
	return nil
}

// resolveSourceName resolves a server name to its ID
func (r *AudioRouter) resolveSourceName(serverName string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	serverID, exists := r.serverNames[serverName]
	if !exists {
		return "", fmt.Errorf("server name '%s' not found (not registered)", serverName)
	}
	return serverID, nil
}

// resolveDestNames resolves client names to their IDs
func (r *AudioRouter) resolveDestNames(clientNames []string) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	clientIDs := make([]string, 0, len(clientNames))
	missing := make([]string, 0)
	
	for _, clientName := range clientNames {
		clientID, exists := r.clientNames[clientName]
		if !exists {
			missing = append(missing, clientName)
			continue
		}
		clientIDs = append(clientIDs, clientID)
	}
	
	if len(missing) > 0 {
		return nil, fmt.Errorf("client names not found (not registered): %v", missing)
	}
	
	if len(clientIDs) == 0 {
		return nil, fmt.Errorf("none of the destination names were found: %v", clientNames)
	}
	
	return clientIDs, nil
}

// activateRoute activates a route and starts routing audio
func (r *AudioRouter) activateRoute(route *Route) error {
	// Resolve source name to ID
	sourceID, err := r.resolveSourceName(route.SourceName)
	if err != nil {
		return fmt.Errorf("failed to resolve source name '%s': %w", route.SourceName, err)
	}
	
	// Check if source stream exists (need lock to access serverStreams)
	r.mu.RLock()
	sourceStream, exists := r.serverStreams[sourceID]
	r.mu.RUnlock()
	if !exists {
		return fmt.Errorf("source stream for '%s' (ID: %s) not found", route.SourceName, sourceID)
	}

	// Create mixer if mixing multiple sources to same destination
	mixer := audio.NewMixer(4096)
	if err := mixer.Start(); err != nil {
		return fmt.Errorf("failed to start mixer: %w", err)
	}

	// Add source to mixer (use sourceID for mixer, but route.SourceName for logging)
	if err := mixer.AddSource(sourceID, sourceStream); err != nil {
		mixer.Close()
		return fmt.Errorf("failed to add source to mixer: %w", err)
	}

	// Set source volume
	if err := mixer.SetSourceVolume(sourceID, route.Volume); err != nil {
		mixer.Close()
		return fmt.Errorf("failed to set source volume: %w", err)
	}

	// Get mixed output
	outputChan := mixer.Output()

	// Create context for this route
	routeCtx, routeCancel := context.WithCancel(r.ctx)

	// Resolve destination names to IDs
	destIDs, err := r.resolveDestNames(route.DestNames)
	if err != nil {
		mixer.Close()
		return fmt.Errorf("failed to resolve destination names: %w", err)
	}
	
	// Start routing goroutine for each destination
	log.Printf("Router: activating route %s with %d destinations: %v (resolved to IDs: %v)", route.ID, len(route.DestNames), route.DestNames, destIDs)
	activeDestinations := 0
	
	// Get client sinks (need lock to access clientSinks)
	r.mu.RLock()
	destSinks := make(map[string]chan<- []float32)
	missingSinks := make([]string, 0)
	for _, destID := range destIDs {
		if sink, exists := r.clientSinks[destID]; exists {
			destSinks[destID] = sink
		} else {
			// Find the name for this ID
			var destName string
			for name, id := range r.clientNames {
				if id == destID {
					destName = name
					break
				}
			}
			if destName == "" {
				destName = destID
			}
			missingSinks = append(missingSinks, destName)
		}
	}
	availableSinkIDs := r.getClientSinkIDs()
	r.mu.RUnlock()
	
	// Return error if any destinations are missing
	if len(missingSinks) > 0 {
		return fmt.Errorf("client sinks not found (clients may not be connected): %v (available sinks: %v)", missingSinks, availableSinkIDs)
	}
	
	// Verify we have the same number of destinations
	if len(destIDs) != len(route.DestNames) {
		return fmt.Errorf("destination count mismatch: expected %d, resolved %d", len(route.DestNames), len(destIDs))
	}
	
	// Start routing to all destinations
	for i, destName := range route.DestNames {
		destID := destIDs[i]
		destSink := destSinks[destID]
		activeDestinations++
		log.Printf("Router: ✓ client sink for '%s' (ID: %s) found, starting audio routing from source '%s' to client '%s' via route %s", destName, destID, route.SourceName, destName, route.ID)

		go r.routeAudio(routeCtx, route.ID, outputChan, destSink, destID)
	}

	// Store active route (need lock to write to activeRoutes)
	r.mu.Lock()
	r.activeRoutes[route.ID] = &activeRoute{
		route:  *route,
		mixer:  mixer,
		cancel: routeCancel,
	}
	r.mu.Unlock()

	return nil
}

// deactivateRoute deactivates a route
func (r *AudioRouter) deactivateRoute(active *activeRoute) {
	active.cancel()
	if active.mixer != nil {
		active.mixer.Close()
	}
}

// routeAudio routes audio from source to destination
func (r *AudioRouter) routeAudio(ctx context.Context, routeID string, source <-chan []float32, dest chan<- []float32, destID string) {
	log.Printf("Router: started routing audio for route %s to client %s", routeID, destID)
	defer log.Printf("Router: stopped routing audio for route %s to client %s", routeID, destID)
	
	sampleCount := 0
	for {
		select {
		case <-ctx.Done():
			return
		case samples, ok := <-source:
			if !ok {
				return
			}
			sampleCount++

			// Send to destination (non-blocking)
			select {
			case dest <- samples:
				if sampleCount == 1 || sampleCount%1000 == 0 {
					log.Printf("Router: routed %d samples to client %s (batch #%d)", len(samples), destID, sampleCount)
				}
			case <-ctx.Done():
				return
			default:
				// Destination buffer full, skip this sample
				log.Printf("Router: WARNING - client %s buffer full for route %s, dropping %d samples", destID, routeID, len(samples))
			}
		}
	}
}

