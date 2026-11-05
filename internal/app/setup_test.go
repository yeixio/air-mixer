package app

import (
	"testing"

	"github.com/yeixio/air-mixer/internal/config"
)

func TestSetupFromConfig(t *testing.T) {
	off, half := false, 0.5
	s, err := setupFromConfig(config.SetupConfig{
		Servers: []config.SetupServer{{Name: "Room", Protocol: "airplay"}},
		Clients: []config.SetupClient{{Name: "L", Protocol: "pulse", Enabled: &off, Config: map[string]any{"sink": "x", "delay_ms": 40}}},
		Routes: []config.SetupRoute{
			{Name: "R", Source: "Room", Destinations: []string{"L"}},
			{Name: "Q", Source: "Room", Volume: &half},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !s.Servers[0].Enabled || s.Servers[0].Config != "" {
		t.Errorf("server = %+v", s.Servers[0])
	}
	if s.Clients[0].Enabled || s.Clients[0].Config != `{"delay_ms":40,"sink":"x"}` {
		t.Errorf("client = %+v", s.Clients[0])
	}
	if s.Routes[0].Volume != 1 || !s.Routes[0].Enabled || s.Routes[1].Volume != 0.5 {
		t.Errorf("routes = %+v", s.Routes)
	}
	if _, err := setupFromConfig(config.SetupConfig{Clients: []config.SetupClient{{Config: map[string]any{"x": make(chan int)}}}}); err == nil {
		t.Error("unencodable config accepted")
	}
}
