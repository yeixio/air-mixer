package upnp

import (
	"context"
	"encoding/xml"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yeixio/air-mixer/internal/audio"
	"github.com/yeixio/air-mixer/internal/protocols"
	"github.com/yeixio/air-mixer/internal/protocols/httpstream"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func startRenderer(t *testing.T) *Server {
	t.Helper()
	s := NewServer(ServerOptions{ID: "den", Name: "Den & Co", Address: "127.0.0.1:0", Logger: quiet, NoSSDP: true})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Stop() })
	return s
}

func TestRendererDescriptionAndSCPD(t *testing.T) {
	s := startRenderer(t)
	dev, err := fetchDescription(context.Background(), http.DefaultClient, s.URL())
	if err != nil {
		t.Fatal(err)
	}
	if dev.Name != "Den & Co" || !strings.HasSuffix(dev.AVTransport, "/AVTransport/control") || dev.RenderingControl == "" {
		t.Errorf("description %+v", dev)
	}
	for _, svc := range services {
		res, err := http.Get(strings.TrimSuffix(s.URL(), "description.xml") + svc.Path + "/scpd.xml")
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Actions []string `xml:"actionList>action>name"`
		}
		err = xml.NewDecoder(res.Body).Decode(&doc)
		res.Body.Close()
		if err != nil || len(doc.Actions) != len(svc.Actions) {
			t.Errorf("%s SCPD: %v, %d actions", svc.Path, err, len(doc.Actions))
		}
	}
	if s.uuid != stableUUID("air-mixer-renderer:den") || len(s.uuid) != 36 {
		t.Errorf("uuid %q", s.uuid)
	}
}

func TestRendererControl(t *testing.T) {
	s := startRenderer(t)
	dev, err := fetchDescription(context.Background(), http.DefaultClient, s.URL())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	c := http.DefaultClient
	if _, err := call(ctx, c, dev.AVTransport, avTransport, "Play", []arg{{"InstanceID", "0"}, {"Speed", "1"}}); err == nil || !strings.Contains(err.Error(), "701") {
		t.Errorf("Play without media = %v", err)
	}
	if _, err := call(ctx, c, dev.AVTransport, avTransport, "SetAVTransportURI", []arg{{"InstanceID", "0"}, {"CurrentURI", "file:///etc/passwd"}, {"CurrentURIMetaData", ""}}); err == nil {
		t.Error("file URI accepted")
	}
	if _, err := call(ctx, c, dev.RenderingControl, renderingControl, "SetVolume", []arg{{"InstanceID", "0"}, {"Channel", "Master"}, {"DesiredVolume", "40"}}); err != nil {
		t.Fatal(err)
	}
	out, err := call(ctx, c, dev.RenderingControl, renderingControl, "GetVolume", []arg{{"InstanceID", "0"}, {"Channel", "Master"}})
	if err != nil || out["CurrentVolume"] != "40" {
		t.Errorf("GetVolume = %v, %v", out, err)
	}
	if _, err := call(ctx, c, dev.RenderingControl, renderingControl, "SetVolume", []arg{{"InstanceID", "0"}, {"Channel", "Master"}, {"DesiredVolume", "400"}}); err == nil {
		t.Error("volume 400 accepted")
	}
	out, err = call(ctx, c, dev.AVTransport, avTransport, "GetTransportInfo", []arg{{"InstanceID", "0"}})
	if err != nil || out["CurrentTransportState"] != stateNoMedia {
		t.Errorf("GetTransportInfo = %v, %v", out, err)
	}
	if _, err := call(ctx, c, dev.AVTransport, avTransport, "Bogus", nil); err == nil {
		t.Error("unknown action accepted")
	}
}

