package upnp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/yeixio/air-mixer/internal/audio/decode"
	"github.com/yeixio/air-mixer/internal/protocols"
	"github.com/yeixio/air-mixer/internal/protocols/httpstream"
)

// Timings; tests shorten them.
var (
	pollInterval = 5 * time.Second
	idleStop     = time.Minute
	resumeGap    = 5 * time.Second
	// failuresLost is how many polls in a row may fail before the device
	// counts as gone.
	failuresLost = 3
)

const silent float32 = 1.0 / 32768

// ClientOptions configures a UPnP/DLNA output.
type ClientOptions struct {
	ID, Name string
	// Address is the renderer's device description URL, as discovery
	// finds it. A plain host or host:port is taken to be a Sonos player
	// (its description is at :1400/xml/device_description.xml).
	Address string
	// Format is "wav", "mp3" or "" to choose: MP3 for Sonos when ffmpeg is
	// installed, WAV otherwise.
	Format    string
	Streams   *httpstream.Registry
	PublicURL string
	WebPort   int
	HTTP      *http.Client
	Logger    *slog.Logger
}

// Client plays on a UPnP/DLNA media renderer by serving a live stream and
// telling the renderer to play it.
type Client struct {
	o      ClientOptions
	log    *slog.Logger
	stream *httpstream.Stream

	mu        sync.Mutex
	dev       *renderer
	lost      error
	stop      chan struct{}
	done      chan struct{}
	uri       string // what we asked the renderer to play, while it plays it
	starting  bool
	displaced bool
	lastSound time.Time
}

// NewClient returns a UPnP/DLNA output.
func NewClient(o ClientOptions) *Client {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 15 * time.Second}
	}
	o.Address = DescriptionURL(o.Address)
	return &Client{
		o: o, log: o.Logger.With("component", "upnp-client", "client", o.ID),
		stream: httpstream.New(o.ID, o.Name, o.Streams, o.Logger),
	}
}

// DescriptionURL turns an output's address into a description URL: a URL
// is used as it is, and a host or host:port is taken to be a Sonos player.
func DescriptionURL(address string) string {
	if strings.HasPrefix(address, "http://") || strings.HasPrefix(address, "https://") {
		return address
	}
	host := address
	if h, _, err := net.SplitHostPort(address); err == nil {
		host = h
	}
	return "http://" + net.JoinHostPort(host, "1400") + "/xml/device_description.xml"
}

func (c *Client) ID() string       { return c.o.ID }
func (c *Client) Name() string     { return c.o.Name }
func (c *Client) Protocol() string { return "upnp" }

// Address is the renderer's description URL.
func (c *Client) Address() string { return c.o.Address }

// Connected reports whether the renderer was reached.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dev != nil && c.lost == nil
}

// Playing reports whether the renderer plays Air-Mixer's stream.
func (c *Client) Playing() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.uri != ""
}

// Connect reads the renderer's description and starts serving the stream.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dev != nil {
		return nil
	}
	dev, err := fetchDescription(ctx, c.o.HTTP, c.o.Address)
	if err != nil {
		if errors.Is(err, errNoAVTransport) {
			return fmt.Errorf("%w: %w", protocols.ErrUnsupportedDevice, err)
		}
		return err
	}
	if err := c.stream.Connect(ctx); err != nil {
		return err
	}
	c.dev, c.lost, c.uri, c.starting, c.displaced = dev, nil, "", false, false
	c.stop, c.done = make(chan struct{}), make(chan struct{})
	go c.supervise(dev, c.stop, c.done)
	c.log.Info("renderer connected", "name", dev.Name, "manufacturer", dev.Manufacturer, "model", dev.Model)
	return nil
}

// Disconnect stops the renderer if it plays our stream.
func (c *Client) Disconnect() error {
	c.mu.Lock()
	dev, stop, done, uri := c.dev, c.stop, c.done, c.uri
	c.dev, c.uri, c.lost = nil, "", nil
	c.mu.Unlock()
	if dev == nil {
		return nil
	}
	close(stop)
	<-done
	if uri != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, _ = call(ctx, c.o.HTTP, dev.AVTransport, avTransport, "Stop", []arg{{"InstanceID", "0"}})
		cancel()
	}
	return c.stream.Disconnect()
}

