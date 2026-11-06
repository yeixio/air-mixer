package upnp

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // only to derive a stable UUID from the input's id
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yeixio/air-mixer/internal/protocols"
	"github.com/yeixio/air-mixer/internal/protocols/radio"
)

// Transport states.
const (
	stateStopped = "STOPPED"
	statePlaying = "PLAYING"
	statePaused  = "PAUSED_PLAYBACK"
	stateNoMedia = "NO_MEDIA_PRESENT"
)

// sinkProtocols is what the renderer says it plays.
var sinkProtocols = strings.Join([]string{
	"http-get:*:audio/mpeg:*", "http-get:*:audio/mp3:*", "http-get:*:audio/wav:*", "http-get:*:audio/x-wav:*",
	"http-get:*:audio/L16:*", "http-get:*:audio/flac:*", "http-get:*:audio/x-flac:*", "http-get:*:audio/aac:*",
	"http-get:*:audio/mp4:*", "http-get:*:audio/x-m4a:*", "http-get:*:audio/ogg:*", "http-get:*:audio/opus:*",
	"http-get:*:application/vnd.apple.mpegurl:*", "http-get:*:audio/x-mpegurl:*",
}, ",")

// ServerOptions configures a renderer input.
type ServerOptions struct {
	ID, Name string
	// Address is the HTTP listen address for control, such as ":49494".
	// Empty picks a free port, which SSDP advertises.
	Address string
	Logger  *slog.Logger
	// NoSSDP turns off advertising, for tests.
	NoSSDP bool
}

// Server is a UPnP/DLNA media renderer: apps such as BubbleUPnP, Hi-Fi
// Cast, foobar2000 or Windows' "Cast to Device" see it as a speaker and
// tell it what to play, and it plays that into Air-Mixer.
type Server struct {
	o     ServerOptions
	log   *slog.Logger
	uuid  string
	audio chan protocols.Packet

	ln   net.Listener
	http *http.Server
	adv  *advertiser

	mu       sync.Mutex
	state    string
	uri      string
	meta     string
	nextURI  string
	nextMeta string
	volume   int
	mute     bool
	player   *radio.Server
	gen      int // counts players, so a late Ended is ignored
	fwdStop  chan struct{}
	subs     map[string]*subscriber
	stopped  bool
}

type subscriber struct {
	path     string
	callback string
	seq      int
	expires  time.Time
}

// NewServer returns a renderer input. Start begins listening.
func NewServer(o ServerOptions) *Server {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Server{
		o: o, log: o.Logger.With("component", "upnp-renderer", "server", o.ID),
		uuid: stableUUID("air-mixer-renderer:" + o.ID), audio: make(chan protocols.Packet, 16),
		state: stateNoMedia, volume: 100, subs: map[string]*subscriber{},
	}
}

