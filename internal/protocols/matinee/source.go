package matinee

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/yeixio/air-mixer/internal/audio"
	"github.com/yeixio/air-mixer/internal/protocols"
)

const (
	// commandWait is how long one ask for commands waits.
	commandWait = 25 * time.Second
	// reportEvery is how often a playing player says where it is.
	reportEvery = 10 * time.Second
	// frames is the audio per packet: about 23 ms.
	frames = 1024
	// lead is how far ahead of real time a source may run, so routes have
	// audio ready without adding much delay.
	lead = 100 * time.Millisecond
	// maxRetry bounds the wait between attempts to reach Matinee.
	maxRetry = 30 * time.Second
)

// sourceCommands are the commands a Matinee input takes.
var sourceCommands = []string{CmdPlayItem, CmdPlay, CmdPause, CmdStop, CmdSeek, CmdPrevious, CmdNext, CmdVolume, CmdMute}

// SourceOptions configures a Source.
type SourceOptions struct {
	ID, Name string
	Account  *Account
	Version  string
	Logger   *slog.Logger
}

// Source is an Air-Mixer input that shows up in Matinee as a player.
// Whatever is played on it comes into Air-Mixer as audio. It implements
// protocols.Server.
type Source struct {
	id, name, player string
	account          *Account
	version          string
	log              *slog.Logger
	out              chan protocols.Packet

	cancel context.CancelFunc
	done   chan struct{}

	mu      sync.Mutex
	current *playback
	volume  float64
	muted   bool
	wake    chan struct{} // a change to report right away
}

// playback is one item playing (or paused) on a Source.
type playback struct {
	item     Item
	from     time.Duration // where the open stream starts
	played   int64         // frames delivered since from
	paused   bool
	stopPlay context.CancelFunc // nil while paused
	done     chan struct{}
}

func (p *playback) position() time.Duration {
	return p.from + time.Duration(p.played)*time.Second/audio.SampleRate
}

// NewSource returns a Matinee input. Call Start to register it with Matinee.
func NewSource(o SourceOptions) *Source {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Source{
		id: o.ID, name: o.Name, player: PlayerID(o.ID), account: o.Account, version: o.Version,
		log:    o.Logger.With("component", "matinee-source", "server", o.ID),
		out:    make(chan protocols.Packet, 16),
		volume: 1,
		wake:   make(chan struct{}, 1),
	}
}

var unsafeID = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// PlayerID is the id an Air-Mixer input uses as a Matinee player. It stays
// the same across restarts, so the player keeps its place in Home
// Assistant.
func PlayerID(serverID string) string {
	id := "airmixer-" + unsafeID.ReplaceAllString(serverID, "-")
	if len(id) > 64 {
		id = id[:64]
	}
	return id
}

func (s *Source) ID() string                     { return s.id }
func (s *Source) Name() string                   { return s.name }
func (s *Source) Protocol() string               { return "matinee" }
func (s *Source) Audio() <-chan protocols.Packet { return s.out }

// PlayerID is this input's id in Matinee.
func (s *Source) PlayerID() string { return s.player }

