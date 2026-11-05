package pulse

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	pa "github.com/jfreymuth/pulse"
	"github.com/jfreymuth/pulse/proto"

	"github.com/yeixio/air-mixer/internal/audio"
)

const (
	// latency is the buffer PulseAudio keeps for the stream. Bluetooth
	// sinks add their own on top; this only needs to absorb scheduling
	// jitter.
	latency = 150 * time.Millisecond
	// requestTimeout bounds each request to the PulseAudio server.
	requestTimeout = 5 * time.Second
)

// ErrNoSink is returned by Connect while the configured sink does not
// exist.
var ErrNoSink = errors.New("pulse: sink not found")

// paStream is a playback stream on a real PulseAudio server.
type paStream struct {
	client *pa.Client
	pb     *pa.PlaybackStream
	sink   *pa.Sink
	once   sync.Once
}

func dialPulse(o Options, fill func([]float32) int) (stream, error) {
	client, err := pa.NewClient(
		pa.ClientServerString(o.Server),
		pa.ClientApplicationName("Air-Mixer"),
		pa.ClientTimeout(requestTimeout),
	)
	if err != nil {
		return nil, fmt.Errorf("pulse: connect to server: %w", err)
	}
	sink, err := findSink(client, o.Sink)
	if err != nil {
		client.Close()
		return nil, err
	}
	read := pa.Float32Reader(func(b []float32) (int, error) { fill(b); return len(b), nil })
	pb, err := client.NewPlayback(read,
		pa.PlaybackStereo,
		pa.PlaybackSampleRate(audio.SampleRate),
		pa.PlaybackLatency(latency.Seconds()), // after rate and channels, which it depends on
		pa.PlaybackSink(sink),
		pa.PlaybackMediaName(o.Name),
	)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("pulse: open stream on %s: %w", sink.ID(), err)
	}
	pb.Start()
	return &paStream{client: client, pb: pb, sink: sink}, nil
}

func (s *paStream) Sink() string { return s.sink.ID() }

func (s *paStream) Check() error {
	if s.pb.Closed() {
		if err := s.pb.Error(); err != nil {
			return fmt.Errorf("stream closed: %w", err)
		}
		return errors.New("stream closed by the server")
	}
	var info proto.GetSinkInputInfoReply
	if err := s.client.RawRequest(&proto.GetSinkInputInfo{SinkInputIndex: s.pb.StreamInputIndex()}, &info); err != nil {
		return fmt.Errorf("query stream: %w", err)
	}
	// PulseAudio moves streams off a sink that disappears, for example
	// when a Bluetooth speaker disconnects. Treat that as lost so the
	// audio does not play somewhere else.
	if info.SinkIndex != s.sink.SinkIndex() {
		return fmt.Errorf("sink %s went away", s.sink.ID())
	}
	return nil
}

func (s *paStream) Close() {
	s.once.Do(func() {
		s.pb.Close()
		s.client.Close()
	})
}

var macAddress = regexp.MustCompile(`^(?i)[0-9a-f]{2}([:_-][0-9a-f]{2}){5}$`)

// findSink resolves a sink by name, description or Bluetooth address.
func findSink(c *pa.Client, spec string) (*pa.Sink, error) {
	if spec == "" {
		s, err := c.DefaultSink()
		if err != nil {
			return nil, fmt.Errorf("pulse: default sink: %w", err)
		}
		return s, nil
	}
	sinks, err := c.ListSinks()
	if err != nil {
		return nil, fmt.Errorf("pulse: list sinks: %w", err)
	}
	candidates := make([]sinkInfo, len(sinks))
	for i, s := range sinks {
		candidates[i] = sinkInfo{id: s.ID(), description: s.Name()}
	}
	i, err := matchSink(candidates, spec)
	if err != nil {
		return nil, err
	}
	return sinks[i], nil
}

type sinkInfo struct{ id, description string }

// matchSink returns the index of the sink spec names. An exact name wins,
// then a description, then a Bluetooth address found in the name, as in
// bluez_sink.AA_BB_CC_DD_EE_FF.a2dp_sink or
// bluez_output.AA_BB_CC_DD_EE_FF.1.
func matchSink(sinks []sinkInfo, spec string) (int, error) {
	for i, s := range sinks {
		if s.id == spec {
			return i, nil
		}
	}
	for i, s := range sinks {
		if strings.EqualFold(s.description, spec) {
			return i, nil
		}
	}
	if macAddress.MatchString(spec) {
		mac := strings.ToUpper(strings.NewReplacer(":", "_", "-", "_").Replace(spec))
		for i, s := range sinks {
			if strings.Contains(strings.ToUpper(s.id), mac) {
				return i, nil
			}
		}
	}
	names := make([]string, len(sinks))
	for i, s := range sinks {
		names[i] = s.id
	}
	return -1, fmt.Errorf("%w: %q (available: %s)", ErrNoSink, spec, strings.Join(names, ", "))
}
