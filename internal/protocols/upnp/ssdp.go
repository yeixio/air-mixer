package upnp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

const ssdpAddr = "239.255.255.250:1900"

// SearchResult is a device that answered an SSDP search.
type SearchResult struct {
	Location string // URL of its device description
	USN      string
	Server   string
	ST       string
}

// Search sends an SSDP M-SEARCH for target, such as a device type, and
// collects answers for timeout.
func Search(ctx context.Context, target string, timeout time.Duration) ([]SearchResult, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return nil, fmt.Errorf("ssdp: %w", err)
	}
	defer conn.Close()
	dst, err := net.ResolveUDPAddr("udp4", ssdpAddr)
	if err != nil {
		return nil, err
	}
	mx := max(1, min(5, int(timeout/time.Second)))
	msg := "M-SEARCH * HTTP/1.1\r\n" +
		"HOST: " + ssdpAddr + "\r\n" +
		"MAN: \"ssdp:discover\"\r\n" +
		fmt.Sprintf("MX: %d\r\n", mx) +
		"ST: " + target + "\r\n" +
		"USER-AGENT: Air-Mixer UPnP/1.1\r\n\r\n"
	// UDP gets lost; ask twice.
	for range 2 {
		if _, err := conn.WriteToUDP([]byte(msg), dst); err != nil {
			return nil, fmt.Errorf("ssdp: %w", err)
		}
	}
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetReadDeadline(deadline)
	seen := map[string]bool{}
	var out []SearchResult
	buf := make([]byte, 4096)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			return out, nil // the deadline passed
		}
		res, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(buf[:n])), nil)
		if err != nil {
			continue
		}
		res.Body.Close()
		r := SearchResult{
			Location: res.Header.Get("Location"), USN: res.Header.Get("Usn"),
			Server: res.Header.Get("Server"), ST: res.Header.Get("St"),
		}
		if r.Location == "" || seen[r.Location] {
			continue
		}
		seen[r.Location] = true
		out = append(out, r)
	}
}

// advertiser answers SSDP searches for a device and announces it.
type advertiser struct {
	uuid     string
	types    []string // device and service types to advertise
	location func(peer net.IP) string
	conn     *net.UDPConn
	done     chan struct{}
	stop     chan struct{}
}

const maxAge = 1800

func newAdvertiser(uuid string, types []string, location func(net.IP) string) (*advertiser, error) {
	group, err := net.ResolveUDPAddr("udp4", ssdpAddr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenMulticastUDP("udp4", nil, group)
	if err != nil {
		return nil, fmt.Errorf("ssdp: listen: %w", err)
	}
	a := &advertiser{uuid: uuid, types: types, location: location, conn: conn, done: make(chan struct{}), stop: make(chan struct{})}
	go a.serve()
	go a.announce()
	return a, nil
}

// targets are every NT/ST the device answers to, with its USN.
func (a *advertiser) targets() [][2]string {
	out := [][2]string{
		{"upnp:rootdevice", "uuid:" + a.uuid + "::upnp:rootdevice"},
		{"uuid:" + a.uuid, "uuid:" + a.uuid},
	}
	for _, t := range a.types {
		out = append(out, [2]string{t, "uuid:" + a.uuid + "::" + t})
	}
	return out
}

func (a *advertiser) serve() {
	defer close(a.done)
	buf := make([]byte, 2048)
	for {
		n, from, err := a.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(buf[:n])))
		if err != nil || req.Method != "M-SEARCH" {
			continue
		}
		st := req.Header.Get("St")
		for _, t := range a.targets() {
			if st == "ssdp:all" || st == t[0] {
				a.reply(from, t[0], t[1])
			}
		}
	}
}

func (a *advertiser) reply(to *net.UDPAddr, st, usn string) {
	msg := "HTTP/1.1 200 OK\r\n" +
		fmt.Sprintf("CACHE-CONTROL: max-age=%d\r\n", maxAge) +
		"EXT:\r\n" +
		"LOCATION: " + a.location(to.IP) + "\r\n" +
		"SERVER: Linux/1.0 UPnP/1.1 Air-Mixer/1.0\r\n" +
		"ST: " + st + "\r\n" +
		"USN: " + usn + "\r\n\r\n"
	c, err := net.DialUDP("udp4", nil, to)
	if err != nil {
		return
	}
	defer c.Close()
	c.Write([]byte(msg)) //nolint:errcheck // best effort, the searcher asks again
}

func (a *advertiser) notify(nts string) {
	group, err := net.ResolveUDPAddr("udp4", ssdpAddr)
	if err != nil {
		return
	}
	c, err := net.DialUDP("udp4", nil, group)
	if err != nil {
		return
	}
	defer c.Close()
	local := net.IPv4zero
	if la, ok := c.LocalAddr().(*net.UDPAddr); ok {
		local = la.IP
	}
	for _, t := range a.targets() {
		msg := "NOTIFY * HTTP/1.1\r\n" +
			"HOST: " + ssdpAddr + "\r\n" +
			fmt.Sprintf("CACHE-CONTROL: max-age=%d\r\n", maxAge) +
			"LOCATION: " + a.location(local) + "\r\n" +
			"NT: " + t[0] + "\r\n" +
			"NTS: " + nts + "\r\n" +
			"SERVER: Linux/1.0 UPnP/1.1 Air-Mixer/1.0\r\n" +
			"USN: " + t[1] + "\r\n\r\n"
		c.Write([]byte(msg)) //nolint:errcheck // best effort
	}
}

func (a *advertiser) announce() {
	a.notify("ssdp:alive")
	t := time.NewTicker(maxAge / 3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-a.stop:
			return
		case <-t.C:
			a.notify("ssdp:alive")
		}
	}
}

func (a *advertiser) Close() {
	close(a.stop)
	a.notify("ssdp:byebye")
	a.conn.Close()
	<-a.done
}
