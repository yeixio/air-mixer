package httpstream

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// URL is where a device reaches the stream of output id. publicURL is how
// other machines reach Air-Mixer's API; when it is empty, URL uses this
// machine's address on the route to peerHost, at webPort.
func URL(publicURL string, webPort int, id, peerHost string) (string, error) {
	base := strings.TrimRight(publicURL, "/")
	if base == "" {
		ip, err := LocalAddrFor(peerHost)
		if err != nil {
			return "", fmt.Errorf("finding Air-Mixer's address for %s (set server.public_url): %w", peerHost, err)
		}
		base = "http://" + net.JoinHostPort(ip, strconv.Itoa(webPort))
	}
	return base + "/api/clients/" + url.PathEscape(id) + "/stream", nil
}

// LocalAddrFor is this machine's address on the way to host, a host name
// or IP with or without a port. No packet is sent: connecting a UDP
// socket only picks the route.
func LocalAddrFor(host string) (string, error) {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	c, err := net.Dial("udp", net.JoinHostPort(host, "9"))
	if err != nil {
		return "", err
	}
	defer c.Close()
	addr, ok := c.LocalAddr().(*net.UDPAddr)
	if !ok {
		return "", errors.New("no local address")
	}
	return addr.IP.String(), nil
}
