package airplay

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/textproto"
	"strconv"
	"sync"
	"time"

	"github.com/yeixio/air-mixer/internal/audio"
	"github.com/yeixio/air-mixer/internal/audio/alac"
	"github.com/yeixio/air-mixer/internal/protocols"
)

// ErrAirPlay2Required is returned when a device refuses classic AirPlay and
// expects an AirPlay 2 session with HomeKit pairing, which is not supported
// yet.
var ErrAirPlay2Required = fmt.Errorf("airplay: device requires AirPlay 2 pairing, which is not supported yet: %w", protocols.ErrUnsupportedDevice)

const (
	defaultPort    = 5000
	framesPerPkt   = 352 // samples per channel in each RTP packet
	defaultLatency = 2 * time.Second
	// handshakeTimeout bounds Connect when ctx has no deadline.
	handshakeTimeout = 10 * time.Second

	// prebuffer is how much audio is queued before playback starts or
	// resumes after an underrun, absorbing jitter in the source.
	prebuffer = 100 * time.Millisecond
	// maxQueue bounds latency when the source runs faster than real time.
	maxQueue = 500 * time.Millisecond
	// maxCatchUp is how many packets are sent at once after a stall.
	maxCatchUp = 32
	// historySize is how many sent packets are kept for retransmission.
	historySize = 1024
)

// ClientOptions configures an AirPlay sender.
type ClientOptions struct {
	ID      string
	Name    string
	Address string // host or host:port of the receiver's RTSP service
	// Latency is the playback delay requested from the receiver. Defaults
	// to 2 seconds, like iTunes.
	Latency time.Duration
	// DisableEncryption sends audio in the clear. Receivers accept both.
	DisableEncryption bool
	Logger            *slog.Logger
}

// Client streams audio to an AirPlay (RAOP) receiver such as an AirPort
// Express, a shairport-sync speaker or most AirPlay-capable AV receivers.
type Client struct {
	opts ClientOptions
	log  *slog.Logger

	mu        sync.Mutex
	connected bool
	conn      net.Conn
	rtsp      *bufio.Reader
	cseq      int
	uri       string
	session   string
	headers   textproto.MIMEHeader
	input     chan []float32
	stop      chan struct{}
	wg        sync.WaitGroup
	udp       []*net.UDPConn

	stream *stream
}

// NewClient returns an AirPlay sender. Call Connect before sending audio.
func NewClient(opts ClientOptions) *Client {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Latency <= 0 {
		opts.Latency = defaultLatency
	}
	return &Client{opts: opts, log: opts.Logger.With("component", "airplay-client", "client", opts.ID)}
}

func (c *Client) ID() string       { return c.opts.ID }
func (c *Client) Name() string     { return c.opts.Name }
func (c *Client) Protocol() string { return "airplay" }

