package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/djtouchette/rivet/internal/schema/catalog"
)

func TestParseIndexDef(t *testing.T) {
	cases := []struct {
		def     string
		cols    []string
		include []string
	}{
		{"CREATE UNIQUE INDEX users_pkey ON public.users USING btree (id)", []string{"id"}, nil},
		{"CREATE INDEX idx ON t USING btree (tenant_id, created_at)", []string{"tenant_id", "created_at"}, nil},
		{"CREATE INDEX idx ON t USING btree (email) INCLUDE (name, status)", []string{"email"}, []string{"name", "status"}},
		{"garbage without parens", nil, nil},
	}
	for _, c := range cases {
		cols, inc := parseIndexDef(c.def)
		if !eq(cols, c.cols) {
			t.Errorf("parseIndexDef(%q) cols = %v, want %v", c.def, cols, c.cols)
		}
		if !eq(inc, c.include) {
			t.Errorf("parseIndexDef(%q) include = %v, want %v", c.def, inc, c.include)
		}
	}
}

func TestIndexUsage(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery("pg_stat_user_indexes").WillReturnRows(
		sqlmock.NewRows([]string{"schemaname", "relname", "indexrelname", "idx_scan", "idx_tup_read", "size"}).
			AddRow("public", "users", "users_pkey", 1500, 9000, 16384).
			AddRow("public", "users", "users_unused_idx", 0, 0, 8192),
	)

	d := &driver{db: db, schema: "public"}
	usage, err := d.IndexUsage(context.Background())
	if err != nil {
		t.Fatalf("IndexUsage: %v", err)
	}
	if len(usage) != 2 {
		t.Fatalf("got %d usage rows, want 2", len(usage))
	}
	if usage[0].Index != "users_pkey" || usage[0].Scans != 1500 || usage[0].SizeBytes != 16384 {
		t.Errorf("row 0 = %+v", usage[0])
	}
	if usage[1].Scans != 0 { // the dead index
		t.Errorf("row 1 scans = %d, want 0", usage[1].Scans)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

// The loader chain (tables -> columns -> indexes) assembles a table map with
// columns, indexes, and a primary key derived from the index definition.
func TestLoadersChain(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery("FROM pg_class c").WillReturnRows(
		sqlmock.NewRows([]string{"nspname", "relname", "reltuples", "size"}).
			AddRow("public", "users", 1000, 32768),
	)
	mock.ExpectQuery("information_schema.columns").WillReturnRows(
		sqlmock.NewRows([]string{"schema", "table", "column", "type", "nullable", "default", "pos"}).
			AddRow("public", "users", "id", "integer", false, "", 1).
			AddRow("public", "users", "email", "text", true, "", 2),
	)
	mock.ExpectQuery("pg_get_indexdef").WillReturnRows(
		sqlmock.NewRows([]string{"schema", "table", "name", "primary", "unique", "method", "where", "def", "size"}).
			AddRow("public", "users", "users_pkey", true, true, "btree", nil,
				"CREATE UNIQUE INDEX users_pkey ON public.users USING btree (id)", 16384),
	)

	d := &driver{db: db, schema: "public"}
	ctx := context.Background()
	tables, err := d.loadTables(ctx)
	if err != nil {
		t.Fatalf("loadTables: %v", err)
	}
	if err := d.loadColumns(ctx, tables); err != nil {
		t.Fatalf("loadColumns: %v", err)
	}
	if err := d.loadIndexes(ctx, tables); err != nil {
		t.Fatalf("loadIndexes: %v", err)
	}

	tbl := tables[key("public", "users")]
	if tbl == nil {
		t.Fatal("users table not loaded")
	}
	if len(tbl.Columns) != 2 || tbl.Columns[0].Name != "id" || tbl.Columns[1].DataType != "text" {
		t.Errorf("columns = %+v", tbl.Columns)
	}
	if tbl.Columns[0].Nullable { // id is NOT NULL
		t.Error("id should be non-nullable")
	}
	if len(tbl.Indexes) != 1 || !tbl.Indexes[0].Primary || tbl.Indexes[0].Method != "btree" {
		t.Errorf("indexes = %+v", tbl.Indexes)
	}
	if len(tbl.Indexes[0].Columns) != 1 || tbl.Indexes[0].Columns[0] != "id" {
		t.Errorf("index columns = %v, want [id] (parsed from def)", tbl.Indexes[0].Columns)
	}
	if len(tbl.PrimaryKey) != 1 || tbl.PrimaryKey[0] != "id" {
		t.Errorf("primary key = %v, want [id]", tbl.PrimaryKey)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

// The optional stats extensions used to return (nil, nil) when absent, which the
// caller could only read as "this server has no slow queries / no hints". They
// now report catalog.ErrNotSupported so an absence of data is distinguishable
// from an absence of the ability to look.
func TestOptionalExtensionsAreNotSilent(t *testing.T) {
	cases := []struct {
		name string
		// installed drives the pg_extension existence probe.
		installed bool
		// queryFails makes the follow-up stats query error, standing in for a
		// denied permission or a stats view with different column names.
		queryFails bool
		// call runs the driver method under test.
		call func(d *driver) error
		// pattern is the stats query sqlmock should expect (empty = none run).
		pattern      string
		wantNotSupp  bool // errors.Is(err, catalog.ErrNotSupported)
		wantSomeErr  bool // any error at all
		wantNilError bool
	}{
		{
			name:        "missing pg_qualstats is reported as unsupported",
			call:        func(d *driver) error { _, err := d.MissingIndexHints(context.Background()); return err },
			wantNotSupp: true,
			wantSomeErr: true,
		},
		{
			name:        "missing pg_stat_statements is reported as unsupported",
			call:        func(d *driver) error { _, err := d.SlowQueries(context.Background(), 5); return err },
			wantNotSupp: true,
			wantSomeErr: true,
		},
		{
			// Installed but unreadable is a different problem with a different
			// fix, so it must not be flattened into ErrNotSupported.
			name:        "a failing pg_stat_statements query surfaces the real error",
			installed:   true,
			queryFails:  true,
			pattern:     "pg_stat_statements",
			call:        func(d *driver) error { _, err := d.SlowQueries(context.Background(), 5); return err },
			wantSomeErr: true,
		},
		{
			name:         "an installed, empty pg_stat_statements is a genuine empty result",
			installed:    true,
			pattern:      "pg_stat_statements",
			call:         func(d *driver) error { _, err := d.SlowQueries(context.Background(), 5); return err },
			wantNilError: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()

			mock.ExpectQuery("pg_extension").WillReturnRows(
				sqlmock.NewRows([]string{"exists"}).AddRow(c.installed))
			if c.pattern != "" {
				q := mock.ExpectQuery(c.pattern)
				if c.queryFails {
					q.WillReturnError(errors.New("permission denied"))
				} else {
					q.WillReturnRows(sqlmock.NewRows([]string{
						"query", "calls", "total", "mean", "rows", "hit", "read"}))
				}
			}

			err = c.call(&driver{db: db, schema: "public"})
			if c.wantNilError && err != nil {
				t.Fatalf("got error %v, want a clean empty result", err)
			}
			if c.wantSomeErr && err == nil {
				t.Fatal("got nil error; an uncaptured section must not look like an empty one")
			}
			if got := errors.Is(err, catalog.ErrNotSupported); got != c.wantNotSupp {
				t.Errorf("errors.Is(err, ErrNotSupported) = %v, want %v (err = %v)", got, c.wantNotSupp, err)
			}
		})
	}
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
