package decode

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/yeixio/air-mixer/internal/audio"
)

// FFmpeg is the ffmpeg binary that decodes formats Go can't, found on
// $PATH at start-up. Empty turns that off.
var FFmpeg = lookFFmpeg()

func lookFFmpeg() string {
	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		return ""
	}
	return p
}

// ffmpegArgs makes ffmpeg write 16-bit stereo PCM at the router's rate.
func ffmpegArgs(input string) []string {
	return []string{
		"-hide_banner", "-loglevel", "error", "-nostdin",
		"-i", input,
		"-vn", "-f", "s16le", "-acodec", "pcm_s16le",
		"-ac", "2", "-ar", fmt.Sprint(audio.SampleRate),
		"pipe:1",
	}
}

// ffmpegDecoder reads the PCM an ffmpeg process writes.
type ffmpegDecoder struct {
	cmd    *exec.Cmd
	out    io.ReadCloser
	cancel context.CancelFunc
	buf    []byte
	eof    bool
	once   sync.Once
	err    error
}

// newFFmpegDecoder decodes r through ffmpeg's standard input.
func newFFmpegDecoder(r io.Reader) (Decoder, error) {
	return startFFmpeg(context.Background(), "pipe:0", r)
}

// OpenURL has ffmpeg fetch and decode a URL itself. It handles what a
// plain HTTP fetch can't, such as HLS playlists (.m3u8) and RTSP. It
// returns ErrUnsupportedFormat when ffmpeg isn't installed.
func OpenURL(ctx context.Context, url string) (Decoder, error) {
	if FFmpeg == "" {
		return nil, fmt.Errorf("%w: %s needs ffmpeg", ErrUnsupportedFormat, url)
	}
	return startFFmpeg(ctx, url, nil)
}

func startFFmpeg(ctx context.Context, input string, stdin io.Reader) (*ffmpegDecoder, error) {
	ctx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(ctx, FFmpeg, ffmpegArgs(input)...) //nolint:gosec // the binary is ffmpeg from $PATH; input is a URL or stdin
	cmd.Stdin = stdin
	cmd.WaitDelay = 2 * time.Second // don't hang on a stalled input
	out, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}
	return &ffmpegDecoder{cmd: cmd, out: out, cancel: cancel}, nil
}

func (d *ffmpegDecoder) Format() audio.Format {
	return audio.Format{SampleRate: audio.SampleRate, Channels: 2}
}

func (d *ffmpegDecoder) Read(buf []float32) (int, error) {
	n := len(buf) / 2 * 2
	if n == 0 {
		return 0, nil
	}
	if cap(d.buf) < n*2 {
		d.buf = make([]byte, n*2)
	}
	b := d.buf[:n*2]
	got, err := io.ReadAtLeast(d.out, b, 4)
	got -= got % 4
	for i := range got / 2 {
		buf[i] = audio.Int16ToFloat(int16(binary.LittleEndian.Uint16(b[i*2:])))
	}
	if got > 0 {
		return got / 2, nil
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		d.eof = true
		if werr := d.Close(); werr != nil {
			return 0, werr
		}
		return 0, io.EOF
	}
	return 0, err
}

// Close stops ffmpeg. After the end of the stream, it reports why ffmpeg
// failed, if it did.
func (d *ffmpegDecoder) Close() error {
	d.once.Do(func() {
		if !d.eof {
			d.cancel() // stopped early: don't wait for the end
		}
		werr := d.cmd.Wait()
		d.cancel()
		var exit *exec.ExitError
		if errors.As(werr, &exit) && exit.ExitCode() > 0 {
			d.err = fmt.Errorf("ffmpeg: %w", werr)
		}
	})
	return d.err
}
