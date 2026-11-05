package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/yeixio/air-mixer/internal/db"
	"github.com/yeixio/air-mixer/internal/router"
)

// Setup is a declared set of servers, clients and routes, usually from the
// config file. Apply makes the stored configuration match it, so a
// deployment can describe its speakers in a Kubernetes ConfigMap instead
// of through the web UI.
type Setup struct {
	Servers []*db.Server
	Clients []*db.Client
	Routes  []router.Route
}

// Empty reports whether the setup declares nothing.
func (s Setup) Empty() bool {
	return len(s.Servers) == 0 && len(s.Clients) == 0 && len(s.Routes) == 0
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// slug turns a name into an ID, so declared items keep the same ID across
// restarts without one being written out.
func slug(prefix, name string) string {
	s := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if s == "" {
		return ""
	}
	return prefix + "-" + s
}

// Apply creates every declared item that does not exist and updates every
// one that differs, matching items by ID; an item without an ID gets one
// from its name. Declared settings win over changes made in the web UI,
// but items that are not declared are left alone. Apply stops at the
// first invalid item.
func (s *Service) Apply(ctx context.Context, setup Setup) error {
	seen := map[string]bool{}
	claim := func(kind, id, name string) error {
		if id == "" {
			return invalid("setup: %s %q needs an id or a name", kind, name)
		}
		if seen[id] {
			return invalid("setup: id %q is declared twice", id)
		}
		seen[id] = true
		return nil
	}

	for _, v := range setup.Servers {
		if v.ID == "" {
			v.ID = slug("server", v.Name)
		}
		if err := claim("server", v.ID, v.Name); err != nil {
			return err
		}
		if err := s.applyServer(ctx, v); err != nil {
			return fmt.Errorf("setup: server %q: %w", v.ID, err)
		}
	}
	for _, v := range setup.Clients {
		if v.ID == "" {
			v.ID = slug("client", v.Name)
		}
		if err := claim("client", v.ID, v.Name); err != nil {
			return err
		}
		if err := s.applyClient(ctx, v); err != nil {
			return fmt.Errorf("setup: client %q: %w", v.ID, err)
		}
	}
	for _, r := range setup.Routes {
		if r.ID == "" {
			r.ID = slug("route", r.Name)
		}
		if err := claim("route", r.ID, r.Name); err != nil {
			return err
		}
		if err := s.applyRoute(ctx, r); err != nil {
			return fmt.Errorf("setup: route %q: %w", r.ID, err)
		}
	}
	s.log.Info("setup applied", "servers", len(setup.Servers), "clients", len(setup.Clients), "routes", len(setup.Routes))
	return nil
}

func (s *Service) applyServer(ctx context.Context, v *db.Server) error {
	old, err := s.db.GetServer(ctx, v.ID)
	switch {
	case errors.Is(err, db.ErrNotFound):
		return s.CreateServer(ctx, v)
	case err != nil:
		return err
	case old.Name == v.Name && old.Protocol == v.Protocol && old.Enabled == v.Enabled && old.Config == v.Config:
		return nil
	}
	return s.UpdateServer(ctx, v)
}

func (s *Service) applyClient(ctx context.Context, v *db.Client) error {
	old, err := s.db.GetClient(ctx, v.ID)
	switch {
	case errors.Is(err, db.ErrNotFound):
		return s.CreateClient(ctx, v)
	case err != nil:
		return err
	case old.Name == v.Name && old.Protocol == v.Protocol && old.Address == v.Address &&
		old.Enabled == v.Enabled && old.Config == v.Config:
		return nil
	}
	return s.UpdateClient(ctx, v)
}

func (s *Service) applyRoute(ctx context.Context, r router.Route) error {
	if r.MixMode == "" {
		r.MixMode = router.MixModeReplace
	}
	old, err := s.db.GetRoute(ctx, r.ID)
	switch {
	case errors.Is(err, db.ErrNotFound):
		_, err := s.CreateRoute(ctx, r)
		return err
	case err != nil:
		return err
	}
	o := toRouter(old)
	if o.Name == r.Name && o.SourceName == r.SourceName && slices.Equal(o.DestNames, r.DestNames) &&
		o.MixMode == r.MixMode && o.Volume == r.Volume && o.Enabled == r.Enabled && o.Priority == r.Priority {
		return nil
	}
	_, err = s.UpdateRoute(ctx, r)
	return err
}
