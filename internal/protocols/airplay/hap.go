package airplay

// HomeKit transient pairing and the encrypted channel it sets up, which
// AirPlay 2 receivers require before anything else. The pairing is SRP-6a
// over the 3072-bit group of RFC 5054 with SHA-512, user "Pair-Setup" and
// the fixed PIN 3939; its session key encrypts the RTSP connection with
// ChaCha20-Poly1305. This follows the HomeKit Accessory Protocol and the
// open-source senders that speak it (pair_ap, used by OwnTone and
// shairport-sync).

import (
	"bytes"
	"crypto/hkdf"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"sync"

	"golang.org/x/crypto/chacha20poly1305"
)

// TLV8 types used by pair-setup.
const (
	tlvMethod    = 0x00
	tlvSalt      = 0x02
	tlvPublicKey = 0x03
	tlvProof     = 0x04
	tlvState     = 0x06
	tlvError     = 0x07
	tlvFlags     = 0x13

	pairFlagTransient = 0x10
)

// tlv8 is a decoded TLV8 message. Values longer than 255 bytes are split
// into consecutive items of the same type, which decoding joins.
type tlv8 map[byte][]byte

func encodeTLV8(items ...tlvItem) []byte {
	var b []byte
	for _, it := range items {
		v := it.value
		for {
			n := min(len(v), 255)
			b = append(b, it.typ, byte(n))
			b = append(b, v[:n]...)
			v = v[n:]
			if len(v) == 0 {
				break
			}
		}
	}
	return b
}

type tlvItem struct {
	typ   byte
	value []byte
}

func decodeTLV8(b []byte) (tlv8, error) {
	t := tlv8{}
	last := -1
	for len(b) > 0 {
		if len(b) < 2 || len(b) < 2+int(b[1]) {
			return nil, errors.New("truncated TLV8")
		}
		typ, n := b[0], int(b[1])
		if int(typ) == last {
			t[typ] = append(t[typ], b[2:2+n]...)
		} else {
			t[typ] = append([]byte(nil), b[2:2+n]...)
		}
		last = int(typ)
		b = b[2+n:]
	}
	return t, nil
}

// srpN is the 3072-bit prime of RFC 5054, with generator 5.
var (
	srpN, _ = new(big.Int).SetString(""+
		"FFFFFFFFFFFFFFFFC90FDAA22168C234C4C6628B80DC1CD129024E088A67CC74020BBEA63B"+
		"139B22514A08798E3404DDEF9519B3CD3A431B302B0A6DF25F14374FE1356D6D51C245E485"+
		"B576625E7EC6F44C42E9A637ED6B0BFF5CB6F406B7EDEE386BFB5A899FA5AE9F24117C4B1F"+
		"E649286651ECE45B3DC2007CB8A163BF0598DA48361C55D39A69163FA8FD24CF5F83655D23"+
		"DCA3AD961C62F356208552BB9ED529077096966D670C354E4ABC9804F1746C08CA18217C32"+
		"905E462E36CE3BE39E772C180E86039B2783A2EC07A28FB5C55DF06F4C52C9DE2BCBF69558"+
		"17183995497CEA956AE515D2261898FA051015728E5A8AAAC42DAD33170D04507A33A85521"+
		"ABDF1CBA64ECFB850458DBEF0A8AEA71575D060C7DB3970F85A6E1E4C7ABF5AE8CDB0933D7"+
		"1E8C94E04A25619DCEE3D2261AD2EE6BF12FFA06D98A0864D87602733EC86A64521F2B1817"+
		"7B200CBBE117577A615D6C770988C0BAD946E208E24FA074E5AB3143DB5BFCE0FD108E4B82"+
		"D120A93AD2CAFFFFFFFFFFFFFFFF", 16)
	srpG    = big.NewInt(5)
	srpNLen = 384
)

const (
	srpUser = "Pair-Setup"
	// transientPIN is the PIN every AirPlay 2 receiver accepts for
	// transient pairing.
	transientPIN = "3939"
)

func sha512Sum(parts ...[]byte) []byte {
	h := sha512.New()
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}

