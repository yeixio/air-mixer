// Package radio is an input that plays a stream or file from a URL, such
// as an internet radio station, and keeps playing it: a station that
// drops is reconnected and a file starts again when it ends.
//
// MP3 and WAV are decoded in Go. AAC, Ogg, FLAC, Opus and HLS (.m3u8)
// need ffmpeg on $PATH, which the Docker image includes. Playlists in
// PLS and M3U form are followed to their first stream.
package radio

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/yeixio/air-mixer/internal/audio/decode"
	"github.com/yeixio/air-mixer/internal/protocols"
)

const (
	// chunk is how much audio each packet holds.
	chunk = 50 * time.Millisecond
	// lead is how far ahead of real time a file may be decoded.
	lead = 200 * time.Millisecond
	// steady is how long a stream must play before a failure counts as
	// new, resetting the backoff.
	steady = 30 * time.Second
	// maxPlaylist bounds a playlist file.
	maxPlaylist = 64 << 10
)

// Backoff between attempts, doubling up to maxBackoff. Tests shorten it.
var (
	minBackoff = time.Second
	maxBackoff = time.Minute
)

// Options configures a radio input.
type Options struct {
	ID, Name string
	URL      string
	// HTTP is the client for fetching; http.DefaultClient if nil.
	HTTP   *http.Client
	Logger *slog.Logger
}

// Server plays a URL as an input.
type Server struct {
	opts  Options
	log   *slog.Logger
	audio chan protocols.Packet

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	err    error // why the last attempt failed
}

// New returns a radio input. Call Start to begin playing.
func New(o Options) *Server {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.HTTP == nil {
		o.HTTP = http.DefaultClient
	}
	return &Server{opts: o, log: o.Logger.With("component", "radio", "server", o.ID), audio: make(chan protocols.Packet, 16)}
}

func (s *Server) ID() string                     { return s.opts.ID }
func (s *Server) Name() string                   { return s.opts.Name }
func (s *Server) Protocol() string               { return "radio" }
func (s *Server) Audio() <-chan protocols.Packet { return s.audio }

