// Package pipe runs an external receiver, such as librespot (Spotify
// Connect) or shairport-sync (AirPlay 2), that writes raw 16-bit
// little-endian stereo PCM to its standard output, and makes it an input.
//
// The program is restarted with backoff when it exits, and its output is
// read in real time: a receiver writing to a pipe has no sound card to
// pace it, so reading slowly is what keeps it from racing ahead.
package pipe

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/yeixio/air-mixer/internal/audio"
	"github.com/yeixio/air-mixer/internal/protocols"
)

// Backoff between restarts, and how much audio a read holds; tests
// shorten them.
var (
	minBackoff = time.Second
	maxBackoff = time.Minute
	chunk      = 20 * time.Millisecond
)

const (
	// lead is how far ahead of real time reading may run.
	lead = 100 * time.Millisecond
	// steady is how long the program must run before a crash counts as
	// new, resetting the backoff.
	steady = 30 * time.Second
)

// Options configures an external receiver.
type Options struct {
	ID, Name string
	// Protocol is what the input reports, such as "spotify".
	Protocol string
	// Command is the program and its arguments.
	Command []string
	// SampleRate of the PCM the program writes; 44100 if 0.
	SampleRate int
	Logger     *slog.Logger
}

// Server is an input fed by an external program.
type Server struct {
	o     Options
	log   *slog.Logger
	audio chan protocols.Packet

	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	running bool
	err     error
}

// New returns an external receiver input.
func New(o Options) *Server {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.SampleRate == 0 {
		o.SampleRate = audio.SampleRate
	}
	return &Server{o: o, log: o.Logger.With("component", o.Protocol, "server", o.ID), audio: make(chan protocols.Packet, 16)}
}

func (s *Server) ID() string                     { return s.o.ID }
func (s *Server) Name() string                   { return s.o.Name }
func (s *Server) Protocol() string               { return s.o.Protocol }
func (s *Server) Audio() <-chan protocols.Packet { return s.audio }

// Running reports whether the program is running.
func (s *Server) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// Err is why the program last stopped.
func (s *Server) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Start runs the program in the background.
func (s *Server) Start(context.Context) error {
	if len(s.o.Command) == 0 {
		return errors.New("pipe: no command")
	}
	if _, err := exec.LookPath(s.o.Command[0]); err != nil {
		return fmt.Errorf("%s: %w", s.o.Protocol, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return errors.New("pipe: already started")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel, s.done = cancel, make(chan struct{})
	go s.run(ctx)
	return nil
}

// Stop ends the program.
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
		err := s.once(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > steady {
			backoff = minBackoff
		}
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
		s.log.Warn("receiver stopped, restarting", "err", err, "retry_in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// tail keeps the last lines a program wrote to stderr, for errors.
type tail struct {
	mu   sync.Mutex
	buf  []byte
	log  *slog.Logger
	line []byte
}

func (t *tail) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > 2048 {
		t.buf = t.buf[len(t.buf)-2048:]
	}
	t.line = append(t.line, b...)
	for {
		i := strings.IndexByte(string(t.line), '\n')
		if i < 0 {
			break
		}
		if l := strings.TrimSpace(string(t.line[:i])); l != "" {
			t.log.Debug("receiver says", "line", l)
		}
		t.line = t.line[i+1:]
	}
	return len(b), nil
}

func (t *tail) last() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(string(t.buf)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func (s *Server) once(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, s.o.Command[0], s.o.Command[1:]...) //nolint:gosec // the command comes from the adapter, not from requests
	cmd.WaitDelay = 3 * time.Second
	stderr := &tail{log: s.log}
	cmd.Stderr = stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	s.mu.Lock()
	s.running, s.err = true, nil
	s.mu.Unlock()
	s.log.Info("receiver started", "program", s.o.Command[0])
	rerr := s.read(ctx, out)
	werr := cmd.Wait()
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
	if ctx.Err() != nil {
		return nil //nolint:nilerr // stopped on purpose, not a failure
	}
	if werr != nil {
		if msg := stderr.last(); msg != "" {
			return fmt.Errorf("%w: %s", werr, msg)
		}
		return werr
	}
	if rerr != nil && !errors.Is(rerr, io.EOF) {
		return rerr
	}
	return errors.New("the receiver exited")
}

// read passes PCM on in real time until the program's output ends.
func (s *Server) read(ctx context.Context, r io.Reader) error {
	f := audio.Format{SampleRate: s.o.SampleRate, Channels: 2}
	frameBytes := 4
	b := make([]byte, int(chunk.Seconds()*float64(f.SampleRate))*frameBytes)
	start := time.Now()
	var frames int64
	for {
		n, err := io.ReadFull(r, b)
		n -= n % frameBytes
		if n > 0 {
			samples := make([]float32, n/2)
			for i := range samples {
				samples[i] = audio.Int16ToFloat(int16(binary.LittleEndian.Uint16(b[2*i:])))
			}
			// A paused receiver writes nothing; start the clock again
			// rather than rush to catch up.
			if behind := time.Since(start) - time.Duration(frames)*time.Second/time.Duration(f.SampleRate); behind > 4*lead {
				start, frames = time.Now(), 0
			}
			frames += int64(n / frameBytes)
			ahead := time.Duration(frames)*time.Second/time.Duration(f.SampleRate) - time.Since(start)
			if wait := ahead - lead; wait > 0 {
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(wait):
				}
			}
			select {
			case s.audio <- protocols.Packet{Samples: samples, Format: f}:
			case <-ctx.Done():
				return nil
			}
		}
		if err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) {
				return io.EOF
			}
			return err
		}
	}
}
