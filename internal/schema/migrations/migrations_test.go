package migrations

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParse_CreateTableAndIndexes(t *testing.T) {
	dir := t.TempDir()

	mustWrite(t, filepath.Join(dir, "001_init.sql"), `
		CREATE TABLE users (
		  id BIGSERIAL PRIMARY KEY,
		  email VARCHAR(255) NOT NULL,
		  tenant_id INT NOT NULL,
		  created_at TIMESTAMP WITH TIME ZONE DEFAULT now()
		);

		CREATE INDEX users_tenant_idx ON users (tenant_id);
		CREATE UNIQUE INDEX users_email_idx ON users (email) WHERE tenant_id IS NOT NULL;
	`)
	mustWrite(t, filepath.Join(dir, "002_orders.sql"), `
		CREATE TABLE orders (
		  id BIGINT NOT NULL PRIMARY KEY,
		  user_id BIGINT NOT NULL,
		  total_cents INT,
		  FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
		);
		CREATE INDEX orders_user_tenant_idx ON orders (user_id, total_cents);
	`)

	res, err := Parse(dir, Options{Dialect: "postgres"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got, want := len(res.Schema.Tables), 2; got != want {
		t.Fatalf("tables: got %d want %d", got, want)
	}

	var users, orders *Result
	_ = users
	_ = orders

	byName := map[string]int{}
	for i, t := range res.Schema.Tables {
		byName[t.Name] = i
	}

	usersTable := res.Schema.Tables[byName["users"]]
	if len(usersTable.Columns) != 4 {
		t.Errorf("users columns: got %d want 4", len(usersTable.Columns))
	}
	if len(usersTable.PrimaryKey) == 0 || usersTable.PrimaryKey[0] != "id" {
		t.Errorf("users PK: got %v", usersTable.PrimaryKey)
	}
	if len(usersTable.Indexes) != 2 {
		t.Errorf("users indexes: got %d want 2", len(usersTable.Indexes))
	}
	// Find unique partial index.
	var partial bool
	for _, idx := range usersTable.Indexes {
		if idx.Name == "users_email_idx" {
			if !idx.Unique {
				t.Errorf("users_email_idx should be unique")
			}
			if idx.Where == "" {
				t.Errorf("users_email_idx should have WHERE clause")
			}
			partial = true
		}
	}
	if !partial {
		t.Errorf("missing users_email_idx")
	}

	ordersTable := res.Schema.Tables[byName["orders"]]
	if len(ordersTable.ForeignKeys) != 1 {
		t.Fatalf("orders FK count: got %d want 1", len(ordersTable.ForeignKeys))
	}
	fk := ordersTable.ForeignKeys[0]
	if fk.ReferencedTable != "users" || len(fk.Columns) != 1 || fk.Columns[0] != "user_id" {
		t.Errorf("orders FK shape wrong: %+v", fk)
	}
	if fk.OnDelete != "CASCADE" {
		t.Errorf("orders FK on delete: got %q", fk.OnDelete)
	}
}

func TestParse_AlterAndDrop(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "001.sql"), `
		CREATE TABLE t (id INT PRIMARY KEY, a INT, b INT);
		CREATE INDEX t_a ON t (a);
	`)
	mustWrite(t, filepath.Join(dir, "002.sql"), `
		ALTER TABLE t ADD COLUMN c TEXT NOT NULL;
		ALTER TABLE t DROP COLUMN b;
		DROP INDEX t_a;
		CREATE INDEX t_c ON t (c);
	`)
	res, err := Parse(dir, Options{Dialect: "postgres"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tbl := res.Schema.Tables[0]
	if len(tbl.Columns) != 3 {
		t.Errorf("columns: got %d want 3", len(tbl.Columns))
	}
	for _, c := range tbl.Columns {
		if c.Name == "b" {
			t.Errorf("column b should have been dropped")
		}
	}
	if len(tbl.Indexes) != 1 || tbl.Indexes[0].Name != "t_c" {
		t.Errorf("expected only t_c index, got %+v", tbl.Indexes)
	}
}

func TestParse_MSSQLBrackets(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "001.sql"), `
		CREATE TABLE [dbo].[Orders] (
		  [Id] INT NOT NULL PRIMARY KEY,
		  [UserId] INT NOT NULL
		);
		CREATE NONCLUSTERED INDEX [IX_Orders_UserId] ON [dbo].[Orders] ([UserId]) INCLUDE ([Id]);
	`)
	res, err := Parse(dir, Options{Dialect: "mssql"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(res.Schema.Tables) != 1 {
		t.Fatalf("tables: got %d", len(res.Schema.Tables))
	}
	tbl := res.Schema.Tables[0]
	if tbl.Schema != "dbo" || tbl.Name != "Orders" {
		t.Errorf("name wrong: %q.%q", tbl.Schema, tbl.Name)
	}
	if len(tbl.Indexes) != 1 {
		t.Fatalf("indexes: got %d", len(tbl.Indexes))
	}
	idx := tbl.Indexes[0]
	if idx.Name != "IX_Orders_UserId" {
		t.Errorf("idx name: %q", idx.Name)
	}
	if len(idx.Columns) != 1 || idx.Columns[0] != "UserId" {
		t.Errorf("idx columns: %v", idx.Columns)
	}
	if len(idx.Include) != 1 || idx.Include[0] != "Id" {
		t.Errorf("idx include: %v", idx.Include)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
