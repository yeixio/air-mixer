package router

// MixMode represents how multiple sources should be mixed
type MixMode string

const (
	MixModeReplace MixMode = "replace" // Replace previous audio
	MixModeMix     MixMode = "mix"     // Mix with previous audio
	MixModeAdd     MixMode = "add"     // Add to previous audio
)

// Route represents a routing configuration
type Route struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	SourceName  string   `json:"source_name"` // Server/input name (resolved to ID at runtime)
	DestNames   []string `json:"dest_names"` // Client/output names (resolved to IDs at runtime)
	MixMode     MixMode  `json:"mix_mode"`
	Volume      float32  `json:"volume"`      // 0.0 to 1.0
	Enabled     bool     `json:"enabled"`
	Priority    int      `json:"priority"`   // Higher priority routes are processed first
}

// RouteStatus represents the current status of a route
type RouteStatus struct {
	Route
	Active      bool     `json:"active"`       // Whether route is currently active
	SourceName  string   `json:"source_name"` // Name of source
	DestNames   []string `json:"dest_names"`  // Names of destinations
	BytesRouted int64    `json:"bytes_routed"` // Total bytes routed through this route
}

// Router manages audio routing between sources and destinations
type Router interface {
	// AddRoute adds a new route
	AddRoute(route Route) error
	
	// RemoveRoute removes a route by ID
	RemoveRoute(routeID string) error
	
	// UpdateRoute updates an existing route
	UpdateRoute(routeID string, route Route) error
	
	// GetRoute retrieves a route by ID
	GetRoute(routeID string) (*Route, error)
	
	// ListRoutes returns all routes
	ListRoutes() []Route
	
	// ListRouteStatus returns all routes with status information
	ListRouteStatus() []RouteStatus
	
	// Start starts the router
	Start() error
	
	// Stop stops the router
	Stop() error
	
	// IsRunning returns whether the router is running
	IsRunning() bool
}

