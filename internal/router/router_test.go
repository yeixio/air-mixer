package router

import (
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func newTestRouter(t *testing.T) *Router {
	t.Helper()
	r := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.Start()
	t.Cleanup(r.Stop)
	return r
}

func route(id, src string, dests ...string) Route {
	return Route{ID: id, Name: id, SourceName: src, DestNames: dests, Volume: 1, Enabled: true}
}

// sinkFor registers a sink and returns its channel.
func sinkFor(r *Router, id, name string) chan []float32 {
	ch := make(chan []float32, 64)
	r.RegisterSink(id, name, ch)
	return ch
}

func sourceFor(r *Router, id, name string) chan []float32 {
	ch := make(chan []float32, 64)
	r.RegisterSource(id, name, ch)
	return ch
}

func recv(t *testing.T, ch <-chan []float32) []float32 {
	t.Helper()
	select {
	case b := <-ch:
		return b
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for audio")
		return nil
	}
}

func silent(t *testing.T, ch <-chan []float32) {
	t.Helper()
	select {
	case b := <-ch:
		t.Fatalf("unexpected audio %v", b)
	case <-time.After(50 * time.Millisecond):
	}
}

func approx(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if d := a[i] - b[i]; d > 1e-6 || d < -1e-6 {
			return false
		}
	}
	return true
}

func TestRouteFansOutToEveryDestination(t *testing.T) {
	r := newTestRouter(t)
	src := sourceFor(r, "s1", "Living Room In")
	k1 := sinkFor(r, "c1", "Kitchen")
	k2 := sinkFor(r, "c2", "Patio")
	if err := r.AddRoute(route("r1", "Living Room In", "Kitchen", "Patio")); err != nil {
		t.Fatal(err)
	}

	for i := range 5 {
		buf := []float32{float32(i) / 10, 0}
		src <- buf
		// Every destination gets every buffer, not a share of them.
		if got := recv(t, k1); !approx(got, buf) {
			t.Fatalf("kitchen buffer %d = %v, want %v", i, got, buf)
		}
		if got := recv(t, k2); !approx(got, buf) {
			t.Fatalf("patio buffer %d = %v, want %v", i, got, buf)
		}
	}
}

func TestRoutesActivateInAnyRegistrationOrder(t *testing.T) {
	r := newTestRouter(t)
	if err := r.AddRoute(route("r1", "src", "dst")); err != nil {
		t.Fatal(err)
	}
	if st, _ := r.Route("r1"); st.Active || st.SourceAvailable {
		t.Fatalf("route active with nothing registered: %+v", st)
	}

	out := sinkFor(r, "c1", "dst")
	if st, _ := r.Route("r1"); st.Active {
		t.Fatal("route active without a source")
	}
	in := sourceFor(r, "s1", "src")
	st, _ := r.Route("r1")
	if !st.Active || !st.SourceAvailable || len(st.ActiveDests) != 1 || st.ActiveDests[0] != "dst" {
		t.Fatalf("route not active after registration: %+v", st)
	}
	in <- []float32{0.5, 0.5}
	recv(t, out)
	if st, _ := r.Route("r1"); st.SamplesRouted != 2 {
		t.Errorf("SamplesRouted = %d, want 2", st.SamplesRouted)
	}

	// Source goes away and comes back under a new ID.
	r.UnregisterSource("s1")
	if st, _ := r.Route("r1"); st.Active {
		t.Fatal("route still active after source unregistered")
	}
	in2 := sourceFor(r, "s2", "src")
	in2 <- []float32{0.25, 0.25}
	if got := recv(t, out); !approx(got, []float32{0.25, 0.25}) {
		t.Errorf("got %v from re-registered source", got)
	}
}

func TestRoutesMixIntoSharedDestination(t *testing.T) {
	r := newTestRouter(t)
	a := sourceFor(r, "s1", "a")
	b := sourceFor(r, "s2", "b")
	out := sinkFor(r, "c1", "speaker")
	ra := route("ra", "a", "speaker")
	rb := route("rb", "b", "speaker")
	rb.Volume = 0.5
	r.AddRoute(ra)
	r.AddRoute(rb)

	a <- []float32{0.2, 0.2}
	b <- []float32{0.4, 0.4}
	if got := recv(t, out); !approx(got, []float32{0.4, 0.4}) {
		t.Errorf("mixed = %v, want [0.4 0.4]", got)
	}
}

func TestUpdateRouteChangesVolumeAndTargets(t *testing.T) {
	r := newTestRouter(t)
	in := sourceFor(r, "s1", "src")
	k1 := sinkFor(r, "c1", "one")
	k2 := sinkFor(r, "c2", "two")
	rt := route("r1", "src", "one")
	r.AddRoute(rt)

	// Updating an active route used to deadlock the old router.
	done := make(chan error, 1)
	go func() {
		rt.Volume = 0.5
		rt.DestNames = []string{"two"}
		done <- r.UpdateRoute(rt)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("UpdateRoute deadlocked")
	}

	in <- []float32{0.8, 0.8}
	if got := recv(t, k2); !approx(got, []float32{0.4, 0.4}) {
		t.Errorf("got %v, want [0.4 0.4]", got)
	}
	silent(t, k1)

	rt.Volume = 1
	r.UpdateRoute(rt)
	in <- []float32{0.8, 0.8}
	if got := recv(t, k2); !approx(got, []float32{0.8, 0.8}) {
		t.Errorf("after volume change got %v", got)
	}
}