// Connected reports whether a session is established.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// Connect performs the RTSP handshake and starts streaming. Until audio
// arrives the receiver is sent silence.
func (c *Client) Connect(ctx context.Context) (err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connected {
		return nil
	}

	addr := c.opts.Address
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, strconv.Itoa(defaultPort))
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("airplay: connect to %s: %w", addr, err)
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(handshakeTimeout)
	}
	conn.SetDeadline(deadline)
	c.conn, c.rtsp, c.cseq = conn, bufio.NewReader(conn), 0
	defer func() {
		if err != nil {
			c.teardownLocked()
		}
	}()

	local := conn.LocalAddr().(*net.TCPAddr).IP
	remote := conn.RemoteAddr().(*net.TCPAddr).IP
	sessionID := randUint32()
	c.uri = fmt.Sprintf("rtsp://%s/%d", local, sessionID)
	c.headers = textproto.MIMEHeader{}
	c.headers.Set("User-Agent", "AirMixer/1.0")
	c.headers.Set("Client-Instance", hex.EncodeToString(randBytes(8)))
	c.headers.Set("DACP-ID", hex.EncodeToString(randBytes(8)))
	c.headers.Set("Active-Remote", strconv.FormatUint(uint64(randUint32()), 10))

	if err := c.options(); err != nil {
		return err
	}

	enc, err := alac.NewEncoder(alac.DefaultConfig())
	if err != nil {
		return err
	}
	st := &stream{
		log:     c.log,
		enc:     enc,
		ssrc:    randUint32(),
		seq:     uint16(randUint32()),
		rtpTime: randUint32(),
		latency: uint32(c.opts.Latency.Seconds() * audio.SampleRate),
	}
	var rsaKey, iv string
	if !c.opts.DisableEncryption {
		key, ivb := randBytes(16), randBytes(16)
		if st.cipher, err = newAESCipher(key, ivb); err != nil {
			return err
		}
		if rsaKey, err = wrapAESKey(key); err != nil {
			return err
		}
		iv = encodeBase64(ivb)
	}
	sdp := announceSDP(sessionID, local.String(), remote.String(), enc.Config().Fmtp(), rsaKey, iv)
	h := textproto.MIMEHeader{}
	h.Set("Content-Type", "application/sdp")
	if err := c.expectOK(c.roundTrip("ANNOUNCE", h, sdp)); err != nil {
		return err
	}

	// Local control and timing ports, then SETUP to learn the receiver's.
	for range 2 {
		u, err := net.ListenUDP("udp", &net.UDPAddr{IP: local})
		if err != nil {
			return fmt.Errorf("airplay: open UDP port: %w", err)
		}
		c.udp = append(c.udp, u)
	}
	st.control, st.timing = c.udp[0], c.udp[1]
	h = textproto.MIMEHeader{}
	h.Set("Transport", fmt.Sprintf("RTP/AVP/UDP;unicast;interleaved=0-1;mode=record;control_port=%d;timing_port=%d",
		udpPort(st.control), udpPort(st.timing)))
	resp, err := c.roundTrip("SETUP", h, nil)
	if err := c.expectOK(resp, err); err != nil {
		return err
	}
	c.session = resp.Header.Get("Session")
	tp := transportParams(resp.Header.Get("Transport"))
	serverPort, err := strconv.Atoi(tp["server_port"])
	if err != nil {
		return fmt.Errorf("airplay: SETUP response has no server_port: %q", resp.Header.Get("Transport"))
	}
	if cp, err := strconv.Atoi(tp["control_port"]); err == nil {
		st.remoteControl = &net.UDPAddr{IP: remote, Port: cp}
	}
	st.audio, err = net.DialUDP("udp", &net.UDPAddr{IP: local}, &net.UDPAddr{IP: remote, Port: serverPort})
	if err != nil {
		return fmt.Errorf("airplay: open audio socket: %w", err)
	}
	c.udp = append(c.udp, st.audio)

	h = textproto.MIMEHeader{}
	h.Set("Range", "npt=0-")
	h.Set("RTP-Info", fmt.Sprintf("seq=%d;rtptime=%d", st.seq, st.rtpTime))
	if err := c.expectOK(c.roundTrip("RECORD", h, nil)); err != nil {
		return err
	}

	conn.SetDeadline(time.Time{})
	c.input = make(chan []float32, 64)
	c.stop = make(chan struct{})
	c.stream = st
	c.wg.Add(3)
	go func() { defer c.wg.Done(); st.run(c.input, c.stop) }()
	go func() { defer c.wg.Done(); st.serveControl() }()
	go func() { defer c.wg.Done(); st.serveTiming() }()
	c.connected = true
	c.log.Info("connected to AirPlay receiver", "addr", addr, "encrypted", st.cipher != nil)
	return nil
}