// Start registers the player with Matinee in the background. Matinee being
// away, or Air-Mixer not being signed in yet, isn't an error: the input
// keeps trying.
func (s *Source) Start(context.Context) error {
	if s.account == nil {
		return errors.New("matinee: no account")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel, s.done = cancel, make(chan struct{})
	go func() {
		defer close(s.done)
		s.run(ctx)
	}()
	return nil
}

// Stop says goodbye to Matinee and stops playing.
func (s *Source) Stop() error {
	if s.cancel == nil {
		return nil
	}
	s.cancel()
	<-s.done
	s.stopPlayback(true)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.account.API().Goodbye(ctx, s.player); err != nil && !errors.Is(err, ErrNotSignedIn) {
		s.log.Debug("saying goodbye to Matinee", "err", err)
	}
	return nil
}

// run says hello, then takes commands, until ctx ends. It says hello again
// whenever Matinee forgets the player, and waits longer after each failure.
func (s *Source) run(ctx context.Context) {
	retry := time.Second
	backoff := func() bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(retry):
		}
		retry = min(retry*2, maxRetry)
		return true
	}
	go s.reporter(ctx)
	for ctx.Err() == nil {
		api := s.account.API()
		if api.Base == "" {
			if !backoff() {
				return
			}
			continue
		}
		if err := s.report(ctx); err != nil {
			s.log.Debug("Matinee isn't answering", "err", err)
			if !backoff() {
				return
			}
			continue
		}
		retry = time.Second
		for ctx.Err() == nil {
			cmds, err := s.account.API().Commands(ctx, s.player, commandWait)
			if IsStatus(err, 404) {
				break // Matinee restarted: say hello again
			}
			if err != nil {
				if ctx.Err() == nil {
					s.log.Debug("asking Matinee for commands", "err", err)
					backoff()
				}
				break
			}
			retry = time.Second
			for _, c := range cmds {
				s.handle(ctx, c)
			}
		}
	}
}

// reporter says where playback is every reportEvery while playing, and
// right away after a change.
func (s *Source) reporter(ctx context.Context) {
	t := time.NewTicker(reportEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.mu.Lock()
			playing := s.current != nil && !s.current.paused
			s.mu.Unlock()
			if !playing {
				continue
			}
		case <-s.wake:
		}
		if err := s.report(ctx); err != nil && ctx.Err() == nil {
			s.log.Debug("reporting to Matinee", "err", err)
		}
	}
}

func (s *Source) changed() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Source) report(ctx context.Context) error {
	s.mu.Lock()
	r := Report{Name: s.name, Platform: "air-mixer", App: "Air-Mixer", Version: s.version, Commands: sourceCommands}
	if p := s.current; p != nil {
		vol := s.volume
		r.Playing = &Playing{ItemID: p.item.ID, PositionMs: p.position().Milliseconds(), Paused: p.paused, Volume: &vol, Muted: s.muted}
		if p.item.DurationMs != nil {
			r.Playing.DurationMs = *p.item.DurationMs
		}
	}
	s.mu.Unlock()
	_, err := s.account.API().Hello(ctx, s.player, r)
	return err
}

// handle carries out one command.
func (s *Source) handle(ctx context.Context, c Command) {
	if c.From != "" {
		s.log.Info("command from Matinee", "type", c.Type, "from", c.From)
	}
	switch c.Type {
	case CmdPlayItem:
		id := c.ItemID
		if c.Item != nil && c.Item.ID != 0 {
			id = c.Item.ID // a show or season, already turned into an episode
		}
		var from *time.Duration
		if c.PositionMs != nil {
			d := time.Duration(*c.PositionMs) * time.Millisecond
			from = &d
		}
		if err := s.PlayItem(ctx, id, from); err != nil {
			s.log.Warn("playing from Matinee", "item", id, "err", err)
		}
	case CmdPlay:
		s.Resume()
	case CmdPause:
		s.Pause()
	case CmdStop:
		s.stopPlayback(true)
	case CmdSeek:
		if c.PositionMs != nil {
			s.Seek(time.Duration(*c.PositionMs) * time.Millisecond)
		}
	case CmdPrevious:
		s.Seek(0)
	case CmdNext:
		s.next(ctx)
	case CmdVolume:
		if c.Volume != nil {
			s.mu.Lock()
			s.volume = max(0, min(1, *c.Volume))
			s.mu.Unlock()
		}
	case CmdMute:
		if c.Muted != nil {
			s.mu.Lock()
			s.muted = *c.Muted
			s.mu.Unlock()
		}
	default:
		return
	}
	s.changed()
}

// PlayItem plays a Matinee item, from where the person left off unless
// from is given.
func (s *Source) PlayItem(ctx context.Context, id int64, from *time.Duration) error {
	it, err := s.account.API().Item(ctx, id)
	if err != nil {
		return err
	}
	if !it.Playable {
		return fmt.Errorf("matinee: %q has nothing to play", it.Title)
	}
	start := time.Duration(it.PositionMs) * time.Millisecond
	if from != nil {
		start = *from
	}
	if it.Kind == "station" {
		start = 0
	}
	s.stopPlayback(true)
	s.mu.Lock()
	s.startLocked(&playback{item: it, from: start})
	s.mu.Unlock()
	s.log.Info("playing from Matinee", "item", it.ID, "title", it.Title)
	s.changed()
	return nil
}

