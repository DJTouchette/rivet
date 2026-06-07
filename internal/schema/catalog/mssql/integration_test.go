package mssql

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

// TestMSSQLIntegration runs the driver against a REAL SQL Server. Skipped unless
// RIVET_TEST_MSSQL_DSN points at a reachable instance, e.g.:
//
//	docker run -d -e ACCEPT_EULA=Y -e MSSQL_SA_PASSWORD='Strong!Passw0rd' \
//	  -p 1433:1433 mcr.microsoft.com/mssql/server:2022-latest
//	RIVET_TEST_MSSQL_DSN='sqlserver://sa:Strong!Passw0rd@localhost:1433?database=master' \
//	  go test ./internal/schema/catalog/mssql/ -run Integration -v
func TestMSSQLIntegration(t *testing.T) {
	dsn := os.Getenv("RIVET_TEST_MSSQL_DSN")
	if dsn == "" {
		t.Skip("set RIVET_TEST_MSSQL_DSN to run against a real SQL Server")
	}

	raw, err := sql.Open("sqlserver", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer raw.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := raw.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	// Drop child (FK) before parent. Prefixed names so we never touch real data.
	teardown := func() {
		raw.ExecContext(ctx, `DROP TABLE IF EXISTS rivet_it_orders`)
		raw.ExecContext(ctx, `DROP TABLE IF EXISTS rivet_it_users`)
	}
	teardown()
	defer teardown()

	mustExec(t, ctx, raw, `CREATE TABLE rivet_it_users (
		id INT IDENTITY(1,1) PRIMARY KEY,
		email NVARCHAR(255) NOT NULL UNIQUE
	)`)
	mustExec(t, ctx, raw, `CREATE TABLE rivet_it_orders (
		id INT IDENTITY(1,1) PRIMARY KEY,
		user_id INT NOT NULL FOREIGN KEY REFERENCES rivet_it_users(id),
		total DECIMAL(10,2)
	)`)
	mustExec(t, ctx, raw, `CREATE INDEX idx_rivet_it_orders_user ON rivet_it_orders(user_id)`)

	cat, err := catalog.Open(&config.Database{Engine: types.EngineMSSQL, DSN: dsn})
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
		t.Fatalf("created tables not found (%d loaded)", len(schema.Tables))
	}
	if colByName(users, "email") == nil || colByName(users, "id") == nil {
		t.Errorf("users columns = %+v", users.Columns)
	}
	if len(users.PrimaryKey) != 1 || users.PrimaryKey[0] != "id" {
		t.Errorf("users PK = %v, want [id]", users.PrimaryKey)
	}
	if !hasIndexOn(orders, "user_id") {
		t.Errorf("orders indexes = %+v, want one covering user_id", orders.Indexes)
	}
	if len(orders.ForeignKeys) == 0 || orders.ForeignKeys[0].ReferencedTable != "rivet_it_users" {
		t.Errorf("orders FKs = %+v, want one referencing rivet_it_users", orders.ForeignKeys)
	}
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
