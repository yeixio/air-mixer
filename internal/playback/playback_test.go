package playback

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yeixio/air-mixer/internal/router"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// constWAV returns a 16-bit stereo 44.1 kHz WAV of the given length at a
// constant level.
func constWAV(d time.Duration, level int16) []byte {
	frames := int(d.Seconds() * 44100)
	s := make([]int16, frames*2)
	for i := range s {
		s[i] = level
	}
	data := pcm16(s...)
	return wavFile(wavPCM, 2, 44100, 16, data, uint32(len(data)), false)
}

type fixture struct {
	router  *router.Router
	player  *Player
	server  *httptest.Server
	kitchen chan []float32
	patio   chan []float32
	fetches atomic.Int32
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{router: router.New(quiet)}
	f.router.Start()
	t.Cleanup(f.router.Stop)
	f.kitchen = make(chan []float32, 1024)
	f.patio = make(chan []float32, 1024)
	f.router.RegisterSink("c1", "kitchen", f.kitchen)
	f.router.RegisterSink("c2", "patio", f.patio)

	mux := http.NewServeMux()
	mux.HandleFunc("/short.wav", func(w http.ResponseWriter, r *http.Request) {
		f.fetches.Add(1)
		w.Header().Set("Content-Type", "audio/wav")
		w.Write(constWAV(100*time.Millisecond, 16384))
	})
	mux.HandleFunc("/long.wav", func(w http.ResponseWriter, r *http.Request) {
		w.Write(constWAV(5*time.Second, 16384))
	})
	mux.HandleFunc("/tone.mp3", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "testdata/tone.mp3")
	})
	mux.HandleFunc("/page.html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<html>hello</html>")
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)

	f.player = New(f.router, Options{Logger: quiet})
	t.Cleanup(f.player.Close)
	return f
}

func (f *fixture) url(path string) string { return f.server.URL + path }

func wait(t *testing.T, done <-chan struct{}, d time.Duration) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatal("playback did not end in time")
	}
}

// drain returns the number of samples queued on ch and their peak.
func drain(ch chan []float32) (n int, peak float32) {
	for {
		select {
		case b := <-ch:
			n += len(b)
			for _, s := range b {
				peak = max(peak, s)
			}
		default:
			return n, peak
		}
	}
}

