package api

import (
	"context"
	"net/http"
	"time"

	"github.com/yeixio/air-mixer/internal/protocols/matinee"
)

// clientStream serves a stream or Matinee output's audio as a live WAV.
func (a *API) clientStream(w http.ResponseWriter, r *http.Request) {
	s, ok := a.streams.Lookup(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "no such stream: the output isn't a connected stream or Matinee output")
		return
	}
	s.ServeHTTP(w, r)
}

func (a *API) matineeOn(w http.ResponseWriter) bool {
	if a.matinee == nil {
		writeError(w, http.StatusNotFound, "Matinee isn't available")
		return false
	}
	return true
}

// matineeState says which Matinee server Air-Mixer uses and whether it's
// signed in, with the code to approve while it signs in.
func (a *API) matineeState(w http.ResponseWriter, r *http.Request) {
	if a.matineeOn(w) {
		writeJSON(w, http.StatusOK, a.matinee.State())
	}
}

// setMatinee changes the Matinee server: {"url": "http://192.168.1.20:7171"}.
func (a *API) setMatinee(w http.ResponseWriter, r *http.Request) {
	if !a.matineeOn(w) {
		return
	}
	var req struct {
		URL string `json:"url"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := a.matinee.SetURL(r.Context(), req.URL); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a.matinee.State())
}

// linkMatinee asks Matinee for a sign-in code for an admin to approve on
// its /link page; GET /api/matinee shows when it's approved.
func (a *API) linkMatinee(w http.ResponseWriter, r *http.Request) {
	if !a.matineeOn(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if _, err := a.matinee.StartLink(ctx); err != nil {
		writeError(w, http.StatusBadGateway, "asking Matinee for a code: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a.matinee.State())
}

// unlinkMatinee signs Air-Mixer out of Matinee.
func (a *API) unlinkMatinee(w http.ResponseWriter, r *http.Request) {
	if !a.matineeOn(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := a.matinee.SignOut(ctx); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// matineePlayer is a Matinee player Air-Mixer can play on.
type matineePlayer struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	Online   bool   `json:"online"`
	User     string `json:"user,omitempty"`
}

// matineePlayers lists Matinee players to use as outputs, online ones
// first. Air-Mixer's own Matinee inputs are left out.
func (a *API) matineePlayers(w http.ResponseWriter, r *http.Request) {
	if !a.matineeOn(w) {
		return
	}
	if !a.matinee.SignedIn() {
		writeError(w, http.StatusConflict, matinee.ErrNotSignedIn.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	players, err := a.matinee.API().Players(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, "asking Matinee for its players: "+err.Error())
		return
	}
	out := []matineePlayer{}
	for _, p := range players {
		if p.Platform == "air-mixer" {
			continue
		}
		mp := matineePlayer{ID: p.ID, Name: p.Name, Platform: p.Platform, Online: p.Online}
		if p.User != nil {
			mp.User = p.User.Name
		}
		out = append(out, mp)
	}
	writeJSON(w, http.StatusOK, out)
}
