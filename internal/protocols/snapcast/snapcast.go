// Package snapcast plays audio into a Snapcast server, so every Snapcast
// client in the house plays it in sync.
//
// Air-Mixer connects to a TCP source on snapserver and writes raw 16-bit
// stereo PCM. Add one to snapserver.conf, for example:
//
//	source = tcp://0.0.0.0:4953?name=Air-Mixer&mode=server
//
// snapserver expects 48 kHz unless the source says otherwise with
// sampleformat=44100:16:2; set the output's sample_rate to match.
package snapcast

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/yeixio/air-mixer/internal/audio"
	"github.com/yeixio/air-mixer/internal/protocols"
)

const (
	// DefaultPort is the port in the example source line above.
	DefaultPort = "4953"
	// DefaultSampleRate is snapserver's default sample format's rate.
	DefaultSampleRate = 48000
	// queued bounds the buffers waiting to be written, about 1.5 s.
	queued = 64
	// writeTimeout is how long a write may stall before the server is
	// taken to be gone.
	writeTimeout = 5 * time.Second
)

// Options configures a Snapcast output.
type Options struct {
	ID, Name string
	// Address is the snapserver TCP source, host or host:port.
	Address string
	// SampleRate is the rate the source expects; DefaultSampleRate if 0.
	SampleRate int
	Logger     *slog.Logger
}

// Client writes audio to a snapserver TCP source.
type Client struct {
	opts Options
	log  *slog.Logger
	conv *audio.Converter

	mu   sync.Mutex
	conn net.Conn
	out  chan []byte
	lost error
	done chan struct{}
}

// New returns a Snapcast output.
func New(o Options) (*Client, error) {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.SampleRate == 0 {
		o.SampleRate = DefaultSampleRate
	}
	if _, _, err := net.SplitHostPort(o.Address); err != nil {
		o.Address = net.JoinHostPort(o.Address, DefaultPort)
	}
	conv, err := audio.NewConverter(audio.Internal, audio.Format{SampleRate: o.SampleRate, Channels: 2})
	if err != nil {
		return nil, err
	}
	return &Client{opts: o, conv: conv, log: o.Logger.With("component", "snapcast-client", "client", o.ID)}, nil
}

func (c *Client) ID() string       { return c.opts.ID }
func (c *Client) Name() string     { return c.opts.Name }
func (c *Client) Protocol() string { return "snapcast" }

// Address is the snapserver source the output writes to.
func (c *Client) Address() string { return c.opts.Address }

// Connected reports whether the output is writing to snapserver.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil && c.lost == nil
}

// Connect opens the TCP source.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return nil
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", c.opts.Address)
	if err != nil {
		return fmt.Errorf("snapcast: %w", err)
	}
	c.conn, c.lost = conn, nil
	c.out, c.done = make(chan []byte, queued), make(chan struct{})
	go c.write(conn, c.out, c.done)
	c.log.Info("snapcast output connected", "address", c.opts.Address)
	return nil
}

func (c *Client) write(conn net.Conn, out <-chan []byte, done chan<- struct{}) {
	defer close(done)
	for b := range out {
		_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		if _, err := conn.Write(b); err != nil {
			c.mu.Lock()
			if c.conn == conn {
				c.lost = err
			}
			c.mu.Unlock()
			c.log.Warn("snapcast output lost", "err", err)
			for range out { // drain until Disconnect closes it
			}
			return
		}
	}
}

// Disconnect closes the connection.
func (c *Client) Disconnect() error {
	c.mu.Lock()
	conn, out, done := c.conn, c.out, c.done
	c.conn, c.out, c.done, c.lost = nil, nil, nil, nil
	c.mu.Unlock()
	if conn == nil {
		return nil
	}
	close(out)
	err := conn.Close()
	<-done
	return err
}

// Send converts audio and queues it for snapserver.
func (c *Client) Send(p protocols.Packet) error {
	if p.Format != audio.Internal {
		return fmt.Errorf("snapcast: expected %+v audio, got %+v", audio.Internal, p.Format)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.conn == nil:
		return protocols.ErrNotConnected
	case c.lost != nil:
		return fmt.Errorf("snapcast: %w: %w", protocols.ErrConnectionLost, c.lost)
	}
	samples, err := c.conv.Process(p.Samples)
	if err != nil {
		return err
	}
	b := make([]byte, 2*len(samples))
	for i, v := range samples {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(audio.FloatToInt16(v)))
	}
	select {
	case c.out <- b:
		return nil
	default:
		return protocols.ErrBufferFull
	}
}
