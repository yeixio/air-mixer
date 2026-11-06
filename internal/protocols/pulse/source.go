package pulse

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	pa "github.com/jfreymuth/pulse"
	"github.com/jfreymuth/pulse/proto"

	"github.com/yeixio/air-mixer/internal/audio"
	"github.com/yeixio/air-mixer/internal/protocols"
)

// ErrNoSource is returned while the configured source does not exist.
var ErrNoSource = errors.New("pulse: source not found")

// How often a capture input tries again to open its source, for example
// while no phone is connected over Bluetooth, and checks that it still
// records from it. Tests shorten them.
var (
	retryInterval       = 2 * time.Second
	sourceCheckInterval = checkInterval
)

// SourceOptions configures a capture input.
type SourceOptions struct {
	ID, Name string
	// Protocol is what the input reports as its protocol; "pulse" if
	// empty. The Bluetooth input reuses this capture.
	Protocol string
	// Server is a PulseAudio server string; empty means $PULSE_SERVER,
	// then the default socket.
	Server string
	// Source selects what to record: a source's name or description, the
	// Bluetooth address of a phone playing to the host, or a sink's name
	// followed by ".monitor" to record what plays on it. Empty means the
	// default source.
	Source string
	Logger *slog.Logger
}

// recorder is an open record stream.
type recorder interface {
	Check() error
	Close()
	Source() string
}

// dialRecord opens a record stream that passes audio to write. Tests
// replace it.
var dialRecord = dialPulseRecord

// Source records from a PulseAudio or PipeWire source, such as a line-in,
// a USB audio interface, or a phone playing to the host over Bluetooth.
// It keeps trying to open the source while it doesn't exist.
type Source struct {
	opts  SourceOptions
	log   *slog.Logger
	audio chan protocols.Packet

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	active string // the source recording now, or ""
}

// NewSource returns a capture input. Call Start to begin recording.
func NewSource(o SourceOptions) *Source {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Protocol == "" {
		o.Protocol = "pulse"
	}
	return &Source{
		opts:  o,
		log:   o.Logger.With("component", "pulse-source", "server", o.ID),
		audio: make(chan protocols.Packet, 64),
	}
}

func (s *Source) ID() string                     { return s.opts.ID }
func (s *Source) Name() string                   { return s.opts.Name }
func (s *Source) Protocol() string               { return s.opts.Protocol }
func (s *Source) Audio() <-chan protocols.Packet { return s.audio }

// Recording names the source being recorded, or "" while it waits for
// one.
func (s *Source) Recording() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

// Start begins recording in the background. It doesn't fail when the
// source is missing; it waits for it.
func (s *Source) Start(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return errors.New("pulse: source already started")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel, s.done = cancel, make(chan struct{})
	go s.run(ctx)
	return nil
}

// Stop stops recording.
func (s *Source) Stop() error {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	<-done
	return nil
}

func (s *Source) run(ctx context.Context) {
	defer close(s.done)
	var lastErr string
	for {
		rec, err := dialRecord(s.opts, s.write)
		if err == nil {
			s.setActive(rec.Source())
			s.log.Info("recording", "source", rec.Source())
			lastErr = ""
			err = s.watch(ctx, rec)
			rec.Close()
			s.setActive("")
			if ctx.Err() != nil {
				return
			}
			s.log.Warn("recording stopped, waiting for the source", "err", err)
		} else if msg := err.Error(); msg != lastErr {
			// Log each new reason once, not every retry.
			lastErr = msg
			s.log.Info("waiting for the source", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(retryInterval):
		}
	}
}

// watch returns when the stream stops recording from its source.
func (s *Source) watch(ctx context.Context, rec recorder) error {
	t := time.NewTicker(sourceCheckInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := rec.Check(); err != nil {
				return err
			}
		}
	}
}

func (s *Source) setActive(name string) {
	s.mu.Lock()
	s.active = name
	s.mu.Unlock()
}

// write passes recorded audio on without blocking PulseAudio.
func (s *Source) write(b []float32) {
	select {
	case s.audio <- protocols.Packet{Samples: slices.Clone(b), Format: audio.Internal}:
	default:
	}
}