// Err is why the stream last failed, or nil while it plays.
func (s *Server) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Start begins playing in the background.
func (s *Server) Start(context.Context) error {
	if _, err := url.ParseRequestURI(s.opts.URL); err != nil {
		return fmt.Errorf("radio: invalid url: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return errors.New("radio: already started")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel, s.done = cancel, make(chan struct{})
	go s.run(ctx)
	return nil
}

// Stop stops playing.
func (s *Server) Stop() error {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	<-done
	return nil
}

func (s *Server) run(ctx context.Context) {
	defer close(s.done)
	backoff := minBackoff
	for {
		start := time.Now()
		err := s.play(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > steady {
			backoff = minBackoff
		}
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
		var wait time.Duration // a file ended: play it again at once
		if err != nil {
			wait = backoff
			backoff = min(backoff*2, maxBackoff)
			s.log.Warn("stream failed, retrying", "err", err, "retry_in", wait)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// play plays the URL once, until it ends or fails.
func (s *Server) play(ctx context.Context) error {
	dec, closeDec, err := s.open(ctx, s.opts.URL, 0)
	if err != nil {
		return err
	}
	defer closeDec()
	s.mu.Lock()
	s.err = nil
	s.mu.Unlock()
	s.log.Info("playing", "url", redact(s.opts.URL))

	f := dec.Format()
	buf := make([]float32, max(1, f.SampleRate*int(chunk/time.Millisecond)/1000)*f.Channels)
	start := time.Now()
	var frames int64
	for {
		n, rerr := dec.Read(buf)
		if n > 0 {
			p := protocols.Packet{Samples: append([]float32(nil), buf[:n]...), Format: f}
			frames += int64(n / f.Channels)
			// A live station arrives in real time; a file must not be
			// played faster than that.
			ahead := time.Duration(frames)*time.Second/time.Duration(f.SampleRate) - time.Since(start)
			if wait := ahead - lead; wait > 0 {
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(wait):
				}
			}
			select {
			case s.audio <- p:
			case <-ctx.Done():
				return nil
			}
		}
		switch {
		case errors.Is(rerr, io.EOF):
			if frames == 0 {
				return errors.New("radio: the stream has no audio")
			}
			return nil
		case rerr != nil:
			return fmt.Errorf("radio: %w", rerr)
		case ctx.Err() != nil:
			return nil
		}
	}
}

// open returns a decoder for rawURL, following playlists up to depth 3.
func (s *Server) open(ctx context.Context, rawURL string, depth int) (decode.Decoder, func(), error) {
	if depth > 3 {
		return nil, nil, errors.New("radio: playlists nested too deep")
	}
	if isHLS(rawURL, "") || !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		// HLS, RTSP and RTMP: ffmpeg fetches them itself.
		dec, err := decode.OpenURL(ctx, rawURL)
		if err != nil {
			return nil, nil, err
		}
		return dec, closer(dec, nil), nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("radio: %w", err)
	}
	req.Header.Set("User-Agent", "Air-Mixer")
	resp, err := s.opts.HTTP.Do(req) //nolint:bodyclose // closed by the returned func
	if err != nil {
		return nil, nil, fmt.Errorf("radio: fetch %s: %w", redact(rawURL), err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		resp.Body.Close()
		return nil, nil, fmt.Errorf("radio: fetch %s: %s", redact(rawURL), resp.Status)
	}
	ct := resp.Header.Get("Content-Type")
	if isHLS(rawURL, ct) {
		resp.Body.Close()
		dec, err := decode.OpenURL(ctx, rawURL)
		if err != nil {
			return nil, nil, err
		}
		return dec, closer(dec, nil), nil
	}
	if isPlaylist(rawURL, ct) {
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxPlaylist))
		resp.Body.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("radio: read playlist: %w", err)
		}
		if strings.Contains(string(body), "#EXT-X-") { // HLS served as a plain .m3u
			dec, err := decode.OpenURL(ctx, rawURL)
			if err != nil {
				return nil, nil, err
			}
			return dec, closer(dec, nil), nil
		}
		next, err := firstEntry(rawURL, string(body))
		if err != nil {
			return nil, nil, err
		}
		return s.open(ctx, next, depth+1)
	}
	dec, err := decode.New(resp.Body, ct)
	if err != nil {
		resp.Body.Close()
		return nil, nil, fmt.Errorf("radio: %s: %w", redact(rawURL), err)
	}
	return dec, closer(dec, resp.Body), nil
}

func closer(dec decode.Decoder, body io.Closer) func() {
	return func() {
		if body != nil {
			body.Close()
		}
		if c, ok := dec.(io.Closer); ok {
			c.Close() //nolint:errcheck,gosec // a failure was already reported by Read
		}
	}
}

func ext(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	p := strings.ToLower(u.Path)
	if i := strings.LastIndexByte(p, '.'); i >= 0 {
		return p[i:]
	}
	return ""
}

func isHLS(rawURL, contentType string) bool {
	ct := strings.ToLower(contentType)
	return ext(rawURL) == ".m3u8" || strings.Contains(ct, "vnd.apple.mpegurl")
}

func isPlaylist(rawURL, contentType string) bool {
	ct := strings.ToLower(contentType)
	switch {
	case strings.Contains(ct, "scpls"), strings.Contains(ct, "mpegurl"):
		return true
	case strings.HasPrefix(ct, "audio/") && ct != "audio/x-pn-realaudio":
		return false
	}
	e := ext(rawURL)
	return e == ".pls" || e == ".m3u"
}

// firstEntry returns the first stream a PLS or M3U playlist names,
// resolved against the playlist's own URL.
func firstEntry(base, body string) (string, error) {
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if k, v, ok := strings.Cut(line, "="); ok && !strings.Contains(k, "/") {
			// A PLS key, such as File1=http://... or Title1=Name.
			if !strings.HasPrefix(strings.ToLower(k), "file") {
				continue
			}
			line = strings.TrimSpace(v)
		}
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		b, err := url.Parse(base)
		if err != nil {
			return "", err
		}
		ref, err := url.Parse(line)
		if err != nil {
			continue
		}
		return b.ResolveReference(ref).String(), nil
	}
	return "", errors.New("radio: the playlist names no stream")
}

// redact hides a URL's password and query, which may hold a token.
func redact(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(invalid url)"
	}
	u.User = nil
	if u.RawQuery != "" {
		u.RawQuery = "…"
	}
	return u.String()
}
