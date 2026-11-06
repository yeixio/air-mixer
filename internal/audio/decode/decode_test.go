package decode

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeixio/air-mixer/internal/audio"
)

// wavFile builds a WAV file. samples are written with the given encoder.
func wavFile(tag uint16, channels, rate, bits int, data []byte, dataSize uint32, extensible bool) []byte {
	var b bytes.Buffer
	fmtLen := 16
	if extensible {
		fmtLen = 40
	}
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(4+8+fmtLen+8+len(data)))
	b.WriteString("WAVE")
	// An unknown chunk before fmt, which must be skipped.
	b.WriteString("LIST")
	binary.Write(&b, binary.LittleEndian, uint32(3))
	b.Write([]byte{1, 2, 3, 0}) // odd size plus pad byte
	b.WriteString("fmt ")
	binary.Write(&b, binary.LittleEndian, uint32(fmtLen))
	wtag := tag
	if extensible {
		wtag = wavExtensible
	}
	binary.Write(&b, binary.LittleEndian, wtag)
	binary.Write(&b, binary.LittleEndian, uint16(channels))
	binary.Write(&b, binary.LittleEndian, uint32(rate))
	binary.Write(&b, binary.LittleEndian, uint32(rate*channels*bits/8))
	binary.Write(&b, binary.LittleEndian, uint16(channels*bits/8))
	binary.Write(&b, binary.LittleEndian, uint16(bits))
	if extensible {
		binary.Write(&b, binary.LittleEndian, uint16(22))
		binary.Write(&b, binary.LittleEndian, uint16(bits))
		binary.Write(&b, binary.LittleEndian, uint32(3))
		binary.Write(&b, binary.LittleEndian, tag)
		b.Write(make([]byte, 14))
	}
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, dataSize)
	b.Write(data)
	return b.Bytes()
}

func pcm16(samples ...int16) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, samples)
	return b.Bytes()
}

