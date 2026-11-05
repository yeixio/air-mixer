package matinee

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yeixio/air-mixer/internal/audio"
	"github.com/yeixio/air-mixer/internal/protocols"
	"github.com/yeixio/air-mixer/internal/protocols/httpstream"
)

// fakeMatinee is the part of Matinee's API Air-Mixer uses.
type fakeMatinee struct {
	t   *testing.T
	srv *httptest.Server

	mu         sync.Mutex
	approved   bool
	hellos     map[string][]Report // by player
	commands   map[string]chan Command
	sent       map[string][]Command // commands Air-Mixer sent to players
	progress   map[int64]int64
	stations   []Item
	stationURL map[int64]string
	audioFrom  []string // startMs asked for, per audio request
	players    []Player
}

const (
	songA   = 11 // a 0.3 s song, the first of its album
	songB   = 12 // the album's next song
	library = 5
)

func newFakeMatinee(t *testing.T) *fakeMatinee {
	f := &fakeMatinee{
		t: t, hellos: map[string][]Report{}, commands: map[string]chan Command{}, sent: map[string][]Command{},
		progress: map[int64]int64{}, stationURL: map[int64]string{},
	}
	mux := http.NewServeMux()
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer tok" {
				http.Error(w, `{"error":"sign in"}`, http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("POST /api/v1/auth/device", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]string
		json.NewDecoder(r.Body).Decode(&req)
		if req["deviceName"] != "Air-Mixer" {
			t.Errorf("device name %q", req["deviceName"])
		}
		writeJSONT(w, 200, DeviceCode{DeviceCode: "secret", UserCode: "WXYZ-2345", VerificationURL: f.srv.URL + "/link", ExpiresIn: 60, Interval: 1})
	})
	mux.HandleFunc("POST /api/v1/auth/device/token", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		ok := f.approved
		f.mu.Unlock()
		if !ok {
			writeJSONT(w, 202, map[string]string{"status": "pending"})
			return
		}
		writeJSONT(w, 200, map[string]any{"status": "approved", "token": "tok", "user": LinkUser{ID: 1, Username: "admin", IsAdmin: true}})
	})
	mux.HandleFunc("PUT /api/v1/clients/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		var rep Report
		json.NewDecoder(r.Body).Decode(&rep)
		id := r.PathValue("id")
		f.mu.Lock()
		f.hellos[id] = append(f.hellos[id], rep)
		if f.commands[id] == nil {
			f.commands[id] = make(chan Command, 8)
		}
		f.mu.Unlock()
		writeJSONT(w, 200, map[string]int{"pollSeconds": 25})
	}))
	mux.HandleFunc("DELETE /api/v1/clients/{id}", auth(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	mux.HandleFunc("GET /api/v1/clients/{id}/commands", auth(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		ch := f.commands[r.PathValue("id")]
		f.mu.Unlock()
		if ch == nil {
			http.Error(w, `{"error":"no such client"}`, 404)
			return
		}
		select {
		case c := <-ch:
			writeJSONT(w, 200, map[string]any{"commands": []Command{c}})
		case <-time.After(200 * time.Millisecond):
			writeJSONT(w, 200, map[string]any{"commands": []Command{}})
		case <-r.Context().Done():
		}
	}))
	mux.HandleFunc("POST /api/v1/clients/{id}/commands", auth(func(w http.ResponseWriter, r *http.Request) {
		var c Command
		json.NewDecoder(r.Body).Decode(&c)
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, p := range f.players {
			if p.ID == r.PathValue("id") && p.Online {
				f.sent[p.ID] = append(f.sent[p.ID], c)
				writeJSONT(w, 202, c)
				return
			}
		}
		http.Error(w, `{"error":"no such player"}`, 404)
	}))
	mux.HandleFunc("GET /api/v1/clients", auth(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		writeJSONT(w, 200, f.players)
	}))
	mux.HandleFunc("GET /api/v1/clients/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, p := range f.players {
			if p.ID == r.PathValue("id") {
				writeJSONT(w, 200, p)
				return
			}
		}
		http.Error(w, `{"error":"no such player"}`, 404)
	}))
	mux.HandleFunc("GET /api/v1/items/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		dur := int64(300)
		switch id {
		case songA:
			writeJSONT(w, 200, Item{ID: id, Kind: "track", Title: "A", DurationMs: &dur, PositionMs: 100, Playable: true})
		case songB:
			writeJSONT(w, 200, Item{ID: id, Kind: "track", Title: "B", DurationMs: &dur, Playable: true})
		default:
			f.mu.Lock()
			defer f.mu.Unlock()
			for _, st := range f.stations {
				if st.ID == id {
					writeJSONT(w, 200, st)
					return
				}
			}
			http.Error(w, `{"error":"item not found"}`, 404)
		}
	}))
	mux.HandleFunc("GET /api/v1/items/{id}/tracks", auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSONT(w, 200, []Item{{ID: songA, Kind: "track"}, {ID: songB, Kind: "track"}})
	}))
	mux.HandleFunc("PUT /api/v1/items/{id}/progress", auth(func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		var req map[string]int64
		json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		f.progress[id] = req["positionMs"]
		f.mu.Unlock()
		w.WriteHeader(204)
	}))
	// 0.3 s of audio: song A is a ramp of 1000s, song B of 2000s, so the
	// test can tell them apart.
	mux.HandleFunc("GET /api/v1/items/{id}/audio", auth(func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		f.mu.Lock()
		f.audioFrom = append(f.audioFrom, r.PathValue("id")+"@"+r.URL.Query().Get("startMs"))
		f.mu.Unlock()
		w.Header().Set("Content-Type", "audio/wav")
		w.Write(httpstream.WAVHeader(44100, 2))
		pcm := make([]byte, 44100*3/10*4)
		for i := 0; i < len(pcm); i += 2 {
			binary.LittleEndian.PutUint16(pcm[i:], uint16(int16(1000*(id-songA+1))))
		}
		w.Write(pcm)
	}))
	mux.HandleFunc("GET /api/v1/libraries", auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSONT(w, 200, []Library{{ID: 1, Name: "Movies", Kind: "movies"}, {ID: library, Name: "Music", Kind: "music"}})
	}))
	mux.HandleFunc("GET /api/v1/libraries/{id}/stations", auth(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		writeJSONT(w, 200, f.stations)
	}))
	mux.HandleFunc("POST /api/v1/stations", auth(func(w http.ResponseWriter, r *http.Request) {
		var raw map[string]any
		json.NewDecoder(r.Body).Decode(&raw)
		var req struct {
			LibraryID int64
			URL, Name string
			Home      bool
		}
		lib, _ := raw["libraryId"].(float64)
		req.LibraryID = int64(lib)
		req.URL, _ = raw["url"].(string)
		req.Name, _ = raw["name"].(string)
		req.Home, _ = raw["home"].(bool)
		if req.LibraryID != library || !req.Home {
			t.Errorf("station request: %+v", raw)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		for id, u := range f.stationURL {
			if u == req.URL {
				_ = id
				http.Error(w, `{"error":"that station is already in the library"}`, http.StatusConflict)
				return
			}
		}
		st := Item{ID: int64(100 + len(f.stations)), Kind: "station", Title: req.Name, Playable: true}
		f.stations = append(f.stations, st)
		f.stationURL[st.ID] = req.URL
		writeJSONT(w, 201, st)
	}))
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func writeJSONT(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// memSettings is an in-memory Settings.
type memSettings struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memSettings) Setting(_ context.Context, k string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[k]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}

func (s *memSettings) SetSetting(_ context.Context, k, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[k] = v
	return nil
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func signedIn(t *testing.T, f *fakeMatinee) (*Account, *memSettings) {
	t.Helper()
	settings := &memSettings{}
	a := NewAccount(context.Background(), settings, f.srv.URL, nil)
	t.Cleanup(a.Close)
	settings.SetSetting(context.Background(), keyToken, "tok")
	a.token = "tok"
	return a, settings
}

// Signing in shows a code, and the token arrives once an admin approves it.
func TestAccountLink(t *testing.T) {
	pollInterval = func(time.Duration) time.Duration { return 10 * time.Millisecond }
	t.Cleanup(func() { pollInterval = func(d time.Duration) time.Duration { return d } })
	f := newFakeMatinee(t)
	settings := &memSettings{}
	a := NewAccount(context.Background(), settings, "", nil)
	defer a.Close()
	if _, err := a.StartLink(context.Background()); err == nil {
		t.Fatal("a link without a server")
	}
	if err := a.SetURL(context.Background(), strings.TrimPrefix(f.srv.URL, "http://")+"/"); err != nil {
		t.Fatal(err)
	}
	if a.State().URL != f.srv.URL {
		t.Fatalf("url: %q", a.State().URL)
	}
	l, err := a.StartLink(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if l.UserCode != "WXYZ-2345" || a.State().Link == nil || a.SignedIn() {
		t.Fatalf("waiting: %+v %+v", l, a.State())
	}
	f.mu.Lock()
	f.approved = true
	f.mu.Unlock()
	eventually(t, "the sign-in", a.SignedIn)
	if st := a.State(); st.User != "admin" || st.Link != nil {
		t.Fatalf("signed in: %+v", st)
	}
	if v, _ := settings.Setting(context.Background(), keyToken); v != "tok" {
		t.Fatalf("saved token %q", v)
	}
	// Loading again keeps it.
	if b := NewAccount(context.Background(), settings, "http://elsewhere", nil); !b.SignedIn() || b.State().URL != f.srv.URL {
		t.Fatalf("reloaded: %+v", b.State())
	}
	// Another server signs out.
	a.SetURL(context.Background(), "http://192.0.2.1:7171")
	if a.SignedIn() {
		t.Fatal("still signed in after changing servers")
	}
}

// A Matinee input says hello as a player, plays what it's sent from where
// the person left off, goes on to the album's next song, and keeps the
// place in Matinee.
func TestSourcePlaysItems(t *testing.T) {
	f := newFakeMatinee(t)
	a, _ := signedIn(t, f)
	s := NewSource(SourceOptions{ID: "server-1", Name: "Kitchen", Account: a, Version: "test"})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	player := s.PlayerID()
	if player != "airmixer-server-1" {
		t.Fatalf("player id %q", player)
	}
	var cmds chan Command
	eventually(t, "a hello", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		cmds = f.commands[player]
		return len(f.hellos[player]) > 0
	})
	f.mu.Lock()
	hello := f.hellos[player][0]
	f.mu.Unlock()
	if hello.Name != "Kitchen" || hello.Platform != "air-mixer" || hello.Playing != nil || !contains(hello.Commands, CmdPlayItem) {
		t.Fatalf("hello: %+v", hello)
	}

	cmds <- Command{Type: CmdPlayItem, ItemID: songA, From: "Home Assistant"}
	// Song A's audio, then song B's, in the router's format.
	var levels []int16
	deadline := time.After(5 * time.Second)
	for len(levels) == 0 || levels[len(levels)-1] != 2000 {
		select {
		case p := <-s.Audio():
			if p.Format != audio.Internal {
				t.Fatalf("format %+v", p.Format)
			}
			v := audio.FloatToInt16(p.Samples[0])
			if len(levels) == 0 || levels[len(levels)-1] != v {
				levels = append(levels, v)
			}
		case <-deadline:
			t.Fatalf("audio so far: %v", levels)
		}
	}
	if len(levels) != 2 || levels[0] != 1000 {
		t.Fatalf("levels: %v", levels)
	}
	f.mu.Lock()
	from := append([]string(nil), f.audioFrom...)
	f.mu.Unlock()
	// A resumes at its saved 100 ms; B starts at the top.
	if len(from) != 2 || from[0] != "11@100" || from[1] != "12@" {
		t.Fatalf("audio asked for: %v", from)
	}
	eventually(t, "song A's place saved", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.progress[songA] >= 350
	})
	eventually(t, "a report of song B", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, h := range f.hellos[player] {
			if h.Playing != nil && h.Playing.ItemID == songB {
				return true
			}
		}
		return false
	})

	// Drain the rest; at the album's end it reports nothing playing.
	go func() {
		for range s.Audio() {
		}
	}()
	eventually(t, "the end of the album", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		h := f.hellos[player]
		return h[len(h)-1].Playing == nil && f.progress[songB] > 0
	})
}

