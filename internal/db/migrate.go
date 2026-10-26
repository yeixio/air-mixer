package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// migrations are applied in order; PRAGMA user_version records how many have
// run. Never edit a released migration, append a new one.
var migrations = []func(context.Context, *sql.Tx) error{
	migrateBaseSchema,
	migrateDestNamesToJSON,
}

func (db *DB) migrate(ctx context.Context) error {
	var version int
	if err := db.conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version > len(migrations) {
		return fmt.Errorf("database schema version %d is newer than this build supports (%d)", version, len(migrations))
	}
	for i := version; i < len(migrations); i++ {
		tx, err := db.conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if err := migrations[i](ctx, tx); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
	}
	return nil
}

// migrateBaseSchema creates the tables. Databases from before versioned
// migrations already have most of them, and may have an older routes table
// keyed by source and destination IDs, which is converted to names.
func migrateBaseSchema(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS servers (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			protocol TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			config TEXT,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
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
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return err
		}
	}

	cols, err := tableColumns(ctx, tx, "routes")
	if err != nil {
		return err
	}
	if cols["source_id"] && !cols["source_name"] {
		if err := convertLegacyRoutes(ctx, tx); err != nil {
			return err
		}
	} else if len(cols) == 0 {
		if _, err := tx.ExecContext(ctx, createRoutes("routes")); err != nil {
			return err
		}
	}

	for _, s := range []string{
		`CREATE INDEX IF NOT EXISTS idx_routes_source_name ON routes(source_name)`,
		`DROP TRIGGER IF EXISTS update_servers_updated_at`,
		`DROP TRIGGER IF EXISTS update_clients_updated_at`,
		`DROP TRIGGER IF EXISTS update_routes_updated_at`,
	} {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

func createRoutes(table string) string {
	return `CREATE TABLE ` + table + ` (
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
	)`
}

func tableColumns(ctx context.Context, tx *sql.Tx, table string) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

// convertLegacyRoutes rewrites routes that reference servers and clients by
// ID so they reference them by name. IDs that no longer resolve are kept as
// names, which leaves the route visible but inactive.
func convertLegacyRoutes(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, createRoutes("routes_new")); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO routes_new (id, name, source_name, dest_names, mix_mode, volume, enabled, priority, created_at, updated_at)
		SELECT r.id, r.name, COALESCE(s.name, r.source_id), r.dest_ids, r.mix_mode, r.volume, r.enabled, r.priority, r.created_at, r.updated_at
		FROM routes r LEFT JOIN servers s ON s.id = r.source_id`)
	if err != nil {
		return err
	}

	rows, err := tx.QueryContext(ctx, `SELECT id, dest_names FROM routes_new`)
	if err != nil {
		return err
	}
	type update struct{ id, names string }
	var updates []update
	for rows.Next() {
		var id, ids string
		if err := rows.Scan(&id, &ids); err != nil {
			rows.Close()
			return err
		}
		var names []string
		for _, cid := range splitList(ids) {
			name := cid
			err := tx.QueryRowContext(ctx, `SELECT name FROM clients WHERE id = ?`, cid).Scan(&name)
			if err != nil && err != sql.ErrNoRows {
				rows.Close()
				return err
			}
			names = append(names, name)
		}
		updates = append(updates, update{id, strings.Join(names, ",")})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, u := range updates {
		if _, err := tx.ExecContext(ctx, `UPDATE routes_new SET dest_names = ? WHERE id = ?`, u.names, u.id); err != nil {
			return err
		}
	}
	for _, s := range []string{`DROP TABLE routes`, `ALTER TABLE routes_new RENAME TO routes`} {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

// migrateDestNamesToJSON stores destination lists as JSON arrays instead of
// comma-separated text, so names may contain commas.
func migrateDestNamesToJSON(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT id, dest_names FROM routes`)
	if err != nil {
		return err
	}
	converted := make(map[string]string)
	for rows.Next() {
		var id, names string
		if err := rows.Scan(&id, &names); err != nil {
			rows.Close()
			return err
		}
		b, err := json.Marshal(splitList(names))
		if err != nil {
			rows.Close()
			return err
		}
		converted[id] = string(b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for id, names := range converted {
		if _, err := tx.ExecContext(ctx, `UPDATE routes SET dest_names = ? WHERE id = ?`, names, id); err != nil {
			return err
		}
	}
	return nil
}

func splitList(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
