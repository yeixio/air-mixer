// Package pulse plays audio on a PulseAudio sink, such as a Bluetooth
// speaker paired with the host. It speaks the PulseAudio native protocol
// in pure Go, so it also works against PipeWire's PulseAudio server and
// from inside a container that mounts the host's socket.
//
// Unlike the local output, any number of pulse outputs can run in one
// process, each on its own sink, which lets one Air-Mixer feed every
// speaker in a room.
package pulse

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/yeixio/air-mixer/internal/audio"
	"github.com/yeixio/air-mixer/internal/protocols"
)

const (
	// maxBuffered bounds the audio queued ahead of the sink.
	maxBuffered = audio.SampleRate * audio.Channels / 2 // 500 ms
	// checkInterval is how often a playing stream is checked for a sink
	// that went away.
	checkInterval = 2 * time.Second
)

// Options configures a pulse output.
type Options struct {
	ID, Name string
	// Server is a PulseAudio server string, such as
	// "unix:/run/pulse/native". Empty means $PULSE_SERVER, then the
	// default socket.
	Server string
	// Sink selects the sink: its name, its description, or the Bluetooth
	// address of the device behind it. Empty means the default sink.
	Sink string
	// Delay holds audio back before playing it, to line this output up
	// with slower ones.
	Delay  time.Duration
	Logger *slog.Logger
}

// stream is an open playback stream.
type stream interface {
	// Check reports why the stream can no longer play where it should,
	// or nil.
	Check() error
	// Close stops playback and releases the connection. It is safe to
	// call more than once.
	Close()
	// Sink names the sink the stream plays to.
	Sink() string
}

// dial opens a playback stream that pulls audio from fill. Tests replace
// it.
var dial = dialPulse

// Client plays audio on a PulseAudio sink.
type Client struct {
	opts Options
	log  *slog.Logger
	buf  *audio.Playout

	mu   sync.Mutex
	st   stream
	lost error
	stop chan struct{}
	done chan struct{}
}

// NewClient returns a pulse output. Call Connect to open the sink.
func NewClient(o Options) *Client {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Client{
		opts: o,
		log:  o.Logger.With("component", "pulse-client", "client", o.ID),
		buf:  audio.NewPlayout(maxBuffered, audio.Samples(o.Delay)),
	}
}

func (c *Client) ID() string       { return c.opts.ID }
func (c *Client) Name() string     { return c.opts.Name }
func (c *Client) Protocol() string { return "pulse" }

// Connected reports whether the stream is open and playing on its sink.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.st != nil && c.lost == nil
}

// Connect opens a playback stream on the sink. It fails while the sink
// does not exist, for example while a Bluetooth speaker is switched off,
// so the Manager keeps retrying until it appears.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.st != nil {
		return nil
	}
	c.buf.Reset()
	type result struct {
		st  stream
		err error
	}
	res := make(chan result, 1)
	go func() {
		st, err := dial(c.opts, c.buf.Fill)
		res <- result{st, err}
	}()
	var r result
	select {
	case r = <-res:
	case <-ctx.Done():
		go func() { // close the stream if it opens after all
			if r := <-res; r.st != nil {
				r.st.Close()
			}
		}()
		return ctx.Err()
	}
	if r.err != nil {
		return r.err
	}
	c.st, c.lost = r.st, nil
	c.stop, c.done = make(chan struct{}), make(chan struct{})
	go c.watch(r.st, c.stop, c.done)
	c.log.Info("pulse output opened", "sink", r.st.Sink())
	return nil
}

// watch closes the stream as soon as it stops playing on its sink, so
// audio meant for a speaker that went away does not move to another one.
func (c *Client) watch(st stream, stop, done chan struct{}) {
	defer close(done)
	t := time.NewTicker(checkInterval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		if err := st.Check(); err != nil {
			st.Close()
			c.mu.Lock()
			if c.st == st {
				c.lost = err
			}
			c.mu.Unlock()
			c.log.Warn("pulse output lost", "sink", st.Sink(), "err", err)
			return
		}
	}
}

// Disconnect stops playback and closes the connection.
func (c *Client) Disconnect() error {
	c.mu.Lock()
	st, stop, done := c.st, c.stop, c.done
	c.st, c.lost, c.stop, c.done = nil, nil, nil, nil
	c.mu.Unlock()
	if st == nil {
		return nil
	}
	close(stop)
	<-done
	st.Close()
	c.buf.Reset()
	c.log.Info("pulse output closed")
	return nil
}

// Send queues audio in the internal format.
func (c *Client) Send(p protocols.Packet) error {
	if p.Format != audio.Internal {
		return fmt.Errorf("pulse: expected %+v audio, got %+v", audio.Internal, p.Format)
	}
	c.mu.Lock()
	st, lost := c.st, c.lost
	c.mu.Unlock()
	switch {
	case st == nil:
		return protocols.ErrNotConnected
	case lost != nil:
		return fmt.Errorf("pulse: %w: %w", protocols.ErrConnectionLost, lost)
	}
	if !c.buf.Push(p.Samples) {
		return protocols.ErrBufferFull
	}
	return nil
}
