// Package cast plays audio on Google Cast devices: Chromecast Audio, Nest
// and Google Home speakers, speaker groups, and TVs with Chromecast built
// in.
//
// The output is served as a live WAV stream (package httpstream), and the
// device is told to play it in Google's Default Media Receiver over the
// Cast v2 protocol: TLS on port 8009, framed protobuf messages with JSON
// payloads. Cast devices buffer a few seconds of a live stream, so a Cast
// output plays later than the others; set the others' delay_ms to match.
//
// The receiver app is launched when audio starts and stopped after a
// minute of silence, so the speaker is free for other apps. If someone
// casts something else meanwhile, Air-Mixer leaves it alone until its own
// audio stops and starts again.
package cast

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/yeixio/air-mixer/internal/protocols"
	"github.com/yeixio/air-mixer/internal/protocols/httpstream"
)

const (
	// DefaultPort is where Cast devices listen.
	DefaultPort = "8009"
	// mediaReceiver is Google's Default Media Receiver app.
	mediaReceiver = "CC1AD845"

	nsConnection = "urn:x-cast:com.google.cast.tp.connection"
	nsHeartbeat  = "urn:x-cast:com.google.cast.tp.heartbeat"
	nsReceiver   = "urn:x-cast:com.google.cast.receiver"
	nsMedia      = "urn:x-cast:com.google.cast.media"

	senderID   = "sender-airmixer"
	receiverID = "receiver-0"
)

// Timings; tests shorten them.
var (
	heartbeat = 5 * time.Second
	// deadAfter without any message from the device means it is gone.
	deadAfter = 20 * time.Second
	// idleStop is the silence after which the receiver app is stopped.
	idleStop = time.Minute
	// resumeGap is the silence that counts as "audio started again" after
	// another app took the device over.
	resumeGap = 5 * time.Second
	// silent is the level below which a buffer counts as silence.
	silent float32 = 1.0 / 32768
)

// Options configures a Cast output.
type Options struct {
	ID, Name string
	// Address is the device, host or host:port (default port 8009).
	Address string
	// Streams serves the output's audio; required.
	Streams *httpstream.Registry
	// PublicURL and WebPort say how the device reaches Air-Mixer.
	PublicURL string
	WebPort   int
	Logger    *slog.Logger
}

// Client is a Cast output.
type Client struct {
	o      Options
	log    *slog.Logger
	stream *httpstream.Stream

	mu        sync.Mutex
	conn      *conn
	lost      error
	stop      chan struct{}
	done      chan struct{}
	session   string // our receiver app's session, or ""
	transport string
	launching bool
	displaced bool // another app took over the device
	lastSound time.Time
	lastSeen  time.Time
}

// New returns a Cast output.
func New(o Options) *Client {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if _, _, err := net.SplitHostPort(o.Address); err != nil {
		o.Address = net.JoinHostPort(o.Address, DefaultPort)
	}
	return &Client{
		o:      o,
		log:    o.Logger.With("component", "cast-client", "client", o.ID),
		stream: httpstream.New(o.ID, o.Name, o.Streams, o.Logger),
	}
}

func (c *Client) ID() string       { return c.o.ID }
func (c *Client) Name() string     { return c.o.Name }
func (c *Client) Protocol() string { return "cast" }

// Address is the device the output casts to.
func (c *Client) Address() string { return c.o.Address }

// Connected reports whether the device is reachable.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil && c.lost == nil
}

// Playing reports whether Air-Mixer's receiver app runs on the device.
func (c *Client) Playing() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.session != ""
}

// dialer opens the TLS connection; tests replace it.
var dialer = func(ctx context.Context, addr string) (net.Conn, error) {
	d := tls.Dialer{Config: &tls.Config{
		// Cast devices present certificates signed by Google's device
		// CA, not a web PKI one, and are reached by IP address.
		InsecureSkipVerify: true, //nolint:gosec // see above; audio only, nothing secret is sent
		MinVersion:         tls.VersionTLS12,
	}}
	return d.DialContext(ctx, "tcp", addr)
}

