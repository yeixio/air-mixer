package api

import (
	"bytes"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yeixio/air-mixer/internal/playback"
	"github.com/yeixio/air-mixer/internal/router"
)

// silentWAV returns d of 16-bit stereo 44.1 kHz silence.
func silentWAV(d time.Duration) []byte {
	data := make([]byte, int(d.Seconds()*44100)*4)
	var b bytes.Buffer
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36+len(data)))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(2), uint32(44100), uint32(44100 * 4), uint16(4), uint16(16)} {
		binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(len(data)))
	b.Write(data)
	return b.Bytes()
}

func audioServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/chime.wav", func(w http.ResponseWriter, r *http.Request) { w.Write(silentWAV(50 * time.Millisecond)) })
	mux.HandleFunc("/alarm.wav", func(w http.ResponseWriter, r *http.Request) { w.Write(silentWAV(10 * time.Second)) })
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func TestPlay(t *testing.T) {
	a := newTestAPI(t, nil)
	srv := audioServer(t)
	a.expect("POST", "/api/play", `{"url":"`+srv.URL+`/chime.wav"}`, 400, nil) // no outputs yet
	a.expect("POST", "/api/clients", `{"id":"client-k","name":"Kitchen","protocol":"fake"}`, 201, nil)
	a.expect("POST", "/api/clients", `{"id":"client-d","name":"Den","protocol":"fake"}`, 201, nil)
	a.expect("POST", "/api/clients", `{"id":"client-off","name":"Garage","protocol":"fake","enabled":false}`, 201, nil)

	// By ID, waiting for the end.
	var it playback.Item
	a.expect("POST", "/api/play", `{"url":"`+srv.URL+`/chime.wav","outputs":["client-k"],"wait":true,"title":"Ding"}`, 200, &it)
	if it.State != playback.StateFinished || len(it.Outputs) != 1 || it.Outputs[0] != "Kitchen" || it.Title != "Ding" {
		t.Errorf("finished item = %+v", it)
	}

	// Every enabled output by default, without waiting.
	a.expect("POST", "/api/play", `{"url":"`+srv.URL+`/alarm.wav","mode":"interrupt","loop":true}`, 202, &it)
	if it.State != playback.StatePlaying || len(it.Outputs) != 2 || it.Mode != playback.ModeInterrupt {
		t.Errorf("started item = %+v", it)
	}
	var list playbackList
	a.expect("GET", "/api/playback", "", 200, &list)
	if len(list.Playback) != 1 || list.Playback[0].ID != it.ID {
		t.Errorf("playback list = %+v", list)
	}
	a.expect("GET", "/api/playback/"+it.ID, "", 200, nil)

	// The playback shows up as a transient route that cannot be edited.
	var rt router.RouteStatus
	a.expect("GET", "/api/routes/"+it.ID, "", 200, &rt)
	if !rt.Transient || rt.Duck == nil || *rt.Duck != 0 {
		t.Errorf("playback route = %+v", rt)
	}
	a.expect("PUT", "/api/routes/"+it.ID, `{"volume":0.5}`, 400, nil)

	// Stop it on one output by name, ignoring case, then everywhere.
	var stopped map[string]int
	a.expect("DELETE", "/api/playback?output=kitchen", "", 200, &stopped)
	if stopped["stopped"] != 1 {
		t.Errorf("stopped = %v", stopped)
	}
	a.expect("GET", "/api/playback/"+it.ID, "", 200, &it)
	if len(it.Outputs) != 1 || it.Outputs[0] != "Den" {
		t.Errorf("after stopping the kitchen: %+v", it.Outputs)
	}
	a.expect("DELETE", "/api/playback/"+it.ID, "", 204, nil)
	a.expect("DELETE", "/api/playback/"+it.ID, "", 404, nil)
	a.expect("DELETE", "/api/playback", "", 200, &stopped)
	if stopped["stopped"] != 0 {
		t.Errorf("stop all = %v", stopped)
	}
	a.expect("GET", "/api/routes/"+it.ID, "", 404, nil)
}

func TestPlayErrors(t *testing.T) {
	a := newTestAPI(t, nil)
	srv := audioServer(t)
	a.expect("POST", "/api/clients", `{"id":"client-k","name":"Kitchen","protocol":"fake"}`, 201, nil)
	a.expect("POST", "/api/play", `{"url":"`+srv.URL+`/chime.wav","outputs":["Nowhere"]}`, 400, nil)
	a.expect("POST", "/api/play", `{"url":"ftp://example.com/a.mp3","outputs":["Kitchen"]}`, 400, nil)
	a.expect("POST", "/api/play", `{"url":"`+srv.URL+`/chime.wav","outputs":["Kitchen"],"mode":"shout"}`, 400, nil)
	a.expect("POST", "/api/play", `{"url":"`+srv.URL+`/missing.mp3","outputs":["Kitchen"]}`, 502, nil)
	a.expect("POST", "/api/play", `not json`, 400, nil)
	a.expect("DELETE", "/api/playback?output=Nowhere", "", 400, nil)
	a.expect("GET", "/api/playback/nope", "", 404, nil)
}

func TestPlaybackDisabled(t *testing.T) {
	h := New(Options{Logger: quiet})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/playback", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}