// startLocked starts the audio of p. s.mu is held.
func (s *Source) startLocked(p *playback) {
	ctx, cancel := context.WithCancel(context.Background())
	p.paused, p.stopPlay, p.done = false, cancel, make(chan struct{})
	s.current = p
	go func() {
		defer close(p.done)
		err := s.stream(ctx, p)
		if ctx.Err() != nil {
			return // stopped, paused or replaced
		}
		if err != nil {
			s.log.Warn("Matinee audio stopped", "item", p.item.ID, "err", err)
		}
		s.finished(p)
	}()
}

// halt stops p's audio and waits for it, dropping audio not yet taken by
// the router so a pause is heard at once. s.mu must not be held.
func (s *Source) halt(p *playback) {
	if p != nil && p.stopPlay != nil {
		p.stopPlay()
		<-p.done
	}
	for {
		select {
		case <-s.out:
		default:
			return
		}
	}
}

// Pause stops the audio and keeps the place.
func (s *Source) Pause() {
	s.mu.Lock()
	p := s.current
	if p == nil || p.paused {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	s.halt(p)
	s.mu.Lock()
	if s.current == p {
		p.paused, p.stopPlay = true, nil
		p.from, p.played = p.position(), 0
	}
	s.mu.Unlock()
	s.saveProgress(p)
}

// Resume plays again from where it paused.
func (s *Source) Resume() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.current; p != nil && p.paused {
		s.startLocked(p)
	}
}

// Seek moves to pos, playing or paused as before.
func (s *Source) Seek(pos time.Duration) {
	s.mu.Lock()
	p := s.current
	if p == nil || p.item.Kind == "station" {
		s.mu.Unlock()
		return
	}
	paused := p.paused
	s.mu.Unlock()
	s.halt(p)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current != p {
		return
	}
	p.from, p.played, p.stopPlay = max(0, pos), 0, nil
	if paused {
		p.paused = true
		return
	}
	s.startLocked(p)
}

// stopPlayback stops what's playing; save keeps its place in Matinee.
func (s *Source) stopPlayback(save bool) {
	s.mu.Lock()
	p := s.current
	s.current = nil
	s.mu.Unlock()
	if p == nil {
		return
	}
	s.halt(p)
	if save {
		s.saveProgress(p)
	}
}

func (s *Source) saveProgress(p *playback) {
	if p.item.Kind == "station" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.account.API().SaveProgress(ctx, p.item.ID, p.position().Milliseconds()); err != nil {
		s.log.Debug("saving the place in Matinee", "err", err)
	}
}

// finished is called when an item's audio ends by itself: a song goes on
// to the next song of its album, anything else stops.
func (s *Source) finished(p *playback) {
	s.mu.Lock()
	if s.current != p {
		s.mu.Unlock()
		return
	}
	s.current = nil
	s.mu.Unlock()
	if p.item.Kind != "station" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = s.account.API().SaveProgress(ctx, p.item.ID, p.position().Milliseconds())
		cancel()
	}
	if p.item.Kind == "track" && s.playNextTrack(p.item) {
		return
	}
	s.changed()
}

// next skips to the next song of the album.
func (s *Source) next(context.Context) {
	s.mu.Lock()
	p := s.current
	s.mu.Unlock()
	if p != nil && p.item.Kind == "track" {
		s.playNextTrack(p.item)
	}
}

// playNextTrack plays the song after it on its album, and reports whether
// there was one.
func (s *Source) playNextTrack(it Item) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tracks, err := s.account.API().Tracks(ctx, it.ID)
	if err != nil {
		return false
	}
	for i, t := range tracks {
		if t.ID == it.ID && i+1 < len(tracks) {
			zero := time.Duration(0)
			return s.PlayItem(ctx, tracks[i+1].ID, &zero) == nil
		}
	}
	return false
}

