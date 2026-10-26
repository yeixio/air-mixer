package api

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/yeixio/air-mixer/internal/db"
)

// config accepts a protocol config either as a JSON string, which is how
// the database stores it and the web UI sends it, or as a JSON object.
type config struct {
	set   bool
	value string
}

func (c *config) UnmarshalJSON(b []byte) error {
	c.set = true
	b = bytes.TrimSpace(b)
	switch {
	case bytes.Equal(b, []byte("null")):
		c.value = ""
	case len(b) > 0 && b[0] == '"':
		return json.Unmarshal(b, &c.value)
	default:
		var buf bytes.Buffer
		if err := json.Compact(&buf, b); err != nil {
			return err
		}
		c.value = buf.String()
	}
	return nil
}

// deviceRequest is the body of a create or update request for a server or
// client. Omitted fields keep their current value on update; on create,
// enabled defaults to true.
type deviceRequest struct {
	ID       string  `json:"id"`
	Name     *string `json:"name"`
	Protocol *string `json:"protocol"`
	Address  *string `json:"address"` // clients only
	Enabled  *bool   `json:"enabled"`
	Config   config  `json:"config"`
}

func (d *deviceRequest) applyServer(v *db.Server) {
	if d.Name != nil {
		v.Name = *d.Name
	}
	if d.Protocol != nil {
		v.Protocol = *d.Protocol
	}
	if d.Enabled != nil {
		v.Enabled = *d.Enabled
	}
	if d.Config.set {
		v.Config = d.Config.value
	}
}

func (d *deviceRequest) applyClient(v *db.Client) {
	if d.Name != nil {
		v.Name = *d.Name
	}
	if d.Protocol != nil {
		v.Protocol = *d.Protocol
	}
	if d.Address != nil {
		v.Address = *d.Address
	}
	if d.Enabled != nil {
		v.Enabled = *d.Enabled
	}
	if d.Config.set {
		v.Config = d.Config.value
	}
}

func (a *API) listServers(w http.ResponseWriter, r *http.Request) {
	list, err := a.svc.Servers(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *API) getServer(w http.ResponseWriter, r *http.Request) {
	v, err := a.svc.Server(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (a *API) createServer(w http.ResponseWriter, r *http.Request) {
	var req deviceRequest
	if !decode(w, r, &req) {
		return
	}
	v := &db.Server{ID: req.ID, Enabled: true}
	req.applyServer(v)
	if err := a.svc.CreateServer(r.Context(), v); err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (a *API) updateServer(w http.ResponseWriter, r *http.Request) {
	var req deviceRequest
	if !decode(w, r, &req) {
		return
	}
	v, err := a.svc.Server(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	req.applyServer(v)
	if err := a.svc.UpdateServer(r.Context(), v); err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (a *API) deleteServer(w http.ResponseWriter, r *http.Request) {
	if err := a.svc.DeleteServer(r.Context(), r.PathValue("id")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) listClients(w http.ResponseWriter, r *http.Request) {
	list, err := a.svc.Clients(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *API) getClient(w http.ResponseWriter, r *http.Request) {
	v, err := a.svc.Client(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (a *API) createClient(w http.ResponseWriter, r *http.Request) {
	var req deviceRequest
	if !decode(w, r, &req) {
		return
	}
	v := &db.Client{ID: req.ID, Enabled: true}
	req.applyClient(v)
	if err := a.svc.CreateClient(r.Context(), v); err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (a *API) updateClient(w http.ResponseWriter, r *http.Request) {
	var req deviceRequest
	if !decode(w, r, &req) {
		return
	}
	v, err := a.svc.Client(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	req.applyClient(v)
	if err := a.svc.UpdateClient(r.Context(), v); err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (a *API) deleteClient(w http.ResponseWriter, r *http.Request) {
	if err := a.svc.DeleteClient(r.Context(), r.PathValue("id")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
