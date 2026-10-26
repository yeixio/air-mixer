package airplay

import (
	"bufio"
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"errors"
	"io"
	"log/slog"
	"math"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yeixio/air-mixer/internal/audio"
	"github.com/yeixio/air-mixer/internal/protocols"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestRTSPRequestRoundTrip(t *testing.T) {
	h := textproto.MIMEHeader{}
	h.Set("CSeq", "3")
	h.Set("DACP-ID", "ABC")
	h.Set("RTP-Info", "seq=1")
	h.Set("Content-Type", "application/sdp")
	req := &request{Method: "ANNOUNCE", URI: "rtsp://1.2.3.4/1", Header: h, Body: []byte("v=0\r\n")}

	var buf bytes.Buffer
	if err := req.write(&buf); err != nil {
		t.Fatal(err)
	}
	wire := buf.String()
	if !strings.HasPrefix(wire, "ANNOUNCE rtsp://1.2.3.4/1 RTSP/1.0\r\nCSeq: 3\r\n") {
		t.Errorf("request line or CSeq not first:\n%s", wire)
	}
	for _, want := range []string{"DACP-ID: ABC\r\n", "RTP-Info: seq=1\r\n", "Content-Length: 5\r\n"} {
		if !strings.Contains(wire, want) {
			t.Errorf("missing %q in:\n%s", want, wire)
		}
	}

	got, err := readRequest(bufio.NewReader(&buf))
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != "ANNOUNCE" || got.URI != req.URI || string(got.Body) != "v=0\r\n" || got.Header.Get("Dacp-Id") != "ABC" {
		t.Errorf("round trip mismatch: %+v", got)
	}
}

func TestRTSPResponseRoundTrip(t *testing.T) {
	resp := newResponse(455)
	resp.Header.Set("CSeq", "9")
	var buf bytes.Buffer
	if err := resp.write(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(buf.String(), "RTSP/1.0 455 Method Not Valid in This State\r\nCSeq: 9\r\n") {
		t.Errorf("unexpected wire form:\n%s", buf.String())
	}
	got, err := readResponse(bufio.NewReader(&buf))
	if err != nil {
		t.Fatal(err)
	}
	if got.StatusCode != 455 || got.Header.Get("CSeq") != "9" || len(got.Body) != 0 {
		t.Errorf("round trip mismatch: %+v", got)
	}
}

func TestRTSPMalformed(t *testing.T) {
	for _, in := range []string{
		"GARBAGE\r\n\r\n",
		"OPTIONS * HTTP/1.1\r\n\r\n",
		"OPTIONS * RTSP/1.0\r\nContent-Length: nope\r\n\r\n",
		"OPTIONS * RTSP/1.0\r\nContent-Length: 999999999\r\n\r\n",
		"OPTIONS * RTSP/1.0\r\nContent-Length: 10\r\n\r\nshort",
	} {
		if _, err := readRequest(bufio.NewReader(strings.NewReader(in))); err == nil {
			t.Errorf("readRequest(%q) succeeded", in)
		}
	}
	for _, in := range []string{"HTTP/1.1 200 OK\r\n\r\n", "RTSP/1.0 abc OK\r\n\r\n"} {
		if _, err := readResponse(bufio.NewReader(strings.NewReader(in))); err == nil {
			t.Errorf("readResponse(%q) succeeded", in)
		}
	}
	if statusText(299) != "Unknown" {
		t.Error("unexpected text for unknown status")
	}
}

func TestSDP(t *testing.T) {
	body := announceSDP(42, "10.0.0.1", "10.0.0.2", "96 352 0 16 40 10 14 2 255 0 0 44100", "KEY", "IV")
	a := sdpAttributes(body)
	if a["rtpmap"] != "96 AppleLossless" || a["fmtp"] != "96 352 0 16 40 10 14 2 255 0 0 44100" ||
		a["rsaaeskey"] != "KEY" || a["aesiv"] != "IV" {
		t.Errorf("attributes: %v", a)
	}
	if !bytes.Contains(body, []byte("o=iTunes 42 0 IN IP4 10.0.0.1\r\n")) {
		t.Errorf("origin line missing:\n%s", body)
	}
	if _, ok := sdpAttributes(announceSDP(1, "a", "b", "f", "", ""))["rsaaeskey"]; ok {
		t.Error("unencrypted SDP has rsaaeskey")
	}

	tp := transportParams("RTP/AVP/UDP;unicast;server_port=6000;control_port=6001")
	if tp["server_port"] != "6000" || tp["control_port"] != "6001" {
		t.Errorf("transport params: %v", tp)
	}
	if _, ok := tp["unicast"]; !ok {
		t.Error("bare flag missing")
	}
}

func TestRTPHeader(t *testing.T) {
	h := rtpHeader{Marker: true, PayloadType: ptAudio, Seq: 0xfffe, Timestamp: 123456, SSRC: 0xdeadbeef}
	pkt := append(h.append(nil), 1, 2, 3)
	if pkt[0] != 0x80 || pkt[1] != 0xe0 {
		t.Errorf("header bytes % x", pkt[:2])
	}
	got, payload, err := parseRTP(pkt)
	if err != nil || got != h || !bytes.Equal(payload, []byte{1, 2, 3}) {
		t.Errorf("parseRTP = %+v, % x, %v", got, payload, err)
	}
	if _, _, err := parseRTP(pkt[:11]); err == nil {
		t.Error("short packet accepted")
	}
}

func TestAESCipher(t *testing.T) {
	key, iv := bytes.Repeat([]byte{1}, 16), bytes.Repeat([]byte{2}, 16)
	c, err := newAESCipher(key, iv)
	if err != nil {
		t.Fatal(err)
	}
	plain := make([]byte, 37) // two blocks and a 5-byte tail
	for i := range plain {
		plain[i] = byte(i)
	}
	enc := make([]byte, len(plain))
	c.encrypt(enc, plain)
	if bytes.Equal(enc[:32], plain[:32]) {
		t.Error("blocks not encrypted")
	}
	if !bytes.Equal(enc[32:], plain[32:]) {
		t.Error("partial block must be sent in the clear")
	}
	// Each packet is independent: encrypting twice gives the same result.
	enc2 := make([]byte, len(plain))
	c.encrypt(enc2, plain)
	if !bytes.Equal(enc, enc2) {
		t.Error("cipher carries state between packets")
	}
	dec := make([]byte, len(enc))
	c.decrypt(dec, enc)
	if !bytes.Equal(dec, plain) {
		t.Error("decrypt did not invert encrypt")
	}

	if _, err := newAESCipher(key, iv[:8]); err == nil {
		t.Error("short IV accepted")
	}
	if _, err := newAESCipher(key[:5], iv); err == nil {
		t.Error("bad key accepted")
	}
}

func TestAESKeyWrap(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 16)
	wrapped, err := wrapAESKey(key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := unwrapAESKey(wrapped + "==") // padding is tolerated
	if err != nil || !bytes.Equal(got, key) {
		t.Fatalf("unwrap = %x, %v", got, err)
	}
	if _, err := unwrapAESKey("!!!"); err == nil {
		t.Error("invalid base64 accepted")
	}
	if _, err := unwrapAESKey(encodeBase64([]byte("not rsa"))); err == nil {
		t.Error("garbage ciphertext accepted")
	}
	short, _ := wrapAESKey(key[:8])
	if _, err := unwrapAESKey(short); err == nil {
		t.Error("8-byte key accepted")
	}
}

func TestAppleResponse(t *testing.T) {
	challenge := bytes.Repeat([]byte{9}, 16)
	ip := net.ParseIP("192.168.1.10")
	mac := net.HardwareAddr{1, 2, 3, 4, 5, 6}
	resp, err := appleResponse(encodeBase64(challenge), ip, mac)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := decodeBase64(resp)
	if err != nil {
		t.Fatal(err)
	}
	msg := challengeMessage(challenge, ip, mac)
	if len(msg) != 32 {
		t.Errorf("message is %d bytes, want 32", len(msg))
	}
	if err := rsa.VerifyPKCS1v15(&airportKey.PublicKey, crypto.Hash(0), msg, sig); err != nil {
		t.Errorf("signature does not verify: %v", err)
	}
	if _, err := appleResponse("!!", ip, mac); err == nil {
		t.Error("invalid challenge accepted")
	}
	if got := hardwareAddrFor(net.ParseIP("127.0.0.1")); len(got) != 6 {
		t.Errorf("hardwareAddrFor(loopback) = %v", got)
	}
}

func TestVolumeGain(t *testing.T) {
	for _, tc := range []struct {
		db   float64
		want float32
	}{{0, 1}, {5, 1}, {-20, 0.1}, {-30, float32(math.Pow(10, -1.5))}, {-30.1, 0}, {-144, 0}} {
		if got := volumeGain(tc.db); math.Abs(float64(got-tc.want)) > 1e-6 {
			t.Errorf("volumeGain(%v) = %v, want %v", tc.db, got, tc.want)
		}
	}
}

func TestDecodeL16(t *testing.T) {
	got, err := decodeL16([]byte{0x40, 0x00, 0x80, 0x00})
	if err != nil || len(got) != 2 || got[0] != 0.5 || got[1] != -1 {
		t.Errorf("decodeL16 = %v, %v", got, err)
	}
	if _, err := decodeL16([]byte{1}); err == nil {
		t.Error("odd payload accepted")
	}
}

func TestNTPTime(t *testing.T) {
	ts := ntpTime(time.Unix(0, 500_000_000))
	if ts>>32 != ntpEpochOffset || uint32(ts) != 1<<31 {
		t.Errorf("ntpTime = %x", ts)
	}
}

func TestIsVirtual(t *testing.T) {
	for name, want := range map[string]bool{"docker0": true, "veth12": true, "utun3": true, "eth0": false, "en0": false, "wlan0": false} {
		if isVirtual(name) != want {
			t.Errorf("isVirtual(%q) = %v", name, !want)
		}
	}
	if _, err := resolveInterface("does-not-exist0"); err == nil {
		t.Error("unknown interface accepted")
	}
}

// startServer runs a receiver on loopback without mDNS.
func startServer(t *testing.T) *Server {
	t.Helper()
	s := NewServer(ServerOptions{ID: "srv", Name: "Test", Address: "127.0.0.1:0", DisableAdvertising: true, Logger: quiet})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Stop() })
	return s
}