func readAll(t *testing.T, d Decoder) []float32 {
	t.Helper()
	var out []float32
	buf := make([]float32, 6)
	for {
		n, err := d.Read(buf)
		out = append(out, buf[:n]...)
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func near(a, b []float32, tol float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if d := a[i] - b[i]; d > tol || d < -tol {
			return false
		}
	}
	return true
}

func TestWAVFormats(t *testing.T) {
	f32 := func(vs ...float32) []byte {
		var b bytes.Buffer
		binary.Write(&b, binary.LittleEndian, vs)
		return b.Bytes()
	}
	half16 := pcm16(16384, -16384, 0, 32767)
	tests := []struct {
		name      string
		file      []byte
		format    audio.Format
		want      []float32
		tolerance float32
	}{
		{"16-bit stereo", wavFile(wavPCM, 2, 44100, 16, half16, uint32(len(half16)), false),
			audio.Format{SampleRate: 44100, Channels: 2}, []float32{0.5, -0.5, 0, 0.99997}, 1e-4},
		{"8-bit mono", wavFile(wavPCM, 1, 8000, 8, []byte{128, 192, 64}, 3, false),
			audio.Format{SampleRate: 8000, Channels: 1}, []float32{0, 0.5, -0.5}, 1e-6},
		{"24-bit", wavFile(wavPCM, 1, 48000, 24, []byte{0, 0, 0x40, 0, 0, 0xC0}, 6, false),
			audio.Format{SampleRate: 48000, Channels: 1}, []float32{0.5, -0.5}, 1e-6},
		{"32-bit", wavFile(wavPCM, 1, 48000, 32, []byte{0, 0, 0, 0x40}, 4, false),
			audio.Format{SampleRate: 48000, Channels: 1}, []float32{0.5}, 1e-6},
		{"float extensible", wavFile(wavFloat, 2, 22050, 32, f32(0.25, -2), 8, true),
			audio.Format{SampleRate: 22050, Channels: 2}, []float32{0.25, -1}, 1e-6},
		{"streamed without length", wavFile(wavPCM, 2, 44100, 16, half16, math.MaxUint32, false),
			audio.Format{SampleRate: 44100, Channels: 2}, []float32{0.5, -0.5, 0, 0.99997}, 1e-4},
		{"truncated last frame", wavFile(wavPCM, 2, 44100, 16, half16[:6], uint32(len(half16)), false),
			audio.Format{SampleRate: 44100, Channels: 2}, []float32{0.5, -0.5}, 1e-4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := New(bytes.NewReader(tt.file), "")
			if err != nil {
				t.Fatal(err)
			}
			if d.Format() != tt.format {
				t.Errorf("format = %+v, want %+v", d.Format(), tt.format)
			}
			if got := readAll(t, d); !near(got, tt.want, tt.tolerance) {
				t.Errorf("samples = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWAVRejectsUnsupported(t *testing.T) {
	for name, file := range map[string][]byte{
		"adpcm":     wavFile(2, 1, 8000, 4, []byte{0}, 1, false),
		"truncated": wavFile(wavPCM, 1, 8000, 16, nil, 0, false)[:20],
		"no data":   wavFile(wavPCM, 1, 8000, 16, nil, 0, false)[:44],
	} {
		if _, err := New(bytes.NewReader(file), ""); !errors.Is(err, ErrUnsupportedFormat) {
			t.Errorf("%s: err = %v, want ErrUnsupportedFormat", name, err)
		}
	}
}

func TestDetectsUnsupportedFormats(t *testing.T) {
	saved := FFmpeg
	FFmpeg = ""
	t.Cleanup(func() { FFmpeg = saved })
	for name, data := range map[string]string{
		"flac":  "fLaC\x00\x00\x00\x22",
		"ogg":   "OggS\x00\x02",
		"html":  "<html><body>not audio</body></html>",
		"empty": "",
	} {
		if _, err := New(bytes.NewReader([]byte(data)), "text/html"); !errors.Is(err, ErrUnsupportedFormat) {
			t.Errorf("%s: err = %v, want ErrUnsupportedFormat", name, err)
		}
	}
}

func TestMP3(t *testing.T) {
	f, err := os.Open("testdata/tone.mp3")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d, err := New(f, "audio/mpeg")
	if err != nil {
		t.Fatal(err)
	}
	if want := (audio.Format{SampleRate: 22050, Channels: 2}); d.Format() != want {
		t.Errorf("format = %+v, want %+v", d.Format(), want)
	}
	samples := readAll(t, d)
	seconds := float64(len(samples)/2) / 22050
	if seconds < 0.45 || seconds > 0.65 {
		t.Errorf("decoded %.3f s, want about 0.5 s", seconds)
	}
	var peak float32
	for _, s := range samples {
		peak = max(peak, s)
	}
	if peak < 0.2 {
		t.Errorf("peak %v: decoded audio is silent", peak)
	}
}

// TestFFmpeg decodes a FLAC file, which needs ffmpeg.
func TestFFmpeg(t *testing.T) {
	if FFmpeg == "" {
		t.Skip("ffmpeg is not installed")
	}
	dir := t.TempDir()
	flac := filepath.Join(dir, "tone.flac")
	cmd := exec.Command(FFmpeg, "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=0.5:sample_rate=48000", "-ac", "1", flac)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make FLAC: %v: %s", err, out)
	}
	f, err := os.Open(flac)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d, err := New(f, "")
	if err != nil {
		t.Fatal(err)
	}
	defer d.(io.Closer).Close()
	if got := d.Format(); got != (audio.Format{SampleRate: audio.SampleRate, Channels: 2}) {
		t.Fatalf("format = %+v", got)
	}
	samples := readAll(t, d)
	// Half a second at 44.1 kHz stereo, give or take the resampler.
	if n := len(samples); n < 43000 || n > 45200 {
		t.Fatalf("decoded %d samples, want about 44100", n)
	}
	var peak float32
	for _, v := range samples {
		peak = max(peak, v)
	}
	if peak < 0.05 { // ffmpeg's sine is at 1/8
		t.Errorf("peak = %v, want a tone", peak)
	}
}

func TestFFmpegStopsEarly(t *testing.T) {
	if FFmpeg == "" {
		t.Skip("ffmpeg is not installed")
	}
	d, err := OpenURL(context.Background(), "anullsrc=r=44100:cl=stereo")
	if err != nil {
		t.Fatal(err)
	}
	// anullsrc isn't a URL ffmpeg opens without -f lavfi, so this fails
	// at the first read; closing must not hang.
	buf := make([]float32, 1024)
	_, _ = d.Read(buf)
	done := make(chan struct{})
	go func() { d.(io.Closer).Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close hung")
	}
}
