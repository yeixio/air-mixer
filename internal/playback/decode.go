package playback

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/hajimehoshi/go-mp3"

	"github.com/yeixio/air-mixer/internal/audio"
)

// ErrUnsupportedFormat is returned for audio Air-Mixer cannot decode.
var ErrUnsupportedFormat = errors.New("unsupported audio format")

// decoder reads interleaved float32 samples from an encoded stream.
type decoder interface {
	Format() audio.Format
	// Read fills buf with whole frames and returns the number of samples
	// written. It returns io.EOF at the end of the stream.
	Read(buf []float32) (int, error)
}

// newDecoder detects the encoding of r from its first bytes, falling back
// to contentType, and returns a decoder for it. MP3 and WAV are supported.
func newDecoder(r io.Reader, contentType string) (decoder, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	head, _ := br.Peek(12)
	ct := strings.ToLower(contentType)
	switch {
	case len(head) >= 12 && string(head[0:4]) == "RIFF" && string(head[8:12]) == "WAVE":
		return newWAVDecoder(br)
	case len(head) >= 3 && string(head[0:3]) == "ID3",
		len(head) >= 2 && head[0] == 0xFF && head[1]&0xE0 == 0xE0:
		return newMP3Decoder(br)
	case len(head) >= 4 && string(head[0:4]) == "fLaC":
		return nil, fmt.Errorf("%w: FLAC (use MP3 or WAV)", ErrUnsupportedFormat)
	case len(head) >= 4 && string(head[0:4]) == "OggS":
		return nil, fmt.Errorf("%w: Ogg (use MP3 or WAV)", ErrUnsupportedFormat)
	case strings.Contains(ct, "mpeg") || strings.Contains(ct, "mp3"):
		return newMP3Decoder(br)
	case len(head) == 0:
		return nil, fmt.Errorf("%w: empty stream", ErrUnsupportedFormat)
	}
	if ct == "" {
		ct = "unknown content type"
	}
	return nil, fmt.Errorf("%w: %s (use MP3 or WAV)", ErrUnsupportedFormat, ct)
}

// mp3Decoder wraps go-mp3, which always produces 16-bit stereo.
type mp3Decoder struct {
	d   *mp3.Decoder
	buf []byte
}

func newMP3Decoder(r io.Reader) (decoder, error) {
	d, err := mp3.NewDecoder(r)
	if err != nil {
		return nil, fmt.Errorf("%w: MP3: %w", ErrUnsupportedFormat, err)
	}
	return &mp3Decoder{d: d}, nil
}

func (m *mp3Decoder) Format() audio.Format {
	return audio.Format{SampleRate: m.d.SampleRate(), Channels: 2}
}

func (m *mp3Decoder) Read(buf []float32) (int, error) {
	n := len(buf) / 2 * 2 // whole stereo frames
	if cap(m.buf) < n*2 {
		m.buf = make([]byte, n*2)
	}
	b := m.buf[:n*2]
	got, err := io.ReadAtLeast(m.d, b, 4)
	got -= got % 4
	for i := range got / 2 {
		buf[i] = audio.Int16ToFloat(int16(binary.LittleEndian.Uint16(b[i*2:])))
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		err = io.EOF
	}
	if got > 0 && errors.Is(err, io.EOF) {
		err = nil // report EOF on the next call
	}
	return got / 2, err
}

// wavDecoder reads RIFF WAVE files holding integer PCM (8, 16, 24 or 32
// bit) or 32-bit float samples.
type wavDecoder struct {
	r         io.Reader
	format    audio.Format
	bytesPer  int // per sample
	float     bool
	remaining int64 // bytes left in the data chunk; -1 means until EOF
	buf       []byte
}

const (
	wavPCM        = 1
	wavFloat      = 3
	wavExtensible = 0xFFFE
)

