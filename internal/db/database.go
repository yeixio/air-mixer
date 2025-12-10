package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// DB wraps the database connection
type DB struct {
	conn *sql.DB
}

// New creates a new database connection
func New(dataSource string) (*DB, error) {
	conn, err := sql.Open("sqlite3", dataSource+"?_foreign_keys=1&_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Set connection pool settings
	conn.SetMaxOpenConns(25)
	conn.SetMaxIdleConns(5)
	conn.SetConnMaxLifetime(5 * time.Minute)

	// Test connection
	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	db := &DB{conn: conn}
	return db, nil
}

// Close closes the database connection
func (db *DB) Close() error {
	if db.conn != nil {
		return db.conn.Close()
	}
	return nil
}

// Conn returns the underlying sql.DB connection
func (db *DB) Conn() *sql.DB {
	return db.conn
}

// Migrate runs database migrations
func (db *DB) Migrate() error {
	// First, create tables that don't need migration (servers, clients, settings)
	queries := []string{
		// Servers table
		`CREATE TABLE IF NOT EXISTS servers (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			protocol TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			config TEXT,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		// Clients table
		`CREATE TABLE IF NOT EXISTS clients (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			protocol TEXT NOT NULL,
			address TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			config TEXT,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		// Settings table
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		// Indexes for servers and clients
		`CREATE INDEX IF NOT EXISTS idx_servers_protocol ON servers(protocol)`,
		`CREATE INDEX IF NOT EXISTS idx_servers_enabled ON servers(enabled)`,
		`CREATE INDEX IF NOT EXISTS idx_clients_protocol ON clients(protocol)`,
		`CREATE INDEX IF NOT EXISTS idx_clients_enabled ON clients(enabled)`,
		// Triggers for servers and clients
		`CREATE TRIGGER IF NOT EXISTS update_servers_updated_at 
			AFTER UPDATE ON servers
			BEGIN
				UPDATE servers SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
			END`,
		`CREATE TRIGGER IF NOT EXISTS update_clients_updated_at 
			AFTER UPDATE ON clients
			BEGIN
				UPDATE clients SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
			END`,
	}

	for _, query := range queries {
		if _, err := db.conn.Exec(query); err != nil {
			return fmt.Errorf("failed to execute migration: %w", err)
		}
	}

	// Migrate routes table from ID-based to name-based if needed
	// This will create the table if it doesn't exist, or migrate it if it does
	if err := db.migrateRoutesTable(); err != nil {
		return fmt.Errorf("failed to migrate routes table: %w", err)
	}

	// Now create indexes and triggers for routes (after migration ensures correct schema)
	routeQueries := []string{
		`CREATE INDEX IF NOT EXISTS idx_routes_source_name ON routes(source_name)`,
		`CREATE INDEX IF NOT EXISTS idx_routes_enabled ON routes(enabled)`,
		`CREATE INDEX IF NOT EXISTS idx_routes_priority ON routes(priority)`,
		`CREATE TRIGGER IF NOT EXISTS update_routes_updated_at 
			AFTER UPDATE ON routes
			BEGIN
				UPDATE routes SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
			END`,
	}

	for _, query := range routeQueries {
		if _, err := db.conn.Exec(query); err != nil {
			return fmt.Errorf("failed to create routes indexes/triggers: %w", err)
		}
	}

	return nil
}

// migrateRoutesTable migrates the routes table from ID-based to name-based routing
func (db *DB) migrateRoutesTable() error {
	// Check if routes table exists
	var tableExists int
	err := db.conn.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master 
		WHERE type='table' AND name='routes'
	`).Scan(&tableExists)
	if err != nil {
		return fmt.Errorf("failed to check if routes table exists: %w", err)
	}

	if tableExists == 0 {
		// Table doesn't exist, create it with the new schema
		_, err = db.conn.Exec(`
			CREATE TABLE routes (
				id TEXT PRIMARY KEY,
				name TEXT NOT NULL,
				source_name TEXT NOT NULL,
				dest_names TEXT NOT NULL,
				mix_mode TEXT NOT NULL DEFAULT 'replace',
				volume REAL NOT NULL DEFAULT 1.0,
				enabled INTEGER NOT NULL DEFAULT 1,
				priority INTEGER NOT NULL DEFAULT 0,
				created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
				updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
			)
		`)
		if err != nil {
			return fmt.Errorf("failed to create routes table: %w", err)
		}
		return nil
	}

	// Check which columns exist by querying table info
	hasOldColumns := false
	hasNewColumns := false

	rows, err := db.conn.Query(`PRAGMA table_info(routes)`)
	if err != nil {
		return fmt.Errorf("failed to get table info: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var defaultValue sql.NullString

		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("failed to scan column info: %w", err)
		}

		if name == "source_id" || name == "dest_ids" {
			hasOldColumns = true
		}
		if name == "source_name" || name == "dest_names" {
			hasNewColumns = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("error reading column info: %w", err)
	}

	// If old columns exist but new ones don't, we need to migrate
	if hasOldColumns && !hasNewColumns {
		// Get all routes with old schema
		rows, err := db.conn.Query(`
			SELECT id, name, source_id, dest_ids, mix_mode, volume, enabled, priority, created_at, updated_at
			FROM routes
		`)
		if err != nil {
			return fmt.Errorf("failed to query old routes: %w", err)
		}
		defer rows.Close()

		// Create new routes table
		_, err = db.conn.Exec(`
			CREATE TABLE routes_new (
				id TEXT PRIMARY KEY,
				name TEXT NOT NULL,
				source_name TEXT NOT NULL,
				dest_names TEXT NOT NULL,
				mix_mode TEXT NOT NULL DEFAULT 'replace',
				volume REAL NOT NULL DEFAULT 1.0,
				enabled INTEGER NOT NULL DEFAULT 1,
				priority INTEGER NOT NULL DEFAULT 0,
				created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
				updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
			)
		`)
		if err != nil {
			return fmt.Errorf("failed to create new routes table: %w", err)
		}

		// Migrate data: resolve IDs to names
		for rows.Next() {
			var id, name, sourceID, destIDs, mixMode string
			var volume float64
			var enabled int
			var priority int
			var createdAt, updatedAt time.Time

			if err := rows.Scan(&id, &name, &sourceID, &destIDs, &mixMode, &volume, &enabled, &priority, &createdAt, &updatedAt); err != nil {
				return fmt.Errorf("failed to scan route: %w", err)
			}

			// Resolve source ID to name
			var sourceName string
			err := db.conn.QueryRow(`SELECT name FROM servers WHERE id = ?`, sourceID).Scan(&sourceName)
			if err != nil {
				if err == sql.ErrNoRows {
					// Server not found, skip this route or use the ID as fallback
					sourceName = sourceID
				} else {
					return fmt.Errorf("failed to resolve source ID %s to name: %w", sourceID, err)
				}
			}

			// Resolve destination IDs to names
			destIDList := []string{}
			if destIDs != "" {
				// Split comma-separated IDs
				for _, destID := range splitAndTrim(destIDs, ",") {
					if destID != "" {
						destIDList = append(destIDList, destID)
					}
				}
			}

			destNames := []string{}
			for _, destID := range destIDList {
				var destName string
				err := db.conn.QueryRow(`SELECT name FROM clients WHERE id = ?`, destID).Scan(&destName)
				if err != nil {
					if err == sql.ErrNoRows {
						// Client not found, use ID as fallback
						destName = destID
					} else {
						return fmt.Errorf("failed to resolve destination ID %s to name: %w", destID, err)
					}
				}
				destNames = append(destNames, destName)
			}

			// Insert into new table
			_, err = db.conn.Exec(`
				INSERT INTO routes_new (id, name, source_name, dest_names, mix_mode, volume, enabled, priority, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, id, name, sourceName, joinStrings(destNames, ","), mixMode, volume, enabled, priority, createdAt, updatedAt)
			if err != nil {
				return fmt.Errorf("failed to insert migrated route: %w", err)
			}
		}

		if err := rows.Err(); err != nil {
			return fmt.Errorf("error iterating routes: %w", err)
		}

		// Drop old table and rename new one
		_, err = db.conn.Exec(`DROP TABLE routes`)
		if err != nil {
			return fmt.Errorf("failed to drop old routes table: %w", err)
		}

		_, err = db.conn.Exec(`ALTER TABLE routes_new RENAME TO routes`)
		if err != nil {
			return fmt.Errorf("failed to rename new routes table: %w", err)
		}

		// Recreate indexes
		indexQueries := []string{
			`CREATE INDEX IF NOT EXISTS idx_routes_source_name ON routes(source_name)`,
			`CREATE INDEX IF NOT EXISTS idx_routes_enabled ON routes(enabled)`,
			`CREATE INDEX IF NOT EXISTS idx_routes_priority ON routes(priority)`,
		}
		for _, query := range indexQueries {
			if _, err := db.conn.Exec(query); err != nil {
				return fmt.Errorf("failed to create index: %w", err)
			}
		}

		// Recreate trigger
		_, err = db.conn.Exec(`
			CREATE TRIGGER IF NOT EXISTS update_routes_updated_at 
			AFTER UPDATE ON routes
			BEGIN
				UPDATE routes SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
			END
		`)
		if err != nil {
			return fmt.Errorf("failed to create trigger: %w", err)
		}
	}

	return nil
}

// Helper functions for string manipulation
func splitAndTrim(s, sep string) []string {
	parts := []string{}
	for _, part := range strings.Split(s, sep) {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return parts
}

func joinStrings(strs []string, sep string) string {
	return strings.Join(strs, sep)
}

