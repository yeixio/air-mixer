// Package service keeps the stored configuration and the running system in
// step: every change to a server, client or route is saved to the database
// and applied to the protocol manager and router. The HTTP API is a thin
// layer over it.
package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"github.com/yeixio/air-mixer/internal/db"
	"github.com/yeixio/air-mixer/internal/protocols"
	"github.com/yeixio/air-mixer/internal/router"
)

// Errors returned by Service methods. Use errors.Is and errors.As:
// db.ErrNotFound and db.ErrConflict pass through, and *InvalidError
// reports bad input.
var (
	ErrNotFound = db.ErrNotFound
	ErrConflict = db.ErrConflict
)

// Service manages servers, clients and routes.
type Service struct {
	db      *db.DB
	router  *router.Router
	manager *protocols.Manager
	factory Factory
	log     *slog.Logger

	// routesMu serialises changes that read stored routes and write them
	// back, such as picking a speaker's source or renaming a device.
	routesMu sync.Mutex
}

// New returns a Service. Call Load to start what the database describes.
func New(store *db.DB, r *router.Router, m *protocols.Manager, f Factory, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{db: store, router: r, manager: m, factory: f, log: logger.With("component", "service")}
}

// Load starts every enabled server and client and loads every route.
// Adapters that fail to start are logged and skipped so that one bad
// record does not keep the rest from running.
func (s *Service) Load(ctx context.Context) error {
	servers, err := s.db.ListServers(ctx)
	if err != nil {
		return err
	}
	for _, v := range servers {
		if v.Enabled {
			if err := s.startServer(ctx, v); err != nil {
				s.log.Error("cannot start server", "id", v.ID, "err", err)
			}
		}
	}
	clients, err := s.db.ListClients(ctx)
	if err != nil {
		return err
	}
	for _, v := range clients {
		s.router.SetSinkVolume(v.ID, float32(v.Volume))
		if v.Enabled {
			if err := s.startClient(v); err != nil {
				s.log.Error("cannot start client", "id", v.ID, "err", err)
			}
		}
	}
	routes, err := s.db.ListRoutes(ctx)
	if err != nil {
		return err
	}
	for _, v := range routes {
		if err := s.router.AddRoute(toRouter(v)); err != nil {
			s.log.Error("cannot load route", "id", v.ID, "err", err)
		}
	}
	s.log.Info("configuration loaded", "servers", len(servers), "clients", len(clients), "routes", len(routes))
	return nil
}

func (s *Service) startServer(ctx context.Context, v *db.Server) error {
	srv, err := s.factory.Server(v)
	if err != nil {
		return err
	}
	return s.manager.AddServer(ctx, srv)
}

func (s *Service) stopServer(id string) {
	if err := s.manager.RemoveServer(id); err != nil && !errors.Is(err, protocols.ErrUnknownAdapter) {
		s.log.Warn("stop server", "id", id, "err", err)
	}
}

func (s *Service) startClient(v *db.Client) error {
	c, err := s.factory.Client(v)
	if err != nil {
		return err
	}
	return s.manager.AddClient(c)
}

func (s *Service) stopClient(id string) {
	if err := s.manager.RemoveClient(id); err != nil && !errors.Is(err, protocols.ErrUnknownAdapter) {
		s.log.Warn("stop client", "id", id, "err", err)
	}
}

// NewID returns a random identifier with the given prefix.
func NewID(prefix string) string {
	b := make([]byte, 8)
	rand.Read(b)
	return prefix + "-" + hex.EncodeToString(b)
}

func validateCommon(id, name, protocol string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return invalidCode("name_required", nil)
	case strings.TrimSpace(protocol) == "":
		return invalidCode("protocol_required", nil)
	case len(id) > 128 || strings.ContainsAny(id, "/?#"):
		return invalidCode("bad_id", nil)
	}
	return nil
}

// Servers

// Servers returns every stored server.
func (s *Service) Servers(ctx context.Context) ([]*db.Server, error) { return s.db.ListServers(ctx) }