// testSignal is quantised to 16 bits and never zero, so it survives ALAC
// exactly and can be told apart from the silence sent around it.
func testSignal(frames int) []float32 {
	out := make([]float32, frames*audio.Channels)
	for i := range out {
		v := int16((i*37)%20000 + 1)
		if i%3 == 0 {
			v = -v
		}
		out[i] = audio.Int16ToFloat(v)
	}
	return out
}

func TestLoopback(t *testing.T) {
	for _, encrypted := range []bool{true, false} {
		name := "plain"
		if encrypted {
			name = "encrypted"
		}
		t.Run(name, func(t *testing.T) {
			srv := startServer(t)
			c := NewClient(ClientOptions{ID: "c", Name: "Test", Address: srv.Addr().String(), DisableEncryption: !encrypted, Logger: quiet})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := c.Connect(ctx); err != nil {
				t.Fatal(err)
			}
			defer c.Disconnect()
			if !c.Connected() {
				t.Fatal("not connected")
			}

			signal := testSignal(framesPerPkt * 32) // whole packets; a partial one waits for more audio
			chunk := 4410
			for i := 0; i < len(signal); i += chunk {
				if err := c.Send(protocols.Packet{Samples: signal[i:min(i+chunk, len(signal))], Format: audio.Internal}); err != nil {
					t.Fatal(err)
				}
			}

			var got []float32
			started := false
			deadline := time.After(5 * time.Second)
			for len(got) < len(signal) {
				select {
				case p := <-srv.Audio():
					if p.Format != audio.Internal {
						t.Fatalf("format %+v", p.Format)
					}
					for _, s := range p.Samples {
						if !started && s == 0 {
							continue
						}
						started = true
						got = append(got, s)
					}
				case <-deadline:
					t.Fatalf("received %d of %d samples", len(got), len(signal))
				}
			}
			got = got[:len(signal)]
			for i := range signal {
				if got[i] != signal[i] {
					t.Fatalf("sample %d = %v, want %v", i, got[i], signal[i])
				}
			}
		})
	}
}

