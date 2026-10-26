package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Server is an audio input: a receiver that devices play to.
type Server struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Protocol  string    `json:"protocol"`
	Enabled   bool      `json:"enabled"`
	Config    string    `json:"config"` // protocol-specific JSON
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Client is an audio output: a speaker Air-Mixer plays to.
type Client struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Protocol  string    `json:"protocol"`
	Address   string    `json:"address"`
	Enabled   bool      `json:"enabled"`
	Config    string    `json:"config"` // protocol-specific JSON
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Route connects a server to clients, by name.
type Route struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	SourceName string    `json:"source_name"`
	DestNames  []string  `json:"dest_names"`
	MixMode    string    `json:"mix_mode"`
	Volume     float64   `json:"volume"`
	Enabled    bool      `json:"enabled"`
	Priority   int       `json:"priority"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func now() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }

type scanner interface{ Scan(...any) error }

// Servers

const serverCols = `id, name, protocol, enabled, COALESCE(config, ''), created_at, updated_at`

func scanServer(s scanner) (*Server, error) {
	v := &Server{}
	err := s.Scan(&v.ID, &v.Name, &v.Protocol, &v.Enabled, &v.Config, &v.CreatedAt, &v.UpdatedAt)
	return v, err
}

// ListServers returns all servers, oldest first.
func (db *DB) ListServers(ctx context.Context) ([]*Server, error) {
	rows, err := db.conn.QueryContext(ctx, `SELECT `+serverCols+` FROM servers ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Server{}
	for rows.Next() {
		v, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// GetServer returns one server.
func (db *DB) GetServer(ctx context.Context, id string) (*Server, error) {
	v, err := scanServer(db.conn.QueryRowContext(ctx, `SELECT `+serverCols+` FROM servers WHERE id = ?`, id))
	return v, notFound(err)
}

// CreateServer inserts a server and, on success, sets its timestamps.
func (db *DB) CreateServer(ctx context.Context, v *Server) error {
	ts := now()
	_, err := db.conn.ExecContext(ctx,
		`INSERT INTO servers (id, name, protocol, enabled, config, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		v.ID, v.Name, v.Protocol, v.Enabled, v.Config, ts, ts)
	if isConstraint(err) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	v.CreatedAt, v.UpdatedAt = ts, ts
	return nil
}

// UpdateServer saves every field except the creation time.
func (db *DB) UpdateServer(ctx context.Context, v *Server) error {
	v.UpdatedAt = now()
	return affectedOne(db.conn.ExecContext(ctx,
		`UPDATE servers SET name = ?, protocol = ?, enabled = ?, config = ?, updated_at = ? WHERE id = ?`,
		v.Name, v.Protocol, v.Enabled, v.Config, v.UpdatedAt, v.ID))
}

// DeleteServer removes a server.
func (db *DB) DeleteServer(ctx context.Context, id string) error {
	return affectedOne(db.conn.ExecContext(ctx, `DELETE FROM servers WHERE id = ?`, id))
}

// Clients

const clientCols = `id, name, protocol, address, enabled, COALESCE(config, ''), created_at, updated_at`

func scanClient(s scanner) (*Client, error) {
	v := &Client{}
	err := s.Scan(&v.ID, &v.Name, &v.Protocol, &v.Address, &v.Enabled, &v.Config, &v.CreatedAt, &v.UpdatedAt)
	return v, err
}

// ListClients returns all clients, oldest first.
func (db *DB) ListClients(ctx context.Context) ([]*Client, error) {
	rows, err := db.conn.QueryContext(ctx, `SELECT `+clientCols+` FROM clients ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Client{}
	for rows.Next() {
		v, err := scanClient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// GetClient returns one client.
func (db *DB) GetClient(ctx context.Context, id string) (*Client, error) {
	v, err := scanClient(db.conn.QueryRowContext(ctx, `SELECT `+clientCols+` FROM clients WHERE id = ?`, id))
	return v, notFound(err)
}

// CreateClient inserts a client and sets its timestamps.
func (db *DB) CreateClient(ctx context.Context, v *Client) error {
	ts := now()
	_, err := db.conn.ExecContext(ctx,
		`INSERT INTO clients (id, name, protocol, address, enabled, config, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		v.ID, v.Name, v.Protocol, v.Address, v.Enabled, v.Config, ts, ts)
	if isConstraint(err) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	v.CreatedAt, v.UpdatedAt = ts, ts
	return nil
}

// UpdateClient saves every field except the creation time.
func (db *DB) UpdateClient(ctx context.Context, v *Client) error {
	v.UpdatedAt = now()
	return affectedOne(db.conn.ExecContext(ctx,
		`UPDATE clients SET name = ?, protocol = ?, address = ?, enabled = ?, config = ?, updated_at = ? WHERE id = ?`,
		v.Name, v.Protocol, v.Address, v.Enabled, v.Config, v.UpdatedAt, v.ID))
}

// DeleteClient removes a client.
func (db *DB) DeleteClient(ctx context.Context, id string) error {
	return affectedOne(db.conn.ExecContext(ctx, `DELETE FROM clients WHERE id = ?`, id))
}

// Routes

const routeCols = `id, name, source_name, dest_names, mix_mode, volume, enabled, priority, created_at, updated_at`

func scanRoute(s scanner) (*Route, error) {
	v := &Route{}
	var dests string
	err := s.Scan(&v.ID, &v.Name, &v.SourceName, &dests, &v.MixMode, &v.Volume, &v.Enabled, &v.Priority, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(dests), &v.DestNames); err != nil {
		return nil, fmt.Errorf("route %s: bad dest_names: %w", v.ID, err)
	}
	return v, nil
}

func encodeNames(names []string) (string, error) {
	if names == nil {
		names = []string{}
	}
	b, err := json.Marshal(names)
	return string(b), err
}

// ListRoutes returns all routes, highest priority first.
func (db *DB) ListRoutes(ctx context.Context) ([]*Route, error) {
	rows, err := db.conn.QueryContext(ctx, `SELECT `+routeCols+` FROM routes ORDER BY priority DESC, created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Route{}
	for rows.Next() {
		v, err := scanRoute(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// GetRoute returns one route.
func (db *DB) GetRoute(ctx context.Context, id string) (*Route, error) {
	v, err := scanRoute(db.conn.QueryRowContext(ctx, `SELECT `+routeCols+` FROM routes WHERE id = ?`, id))
	return v, notFound(err)
}

// CreateRoute inserts a route and sets its timestamps.
func (db *DB) CreateRoute(ctx context.Context, v *Route) error {
	dests, err := encodeNames(v.DestNames)
	if err != nil {
		return err
	}
	ts := now()
	_, err = db.conn.ExecContext(ctx,
		`INSERT INTO routes (`+routeCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		v.ID, v.Name, v.SourceName, dests, v.MixMode, v.Volume, v.Enabled, v.Priority, ts, ts)
	if isConstraint(err) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	v.CreatedAt, v.UpdatedAt = ts, ts
	return nil
}

// UpdateRoute saves every field except the creation time.
func (db *DB) UpdateRoute(ctx context.Context, v *Route) error {
	dests, err := encodeNames(v.DestNames)
	if err != nil {
		return err
	}
	v.UpdatedAt = now()
	return affectedOne(db.conn.ExecContext(ctx,
		`UPDATE routes SET name = ?, source_name = ?, dest_names = ?, mix_mode = ?, volume = ?, enabled = ?, priority = ?, updated_at = ? WHERE id = ?`,
		v.Name, v.SourceName, dests, v.MixMode, v.Volume, v.Enabled, v.Priority, v.UpdatedAt, v.ID))
}

// DeleteRoute removes a route.
func (db *DB) DeleteRoute(ctx context.Context, id string) error {
	return affectedOne(db.conn.ExecContext(ctx, `DELETE FROM routes WHERE id = ?`, id))
}

// Settings

// Setting returns a stored setting, or ErrNotFound.
func (db *DB) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := db.conn.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	return v, notFound(err)
}

// SetSetting stores a setting.
func (db *DB) SetSetting(ctx context.Context, key, value string) error {
	_, err := db.conn.ExecContext(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, now())
	return err
}