// Connect opens the control connection and starts serving the stream.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return nil
	}
	nc, err := dialer(ctx, c.o.Address)
	if err != nil {
		return fmt.Errorf("cast: %w", err)
	}
	cn := &conn{nc: nc}
	if err := cn.send(nsConnection, receiverID, map[string]any{"type": "CONNECT"}); err != nil {
		nc.Close()
		return fmt.Errorf("cast: %w", err)
	}
	if err := c.stream.Connect(ctx); err != nil {
		nc.Close()
		return err
	}
	c.conn, c.lost = cn, nil
	c.session, c.transport, c.launching, c.displaced = "", "", false, false
	c.lastSeen = time.Now()
	c.stop, c.done = make(chan struct{}), make(chan struct{})
	go c.read(cn)
	go c.supervise(cn, c.stop, c.done)
	c.log.Info("cast device connected", "address", c.o.Address)
	return nil
}

// Disconnect stops the receiver app and closes the connection.
func (c *Client) Disconnect() error {
	c.mu.Lock()
	cn, stop, done, session := c.conn, c.stop, c.done, c.session
	c.conn, c.lost, c.session, c.transport, c.launching = nil, nil, "", "", false
	c.mu.Unlock()
	if cn == nil {
		return nil
	}
	close(stop)
	<-done
	if session != "" {
		_ = cn.send(nsReceiver, receiverID, map[string]any{"type": "STOP", "sessionId": session})
	}
	cn.nc.Close()
	return c.stream.Disconnect()
}

// Send queues audio for the stream and starts the receiver app when
// sound begins.
func (c *Client) Send(p protocols.Packet) error {
	c.mu.Lock()
	if c.conn == nil {
		c.mu.Unlock()
		return protocols.ErrNotConnected
	}
	if c.lost != nil {
		err := c.lost
		c.mu.Unlock()
		return fmt.Errorf("cast: %w: %w", protocols.ErrConnectionLost, err)
	}
	if loud(p.Samples) {
		now := time.Now()
		gap := now.Sub(c.lastSound)
		c.lastSound = now
		if c.session == "" && !c.launching && (!c.displaced || gap > resumeGap) {
			c.launching, c.displaced = true, false
			go c.launch(c.conn)
		}
	}
	c.mu.Unlock()
	return c.stream.Send(p)
}

func loud(samples []float32) bool {
	for _, v := range samples {
		if v > silent || v < -silent {
			return true
		}
	}
	return false
}

func (c *Client) launch(cn *conn) {
	c.log.Info("starting the media receiver")
	if err := cn.send(nsReceiver, receiverID, map[string]any{"type": "LAUNCH", "appId": mediaReceiver}); err != nil {
		c.fail(cn, err)
	}
}