func TestClientSendErrors(t *testing.T) {
	c := NewClient(ClientOptions{Address: "127.0.0.1:1", Logger: quiet})
	if err := c.Send(protocols.Packet{Format: audio.Internal}); !errors.Is(err, protocols.ErrNotConnected) {
		t.Errorf("Send before Connect = %v", err)
	}
	if err := c.Send(protocols.Packet{Format: audio.Format{SampleRate: 48000, Channels: 2}}); err == nil {
		t.Error("wrong format accepted")
	}
	if err := c.Disconnect(); err != nil {
		t.Errorf("Disconnect when not connected = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Connect(ctx); err == nil {
		t.Error("Connect to a closed port succeeded")
	}
}

// fakeReceiver answers every RTSP request with the given status.
func fakeReceiver(t *testing.T, status int) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				r := bufio.NewReader(conn)
				for {
					req, err := readRequest(r)
					if err != nil {
						return
					}
					resp := newResponse(status)
					resp.Header.Set("CSeq", req.Header.Get("CSeq"))
					resp.write(conn)
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func TestClientAirPlay2Required(t *testing.T) {
	for _, status := range []int{401, 403, 470} {
		c := NewClient(ClientOptions{Address: fakeReceiver(t, status), Logger: quiet})
		if err := c.Connect(context.Background()); !errors.Is(err, ErrAirPlay2Required) {
			t.Errorf("status %d: Connect = %v", status, err)
		}
	}
	c := NewClient(ClientOptions{Address: fakeReceiver(t, 500), Logger: quiet})
	if err := c.Connect(context.Background()); err == nil || errors.Is(err, ErrAirPlay2Required) {
		t.Errorf("status 500: Connect = %v", err)
	}
}

// rtspConn is a raw RTSP connection to a server under test.
type rtspConn struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
	cseq int
}

func dialRTSP(t *testing.T, s *Server) *rtspConn {
	conn, err := net.Dial("tcp", s.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return &rtspConn{t: t, conn: conn, r: bufio.NewReader(conn)}
}

func (c *rtspConn) do(method string, h textproto.MIMEHeader, body string) *response {
	c.t.Helper()
	if h == nil {
		h = textproto.MIMEHeader{}
	}
	c.cseq++
	h.Set("CSeq", strconv.Itoa(c.cseq))
	req := &request{Method: method, URI: "rtsp://127.0.0.1/1", Header: h, Body: []byte(body)}
	if err := req.write(c.conn); err != nil {
		c.t.Fatal(err)
	}
	resp, err := readResponse(c.r)
	if err != nil {
		c.t.Fatal(err)
	}
	return resp
}

func TestServerRTSP(t *testing.T) {
	srv := startServer(t)
	c := dialRTSP(t, srv)

	h := textproto.MIMEHeader{}
	h.Set("Apple-Challenge", encodeBase64(bytes.Repeat([]byte{1}, 16)))
	resp := c.do("OPTIONS", h, "")
	if resp.StatusCode != 200 || resp.Header.Get("Apple-Response") == "" || !strings.Contains(resp.Header.Get("Public"), "ANNOUNCE") {
		t.Errorf("OPTIONS: %+v", resp)
	}
	if resp := c.do("SETUP", nil, ""); resp.StatusCode != 455 {
		t.Errorf("SETUP before ANNOUNCE = %d", resp.StatusCode)
	}
	if resp := c.do("ANNOUNCE", nil, "v=0\r\na=rtpmap:96 mpeg4-generic/44100/2\r\n"); resp.StatusCode != 415 {
		t.Errorf("ANNOUNCE of unsupported codec = %d", resp.StatusCode)
	}
	if resp := c.do("ANNOUNCE", nil, "v=0\r\n"); resp.StatusCode != 415 {
		t.Errorf("ANNOUNCE without rtpmap = %d", resp.StatusCode)
	}
	if resp := c.do("ANNOUNCE", nil, "v=0\r\na=rtpmap:96 L16/44100/2\r\n"); resp.StatusCode != 200 {
		t.Errorf("ANNOUNCE L16 = %d", resp.StatusCode)
	}
	resp = c.do("SETUP", nil, "")
	tp := transportParams(resp.Header.Get("Transport"))
	if resp.StatusCode != 200 || tp["server_port"] == "" || tp["control_port"] == "" {
		t.Fatalf("SETUP: %d %v", resp.StatusCode, tp)
	}
	if resp := c.do("RECORD", nil, ""); resp.StatusCode != 200 {
		t.Errorf("RECORD = %d", resp.StatusCode)
	}

	// Half volume, then an L16 packet over UDP.
	h = textproto.MIMEHeader{}
	h.Set("Content-Type", "text/parameters")
	if resp := c.do("SET_PARAMETER", h, "volume: -6.020600\r\n"); resp.StatusCode != 200 {
		t.Errorf("SET_PARAMETER = %d", resp.StatusCode)
	}
	if resp := c.do("BOGUS", nil, ""); resp.StatusCode != 501 {
		t.Errorf("unknown method = %d", resp.StatusCode)
	}

	udp, err := net.Dial("udp", "127.0.0.1:"+tp["server_port"])
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	send := func(seq uint16) {
		pkt := rtpHeader{PayloadType: ptAudio, Seq: seq}.append(nil)
		udp.Write(append(pkt, 0x40, 0x00, 0xc0, 0x00)) // 0.5, -0.5
	}
	send(10)
	send(10) // duplicate, dropped
	send(11)
	for _, seq := range []uint16{10, 11} {
		select {
		case p := <-srv.Audio():
			if len(p.Samples) != 2 || math.Abs(float64(p.Samples[0]-0.25)) > 1e-3 || math.Abs(float64(p.Samples[1]+0.25)) > 1e-3 {
				t.Errorf("seq %d: samples %v, want about [0.25 -0.25]", seq, p.Samples)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("seq %d not delivered", seq)
		}
	}
	select {
	case p := <-srv.Audio():
		t.Errorf("duplicate delivered: %v", p.Samples)
	case <-time.After(100 * time.Millisecond):
	}

	if resp := c.do("TEARDOWN", nil, ""); resp.StatusCode != 200 {
		t.Errorf("TEARDOWN = %d", resp.StatusCode)
	}
}

func TestServerPreemption(t *testing.T) {
	srv := startServer(t)
	first := dialRTSP(t, srv)
	first.do("ANNOUNCE", nil, "a=rtpmap:96 L16/44100/2\r\n")
	first.do("SETUP", nil, "")
	first.do("RECORD", nil, "")

	second := dialRTSP(t, srv)
	second.do("ANNOUNCE", nil, "a=rtpmap:96 L16/44100/2\r\n")
	second.do("SETUP", nil, "")
	second.do("RECORD", nil, "")

	// The first sender's connection is closed by the server.
	first.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := first.r.ReadByte(); err == nil {
		t.Error("preempted session still open")
	}
}

func TestServerLifecycle(t *testing.T) {
	s := NewServer(ServerOptions{Address: "127.0.0.1:0", DisableAdvertising: true, Logger: quiet})
	if s.Addr() != nil {
		t.Error("Addr before Start")
	}
	if s.Protocol() != "airplay" {
		t.Error("protocol")
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(context.Background()); err == nil {
		t.Error("second Start succeeded")
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(); err != nil {
		t.Errorf("second Stop = %v", err)
	}
	bad := NewServer(ServerOptions{Address: "127.0.0.1:-1", DisableAdvertising: true, Logger: quiet})
	if err := bad.Start(context.Background()); err == nil {
		t.Error("invalid address accepted")
	}
}