// stableUUID makes a version-5-shaped UUID from a name, so the renderer
// keeps its identity across restarts.
func stableUUID(name string) string {
	h := sha1.Sum([]byte(name)) //nolint:gosec // not for security
	h[6] = h[6]&0x0f | 0x50
	h[8] = h[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

func (s *Server) ID() string                     { return s.o.ID }
func (s *Server) Name() string                   { return s.o.Name }
func (s *Server) Protocol() string               { return "upnp" }
func (s *Server) Audio() <-chan protocols.Packet { return s.audio }

// URL is the renderer's device description, as reached from this host.
func (s *Server) URL() string {
	return "http://" + s.ln.Addr().String() + "/description.xml"
}

// Start listens for control points and advertises the renderer.
func (s *Server) Start(context.Context) error {
	addr := s.o.Address
	if addr == "" {
		addr = ":0"
	}
	ln, err := net.Listen("tcp4", addr)
	if err != nil {
		return fmt.Errorf("upnp: listen: %w", err)
	}
	s.ln = ln
	mux := http.NewServeMux()
	mux.HandleFunc("GET /description.xml", s.description)
	for _, svc := range services {
		svc := svc
		mux.HandleFunc("GET /"+svc.Path+"/scpd.xml", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
			io.WriteString(w, svc.scpd()) //nolint:errcheck // the client went away
		})
		mux.HandleFunc("POST /"+svc.Path+"/control", func(w http.ResponseWriter, r *http.Request) { s.control(w, r, svc) })
		mux.HandleFunc("/"+svc.Path+"/event", func(w http.ResponseWriter, r *http.Request) { s.event(w, r, svc) })
	}
	s.http = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go s.http.Serve(ln) //nolint:errcheck // ends with Stop
	if !s.o.NoSSDP {
		port := ln.Addr().(*net.TCPAddr).Port
		types := []string{mediaRenderer, avTransport, renderingControl, connectionManager}
		adv, err := newAdvertiser(s.uuid, types, func(peer net.IP) string {
			ip := peer.String()
			if local, err := localIPFor(peer); err == nil {
				ip = local
			}
			return "http://" + net.JoinHostPort(ip, strconv.Itoa(port)) + "/description.xml"
		})
		if err != nil {
			s.http.Close()
			return err
		}
		s.adv = adv
	}
	s.log.Info("media renderer listening", "address", ln.Addr().String(), "uuid", s.uuid)
	return nil
}

func localIPFor(peer net.IP) (string, error) {
	if peer.IsMulticast() || peer.IsUnspecified() {
		peer = net.IPv4(192, 0, 2, 1) // any outside address picks the default route
	}
	c, err := net.Dial("udp4", net.JoinHostPort(peer.String(), "9"))
	if err != nil {
		return "", err
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String(), nil
}

// Stop stops playing and listening.
func (s *Server) Stop() error {
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()
	s.stopPlayer()
	if s.adv != nil {
		s.adv.Close()
	}
	if s.http != nil {
		return s.http.Close()
	}
	return nil
}

func (s *Server) description(w http.ResponseWriter, _ *http.Request) {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?><root xmlns="urn:schemas-upnp-org:device-1-0" xmlns:dlna="urn:schemas-dlna-org:device-1-0"><specVersion><major>1</major><minor>0</minor></specVersion><device>`)
	b.WriteString(`<deviceType>` + mediaRenderer + `</deviceType>`)
	b.WriteString(`<friendlyName>` + escape(s.o.Name) + `</friendlyName>`)
	b.WriteString(`<manufacturer>Air-Mixer</manufacturer><manufacturerURL>https://github.com/yeixio/air-mixer</manufacturerURL>`)
	b.WriteString(`<modelName>Air-Mixer</modelName><modelDescription>Air-Mixer speaker</modelDescription><modelNumber>1</modelNumber>`)
	b.WriteString(`<dlna:X_DLNADOC>DMR-1.50</dlna:X_DLNADOC>`)
	b.WriteString(`<UDN>uuid:` + s.uuid + `</UDN><serviceList>`)
	for _, svc := range services {
		fmt.Fprintf(&b, `<service><serviceType>%s</serviceType><serviceId>%s</serviceId><SCPDURL>/%s/scpd.xml</SCPDURL><controlURL>/%s/control</controlURL><eventSubURL>/%s/event</eventSubURL></service>`,
			svc.Type, svc.ID, svc.Path, svc.Path, svc.Path)
	}
	b.WriteString(`</serviceList></device></root>`)
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	io.WriteString(w, b.String()) //nolint:errcheck // the client went away
}

// soapFault answers a request with a UPnP error.
func soapFault(w http.ResponseWriter, code int, desc string) {
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	w.WriteHeader(http.StatusInternalServerError)
	w.Write(envelope(`<s:Fault><faultcode>s:Client</faultcode><faultstring>UPnPError</faultstring><detail>` + //nolint:errcheck // the client went away
		`<UPnPError xmlns="urn:schemas-upnp-org:control-1-0"><errorCode>` + strconv.Itoa(code) + `</errorCode><errorDescription>` + escape(desc) + `</errorDescription></UPnPError></detail></s:Fault>`))
}

func (s *Server) control(w http.ResponseWriter, r *http.Request, svc serviceDesc) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxSOAP))
	if err != nil {
		soapFault(w, 501, "Action Failed")
		return
	}
	act, in, _, err := parseBody(body)
	if err != nil {
		soapFault(w, 401, "Invalid Action")
		return
	}
	out, code := s.handle(svc.Type, act, in)
	if code != 0 {
		soapFault(w, code, errorText[code])
		return
	}
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	w.Header().Set("EXT", "")
	w.Write(envelope(actionXML(svc.Type, act, out, true))) //nolint:errcheck // the client went away
}

var errorText = map[int]string{
	401: "Invalid Action", 402: "Invalid Args", 501: "Action Failed",
	701: "Transition not available", 714: "Illegal MIME-type", 716: "Resource not found",
}

