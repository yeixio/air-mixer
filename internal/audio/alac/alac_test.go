package alac

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
)

type fixture struct {
	cfg    Config
	frames [][]byte
	pcm    []int32 // at the stream's bit depth
}

func loadFixture(t *testing.T, name string) fixture {
	t.Helper()
	read := func(ext string) []byte {
		b, err := os.ReadFile(filepath.Join("testdata", name+ext))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	cfg, err := ParseMagicCookie(read(".cookie"))
	if err != nil {
		t.Fatal(err)
	}
	var f fixture
	f.cfg = cfg
	for b := read(".frames"); len(b) > 0; {
		n := binary.BigEndian.Uint32(b)
		f.frames = append(f.frames, b[4:4+n])
		b = b[4+n:]
	}
	raw := read(".pcm")
	if cfg.BitDepth == 16 {
		for i := 0; i < len(raw); i += 2 {
			f.pcm = append(f.pcm, int32(int16(binary.LittleEndian.Uint16(raw[i:]))))
		}
	} else {
		for i := 0; i < len(raw); i += 4 {
			f.pcm = append(f.pcm, int32(binary.LittleEndian.Uint32(raw[i:]))>>(32-cfg.BitDepth))
		}
	}
	return f
}

// TestDecodeMatchesFFmpeg decodes frames produced by ffmpeg's ALAC encoder,
// which uses compression, stereo decorrelation and (for 24-bit) uncompressed
// low bytes, and checks every sample against ffmpeg's own decode.
func TestDecodeMatchesFFmpeg(t *testing.T) {
	for _, name := range []string{"stereo16", "mono16", "stereo24"} {
		t.Run(name, func(t *testing.T) {
			f := loadFixture(t, name)
			d, err := NewDecoder(f.cfg)
			if err != nil {
				t.Fatal(err)
			}
			var got []int32
			for i, frame := range f.frames {
				pcm, err := d.Decode(frame)
				if err != nil {
					t.Fatalf("frame %d: %v", i, err)
				}
				got = append(got, pcm...)
			}
			if len(got) != len(f.pcm) {
				t.Fatalf("decoded %d samples, want %d", len(got), len(f.pcm))
			}
			for i := range got {
				if got[i] != f.pcm[i] {
					t.Fatalf("sample %d = %d, want %d", i, got[i], f.pcm[i])
				}
			}
		})
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	for _, channels := range []uint8{1, 2} {
		cfg := DefaultConfig()
		cfg.NumChannels = channels
		enc, err := NewEncoder(cfg)
		if err != nil {
			t.Fatal(err)
		}
		dec, err := NewDecoder(cfg)
		if err != nil {
			t.Fatal(err)
		}
		// A full frame, then a short final frame that carries its size.
		for _, n := range []int{int(cfg.FrameLength), 100} {
			pcm := make([]int16, n*int(channels))
			for i := range pcm {
				pcm[i] = int16(rand.IntN(1<<16) - 1<<15)
			}
			pcm[0], pcm[len(pcm)-1] = math.MinInt16, math.MaxInt16

			frame, err := enc.Encode(pcm)
			if err != nil {
				t.Fatal(err)
			}
			got, err := dec.Decode(frame)
			if err != nil {
				t.Fatalf("channels=%d n=%d: %v", channels, n, err)
			}
			if len(got) != len(pcm) {
				t.Fatalf("channels=%d n=%d: got %d samples, want %d", channels, n, len(got), len(pcm))
			}
			for i := range pcm {
				if got[i] != int32(pcm[i]) {
					t.Fatalf("channels=%d n=%d: sample %d = %d, want %d", channels, n, i, got[i], pcm[i])
				}
			}
		}
	}
}

func TestEncodeRejectsBadInput(t *testing.T) {
	enc, err := NewEncoder(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	for name, pcm := range map[string][]int16{
		"empty":       nil,
		"odd":         make([]int16, 3),
		"over length": make([]int16, 2*353),
	} {
		if _, err := enc.Encode(pcm); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	cfg := DefaultConfig()
	cfg.BitDepth = 24
	if _, err := NewEncoder(cfg); err == nil {
		t.Error("expected 24-bit encoder to be rejected")
	}
}

func TestDecodeFloat(t *testing.T) {
	cfg := DefaultConfig()
	enc, _ := NewEncoder(cfg)
	dec, _ := NewDecoder(cfg)
	frame, err := enc.Encode([]int16{0, 16384, -16384, -32768, 32767, 32767})
	if err != nil {
		t.Fatal(err)
	}
	got, err := dec.DecodeFloat(frame)
	if err != nil {
		t.Fatal(err)
	}
	want := []float32{0, 0.5, -0.5, -1, 32767.0 / 32768, 32767.0 / 32768}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sample %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestDecodeErrors(t *testing.T) {
	dec, _ := NewDecoder(DefaultConfig())
	enc, _ := NewEncoder(DefaultConfig())
	frame, _ := enc.Encode(make([]int16, 2*352))

	cases := map[string][]byte{
		"empty":       {},
		"truncated":   frame[:len(frame)/2],
		"bad element": {0xE0, 0, 0, 0},
	}
	oversize := bytes.Clone(frame[:3])
	oversize[2] |= 0x10 // set the has-size flag
	oversize = append(oversize, 0xFF, 0xFF, 0xFF, 0xFF)
	cases["oversize"] = oversize

	for name, b := range cases {
		if _, err := dec.Decode(b); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

// FuzzDecode makes sure malformed frames return errors instead of
// panicking; receivers decode whatever arrives on the network.
func FuzzDecode(f *testing.F) {
	enc, _ := NewEncoder(DefaultConfig())
	frame, _ := enc.Encode(make([]int16, 2*352))
	f.Add(frame)
	fx, err := os.ReadFile(filepath.Join("testdata", "stereo16.frames"))
	if err == nil && len(fx) > 4 {
		f.Add(fx[4 : 4+binary.BigEndian.Uint32(fx)])
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		dec, _ := NewDecoder(DefaultConfig())
		_, _ = dec.Decode(b)
	})
}

func TestConfigFormats(t *testing.T) {
	cfg := DefaultConfig()
	if got := cfg.Fmtp(); got != "96 352 0 16 40 10 14 2 255 0 0 44100" {
		t.Errorf("Fmtp() = %q", got)
	}
	parsed, err := ParseFmtp(cfg.Fmtp())
	if err != nil || parsed != cfg {
		t.Errorf("ParseFmtp round trip = %+v, %v", parsed, err)
	}
	parsed, err = ParseMagicCookie(cfg.MagicCookie())
	if err != nil || parsed != cfg {
		t.Errorf("ParseMagicCookie round trip = %+v, %v", parsed, err)
	}

	for _, bad := range []string{"", "96 352", "96 x 0 16 40 10 14 2 255 0 0 44100", "96 352 0 12 40 10 14 2 255 0 0 44100"} {
		if _, err := ParseFmtp(bad); err == nil {
			t.Errorf("ParseFmtp(%q): expected error", bad)
		}
	}
	if _, err := ParseMagicCookie(make([]byte, 10)); err == nil {
		t.Error("expected short cookie to be rejected")
	}
}

func TestBitWriterReader(t *testing.T) {
	w := &bitWriter{}
	fields := []struct {
		v uint32
		n int
	}{{5, 3}, {0, 4}, {0xABC, 12}, {1, 1}, {0xDEADBEEF, 32}, {3, 2}}
	for _, f := range fields {
		w.write(f.v, f.n)
	}
	r := &bitReader{buf: w.buf}
	for _, f := range fields {
		if got := r.read(f.n); got != f.v {
			t.Errorf("read(%d) = %#x, want %#x", f.n, got, f.v)
		}
	}
	if r.err != nil {
		t.Fatal(r.err)
	}
	r.read(8)
	if r.err == nil {
		t.Error("expected error reading past end")
	}
}