// Send queues audio for the stream and starts the renderer when sound
// begins.
func (c *Client) Send(p protocols.Packet) error {
	c.mu.Lock()
	if c.dev == nil {
		c.mu.Unlock()
		return protocols.ErrNotConnected
	}
	if c.lost != nil {
		err := c.lost
		c.mu.Unlock()
		return fmt.Errorf("upnp: %w: %w", protocols.ErrConnectionLost, err)
	}
	if loud(p.Samples) {
		now := time.Now()
		gap := now.Sub(c.lastSound)
		c.lastSound = now
		if c.uri == "" && !c.starting && (!c.displaced || gap > resumeGap) {
			c.starting, c.displaced = true, false
			go c.start(c.dev)
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

// mp3 reports whether to send MP3.
func (c *Client) mp3(dev *renderer) bool {
	switch c.o.Format {
	case "mp3":
		return true
	case "wav":
		return false
	}
	return decode.FFmpeg != "" && isSonos(dev)
}

func isSonos(dev *renderer) bool { return strings.Contains(strings.ToLower(dev.Manufacturer), "sonos") }

// streamURI is what the renderer is told to play, and its DIDL-Lite.
func (c *Client) streamURI(dev *renderer) (uri, meta string, err error) {
	base, err := httpstream.URL(c.o.PublicURL, c.o.WebPort, c.o.ID, hostOf(c.o.Address))
	if err != nil {
		return "", "", err
	}
	mime := "audio/wav"
	uri = base + "?raw=1"
	if c.mp3(dev) {
		mime = "audio/mpeg"
		uri = base + "?format=mp3&raw=1"
	}
	httpURI := uri
	if mime == "audio/mpeg" {
		if isSonos(dev) {
			// Sonos plays endless MP3 as a radio station only with its
			// own scheme.
			uri = "x-rincon-mp3radio://" + strings.TrimPrefix(strings.TrimPrefix(uri, "http://"), "https://")
		}
	}
	meta = `<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">` +
		`<item id="airmixer" parentID="0" restricted="1">` +
		`<dc:title>` + escape(c.o.Name) + `</dc:title>` +
		`<upnp:class>object.item.audioItem.audioBroadcast</upnp:class>` +
		`<res protocolInfo="http-get:*:` + mime + `:DLNA.ORG_OP=00;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=01700000000000000000000000000000">` + escape(httpURI) + `</res>` +
		`</item></DIDL-Lite>`
	return uri, meta, nil
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return u.Host
}

func (c *Client) start(dev *renderer) {
	uri, meta, err := c.streamURI(dev)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		_, err = call(ctx, c.o.HTTP, dev.AVTransport, avTransport, "SetAVTransportURI", []arg{
			{"InstanceID", "0"}, {"CurrentURI", uri}, {"CurrentURIMetaData", meta},
		})
		if err == nil {
			_, err = call(ctx, c.o.HTTP, dev.AVTransport, avTransport, "Play", []arg{{"InstanceID", "0"}, {"Speed", "1"}})
		}
		cancel()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dev != dev {
		return
	}
	c.starting = false
	if err != nil {
		c.log.Warn("renderer would not play the stream", "err", err)
		return
	}
	c.uri = uri
	c.log.Info("playing on renderer", "uri", uri)
}

// supervise polls the renderer: it notices another app taking over, plays
// again if the renderer stopped on its own, stops it after a long
// silence, and reports the device lost when it stops answering.
func (c *Client) supervise(dev *renderer, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	failures := 0
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		info, err := call(ctx, c.o.HTTP, dev.AVTransport, avTransport, "GetTransportInfo", []arg{{"InstanceID", "0"}})
		var media map[string]string
		if err == nil {
			media, err = call(ctx, c.o.HTTP, dev.AVTransport, avTransport, "GetMediaInfo", []arg{{"InstanceID", "0"}})
		}
		cancel()
		if err != nil {
			if failures++; failures >= failuresLost {
				c.mu.Lock()
				if c.dev == dev {
					c.lost = err
				}
				c.mu.Unlock()
				c.log.Warn("renderer lost", "err", err)
				return
			}
			continue
		}
		failures = 0
		state, current := info["CurrentTransportState"], media["CurrentURI"]

		c.mu.Lock()
		uri := c.uri
		var action string
		switch {
		case uri == "":
		case current != uri:
			c.log.Info("another app took over the renderer")
			c.uri, c.displaced = "", true
		case time.Since(c.lastSound) > idleStop:
			c.uri = ""
			action = "Stop"
		case state == "STOPPED" || state == "NO_MEDIA_PRESENT":
			action = "Play" // the renderer gave up on the stream; carry on
		}
		c.mu.Unlock()
		if action != "" {
			args := []arg{{"InstanceID", "0"}}
			if action == "Play" {
				args = append(args, arg{"Speed", "1"})
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if _, err := call(ctx, c.o.HTTP, dev.AVTransport, avTransport, action, args); err != nil {
				c.log.Warn("renderer "+strings.ToLower(action), "err", err)
			}
			cancel()
		}
	}
}
