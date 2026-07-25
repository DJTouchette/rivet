package migrations

import (
	"os"
	"path/filepath"
	"strings"
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

// Bug 2: only the first configured migration root was ever read, so a project
// that splits its migrations got a silently half-built schema.
func TestParseAll_MergesEveryRoot(t *testing.T) {
	cases := []struct {
		name string
		// files maps a root name to the files it holds. Roots are passed to
		// ParseAll in the order listed by roots.
		roots     []string
		files     map[string]map[string]string
		missing   []string // roots to pass but never create
		wantErr   bool
		wantTable []string // tables expected in the merged schema
		wantFiles int
		wantWarn  string // substring expected in Summary.Warnings
		// wantSourceErrs is how many roots must be reported as failed.
		wantSourceErrs int
	}{
		{
			name:  "two roots both contribute",
			roots: []string{"a", "b"},
			files: map[string]map[string]string{
				"a": {"001_users.sql": `CREATE TABLE users (id INT PRIMARY KEY);`},
				"b": {"001_orders.sql": `CREATE TABLE orders (id INT PRIMARY KEY);`},
			},
			wantTable: []string{"orders", "users"},
			wantFiles: 2,
		},
		{
			// The roots share one parser, so a later root can alter a table an
			// earlier root created. That is what makes this a merge rather than
			// two schemas printed next to each other.
			name:  "a later root alters an earlier root's table",
			roots: []string{"base", "later"},
			files: map[string]map[string]string{
				"base":  {"001.sql": `CREATE TABLE t (id INT PRIMARY KEY, a INT);`},
				"later": {"002.sql": `ALTER TABLE t ADD COLUMN b TEXT;`},
			},
			wantTable: []string{"t"},
			wantFiles: 2,
		},
		{
			// Two roots have two independent numbering spaces, so a shared
			// filename means the replay order is a guess. Say so.
			name:  "duplicate filenames across roots are flagged",
			roots: []string{"a", "b"},
			files: map[string]map[string]string{
				"a": {"001.sql": `CREATE TABLE users (id INT PRIMARY KEY);`},
				"b": {"001.sql": `CREATE TABLE orders (id INT PRIMARY KEY);`},
			},
			wantTable: []string{"orders", "users"},
			wantFiles: 2,
			wantWarn:  "appears in both",
		},
		{
			// Half a schema, clearly labelled half, beats no schema at all.
			name:           "one bad root does not lose the good one",
			roots:          []string{"a", "nope"},
			files:          map[string]map[string]string{"a": {"001.sql": `CREATE TABLE users (id INT PRIMARY KEY);`}},
			missing:        []string{"nope"},
			wantTable:      []string{"users"},
			wantFiles:      1,
			wantSourceErrs: 1,
		},
		{
			name:    "no readable root at all is an error",
			roots:   []string{"nope", "also-nope"},
			missing: []string{"nope", "also-nope"},
			wantErr: true,
		},
		{
			// Listing the same directory in both `dir` and `dirs` must not
			// replay it twice and double the file count.
			name:      "a repeated root is applied once",
			roots:     []string{"a", "a"},
			files:     map[string]map[string]string{"a": {"001.sql": `CREATE TABLE users (id INT PRIMARY KEY);`}},
			wantTable: []string{"users"},
			wantFiles: 1,
			wantWarn:  "configured more than once",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := t.TempDir()
			missing := map[string]bool{}
			for _, m := range c.missing {
				missing[m] = true
			}
			dirs := make([]string, 0, len(c.roots))
			for _, r := range c.roots {
				dirs = append(dirs, filepath.Join(base, r))
			}
			for root, files := range c.files {
				for name, body := range files {
					mustWrite(t, filepath.Join(base, root, name), body)
				}
			}

			res, err := ParseAll(dirs, Options{Dialect: "postgres"})
			if c.wantErr {
				if err == nil {
					t.Fatal("expected an error when no root could be read")
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseAll: %v", err)
			}

			var got []string
			for _, tbl := range res.Schema.Tables {
				got = append(got, tbl.Name)
			}
			if len(got) != len(c.wantTable) {
				t.Fatalf("tables = %v, want %v", got, c.wantTable)
			}
			for i, want := range c.wantTable {
				if got[i] != want {
					t.Errorf("tables = %v, want %v", got, c.wantTable)
					break
				}
			}
			if res.Summary.Files != c.wantFiles {
				t.Errorf("Files = %d, want %d", res.Summary.Files, c.wantFiles)
			}
			// Which roots contributed has to be readable from the output; a
			// merged total alone can't show that one root gave nothing.
			if len(res.Summary.Sources) == 0 {
				t.Error("Summary.Sources is empty; the merge does not say which roots contributed")
			}
			gotErrs := 0
			for _, s := range res.Summary.Sources {
				if s.Error != "" {
					gotErrs++
				}
			}
			if gotErrs != c.wantSourceErrs {
				t.Errorf("%d failed sources, want %d (%+v)", gotErrs, c.wantSourceErrs, res.Summary.Sources)
			}
			if c.wantWarn != "" {
				joined := strings.Join(res.Summary.Warnings, "|")
				if !strings.Contains(joined, c.wantWarn) {
					t.Errorf("warnings %q do not mention %q", joined, c.wantWarn)
				}
			}
		})
	}
}

// Roots replay in configured order, not merged by filename: two roots have two
// independent numbering spaces, so a global sort would invent an order nobody
// wrote. The observable consequence is that a DROP in the second root wins over
// a CREATE in the first even though its filename sorts earlier.
func TestParseAll_ConfiguredOrderWins(t *testing.T) {
	base := t.TempDir()
	first := filepath.Join(base, "first")
	second := filepath.Join(base, "second")
	mustWrite(t, filepath.Join(first, "999_create.sql"), `CREATE TABLE t (id INT PRIMARY KEY);`)
	mustWrite(t, filepath.Join(second, "001_drop.sql"), `DROP TABLE t;`)

	res, err := ParseAll([]string{first, second}, Options{Dialect: "postgres"})
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}
	if len(res.Schema.Tables) != 0 {
		t.Errorf("tables = %+v, want the second root's DROP to have been applied last", res.Schema.Tables)
	}

	// Reversing the configuration reverses the outcome — order is the user's.
	res, err = ParseAll([]string{second, first}, Options{Dialect: "postgres"})
	if err != nil {
		t.Fatalf("ParseAll reversed: %v", err)
	}
	if len(res.Schema.Tables) != 1 {
		t.Errorf("tables = %+v, want the CREATE to survive when its root is applied last", res.Schema.Tables)
	}
}

// Parse is now ParseAll with one root; the single-root behaviour it had before
// must be unchanged for every existing caller.
func TestParse_IsSingleRootParseAll(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "001.sql"), `CREATE TABLE users (id INT PRIMARY KEY);`)

	res, err := Parse(dir, Options{Dialect: "postgres"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if res.Summary.Directory != dir || res.Summary.Files != 1 || len(res.Schema.Tables) != 1 {
		t.Errorf("single-root parse changed shape: %+v", res.Summary)
	}
	if _, err := Parse(filepath.Join(dir, "does-not-exist"), Options{}); err == nil {
		t.Error("a missing single root must still be an error")
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
