package store

import (
	"database/sql"
	"strings"
	"testing"
)

// openRaw returns an in-memory database with the given tables already in it,
// standing in for a database written by an older version.
func openRaw(t *testing.T, ddl string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(ddl); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestApplySchemaAddsMissingColumns(t *testing.T) {
	db := openRaw(t, `
		CREATE TABLE things (id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '');
		INSERT INTO things (id, name) VALUES ('a', 'kept');`)

	const newer = `
		CREATE TABLE IF NOT EXISTS things (
			id    TEXT PRIMARY KEY,
			name  TEXT NOT NULL DEFAULT '',
			kind  TEXT NOT NULL DEFAULT 'plain',
			score REAL
		);
		-- An index on a new column only works if the column is added first.
		CREATE INDEX IF NOT EXISTS things_kind ON things (kind);
		CREATE TABLE IF NOT EXISTS extras (id TEXT PRIMARY KEY);`

	// Applying twice proves a second start is a no-op.
	for i := 0; i < 2; i++ {
		if err := applySchema(db, newer); err != nil {
			t.Fatalf("applySchema pass %d: %v", i+1, err)
		}
	}

	var name, kind string
	var score sql.NullFloat64
	if err := db.QueryRow(`SELECT name, kind, score FROM things WHERE id = 'a'`).
		Scan(&name, &kind, &score); err != nil {
		t.Fatalf("read migrated row: %v", err)
	}
	if name != "kept" || kind != "plain" || score.Valid {
		t.Errorf("row = (%q, %q, %v), want existing data kept and new columns at their defaults",
			name, kind, score)
	}

	// The new column keeps its NOT NULL.
	if _, err := db.Exec(`INSERT INTO things (id, kind) VALUES ('b', NULL)`); err == nil {
		t.Error("inserting NULL into an added NOT NULL column succeeded")
	}
	if _, err := db.Exec(`INSERT INTO extras (id) VALUES ('x')`); err != nil {
		t.Errorf("new table was not created: %v", err)
	}
}

// A column SQLite cannot add fails loudly and leaves the database untouched,
// rather than being added without its constraint.
func TestApplySchemaRejectsColumnsSQLiteCannotAdd(t *testing.T) {
	for _, tc := range []struct{ name, column, want string }{
		{"not null without default", "kind TEXT NOT NULL", "needs a DEFAULT"},
		{"non-constant default", "made TEXT DEFAULT CURRENT_TIMESTAMP", "add things.made"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// SQLite only refuses a non-constant default when the table has
			// rows, which a real one will.
			db := openRaw(t, `CREATE TABLE things (id TEXT PRIMARY KEY);
				INSERT INTO things (id) VALUES ('a');`)
			newer := `CREATE TABLE IF NOT EXISTS things (id TEXT PRIMARY KEY, ok REAL, ` + tc.column + `);`

			err := applySchema(db, newer)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("applySchema = %v, want an error containing %q", err, tc.want)
			}
			cols, _ := tableColumns(db, "things")
			if len(cols) != 1 {
				t.Errorf("table has %d columns after a failed migration, want the original 1", len(cols))
			}
		})
	}
}

// The embedded schema applies cleanly to itself, so a fresh install and every
// later start agree.
func TestApplySchemaToCurrentSchemaIsANoOp(t *testing.T) {
	s := newTestStore(t)
	if err := applySchema(s.db, schema); err != nil {
		t.Fatalf("re-applying the schema: %v", err)
	}
}