func TestDisabledRoutesAndStop(t *testing.T) {
	r := newTestRouter(t)
	in := sourceFor(r, "s1", "src")
	out := sinkFor(r, "c1", "dst")
	rt := route("r1", "src", "dst")
	rt.Enabled = false
	r.AddRoute(rt)
	in <- []float32{1, 1}
	silent(t, out)

	rt.Enabled = true
	r.UpdateRoute(rt)
	in <- []float32{1, 1}
	recv(t, out)

	r.Stop()
	if r.Running() || r.ActiveRoutes() != 0 {
		t.Fatal("routes still active after Stop")
	}
	in <- []float32{1, 1}
	silent(t, out)
	r.Start()
	if r.ActiveRoutes() != 1 {
		t.Fatal("route not reactivated after Start")
	}
}

func TestRemoveRouteAndSink(t *testing.T) {
	r := newTestRouter(t)
	in := sourceFor(r, "s1", "src")
	out := sinkFor(r, "c1", "dst")
	r.AddRoute(route("r1", "src", "dst"))

	r.UnregisterSink("c1")
	// After UnregisterSink returns the channel may be closed safely.
	close(out)
	in <- []float32{1, 1}
	time.Sleep(20 * time.Millisecond)

	if err := r.RemoveRoute("r1"); err != nil {
		t.Fatal(err)
	}
	if err := r.RemoveRoute("r1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second RemoveRoute = %v", err)
	}
	if len(r.Routes()) != 0 {
		t.Error("route still listed")
	}
}

func TestRouteValidationAndErrors(t *testing.T) {
	r := newTestRouter(t)
	bad := []Route{
		{},
		{ID: "x", DestNames: []string{"d"}},
		{ID: "x", SourceName: "s"},
		{ID: "x", SourceName: "s", DestNames: []string{"d"}, Volume: 2},
		{ID: "x", SourceName: "s", DestNames: []string{""}},
		{ID: "x", SourceName: "s", DestNames: []string{"d"}, MixMode: "bogus"},
	}
	for _, rt := range bad {
		var ve *ValidationError
		if err := r.AddRoute(rt); !errors.As(err, &ve) {
			t.Errorf("AddRoute(%+v) = %v, want ValidationError", rt, err)
		}
	}
	r.AddRoute(route("r1", "s", "d"))
	if err := r.AddRoute(route("r1", "s", "d")); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate AddRoute = %v", err)
	}
	if err := r.UpdateRoute(route("nope", "s", "d")); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateRoute missing = %v", err)
	}
	if _, err := r.Route("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Route missing = %v", err)
	}
}

func TestRoutesSortedByPriority(t *testing.T) {
	r := newTestRouter(t)
	for i, p := range []int{1, 5, 3} {
		rt := route(string(rune('a'+i)), "s", "d")
		rt.Priority = p
		r.AddRoute(rt)
	}
	got := r.Routes()
	if got[0].Priority != 5 || got[1].Priority != 3 || got[2].Priority != 1 {
		t.Errorf("order = %d %d %d", got[0].Priority, got[1].Priority, got[2].Priority)
	}
	// Mutating a returned route must not change the router.
	got[0].DestNames[0] = "changed"
	if again, _ := r.Route(got[0].ID); again.DestNames[0] != "d" {
		t.Error("Routes returned shared slices")
	}
}

func TestSourceEndingClosesCleanly(t *testing.T) {
	r := newTestRouter(t)
	in := sourceFor(r, "s1", "src")
	out := sinkFor(r, "c1", "dst")
	r.AddRoute(route("r1", "src", "dst"))
	close(in)
	silent(t, out)
	r.UnregisterSource("s1")
	r.UnregisterSink("c1")
}

func TestConcurrentChurn(t *testing.T) {
	r := newTestRouter(t)
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Go(func() {
			name := string(rune('a' + w))
			for i := range 50 {
				in := sourceFor(r, name+"-src", name)
				out := sinkFor(r, name+"-dst", name+"-out")
				rt := route(name, name, name+"-out")
				r.AddRoute(rt)
				in <- []float32{0.1, 0.1}
				rt.Volume = float32(i%10) / 10
				r.UpdateRoute(rt)
				_ = r.Routes()
				r.UnregisterSink(name + "-dst")
				close(out)
				r.UnregisterSource(name + "-src")
				r.RemoveRoute(name)
			}
		})
	}
	wg.Wait()
}
