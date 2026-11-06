package airplay

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"net"
	"net/textproto"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/chacha20poly1305"

	"github.com/yeixio/air-mixer/internal/audio"
	"github.com/yeixio/air-mixer/internal/audio/alac"
	"github.com/yeixio/air-mixer/internal/protocols"
)

func TestTLV8(t *testing.T) {
	long := bytes.Repeat([]byte{7}, 600)
	b := encodeTLV8(tlvItem{tlvState, []byte{1}}, tlvItem{tlvPublicKey, long}, tlvItem{tlvProof, []byte{}})
	got, err := decodeTLV8(b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got[tlvState], []byte{1}) || !bytes.Equal(got[tlvPublicKey], long) {
		t.Errorf("decoded %v", got)
	}
	if len(b) != 3+(2+255)*2+(2+90)+2 {
		t.Errorf("long value not split into 255-byte items: %d bytes", len(b))
	}
	if _, err := decodeTLV8([]byte{1, 5, 0}); err == nil {
		t.Error("truncated TLV accepted")
	}
}

func TestPlistRoundTrip(t *testing.T) {
	in := map[string]any{
		"streams": []any{map[string]any{
			"type": int64(96), "ct": int64(2), "shk": bytes.Repeat([]byte{1}, 32),
			"isMedia": true, "latencyMax": int64(88200), "streamConnectionID": int64(-5),
		}},
		"name":           "Küche",
		"timingProtocol": "NTP",
		"rate":           1.5,
		"big":            int64(1 << 40),
		"empty":          map[string]any{},
	}
	// Enough keys to need a long-form count.
	for i := range 20 {
		in["k"+strconv.Itoa(i)] = int64(i)
	}
	b, err := encodePlist(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := decodePlist(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round trip:\n in %v\nout %v", in, out)
	}
	for _, bad := range [][]byte{nil, []byte("bplist00"), append([]byte("bplist00"), make([]byte, 40)...)} {
		if _, err := decodePlist(bad); err == nil {
			t.Errorf("decoded %q", bad)
		}
	}
}

// TestPlistApple decodes a SETUP answer as Apple's plutil writes it.
func TestPlistApple(t *testing.T) {
	// {"eventPort": 50001, "timingPort": 0}
	b := []byte("bplist00\xd2\x01\x02\x03\x04YeventPortZtimingPort\x11\xc3Q\x10\x00" +
		"\x08\x0d\x17\x22\x25" +
		"\x00\x00\x00\x00\x00\x00\x01\x01\x00\x00\x00\x00\x00\x00\x00\x05" +
		"\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x27")
	v, err := decodePlist(b)
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	if p, _ := plistInt(m, "eventPort"); p != 50001 {
		t.Errorf("decoded %v", m)
	}
}

// srpServer is the accessory side of SRP, for the fake receiver.
type srpServer struct {
	salt, B []byte
	b, v    *big.Int
}

func newSRPServer(pin string) *srpServer {
	s := &srpServer{salt: randBytes(16)}
	x := srpX(new(big.Int).SetBytes(s.salt).Bytes(), srpUser, pin)
	s.v = new(big.Int).Exp(srpG, x, srpN)
	s.b = new(big.Int).SetBytes(randBytes(32))
	B := new(big.Int).Mul(srpMultiplier(), s.v)
	B.Add(B, new(big.Int).Exp(srpG, s.b, srpN))
	B.Mod(B, srpN)
	s.B = B.Bytes()
	return s
}

// verify checks the client's proof and returns the session key and the
// server's proof.
func (s *srpServer) verify(aBytes, m1 []byte) (key, proof []byte, err error) {
	A := new(big.Int).SetBytes(aBytes)
	u := new(big.Int).SetBytes(sha512Sum(pad(A), pad(new(big.Int).SetBytes(s.B))))
	S := new(big.Int).Exp(new(big.Int).Mul(A, new(big.Int).Exp(s.v, u, srpN)), s.b, srpN)
	key = sha512Sum(S.Bytes())
	want := srpProof(srpUser, new(big.Int).SetBytes(s.salt).Bytes(), A.Bytes(), new(big.Int).SetBytes(s.B).Bytes(), key)
	if !bytes.Equal(m1, want) {
		return nil, nil, errors.New("bad proof")
	}
	return key, sha512Sum(A.Bytes(), m1, key), nil
}

func TestPairTransient(t *testing.T) {
	for _, pin := range []string{transientPIN, "1234"} {
		srv := newSRPServer(pin)
		var key []byte
		got, err := pairTransient(func(body []byte) ([]byte, error) {
			m, err := decodeTLV8(body)
			if err != nil {
				return nil, err
			}
			switch m[tlvState][0] {
			case 1:
				if !bytes.Equal(m[tlvFlags], []byte{pairFlagTransient}) {
					t.Errorf("M1 flags %v", m[tlvFlags])
				}
				return encodeTLV8(tlvItem{tlvState, []byte{2}}, tlvItem{tlvSalt, srv.salt}, tlvItem{tlvPublicKey, srv.B}), nil
			case 3:
				k, proof, err := srv.verify(m[tlvPublicKey], m[tlvProof])
				if err != nil {
					// The receiver answers a bad proof with an error TLV.
					return encodeTLV8(tlvItem{tlvState, []byte{4}}, tlvItem{tlvError, []byte{2}}), nil //nolint:nilerr
				}
				key = k
				return encodeTLV8(tlvItem{tlvState, []byte{4}}, tlvItem{tlvProof, proof}), nil
			}
			return nil, errors.New("unexpected state")
		}, rand.Reader)
		if pin == transientPIN {
			if err != nil || len(got) != 64 || !bytes.Equal(got, key) {
				t.Errorf("pairing = %x, %v; server key %x", got, err, key)
			}
		} else if err == nil {
			t.Error("pairing with the wrong PIN succeeded")
		}
	}
}

func TestSecureConn(t *testing.T) {
	a, b := net.Pipe()
	k1, k2 := randBytes(32), randBytes(32)
	ca, _ := newSecureConn(a, k1, k2)
	cb, _ := newSecureConn(b, k2, k1)
	msg := bytes.Repeat([]byte("AirPlay "), 300) // three frames
	go func() { ca.Write(msg); ca.Write([]byte("again")) }()
	got := make([]byte, len(msg)+5)
	if _, err := ioReadFull(cb, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, append(msg, "again"...)) {
		t.Error("decrypted text differs")
	}
}

func ioReadFull(r interface{ Read([]byte) (int, error) }, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		m, err := r.Read(b[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func TestSealAudio(t *testing.T) {
	a, _ := chacha20poly1305.New(randBytes(32))
	hdr := rtpHeader{PayloadType: ptAudio, Seq: 9, Timestamp: 1234, SSRC: 77}.append(nil)
	pkt := sealAudio(a, append([]byte(nil), hdr...), []byte("alac frame"), 42)
	if !bytes.Equal(pkt[:rtpHeaderLen], hdr) || len(pkt) != rtpHeaderLen+10+16+8 {
		t.Fatalf("packet %x", pkt)
	}
	got, err := openAudio(a, pkt)
	if err != nil || string(got) != "alac frame" {
		t.Errorf("open = %q, %v", got, err)
	}
	pkt[5] ^= 1 // the timestamp is authenticated
	if _, err := openAudio(a, pkt); err == nil {
		t.Error("tampered header accepted")
	}
}

// fakeReceiver2 is an AirPlay 2 receiver with transient pairing, enough to
// run the client against.
type fakeReceiver2 struct {
	t        *testing.T
	ln       net.Listener
	audio    *net.UDPConn
	control  *net.UDPConn
	refuse   bool
	mu       sync.Mutex
	methods  []string
	session  map[string]any
	stream   map[string]any
	secret   []byte
	eventAck chan bool
}

func startReceiver2(t *testing.T) *fakeReceiver2 {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	au, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	ctl, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	r := &fakeReceiver2{t: t, ln: ln, audio: au, control: ctl, eventAck: make(chan bool, 1)}
	t.Cleanup(func() { ln.Close(); au.Close(); ctl.Close() })
	go r.serve()
	return r
}

func (r *fakeReceiver2) serve() {
	for {
		conn, err := r.ln.Accept()
		if err != nil {
			return
		}
		go r.handle(conn)
	}
}

func (r *fakeReceiver2) handle(raw net.Conn) {
	defer raw.Close()
	conn := raw
	br := bufio.NewReader(conn)
	srp := newSRPServer(transientPIN)
	for {
		req, err := readRequest(br)
		if err != nil {
			return
		}
		r.mu.Lock()
		r.methods = append(r.methods, req.Method+" "+req.URI)
		r.mu.Unlock()
		resp := newResponse(200)
		resp.Header.Set("CSeq", req.Header.Get("CSeq"))
		var upgrade []byte
		switch {
		case req.URI == "/pair-setup":
			if r.refuse {
				resp = newResponse(470)
				resp.Header.Set("CSeq", req.Header.Get("CSeq"))
				break
			}
			if req.Header.Get("X-Apple-HKP") != "4" {
				r.t.Errorf("pair-setup without X-Apple-HKP: 4")
			}
			m, _ := decodeTLV8(req.Body)
			if m[tlvState][0] == 1 {
				resp.Body = encodeTLV8(tlvItem{tlvState, []byte{2}}, tlvItem{tlvSalt, srp.salt}, tlvItem{tlvPublicKey, srp.B})
			} else {
				key, proof, err := srp.verify(m[tlvPublicKey], m[tlvProof])
				if err != nil {
					r.t.Errorf("pairing: %v", err)
					return
				}
				resp.Body = encodeTLV8(tlvItem{tlvState, []byte{4}}, tlvItem{tlvProof, proof})
				upgrade = key
			}
		case req.Method == "SETUP":
			if req.Header.Get("Content-Type") != "application/x-apple-binary-plist" {
				r.t.Errorf("SETUP content type %q", req.Header.Get("Content-Type"))
			}
			v, err := decodePlist(req.Body)
			if err != nil {
				r.t.Errorf("SETUP body: %v", err)
				return
			}
			m := v.(map[string]any)
			var answer map[string]any
			if streams, ok := m["streams"].([]any); ok {
				r.mu.Lock()
				r.stream = streams[0].(map[string]any)
				r.mu.Unlock()
				answer = map[string]any{"streams": []any{map[string]any{
					"type": int64(96), "streamID": int64(1),
					"dataPort":    int64(r.audio.LocalAddr().(*net.UDPAddr).Port),
					"controlPort": int64(r.control.LocalAddr().(*net.UDPAddr).Port),
				}}}
			} else {
				r.mu.Lock()
				r.session = m
				r.mu.Unlock()
				answer = map[string]any{"eventPort": int64(r.events()), "timingPort": int64(0)}
			}
			resp.Body, _ = encodePlist(answer)
		}
		if err := resp.write(conn); err != nil {
			return
		}
		if upgrade != nil {
			r.mu.Lock()
			r.secret = upgrade
			r.mu.Unlock()
			w, rd, _ := channelKeys(upgrade, "Control-Salt", "Control-Read-Encryption-Key", "Control-Write-Encryption-Key")
			sc, _ := newSecureConn(raw, w, rd)
			conn, br = sc, bufio.NewReader(sc)
		}
	}
}

// events opens the event port and sends one request once the client
// connects.
func (r *fakeReceiver2) events() int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		r.t.Fatal(err)
	}
	go func() {
		defer ln.Close()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r.mu.Lock()
		secret := r.secret
		r.mu.Unlock()
		w, rd, _ := channelKeys(secret, "Events-Salt", "Events-Write-Encryption-Key", "Events-Read-Encryption-Key")
		sc, _ := newSecureConn(conn, w, rd)
		req := &request{Method: "POST", URI: "/command", Header: textproto.MIMEHeader{"Cseq": {"1"}}, Body: []byte("x")}
		if err := req.write(sc); err != nil {
			return
		}
		resp, err := readResponse(bufio.NewReader(sc))
		r.eventAck <- err == nil && resp.StatusCode == 200
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestAirPlay2Loopback(t *testing.T) {
	r := startReceiver2(t)
	c := NewClient(ClientOptions{ID: "c", Name: "Den", Address: r.ln.Addr().String(), AirPlay2: true, Logger: quiet})
	if c.Protocol() != "airplay2" {
		t.Errorf("protocol %q", c.Protocol())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Disconnect()

	select {
	case ok := <-r.eventAck:
		if !ok {
			t.Error("event request not answered")
		}
	case <-time.After(3 * time.Second):
		t.Error("client never opened the event channel")
	}

	r.mu.Lock()
	session, stream, secret := r.session, r.stream, r.secret
	r.mu.Unlock()
	if session["timingProtocol"] != "NTP" || session["name"] != "Den" {
		t.Errorf("session SETUP %v", session)
	}
	if tp, _ := plistInt(session, "timingPort"); tp == 0 {
		t.Error("no timing port")
	}
	for k, want := range map[string]int64{"type": 96, "ct": 2, "audioFormat": 1 << 18, "spf": framesPerPkt, "sr": 44100} {
		if got, _ := plistInt(stream, k); got != want {
			t.Errorf("stream %s = %d, want %d", k, got, want)
		}
	}
	shk, _ := stream["shk"].([]byte)
	if !bytes.Equal(shk, secret[:32]) {
		t.Error("shk is not the pairing secret")
	}

	signal := testSignal(framesPerPkt * 16)
	if err := c.Send(protocols.Packet{Samples: signal, Format: audio.Internal}); err != nil {
		t.Fatal(err)
	}
	a, _ := chacha20poly1305.New(shk)
	dec, _ := alac.NewDecoder(alac.DefaultConfig())
	var got []float32
	buf := make([]byte, 4096)
	r.audio.SetReadDeadline(time.Now().Add(5 * time.Second))
	for len(got) < len(signal) {
		n, err := r.audio.Read(buf)
		if err != nil {
			t.Fatalf("received %d of %d samples: %v", len(got), len(signal), err)
		}
		payload, err := openAudio(a, buf[:n])
		if err != nil {
			t.Fatalf("decrypt audio: %v", err)
		}
		samples, err := dec.DecodeFloat(payload)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range samples {
			if len(got) == 0 && s == 0 {
				continue
			}
			got = append(got, s)
		}
	}
	for i := range signal {
		if got[i] != signal[i] {
			t.Fatalf("sample %d = %v, want %v", i, got[i], signal[i])
		}
	}

	// A sync packet reaches the receiver's control port.
	r.control.SetReadDeadline(time.Now().Add(3 * time.Second))
	if n, err := r.control.Read(buf); err != nil || n != 20 || buf[1] != 0x80|ptSync {
		t.Errorf("sync packet %x, %v", buf[:n], err)
	}

	time.Sleep(feedbackEvery + 500*time.Millisecond)
	if err := c.Disconnect(); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	methods := r.methods
	r.mu.Unlock()
	want := []string{"GET /info", "POST /pair-setup", "POST /pair-setup", "SETUP", "RECORD", "SETUP", "POST /feedback", "TEARDOWN"}
	var seen []string
	for _, m := range methods {
		name, uri, _ := cutSpace(m)
		if name == "POST" || name == "GET" {
			seen = append(seen, name+" "+uri)
		} else {
			seen = append(seen, name)
		}
	}
	if !reflect.DeepEqual(seen, want) {
		t.Errorf("requests %v, want %v", seen, want)
	}
}

func cutSpace(s string) (string, string, bool) {
	i := bytes.IndexByte([]byte(s), ' ')
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+1:], true
}

func TestAirPlay2PairingRefused(t *testing.T) {
	r := startReceiver2(t)
	r.refuse = true
	c := NewClient(ClientOptions{Address: r.ln.Addr().String(), AirPlay2: true, Logger: quiet})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Connect(ctx); !errors.Is(err, ErrPairingRefused) {
		t.Errorf("Connect = %v", err)
	}
}
