// Package playback plays audio from a URL on chosen outputs: clips,
// text-to-speech, alarms and streams sent by home automation such as Home
// Assistant.
//
// Each playback is a temporary router source with a transient route to its
// outputs. Announcements duck (lower) or interrupt (mute) whatever else
// plays on those outputs and restore it when they end; the other audio
// keeps flowing underneath, so nothing has to be paused or resumed.
package playback

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yeixio/air-mixer/internal/audio"
	"github.com/yeixio/air-mixer/internal/audio/decode"
	"github.com/yeixio/air-mixer/internal/router"
)

// Router is the part of router.Router playback uses.
type Router interface {
	RegisterSource(id, name string, stream <-chan []float32)
	UnregisterSource(id string)
	AddRoute(router.Route) error
	UpdateRoute(router.Route) error
	RemoveRoute(id string) error
}

// Mode says what happens to other audio on the outputs.
type Mode string

const (
	// ModeMix plays alongside other audio, which announcements duck.
	ModeMix Mode = "mix"
	// ModeDuck lowers other audio to DuckLevel while playing.
	ModeDuck Mode = "duck"
	// ModeInterrupt silences other audio while playing.
	ModeInterrupt Mode = "interrupt"
)

// DefaultDuckLevel is the volume other audio plays at under a ModeDuck
// announcement.
const DefaultDuckLevel = 0.2

// Playback states.
const (
	StatePlaying  = "playing"
	StateFinished = "finished"
	StateStopped  = "stopped"
	StateFailed   = "failed"
)

// Errors returned by Player.
var (
	ErrNotFound = errors.New("playback not found")
	ErrBusy     = errors.New("too many playbacks at once")
	ErrClosed   = errors.New("player is closed")
)

// InvalidError reports a request that cannot be played.
type InvalidError struct{ Reason string }

func (e *InvalidError) Error() string { return e.Reason }

func invalid(format string, args ...any) error { return &InvalidError{fmt.Sprintf(format, args...)} }

// FetchError reports a URL that could not be fetched or decoded.
type FetchError struct{ Err error }

func (e *FetchError) Error() string { return e.Err.Error() }
func (e *FetchError) Unwrap() error { return e.Err }

// Request describes audio to play.
type Request struct {
	// URL of an MP3 or WAV file or stream, over http or https.
	URL string `json:"url"`
	// Outputs are the client names to play on. The caller resolves IDs
	// and "all outputs" to names.
	Outputs []string `json:"outputs"`
	// Mode defaults to ModeDuck.
	Mode Mode `json:"mode,omitempty"`
	// Volume of the playback, 0 to 1. Nil means 1.
	Volume *float32 `json:"volume,omitempty"`
	// DuckLevel is the volume of other audio under ModeDuck, 0 to 1. Nil
	// means DefaultDuckLevel.
	DuckLevel *float32 `json:"duck_level,omitempty"`
	// Repeat plays the URL this many times. Zero means once.
	Repeat int `json:"repeat,omitempty"`
	// Loop repeats until stopped or MaxDuration passes.
	Loop bool `json:"loop,omitempty"`
	// MaxDuration, in seconds, stops playback after this long. Zero means
	// the player's limit.
	MaxDuration float64 `json:"max_duration,omitempty"`
	// Title is shown in status, for example the announcement's text.
	Title string `json:"title,omitempty"`
}

