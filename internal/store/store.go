// Package store persists keg data, taps, beverages and settings in SQLite.
package store

import (
	"database/sql"
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

// Store is the application's database handle.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database at path and applies the schema.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create data directory %s: %w", dir, err)
		}
	}

	// WAL keeps the HTTP read path from blocking behind an ingest write, and
	// the busy timeout absorbs the overlap when it happens anyway.
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}

	// The driver serialises access per connection; a single writer avoids
	// SQLITE_BUSY between the ingest and API paths entirely.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}
	if err := applySchema(db, schema); err != nil {
		db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

// OpenMemory opens a private in-memory database, for tests.
func OpenMemory() (*Store, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := applySchema(db, schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// applySchema brings db up to schemaSQL.
//
// CREATE TABLE IF NOT EXISTS leaves an existing table exactly as it is, so a
// column added to schema.sql would never reach a database created before it.
// Any column the schema declares but an existing table lacks is therefore
// added first; the schema itself then runs, creating new tables and any
// indexes, which may depend on the columns just added.
func applySchema(db *sql.DB, schemaSQL string) error {
	if err := addMissingColumns(db, schemaSQL); err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	return nil
}

// column is one row of PRAGMA table_info.
type column struct {
	name, typ string
	notNull   bool
	dflt      sql.NullString
	pk        bool
}

// addMissingColumns builds schemaSQL in a scratch database and adds to db each
// column that is declared there but missing from a table db already has.
//
// Only additions are handled: a renamed, retyped or dropped column still needs
// a hand-written migration. SQLite can only add a column that is nullable or
// has a constant default and is not part of the primary key, and PRAGMA
// table_info does not report UNIQUE, CHECK or REFERENCES, so a column added
// here carries its type, NOT NULL and default but no other constraint.
func addMissingColumns(db *sql.DB, schemaSQL string) error {
	want, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return err
	}
	defer want.Close()
	// Each connection to :memory: is a separate database.
	want.SetMaxOpenConns(1)
	if _, err := want.Exec(schemaSQL); err != nil {
		return fmt.Errorf("build reference schema: %w", err)
	}

	tables, err := tableNames(want)
	if err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var added []string
	for _, table := range tables {
		have, err := tableColumns(tx, table)
		if err != nil {
			return err
		}
		if len(have) == 0 {
			continue // a new table; the schema creates it whole
		}
		present := make(map[string]bool, len(have))
		for _, c := range have {
			present[strings.ToLower(c.name)] = true
		}

		wantCols, err := tableColumns(want, table)
		if err != nil {
			return err
		}
		for _, c := range wantCols {
			if present[strings.ToLower(c.name)] {
				continue
			}
			if c.pk {
				return fmt.Errorf("cannot add %s.%s: SQLite cannot add a primary key column", table, c.name)
			}
			if c.notNull && !c.dflt.Valid {
				return fmt.Errorf("cannot add %s.%s: a NOT NULL column needs a DEFAULT", table, c.name)
			}
			if _, err := tx.Exec("ALTER TABLE " + quoteIdent(table) + " ADD COLUMN " + c.decl()); err != nil {
				return fmt.Errorf("add %s.%s: %w", table, c.name, err)
			}
			added = append(added, table+"."+c.name)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// Logged only once committed: a later failure rolls every addition back.
	for _, name := range added {
		slog.Info("added a database column", "column", name)
	}
	return nil
}

// decl rebuilds the column's definition for ALTER TABLE.
func (c column) decl() string {
	d := quoteIdent(c.name)
	if c.typ != "" {
		d += " " + c.typ
	}
	if c.notNull {
		d += " NOT NULL"
	}
	if c.dflt.Valid {
		// table_info reports the default expression as written, e.g. '' or 0.
		d += " DEFAULT " + c.dflt.String
	}
	return d
}

type querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

func tableNames(q querier) ([]string, error) {
	rows, err := q.Query(`SELECT name FROM sqlite_master
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// tableColumns lists a table's columns, or none if the table does not exist.
func tableColumns(q querier, table string) ([]column, error) {
	rows, err := q.Query(`SELECT name, type, "notnull", dflt_value, pk FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []column
	for rows.Next() {
		var c column
		var pk int
		if err := rows.Scan(&c.name, &c.typ, &c.notNull, &c.dflt, &pk); err != nil {
			return nil, err
		}
		c.pk = pk > 0
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the underlying handle for callers that need it.
func (s *Store) DB() *sql.DB { return s.db }
