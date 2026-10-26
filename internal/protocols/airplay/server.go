package airplay

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/libp2p/zeroconf/v2"

	"github.com/yeixio/air-mixer/internal/audio"
	"github.com/yeixio/air-mixer/internal/audio/alac"
	"github.com/yeixio/air-mixer/internal/protocols"
)

// ServerOptions configures an AirPlay receiver.
type ServerOptions struct {
	ID   string
	Name string // shown to senders
	// Address is the RTSP listen address, for example ":49152".
	Address string
	// Interface is the network interface to advertise on, or "auto".
	Interface string
	// DisableAdvertising turns off mDNS; senders must be pointed at the
	// address directly. Used in tests.
	DisableAdvertising bool
	Logger             *slog.Logger
}

// Server is an AirPlay (RAOP) receiver. Senders such as iPhones and Macs see
// it as an AirPlay speaker; the audio they play is decoded and delivered on
// Audio().
//
// Only one sender streams at a time: a new session takes over from the
// previous one, as on a real AirPlay speaker.
type Server struct {
	opts  ServerOptions
	log   *slog.Logger
	audio chan protocols.Packet

	mu       sync.Mutex
	listener net.Listener
	mdns     *zeroconf.Server
	active   *session
	sessions map[*session]struct{}
	stopped  bool
	wg       sync.WaitGroup
}

// NewServer returns an AirPlay receiver. Call Start to begin listening.
func NewServer(opts ServerOptions) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Server{
		opts:     opts,
		log:      opts.Logger.With("component", "airplay-server", "server", opts.ID),
		audio:    make(chan protocols.Packet, 256),
		sessions: make(map[*session]struct{}),
	}
}

func (s *Server) ID() string                     { return s.opts.ID }
func (s *Server) Name() string                   { return s.opts.Name }
func (s *Server) Protocol() string               { return "airplay" }
func (s *Server) Audio() <-chan protocols.Packet { return s.audio }

// Addr returns the RTSP listen address once started.
func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

// Start listens for RTSP connections and advertises the receiver.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil || s.stopped {
		return errors.New("airplay: server already started")
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", s.opts.Address)
	if err != nil {
		return fmt.Errorf("airplay: listen: %w", err)
	}
	if !s.opts.DisableAdvertising {
		s.mdns, err = s.advertise(ln.Addr().(*net.TCPAddr).Port)
		if err != nil {
			ln.Close()
			return err
		}
	}
	s.listener = ln
	s.wg.Add(1)
	go s.accept(ln)
	s.log.Info("AirPlay receiver listening", "name", s.opts.Name, "addr", ln.Addr())
	return nil
}

// txtRecord is what shairport-sync advertises for a classic AirPlay (RAOP)
// receiver: ALAC or PCM, optional RSA-wrapped AES, 44.1 kHz 16-bit stereo.
var txtRecord = []string{
	"txtvers=1", "ch=2", "cn=0,1", "et=0,1", "sv=false", "da=true",
	"sr=44100", "ss=16", "pw=false", "vn=65537", "tp=UDP", "vs=130.14",
	"am=AirMixer", "sm=false", "ek=1",
}

func (s *Server) advertise(port int) (*zeroconf.Server, error) {
	iface, err := resolveInterface(s.opts.Interface)
	if err != nil {
		return nil, err
	}
	// RAOP instance names are "<MAC>@<name>".
	mac := strings.ToUpper(strings.ReplaceAll(iface.HardwareAddr.String(), ":", ""))
	if mac == "" {
		mac = "000000000000"
	}
	srv, err := zeroconf.Register(mac+"@"+s.opts.Name, "_raop._tcp", "local.", port, txtRecord, []net.Interface{*iface})
	if err != nil {
		return nil, fmt.Errorf("airplay: mDNS advertisement on %s: %w", iface.Name, err)
	}
	s.log.Info("advertising AirPlay receiver", "interface", iface.Name)
	return srv, nil
}

