package db

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func openTest(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestServersCRUD(t *testing.T) {
	ctx := context.Background()
	db := openTest(t)

	s := &Server{ID: "s1", Name: "Living Room", Protocol: "airplay", Enabled: true}
	if err := db.CreateServer(ctx, s); err != nil {
		t.Fatal(err)
	}
	if s.CreatedAt.IsZero() {
		t.Error("CreatedAt not set")
	}
	if err := db.CreateServer(ctx, s); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate create = %v, want ErrConflict", err)
	}

	got, err := db.GetServer(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Living Room" || !got.Enabled || !got.CreatedAt.Equal(s.CreatedAt) {
		t.Errorf("GetServer = %+v", got)
	}

	s.Name, s.Enabled, s.Config = "Den", false, `{"port":5001}`
	if err := db.UpdateServer(ctx, s); err != nil {
		t.Fatal(err)
	}
	list, err := db.ListServers(ctx)
	if err != nil || len(list) != 1 || list[0].Name != "Den" || list[0].Enabled || list[0].Config != `{"port":5001}` {
		t.Errorf("ListServers = %+v, %v", list, err)
	}

	if err := db.DeleteServer(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetServer(ctx, "s1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetServer after delete = %v", err)
	}
	if err := db.DeleteServer(ctx, "s1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete = %v", err)
	}
	if err := db.UpdateServer(ctx, s); !errors.Is(err, ErrNotFound) {
		t.Errorf("update missing = %v", err)
	}
}

func TestClientsCRUD(t *testing.T) {
	ctx := context.Background()
	db := openTest(t)
	if list, _ := db.ListClients(ctx); list == nil || len(list) != 0 {
		t.Errorf("empty ListClients = %#v, want empty slice", list)
	}

	c := &Client{ID: "c1", Name: "Kitchen", Protocol: "airplay", Address: "10.0.0.5:5000", Enabled: true}
	if err := db.CreateClient(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateClient(ctx, c); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate create = %v", err)
	}
	c.Address = "10.0.0.6"
	if err := db.UpdateClient(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetClient(ctx, "c1")
	if err != nil || got.Address != "10.0.0.6" {
		t.Errorf("GetClient = %+v, %v", got, err)
	}
	if err := db.DeleteClient(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteClient(ctx, "c1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete = %v", err)
	}
	if err := db.UpdateClient(ctx, c); !errors.Is(err, ErrNotFound) {
		t.Errorf("update missing = %v", err)
	}
	if _, err := db.GetClient(ctx, "c1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("get missing = %v", err)
	}
}

func TestRoutesCRUD(t *testing.T) {
	ctx := context.Background()
	db := openTest(t)
	low := &Route{ID: "r1", Name: "low", SourceName: "in", DestNames: []string{"Kitchen, left", "Patio"}, MixMode: "mix", Volume: 0.5, Enabled: true}
	high := &Route{ID: "r2", Name: "high", SourceName: "in", MixMode: "mix", Volume: 1, Priority: 10}
	for _, r := range []*Route{low, high} {
		if err := db.CreateRoute(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.CreateRoute(ctx, low); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate create = %v", err)
	}

	list, err := db.ListRoutes(ctx)
	if err != nil || len(list) != 2 || list[0].ID != "r2" {
		t.Fatalf("ListRoutes = %+v, %v", list, err)
	}
	// Names may contain commas now that the list is stored as JSON.
	if got := list[1].DestNames; len(got) != 2 || got[0] != "Kitchen, left" {
		t.Errorf("DestNames = %q", got)
	}
	if list[0].DestNames == nil {
		t.Error("nil DestNames should read back as empty")
	}

	low.DestNames = []string{"Den"}
	low.Volume = 0.25
	if err := db.UpdateRoute(ctx, low); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetRoute(ctx, "r1")
	if err != nil || got.Volume != 0.25 || len(got.DestNames) != 1 || got.DestNames[0] != "Den" {
		t.Errorf("GetRoute = %+v, %v", got, err)
	}
	if err := db.DeleteRoute(ctx, "r1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetRoute(ctx, "r1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("get after delete = %v", err)
	}
	if err := db.UpdateRoute(ctx, low); !errors.Is(err, ErrNotFound) {
		t.Errorf("update missing = %v", err)
	}
}

func TestSettings(t *testing.T) {
	ctx := context.Background()
	db := openTest(t)
	if _, err := db.Setting(ctx, "k"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing setting = %v", err)
	}
	db.SetSetting(ctx, "k", "a")
	db.SetSetting(ctx, "k", "b")
	if v, err := db.Setting(ctx, "k"); err != nil || v != "b" {
		t.Errorf("Setting = %q, %v", v, err)
	}
}

// TestMigratesLegacyDatabase builds a database the way the pre-2026 code
// did, with routes that point at server and client IDs, and checks that
// opening it converts them to names.
func TestMigratesLegacyDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		`CREATE TABLE servers (id TEXT PRIMARY KEY, name TEXT NOT NULL, protocol TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, config TEXT, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE clients (id TEXT PRIMARY KEY, name TEXT NOT NULL, protocol TEXT NOT NULL, address TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, config TEXT, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE routes (id TEXT PRIMARY KEY, name TEXT NOT NULL, source_id TEXT NOT NULL, dest_ids TEXT NOT NULL, mix_mode TEXT NOT NULL DEFAULT 'replace', volume REAL NOT NULL DEFAULT 1.0, enabled INTEGER NOT NULL DEFAULT 1, priority INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`INSERT INTO servers (id, name, protocol, config) VALUES ('s1', 'Living Room', 'airplay', NULL)`,
		`INSERT INTO clients (id, name, protocol, address) VALUES ('c1', 'Kitchen', 'airplay', '10.0.0.5')`,
		`INSERT INTO routes (id, name, source_id, dest_ids) VALUES ('r1', 'main', 's1', 'c1, gone')`,
	} {
		if _, err := raw.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	raw.Close()

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r, err := db.GetRoute(ctx, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if r.SourceName != "Living Room" || len(r.DestNames) != 2 || r.DestNames[0] != "Kitchen" || r.DestNames[1] != "gone" {
		t.Errorf("migrated route = %+v", r)
	}
	// A NULL config column reads as an empty string.
	if s, err := db.GetServer(ctx, "s1"); err != nil || s.Config != "" {
		t.Errorf("server = %+v, %v", s, err)
	}
	db.Close()

	// Reopening is a no-op.
	db2, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	db2.Close()
}

func TestRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.db")
	raw, _ := sql.Open("sqlite", path)
	raw.Exec(`PRAGMA user_version = 99`)
	raw.Close()
	if _, err := Open(context.Background(), path); err == nil {
		t.Error("expected error opening a newer schema")
	}
}