// Server returns one stored server.
func (s *Service) Server(ctx context.Context, id string) (*db.Server, error) {
	return s.db.GetServer(ctx, id)
}

// CreateServer saves a server and starts it if enabled. If it cannot
// start, nothing is saved.
func (s *Service) CreateServer(ctx context.Context, v *db.Server) error {
	if v.ID == "" {
		v.ID = NewID("server")
	}
	if err := validateCommon(v.ID, v.Name, v.Protocol); err != nil {
		return err
	}
	if _, err := s.factory.Server(v); err != nil {
		return err
	}
	if err := s.db.CreateServer(ctx, v); err != nil {
		return err
	}
	if v.Enabled {
		if err := s.startServer(ctx, v); err != nil {
			if derr := s.db.DeleteServer(ctx, v.ID); derr != nil {
				s.log.Error("roll back server", "id", v.ID, "err", derr)
			}
			return fmt.Errorf("start server: %w", err)
		}
	}
	return nil
}

// UpdateServer saves a server and restarts it with the new settings.
func (s *Service) UpdateServer(ctx context.Context, v *db.Server) error {
	if err := validateCommon(v.ID, v.Name, v.Protocol); err != nil {
		return err
	}
	if _, err := s.factory.Server(v); err != nil {
		return err
	}
	old, err := s.db.GetServer(ctx, v.ID)
	if err != nil {
		return err
	}
	v.CreatedAt = old.CreatedAt
	if err := s.db.UpdateServer(ctx, v); err != nil {
		return err
	}
	if err := s.renameInRoutes(ctx, old.Name, v.Name, true); err != nil {
		return err
	}
	s.stopServer(v.ID)
	if v.Enabled {
		if err := s.startServer(ctx, v); err != nil {
			return fmt.Errorf("saved, but the server did not start: %w", err)
		}
	}
	return nil
}

// DeleteServer stops and deletes a server.
func (s *Service) DeleteServer(ctx context.Context, id string) error {
	if _, err := s.db.GetServer(ctx, id); err != nil {
		return err
	}
	s.stopServer(id)
	return s.db.DeleteServer(ctx, id)
}

// Clients

// Clients returns every stored client.
func (s *Service) Clients(ctx context.Context) ([]*db.Client, error) { return s.db.ListClients(ctx) }

// Client returns one stored client.
func (s *Service) Client(ctx context.Context, id string) (*db.Client, error) {
	return s.db.GetClient(ctx, id)
}

// CreateClient saves a client and, if enabled, starts connecting to it.
// Connection happens in the background; see Status. A new client starts
// at full volume; SetClientVolume changes it.
func (s *Service) CreateClient(ctx context.Context, v *db.Client) error {
	if v.ID == "" {
		v.ID = NewID("client")
	}
	v.Volume = 1
	if err := validateCommon(v.ID, v.Name, v.Protocol); err != nil {
		return err
	}
	if _, err := s.factory.Client(v); err != nil {
		return err
	}
	if err := s.db.CreateClient(ctx, v); err != nil {
		return err
	}
	s.router.SetSinkVolume(v.ID, 1)
	if v.Enabled {
		if err := s.startClient(v); err != nil {
			if derr := s.db.DeleteClient(ctx, v.ID); derr != nil {
				s.log.Error("roll back client", "id", v.ID, "err", derr)
			}
			return err
		}
	}
	return nil
}

// UpdateClient saves a client and reconnects it with the new settings.
func (s *Service) UpdateClient(ctx context.Context, v *db.Client) error {
	if err := validateCommon(v.ID, v.Name, v.Protocol); err != nil {
		return err
	}
	if _, err := s.factory.Client(v); err != nil {
		return err
	}
	old, err := s.db.GetClient(ctx, v.ID)
	if err != nil {
		return err
	}
	v.CreatedAt, v.Volume = old.CreatedAt, old.Volume
	if err := s.db.UpdateClient(ctx, v); err != nil {
		return err
	}
	if err := s.renameInRoutes(ctx, old.Name, v.Name, false); err != nil {
		return err
	}
	s.stopClient(v.ID)
	if v.Enabled {
		return s.startClient(v)
	}
	return nil
}