// Stop closes the listener and every session and withdraws the mDNS
// advertisement.
func (s *Server) Stop() error {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return nil
	}
	s.stopped = true
	if s.mdns != nil {
		s.mdns.Shutdown()
	}
	if s.listener != nil {
		s.listener.Close()
	}
	for ss := range s.sessions {
		ss.close()
	}
	s.mu.Unlock()
	s.wg.Wait()
	s.log.Info("AirPlay receiver stopped")
	return nil
}

func (s *Server) accept(ln net.Listener) {
	defer s.wg.Done()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		ss := &session{srv: s, conn: conn, log: s.log.With("peer", conn.RemoteAddr().String())}
		ss.gain.Store(math.Float32bits(1))
		s.mu.Lock()
		if s.stopped {
			s.mu.Unlock()
			conn.Close()
			return
		}
		s.sessions[ss] = struct{}{}
		s.wg.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.wg.Done()
			ss.serve()
			s.mu.Lock()
			delete(s.sessions, ss)
			if s.active == ss {
				s.active = nil
			}
			s.mu.Unlock()
		}()
	}
}

// activate makes ss the session whose audio is delivered, ending any other.
func (s *Server) activate(ss *session) {
	s.mu.Lock()
	prev := s.active
	s.active = ss
	s.mu.Unlock()
	if prev != nil && prev != ss {
		prev.log.Info("session preempted by a new sender")
		prev.close()
	}
}

func (s *Server) deliver(ss *session, samples []float32, format audio.Format) {
	s.mu.Lock()
	active := s.active == ss
	s.mu.Unlock()
	if !active {
		return
	}
	select {
	case s.audio <- protocols.Packet{Samples: samples, Format: format}:
	default:
		ss.log.Debug("audio buffer full, dropping packet")
	}
}

// session is one RTSP connection from a sender.
type session struct {
	srv  *Server
	conn net.Conn
	log  *slog.Logger

	gain atomic.Uint32 // float32 bits, from SET_PARAMETER volume

	mu        sync.Mutex
	format    audio.Format
	decode    func([]byte) ([]float32, error)
	cipher    *aesCipher
	udp       []*net.UDPConn
	lastSeq   uint16
	haveSeq   bool
	closeOnce sync.Once
}

func (ss *session) close() {
	ss.closeOnce.Do(func() {
		ss.conn.Close()
		ss.closeUDP()
	})
}

func (ss *session) closeUDP() {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	for _, c := range ss.udp {
		c.Close()
	}
	ss.udp = nil
}

func (ss *session) serve() {
	defer ss.close()
	r := bufio.NewReader(ss.conn)
	for {
		req, err := readRequest(r)
		if err != nil {
			return
		}
		resp := ss.handle(req)
		resp.Header.Set("CSeq", req.Header.Get("CSeq"))
		resp.Header.Set("Server", "AirMixer/1.0")
		if ch := req.Header.Get("Apple-Challenge"); ch != "" {
			local := ss.conn.LocalAddr().(*net.TCPAddr).IP
			if ar, err := appleResponse(ch, local, hardwareAddrFor(local)); err == nil {
				resp.Header.Set("Apple-Response", ar)
			} else {
				ss.log.Warn("cannot answer Apple-Challenge", "err", err)
			}
		}
		if err := resp.write(ss.conn); err != nil {
			return
		}
		if req.Method == "TEARDOWN" {
			return
		}
	}
}

func (ss *session) handle(req *request) *response {
	ss.log.Debug("RTSP request", "method", req.Method, "uri", req.URI)
	switch req.Method {
	case "OPTIONS":
		resp := newResponse(200)
		resp.Header.Set("Public", "ANNOUNCE, SETUP, RECORD, PAUSE, FLUSH, TEARDOWN, OPTIONS, GET_PARAMETER, SET_PARAMETER")
		return resp
	case "ANNOUNCE":
		if err := ss.announce(req.Body); err != nil {
			ss.log.Warn("rejecting stream", "err", err)
			return newResponse(415)
		}
		return newResponse(200)
	case "SETUP":
		return ss.setup(req)
	case "RECORD":
		ss.srv.activate(ss)
		resp := newResponse(200)
		resp.Header.Set("Audio-Latency", "11025")
		return resp
	case "FLUSH":
		ss.mu.Lock()
		ss.haveSeq = false
		ss.mu.Unlock()
		return newResponse(200)
	case "SET_PARAMETER":
		ss.setParameter(req)
		return newResponse(200)
	case "GET_PARAMETER", "PAUSE", "TEARDOWN":
		return newResponse(200)
	}
	return newResponse(501)
}