// options sends OPTIONS with an Apple-Challenge. Receivers that require
// AirPlay 2 pairing refuse it. The Apple-Response signature is not checked:
// it covers the receiver's MAC address, which a sender cannot know, and it
// only proves the receiver holds the widely published AirPort key anyway.
func (c *Client) options() error {
	challenge := randBytes(16)
	h := textproto.MIMEHeader{}
	h.Set("Apple-Challenge", encodeBase64(challenge))
	resp, err := c.roundTrip("OPTIONS", h, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode == 403 || resp.StatusCode == 401 || resp.StatusCode == 470 {
		return ErrAirPlay2Required
	}
	return c.expectOK(resp, nil)
}

func (c *Client) roundTrip(method string, h textproto.MIMEHeader, body []byte) (*response, error) {
	c.cseq++
	req := &request{Method: method, URI: c.uri, Header: textproto.MIMEHeader{}, Body: body}
	for k, v := range c.headers {
		req.Header[k] = v
	}
	for k, v := range h {
		req.Header[k] = v
	}
	req.Header.Set("CSeq", strconv.Itoa(c.cseq))
	if c.session != "" {
		req.Header.Set("Session", c.session)
	}
	if err := req.write(c.conn); err != nil {
		return nil, fmt.Errorf("airplay: %s: %w", method, err)
	}
	resp, err := readResponse(c.rtsp)
	if err != nil {
		return nil, fmt.Errorf("airplay: %s: %w", method, err)
	}
	if resp.StatusCode != 200 {
		c.log.Debug("RTSP error", "method", method, "status", resp.StatusCode)
	}
	return resp, nil
}

func (c *Client) expectOK(resp *response, err error) error {
	switch {
	case err != nil:
		return err
	case resp.StatusCode == 403 || resp.StatusCode == 401:
		return ErrAirPlay2Required
	case resp.StatusCode != 200:
		return fmt.Errorf("airplay: receiver answered %d %s", resp.StatusCode, resp.Reason)
	}
	return nil
}

// Disconnect stops streaming and ends the session.
func (c *Client) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.connected {
		return nil
	}
	close(c.stop)
	c.conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.roundTrip("TEARDOWN", nil, nil); err != nil {
		c.log.Debug("TEARDOWN failed", "err", err)
	}
	c.teardownLocked()
	c.wg.Wait()
	c.connected = false
	c.log.Info("disconnected from AirPlay receiver")
	return nil
}

func (c *Client) teardownLocked() {
	if c.conn != nil {
		c.conn.Close()
	}
	for _, u := range c.udp {
		u.Close()
	}
	c.udp, c.session = nil, ""
}

// Send queues audio in the internal format.
func (c *Client) Send(p protocols.Packet) error {
	if p.Format != audio.Internal {
		return fmt.Errorf("airplay: expected %+v audio, got %+v", audio.Internal, p.Format)
	}
	c.mu.Lock()
	connected, input := c.connected, c.input
	c.mu.Unlock()
	if !connected {
		return protocols.ErrNotConnected
	}
	select {
	case input <- p.Samples:
		return nil
	default:
		return protocols.ErrBufferFull
	}
}

// stream paces audio to the receiver in real time.
type stream struct {
	log     *slog.Logger
	enc     *alac.Encoder
	cipher  *aesCipher
	ssrc    uint32
	seq     uint16
	rtpTime uint32
	latency uint32

	audio, control, timing *net.UDPConn
	remoteControl          *net.UDPAddr

	historyMu sync.Mutex
	history   [historySize][]byte
}

func (s *stream) run(input <-chan []float32, stop <-chan struct{}) {
	const (
		frameLen   = framesPerPkt * audio.Channels
		pktPerSync = audio.SampleRate / framesPerPkt // about one per second
	)
	samplesFor := func(d time.Duration) int {
		return int(d.Seconds()*audio.SampleRate) * audio.Channels
	}
	var (
		queue     []float32
		buffering = true
		start     = time.Now()
		sent      int
		ticker    = time.NewTicker(5 * time.Millisecond)
		silence   = make([]float32, frameLen)
	)
	defer ticker.Stop()

	enqueue := func(buf []float32) {
		queue = append(queue, buf...)
		if over := len(queue) - samplesFor(maxQueue); over > 0 {
			over += (frameLen - over%frameLen) % frameLen
			queue = queue[over:]
		}
	}
	for {
		select {
		case <-stop:
			return
		case buf := <-input:
			enqueue(buf)
			continue
		case <-ticker.C:
		}
		// Take everything already waiting, so a tick never finds the queue
		// short while audio sits in the channel.
	drain:
		for {
			select {
			case buf := <-input:
				enqueue(buf)
			default:
				break drain
			}
		}

		due := int(time.Since(start).Seconds() * audio.SampleRate / framesPerPkt)
		if behind := due - sent; behind > maxCatchUp {
			// Stalled (for example the machine slept). Skip the gap rather
			// than bursting audio; the next sync packet realigns playback.
			sent = due - maxCatchUp
		}
		for ; sent < due; sent++ {
			if buffering && len(queue) >= samplesFor(prebuffer) {
				buffering = false
			}
			frame := silence
			if !buffering && len(queue) >= frameLen {
				frame, queue = queue[:frameLen], queue[frameLen:]
			} else {
				buffering = true
			}
			if sent%pktPerSync == 0 {
				s.sendSync(sent == 0)
			}
			if err := s.sendFrame(frame, sent == 0); err != nil {
				s.log.Debug("send audio", "err", err)
			}
		}
	}
}