// DeleteClient disconnects and deletes a client.
func (s *Service) DeleteClient(ctx context.Context, id string) error {
	if _, err := s.db.GetClient(ctx, id); err != nil {
		return err
	}
	s.stopClient(id)
	if err := s.db.DeleteClient(ctx, id); err != nil {
		return err
	}
	s.router.SetSinkVolume(id, 1)
	return nil
}

// SetClientVolume sets a speaker's own volume, 0 to 1, without
// reconnecting it. It scales every route that plays on the speaker.
func (s *Service) SetClientVolume(ctx context.Context, id string, volume float64) error {
	if volume < 0 || volume > 1 {
		return invalidCode("route_volume_range", map[string]any{"volume": volume})
	}
	if err := s.db.SetClientVolume(ctx, id, volume); err != nil {
		return err
	}
	s.router.SetSinkVolume(id, float32(volume))
	return nil
}

// SetClientSource makes a speaker play one source, named as its server is,
// or nothing when source is empty. It edits the stored routes the way a
// person would in the routes table: the speaker leaves every enabled route
// from another source, which is deleted once it has no speakers left, and
// joins an enabled route from the new source, or a new route named after
// the source when there is none. Speakers that pick the same source so
// share one route, which is how speakers are grouped.
func (s *Service) SetClientSource(ctx context.Context, id, source string) error {
	c, err := s.db.GetClient(ctx, id)
	if err != nil {
		return err
	}
	if source != "" {
		servers, err := s.db.ListServers(ctx)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(servers, func(v *db.Server) bool { return v.Name == source }) {
			return invalidCode("source_unknown", map[string]any{"source": source})
		}
	}

	s.routesMu.Lock()
	defer s.routesMu.Unlock()
	routes, err := s.db.ListRoutes(ctx)
	if err != nil {
		return err
	}
	joined := false
	for _, v := range routes {
		if !v.Enabled || !slices.Contains(v.DestNames, c.Name) {
			continue // a route that is off plays nothing: leave it be
		}
		if !joined && source != "" && v.SourceName == source {
			joined = true // already playing it: keep this one
			continue
		}
		v.DestNames = slices.DeleteFunc(slices.Clone(v.DestNames), func(n string) bool { return n == c.Name })
		if len(v.DestNames) == 0 {
			err = s.DeleteRoute(ctx, v.ID)
		} else {
			_, err = s.UpdateRoute(ctx, toRouter(v))
		}
		if err != nil {
			return err
		}
	}
	if source == "" || joined {
		return nil
	}
	for _, v := range routes {
		if v.SourceName == source && v.Enabled && !slices.Contains(v.DestNames, c.Name) {
			v.DestNames = append(slices.Clone(v.DestNames), c.Name)
			_, err := s.UpdateRoute(ctx, toRouter(v))
			return err
		}
	}
	_, err = s.CreateRoute(ctx, router.Route{
		Name: source, SourceName: source, DestNames: []string{c.Name}, Volume: 1, Enabled: true,
	})
	return err
}

// renameInRoutes points stored routes at a server's or client's new name,
// so renaming a device in the web UI keeps what it plays. A route named
// after a renamed source takes the new name too.
func (s *Service) renameInRoutes(ctx context.Context, from, to string, isSource bool) error {
	if from == to {
		return nil
	}
	s.routesMu.Lock()
	defer s.routesMu.Unlock()
	routes, err := s.db.ListRoutes(ctx)
	if err != nil {
		return err
	}
	for _, v := range routes {
		changed := false
		if isSource && v.SourceName == from {
			v.SourceName, changed = to, true
			if v.Name == from {
				v.Name = to
			}
		}
		if !isSource && slices.Contains(v.DestNames, from) {
			v.DestNames = slices.Clone(v.DestNames)
			for i, n := range v.DestNames {
				if n == from {
					v.DestNames[i] = to
				}
			}
			changed = true
		}
		if changed {
			if _, err := s.UpdateRoute(ctx, toRouter(v)); err != nil {
				return err
			}
		}
	}
	return nil
}

