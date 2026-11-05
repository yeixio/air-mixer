// Package matinee connects Air-Mixer to Matinee, the self-hosted media
// server (https://github.com/yeixio/matinee), in both directions:
//
//   - A Matinee input (Source) shows up in Matinee as a player, like a TV
//     or a phone. Whatever Matinee, Home Assistant or anyone with the API
//     plays on it comes into Air-Mixer as audio, and routes take it to any
//     speakers.
//   - A Matinee output (Speaker) makes a Matinee player one of Air-Mixer's
//     speakers. Air-Mixer serves the output's audio as a live stream (see
//     package httpstream), adds it to Matinee as a radio station on the
//     home network, and tells the player to play it.
//
// Air-Mixer signs in to Matinee once, the way Home Assistant does: it asks
// for a code and an admin approves it on Matinee's /link page (see
// Account).
package matinee

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrNotSignedIn means Air-Mixer has no Matinee server, or isn't signed in
// to it yet.
var ErrNotSignedIn = errors.New("matinee: not signed in (open Settings → Matinee)")

// StatusError is an answer from Matinee other than the one asked for.
type StatusError struct {
	Code    int
	Message string
}

func (e *StatusError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("matinee: %d %s", e.Code, e.Message)
	}
	return fmt.Sprintf("matinee: %d %s", e.Code, http.StatusText(e.Code))
}

// IsStatus reports whether err is a StatusError with that code.
func IsStatus(err error, code int) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Code == code
}

// API calls one Matinee server's API (internal/api/openapi.yaml in the
// Matinee repository).
type API struct {
	Base  string // such as http://192.168.1.20:7171
	Token string // from Account; empty for the home guest
	HTTP  *http.Client
}

// apiClient is for calls that answer quickly. Streams use streamClient,
// which has no overall time limit.
var (
	apiClient    = &http.Client{Timeout: 40 * time.Second} // longer than a 25 s command wait
	streamClient = &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: 20 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	}}
)

// NormalizeURL checks a Matinee address and fills in http:// when it is
// left out.
func NormalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("%q is not a web address", raw)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}

func (a *API) client() *http.Client {
	if a.HTTP != nil {
		return a.HTTP
	}
	return apiClient
}

func (a *API) request(ctx context.Context, method, path string, body any) (*http.Request, error) {
	if a.Base == "" {
		return nil, ErrNotSignedIn
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.Base+"/api/v1"+path, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Air-Mixer")
	if a.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.Token)
	}
	return req, nil
}

// do sends a JSON request and decodes a JSON answer into out, if not nil.
// It returns the status code for answers in the 2xx range.
func (a *API) do(ctx context.Context, method, path string, body, out any) (int, error) {
	req, err := a.request(ctx, method, path, body)
	if err != nil {
		return 0, err
	}
	resp, err := a.client().Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, statusError(resp)
	}
	if out != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out); err != nil {
			return resp.StatusCode, fmt.Errorf("matinee: %s %s: %w", method, path, err)
		}
	}
	return resp.StatusCode, nil
}

func statusError(resp *http.Response) error {
	var e struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&e)
	return &StatusError{Code: resp.StatusCode, Message: e.Error}
}

// Command types, from Matinee's CommandType.
const (
	CmdPlay     = "play"
	CmdPause    = "pause"
	CmdStop     = "stop"
	CmdSeek     = "seek"
	CmdNext     = "next"
	CmdPrevious = "previous"
	CmdPlayItem = "playItem"
	CmdVolume   = "volume"
	CmdMute     = "mute"
)

// Playing is what a player reports it is playing.
type Playing struct {
	ItemID     int64    `json:"itemId"`
	PositionMs int64    `json:"positionMs"`
	DurationMs int64    `json:"durationMs,omitempty"`
	Paused     bool     `json:"paused"`
	Buffering  bool     `json:"buffering,omitempty"`
	Volume     *float64 `json:"volume,omitempty"`
	Muted      bool     `json:"muted,omitempty"`
}

