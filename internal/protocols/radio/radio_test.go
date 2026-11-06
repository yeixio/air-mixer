package radio

import (
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yeixio/air-mixer/internal/audio"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// wav is 0.1 s of 16-bit stereo at 44.1 kHz at a constant level.
func wav() []byte {
	frames := 4410
	h := make([]byte, 44+frames*4)
	copy(h, "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(len(h)-8))
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], 2)
	binary.LittleEndian.PutUint32(h[24:], 44100)
	binary.LittleEndian.PutUint32(h[28:], 44100*4)
	binary.LittleEndian.PutUint16(h[32:], 4)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], uint32(frames*4))
	for i := 44; i < len(h); i += 2 {
		binary.LittleEndian.PutUint16(h[i:], 8192)
	}
	return h
}

func shorten(t *testing.T) {
	lo, hi := minBackoff, maxBackoff
	minBackoff, maxBackoff = 10*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { minBackoff, maxBackoff = lo, hi })
}

func TestPlaysAndRepeatsThroughPlaylist(t *testing.T) {
	shorten(t)
	var fetches atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/station.pls", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/x-scpls")
		io.WriteString(w, "[playlist]\nNumberOfEntries=1\nFile1=/live?token=secret\nTitle1=Test\n")
	})
	mux.HandleFunc("/live", func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		w.Header().Set("Content-Type", "audio/wav")
		w.Write(wav())
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	s := New(Options{ID: "r", Name: "Radio", URL: ts.URL + "/station.pls", Logger: quiet})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	var samples int
	deadline := time.After(5 * time.Second)
	for fetches.Load() < 2 {
		select {
		case p := <-s.Audio():
			if p.Format != audio.Internal {
				t.Fatalf("format %+v", p.Format)
			}
			if p.Samples[0] < 0.24 || p.Samples[0] > 0.26 {
				t.Fatalf("sample %v, want 0.25", p.Samples[0])
			}
			samples += len(p.Samples)
		case <-deadline:
			t.Fatalf("file played %d times, err %v", fetches.Load(), s.Err())
		}
	}
	if samples < 4410*2 {
		t.Errorf("got %d samples before the repeat", samples)
	}
}

func TestReportsFailures(t *testing.T) {
	shorten(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer ts.Close()
	s := New(Options{ID: "r", URL: ts.URL + "/x.mp3", Logger: quiet})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for s.Err() == nil {
		if time.Now().After(deadline) {
			t.Fatal("no error reported")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestStartRejectsBadURL(t *testing.T) {
	if err := New(Options{URL: "not a url"}).Start(context.Background()); err == nil {
		t.Error("no error")
	}
}

func TestFirstEntry(t *testing.T) {
	for body, want := range map[string]string{
		"#EXTM3U\n#EXTINF:-1,Station\nhttp://a.example/stream\n": "http://a.example/stream",
		"[playlist]\nFile1=http://b.example/x?y=1\n":             "http://b.example/x?y=1",
		"relative.mp3\n": "http://host.example/dir/relative.mp3",
	} {
		got, err := firstEntry("http://host.example/dir/list.m3u", body)
		if err != nil || got != want {
			t.Errorf("%q: %q, %v; want %q", body, got, err, want)
		}
	}
	if _, err := firstEntry("http://h/", "#EXTM3U\n"); err == nil {
		t.Error("empty playlist: no error")
	}
}

func TestRedact(t *testing.T) {
	if got := redact("http://user:pw@h.example/s?token=x"); got != "http://h.example/s?…" {
		t.Errorf("redact = %q", got)
	}
}
