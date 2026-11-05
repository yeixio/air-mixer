package app

import (
	"encoding/json"
	"fmt"

	"github.com/yeixio/air-mixer/internal/config"
	"github.com/yeixio/air-mixer/internal/db"
	"github.com/yeixio/air-mixer/internal/router"
	"github.com/yeixio/air-mixer/internal/service"
)

// setupFromConfig converts the config file's setup section to a
// service.Setup.
func setupFromConfig(c config.SetupConfig) (service.Setup, error) {
	var out service.Setup
	enabled := func(b *bool) bool { return b == nil || *b }
	for _, v := range c.Servers {
		cfg, err := configJSON(v.Config)
		if err != nil {
			return out, fmt.Errorf("setup: server %q: %w", v.Name, err)
		}
		out.Servers = append(out.Servers, &db.Server{
			ID: v.ID, Name: v.Name, Protocol: v.Protocol, Enabled: enabled(v.Enabled), Config: cfg,
		})
	}
	for _, v := range c.Clients {
		cfg, err := configJSON(v.Config)
		if err != nil {
			return out, fmt.Errorf("setup: client %q: %w", v.Name, err)
		}
		out.Clients = append(out.Clients, &db.Client{
			ID: v.ID, Name: v.Name, Protocol: v.Protocol, Address: v.Address, Enabled: enabled(v.Enabled), Config: cfg,
		})
	}
	for _, v := range c.Routes {
		volume := 1.0
		if v.Volume != nil {
			volume = *v.Volume
		}
		out.Routes = append(out.Routes, router.Route{
			ID: v.ID, Name: v.Name, SourceName: v.Source, DestNames: v.Destinations,
			MixMode: router.MixMode(v.MixMode), Volume: float32(volume), Enabled: enabled(v.Enabled), Priority: v.Priority,
		})
	}
	return out, nil
}

// configJSON encodes an adapter's config map the way the API stores it.
// Keys are sorted, so the same map always gives the same string and
// unchanged items are not restarted.
func configJSON(m map[string]any) (string, error) {
	if len(m) == 0 {
		return "", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("config: %w", err)
	}
	return string(b), nil
}