// Pausing keeps the place, and playing again asks for audio from there.
func TestSourcePauseResume(t *testing.T) {
	f := newFakeMatinee(t)
	a, _ := signedIn(t, f)
	s := NewSource(SourceOptions{ID: "server-2", Name: "Den", Account: a})
	zero := time.Duration(0)
	if err := s.PlayItem(context.Background(), songA, &zero); err != nil {
		t.Fatal(err)
	}
	<-s.Audio()
	<-s.Audio()
	s.Pause()
	s.mu.Lock()
	pos := s.current.position()
	paused := s.current.paused
	s.mu.Unlock()
	if !paused || pos <= 0 {
		t.Fatalf("paused at %v (%v)", pos, paused)
	}
	s.Resume()
	var from []string
	eventually(t, "audio again", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		from = append([]string(nil), f.audioFrom...)
		return len(from) == 2
	})
	if from[1] != "11@"+strconv.FormatInt(pos.Milliseconds(), 10) {
		t.Fatalf("audio asked for: %v (paused at %v)", from, pos)
	}
	s.stopPlayback(true)
}

// A Matinee output adds its stream as a home station once, starts it on
// the player, serves the audio it's sent, and stops the player when it's
// removed.
func TestSpeaker(t *testing.T) {
	f := newFakeMatinee(t)
	f.players = []Player{{ID: "tv-1", Name: "Living room TV", Platform: "tvos", Online: true}}
	a, settings := signedIn(t, f)
	streams := httpstream.NewRegistry()
	sp := NewSpeaker(SpeakerOptions{
		ID: "client-tv", Name: "Living room", Player: "tv-1", Account: a, Streams: streams, Settings: settings,
		PublicURL: "http://airmixer.lan:9001/",
	})
	if err := sp.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	if len(f.stations) != 1 || f.stations[0].Title != "Air-Mixer · Living room" ||
		f.stationURL[f.stations[0].ID] != "http://airmixer.lan:9001/api/clients/client-tv/stream" {
		t.Fatalf("stations: %+v %v", f.stations, f.stationURL)
	}
	station := f.stations[0].ID
	if sent := f.sent["tv-1"]; len(sent) != 1 || sent[0].Type != CmdPlayItem || sent[0].ItemID != station {
		t.Fatalf("sent: %+v", sent)
	}
	f.mu.Unlock()

	// The stream: a WAV header, then audio in real time.
	st, ok := streams.Lookup("client-tv")
	if !ok {
		t.Fatal("stream not registered")
	}
	srv := httptest.NewServer(st)
	defer srv.Close()
	res, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	hdr := make([]byte, 44)
	if _, err := io.ReadFull(res.Body, hdr); err != nil || string(hdr[:4]) != "RIFF" || res.Header.Get("Content-Type") != "audio/wav" {
		t.Fatalf("header: % x %v", hdr, err)
	}
	eventually(t, "a listener", func() bool { return st.Listeners() == 1 })
	tone := make([]float32, 4410*2)
	for i := range tone {
		tone[i] = 0.5
	}
	if err := sp.Send(protocols.Packet{Samples: tone, Format: audio.Internal}); err != nil {
		t.Fatal(err)
	}
	// Silence (the preroll and padding), then the tone.
	buf := make([]byte, 4)
	for deadline := time.Now().Add(5 * time.Second); ; {
		if time.Now().After(deadline) {
			t.Fatal("the tone never came")
		}
		if _, err := io.ReadFull(res.Body, buf); err != nil {
			t.Fatal(err)
		}
		if int16(binary.LittleEndian.Uint16(buf)) == audio.FloatToInt16(0.5) {
			break
		}
	}

	// Connecting again finds the same station.
	f.mu.Lock()
	f.players[0].Playing = &struct {
		Item   *Item `json:"item"`
		Paused bool  `json:"paused"`
	}{Item: &Item{ID: station}}
	f.mu.Unlock()
	if err := sp.Disconnect(); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	if sent := f.sent["tv-1"]; len(sent) != 2 || sent[1].Type != CmdStop {
		t.Fatalf("after disconnect: %+v", sent)
	}
	f.mu.Unlock()
	if _, ok := streams.Lookup("client-tv"); ok {
		t.Fatal("stream still registered")
	}
	if err := sp.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer sp.Disconnect()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.stations) != 1 || len(f.sent["tv-1"]) != 3 {
		t.Fatalf("again: %d stations, sent %+v", len(f.stations), f.sent["tv-1"])
	}
}

