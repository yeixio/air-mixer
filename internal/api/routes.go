package api

import (
	"net/http"

	"github.com/yeixio/air-mixer/internal/router"
)

// routeRequest is the body of a route create or update request. Omitted
// fields keep their current value on update; on create, volume defaults
// to 1 and enabled to true.
type routeRequest struct {
	ID         string    `json:"id"`
	Name       *string   `json:"name"`
	SourceName *string   `json:"source_name"`
	DestNames  *[]string `json:"dest_names"`
	MixMode    *string   `json:"mix_mode"`
	Volume     *float32  `json:"volume"`
	Enabled    *bool     `json:"enabled"`
	Priority   *int      `json:"priority"`
}

func (q *routeRequest) apply(r *router.Route) {
	if q.Name != nil {
		r.Name = *q.Name
	}
	if q.SourceName != nil {
		r.SourceName = *q.SourceName
	}
	if q.DestNames != nil {
		r.DestNames = *q.DestNames
	}
	if q.MixMode != nil {
		r.MixMode = router.MixMode(*q.MixMode)
	}
	if q.Volume != nil {
		r.Volume = *q.Volume
	}
	if q.Enabled != nil {
		r.Enabled = *q.Enabled
	}
	if q.Priority != nil {
		r.Priority = *q.Priority
	}
}

func (a *API) listRoutes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.svc.Routes())
}

func (a *API) getRoute(w http.ResponseWriter, r *http.Request) {
	st, err := a.svc.Route(r.PathValue("id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (a *API) createRoute(w http.ResponseWriter, r *http.Request) {
	var req routeRequest
	if !decode(w, r, &req) {
		return
	}
	route := router.Route{ID: req.ID, Volume: 1, Enabled: true}
	req.apply(&route)
	route, err := a.svc.CreateRoute(r.Context(), route)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, route)
}

func (a *API) updateRoute(w http.ResponseWriter, r *http.Request) {
	var req routeRequest
	if !decode(w, r, &req) {
		return
	}
	st, err := a.svc.Route(r.PathValue("id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	route := st.Route
	req.apply(&route)
	if route, err = a.svc.UpdateRoute(r.Context(), route); err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, route)
}

func (a *API) deleteRoute(w http.ResponseWriter, r *http.Request) {
	if err := a.svc.DeleteRoute(r.Context(), r.PathValue("id")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