// announce configures the codec and encryption from the sender's SDP.
func (ss *session) announce(body []byte) error {
	attrs := sdpAttributes(body)
	codec := strings.Fields(attrs["rtpmap"])
	if len(codec) < 2 {
		return errors.New("SDP has no rtpmap")
	}

	var (
		decode func([]byte) ([]float32, error)
		format audio.Format
	)
	switch {
	case codec[1] == "AppleLossless":
		cfg, err := alac.ParseFmtp(attrs["fmtp"])
		if err != nil {
			return err
		}
		dec, err := alac.NewDecoder(cfg)
		if err != nil {
			return err
		}
		decode = dec.DecodeFloat
		format = audio.Format{SampleRate: int(cfg.SampleRate), Channels: int(cfg.NumChannels)}
	case strings.HasPrefix(codec[1], "L16/"):
		// L16/<rate>/<channels>, big-endian signed 16-bit PCM.
		parts := strings.Split(codec[1], "/")
		rate, channels := 44100, 2
		if len(parts) > 1 {
			rate, _ = strconv.Atoi(parts[1])
		}
		if len(parts) > 2 {
			channels, _ = strconv.Atoi(parts[2])
		}
		format = audio.Format{SampleRate: rate, Channels: channels}
		decode = decodeL16
	default:
		return fmt.Errorf("unsupported codec %q", codec[1])
	}
	if !format.Valid() {
		return fmt.Errorf("invalid audio format %+v", format)
	}

	var c *aesCipher
	if k := attrs["rsaaeskey"]; k != "" {
		key, err := unwrapAESKey(k)
		if err != nil {
			return err
		}
		iv, err := decodeBase64(attrs["aesiv"])
		if err != nil {
			return fmt.Errorf("decode aesiv: %w", err)
		}
		if c, err = newAESCipher(key, iv); err != nil {
			return err
		}
	}

	ss.mu.Lock()
	ss.decode, ss.format, ss.cipher = decode, format, c
	ss.mu.Unlock()
	ss.log.Info("sender announced stream", "codec", codec[1], "encrypted", c != nil)
	return nil
}

func decodeL16(b []byte) ([]float32, error) {
	if len(b)%2 != 0 {
		return nil, errors.New("odd-length L16 payload")
	}
	out := make([]float32, len(b)/2)
	for i := range out {
		out[i] = audio.Int16ToFloat(int16(binary.BigEndian.Uint16(b[2*i:])))
	}
	return out, nil
}

// setup opens the UDP ports for audio, control and timing.
func (ss *session) setup(req *request) *response {
	ss.mu.Lock()
	ready := ss.decode != nil
	ss.mu.Unlock()
	if !ready {
		return newResponse(455) // SETUP before ANNOUNCE
	}
	ss.closeUDP()
	host := ss.conn.LocalAddr().(*net.TCPAddr).IP
	var conns []*net.UDPConn
	for range 3 {
		c, err := net.ListenUDP("udp", &net.UDPAddr{IP: host})
		if err != nil {
			for _, c := range conns {
				c.Close()
			}
			ss.log.Error("open UDP port", "err", err)
			return newResponse(500)
		}
		conns = append(conns, c)
	}
	ss.mu.Lock()
	ss.udp = conns
	ss.mu.Unlock()

	audioConn, controlConn, timingConn := conns[0], conns[1], conns[2]
	go ss.readAudio(audioConn, false)
	go ss.readAudio(controlConn, true)
	go drain(timingConn)

	port := func(c *net.UDPConn) int { return c.LocalAddr().(*net.UDPAddr).Port }
	resp := newResponse(200)
	resp.Header.Set("Transport", fmt.Sprintf("RTP/AVP/UDP;unicast;mode=record;server_port=%d;control_port=%d;timing_port=%d",
		port(audioConn), port(controlConn), port(timingConn)))
	resp.Header.Set("Session", "1")
	resp.Header.Set("Audio-Jack-Status", "connected; type=analog")
	return resp
}