// stream plays p's audio into the router in real time until it ends or
// ctx does.
func (s *Source) stream(ctx context.Context, p *playback) error {
	body, err := s.account.API().Audio(ctx, p.item.ID, p.from)
	if err != nil {
		return err
	}
	defer body.Close()
	r := bufio.NewReaderSize(body, 64<<10)
	format, err := readWAVHeader(r)
	if err != nil {
		return err
	}
	if format.bits != 16 {
		return fmt.Errorf("matinee: %d-bit audio isn't supported", format.bits)
	}
	pf := audio.Format{SampleRate: format.rate, Channels: format.channels}
	buf := make([]byte, frames*format.channels*2)
	begin := time.Now()
	var sent int64 // frames
	for {
		n, err := io.ReadFull(r, buf)
		n -= n % (format.channels * 2)
		if n > 0 {
			s.mu.Lock()
			gain := float32(s.volume)
			if s.muted {
				gain = 0
			}
			s.mu.Unlock()
			samples := make([]float32, n/2)
			for i := range samples {
				samples[i] = audio.Int16ToFloat(int16(binary.LittleEndian.Uint16(buf[2*i:]))) * gain
			}
			// Keep to real time, a little ahead.
			due := begin.Add(time.Duration(sent) * time.Second / time.Duration(format.rate))
			if wait := time.Until(due) - lead; wait > 0 {
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(wait):
				}
			}
			select {
			case <-ctx.Done():
				return nil
			case s.out <- protocols.Packet{Samples: samples, Format: pf}:
			}
			got := int64(len(samples) / format.channels)
			sent += got
			s.mu.Lock()
			// Played is in router frames for position(); the source rate
			// is the router's for Matinee, but convert in case it isn't.
			p.played += int64(math.Round(float64(got) * audio.SampleRate / float64(format.rate)))
			s.mu.Unlock()
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}

type wavFormat struct{ rate, channels, bits int }

// readWAVHeader reads a PCM WAV's header up to its audio.
func readWAVHeader(r io.Reader) (wavFormat, error) {
	var riff [12]byte
	if _, err := io.ReadFull(r, riff[:]); err != nil {
		return wavFormat{}, fmt.Errorf("matinee: reading the audio: %w", err)
	}
	if string(riff[0:4]) != "RIFF" || string(riff[8:12]) != "WAVE" {
		return wavFormat{}, errors.New("matinee: the audio isn't a WAV (is Matinee up to date?)")
	}
	var f wavFormat
	for {
		var hdr [8]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return wavFormat{}, fmt.Errorf("matinee: reading the audio: %w", err)
		}
		id, size := string(hdr[0:4]), binary.LittleEndian.Uint32(hdr[4:])
		switch strings.TrimSpace(id) {
		case "fmt":
			if size < 16 || size > 1024 {
				return wavFormat{}, errors.New("matinee: bad WAV format chunk")
			}
			b := make([]byte, size+size%2)
			if _, err := io.ReadFull(r, b); err != nil {
				return wavFormat{}, err
			}
			if tag := binary.LittleEndian.Uint16(b[0:]); tag != 1 && tag != 0xFFFE {
				return wavFormat{}, fmt.Errorf("matinee: WAV encoding %d isn't PCM", tag)
			}
			f.channels = int(binary.LittleEndian.Uint16(b[2:]))
			f.rate = int(binary.LittleEndian.Uint32(b[4:]))
			f.bits = int(binary.LittleEndian.Uint16(b[14:]))
		case "data":
			if f.rate <= 0 || f.channels <= 0 {
				return wavFormat{}, errors.New("matinee: WAV audio before its format")
			}
			return f, nil
		default:
			if size > 1<<20 {
				return wavFormat{}, errors.New("matinee: bad WAV chunk")
			}
			if _, err := io.CopyN(io.Discard, r, int64(size+size%2)); err != nil {
				return wavFormat{}, err
			}
		}
	}
}
