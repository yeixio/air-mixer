package service

import (
	"context"
	"slices"
	"testing"

	"github.com/yeixio/air-mixer/internal/db"
	"github.com/yeixio/air-mixer/internal/router"
)

// speakers adds servers and clients by name and returns the clients' IDs.
func speakers(t *testing.T, e *env, sources []string, outputs ...string) map[string]string {
	t.Helper()
	ctx := context.Background()
	for _, n := range sources {
		if err := e.svc.CreateServer(ctx, &db.Server{Name: n, Protocol: "fake", Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	ids := map[string]string{}
	for _, n := range outputs {
		c := &db.Client{Name: n, Protocol: "fake", Enabled: true}
		if err := e.svc.CreateClient(ctx, c); err != nil {
			t.Fatal(err)
		}
		ids[n] = c.ID
	}
	return ids
}

// plays maps each speaker to the sources of the stored routes it is in.
func plays(t *testing.T, e *env) map[string][]string {
	t.Helper()
	routes, err := e.db.ListRoutes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for _, r := range routes {
		for _, d := range r.DestNames {
			out[d] = append(out[d], r.SourceName)
		}
	}
	for k := range out {
		slices.Sort(out[k])
	}
	return out
}

func TestSetClientSourceGroupsAndReplaces(t *testing.T) {
	e := newEnv(t, "")
	ctx := context.Background()
	ids := speakers(t, e, []string{"Jazz FM", "Living Room"}, "Kitchen", "Den", "Patio")

	if err := e.svc.SetClientSource(ctx, ids["Kitchen"], "Jazz FM"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetClientSource(ctx, ids["Den"], "Jazz FM"); err != nil {
		t.Fatal(err)
	}
	routes, _ := e.db.ListRoutes(ctx)
	if len(routes) != 1 || routes[0].Name != "Jazz FM" || !slices.Equal(routes[0].DestNames, []string{"Kitchen", "Den"}) {
		t.Fatalf("two speakers on one source should share one route, got %+v", routes)
	}

	// Kitchen moves to Living Room: it leaves the radio route, Den stays.
	if err := e.svc.SetClientSource(ctx, ids["Kitchen"], "Living Room"); err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{"Den": {"Jazz FM"}, "Kitchen": {"Living Room"}}
	if got := plays(t, e); !mapsEqual(got, want) {
		t.Fatalf("after move got %v, want %v", got, want)
	}

	// Picking the source a speaker already plays changes nothing.
	before, _ := e.db.ListRoutes(ctx)
	if err := e.svc.SetClientSource(ctx, ids["Den"], "Jazz FM"); err != nil {
		t.Fatal(err)
	}
	if after, _ := e.db.ListRoutes(ctx); len(after) != len(before) {
		t.Fatalf("routes changed: %d -> %d", len(before), len(after))
	}

	// Nothing: Den leaves, and the radio route, now empty, is deleted.
	if err := e.svc.SetClientSource(ctx, ids["Den"], ""); err != nil {
		t.Fatal(err)
	}
	if got := plays(t, e); !mapsEqual(got, map[string][]string{"Kitchen": {"Living Room"}}) {
		t.Fatalf("after nothing got %v", got)
	}
	if n := len(e.svc.Routes()); n != 1 {
		t.Fatalf("router has %d routes, want 1", n)
	}

	if err := e.svc.SetClientSource(ctx, ids["Patio"], "Nope"); !isInvalid(err) {
		t.Fatalf("unknown source: err = %v, want invalid", err)
	}
	if err := e.svc.SetClientSource(ctx, "client-missing", ""); err == nil {
		t.Fatal("unknown speaker: want an error")
	}
}

func TestSetClientSourceEndsAMix(t *testing.T) {
	e := newEnv(t, "")
	ctx := context.Background()
	ids := speakers(t, e, []string{"Jazz FM", "Living Room"}, "Kitchen", "Patio")
	for _, r := range []router.Route{
		{Name: "Morning", SourceName: "Jazz FM", DestNames: []string{"Kitchen"}, Volume: 0.6, Enabled: true},
		{Name: "Party", SourceName: "Living Room", DestNames: []string{"Kitchen", "Patio"}, Volume: 1, Enabled: true},
	} {
		if _, err := e.svc.CreateRoute(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	// Kitchen mixes both; picking Living Room keeps Party and drops Morning.
	if err := e.svc.SetClientSource(ctx, ids["Kitchen"], "Living Room"); err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{"Kitchen": {"Living Room"}, "Patio": {"Living Room"}}
	if got := plays(t, e); !mapsEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	routes, _ := e.db.ListRoutes(ctx)
	if len(routes) != 1 || routes[0].Name != "Party" {
		t.Fatalf("want only Party left, got %+v", routes)
	}
}

func TestSetClientSourceSkipsDisabledRoutes(t *testing.T) {
	e := newEnv(t, "")
	ctx := context.Background()
	ids := speakers(t, e, []string{"Jazz FM"}, "Kitchen", "Den")
	if _, err := e.svc.CreateRoute(ctx, router.Route{Name: "Off", SourceName: "Jazz FM", DestNames: []string{"Den"}, Volume: 1}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetClientSource(ctx, ids["Kitchen"], "Jazz FM"); err != nil {
		t.Fatal(err)
	}
	for _, r := range e.svc.Routes() {
		if r.Name == "Off" && slices.Contains(r.DestNames, "Kitchen") {
			t.Fatal("joined a route that is switched off")
		}
	}
	if len(e.svc.Routes()) != 2 {
		t.Fatalf("want a new route beside the disabled one, got %+v", e.svc.Routes())
	}
}

func TestRenameKeepsRoutes(t *testing.T) {
	e := newEnv(t, "")
	ctx := context.Background()
	ids := speakers(t, e, []string{"Jazz FM"}, "Kitchen")
	if err := e.svc.SetClientSource(ctx, ids["Kitchen"], "Jazz FM"); err != nil {
		t.Fatal(err)
	}
	servers, _ := e.db.ListServers(ctx)
	srv := servers[0]
	srv.Name = "Jazz 24"
	if err := e.svc.UpdateServer(ctx, srv); err != nil {
		t.Fatal(err)
	}
	c, _ := e.db.GetClient(ctx, ids["Kitchen"])
	c.Name = "Kitchen Sonos"
	if err := e.svc.UpdateClient(ctx, c); err != nil {
		t.Fatal(err)
	}
	routes, _ := e.db.ListRoutes(ctx)
	if len(routes) != 1 || routes[0].Name != "Jazz 24" || routes[0].SourceName != "Jazz 24" ||
		!slices.Equal(routes[0].DestNames, []string{"Kitchen Sonos"}) {
		t.Fatalf("renames not carried into the route: %+v", routes)
	}
	if st := e.svc.Routes(); len(st) != 1 || st[0].SourceName != "Jazz 24" {
		t.Fatalf("router not updated: %+v", st)
	}
}

func TestClientVolume(t *testing.T) {
	e := newEnv(t, "")
	ctx := context.Background()
	ids := speakers(t, e, nil, "Kitchen")
	id := ids["Kitchen"]
	if c, _ := e.db.GetClient(ctx, id); c.Volume != 1 {
		t.Fatalf("new speaker volume = %v, want 1", c.Volume)
	}
	if err := e.svc.SetClientVolume(ctx, id, 0.4); err != nil {
		t.Fatal(err)
	}
	// An update that doesn't know about volume keeps it.
	c, _ := e.db.GetClient(ctx, id)
	c.Volume = 0
	c.Address = "elsewhere"
	if err := e.svc.UpdateClient(ctx, c); err != nil {
		t.Fatal(err)
	}
	if c, _ := e.db.GetClient(ctx, id); c.Volume != 0.4 {
		t.Fatalf("volume after update = %v, want 0.4", c.Volume)
	}
	for _, bad := range []float64{-0.1, 1.5} {
		if err := e.svc.SetClientVolume(ctx, id, bad); !isInvalid(err) {
			t.Fatalf("volume %v: err = %v, want invalid", bad, err)
		}
	}
	if err := e.svc.SetClientVolume(ctx, "client-missing", 0.5); err == nil {
		t.Fatal("unknown speaker: want an error")
	}
}

func mapsEqual(a, b map[string][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if !slices.Equal(v, b[k]) {
			return false
		}
	}
	return true
}
