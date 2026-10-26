package airplay

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net"
	"strings"
)

// airportKeyPEM is the RSA key that AirPort Express receivers use to prove
// their identity and to unwrap the session's AES key. It was extracted from
// AirPort Express firmware and published by James Laird's shairport in 2011;
// every open-source RAOP implementation embeds it. It is part of the
// protocol, not a secret belonging to this project.
//
// Receivers use the private half; senders use the public half.
const airportKeyPEM = `-----BEGIN RSA PRIVATE KEY-----
MIIEpQIBAAKCAQEA59dE8qLieItsH1WgjrcFRKj6eUWqi+bGLOX1HL3U3GhC/j0Qg90u3sG/1CUt
wC5vOYvfDmFI6oSFXi5ELabWJmT2dKHzBJKa3k9ok+8t9ucRqMd6DZHJ2YCCLlDRKSKv6kDqnw4U
wPdpOMXziC/AMj3Z/lUVX1G7WSHCAWKf1zNS1eLvqr+boEjXuBOitnZ/bDzPHrTOZz0Dew0uowxf
/+sG+NCK3eQJVxqcaJ/vEHKIVd2M+5qL71yJQ+87X6oV3eaYvt3zWZYD6z5vYTcrtij2VZ9Zmni/
UAaHqn9JdsBWLUEpVviYnhimNVvYFZeCXg/IdTQ+x4IRdiXNv5hEewIDAQABAoIBAQDl8Axy9XfW
BLmkzkEiqoSwF0PsmVrPzH9KsnwLGH+QZlvjWd8SWYGN7u1507HvhF5N3drJoVU3O14nDY4TFQAa
LlJ9VM35AApXaLyY1ERrN7u9ALKd2LUwYhM7Km539O4yUFYikE2nIPscEsA5ltpxOgUGCY7b7ez5
NtD6nL1ZKauw7aNXmVAvmJTcuPxWmoktF3gDJKK2wxZuNGcJE0uFQEG4Z3BrWP7yoNuSK3dii2jm
lpPHr0O/KnPQtzI3eguhe0TwUem/eYSdyzMyVx/YpwkzwtYL3sR5k0o9rKQLtvLzfAqdBxBurciz
aaA/L0HIgAmOit1GJA2saMxTVPNhAoGBAPfgv1oeZxgxmotiCcMXFEQEWflzhWYTsXrhUIuz5jFu
a39GLS99ZEErhLdrwj8rDDViRVJ5skOp9zFvlYAHs0xh92ji1E7V/ysnKBfsMrPkk5KSKPrnjndM
oPdevWnVkgJ5jxFuNgxkOLMuG9i53B4yMvDTCRiIPMQ++N2iLDaRAoGBAO9v//mU8eVkQaoANf0Z
oMjW8CN4xwWA2cSEIHkd9AfFkftuv8oyLDCG3ZAf0vrhrrtkrfa7ef+AUb69DNggq4mHQAYBp7L+
k5DKzJrKuO0r+R0YbY9pZD1+/g9dVt91d6LQNepUE/yY2PP5CNoFmjedpLHMOPFdVgqDzDFxU8hL
AoGBANDrr7xAJbqBjHVwIzQ4To9pb4BNeqDndk5Qe7fT3+/H1njGaC0/rXE0Qb7q5ySgnsCb3DvA
cJyRM9SJ7OKlGt0FMSdJD5KG0XPIpAVNwgpXXH5MDJg09KHeh0kXo+QA6viFBi21y340NonnEfdf
54PX4ZGS/Xac1UK+pLkBB+zRAoGAf0AY3H3qKS2lMEI4bzEFoHeK3G895pDaK3TFBVmD7fV0Zhov
17fegFPMwOII8MisYm9ZfT2Z0s5Ro3s5rkt+nvLAdfC/PYPKzTLalpGSwomSNYJcB9HNMlmhkGzc
1JnLYT4iyUyx6pcZBmCd8bD0iwY/FzcgNDaUmbX9+XDvRA0CgYEAkE7pIPlE71qvfJQgoA9em0gI
LAuE4Pu13aKiJnfft7hIjbK+5kyb3TysZvoyDnb3HOKvInK7vXbKuU4ISgxB2bB3HcYzQMGsz1qJ
2gG0N5hvJpzwwhbhXqFKA4zaaSrw622wDniAK5MlIE0tIAKKP4yxNGjoD2QYjhBGuhvkWKY=
-----END RSA PRIVATE KEY-----`

var airportKey = func() *rsa.PrivateKey {
	block, _ := pem.Decode([]byte(airportKeyPEM))
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		panic("airplay: cannot parse AirPort key: " + err.Error())
	}
	return key
}()

// decodeBase64 accepts standard base64 with or without padding, as senders
// differ.
func decodeBase64(s string) ([]byte, error) {
	return base64.RawStdEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(s), "="))
}

// encodeBase64 produces unpadded base64, which every receiver accepts.
func encodeBase64(b []byte) string { return base64.RawStdEncoding.EncodeToString(b) }

// unwrapAESKey decrypts the rsaaeskey SDP attribute.
func unwrapAESKey(rsaaeskey string) ([]byte, error) {
	b, err := decodeBase64(rsaaeskey)
	if err != nil {
		return nil, fmt.Errorf("decode rsaaeskey: %w", err)
	}
	key, err := rsa.DecryptOAEP(sha1.New(), nil, airportKey, b, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt rsaaeskey: %w", err)
	}
	if len(key) != 16 {
		return nil, fmt.Errorf("AES key is %d bytes, want 16", len(key))
	}
	return key, nil
}

// wrapAESKey encrypts an AES key for the rsaaeskey SDP attribute.
func wrapAESKey(key []byte) (string, error) {
	b, err := rsa.EncryptOAEP(sha1.New(), rand.Reader, &airportKey.PublicKey, key, nil)
	if err != nil {
		return "", err
	}
	return encodeBase64(b), nil
}

// challengeMessage builds the data signed in an Apple-Response: the
// challenge, the receiver's IP address and MAC address, zero-padded to 32
// bytes.
func challengeMessage(challenge []byte, ip net.IP, mac net.HardwareAddr) []byte {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	msg := append(append(append([]byte{}, challenge...), ip...), mac...)
	for len(msg) < 32 {
		msg = append(msg, 0)
	}
	return msg
}

// appleResponse answers an Apple-Challenge header on behalf of a receiver.
func appleResponse(challenge string, ip net.IP, mac net.HardwareAddr) (string, error) {
	c, err := decodeBase64(challenge)
	if err != nil {
		return "", fmt.Errorf("decode Apple-Challenge: %w", err)
	}
	sig, err := rsa.SignPKCS1v15(nil, airportKey, crypto.Hash(0), challengeMessage(c, ip, mac))
	if err != nil {
		return "", err
	}
	return encodeBase64(sig), nil
}

// hardwareAddrFor returns the MAC address of the interface that owns ip, or
// six zero bytes if there is none (for example on loopback).
func hardwareAddrFor(ip net.IP) net.HardwareAddr {
	ifaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range ifaces {
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, a := range addrs {
				if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) && len(iface.HardwareAddr) == 6 {
					return iface.HardwareAddr
				}
			}
		}
	}
	return make(net.HardwareAddr, 6)
}