// Routes

func toRouter(v *db.Route) router.Route {
	return router.Route{
		ID: v.ID, Name: v.Name, SourceName: v.SourceName, DestNames: v.DestNames,
		MixMode: router.MixMode(v.MixMode), Volume: float32(v.Volume), Enabled: v.Enabled, Priority: v.Priority,
	}
}

func fromRouter(r router.Route) *db.Route {
	return &db.Route{
		ID: r.ID, Name: r.Name, SourceName: r.SourceName, DestNames: r.DestNames,
		MixMode: string(r.MixMode), Volume: float64(r.Volume), Enabled: r.Enabled, Priority: r.Priority,
	}
}

func checkRoute(r router.Route) error {
	if r.Transient || r.Duck != nil {
		return invalidCode("route_playback_owned", nil)
	}
	if err := r.Validate(); err != nil {
		var ve *router.ValidationError
		if errors.As(err, &ve) {
			return &InvalidError{Reason: ve.Reason, Code: ve.Code, Params: ve.Params}
		}
		return err
	}
	return nil
}

// Routes returns every route with its live status.
func (s *Service) Routes() []router.RouteStatus { return s.router.Routes() }

// Route returns one route with its live status.
func (s *Service) Route(id string) (router.RouteStatus, error) {
	st, err := s.router.Route(id)
	if errors.Is(err, router.ErrNotFound) {
		return st, ErrNotFound
	}
	return st, err
}

// CreateRoute saves a route and activates it. An empty ID or mix mode is
// filled in.
func (s *Service) CreateRoute(ctx context.Context, r router.Route) (router.Route, error) {
	if r.ID == "" {
		r.ID = NewID("route")
	}
	if r.MixMode == "" {
		r.MixMode = router.MixModeReplace
	}
	if err := checkRoute(r); err != nil {
		return r, err
	}
	if err := s.db.CreateRoute(ctx, fromRouter(r)); err != nil {
		return r, err
	}
	if err := s.router.AddRoute(r); err != nil {
		if derr := s.db.DeleteRoute(ctx, r.ID); derr != nil {
			s.log.Error("roll back route", "id", r.ID, "err", derr)
		}
		return r, err
	}
	return r, nil
}

// UpdateRoute saves and applies a route.
func (s *Service) UpdateRoute(ctx context.Context, r router.Route) (router.Route, error) {
	if r.MixMode == "" {
		r.MixMode = router.MixModeReplace
	}
	if err := checkRoute(r); err != nil {
		return r, err
	}
	if err := s.db.UpdateRoute(ctx, fromRouter(r)); err != nil {
		return r, err
	}
	if err := s.router.UpdateRoute(r); err != nil {
		if errors.Is(err, router.ErrNotFound) {
			// Stored but not loaded, for example after a failed load.
			return r, s.router.AddRoute(r)
		}
		return r, err
	}
	return r, nil
}

// DeleteRoute deletes a route.
func (s *Service) DeleteRoute(ctx context.Context, id string) error {
	if err := s.db.DeleteRoute(ctx, id); err != nil {
		return err
	}
	if err := s.router.RemoveRoute(id); err != nil && !errors.Is(err, router.ErrNotFound) {
		return err
	}
	return nil
}

// Status

// Status summarises the running system.
type Status struct {
	RouterRunning bool               `json:"router_running"`
	RoutesActive  int                `json:"routes_active"`
	Adapters      []protocols.Status `json:"adapters"`
}

// Status returns the live state of the router and every adapter.
func (s *Service) Status() Status {
	return Status{
		RouterRunning: s.router.Running(),
		RoutesActive:  s.router.ActiveRoutes(),
		Adapters:      s.manager.Status(),
	}
}

// Ping checks the database.
func (s *Service) Ping(ctx context.Context) error { return s.db.Ping(ctx) }
