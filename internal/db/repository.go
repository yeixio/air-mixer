package db

import (
	"database/sql"
	"fmt"
	"time"
)

// ServerRepository handles server operations
type ServerRepository struct {
	db *DB
}

// NewServerRepository creates a new server repository
func NewServerRepository(db *DB) *ServerRepository {
	return &ServerRepository{db: db}
}

// Create creates a new server
func (r *ServerRepository) Create(server *Server) error {
	now := time.Now()
	server.CreatedAt = now
	server.UpdatedAt = now

	query := `INSERT INTO servers (id, name, protocol, enabled, config, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`
	
	_, err := r.db.conn.Exec(query,
		server.ID,
		server.Name,
		server.Protocol,
		server.Enabled,
		server.Config,
		server.CreatedAt,
		server.UpdatedAt,
	)
	return err
}

// Get retrieves a server by ID
func (r *ServerRepository) Get(id string) (*Server, error) {
	server := &Server{}
	query := `SELECT id, name, protocol, enabled, config, created_at, updated_at
		FROM servers WHERE id = ?`
	
	err := r.db.conn.QueryRow(query, id).Scan(
		&server.ID,
		&server.Name,
		&server.Protocol,
		&server.Enabled,
		&server.Config,
		&server.CreatedAt,
		&server.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("server not found: %s", id)
	}
	return server, err
}

// List retrieves all servers
func (r *ServerRepository) List() ([]*Server, error) {
	query := `SELECT id, name, protocol, enabled, config, created_at, updated_at
		FROM servers ORDER BY created_at DESC`
	
	rows, err := r.db.conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var servers []*Server
	for rows.Next() {
		server := &Server{}
		if err := rows.Scan(
			&server.ID,
			&server.Name,
			&server.Protocol,
			&server.Enabled,
			&server.Config,
			&server.CreatedAt,
			&server.UpdatedAt,
		); err != nil {
			return nil, err
		}
		servers = append(servers, server)
	}
	return servers, rows.Err()
}

// Update updates an existing server
func (r *ServerRepository) Update(server *Server) error {
	server.UpdatedAt = time.Now()
	query := `UPDATE servers SET name = ?, protocol = ?, enabled = ?, config = ?, updated_at = ?
		WHERE id = ?`
	
	_, err := r.db.conn.Exec(query,
		server.Name,
		server.Protocol,
		server.Enabled,
		server.Config,
		server.UpdatedAt,
		server.ID,
	)
	return err
}

// Delete deletes a server by ID
func (r *ServerRepository) Delete(id string) error {
	query := `DELETE FROM servers WHERE id = ?`
	_, err := r.db.conn.Exec(query, id)
	return err
}

// ClientRepository handles client operations
type ClientRepository struct {
	db *DB
}

// NewClientRepository creates a new client repository
func NewClientRepository(db *DB) *ClientRepository {
	return &ClientRepository{db: db}
}

