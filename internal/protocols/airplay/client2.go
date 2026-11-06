package airplay

// The AirPlay 2 sender. After transient pairing, the RTSP connection is
// encrypted and the session is set up with binary-plist SETUP requests:
// one for the session (NTP timing, an event channel) and one for a
// realtime audio stream, ALAC at 44.1 kHz as in classic AirPlay but with
// each packet sealed with ChaCha20-Poly1305. Timing and sync packets are
// the classic ones. This is untested against Apple hardware.

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/chacha20poly1305"

	"github.com/yeixio/air-mixer/internal/audio"
	"github.com/yeixio/air-mixer/internal/audio/alac"
)

const (
	// userAgent2 is what AirPlay 2 receivers are sent; recent HomePod
	// software checks the AirPlay version in it.
	userAgent2 = "AirPlay/999.0.0"
	// feedbackEvery is how often the session is kept alive.
	feedbackEvery = 2 * time.Second
	// streamTypeRealtime is a UDP stream of RTP audio.
	streamTypeRealtime = 96
	// formatALAC44100 is the audioFormat bit for ALAC, 44.1 kHz, 16-bit
	// stereo.
	formatALAC44100 = 1 << 18
	// compressionALAC is the "ct" value for ALAC.
	compressionALAC = 2
)

// ErrPairingRefused means the receiver wants a PIN or a HomeKit owner,
// rather than accepting transient pairing.
var ErrPairingRefused = errors.New("airplay2: the receiver refused transient pairing; allow \"Anyone on the same network\" in its AirPlay settings")

func (c *Client) connectAirPlay2(local, remote net.IP, sessionID uint32, addr string) error {
	c.headers.Set("User-Agent", userAgent2)
	c.headers.Set("X-Apple-Client-Name", c.opts.Name)

	// GET /info is informational; some receivers like being asked first.
	if resp, err := c.request("GET", "/info", nil, nil); err != nil {
		return err
	} else if resp.StatusCode != 200 {
		c.log.Debug("GET /info failed", "status", resp.StatusCode)
	}

	secret, err := pairTransient(func(body []byte) ([]byte, error) {
		h := textproto.MIMEHeader{}
		h.Set("Content-Type", "application/octet-stream")
		h.Set("X-Apple-HKP", "4")
		resp, err := c.request("POST", "/pair-setup", h, body)
		if err != nil {
			return nil, err
		}
		switch resp.StatusCode {
		case 200:
			return resp.Body, nil
		case 401, 403, 470:
			return nil, ErrPairingRefused
		}
		return nil, fmt.Errorf("airplay2: pair-setup answered %d %s", resp.StatusCode, resp.Reason)
	}, rand.Reader)
	if err != nil {
		return err
	}
	write, read, err := channelKeys(secret, "Control-Salt", "Control-Write-Encryption-Key", "Control-Read-Encryption-Key")
	if err != nil {
		return err
	}
	sc, err := newSecureConn(c.conn, write, read)
	if err != nil {
		return err
	}
	c.conn, c.rtsp = sc, bufio.NewReader(sc)

	// Local timing and control ports.
	for range 2 {
		u, err := net.ListenUDP("udp", &net.UDPAddr{IP: local})
		if err != nil {
			return fmt.Errorf("airplay2: open UDP port: %w", err)
		}
		c.udp = append(c.udp, u)
	}
	enc, err := alac.NewEncoder(alac.DefaultConfig())
	if err != nil {
		return err
	}
	audioKey := secret[:chacha20poly1305.KeySize]
	chacha, err := chacha20poly1305.New(audioKey)
	if err != nil {
		return err
	}
	st := &stream{
		log:     c.log,
		enc:     enc,
		chacha:  chacha,
		ssrc:    randUint32(),
		seq:     uint16(randUint32()),
		rtpTime: randUint32(),
		latency: uint32(c.opts.Latency.Seconds() * audio.SampleRate),
		timing:  c.udp[0],
		control: c.udp[1],
	}

	deviceID := macLike(randBytes(6))
	resp, err := c.plistRequest("SETUP", map[string]any{
		"deviceID":                 deviceID,
		"macAddress":               deviceID,
		"sessionUUID":              uuidString(randBytes(16)),
		"groupUUID":                uuidString(randBytes(16)),
		"groupContainsGroupLeader": false,
		"isMultiSelectAirPlay":     true,
		"name":                     c.opts.Name,
		"model":                    "Air-Mixer",
		"sourceVersion":            "366.0",
		"timingPort":               udpPort(st.timing),
		"timingProtocol":           "NTP",
	})
	if err != nil {
		return err
	}
	if port, ok := plistInt(resp, "eventPort"); ok && port > 0 {
		if err := c.openEvents(remote, int(port), secret); err != nil {
			// Playback works without it; the receiver may just log it.
			c.log.Warn("could not open the AirPlay 2 event channel", "err", err)
		}
	}

	if err := c.expectOK(c.roundTrip("RECORD", nil, nil)); err != nil {
		return err
	}

	latencyMax := max(int64(st.latency), 88200)
	resp, err = c.plistRequest("SETUP", map[string]any{
		"streams": []any{map[string]any{
			"type":                    streamTypeRealtime,
			"ct":                      compressionALAC,
			"audioFormat":             formatALAC44100,
			"audioMode":               "default",
			"spf":                     framesPerPkt,
			"sr":                      audio.SampleRate,
			"controlPort":             udpPort(st.control),
			"latencyMin":              11025,
			"latencyMax":              latencyMax,
			"shk":                     audioKey,
			"isMedia":                 true,
			"supportsDynamicStreamID": false,
			"streamConnectionID":      int64(sessionID),
		}},
	})
	if err != nil {
		return err
	}
	streams, _ := resp["streams"].([]any)
	if len(streams) == 0 {
		return errors.New("airplay2: SETUP answered without a stream")
	}
	s0, _ := streams[0].(map[string]any)
	dataPort, ok1 := plistInt(s0, "dataPort")
	controlPort, ok2 := plistInt(s0, "controlPort")
	if !ok1 || !ok2 || dataPort <= 0 || controlPort <= 0 {
		return fmt.Errorf("airplay2: SETUP answered without data and control ports: %v", s0)
	}
	st.remoteControl = &net.UDPAddr{IP: remote, Port: int(controlPort)}
	st.audio, err = net.DialUDP("udp", &net.UDPAddr{IP: local}, &net.UDPAddr{IP: remote, Port: int(dataPort)})
	if err != nil {
		return fmt.Errorf("airplay2: open audio socket: %w", err)
	}
	c.udp = append(c.udp, st.audio)

	c.conn.SetDeadline(time.Time{})
	c.input = make(chan []float32, 64)
	c.stop = make(chan struct{})
	c.stream = st
	c.wg.Add(4)
	go func() { defer c.wg.Done(); st.run(c.input, c.stop) }()
	go func() { defer c.wg.Done(); st.serveControl() }()
	go func() { defer c.wg.Done(); st.serveTiming() }()
	go func() { defer c.wg.Done(); c.feedback(c.stop) }()
	c.connected = true
	c.log.Info("connected to AirPlay 2 receiver", "addr", addr)
	return nil
}