// Item is a playback and its state.
type Item struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	Title     string    `json:"title,omitempty"`
	Outputs   []string  `json:"outputs"`
	Mode      Mode      `json:"mode"`
	Volume    float32   `json:"volume"`
	DuckLevel *float32  `json:"duck_level,omitempty"`
	Repeat    int       `json:"repeat"`
	Loop      bool      `json:"loop,omitempty"`
	State     string    `json:"state"`
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"started_at"`
	// Position is how many seconds have been played, across repeats.
	Position float64 `json:"position"`
}

// Options configures a Player.
type Options struct {
	Logger *slog.Logger
	// Client fetches URLs. Nil means a client with sensible timeouts.
	Client *http.Client
	// MaxDuration caps every playback. Zero means one hour.
	MaxDuration time.Duration
	// MaxActive caps simultaneous playbacks. Zero means 16.
	MaxActive int
	// MaxBytes caps how much is downloaded per pass. Zero means 256 MiB.
	MaxBytes int64
}

const (
	chunk    = 20 * time.Millisecond  // audio sent to the router at a time
	lead     = 100 * time.Millisecond // how far ahead of real time to stay
	tail     = 250 * time.Millisecond // wait after the end for the mixer to drain
	priority = math.MaxInt32          // announcements duck everything else
)

// Player runs playbacks. Methods are safe for concurrent use.
type Player struct {
	router      Router
	log         *slog.Logger
	client      *http.Client
	maxDuration time.Duration
	maxActive   int
	maxBytes    int64

	mu     sync.Mutex
	items  map[string]*playing
	closed bool
	wg     sync.WaitGroup
	seq    atomic.Uint64
}

type playing struct {
	cancel context.CancelFunc
	done   chan struct{}
	frames atomic.Int64

	mu    sync.Mutex
	item  Item
	route router.Route
}

func (p *playing) snapshot() Item {
	p.mu.Lock()
	defer p.mu.Unlock()
	it := p.item
	it.Outputs = slices.Clone(it.Outputs)
	it.Position = float64(p.frames.Load()) / audio.SampleRate
	return it
}

// New returns a Player that plays through r.
func New(r Router, opts Options) *Player {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Client == nil {
		opts.Client = &http.Client{Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 15 * time.Second,
		}}
	}
	if opts.MaxDuration <= 0 {
		opts.MaxDuration = time.Hour
	}
	if opts.MaxActive <= 0 {
		opts.MaxActive = 16
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = 256 << 20
	}
	return &Player{
		router: r, log: opts.Logger.With("component", "playback"), client: opts.Client,
		maxDuration: opts.MaxDuration, maxActive: opts.MaxActive, maxBytes: opts.MaxBytes,
		items: make(map[string]*playing),
	}
}

func validate(req *Request) error {
	u, err := url.Parse(req.URL)
	switch {
	case req.URL == "":
		return invalid("url is required")
	case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
		return invalid("url must be an http or https URL")
	case len(req.Outputs) == 0:
		return invalid("no outputs to play on")
	case req.Volume != nil && (*req.Volume < 0 || *req.Volume > 1):
		return invalid("volume must be between 0 and 1")
	case req.DuckLevel != nil && (*req.DuckLevel < 0 || *req.DuckLevel > 1):
		return invalid("duck_level must be between 0 and 1")
	case req.Repeat < 0 || req.Repeat > 100:
		return invalid("repeat must be between 0 and 100")
	case req.MaxDuration < 0:
		return invalid("max_duration cannot be negative")
	case len(req.Title) > 1024:
		return invalid("title is too long")
	}
	switch req.Mode {
	case "":
		req.Mode = ModeDuck
	case ModeMix, ModeDuck, ModeInterrupt:
	default:
		return invalid("mode must be mix, duck or interrupt")
	}
	return nil
}

// Handle follows one playback.
type Handle struct{ p *playing }

// Item returns the playback's current state, or its final state once Done
// is closed.
func (h Handle) Item() Item { return h.p.snapshot() }

// Done is closed when playback ends.
func (h Handle) Done() <-chan struct{} { return h.p.done }

// Play fetches the URL and starts playing it. It returns once playback has
// started, or with an *InvalidError or *FetchError if it cannot.
func (p *Player) Play(ctx context.Context, req Request) (Handle, error) {
	if err := validate(&req); err != nil {
		return Handle{}, err
	}
	req.Outputs = uniq(req.Outputs)

	p.mu.Lock()
	switch {
	case p.closed:
		p.mu.Unlock()
		return Handle{}, ErrClosed
	case len(p.items) >= p.maxActive:
		p.mu.Unlock()
		return Handle{}, ErrBusy
	}
	p.mu.Unlock()

	limit := p.maxDuration
	if req.MaxDuration > 0 {
		limit = min(limit, time.Duration(req.MaxDuration*float64(time.Second)))
	}
	runCtx, cancel := context.WithTimeout(context.Background(), limit)
	first, closeFirst, err := p.open(ctx, runCtx, req.URL)
	if err != nil {
		cancel()
		return Handle{}, err
	}

	id := fmt.Sprintf("play-%d", p.seq.Add(1))
	volume := float32(1)
	if req.Volume != nil {
		volume = *req.Volume
	}
	var duck *float32
	switch req.Mode {
	case ModeDuck:
		d := float32(DefaultDuckLevel)
		if req.DuckLevel != nil {
			d = *req.DuckLevel
		}
		duck = &d
	case ModeInterrupt:
		duck = new(float32)
	}
	repeat := max(1, req.Repeat)
	pl := &playing{
		cancel: cancel,
		done:   make(chan struct{}),
		item: Item{
			ID: id, URL: req.URL, Title: req.Title, Outputs: req.Outputs, Mode: req.Mode,
			Volume: volume, DuckLevel: duck, Repeat: repeat, Loop: req.Loop,
			State: StatePlaying, StartedAt: time.Now(),
		},
		route: router.Route{
			ID: id, Name: "Playback: " + displayName(req), SourceName: id, DestNames: req.Outputs,
			MixMode: router.MixModeMix, Volume: volume, Enabled: true, Duck: duck, Transient: true,
		},
	}
	if duck != nil {
		pl.route.Priority = priority
	}

	p.mu.Lock()
	if p.closed || len(p.items) >= p.maxActive {
		p.mu.Unlock()
		closeFirst()
		cancel()
		if p.closed {
			return Handle{}, ErrClosed
		}
		return Handle{}, ErrBusy
	}
	stream := make(chan []float32, 16)
	p.router.RegisterSource(id, id, stream)
	if err := p.router.AddRoute(pl.route); err != nil {
		p.router.UnregisterSource(id)
		p.mu.Unlock()
		closeFirst()
		cancel()
		return Handle{}, err
	}
	p.items[id] = pl
	p.wg.Add(1)
	p.mu.Unlock()

	p.log.Info("playback started", "id", id, "url", redact(req.URL), "outputs", req.Outputs, "mode", req.Mode)
	go p.run(runCtx, pl, req.URL, first, closeFirst, stream)
	return Handle{pl}, nil
}

func displayName(req Request) string {
	if req.Title != "" {
		return req.Title
	}
	return redact(req.URL)
}

// redact drops the query string, which may hold an access token.
func redact(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(invalid url)"
	}
	u.RawQuery, u.User, u.Fragment = "", nil, ""
	return u.String()
}

func uniq(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if n != "" && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// open fetches url and returns a decoder for it. reqCtx bounds the request
// until the response headers arrive; bodyCtx bounds reading the body.
func (p *Player) open(reqCtx, bodyCtx context.Context, rawURL string) (decode.Decoder, func(), error) {
	ctx, cancel := context.WithCancel(bodyCtx)
	stop := context.AfterFunc(reqCtx, cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		cancel()
		return nil, nil, invalid("invalid url: %v", err)
	}
	req.Header.Set("User-Agent", "Air-Mixer")
	req.Header.Set("Accept", "audio/mpeg, audio/wav, audio/*;q=0.8")
	resp, err := p.client.Do(req) //nolint:bodyclose // closed by the returned func or on error below
	if err != nil {
		cancel()
		return nil, nil, &FetchError{fmt.Errorf("fetch %s: %w", redact(rawURL), err)}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		resp.Body.Close()
		cancel()
		return nil, nil, &FetchError{fmt.Errorf("fetch %s: %s", redact(rawURL), resp.Status)}
	}
	body := io.LimitReader(resp.Body, p.maxBytes)
	dec, err := decode.New(body, resp.Header.Get("Content-Type"))
	stop() // from here on only bodyCtx ends the download
	if err != nil {
		resp.Body.Close()
		cancel()
		return nil, nil, &FetchError{err}
	}
	return dec, func() {
		resp.Body.Close()
		if c, ok := dec.(io.Closer); ok {
			c.Close() //nolint:errcheck,gosec // an error reading was already reported by Read
		}
		cancel()
	}, nil
}

func (p *Player) run(ctx context.Context, pl *playing, rawURL string, dec decode.Decoder, closeDec func(), stream chan []float32) {
	defer p.wg.Done()
	defer close(pl.done)

	start := time.Now()
	var sent time.Duration // audio sent, in playing time
	var err error
	pl.mu.Lock()
	passes := pl.item.Repeat
	loop := pl.item.Loop
	pl.mu.Unlock()

	for pass := 0; loop || pass < passes; pass++ {
		if pass > 0 {
			if dec, closeDec, err = p.open(ctx, ctx, rawURL); err != nil {
				break
			}
		}
		var n time.Duration
		n, err = p.stream(ctx, pl, dec, start, sent, stream)
		closeDec()
		sent += n
		if err != nil || n == 0 {
			break // stopped, failed, or an empty file that would loop forever
		}
	}
	close(stream)

	state := StateFinished
	switch {
	case errors.Is(err, context.Canceled):
		state, err = StateStopped, nil
	case errors.Is(err, context.DeadlineExceeded):
		err = nil // reached max_duration
		state = StateFinished
	case err != nil:
		state = StateFailed
	}
	if state == StateFinished {
		// Let the router play out what is queued before un-ducking.
		ahead := sent - time.Since(start)
		time.Sleep(max(0, ahead) + tail)
	}

	p.mu.Lock()
	delete(p.items, pl.item.ID)
	p.mu.Unlock()
	if rerr := p.router.RemoveRoute(pl.item.ID); rerr != nil && !errors.Is(rerr, router.ErrNotFound) {
		p.log.Warn("remove playback route", "id", pl.item.ID, "err", rerr)
	}
	p.router.UnregisterSource(pl.item.ID)

	pl.mu.Lock()
	pl.item.State = state
	if err != nil {
		pl.item.Error = err.Error()
	}
	pl.mu.Unlock()
	if err != nil {
		p.log.Warn("playback failed", "id", pl.item.ID, "err", err)
	} else {
		p.log.Info("playback ended", "id", pl.item.ID, "state", state, "played", sent.Round(time.Millisecond))
	}
}

// stream decodes dec in real time into the router and returns how much
// audio it sent. start and offset place it on the playback's timeline.
func (p *Player) stream(ctx context.Context, pl *playing, dec decode.Decoder, start time.Time, offset time.Duration, out chan<- []float32) (time.Duration, error) {
	f := dec.Format()
	conv, err := audio.NewConverter(f, audio.Internal)
	if err != nil {
		return 0, err
	}
	buf := make([]float32, max(1, f.SampleRate*int(chunk/time.Millisecond)/1000)*f.Channels)
	var frames int64
	for {
		n, rerr := dec.Read(buf)
		if n > 0 {
			samples, err := conv.Process(slices.Clone(buf[:n]))
			if err != nil {
				return dur(frames), err
			}
			if len(samples) > 0 {
				// Stay no more than lead ahead of real time.
				if wait := offset + dur(frames) - time.Since(start) - lead; wait > 0 {
					t := time.NewTimer(wait)
					select {
					case <-ctx.Done():
						t.Stop()
						return dur(frames), ctx.Err()
					case <-t.C:
					}
				}
				select {
				case out <- samples:
				case <-ctx.Done():
					return dur(frames), ctx.Err()
				}
				k := int64(len(samples) / audio.Channels)
				frames += k
				pl.frames.Add(k)
			}
		}
		if errors.Is(rerr, io.EOF) {
			return dur(frames), nil
		}
		if rerr != nil {
			if ctx.Err() != nil {
				return dur(frames), ctx.Err()
			}
			return dur(frames), fmt.Errorf("decode: %w", rerr)
		}
		if ctx.Err() != nil {
			return dur(frames), ctx.Err()
		}
	}
}

func dur(frames int64) time.Duration {
	return time.Duration(frames) * time.Second / audio.SampleRate
}

// List returns the playbacks in progress, oldest first.
func (p *Player) List() []Item {
	p.mu.Lock()
	out := make([]Item, 0, len(p.items))
	for _, pl := range p.items {
		out = append(out, pl.snapshot())
	}
	p.mu.Unlock()
	slices.SortFunc(out, func(a, b Item) int { return a.StartedAt.Compare(b.StartedAt) })
	return out
}

// Get returns one playback in progress.
func (p *Player) Get(id string) (Item, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pl, ok := p.items[id]
	if !ok {
		return Item{}, ErrNotFound
	}
	return pl.snapshot(), nil
}

// Stop ends a playback at once.
func (p *Player) Stop(id string) error {
	p.mu.Lock()
	pl, ok := p.items[id]
	p.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	pl.cancel()
	<-pl.done
	return nil
}

// StopOutput stops playback on one output and returns how many playbacks
// it changed. A playback on other outputs too keeps playing there.
func (p *Player) StopOutput(name string) int {
	p.mu.Lock()
	var stop []*playing
	changed := 0
	for _, pl := range p.items {
		pl.mu.Lock()
		if i := slices.Index(pl.item.Outputs, name); i >= 0 {
			changed++
			if len(pl.item.Outputs) == 1 {
				stop = append(stop, pl)
			} else {
				pl.item.Outputs = slices.Delete(slices.Clone(pl.item.Outputs), i, i+1)
				pl.route.DestNames = slices.Clone(pl.item.Outputs)
				if err := p.router.UpdateRoute(pl.route); err != nil {
					p.log.Warn("update playback route", "id", pl.item.ID, "err", err)
				}
			}
		}
		pl.mu.Unlock()
	}
	p.mu.Unlock()
	for _, pl := range stop {
		pl.cancel()
		<-pl.done
	}
	return changed
}

// StopAll stops every playback.
func (p *Player) StopAll() int {
	p.mu.Lock()
	all := make([]*playing, 0, len(p.items))
	for _, pl := range p.items {
		all = append(all, pl)
	}
	p.mu.Unlock()
	for _, pl := range all {
		pl.cancel()
		<-pl.done
	}
	return len(all)
}

// Close stops every playback and refuses new ones.
func (p *Player) Close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	p.StopAll()
	p.wg.Wait()
}
