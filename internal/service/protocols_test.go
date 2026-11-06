package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/yeixio/air-mixer/internal/db"
	"github.com/yeixio/air-mixer/internal/protocols/httpstream"
	"github.com/yeixio/air-mixer/internal/protocols/matinee"
	"github.com/yeixio/air-mixer/internal/protocols/pulse"
	"github.com/yeixio/air-mixer/internal/protocols/radio"
	"github.com/yeixio/air-mixer/internal/protocols/snapcast"
)

// sample fills in every required field of p with a valid value.
func sample(p Protocol) (address string, config string) {
	cfg := map[string]any{}
	for _, f := range p.Fields {
		if !f.Required {
			continue
		}
		v := "10.0.0.2:7000"
		if f.Type == "url" {
			v = "http://radio.example/stream.mp3"
		}
		if f.Key == "address" {
			address = v
		} else {
			cfg[f.Key] = v
		}
	}
	b, _ := json.Marshal(cfg)
	return address, string(b)
}

// TestCatalogMatchesFactory checks that every protocol in the catalog
// builds, and that the factory needs what the catalog says it does.
func TestCatalogMatchesFactory(t *testing.T) {
	store, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	account := matinee.NewAccount(context.Background(), store, "", quiet)
	defer account.Close()
	f := NewFactory(Defaults{AirPlayPort: 5000, Streams: httpstream.NewRegistry(), Matinee: account, Settings: store}, quiet)

	for _, p := range Protocols() {
		address, config := sample(p)
		var name string
		var err error
		if p.Kind == "server" {
			var s interface{ Protocol() string }
			s, err = f.Server(&db.Server{ID: "x", Name: "X", Protocol: p.Name, Config: config})
			if err == nil {
				name = s.Protocol()
			}
		} else {
			var c interface{ Protocol() string }
			c, err = f.Client(&db.Client{ID: "x", Name: "X", Protocol: p.Name, Address: address, Config: config})
			if err == nil {
				name = c.Protocol()
			}
		}
		if err != nil {
			t.Errorf("%s %s: %v", p.Kind, p.Name, err)
			continue
		}
		if name != p.Name {
			t.Errorf("%s %s builds an adapter for %q", p.Kind, p.Name, name)
		}
		for _, fl := range p.Fields {
			if fl.Required && p.Kind == "client" && fl.Key == "address" && p.Name != "matinee" {
				if _, err := f.Client(&db.Client{Protocol: p.Name}); !isInvalid(err) {
					t.Errorf("%s client without address = %v", p.Name, err)
				}
			}
		}
	}
}

// TestCatalogTranslations checks that every key the catalog names is in
// the English web UI text.
func TestCatalogTranslations(t *testing.T) {
	b, err := os.ReadFile("../../i18n/locales/en/web.json")
	if err != nil {
		t.Fatal(err)
	}
	var web map[string]map[string]string
	if err := json.Unmarshal(b, &web); err != nil {
		t.Fatal(err)
	}
	has := func(key string) bool {
		for group, keys := range web {
			for k := range keys {
				if group+"."+k == key {
					return true
				}
			}
		}
		return false
	}
	for _, p := range Protocols() {
		keys := []string{p.Description}
		for _, f := range p.Fields {
			keys = append(keys, f.Label, f.Hint, f.PlaceholderKey)
		}
		for _, k := range keys {
			if k != "" && !has(k) {
				t.Errorf("%s %s: %q is not in en/web.json", p.Kind, p.Name, k)
			}
		}
	}
}

func TestNewProtocols(t *testing.T) {
	f := NewFactory(Defaults{}, quiet)
	if s, err := f.Server(&db.Server{ID: "r", Name: "R", Protocol: "radio", Config: `{"url":"https://radio.example/live.aac"}`}); err != nil {
		t.Error(err)
	} else if _, ok := s.(*radio.Server); !ok {
		t.Errorf("radio is %T", s)
	}
	for _, bad := range []string{``, `{"url":"ftp://x/y"}`, `{"url":"not a url"}`, `{"url":"http://"}`} {
		if _, err := f.Server(&db.Server{Protocol: "radio", Config: bad}); !isInvalid(err) {
			t.Errorf("radio %s = %v", bad, err)
		}
	}
	if s, err := f.Server(&db.Server{ID: "p", Name: "P", Protocol: "pulse", Config: `{"source":"11:22:33:44:55:66"}`}); err != nil {
		t.Error(err)
	} else if _, ok := s.(*pulse.Source); !ok {
		t.Errorf("pulse input is %T", s)
	}
	if c, err := f.Client(&db.Client{ID: "s", Name: "S", Protocol: "snapcast", Address: "snapserver"}); err != nil {
		t.Error(err)
	} else if sc, ok := c.(*snapcast.Client); !ok || sc.Address() != "snapserver:4953" {
		t.Errorf("snapcast is %T", c)
	}
	if _, err := f.Client(&db.Client{Protocol: "snapcast", Address: "s", Config: `{"sample_rate":1}`}); !isInvalid(err) {
		t.Errorf("bad sample_rate = %v", err)
	}
}
