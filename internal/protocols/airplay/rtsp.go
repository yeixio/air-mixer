package airplay

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/textproto"
	"slices"
	"strconv"
	"strings"
)

// maxBody bounds RTSP bodies. Real ones are a few hundred bytes of SDP or
// parameters; cover art can reach a few hundred kilobytes.
const maxBody = 4 << 20

// request is an RTSP request.
type request struct {
	Method string
	URI    string
	Header textproto.MIMEHeader
	Body   []byte
}

// response is an RTSP response.
type response struct {
	StatusCode int
	Reason     string
	Header     textproto.MIMEHeader
	Body       []byte
}

func newResponse(code int) *response {
	return &response{StatusCode: code, Reason: statusText(code), Header: textproto.MIMEHeader{}}
}

func statusText(code int) string {
	switch code {
	case 200:
		return "OK"
	case 400:
		return "Bad Request"
	case 403:
		return "Forbidden"
	case 404:
		return "Not Found"
	case 415:
		return "Unsupported Media Type"
	case 454:
		return "Session Not Found"
	case 455:
		return "Method Not Valid in This State"
	case 500:
		return "Internal Server Error"
	case 501:
		return "Not Implemented"
	}
	return "Unknown"
}

func readRequest(r *bufio.Reader) (*request, error) {
	tp := textproto.NewReader(r)
	line, err := tp.ReadLine()
	if err != nil {
		return nil, err
	}
	parts := strings.Fields(line)
	if len(parts) != 3 || !strings.HasPrefix(parts[2], "RTSP/") {
		return nil, fmt.Errorf("malformed RTSP request line %q", line)
	}
	req := &request{Method: parts[0], URI: parts[1]}
	if req.Header, err = tp.ReadMIMEHeader(); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	req.Body, err = readBody(r, req.Header)
	return req, err
}

func readResponse(r *bufio.Reader) (*response, error) {
	tp := textproto.NewReader(r)
	line, err := tp.ReadLine()
	if err != nil {
		return nil, err
	}
	proto, rest, _ := strings.Cut(line, " ")
	code, reason, _ := strings.Cut(rest, " ")
	if !strings.HasPrefix(proto, "RTSP/") {
		return nil, fmt.Errorf("malformed RTSP status line %q", line)
	}
	resp := &response{Reason: reason}
	if resp.StatusCode, err = strconv.Atoi(code); err != nil {
		return nil, fmt.Errorf("malformed RTSP status line %q", line)
	}
	if resp.Header, err = tp.ReadMIMEHeader(); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	resp.Body, err = readBody(r, resp.Header)
	return resp, err
}

func readBody(r *bufio.Reader, h textproto.MIMEHeader) ([]byte, error) {
	cl := h.Get("Content-Length")
	if cl == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(cl)
	if err != nil || n < 0 || n > maxBody {
		return nil, fmt.Errorf("invalid Content-Length %q", cl)
	}
	body := make([]byte, n)
	_, err = io.ReadFull(r, body)
	return body, err
}

func (req *request) write(w io.Writer) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s RTSP/1.0\r\n", req.Method, req.URI)
	writeHeader(&b, req.Header, len(req.Body))
	b.Write(req.Body)
	_, err := io.WriteString(w, b.String())
	return err
}

func (resp *response) write(w io.Writer) error {
	var b strings.Builder
	fmt.Fprintf(&b, "RTSP/1.0 %d %s\r\n", resp.StatusCode, resp.Reason)
	writeHeader(&b, resp.Header, len(resp.Body))
	b.Write(resp.Body)
	_, err := io.WriteString(w, b.String())
	return err
}

// writeHeader writes headers in a stable order, CSeq first, using the
// spellings Apple devices expect.
func writeHeader(b *strings.Builder, h textproto.MIMEHeader, bodyLen int) {
	if v := h.Get("CSeq"); v != "" {
		fmt.Fprintf(b, "CSeq: %s\r\n", v)
	}
	keys := make([]string, 0, len(h))
	for k := range h {
		if k != "Cseq" && k != "Content-Length" {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		for _, v := range h[k] {
			fmt.Fprintf(b, "%s: %s\r\n", headerSpelling(k), v)
		}
	}
	if bodyLen > 0 {
		fmt.Fprintf(b, "Content-Length: %d\r\n", bodyLen)
	}
	b.WriteString("\r\n")
}

// headerSpelling undoes Go's MIME canonicalisation for headers whose
// canonical form differs from what AirPlay uses.
func headerSpelling(k string) string {
	switch k {
	case "Dacp-Id":
		return "DACP-ID"
	case "Rtp-Info":
		return "RTP-Info"
	}
	return k
}
