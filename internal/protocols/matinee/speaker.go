package matinee

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yeixio/air-mixer/internal/protocols"
	"github.com/yeixio/air-mixer/internal/protocols/httpstream"
)

// watchEvery is how often a Matinee output checks on its player, to start
// the stream again when the player comes back.
const watchEvery = 15 * time.Second

// SpeakerOptions configures a Speaker.
type SpeakerOptions struct {
	ID, Name string
	// Player is the Matinee player's id (GET /api/matinee/players).
	Player string
	// LibraryID is the music library the output's station goes in; 0 for
	// the first music library.
	LibraryID int64
	Account   *Account
	Streams   *httpstream.Registry
	// PublicURL is how Matinee reaches Air-Mixer, such as
	// http://192.168.1.10:9001. Empty means this machine's address on the
	// network Matinee is on, at WebPort.
	PublicURL string
	WebPort   int
	// Settings remembers each output's station between restarts.
	Settings Settings
	Logger   *slog.Logger
}

// Speaker is an Air-Mixer output that plays on a Matinee player. It serves
// its audio as a live stream, adds that to Matinee as a radio station on
// the home network, and asks the player to play the station. It implements
// protocols.Client.
type Speaker struct {
	o      SpeakerOptions
	stream *httpstream.Stream
	log    *slog.Logger

	mu      sync.Mutex
	station int64
	cancel  context.CancelFunc
	done    chan struct{}
}

// NewSpeaker returns a Matinee output. Call Connect to start it.
func NewSpeaker(o SpeakerOptions) *Speaker {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Speaker{
		o:      o,
		stream: httpstream.New(o.ID, o.Name, o.Streams, o.Logger),
		log:    o.Logger.With("component", "matinee-speaker", "client", o.ID, "player", o.Player),
	}
}

func (s *Speaker) ID() string       { return s.o.ID }
func (s *Speaker) Name() string     { return s.o.Name }
func (s *Speaker) Protocol() string { return "matinee" }
func (s *Speaker) Connected() bool  { return s.stream.Connected() }

// Send queues audio for the player.
func (s *Speaker) Send(p protocols.Packet) error { return s.stream.Send(p) }

// StationName is what the output's station is called in Matinee.
func (s *Speaker) StationName() string { return "Air-Mixer · " + s.o.Name }

// Connect serves the stream and starts it on the player. It fails while
// Air-Mixer isn't signed in to Matinee or the player isn't open, and the
// manager tries again.
func (s *Speaker) Connect(ctx context.Context) error {
	if s.o.Player == "" {
		return fmt.Errorf("matinee: no player: %w", protocols.ErrUnsupportedDevice)
	}
	if s.o.Account == nil || !s.o.Account.SignedIn() {
		return ErrNotSignedIn
	}
	api := s.o.Account.API()
	streamURL, err := s.streamURL(api.Base)
	if err != nil {
		return err
	}
	if err := s.stream.Connect(ctx); err != nil {
		return err
	}
	station, err := s.ensureStation(ctx, api, streamURL)
	if err == nil {
		err = s.start(ctx, api, station)
	}
	if err != nil {
		_ = s.stream.Disconnect()
		return err
	}
	watch, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.station, s.cancel, s.done = station, cancel, make(chan struct{})
	done := s.done
	s.mu.Unlock()
	go func() {
		defer close(done)
		s.watch(watch, station)
	}()
	s.log.Info("playing on Matinee", "station", station, "stream", streamURL)
	return nil
}

// Disconnect stops the player if it's still playing the output, and stops
// serving the stream.
func (s *Speaker) Disconnect() error {
	s.mu.Lock()
	cancel, done, station := s.cancel, s.done, s.station
	s.cancel, s.done = nil, nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	if station != 0 && s.o.Account != nil && s.o.Account.SignedIn() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		api := s.o.Account.API()
		if p, err := api.Player(ctx, s.o.Player); err == nil && p.playing(station) {
			_ = api.Send(ctx, s.o.Player, Command{Type: CmdStop})
		}
		cancel()
	}
	return s.stream.Disconnect()
}

func (p Player) playing(item int64) bool {
	return p.Online && p.Playing != nil && p.Playing.Item != nil && p.Playing.Item.ID == item
}

// start asks the player to play the station.
func (s *Speaker) start(ctx context.Context, api *API, station int64) error {
	err := api.Send(ctx, s.o.Player, Command{Type: CmdPlayItem, ItemID: station})
	switch {
	case IsStatus(err, 404):
		return fmt.Errorf("matinee: player %s isn't open", s.o.Player)
	case IsStatus(err, 409):
		return fmt.Errorf("matinee: player %s can't play from Air-Mixer right now", s.o.Player)
	}
	return err
}

