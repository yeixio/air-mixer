package airplay

import (
	"fmt"
	"strings"
)

// sdpAttributes returns the "a=" attributes of an SDP body. For rtpmap and
// fmtp the payload type prefix is kept in the value.
func sdpAttributes(body []byte) map[string]string {
	attrs := make(map[string]string)
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(line, "a=")
		if !ok {
			continue
		}
		k, v, _ := strings.Cut(rest, ":")
		attrs[k] = v
	}
	return attrs
}

// announceSDP builds the ANNOUNCE body a sender transmits.
func announceSDP(sessionID uint32, localIP, remoteIP, fmtp, rsaAESKey, aesIV string) []byte {
	lines := []string{
		"v=0",
		fmt.Sprintf("o=iTunes %d 0 IN IP4 %s", sessionID, localIP),
		"s=iTunes",
		"c=IN IP4 " + remoteIP,
		"t=0 0",
		"m=audio 0 RTP/AVP 96",
		"a=rtpmap:96 AppleLossless",
		"a=fmtp:" + fmtp,
	}
	if rsaAESKey != "" {
		lines = append(lines, "a=rsaaeskey:"+rsaAESKey, "a=aesiv:"+aesIV)
	}
	return []byte(strings.Join(lines, "\r\n") + "\r\n")
}

// transportParams parses an RTSP Transport header into its key=value
// parameters; bare flags map to "".
func transportParams(h string) map[string]string {
	params := make(map[string]string)
	for _, p := range strings.Split(h, ";") {
		k, v, _ := strings.Cut(strings.TrimSpace(p), "=")
		params[k] = v
	}
	return params
}
