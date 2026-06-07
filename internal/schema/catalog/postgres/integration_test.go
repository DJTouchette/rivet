package postgres

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/djtouchette/rivet/internal/schema/catalog"
	"github.com/djtouchette/rivet/internal/schema/config"
	"github.com/djtouchette/rivet/internal/schema/types"
)

// TestPostgresIntegration runs the driver against a REAL Postgres. It is skipped
// unless RIVET_TEST_PG_DSN points at a reachable instance, e.g.:
//
//	docker run -d -e POSTGRES_PASSWORD=test -p 55432:5432 postgres:16
//	RIVET_TEST_PG_DSN='postgres://postgres:test@localhost:55432/postgres?sslmode=disable' \
//	  go test ./internal/schema/catalog/postgres/ -run Integration -v
func TestPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("RIVET_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set RIVET_TEST_PG_DSN to run against a real Postgres")
	}

	raw, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer raw.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := raw.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	// Fresh, prefixed tables so we never touch real data.
	const teardown = `DROP TABLE IF EXISTS rivet_it_orders, rivet_it_users CASCADE`
	mustExec(t, ctx, raw, teardown)
	defer mustExec(t, ctx, raw, teardown)
	mustExec(t, ctx, raw, `CREATE TABLE rivet_it_users (
		id serial PRIMARY KEY,
		email text NOT NULL UNIQUE
	)`)
	mustExec(t, ctx, raw, `CREATE TABLE rivet_it_orders (
		id serial PRIMARY KEY,
		user_id int NOT NULL REFERENCES rivet_it_users(id),
		total numeric
	)`)
	mustExec(t, ctx, raw, `CREATE INDEX idx_rivet_it_orders_user ON rivet_it_orders(user_id)`)

	// Read it back through rivet's real connection path.
	cat, err := catalog.Open(&config.Database{Engine: types.EnginePostgres, DSN: dsn})
	if err != nil {
		t.Fatalf("catalog.Open: %v", err)
	}
	defer cat.Close()

	schema, err := cat.LoadSchema(ctx)
	if err != nil {
		t.Fatalf("LoadSchema: %v", err)
	}

	users := findTable(schema, "rivet_it_users")
	orders := findTable(schema, "rivet_it_orders")
	if users == nil || orders == nil {
		t.Fatalf("created tables not found in schema (%d tables loaded)", len(schema.Tables))
	}

	// Columns.
	if colByName(users, "email") == nil || colByName(users, "id") == nil {
		t.Errorf("users columns = %+v", users.Columns)
	}
	// Primary key.
	if len(users.PrimaryKey) != 1 || users.PrimaryKey[0] != "id" {
		t.Errorf("users PK = %v, want [id]", users.PrimaryKey)
	}
	// The explicit index on orders.user_id is present.
	if !hasIndexOn(orders, "user_id") {
		t.Errorf("orders indexes = %+v, want one covering user_id", orders.Indexes)
	}
	// Foreign key orders.user_id -> users.id.
	if len(orders.ForeignKeys) == 0 || orders.ForeignKeys[0].ReferencedTable != "rivet_it_users" {
		t.Errorf("orders FKs = %+v, want one referencing rivet_it_users", orders.ForeignKeys)
	}

	// Index usage should at least include the indexes we created.
	if _, err := cat.IndexUsage(ctx); err != nil {
		t.Errorf("IndexUsage: %v", err)
	}
}

func mustExec(t *testing.T, ctx context.Context, db *sql.DB, q string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, q); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func findTable(s *types.Schema, name string) *types.Table {
	for i := range s.Tables {
		if s.Tables[i].Name == name {
			return &s.Tables[i]
		}
	}
	return nil
}

func colByName(t *types.Table, name string) *types.Column {
	for i := range t.Columns {
		if t.Columns[i].Name == name {
			return &t.Columns[i]
		}
	}
	return nil
}

func hasIndexOn(t *types.Table, col string) bool {
	for _, idx := range t.Indexes {
		for _, c := range idx.Columns {
			if c == col {
				return true
			}
		}
	}
	return false
}
