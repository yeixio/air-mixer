package db

import (
	"database/sql"
	"time"
)

// Server represents an input audio server (protocol receiver)
type Server struct {
	ID          string    `json:"id" db:"id"`
	Name        string    `json:"name" db:"name"`
	Protocol    string    `json:"protocol" db:"protocol"` // airplay, bluetooth, chromecast, upnp
	Enabled     bool      `json:"enabled" db:"enabled"`
	Config      string    `json:"config" db:"config"` // JSON config for protocol-specific settings
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

// Client represents an output audio client (protocol sender)
type Client struct {
	ID          string    `json:"id" db:"id"`
	Name        string    `json:"name" db:"name"`
	Protocol    string    `json:"protocol" db:"protocol"` // airplay, bluetooth, chromecast, upnp
	Address     string    `json:"address" db:"address"` // Network address or device identifier
	Enabled     bool      `json:"enabled" db:"enabled"`
	Config      string    `json:"config" db:"config"` // JSON config for protocol-specific settings
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

// Route represents a routing configuration between sources and destinations
type Route struct {
	ID          string    `json:"id" db:"id"`
	Name        string    `json:"name" db:"name"`
	SourceName  string    `json:"source_name" db:"source_name"` // Server name (resolved to ID at runtime)
	DestNames   string    `json:"dest_names" db:"dest_names"` // Comma-separated client names (resolved to IDs at runtime)
	MixMode     string    `json:"mix_mode" db:"mix_mode"` // replace, mix, add
	Volume      float64   `json:"volume" db:"volume"` // 0.0 to 1.0
	Enabled     bool      `json:"enabled" db:"enabled"`
	Priority    int       `json:"priority" db:"priority"` // For route ordering
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

// Setting represents application settings
type Setting struct {
	Key       string    `json:"key" db:"key"`
	Value     string    `json:"value" db:"value"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// Scan implements the sql.Scanner interface for Server
func (s *Server) Scan(rows *sql.Rows) error {
	return rows.Scan(
		&s.ID,
		&s.Name,
		&s.Protocol,
		&s.Enabled,
		&s.Config,
		&s.CreatedAt,
		&s.UpdatedAt,
	)
}

// Scan implements the sql.Scanner interface for Client
func (c *Client) Scan(rows *sql.Rows) error {
	return rows.Scan(
		&c.ID,
		&c.Name,
		&c.Protocol,
		&c.Address,
		&c.Enabled,
		&c.Config,
		&c.CreatedAt,
		&c.UpdatedAt,
	)
}

// Scan implements the sql.Scanner interface for Route
func (r *Route) Scan(rows *sql.Rows) error {
	return rows.Scan(
		&r.ID,
		&r.Name,
		&r.SourceName,
		&r.DestNames,
		&r.MixMode,
		&r.Volume,
		&r.Enabled,
		&r.Priority,
		&r.CreatedAt,
		&r.UpdatedAt,
	)
}