// paRecord is a record stream on a real PulseAudio server.
type paRecord struct {
	client *pa.Client
	rec    *pa.RecordStream
	source *pa.Source
	once   sync.Once
}

func dialPulseRecord(o SourceOptions, write func([]float32)) (recorder, error) {
	client, err := pa.NewClient(
		pa.ClientServerString(o.Server),
		pa.ClientApplicationName("Air-Mixer"),
		pa.ClientTimeout(requestTimeout),
	)
	if err != nil {
		return nil, fmt.Errorf("pulse: connect to server: %w", err)
	}
	src, err := findSource(client, o.Source)
	if err != nil {
		client.Close()
		return nil, err
	}
	w := pa.Float32Writer(func(b []float32) (int, error) { write(b); return len(b), nil })
	rec, err := client.NewRecord(w,
		pa.RecordStereo,
		pa.RecordSampleRate(audio.SampleRate),
		pa.RecordLatency(latency.Seconds()),
		pa.RecordSource(src),
		pa.RecordMediaName(o.Name),
	)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("pulse: record from %s: %w", src.ID(), err)
	}
	rec.Start()
	return &paRecord{client: client, rec: rec, source: src}, nil
}

func (r *paRecord) Source() string { return r.source.ID() }

func (r *paRecord) Check() error {
	if r.rec.Closed() {
		if err := r.rec.Error(); err != nil {
			return fmt.Errorf("stream closed: %w", err)
		}
		return errors.New("stream closed by the server")
	}
	var info proto.GetSourceOutputInfoReply
	if err := r.client.RawRequest(&proto.GetSourceOutputInfo{SourceOutpuIndex: r.rec.StreamIndex()}, &info); err != nil {
		return fmt.Errorf("query stream: %w", err)
	}
	// PulseAudio moves a stream to another source when its own goes
	// away, as when a phone disconnects. Stop rather than record the
	// wrong thing.
	if info.SourceIndex != r.source.SourceIndex() {
		return fmt.Errorf("source %s went away", r.source.ID())
	}
	return nil
}

func (r *paRecord) Close() {
	r.once.Do(func() {
		r.rec.Close()
		r.client.Close()
	})
}

// findSource resolves a source the way findSink resolves a sink. A
// Bluetooth address matches bluez_source.<MAC>.a2dp_source (PulseAudio)
// and bluez_input.<MAC>.* (PipeWire), never a speaker's sink monitor.
func findSource(c *pa.Client, spec string) (*pa.Source, error) {
	if spec == "" {
		s, err := c.DefaultSource()
		if err != nil {
			return nil, fmt.Errorf("pulse: default source: %w", err)
		}
		return s, nil
	}
	sources, err := c.ListSources()
	if err != nil {
		return nil, fmt.Errorf("pulse: list sources: %w", err)
	}
	candidates := make([]sinkInfo, len(sources))
	for i, s := range sources {
		candidates[i] = sinkInfo{id: s.ID(), description: s.Name()}
	}
	i, err := matchSource(candidates, spec)
	if err != nil {
		return nil, err
	}
	return sources[i], nil
}

func matchSource(sources []sinkInfo, spec string) (int, error) {
	if macAddress.MatchString(spec) {
		// A Bluetooth speaker's sink has a monitor source with the same
		// address; skip it so only a phone's audio matches.
		var inputs []sinkInfo
		var index []int
		for i, s := range sources {
			if !isMonitor(s.id) {
				inputs = append(inputs, s)
				index = append(index, i)
			}
		}
		i, err := matchSink(inputs, spec)
		if err != nil {
			return -1, noSource(sources, spec)
		}
		return index[i], nil
	}
	i, err := matchSink(sources, spec)
	if err != nil {
		return -1, noSource(sources, spec)
	}
	return i, nil
}

func isMonitor(id string) bool { return strings.HasSuffix(id, ".monitor") }

func noSource(sources []sinkInfo, spec string) error {
	names := make([]string, len(sources))
	for i, s := range sources {
		names[i] = s.id
	}
	return fmt.Errorf("%w: %q (available: %s)", ErrNoSource, spec, strings.Join(names, ", "))
}