// Report is a player's hello (PUT /clients/{id}).
type Report struct {
	Name     string   `json:"name"`
	Platform string   `json:"platform"`
	App      string   `json:"app,omitempty"`
	Version  string   `json:"version,omitempty"`
	Commands []string `json:"commands"`
	Playing  *Playing `json:"playing"`
}

// Command is something a player is asked to do.
type Command struct {
	ID         string   `json:"id,omitempty"`
	Type       string   `json:"type"`
	PositionMs *int64   `json:"positionMs,omitempty"`
	ItemID     int64    `json:"itemId,omitempty"`
	Item       *Item    `json:"item,omitempty"`
	Volume     *float64 `json:"volume,omitempty"`
	Muted      *bool    `json:"muted,omitempty"`
	From       string   `json:"from,omitempty"`
}

// Item is the part of a Matinee item Air-Mixer uses.
type Item struct {
	ID         int64  `json:"id"`
	LibraryID  int64  `json:"libraryId,omitempty"`
	Kind       string `json:"kind"`
	Title      string `json:"title"`
	DurationMs *int64 `json:"durationMs,omitempty"`
	PositionMs int64  `json:"positionMs,omitempty"`
	Playable   bool   `json:"playable,omitempty"`
}

// Player is a Matinee player (a Client in Matinee's API).
type Player struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Platform string   `json:"platform"`
	App      string   `json:"app"`
	Commands []string `json:"commands"`
	Online   bool     `json:"online"`
	User     *struct {
		Name  string `json:"name"`
		Guest bool   `json:"guest"`
	} `json:"user,omitempty"`
	Playing *struct {
		Item   *Item `json:"item"`
		Paused bool  `json:"paused"`
	} `json:"playing,omitempty"`
}

// Hello says this player is open and what it plays. It returns the longest
// wait to ask for commands with.
func (a *API) Hello(ctx context.Context, id string, r Report) (int, error) {
	var out struct {
		PollSeconds int `json:"pollSeconds"`
	}
	_, err := a.do(ctx, http.MethodPut, "/clients/"+url.PathEscape(id), r, &out)
	return out.PollSeconds, err
}

// Goodbye says this player closed.
func (a *API) Goodbye(ctx context.Context, id string) error {
	_, err := a.do(ctx, http.MethodDelete, "/clients/"+url.PathEscape(id), nil, nil)
	return err
}

// Commands waits up to wait for commands for player id. A 404 means
// Matinee doesn't know the player (it restarted): say hello again.
func (a *API) Commands(ctx context.Context, id string, wait time.Duration) ([]Command, error) {
	var out struct {
		Commands []json.RawMessage `json:"commands"`
	}
	path := "/clients/" + url.PathEscape(id) + "/commands?wait=" + strconv.Itoa(int(wait.Seconds()))
	if _, err := a.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	cmds := make([]Command, 0, len(out.Commands))
	for _, raw := range out.Commands {
		var c Command
		if json.Unmarshal(raw, &c) == nil {
			cmds = append(cmds, c)
		}
	}
	return cmds, nil
}

// Send asks player id to do c.
func (a *API) Send(ctx context.Context, id string, c Command) error {
	_, err := a.do(ctx, http.MethodPost, "/clients/"+url.PathEscape(id)+"/commands", c, nil)
	return err
}

// Players lists the players this account may control, online ones first.
func (a *API) Players(ctx context.Context) ([]Player, error) {
	var out []Player
	_, err := a.do(ctx, http.MethodGet, "/clients", nil, &out)
	return out, err
}

// Player returns one player.
func (a *API) Player(ctx context.Context, id string) (Player, error) {
	var out Player
	_, err := a.do(ctx, http.MethodGet, "/clients/"+url.PathEscape(id), nil, &out)
	return out, err
}

// Item returns one item.
func (a *API) Item(ctx context.Context, id int64) (Item, error) {
	var out Item
	_, err := a.do(ctx, http.MethodGet, "/items/"+strconv.FormatInt(id, 10), nil, &out)
	return out, err
}