// A player that isn't open, or no sign-in, fails Connect so the manager
// tries again later.
func TestSpeakerNotReady(t *testing.T) {
	f := newFakeMatinee(t)
	sp := NewSpeaker(SpeakerOptions{ID: "c", Name: "TV", Player: "tv-1", Account: NewAccount(context.Background(), nil, f.srv.URL, nil), Streams: httpstream.NewRegistry(), PublicURL: "http://x:1"})
	if err := sp.Connect(context.Background()); !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("not signed in: %v", err)
	}
	a, _ := signedIn(t, f)
	sp = NewSpeaker(SpeakerOptions{ID: "c", Name: "TV", Player: "tv-1", Account: a, Streams: httpstream.NewRegistry(), PublicURL: "http://x:1"})
	if err := sp.Connect(context.Background()); err == nil || !strings.Contains(err.Error(), "isn't open") {
		t.Fatalf("player closed: %v", err)
	}
	if sp.Connected() {
		t.Fatal("connected anyway")
	}
}

func TestReadWAVHeader(t *testing.T) {
	// A header with an extra chunk before the audio.
	var b []byte
	b = append(b, "RIFF\xff\xff\xff\xffWAVE"...)
	b = append(b, "LIST\x04\x00\x00\x00abcd"...)
	h := httpstream.WAVHeader(48000, 1)
	b = append(b, h[12:]...)
	f, err := readWAVHeader(strings.NewReader(string(b)))
	if err != nil || f != (wavFormat{rate: 48000, channels: 1, bits: 16}) {
		t.Fatalf("%+v %v", f, err)
	}
	if _, err := readWAVHeader(strings.NewReader("ID3 not a wav at all")); err == nil {
		t.Fatal("not a WAV")
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