// handle carries out one action; code is a UPnP error, or 0.
func (s *Server) handle(service, act string, in map[string]string) (out []arg, code int) {
	switch service + "#" + act {
	case avTransport + "#SetAVTransportURI":
		uri := strings.TrimSpace(in["CurrentURI"])
		if !strings.HasPrefix(uri, "http://") && !strings.HasPrefix(uri, "https://") {
			return nil, 714
		}
		s.mu.Lock()
		wasPlaying := s.state == statePlaying
		s.uri, s.meta, s.nextURI, s.nextMeta = uri, in["CurrentURIMetaData"], "", ""
		s.mu.Unlock()
		s.stopPlayer()
		if wasPlaying {
			s.play()
		} else {
			s.setState(stateStopped)
		}
		s.log.Info("told to play", "uri", redactURI(uri))
	case avTransport + "#SetNextAVTransportURI":
		s.mu.Lock()
		s.nextURI, s.nextMeta = strings.TrimSpace(in["NextURI"]), in["NextURIMetaData"]
		s.mu.Unlock()
	case avTransport + "#Play":
		s.mu.Lock()
		uri, playing := s.uri, s.state == statePlaying
		s.mu.Unlock()
		if uri == "" {
			return nil, 701
		}
		if !playing {
			s.play()
		}
	case avTransport + "#Pause":
		s.stopPlayer()
		s.setState(statePaused)
	case avTransport + "#Stop":
		s.stopPlayer()
		s.mu.Lock()
		hasMedia := s.uri != ""
		s.mu.Unlock()
		if hasMedia {
			s.setState(stateStopped)
		}
	case avTransport + "#Seek", avTransport + "#Next", avTransport + "#Previous":
		// Streams play from the start; accept and carry on.
	case avTransport + "#GetTransportInfo":
		s.mu.Lock()
		st := s.state
		s.mu.Unlock()
		out = []arg{{"CurrentTransportState", st}, {"CurrentTransportStatus", "OK"}, {"CurrentSpeed", "1"}}
	case avTransport + "#GetMediaInfo":
		s.mu.Lock()
		tracks := "0"
		if s.uri != "" {
			tracks = "1"
		}
		out = []arg{{"NrTracks", tracks}, {"MediaDuration", trackDuration(s.meta)}, {"CurrentURI", s.uri}, {"CurrentURIMetaData", s.meta},
			{"NextURI", s.nextURI}, {"NextURIMetaData", s.nextMeta}, {"PlayMedium", "NETWORK"}, {"RecordMedium", "NOT_IMPLEMENTED"}, {"WriteStatus", "NOT_IMPLEMENTED"}}
		s.mu.Unlock()
	case avTransport + "#GetPositionInfo":
		s.mu.Lock()
		var played time.Duration
		if s.player != nil {
			played = s.player.Played()
		}
		track := "0"
		if s.uri != "" {
			track = "1"
		}
		rel := hms(played)
		out = []arg{{"Track", track}, {"TrackDuration", trackDuration(s.meta)}, {"TrackMetaData", s.meta}, {"TrackURI", s.uri},
			{"RelTime", rel}, {"AbsTime", rel}, {"RelCount", "2147483647"}, {"AbsCount", "2147483647"}}
		s.mu.Unlock()
	case avTransport + "#GetDeviceCapabilities":
		out = []arg{{"PlayMedia", "NETWORK"}, {"RecMedia", "NOT_IMPLEMENTED"}, {"RecQualityModes", "NOT_IMPLEMENTED"}}
	case avTransport + "#GetTransportSettings":
		out = []arg{{"PlayMode", "NORMAL"}, {"RecQualityMode", "NOT_IMPLEMENTED"}}
	case avTransport + "#GetCurrentTransportActions":
		out = []arg{{"Actions", s.actions()}}
	case renderingControl + "#GetVolume":
		s.mu.Lock()
		out = []arg{{"CurrentVolume", strconv.Itoa(s.volume)}}
		s.mu.Unlock()
	case renderingControl + "#SetVolume":
		v, err := strconv.Atoi(strings.TrimSpace(in["DesiredVolume"]))
		if err != nil || v < 0 || v > 100 {
			return nil, 402
		}
		s.mu.Lock()
		s.volume = v
		s.mu.Unlock()
		s.notifyAll()
	case renderingControl + "#GetMute":
		s.mu.Lock()
		out = []arg{{"CurrentMute", boolArg(s.mute)}}
		s.mu.Unlock()
	case renderingControl + "#SetMute":
		m := strings.TrimSpace(in["DesiredMute"])
		s.mu.Lock()
		s.mute = m == "1" || strings.EqualFold(m, "true") || strings.EqualFold(m, "yes")
		s.mu.Unlock()
		s.notifyAll()
	case connectionManager + "#GetProtocolInfo":
		out = []arg{{"Source", ""}, {"Sink", sinkProtocols}}
	case connectionManager + "#GetCurrentConnectionIDs":
		out = []arg{{"ConnectionIDs", "0"}}
	case connectionManager + "#GetCurrentConnectionInfo":
		out = []arg{{"RcsID", "0"}, {"AVTransportID", "0"}, {"ProtocolInfo", ""}, {"PeerConnectionManager", ""},
			{"PeerConnectionID", "-1"}, {"Direction", "Input"}, {"Status", "OK"}}
	default:
		return nil, 401
	}
	return out, 0
}