func newWAVDecoder(r io.Reader) (decoder, error) {
	bad := func(msg string) error { return fmt.Errorf("%w: WAV: %s", ErrUnsupportedFormat, msg) }
	var riff [12]byte
	if _, err := io.ReadFull(r, riff[:]); err != nil {
		return nil, bad("truncated header")
	}
	d := &wavDecoder{r: r}
	haveFmt := false
	for {
		var hdr [8]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return nil, bad("no data chunk")
		}
		id, size := string(hdr[:4]), binary.LittleEndian.Uint32(hdr[4:])
		switch id {
		case "fmt ":
			if size < 16 || size > 1024 {
				return nil, bad("invalid fmt chunk")
			}
			b := make([]byte, size+size%2)
			if _, err := io.ReadFull(r, b); err != nil {
				return nil, bad("truncated fmt chunk")
			}
			tag := binary.LittleEndian.Uint16(b[0:])
			channels := int(binary.LittleEndian.Uint16(b[2:]))
			rate := int(binary.LittleEndian.Uint32(b[4:]))
			bits := int(binary.LittleEndian.Uint16(b[14:]))
			if tag == wavExtensible && size >= 26 {
				tag = binary.LittleEndian.Uint16(b[24:]) // first two bytes of the subformat GUID
			}
			switch {
			case tag == wavPCM && (bits == 8 || bits == 16 || bits == 24 || bits == 32):
			case tag == wavFloat && bits == 32:
				d.float = true
			default:
				return nil, bad(fmt.Sprintf("format %#x with %d bits is not supported (use 16-bit PCM)", tag, bits))
			}
			if channels < 1 || channels > 8 || rate < 1000 || rate > 384000 {
				return nil, bad(fmt.Sprintf("%d channels at %d Hz is not supported", channels, rate))
			}
			d.format = audio.Format{SampleRate: rate, Channels: channels}
			d.bytesPer = bits / 8
			haveFmt = true
		case "data":
			if !haveFmt {
				return nil, bad("data before fmt chunk")
			}
			d.remaining = int64(size)
			if size == 0 || size == math.MaxUint32 {
				d.remaining = -1 // streamed WAV with no length
			}
			return d, nil
		default:
			if _, err := io.CopyN(io.Discard, r, int64(size)+int64(size%2)); err != nil {
				return nil, bad("truncated chunk " + id)
			}
		}
	}
}

func (d *wavDecoder) Format() audio.Format { return d.format }

func (d *wavDecoder) Read(buf []float32) (int, error) {
	frame := d.bytesPer * d.format.Channels
	frames := len(buf) / d.format.Channels
	want := int64(frames * frame)
	if d.remaining >= 0 {
		want = min(want, d.remaining-d.remaining%int64(frame))
	}
	if want <= 0 {
		return 0, io.EOF
	}
	if int64(cap(d.buf)) < want {
		d.buf = make([]byte, want)
	}
	b := d.buf[:want]
	got, err := io.ReadAtLeast(d.r, b, frame)
	got -= got % frame
	if d.remaining >= 0 {
		d.remaining -= int64(got)
	}
	n := got / d.bytesPer
	for i := range n {
		s := b[i*d.bytesPer:]
		switch {
		case d.float:
			buf[i] = audio.Clamp(math.Float32frombits(binary.LittleEndian.Uint32(s)))
		case d.bytesPer == 1:
			buf[i] = (float32(s[0]) - 128) / 128
		case d.bytesPer == 2:
			buf[i] = audio.Int16ToFloat(int16(binary.LittleEndian.Uint16(s)))
		case d.bytesPer == 3:
			v := int32(uint32(s[0])<<8|uint32(s[1])<<16|uint32(s[2])<<24) >> 8
			buf[i] = float32(v) / (1 << 23)
		default:
			buf[i] = float32(int32(binary.LittleEndian.Uint32(s))) / (1 << 31)
		}
	}
	switch {
	case n > 0:
		return n, nil
	case err == nil, errors.Is(err, io.ErrUnexpectedEOF):
		return 0, io.EOF
	}
	return 0, err
}