// setParameter handles volume changes. Other parameters (track metadata,
// artwork, progress) are accepted and ignored.
func (ss *session) setParameter(req *request) {
	if !strings.HasPrefix(req.Header.Get("Content-Type"), "text/parameters") {
		return
	}
	for _, line := range strings.Split(string(req.Body), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || strings.TrimSpace(k) != "volume" {
			continue
		}
		db, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			continue
		}
		ss.gain.Store(math.Float32bits(volumeGain(db)))
		ss.log.Debug("volume changed", "db", db)
	}
}

// volumeGain converts AirPlay volume (-30 dB to 0 dB, -144 for mute) to a
// linear gain.
func volumeGain(db float64) float32 {
	if db < -30 {
		return 0
	}
	return float32(math.Pow(10, min(db, 0)/20))
}

func drain(c *net.UDPConn) {
	buf := make([]byte, 128)
	for {
		if _, _, err := c.ReadFromUDP(buf); err != nil {
			return
		}
	}
}

// readAudio reads audio packets from the audio port, or retransmitted ones
// from the control port.
func (ss *session) readAudio(c *net.UDPConn, control bool) {
	buf := make([]byte, 8192)
	for {
		n, _, err := c.ReadFromUDP(buf)
		if err != nil {
			return
		}
		pkt := buf[:n]
		if control {
			// A retransmission wraps the original packet in a 4-byte header.
			if n < 4 || pkt[1]&0x7f != ptRetransmitAnswer {
				continue
			}
			pkt = pkt[4:]
		}
		ss.handleAudio(pkt)
	}
}

func (ss *session) handleAudio(pkt []byte) {
	h, payload, err := parseRTP(pkt)
	if err != nil || h.PayloadType != ptAudio || len(payload) == 0 {
		return
	}

	ss.mu.Lock()
	if ss.haveSeq && int16(h.Seq-ss.lastSeq) <= 0 {
		ss.mu.Unlock()
		return // duplicate or late
	}
	ss.lastSeq, ss.haveSeq = h.Seq, true
	decode, format, c := ss.decode, ss.format, ss.cipher
	data := make([]byte, len(payload))
	if c != nil {
		c.decrypt(data, payload)
	} else {
		copy(data, payload)
	}
	samples, err := decode(data)
	ss.mu.Unlock()
	if err != nil {
		ss.log.Debug("dropping undecodable packet", "seq", h.Seq, "err", err)
		return
	}

	if g := math.Float32frombits(ss.gain.Load()); g != 1 {
		for i := range samples {
			samples[i] *= g
		}
	}
	ss.srv.deliver(ss, samples, format)
}

// resolveInterface picks the interface to advertise on. "auto" (or empty)
// chooses the first interface that is up, not loopback, has an IPv4
// address and does not look virtual.
func resolveInterface(name string) (*net.Interface, error) {
	if name != "" && name != "auto" {
		iface, err := net.InterfaceByName(name)
		if err != nil {
			return nil, fmt.Errorf("airplay: interface %q: %w", name, err)
		}
		return iface, nil
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var fallback *net.Interface
	for i := range ifaces {
		iface := &ifaces[i]
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || !hasIPv4(iface) {
			continue
		}
		if isVirtual(iface.Name) {
			if fallback == nil {
				fallback = iface
			}
			continue
		}
		return iface, nil
	}
	if fallback != nil {
		return fallback, nil
	}
	return nil, errors.New("airplay: no network interface with an IPv4 address")
}

func hasIPv4(iface *net.Interface) bool {
	addrs, err := iface.Addrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
			return true
		}
	}
	return false
}

func isVirtual(name string) bool {
	for _, p := range []string{"veth", "docker", "br-", "virbr", "vmnet", "vboxnet", "utun", "tun", "tap", "zt", "tailscale", "wg"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}