// read handles messages from the device until the connection ends.
func (c *Client) read(cn *conn) {
	for {
		m, err := readMessage(cn.nc)
		if err != nil {
			c.fail(cn, err)
			return
		}
		c.mu.Lock()
		if c.conn == cn {
			c.lastSeen = time.Now()
		}
		c.mu.Unlock()
		var payload struct {
			Type   string `json:"type"`
			Status *struct {
				Applications []struct {
					AppID       string `json:"appId"`
					SessionID   string `json:"sessionId"`
					TransportID string `json:"transportId"`
				} `json:"applications"`
			} `json:"status"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal([]byte(m.Payload), &payload); err != nil {
			continue
		}
		switch payload.Type {
		case "PING":
			_ = cn.send(nsHeartbeat, m.Source, map[string]any{"type": "PONG"})
		case "CLOSE":
			c.mu.Lock()
			if c.conn == cn && m.Source == c.transport {
				c.session, c.transport = "", ""
			}
			c.mu.Unlock()
		case "RECEIVER_STATUS":
			if payload.Status != nil {
				c.receiverStatus(cn, payload.Status.Applications)
			}
		case "LAUNCH_ERROR", "LOAD_FAILED", "LOAD_CANCELLED", "INVALID_REQUEST":
			c.log.Warn("cast device refused", "type", payload.Type, "reason", payload.Reason)
			c.mu.Lock()
			if c.conn == cn {
				c.launching = false
			}
			c.mu.Unlock()
		}
	}
}

func (c *Client) receiverStatus(cn *conn, apps []struct {
	AppID       string `json:"appId"`
	SessionID   string `json:"sessionId"`
	TransportID string `json:"transportId"`
},
) {
	var session, transport string
	for _, a := range apps {
		if a.AppID == mediaReceiver {
			session, transport = a.SessionID, a.TransportID
		}
	}
	c.mu.Lock()
	if c.conn != cn {
		c.mu.Unlock()
		return
	}
	switch {
	case c.launching && session != "":
		c.launching = false
		c.session, c.transport = session, transport
		c.mu.Unlock()
		c.load(cn, transport)
		return
	case c.session != "" && session != c.session:
		// Someone cast something else: leave it alone.
		c.log.Info("another app took over the cast device")
		c.session, c.transport, c.displaced = "", "", true
	}
	c.mu.Unlock()
}

// load connects to the receiver app and has it play the stream.
func (c *Client) load(cn *conn, transport string) {
	url, err := httpstream.URL(c.o.PublicURL, c.o.WebPort, c.o.ID, c.o.Address)
	if err != nil {
		c.log.Warn("cannot start casting", "err", err)
		return
	}
	if err := cn.send(nsConnection, transport, map[string]any{"type": "CONNECT"}); err != nil {
		c.fail(cn, err)
		return
	}
	err = cn.send(nsMedia, transport, map[string]any{
		"type": "LOAD",
		"media": map[string]any{
			"contentId":   url,
			"contentType": "audio/wav",
			"streamType":  "LIVE",
			"metadata":    map[string]any{"metadataType": 0, "title": c.o.Name, "subtitle": "Air-Mixer"},
		},
		"autoplay": true,
	})
	if err != nil {
		c.fail(cn, err)
		return
	}
	c.log.Info("casting", "stream", url)
}

// supervise sends heartbeats, notices a device that went silent, and
// stops the receiver app after a long silence.
func (c *Client) supervise(cn *conn, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	t := time.NewTicker(heartbeat)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		if err := cn.send(nsHeartbeat, receiverID, map[string]any{"type": "PING"}); err != nil {
			c.fail(cn, err)
			return
		}
		c.mu.Lock()
		if c.conn != cn {
			c.mu.Unlock()
			return
		}
		if time.Since(c.lastSeen) > deadAfter {
			c.mu.Unlock()
			c.fail(cn, errors.New("the device stopped answering"))
			return
		}
		session := c.session
		quiet := session != "" && time.Since(c.lastSound) > idleStop
		if quiet {
			c.session, c.transport = "", ""
		}
		c.mu.Unlock()
		if quiet {
			c.log.Info("silent for a while, stopping the media receiver")
			_ = cn.send(nsReceiver, receiverID, map[string]any{"type": "STOP", "sessionId": session})
		}
	}
}

func (c *Client) fail(cn *conn, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == cn && c.lost == nil {
		c.lost = err
		c.log.Warn("cast device lost", "err", err)
		cn.nc.Close()
	}
}

// conn is a Cast control connection.
type conn struct {
	nc    net.Conn
	wmu   sync.Mutex
	reqID int
}

// send writes one JSON message, numbering requests.
func (cn *conn) send(namespace, dest string, payload map[string]any) error {
	cn.wmu.Lock()
	defer cn.wmu.Unlock()
	if namespace == nsReceiver || namespace == nsMedia {
		cn.reqID++
		payload["requestId"] = cn.reqID
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_ = cn.nc.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err = cn.nc.Write(message{Source: senderID, Destination: dest, Namespace: namespace, Payload: string(b)}.marshal())
	return err
}