func boolArg(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func (s *Server) actions() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.state {
	case statePlaying:
		return "Pause,Stop"
	case stateStopped, statePaused:
		return "Play"
	}
	return ""
}

var durationAttr = regexp.MustCompile(`duration="([0-9:.]+)"`)

// trackDuration is the duration a track's DIDL-Lite gives, or 0.
func trackDuration(meta string) string {
	if m := durationAttr.FindStringSubmatch(strings.ReplaceAll(meta, "&quot;", `"`)); m != nil {
		d, _, _ := strings.Cut(m[1], ".")
		return d
	}
	return "0:00:00"
}

func hms(d time.Duration) string {
	sec := int(d / time.Second)
	return fmt.Sprintf("%d:%02d:%02d", sec/3600, sec/60%60, sec%60)
}

func redactURI(u string) string {
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		return u[:i] + "?…"
	}
	return u
}

func (s *Server) setState(st string) {
	s.mu.Lock()
	changed := s.state != st
	s.state = st
	s.mu.Unlock()
	if changed {
		s.notifyAll()
	}
}

// play starts playing the current URI.
func (s *Server) play() {
	s.mu.Lock()
	if s.stopped || s.uri == "" {
		s.mu.Unlock()
		return
	}
	s.gen++
	gen := s.gen
	uri := s.uri
	p := radio.New(radio.Options{
		ID: s.o.ID, Name: s.o.Name, URL: uri, Once: true, Logger: s.o.Logger,
		Ended: func(err error) { go s.ended(gen, err) },
	})
	s.player = p
	stop := make(chan struct{})
	s.fwdStop = stop
	s.mu.Unlock()
	if err := p.Start(context.Background()); err != nil {
		s.log.Warn("cannot play", "err", err)
		s.setState(stateStopped)
		return
	}
	go s.forward(p, stop)
	s.setState(statePlaying)
}

// forward passes the player's audio on at the renderer's volume.
func (s *Server) forward(p *radio.Server, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case pkt := <-p.Audio():
			s.mu.Lock()
			g := float32(s.volume) / 100
			if s.mute {
				g = 0
			}
			s.mu.Unlock()
			if g != 1 {
				for i := range pkt.Samples {
					pkt.Samples[i] *= g
				}
			}
			select {
			case s.audio <- pkt:
			case <-stop:
				return
			}
		}
	}
}

// ended moves to the next track, or stops, when a track finishes.
func (s *Server) ended(gen int, err error) {
	s.mu.Lock()
	if gen != s.gen {
		s.mu.Unlock()
		return
	}
	next, nextMeta := s.nextURI, s.nextMeta
	s.mu.Unlock()
	if err != nil {
		s.log.Warn("track failed", "err", err)
	}
	// Let the last packets drain before stopping the forwarder.
	time.Sleep(300 * time.Millisecond)
	s.stopPlayer()
	if next != "" {
		s.mu.Lock()
		s.uri, s.meta, s.nextURI, s.nextMeta = next, nextMeta, "", ""
		s.mu.Unlock()
		s.play()
		return
	}
	s.setState(stateStopped)
}

func (s *Server) stopPlayer() {
	s.mu.Lock()
	p, stop := s.player, s.fwdStop
	s.player, s.fwdStop = nil, nil
	s.gen++
	s.mu.Unlock()
	if p == nil {
		return
	}
	close(stop)
	p.Stop() //nolint:errcheck // Stop of a radio input doesn't fail
}

// ---- eventing (GENA) ----