func TestGENA(t *testing.T) {
	s := startRenderer(t)
	got := make(chan string, 4)
	cb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.Method == "NOTIFY" && r.Header.Get("Sid") != "" {
			got <- string(b)
		}
	}))
	defer cb.Close()
	req, _ := http.NewRequest("SUBSCRIBE", strings.TrimSuffix(s.URL(), "description.xml")+"RenderingControl/event", nil)
	req.Header.Set("Callback", "<"+cb.URL+"/>")
	req.Header.Set("NT", "upnp:event")
	req.Header.Set("Timeout", "Second-300")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.Header.Get("Sid") == "" || res.Header.Get("Timeout") != "Second-300" {
		t.Fatalf("SUBSCRIBE answer %v %v", res.Status, res.Header)
	}
	select {
	case body := <-got:
		if !strings.Contains(body, "Volume channel=&#34;Master&#34; val=&#34;100&#34;") && !strings.Contains(body, `Volume channel=&quot;Master&quot; val=&quot;100&quot;`) {
			t.Errorf("initial event %s", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no initial event")
	}
	s.handle(renderingControl, "SetMute", map[string]string{"DesiredMute": "1"})
	select {
	case body := <-got:
		if !strings.Contains(body, "Mute channel") {
			t.Errorf("event %s", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no change event")
	}
}

// TestOutputPlaysIntoRenderer runs the whole loop: an Air-Mixer UPnP
// output tells Air-Mixer's own renderer input to play its stream.
func TestOutputPlaysIntoRenderer(t *testing.T) {
	pi, it, rg := pollInterval, idleStop, resumeGap
	pollInterval, idleStop, resumeGap = 50*time.Millisecond, 400*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { pollInterval, idleStop, resumeGap = pi, it, rg })

	renderer := startRenderer(t)
	streams := httpstream.NewRegistry()
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/clients/"), "/stream")
		st, ok := streams.Lookup(id)
		if !ok {
			http.NotFound(w, r)
			return
		}
		st.ServeHTTP(w, r)
	}))
	defer web.Close()

	out := NewClient(ClientOptions{ID: "tv", Name: "TV", Address: renderer.URL(), Format: "wav", Streams: streams, PublicURL: web.URL, Logger: quiet})
	if err := out.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer out.Disconnect()

	var mu sync.Mutex
	var peak float32
	stopRead := make(chan struct{})
	defer close(stopRead)
	go func() {
		for {
			select {
			case <-stopRead:
				return
			case p := <-renderer.Audio():
				mu.Lock()
				for _, v := range p.Samples {
					peak = max(peak, v)
				}
				mu.Unlock()
			}
		}
	}()

	// Keep sending a tone; the output starts the renderer, which pulls
	// the stream.
	stopSend := make(chan struct{})
	go func() {
		tone := make([]float32, audio.SampleRate/50*2)
		for i := range tone {
			tone[i] = 0.5
		}
		tk := time.NewTicker(20 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-stopSend:
				return
			case <-tk.C:
				out.Send(protocols.Packet{Samples: tone, Format: audio.Internal})
			}
		}
	}()
	waitFor(t, "audio through the renderer", func() bool { mu.Lock(); defer mu.Unlock(); return peak > 0.45 })
	if !out.Playing() {
		t.Error("output doesn't think it plays")
	}
	close(stopSend)
	// Silence stops the renderer.
	waitFor(t, "idle stop", func() bool {
		renderer.mu.Lock()
		defer renderer.mu.Unlock()
		return renderer.state == stateStopped
	})
}

func TestDescriptionURL(t *testing.T) {
	for in, want := range map[string]string{
		"192.168.1.50":                   "http://192.168.1.50:1400/xml/device_description.xml",
		"192.168.1.50:1400":              "http://192.168.1.50:1400/xml/device_description.xml",
		"http://10.0.0.5:49152/desc.xml": "http://10.0.0.5:49152/desc.xml",
	} {
		if got := DescriptionURL(in); got != want {
			t.Errorf("%s: %s", in, got)
		}
	}
	if got := trackDuration(`<res duration="0:03:25.000">x</res>`); got != "0:03:25" {
		t.Errorf("trackDuration %q", got)
	}
}
