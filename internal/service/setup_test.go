package service

import (
	"context"
	"testing"

	"github.com/yeixio/air-mixer/internal/db"
	"github.com/yeixio/air-mixer/internal/router"
)

func roomSetup() Setup {
	return Setup{
		Servers: []*db.Server{{Name: "Living Room", Protocol: "fake", Enabled: true}},
		Clients: []*db.Client{
			{Name: "Left Speaker", Protocol: "fake", Enabled: true, Config: `{"delay_ms":40}`},
			{ID: "right", Name: "Right Speaker", Protocol: "fake", Enabled: true},
		},
		Routes: []router.Route{{
			Name: "Living Room", SourceName: "Living Room", DestNames: []string{"Left Speaker", "Right Speaker"},
			Volume: 1, Enabled: true,
		}},
	}
}

func TestApply(t *testing.T) {
	e := newEnv(t, "")
	ctx := context.Background()
	if err := e.svc.Apply(ctx, roomSetup()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.GetServer(ctx, "server-living-room"); err != nil {
		t.Errorf("server not created under its name: %v", err)
	}
	c, err := e.db.GetClient(ctx, "client-left-speaker")
	if err != nil || c.Config != `{"delay_ms":40}` {
		t.Errorf("client = %+v, %v", c, err)
	}
	if _, err := e.db.GetClient(ctx, "right"); err != nil {
		t.Errorf("explicit id: %v", err)
	}
	r, err := e.svc.Route("route-living-room")
	if err != nil || r.MixMode != router.MixModeReplace || len(r.DestNames) != 2 {
		t.Errorf("route = %+v, %v", r, err)
	}

	// Applying the same setup again changes nothing, so running adapters
	// are not restarted.
	before := e.factory.server("server-living-room")
	if err := e.svc.Apply(ctx, roomSetup()); err != nil {
		t.Fatal(err)
	}
	if e.factory.server("server-living-room") != before {
		t.Error("unchanged server was restarted")
	}

	// A changed setup wins over the stored one.
	changed := roomSetup()
	changed.Clients[0].Config = `{"delay_ms":80}`
	changed.Routes[0].Volume = 0.5
	if err := e.svc.Apply(ctx, changed); err != nil {
		t.Fatal(err)
	}
	if c, _ := e.db.GetClient(ctx, "client-left-speaker"); c.Config != `{"delay_ms":80}` {
		t.Errorf("client not updated: %s", c.Config)
	}
	if r, _ := e.svc.Route("route-living-room"); r.Volume != 0.5 {
		t.Errorf("route not updated: %v", r.Volume)
	}
}

func TestApplyInvalid(t *testing.T) {
	e := newEnv(t, "")
	ctx := context.Background()
	for name, s := range map[string]Setup{
		"no name":   {Servers: []*db.Server{{Protocol: "fake"}}},
		"duplicate": {Clients: []*db.Client{{Name: "A", Protocol: "fake"}, {Name: "a", Protocol: "fake"}}},
		"protocol":  {Clients: []*db.Client{{Name: "A", Protocol: "nope"}}},
		"route":     {Routes: []router.Route{{Name: "R"}}},
	} {
		if err := e.svc.Apply(ctx, s); err == nil {
			t.Errorf("%s: Apply succeeded", name)
		}
	}
	if !(Setup{}).Empty() || roomSetup().Empty() {
		t.Error("Empty")
	}
}