func TestPlayDucksAndCleansUp(t *testing.T) {
	f := newFixture(t)
	vol := float32(0.5)
	h, err := f.player.Play(context.Background(), Request{
		URL: f.url("/short.wav"), Outputs: []string{"kitchen", "kitchen"}, Volume: &vol, Title: "Dinner is ready",
	})
	if err != nil {
		t.Fatal(err)
	}
	it, done := h.Item(), h.Done()
	if it.State != StatePlaying || it.Mode != ModeDuck || len(it.Outputs) != 1 || it.DuckLevel == nil || *it.DuckLevel != DefaultDuckLevel {
		t.Errorf("item = %+v", it)
	}
	st, err := f.router.Route(it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Transient || st.Duck == nil || st.Priority != priority || !st.Active {
		t.Errorf("route = %+v, want an active transient ducking route", st)
	}
	if got := f.player.List(); len(got) != 1 || got[0].ID != it.ID {
		t.Errorf("List = %+v", got)
	}

	wait(t, done, 3*time.Second)
	n, peak := drain(f.kitchen)
	if want := 4410 * 2; n < want-200 || n > want+200 {
		t.Errorf("kitchen got %d samples, want about %d", n, want)
	}
	if peak < 0.24 || peak > 0.26 {
		t.Errorf("peak = %v, want 0.25 (0.5 at volume 0.5)", peak)
	}
	if n, _ := drain(f.patio); n != 0 {
		t.Errorf("patio got %d samples", n)
	}
	if _, err := f.router.Route(it.ID); !errors.Is(err, router.ErrNotFound) {
		t.Errorf("route still present after playback: %v", err)
	}
	if got := f.player.List(); len(got) != 0 {
		t.Errorf("List after end = %+v", got)
	}
}

func TestPlayMP3PlaysInRealTime(t *testing.T) {
	f := newFixture(t)
	start := time.Now()
	h, err := f.player.Play(context.Background(), Request{URL: f.url("/tone.mp3"), Outputs: []string{"patio"}, Mode: ModeMix})
	if err != nil {
		t.Fatal(err)
	}
	done := h.Done()
	wait(t, done, 3*time.Second)
	if took := time.Since(start); took < 400*time.Millisecond {
		t.Errorf("half a second of audio took %v; it was not paced", took)
	}
	n, _ := drain(f.patio)
	if seconds := float64(n/2) / 44100; seconds < 0.45 || seconds > 0.65 {
		t.Errorf("patio got %.3f s, want about 0.5 s resampled to 44.1 kHz", seconds)
	}
}

func TestRepeat(t *testing.T) {
	f := newFixture(t)
	h, err := f.player.Play(context.Background(), Request{URL: f.url("/short.wav"), Outputs: []string{"kitchen"}, Repeat: 3})
	if err != nil {
		t.Fatal(err)
	}
	it, done := h.Item(), h.Done()
	if it.Repeat != 3 {
		t.Errorf("repeat = %d", it.Repeat)
	}
	wait(t, done, 3*time.Second)
	if got := f.fetches.Load(); got != 3 {
		t.Errorf("fetched %d times, want 3", got)
	}
	if n, _ := drain(f.kitchen); n < 3*8820-600 {
		t.Errorf("kitchen got %d samples, want about %d", n, 3*8820)
	}
}

func TestStop(t *testing.T) {
	f := newFixture(t)
	h, err := f.player.Play(context.Background(), Request{URL: f.url("/short.wav"), Outputs: []string{"kitchen"}, Loop: true, Mode: ModeInterrupt})
	if err != nil {
		t.Fatal(err)
	}
	it, done := h.Item(), h.Done()
	if st, _ := f.router.Route(it.ID); st.Duck == nil || *st.Duck != 0 {
		t.Errorf("interrupt route duck = %v, want 0", st.Duck)
	}
	time.Sleep(150 * time.Millisecond)
	if err := f.player.Stop(it.ID); err != nil {
		t.Fatal(err)
	}
	wait(t, done, time.Second)
	if err := f.player.Stop(it.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Stop = %v", err)
	}
	if _, err := f.player.Get(it.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Stop = %v", err)
	}
}

func TestStopOutputKeepsOtherOutputs(t *testing.T) {
	f := newFixture(t)
	h, err := f.player.Play(context.Background(), Request{URL: f.url("/long.wav"), Outputs: []string{"kitchen", "patio"}})
	if err != nil {
		t.Fatal(err)
	}
	it, done := h.Item(), h.Done()
	if n := f.player.StopOutput("kitchen"); n != 1 {
		t.Errorf("StopOutput changed %d playbacks, want 1", n)
	}
	got, err := f.player.Get(it.ID)
	if err != nil || len(got.Outputs) != 1 || got.Outputs[0] != "patio" {
		t.Errorf("after StopOutput: %+v, %v", got, err)
	}
	if st, _ := f.router.Route(it.ID); len(st.DestNames) != 1 || st.DestNames[0] != "patio" {
		t.Errorf("route dests = %v", st.DestNames)
	}
	if n := f.player.StopOutput("patio"); n != 1 {
		t.Errorf("StopOutput(patio) = %d", n)
	}
	wait(t, done, time.Second)
	if n := f.player.StopAll(); n != 0 {
		t.Errorf("StopAll stopped %d, want none left", n)
	}
}

func TestMaxDuration(t *testing.T) {
	f := newFixture(t)
	start := time.Now()
	h, err := f.player.Play(context.Background(), Request{URL: f.url("/long.wav"), Outputs: []string{"kitchen"}, MaxDuration: 0.2})
	if err != nil {
		t.Fatal(err)
	}
	done := h.Done()
	wait(t, done, 2*time.Second)
	if took := time.Since(start); took > time.Second {
		t.Errorf("took %v with max_duration 0.2 s", took)
	}
}

func TestPlayErrors(t *testing.T) {
	f := newFixture(t)
	bad := float32(2)
	tests := []struct {
		name string
		req  Request
		want any
	}{
		{"no url", Request{Outputs: []string{"kitchen"}}, &InvalidError{}},
		{"file url", Request{URL: "file:///etc/passwd", Outputs: []string{"kitchen"}}, &InvalidError{}},
		{"no outputs", Request{URL: f.url("/short.wav")}, &InvalidError{}},
		{"bad volume", Request{URL: f.url("/short.wav"), Outputs: []string{"kitchen"}, Volume: &bad}, &InvalidError{}},
		{"bad mode", Request{URL: f.url("/short.wav"), Outputs: []string{"kitchen"}, Mode: "loud"}, &InvalidError{}},
		{"not found", Request{URL: f.url("/missing.wav"), Outputs: []string{"kitchen"}}, &FetchError{}},
		{"not audio", Request{URL: f.url("/page.html"), Outputs: []string{"kitchen"}}, &FetchError{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := f.player.Play(context.Background(), tt.req)
			switch tt.want.(type) {
			case *InvalidError:
				var e *InvalidError
				if !errors.As(err, &e) {
					t.Errorf("err = %v, want InvalidError", err)
				}
			case *FetchError:
				var e *FetchError
				if !errors.As(err, &e) {
					t.Errorf("err = %v, want FetchError", err)
				}
			}
		})
	}
	if got := f.player.List(); len(got) != 0 {
		t.Errorf("failed requests left playbacks: %+v", got)
	}
	if got := f.router.Routes(); len(got) != 0 {
		t.Errorf("failed requests left routes: %+v", got)
	}
}

func TestBusyAndClosed(t *testing.T) {
	f := newFixture(t)
	p := New(f.router, Options{Logger: quiet, MaxActive: 1})
	req := Request{URL: f.url("/long.wav"), Outputs: []string{"kitchen"}}
	if _, err := p.Play(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Play(context.Background(), req); !errors.Is(err, ErrBusy) {
		t.Errorf("second Play = %v, want ErrBusy", err)
	}
	p.Close()
	if _, err := p.Play(context.Background(), req); !errors.Is(err, ErrClosed) {
		t.Errorf("Play after Close = %v, want ErrClosed", err)
	}
	if got := f.router.Routes(); len(got) != 0 {
		t.Errorf("Close left routes: %+v", got)
	}
}

func TestRedact(t *testing.T) {
	if got := redact("http://user:pw@ha.local:8123/api/tts_proxy/x.mp3?authSig=secret#f"); got != "http://ha.local:8123/api/tts_proxy/x.mp3" {
		t.Errorf("redact = %q", got)
	}
}