// Create creates a new client
func (r *ClientRepository) Create(client *Client) error {
	now := time.Now()
	client.CreatedAt = now
	client.UpdatedAt = now

	query := `INSERT INTO clients (id, name, protocol, address, enabled, config, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	
	_, err := r.db.conn.Exec(query,
		client.ID,
		client.Name,
		client.Protocol,
		client.Address,
		client.Enabled,
		client.Config,
		client.CreatedAt,
		client.UpdatedAt,
	)
	return err
}

// Get retrieves a client by ID
func (r *ClientRepository) Get(id string) (*Client, error) {
	client := &Client{}
	query := `SELECT id, name, protocol, address, enabled, config, created_at, updated_at
		FROM clients WHERE id = ?`
	
	err := r.db.conn.QueryRow(query, id).Scan(
		&client.ID,
		&client.Name,
		&client.Protocol,
		&client.Address,
		&client.Enabled,
		&client.Config,
		&client.CreatedAt,
		&client.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("client not found: %s", id)
	}
	return client, err
}

// List retrieves all clients
func (r *ClientRepository) List() ([]*Client, error) {
	query := `SELECT id, name, protocol, address, enabled, config, created_at, updated_at
		FROM clients ORDER BY created_at DESC`
	
	rows, err := r.db.conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clients []*Client
	for rows.Next() {
		client := &Client{}
		if err := rows.Scan(
			&client.ID,
			&client.Name,
			&client.Protocol,
			&client.Address,
			&client.Enabled,
			&client.Config,
			&client.CreatedAt,
			&client.UpdatedAt,
		); err != nil {
			return nil, err
		}
		clients = append(clients, client)
	}
	return clients, rows.Err()
}

// Update updates an existing client
func (r *ClientRepository) Update(client *Client) error {
	client.UpdatedAt = time.Now()
	query := `UPDATE clients SET name = ?, protocol = ?, address = ?, enabled = ?, config = ?, updated_at = ?
		WHERE id = ?`
	
	_, err := r.db.conn.Exec(query,
		client.Name,
		client.Protocol,
		client.Address,
		client.Enabled,
		client.Config,
		client.UpdatedAt,
		client.ID,
	)
	return err
}

// Delete deletes a client by ID
func (r *ClientRepository) Delete(id string) error {
	query := `DELETE FROM clients WHERE id = ?`
	result, err := r.db.conn.Exec(query, id)
	if err != nil {
		return err
	}
	
	// Verify that a row was actually deleted
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	
	if rowsAffected == 0 {
		return fmt.Errorf("client not found: %s", id)
	}
	
	return nil
}

// RouteRepository handles route operations
type RouteRepository struct {
	db *DB
}

// NewRouteRepository creates a new route repository
func NewRouteRepository(db *DB) *RouteRepository {
	return &RouteRepository{db: db}
}

// Create creates a new route
func (r *RouteRepository) Create(route *Route) error {
	now := time.Now()
	route.CreatedAt = now
	route.UpdatedAt = now

	query := `INSERT INTO routes (id, name, source_name, dest_names, mix_mode, volume, enabled, priority, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	
	_, err := r.db.conn.Exec(query,
		route.ID,
		route.Name,
		route.SourceName,
		route.DestNames,
		route.MixMode,
		route.Volume,
		route.Enabled,
		route.Priority,
		route.CreatedAt,
		route.UpdatedAt,
	)
	return err
}

// Get retrieves a route by ID
func (r *RouteRepository) Get(id string) (*Route, error) {
	route := &Route{}
	query := `SELECT id, name, source_name, dest_names, mix_mode, volume, enabled, priority, created_at, updated_at
		FROM routes WHERE id = ?`
	
	err := r.db.conn.QueryRow(query, id).Scan(
		&route.ID,
		&route.Name,
		&route.SourceName,
		&route.DestNames,
		&route.MixMode,
		&route.Volume,
		&route.Enabled,
		&route.Priority,
		&route.CreatedAt,
		&route.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("route not found: %s", id)
	}
	return route, err
}

// List retrieves all routes
func (r *RouteRepository) List() ([]*Route, error) {
	query := `SELECT id, name, source_name, dest_names, mix_mode, volume, enabled, priority, created_at, updated_at
		FROM routes ORDER BY priority DESC, created_at DESC`
	
	rows, err := r.db.conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var routes []*Route
	for rows.Next() {
		route := &Route{}
		if err := rows.Scan(
			&route.ID,
			&route.Name,
			&route.SourceName,
			&route.DestNames,
			&route.MixMode,
			&route.Volume,
			&route.Enabled,
			&route.Priority,
			&route.CreatedAt,
			&route.UpdatedAt,
		); err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	return routes, rows.Err()
}

// Update updates an existing route
func (r *RouteRepository) Update(route *Route) error {
	route.UpdatedAt = time.Now()
	query := `UPDATE routes SET name = ?, source_name = ?, dest_names = ?, mix_mode = ?, volume = ?, enabled = ?, priority = ?, updated_at = ?
		WHERE id = ?`
	
	_, err := r.db.conn.Exec(query,
		route.Name,
		route.SourceName,
		route.DestNames,
		route.MixMode,
		route.Volume,
		route.Enabled,
		route.Priority,
		route.UpdatedAt,
		route.ID,
	)
	return err
}

// Delete deletes a route by ID
func (r *RouteRepository) Delete(id string) error {
	query := `DELETE FROM routes WHERE id = ?`
	_, err := r.db.conn.Exec(query, id)
	return err
}

// SettingRepository handles setting operations
type SettingRepository struct {
	db *DB
}

// NewSettingRepository creates a new setting repository
func NewSettingRepository(db *DB) *SettingRepository {
	return &SettingRepository{db: db}
}

// Get retrieves a setting by key
func (r *SettingRepository) Get(key string) (string, error) {
	var value string
	query := `SELECT value FROM settings WHERE key = ?`
	err := r.db.conn.QueryRow(query, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("setting not found: %s", key)
	}
	return value, err
}

// Set sets a setting value
func (r *SettingRepository) Set(key, value string) error {
	query := `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`
	_, err := r.db.conn.Exec(query, key, value)
	return err
}

// Delete deletes a setting by key
func (r *SettingRepository) Delete(key string) error {
	query := `DELETE FROM settings WHERE key = ?`
	_, err := r.db.conn.Exec(query, key)
	return err
}

