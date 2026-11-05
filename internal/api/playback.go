package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/yeixio/air-mixer/internal/db"
	"github.com/yeixio/air-mixer/internal/playback"
)

// Player plays audio from URLs on outputs.
type Player interface {
	Play(ctx context.Context, req playback.Request) (playback.Handle, error)
	List() []playback.Item
	Get(id string) (playback.Item, error)
	Stop(id string) error
	StopOutput(name string) int
	StopAll() int
}

// playRequest is the body of POST /api/play.
type playRequest struct {
	playback.Request
	// Wait makes the request return when playback ends, not when it
	// starts.
	Wait bool `json:"wait"`
}

type playbackList struct {
	Playback []playback.Item `json:"playback"`
}

func (a *API) registerPlayback(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/play", a.play)
	mux.HandleFunc("GET /api/playback", a.listPlayback)
	mux.HandleFunc("GET /api/playback/{id}", a.getPlayback)
	mux.HandleFunc("DELETE /api/playback", a.stopPlayback)
	mux.HandleFunc("DELETE /api/playback/{id}", a.stopPlaybackID)
}

func (a *API) playerReady(w http.ResponseWriter) bool {
	if a.player == nil {
		writeError(w, http.StatusServiceUnavailable, "playback is not available")
		return false
	}
	return true
}

// resolveOutputs maps client IDs or names to names. An empty list means
// every enabled client.
func (a *API) resolveOutputs(ctx context.Context, wanted []string) ([]string, error) {
	clients, err := a.svc.Clients(ctx)
	if err != nil {
		return nil, err
	}
	if len(wanted) == 0 {
		var names []string
		for _, c := range clients {
			if c.Enabled {
				names = append(names, c.Name)
			}
		}
		if len(names) == 0 {
			return nil, &playback.InvalidError{Reason: "there are no enabled outputs"}
		}
		return names, nil
	}
	names := make([]string, 0, len(wanted))
	for _, w := range wanted {
		c := findClient(clients, w)
		if c == nil {
			return nil, &playback.InvalidError{Reason: "unknown output " + quote(w)}
		}
		names = append(names, c.Name)
	}
	return names, nil
}

// findClient matches an ID, then a name, then a name ignoring case.
func findClient(clients []*db.Client, s string) *db.Client {
	for _, c := range clients {
		if c.ID == s {
			return c
		}
	}
	for _, c := range clients {
		if c.Name == s {
			return c
		}
	}
	for _, c := range clients {
		if strings.EqualFold(c.Name, s) {
			return c
		}
	}
	return nil
}

func quote(s string) string { return `"` + s + `"` }

func (a *API) playbackFail(w http.ResponseWriter, r *http.Request, err error) {
	var invalid *playback.InvalidError
	var fetch *playback.FetchError
	switch {
	case errors.As(err, &invalid):
		writeError(w, http.StatusBadRequest, invalid.Reason)
	case errors.As(err, &fetch):
		writeError(w, http.StatusBadGateway, fetch.Error())
	case errors.Is(err, playback.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, playback.ErrBusy):
		writeError(w, http.StatusTooManyRequests, err.Error())
	case errors.Is(err, playback.ErrClosed):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	default:
		a.fail(w, r, err)
	}
}

func (a *API) play(w http.ResponseWriter, r *http.Request) {
	if !a.playerReady(w) {
		return
	}
	var req playRequest
	if !decode(w, r, &req) {
		return
	}
	outputs, err := a.resolveOutputs(r.Context(), req.Outputs)
	if err != nil {
		a.playbackFail(w, r, err)
		return
	}
	req.Outputs = outputs
	h, err := a.player.Play(r.Context(), req.Request)
	if err != nil {
		a.playbackFail(w, r, err)
		return
	}
	if !req.Wait {
		writeJSON(w, http.StatusAccepted, h.Item())
		return
	}
	select {
	case <-h.Done():
		writeJSON(w, http.StatusOK, h.Item())
	case <-r.Context().Done():
		// The caller went away; playback carries on.
	}
}

func (a *API) listPlayback(w http.ResponseWriter, r *http.Request) {
	if !a.playerReady(w) {
		return
	}
	writeJSON(w, http.StatusOK, playbackList{Playback: a.player.List()})
}

func (a *API) getPlayback(w http.ResponseWriter, r *http.Request) {
	if !a.playerReady(w) {
		return
	}
	item, err := a.player.Get(r.PathValue("id"))
	if err != nil {
		a.playbackFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

// stopPlayback stops everything, or everything on ?output=.
func (a *API) stopPlayback(w http.ResponseWriter, r *http.Request) {
	if !a.playerReady(w) {
		return
	}
	output := r.URL.Query().Get("output")
	if output == "" {
		writeJSON(w, http.StatusOK, map[string]int{"stopped": a.player.StopAll()})
		return
	}
	names, err := a.resolveOutputs(r.Context(), []string{output})
	if err != nil {
		a.playbackFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"stopped": a.player.StopOutput(names[0])})
}

func (a *API) stopPlaybackID(w http.ResponseWriter, r *http.Request) {
	if !a.playerReady(w) {
		return
	}
	if err := a.player.Stop(r.PathValue("id")); err != nil {
		a.playbackFail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