// plistRequest sends a binary-plist request and decodes the answer.
func (c *Client) plistRequest(method string, body map[string]any) (map[string]any, error) {
	b, err := encodePlist(body)
	if err != nil {
		return nil, err
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Type", "application/x-apple-binary-plist")
	resp, err := c.roundTrip(method, h, b)
	if err := c.expectOK(resp, err); err != nil {
		return nil, err
	}
	if len(resp.Body) == 0 {
		return map[string]any{}, nil
	}
	v, err := decodePlist(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("airplay2: %s answer: %w", method, err)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("airplay2: %s answer is not a dictionary", method)
	}
	return m, nil
}

// openEvents connects to the receiver's event channel and answers what it
// sends there. The channel is encrypted with the events keys, read and
// write swapped since the receiver initiates requests on it.
func (c *Client) openEvents(remote net.IP, port int, secret []byte) error {
	write, read, err := channelKeys(secret, "Events-Salt", "Events-Read-Encryption-Key", "Events-Write-Encryption-Key")
	if err != nil {
		return err
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(remote.String(), strconv.Itoa(port)), 5*time.Second)
	if err != nil {
		return err
	}
	sc, err := newSecureConn(conn, write, read)
	if err != nil {
		conn.Close()
		return err
	}
	c.events = sc
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		r := bufio.NewReader(sc)
		for {
			req, err := readRequest(r)
			if err != nil {
				return
			}
			c.log.Debug("AirPlay 2 event", "method", req.Method, "uri", req.URI)
			resp := newResponse(200)
			if cseq := req.Header.Get("CSeq"); cseq != "" {
				resp.Header.Set("CSeq", cseq)
			}
			if err := resp.write(sc); err != nil {
				return
			}
		}
	}()
	return nil
}

// feedback keeps the session alive. Two failures in a row mark the
// connection lost, so the Manager reconnects.
func (c *Client) feedback(stop <-chan struct{}) {
	t := time.NewTicker(feedbackEvery)
	defer t.Stop()
	failures := 0
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		c.conn.SetDeadline(time.Now().Add(5 * time.Second))
		resp, err := c.request("POST", "/feedback", nil, nil)
		c.conn.SetDeadline(time.Time{})
		if err == nil && resp.StatusCode == 200 {
			failures = 0
			continue
		}
		select {
		case <-stop:
			return
		default:
		}
		failures++
		c.log.Debug("AirPlay 2 feedback failed", "err", err, "failures", failures)
		if failures >= 2 {
			c.lost.Store(true)
			return
		}
	}
}

// sealAudio appends payload, encrypted, to the RTP header in pkt: the
// ciphertext, its 16-byte tag and the last 8 bytes of the nonce. The
// timestamp and SSRC are the additional data.
func sealAudio(a aead, pkt, payload []byte, counter uint64) []byte {
	nonce := counterNonce(counter)
	aad := append([]byte(nil), pkt[4:rtpHeaderLen]...)
	pkt = a.Seal(pkt, nonce, payload, aad)
	return append(pkt, nonce[4:]...)
}

// openAudio reverses sealAudio, for tests and receivers.
func openAudio(a aead, pkt []byte) ([]byte, error) {
	if len(pkt) < rtpHeaderLen+16+8 {
		return nil, errors.New("short AirPlay 2 audio packet")
	}
	nonce := make([]byte, 12)
	copy(nonce[4:], pkt[len(pkt)-8:])
	return a.Open(nil, nonce, pkt[rtpHeaderLen:len(pkt)-8], pkt[4:rtpHeaderLen])
}

func macLike(b []byte) string {
	parts := make([]string, len(b))
	for i, v := range b {
		parts[i] = fmt.Sprintf("%02X", v)
	}
	return strings.Join(parts, ":")
}

func uuidString(b []byte) string {
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := strings.ToUpper(hex.EncodeToString(b))
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