// pad left-pads n to the length of N.
func pad(n *big.Int) []byte { return n.FillBytes(make([]byte, srpNLen)) }

// srpMultiplier is k = H(N | pad(g)).
func srpMultiplier() *big.Int {
	return new(big.Int).SetBytes(sha512Sum(srpN.Bytes(), pad(srpG)))
}

// srpX is x = H(s | H(I ":" P)).
func srpX(salt []byte, user, pin string) *big.Int {
	return new(big.Int).SetBytes(sha512Sum(salt, sha512Sum([]byte(user+":"+pin))))
}

// srpProof is M1 = H(H(N) xor H(g) | H(I) | s | A | B | K).
func srpProof(user string, salt, a, b, key []byte) []byte {
	hn, hg := sha512Sum(srpN.Bytes()), sha512Sum(srpG.Bytes())
	for i := range hn {
		hn[i] ^= hg[i]
	}
	return sha512Sum(hn, sha512Sum([]byte(user)), salt, a, b, key)
}

// srpClient is the controller side of one SRP exchange.
type srpClient struct {
	a     *big.Int
	A     []byte
	M1    []byte
	key   []byte // K = H(S), the session key
	proof []byte // the server proof expected: H(A | M1 | K)
}

func newSRPClient(random io.Reader) (*srpClient, error) {
	buf := make([]byte, 32)
	if _, err := io.ReadFull(random, buf); err != nil {
		return nil, err
	}
	a := new(big.Int).SetBytes(buf)
	return &srpClient{a: a, A: new(big.Int).Exp(srpG, a, srpN).Bytes()}, nil
}

// challenge computes the client proof from the server's salt and B.
func (c *srpClient) challenge(salt, bBytes []byte, user, pin string) error {
	B := new(big.Int).SetBytes(bBytes)
	if new(big.Int).Mod(B, srpN).Sign() == 0 {
		return errors.New("pair-setup: invalid SRP public key")
	}
	u := new(big.Int).SetBytes(sha512Sum(pad(new(big.Int).SetBytes(c.A)), pad(B)))
	if u.Sign() == 0 {
		return errors.New("pair-setup: invalid SRP scrambler")
	}
	x := srpX(salt, user, pin)
	k := srpMultiplier()
	// S = (B - k*g^x) ^ (a + u*x) mod N
	gx := new(big.Int).Exp(srpG, x, srpN)
	base := new(big.Int).Sub(B, new(big.Int).Mul(k, gx))
	base.Mod(base, srpN)
	exp := new(big.Int).Add(c.a, new(big.Int).Mul(u, x))
	S := new(big.Int).Exp(base, exp, srpN)
	c.key = sha512Sum(S.Bytes())
	c.M1 = srpProof(user, salt, c.A, bBytes, c.key)
	c.proof = sha512Sum(c.A, c.M1, c.key)
	return nil
}

// pairTransient runs transient pair-setup and returns the 64-byte shared
// secret. post sends one pair-setup message and returns the answer.
func pairTransient(post func(body []byte) ([]byte, error), random io.Reader) ([]byte, error) {
	m2raw, err := post(encodeTLV8(
		tlvItem{tlvMethod, []byte{0}},
		tlvItem{tlvState, []byte{1}},
		tlvItem{tlvFlags, []byte{pairFlagTransient}},
	))
	if err != nil {
		return nil, err
	}
	m2, err := decodeTLV8(m2raw)
	if err != nil {
		return nil, fmt.Errorf("pair-setup M2: %w", err)
	}
	if e := m2[tlvError]; len(e) > 0 {
		return nil, fmt.Errorf("pair-setup M2: receiver reported error %d", e[0])
	}
	if !bytes.Equal(m2[tlvState], []byte{2}) || len(m2[tlvSalt]) == 0 || len(m2[tlvPublicKey]) == 0 {
		return nil, errors.New("pair-setup M2: missing salt or public key")
	}
	srp, err := newSRPClient(random)
	if err != nil {
		return nil, err
	}
	// Numbers are hashed without leading zeros, as pair_ap does.
	salt := new(big.Int).SetBytes(m2[tlvSalt]).Bytes()
	b := new(big.Int).SetBytes(m2[tlvPublicKey]).Bytes()
	if err := srp.challenge(salt, b, srpUser, transientPIN); err != nil {
		return nil, err
	}
	m4raw, err := post(encodeTLV8(
		tlvItem{tlvState, []byte{3}},
		tlvItem{tlvPublicKey, srp.A},
		tlvItem{tlvProof, srp.M1},
	))
	if err != nil {
		return nil, err
	}
	m4, err := decodeTLV8(m4raw)
	if err != nil {
		return nil, fmt.Errorf("pair-setup M4: %w", err)
	}
	if e := m4[tlvError]; len(e) > 0 {
		return nil, fmt.Errorf("pair-setup M4: receiver reported error %d", e[0])
	}
	if subtle.ConstantTimeCompare(m4[tlvProof], srp.proof) != 1 {
		return nil, errors.New("pair-setup M4: receiver proof does not match")
	}
	return srp.key, nil
}

