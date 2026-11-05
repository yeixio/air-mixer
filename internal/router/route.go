// Package router connects audio sources to destinations according to
// user-defined routes.
//
// A route names one source and any number of destinations. Names rather
// than IDs are used so that a route survives a device being removed and
// added again. The router activates a route's links as soon as its source
// and destinations are registered, and tears them down when either goes
// away, so registration order never matters.
//
// Every source feeds a fan-out hub, so one source can play on many
// destinations and in many routes at once. Every destination has a mixer
// that sums all routes playing on it.
package router

import (
	"errors"

	"github.com/yeixio/air-mixer/i18n"
)

// MixMode is stored with each route for compatibility with existing
// configurations. All routes that share a destination are currently mixed
// together regardless of mode; an exclusive mode is on the roadmap. Ducking
// is set per route with Route.Duck.
type MixMode string

const (
	MixModeReplace MixMode = "replace"
	MixModeMix     MixMode = "mix"
	MixModeAdd     MixMode = "add"
)

// Route is a configured connection from one source to one or more
// destinations.
type Route struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	SourceName string   `json:"source_name"`
	DestNames  []string `json:"dest_names"`
	MixMode    MixMode  `json:"mix_mode"`
	Volume     float32  `json:"volume"` // 0 to 1
	Enabled    bool     `json:"enabled"`
	Priority   int      `json:"priority"` // routes are listed highest first

	// Duck, when set, makes this route an announcement: while it is
	// active, every other route on the same destinations plays at Duck
	// times its volume (0 silences them). Routes that duck do not duck
	// each other unless they have a lower priority.
	Duck *float32 `json:"duck,omitempty"`
	// Transient marks a route created at runtime, for example by
	// playback, that is not part of the stored configuration.
	Transient bool `json:"transient,omitempty"`
}

// RouteStatus is a route plus its live state.
type RouteStatus struct {
	Route
	// Active is true while at least one destination receives audio.
	Active bool `json:"active"`
	// ActiveDests lists destinations currently receiving audio.
	ActiveDests []string `json:"active_dests"`
	// SourceAvailable is true when the source is registered.
	SourceAvailable bool `json:"source_available"`
	// SamplesRouted counts samples delivered to destinations by this
	// route since the router started.
	SamplesRouted int64 `json:"samples_routed"`
}

var (
	// ErrNotFound is returned for unknown route IDs.
	ErrNotFound = errors.New("route not found")
	// ErrExists is returned when adding a route whose ID is taken.
	ErrExists = errors.New("route already exists")
)

// ValidationError describes a route that cannot be saved. Code and Params
// name the message in the i18n error catalog, so clients can show it in
// their reader's language.
type ValidationError struct {
	Reason string
	Code   string
	Params map[string]any
}

func invalidRoute(code string, params map[string]any) *ValidationError {
	return &ValidationError{Reason: i18n.Message(code, params), Code: code, Params: params}
}

func (e *ValidationError) Error() string { return "invalid route: " + e.Reason }

// Validate checks that r can be added to a router.
func (r Route) Validate() error {
	switch {
	case r.ID == "":
		return invalidRoute("route_id_required", nil)
	case r.SourceName == "":
		return invalidRoute("route_source_required", nil)
	case len(r.DestNames) == 0:
		return invalidRoute("route_dest_required", nil)
	case r.Volume < 0 || r.Volume > 1:
		return invalidRoute("route_volume_range", map[string]any{"volume": r.Volume})
	case r.Duck != nil && (*r.Duck < 0 || *r.Duck > 1):
		return invalidRoute("route_duck_range", map[string]any{"duck": *r.Duck})
	}
	switch r.MixMode {
	case "", MixModeReplace, MixModeMix, MixModeAdd:
	default:
		return invalidRoute("route_mix_mode", map[string]any{"mode": string(r.MixMode)})
	}
	for _, d := range r.DestNames {
		if d == "" {
			return invalidRoute("route_dest_empty", nil)
		}
	}
	return nil
}

func (r Route) clone() Route {
	r.DestNames = append([]string(nil), r.DestNames...)
	if r.Duck != nil {
		d := *r.Duck
		r.Duck = &d
	}
	return r
}
