package matinee

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// Settings is where an Account keeps the server and token (db.DB).
type Settings interface {
	Setting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
}

const (
	keyURL   = "matinee.url"
	keyToken = "matinee.token"
	keyUser  = "matinee.user"
)

// Account is Air-Mixer's sign-in to one Matinee server, shared by every
// Matinee input and output. It is safe for concurrent use.
type Account struct {
	settings Settings
	log      *slog.Logger
	// DeviceName is what Matinee calls Air-Mixer on its /link page and under
	// Your devices.
	DeviceName string

	mu    sync.Mutex
	url   string
	token string
	user  string
	link  *Link
	stop  context.CancelFunc // ends the link poll
}

// Link is a sign-in code waiting for an admin to approve it.
type Link struct {
	UserCode        string    `json:"user_code"`
	VerificationURL string    `json:"verification_url"`
	CompleteURL     string    `json:"verification_url_complete"`
	ExpiresAt       time.Time `json:"expires_at"`
	Error           string    `json:"error,omitempty"` // why the last attempt ended
}

// State is what the API shows about the account. The token is never shown.
type State struct {
	URL      string `json:"url"`
	SignedIn bool   `json:"signed_in"`
	User     string `json:"user,omitempty"`
	Link     *Link  `json:"link,omitempty"`
}

// NewAccount loads the account from settings (which may be nil, for tests).
func NewAccount(ctx context.Context, settings Settings, defaultURL string, logger *slog.Logger) *Account {
	if logger == nil {
		logger = slog.Default()
	}
	a := &Account{settings: settings, log: logger.With("component", "matinee"), DeviceName: "Air-Mixer"}
	a.url, _ = NormalizeURL(defaultURL)
	if settings != nil {
		if v, err := settings.Setting(ctx, keyURL); err == nil {
			a.url = v
		}
		a.token, _ = settings.Setting(ctx, keyToken)
		a.user, _ = settings.Setting(ctx, keyUser)
	}
	return a
}

// API returns a client for the server, signed in when the account is.
func (a *Account) API() *API {
	a.mu.Lock()
	defer a.mu.Unlock()
	return &API{Base: a.url, Token: a.token}
}

// SignedIn reports whether there's a server and a token for it.
func (a *Account) SignedIn() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.url != "" && a.token != ""
}

// State returns the account's state.
func (a *Account) State() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := State{URL: a.url, SignedIn: a.url != "" && a.token != "", User: a.user}
	if a.link != nil {
		l := *a.link
		s.Link = &l
	}
	return s
}

func (a *Account) save(ctx context.Context, kv ...string) error {
	if a.settings == nil {
		return nil
	}
	for i := 0; i+1 < len(kv); i += 2 {
		if err := a.settings.SetSetting(ctx, kv[i], kv[i+1]); err != nil {
			return err
		}
	}
	return nil
}

// SetURL changes the server. A different server signs the account out.
func (a *Account) SetURL(ctx context.Context, raw string) error {
	u, err := NormalizeURL(raw)
	if err != nil {
		return err
	}
	a.mu.Lock()
	if u == a.url {
		a.mu.Unlock()
		return nil
	}
	a.cancelLinkLocked()
	a.url, a.token, a.user, a.link = u, "", "", nil
	a.mu.Unlock()
	return a.save(ctx, keyURL, u, keyToken, "", keyUser, "")
}

// StartLink asks the server for a sign-in code and waits in the background
// for an admin to approve it on the server's /link page.
func (a *Account) StartLink(ctx context.Context) (Link, error) {
	api := a.API()
	if api.Base == "" {
		return Link{}, errors.New("set the Matinee server's address first")
	}
	api.Token = "" // signing in again
	code, err := api.StartDeviceLink(ctx, a.DeviceName)
	if err != nil {
		return Link{}, err
	}
	l := &Link{
		UserCode: code.UserCode, VerificationURL: code.VerificationURL, CompleteURL: code.VerificationURLComplete,
		ExpiresAt: time.Now().Add(time.Duration(code.ExpiresIn) * time.Second).UTC().Truncate(time.Second),
	}
	pollCtx, cancel := context.WithDeadline(context.Background(), l.ExpiresAt)
	a.mu.Lock()
	a.cancelLinkLocked()
	a.link, a.stop = l, cancel
	a.mu.Unlock()
	interval := time.Duration(max(code.Interval, 1)) * time.Second
	go a.poll(pollCtx, api, code.DeviceCode, l, interval)
	return *l, nil
}

func (a *Account) cancelLinkLocked() {
	if a.stop != nil {
		a.stop()
		a.stop = nil
	}
}

// pollInterval can be shortened by tests.
var pollInterval = func(d time.Duration) time.Duration { return d }

func (a *Account) poll(ctx context.Context, api *API, deviceCode string, l *Link, interval time.Duration) {
	t := time.NewTicker(pollInterval(interval))
	defer t.Stop()
	fail := func(msg string) {
		a.mu.Lock()
		if a.link == l {
			a.link.Error = msg
		}
		a.mu.Unlock()
	}
	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				fail("the code expired; ask for a new one")
			}
			return
		case <-t.C:
		}
		token, user, err := api.PollDeviceLink(ctx, deviceCode)
		switch {
		case IsStatus(err, 403):
			fail("the code was turned down")
			return
		case IsStatus(err, 410), IsStatus(err, 404):
			fail("the code expired; ask for a new one")
			return
		case err != nil:
			a.log.Debug("waiting for the sign-in code", "err", err)
			continue
		case token == "":
			continue
		}
		a.mu.Lock()
		if a.link != l { // replaced or cancelled meanwhile
			a.mu.Unlock()
			return
		}
		a.token, a.user, a.link = token, user.Username, nil
		a.cancelLinkLocked()
		a.mu.Unlock()
		if err := a.save(context.Background(), keyToken, token, keyUser, user.Username); err != nil {
			a.log.Error("save Matinee sign-in", "err", err)
		}
		a.log.Info("signed in to Matinee", "url", api.Base, "user", user.Username, "admin", user.IsAdmin)
		return
	}
}

// SignOut signs Air-Mixer out of Matinee and forgets the token.
func (a *Account) SignOut(ctx context.Context) error {
	api := a.API()
	a.mu.Lock()
	a.cancelLinkLocked()
	a.token, a.user, a.link = "", "", nil
	a.mu.Unlock()
	if api.Token != "" {
		if err := api.Logout(ctx); err != nil {
			a.log.Warn("signing out of Matinee", "err", err)
		}
	}
	return a.save(ctx, keyToken, "", keyUser, "")
}

// Close stops waiting for a code.
func (a *Account) Close() {
	a.mu.Lock()
	a.cancelLinkLocked()
	a.mu.Unlock()
}