// channelKeys derives the keys of one encrypted channel from the pairing
// secret, with HKDF-SHA-512.
func channelKeys(secret []byte, salt, writeInfo, readInfo string) (write, read []byte, err error) {
	if write, err = hkdf.Key(sha512.New, secret, []byte(salt), writeInfo, 32); err != nil {
		return nil, nil, err
	}
	read, err = hkdf.Key(sha512.New, secret, []byte(salt), readInfo, 32)
	return write, read, err
}

// maxFrame is the largest plaintext in one encrypted frame.
const maxFrame = 1024

// secureConn frames a connection as HAP does: each frame is a 2-byte
// little-endian length, that many bytes of ChaCha20-Poly1305 ciphertext and
// a 16-byte tag. The length is the additional data and the nonce a 64-bit
// little-endian counter per direction.
type secureConn struct {
	net.Conn

	wmu     sync.Mutex
	enc     aead
	wcount  uint64
	dec     aead
	rcount  uint64
	pending []byte
}

type aead interface {
	Seal(dst, nonce, plaintext, additionalData []byte) []byte
	Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error)
	Overhead() int
}

func newSecureConn(c net.Conn, writeKey, readKey []byte) (*secureConn, error) {
	enc, err := chacha20poly1305.New(writeKey)
	if err != nil {
		return nil, err
	}
	dec, err := chacha20poly1305.New(readKey)
	if err != nil {
		return nil, err
	}
	return &secureConn{Conn: c, enc: enc, dec: dec}, nil
}

func counterNonce(n uint64) []byte {
	nonce := make([]byte, chacha20poly1305.NonceSize)
	binary.LittleEndian.PutUint64(nonce[4:], n)
	return nonce
}

func (s *secureConn) Write(p []byte) (int, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	var out []byte
	for rest := p; len(rest) > 0; {
		n := min(len(rest), maxFrame)
		hdr := binary.LittleEndian.AppendUint16(nil, uint16(n))
		out = append(out, hdr...)
		out = s.enc.Seal(out, counterNonce(s.wcount), rest[:n], hdr)
		s.wcount++
		rest = rest[n:]
	}
	if _, err := s.Conn.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *secureConn) Read(p []byte) (int, error) {
	if len(s.pending) == 0 {
		hdr := make([]byte, 2)
		if _, err := io.ReadFull(s.Conn, hdr); err != nil {
			return 0, err
		}
		n := int(binary.LittleEndian.Uint16(hdr))
		if n > maxFrame {
			return 0, fmt.Errorf("encrypted frame of %d bytes", n)
		}
		frame := make([]byte, n+s.dec.Overhead())
		if _, err := io.ReadFull(s.Conn, frame); err != nil {
			return 0, err
		}
		plain, err := s.dec.Open(frame[:0], counterNonce(s.rcount), frame, hdr)
		if err != nil {
			return 0, fmt.Errorf("decrypt frame: %w", err)
		}
		s.rcount++
		s.pending = plain
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	return n, nil
}