// watch starts the station again on the player each time it comes back
// after being closed, so a TV that was switched off plays again when it's
// on. It leaves the player alone otherwise: someone may have picked
// something else on purpose.
func (s *Speaker) watch(ctx context.Context, station int64) {
	t := time.NewTicker(watchEvery)
	defer t.Stop()
	online := true
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		api := s.o.Account.API()
		p, err := api.Player(ctx, s.o.Player)
		now := err == nil && p.Online
		if now && !online && !p.playing(station) {
			s.log.Info("player is back, starting Air-Mixer on it again")
			if err := s.start(ctx, api, station); err != nil {
				s.log.Warn("starting the player again", "err", err)
				now = false // try again next time
			}
		}
		online = now
	}
}

// ensureStation finds or adds the output's station in Matinee, and
// returns its item id.
func (s *Speaker) ensureStation(ctx context.Context, api *API, streamURL string) (int64, error) {
	key := "matinee.station." + s.o.ID
	if s.o.Settings != nil {
		if v, err := s.o.Settings.Setting(ctx, key); err == nil {
			if id, stored, ok := strings.Cut(v, " "); ok && stored == streamURL+"@"+api.Base {
				if n, err := strconv.ParseInt(id, 10, 64); err == nil {
					if it, err := api.Item(ctx, n); err == nil && it.Kind == "station" {
						return n, nil
					}
				}
			}
		}
	}
	lib, err := s.library(ctx, api)
	if err != nil {
		return 0, err
	}
	it, err := api.AddHomeStation(ctx, lib, s.StationName(), streamURL)
	switch {
	case IsStatus(err, 409):
		// Added before; find it by name.
		stations, lerr := api.Stations(ctx, lib)
		if lerr != nil {
			return 0, lerr
		}
		found := false
		for _, st := range stations {
			if st.Title == s.StationName() {
				it, found = st, true
				break
			}
		}
		if !found {
			return 0, fmt.Errorf("matinee: the library has a station for %s under another name; delete it in Matinee and Air-Mixer adds it again", streamURL)
		}
	case IsStatus(err, 403):
		return 0, errors.New("matinee: only an admin can add Air-Mixer's stream; sign in to Matinee as an admin")
	case IsStatus(err, 502):
		return 0, fmt.Errorf("matinee: Matinee couldn't reach %s; set server.public_url to an address Matinee can reach", streamURL)
	case err != nil:
		return 0, err
	}
	if s.o.Settings != nil {
		_ = s.o.Settings.SetSetting(ctx, key, strconv.FormatInt(it.ID, 10)+" "+streamURL+"@"+api.Base)
	}
	return it.ID, nil
}

// library is the music library for the station.
func (s *Speaker) library(ctx context.Context, api *API) (int64, error) {
	if s.o.LibraryID != 0 {
		return s.o.LibraryID, nil
	}
	libs, err := api.Libraries(ctx)
	if err != nil {
		return 0, err
	}
	for _, l := range libs {
		if l.Kind == "music" {
			return l.ID, nil
		}
	}
	return 0, errors.New("matinee: Air-Mixer plays on Matinee players as a radio station, which needs a music library; add one in Matinee")
}

// streamURL is the output's stream as Matinee reaches it.
func (s *Speaker) streamURL(matineeBase string) (string, error) {
	base := strings.TrimRight(s.o.PublicURL, "/")
	if base == "" {
		ip, err := localAddrFor(matineeBase)
		if err != nil {
			return "", fmt.Errorf("matinee: finding Air-Mixer's address for Matinee (set server.public_url): %w", err)
		}
		base = "http://" + net.JoinHostPort(ip, strconv.Itoa(s.o.WebPort))
	}
	return base + "/api/clients/" + url.PathEscape(s.o.ID) + "/stream", nil
}

// localAddrFor is this machine's address on the way to a URL's host. No
// packet is sent: connecting a UDP socket only picks the route.
func localAddrFor(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	port := u.Port()
	if port == "" {
		port = "80"
	}
	c, err := net.Dial("udp", net.JoinHostPort(u.Hostname(), port))
	if err != nil {
		return "", err
	}
	defer c.Close()
	addr, ok := c.LocalAddr().(*net.UDPAddr)
	if !ok {
		return "", errors.New("no local address")
	}
	return addr.IP.String(), nil
}