// Tracks returns every track of an album, or of a track's album, in order.
func (a *API) Tracks(ctx context.Context, id int64) ([]Item, error) {
	var out []Item
	_, err := a.do(ctx, http.MethodGet, "/items/"+strconv.FormatInt(id, 10)+"/tracks", nil, &out)
	return out, err
}

// SaveProgress saves where playback of an item is.
func (a *API) SaveProgress(ctx context.Context, id, positionMs int64) error {
	_, err := a.do(ctx, http.MethodPut, "/items/"+strconv.FormatInt(id, 10)+"/progress", map[string]int64{"positionMs": positionMs}, nil)
	return err
}

// Library is a Matinee library.
type Library struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// Libraries lists the libraries this account sees.
func (a *API) Libraries(ctx context.Context) ([]Library, error) {
	var out []Library
	_, err := a.do(ctx, http.MethodGet, "/libraries", nil, &out)
	return out, err
}

// Stations lists a music library's radio stations.
func (a *API) Stations(ctx context.Context, libraryID int64) ([]Item, error) {
	var out []Item
	_, err := a.do(ctx, http.MethodGet, "/libraries/"+strconv.FormatInt(libraryID, 10)+"/stations", nil, &out)
	return out, err
}

// AddHomeStation adds a radio station on the home network to a music
// library (admins only).
func (a *API) AddHomeStation(ctx context.Context, libraryID int64, name, streamURL string) (Item, error) {
	var out Item
	_, err := a.do(ctx, http.MethodPost, "/stations", map[string]any{
		"libraryId": libraryID, "url": streamURL, "name": name, "home": true, "tags": []string{"Air-Mixer"},
	}, &out)
	return out, err
}

// Audio opens an item's audio as an endless 16-bit 44.1 kHz stereo WAV
// from start (GET /items/{id}/audio).
func (a *API) Audio(ctx context.Context, id int64, start time.Duration) (io.ReadCloser, error) {
	path := "/items/" + strconv.FormatInt(id, 10) + "/audio"
	if start > 0 {
		path += "?startMs=" + strconv.FormatInt(start.Milliseconds(), 10)
	}
	req, err := a.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "audio/wav")
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, statusError(resp)
	}
	return resp.Body, nil
}

// DeviceCode is a sign-in code waiting for approval.
type DeviceCode struct {
	DeviceCode              string `json:"deviceCode"`
	UserCode                string `json:"userCode"`
	VerificationURL         string `json:"verificationUrl"`
	VerificationURLComplete string `json:"verificationUrlComplete"`
	ExpiresIn               int    `json:"expiresIn"`
	Interval                int    `json:"interval"`
}

// StartDeviceLink asks for a code to sign in with.
func (a *API) StartDeviceLink(ctx context.Context, deviceName string) (DeviceCode, error) {
	var out DeviceCode
	_, err := a.do(ctx, http.MethodPost, "/auth/device", map[string]string{"deviceName": deviceName, "platform": "air-mixer"}, &out)
	return out, err
}

// LinkUser is who approved a code.
type LinkUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	IsAdmin  bool   `json:"isAdmin"`
}

// PollDeviceLink asks whether a code was approved. It returns the token
// once it was, and "" while it waits.
func (a *API) PollDeviceLink(ctx context.Context, deviceCode string) (string, LinkUser, error) {
	var out struct {
		Status string   `json:"status"`
		Token  string   `json:"token"`
		User   LinkUser `json:"user"`
	}
	code, err := a.do(ctx, http.MethodPost, "/auth/device/token", map[string]string{"deviceCode": deviceCode}, &out)
	if err != nil {
		return "", LinkUser{}, err
	}
	if code == http.StatusAccepted || out.Token == "" {
		return "", LinkUser{}, nil
	}
	return out.Token, out.User, nil
}

// Me returns who the token signs in as.
func (a *API) Me(ctx context.Context) (LinkUser, error) {
	var out LinkUser
	_, err := a.do(ctx, http.MethodGet, "/me", nil, &out)
	return out, err
}

// Logout signs this device out.
func (a *API) Logout(ctx context.Context) error {
	_, err := a.do(ctx, http.MethodPost, "/auth/logout", nil, nil)
	return err
}
