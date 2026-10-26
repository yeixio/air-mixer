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
	"fmt"
)

// MixMode is stored with each route for compatibility with existing
// configurations. All routes that share a destination are currently mixed
// together regardless of mode; exclusive and ducking modes are on the
// roadmap.
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

// ValidationError describes a route that cannot be saved.
type ValidationError struct{ Reason string }

func (e *ValidationError) Error() string { return "invalid route: " + e.Reason }

// Validate checks that r can be added to a router.
func (r Route) Validate() error {
	switch {
	case r.ID == "":
		return &ValidationError{"id is required"}
	case r.SourceName == "":
		return &ValidationError{"source_name is required"}
	case len(r.DestNames) == 0:
		return &ValidationError{"at least one destination is required"}
	case r.Volume < 0 || r.Volume > 1:
		return &ValidationError{fmt.Sprintf("volume %v is outside 0..1", r.Volume)}
	}
	switch r.MixMode {
	case "", MixModeReplace, MixModeMix, MixModeAdd:
	default:
		return &ValidationError{fmt.Sprintf("unknown mix_mode %q", r.MixMode)}
	}
	for _, d := range r.DestNames {
		if d == "" {
			return &ValidationError{"destination names cannot be empty"}
		}
	}
	return nil
}

func (r Route) clone() Route {
	r.DestNames = append([]string(nil), r.DestNames...)
	return r
}