func (s *stream) sendFrame(samples []float32, first bool) error {
	pcm := make([]int16, len(samples))
	for i, v := range samples {
		pcm[i] = audio.FloatToInt16(v)
	}
	payload, err := s.enc.Encode(pcm)
	if err != nil {
		return err
	}
	if s.cipher != nil {
		s.cipher.encrypt(payload, payload)
	}
	pkt := rtpHeader{Marker: first, PayloadType: ptAudio, Seq: s.seq, Timestamp: s.rtpTime, SSRC: s.ssrc}.append(make([]byte, 0, rtpHeaderLen+len(payload)))
	pkt = append(pkt, payload...)

	s.historyMu.Lock()
	s.history[s.seq%historySize] = pkt
	s.historyMu.Unlock()

	s.seq++
	s.rtpTime += framesPerPkt
	_, err = s.audio.Write(pkt)
	return err
}

// sendSync tells the receiver which RTP timestamp is playing now, so it can
// schedule playback latency samples later.
func (s *stream) sendSync(first bool) {
	if s.remoteControl == nil {
		return
	}
	b := make([]byte, 20)
	b[0] = 0x80
	if first {
		b[0] = 0x90
	}
	b[1] = 0x80 | ptSync
	binary.BigEndian.PutUint16(b[2:4], 7)
	binary.BigEndian.PutUint32(b[4:8], s.rtpTime-s.latency)
	binary.BigEndian.PutUint64(b[8:16], ntpTime(time.Now()))
	binary.BigEndian.PutUint32(b[16:20], s.rtpTime)
	if _, err := s.control.WriteToUDP(b, s.remoteControl); err != nil {
		s.log.Debug("send sync", "err", err)
	}
}

// serveControl answers retransmission requests from the receiver.
func (s *stream) serveControl() {
	buf := make([]byte, 64)
	for {
		n, from, err := s.control.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if n < 8 || buf[1]&0x7f != ptRetransmitReq {
			continue
		}
		first := binary.BigEndian.Uint16(buf[4:6])
		count := binary.BigEndian.Uint16(buf[6:8])
		for i := range min(count, historySize) {
			seq := first + i
			s.historyMu.Lock()
			pkt := s.history[seq%historySize]
			s.historyMu.Unlock()
			if len(pkt) < rtpHeaderLen || binary.BigEndian.Uint16(pkt[2:4]) != seq {
				continue
			}
			out := append([]byte{0x80, 0x80 | ptRetransmitAnswer, byte(seq >> 8), byte(seq)}, pkt...)
			s.control.WriteToUDP(out, from)
		}
	}
}

// serveTiming answers NTP-style timing requests from the receiver.
func (s *stream) serveTiming() {
	buf := make([]byte, 64)
	for {
		n, from, err := s.timing.ReadFromUDP(buf)
		if err != nil {
			return
		}
		received := ntpTime(time.Now())
		if n < 32 || buf[1]&0x7f != ptTimingRequest {
			continue
		}
		resp := make([]byte, 32)
		resp[0], resp[1] = 0x80, 0x80|ptTimingResponse
		binary.BigEndian.PutUint16(resp[2:4], 7)
		copy(resp[8:16], buf[24:32]) // the request's send time
		binary.BigEndian.PutUint64(resp[16:24], received)
		binary.BigEndian.PutUint64(resp[24:32], ntpTime(time.Now()))
		s.timing.WriteToUDP(resp, from)
	}
}

func udpPort(c *net.UDPConn) int { return c.LocalAddr().(*net.UDPAddr).Port }

func randBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}

func randUint32() uint32 { return binary.BigEndian.Uint32(randBytes(4)) }