func (s *Server) event(w http.ResponseWriter, r *http.Request, svc serviceDesc) {
	switch r.Method {
	case "SUBSCRIBE":
		timeout := 1800
		if t := strings.TrimPrefix(r.Header.Get("Timeout"), "Second-"); t != "" {
			if n, err := strconv.Atoi(t); err == nil && n > 0 {
				timeout = min(n, 7200)
			}
		}
		sid := r.Header.Get("Sid")
		s.mu.Lock()
		if sid != "" {
			sub, ok := s.subs[sid]
			if !ok {
				s.mu.Unlock()
				http.Error(w, "unknown subscription", http.StatusPreconditionFailed)
				return
			}
			sub.expires = time.Now().Add(time.Duration(timeout) * time.Second)
			s.mu.Unlock()
		} else {
			cb := strings.Trim(strings.SplitN(r.Header.Get("Callback"), ">", 2)[0], "< ")
			if !strings.HasPrefix(cb, "http://") {
				s.mu.Unlock()
				http.Error(w, "missing callback", http.StatusPreconditionFailed)
				return
			}
			sid = "uuid:" + stableUUID(fmt.Sprintf("%s-%d-%s", cb, time.Now().UnixNano(), svc.Path))
			sub := &subscriber{path: svc.Path, callback: cb, expires: time.Now().Add(time.Duration(timeout) * time.Second)}
			s.subs[sid] = sub
			s.mu.Unlock()
			defer func() { go s.notify(sid, sub, s.lastChange(svc)) }()
		}
		w.Header().Set("SID", sid)
		w.Header().Set("TIMEOUT", "Second-"+strconv.Itoa(timeout))
		w.WriteHeader(http.StatusOK)
	case "UNSUBSCRIBE":
		s.mu.Lock()
		delete(s.subs, r.Header.Get("Sid"))
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// lastChange is the event body a service sends.
func (s *Server) lastChange(svc serviceDesc) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var props string
	switch svc.Type {
	case avTransport:
		ev := `<Event xmlns="urn:schemas-upnp-org:metadata-1-0/AVT/"><InstanceID val="0">` +
			`<TransportState val="` + s.state + `"/><TransportStatus val="OK"/>` +
			`<AVTransportURI val="` + escape(s.uri) + `"/><CurrentTrackURI val="` + escape(s.uri) + `"/>` +
			`<CurrentTrackMetaData val="` + escape(s.meta) + `"/><NumberOfTracks val="1"/><CurrentPlayMode val="NORMAL"/>` +
			`</InstanceID></Event>`
		props = `<e:property><LastChange>` + escape(ev) + `</LastChange></e:property>`
	case renderingControl:
		ev := `<Event xmlns="urn:schemas-upnp-org:metadata-1-0/RCS/"><InstanceID val="0">` +
			`<Volume channel="Master" val="` + strconv.Itoa(s.volume) + `"/><Mute channel="Master" val="` + boolArg(s.mute) + `"/>` +
			`</InstanceID></Event>`
		props = `<e:property><LastChange>` + escape(ev) + `</LastChange></e:property>`
	default:
		props = `<e:property><SourceProtocolInfo></SourceProtocolInfo></e:property><e:property><SinkProtocolInfo>` + escape(sinkProtocols) +
			`</SinkProtocolInfo></e:property><e:property><CurrentConnectionIDs>0</CurrentConnectionIDs></e:property>`
	}
	return `<?xml version="1.0" encoding="utf-8"?><e:propertyset xmlns:e="urn:schemas-upnp-org:event-1-0">` + props + `</e:propertyset>`
}

// notifyAll sends every subscriber its service's current state.
func (s *Server) notifyAll() {
	s.mu.Lock()
	type target struct {
		sid string
		sub *subscriber
	}
	var targets []target
	now := time.Now()
	for sid, sub := range s.subs {
		if now.After(sub.expires) {
			delete(s.subs, sid)
			continue
		}
		targets = append(targets, target{sid, sub})
	}
	s.mu.Unlock()
	for _, t := range targets {
		for _, svc := range services {
			if svc.Path == t.sub.path {
				go s.notify(t.sid, t.sub, s.lastChange(svc))
			}
		}
	}
}

var notifyClient = &http.Client{Timeout: 5 * time.Second}

func (s *Server) notify(sid string, sub *subscriber, body string) {
	s.mu.Lock()
	seq := sub.seq
	sub.seq++
	s.mu.Unlock()
	req, err := http.NewRequest("NOTIFY", sub.callback, bytes.NewReader([]byte(body)))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("NT", "upnp:event")
	req.Header.Set("NTS", "upnp:propchange")
	req.Header.Set("SID", sid)
	req.Header.Set("SEQ", strconv.Itoa(seq))
	res, err := notifyClient.Do(req)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) || strings.Contains(err.Error(), "refused") {
			s.mu.Lock()
			delete(s.subs, sid) // the control point went away
			s.mu.Unlock()
		}
		return
	}
	res.Body.Close()
}
