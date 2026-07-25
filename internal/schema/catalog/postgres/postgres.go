// Package postgres implements the Catalog interface for PostgreSQL.
//
// All queries target system catalogs and stats views — nothing ever touches
// application data.
//
// The optional stats extensions (pg_stat_statements, pg_qualstats) are consulted
// when present and reported as catalog.ErrNotSupported when absent. Returning an
// error rather than an empty slice is what lets the caller distinguish "this
// server has no slow queries" from "this server cannot tell us"; the caller
// decides that a missing extension is not fatal.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib" // registers "pgx" database/sql driver

	"github.com/djtouchette/rivet/internal/schema/catalog"
	"github.com/djtouchette/rivet/internal/schema/config"
	"github.com/djtouchette/rivet/internal/schema/types"
)

func init() {
	catalog.Register(types.EnginePostgres, New)
}

// driver implements catalog.Catalog for Postgres.
type driver struct {
	db     *sql.DB
	schema string // default search schema (defaults to "public")
}

// New builds a Postgres catalog driver.
func New(db *sql.DB, cfg *config.Database) (catalog.Catalog, error) {
	sch := cfg.Schema
	if sch == "" {
		sch = "public"
	}
	return &driver{db: db, schema: sch}, nil
}

func (d *driver) Engine() types.Engine { return types.EnginePostgres }

func (d *driver) Close() error { return d.db.Close() }

func (d *driver) Ping(ctx context.Context) error { return d.db.PingContext(ctx) }

// LoadSchema pulls tables, columns, indexes, FKs, and size estimates.
func (d *driver) LoadSchema(ctx context.Context) (*types.Schema, error) {
	out := &types.Schema{
		Engine: types.EnginePostgres,
		Source: "live",
	}

	if err := d.db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&out.Database); err != nil {
		return nil, fmt.Errorf("current_database: %w", err)
	}
	_ = d.db.QueryRowContext(ctx, `SELECT version()`).Scan(&out.Version)

	tables, err := d.loadTables(ctx)
	if err != nil {
		return nil, err
	}
	if err := d.loadColumns(ctx, tables); err != nil {
		return nil, err
	}
	if err := d.loadIndexes(ctx, tables); err != nil {
		return nil, err
	}
	if err := d.loadForeignKeys(ctx, tables); err != nil {
		return nil, err
	}

	out.Tables = make([]types.Table, 0, len(tables))
	for _, t := range tables {
		out.Tables = append(out.Tables, *t)
	}
	return out, nil
}

func (d *driver) loadTables(ctx context.Context) (map[string]*types.Table, error) {
	const q = `
		SELECT n.nspname, c.relname,
		       COALESCE(c.reltuples, 0)::bigint,
		       COALESCE(pg_total_relation_size(c.oid), 0)
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind = 'r'
		  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
		  AND n.nspname NOT LIKE 'pg_toast%'
		ORDER BY n.nspname, c.relname`

	rows, err := d.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	defer rows.Close()

	tables := make(map[string]*types.Table)
	for rows.Next() {
		t := &types.Table{}
		if err := rows.Scan(&t.Schema, &t.Name, &t.RowEstimate, &t.SizeBytes); err != nil {
			return nil, err
		}
		tables[key(t.Schema, t.Name)] = t
	}
	return tables, rows.Err()
}

func (d *driver) loadColumns(ctx context.Context, tables map[string]*types.Table) error {
	const q = `
		SELECT table_schema, table_name, column_name, data_type,
		       is_nullable = 'YES', COALESCE(column_default, ''), ordinal_position
		FROM information_schema.columns
		WHERE table_schema NOT IN ('pg_catalog', 'information_schema')
		ORDER BY table_schema, table_name, ordinal_position`

	rows, err := d.db.QueryContext(ctx, q)
	if err != nil {
		return fmt.Errorf("list columns: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var schema, table string
		var col types.Column
		if err := rows.Scan(&schema, &table, &col.Name, &col.DataType, &col.Nullable, &col.Default, &col.Position); err != nil {
			return err
		}
		t, ok := tables[key(schema, table)]
		if !ok {
			continue
		}
		t.Columns = append(t.Columns, col)
	}
	return rows.Err()
}

func (d *driver) loadIndexes(ctx context.Context, tables map[string]*types.Table) error {
	const q = `
		SELECT n.nspname, t.relname, i.relname,
		       ix.indisprimary, ix.indisunique,
		       am.amname,
		       pg_get_expr(ix.indpred, ix.indrelid),
		       pg_get_indexdef(ix.indexrelid),
		       COALESCE(pg_relation_size(i.oid), 0)
		FROM pg_class t
		JOIN pg_namespace n ON n.oid = t.relnamespace
		JOIN pg_index ix ON ix.indrelid = t.oid
		JOIN pg_class i ON i.oid = ix.indexrelid
		JOIN pg_am am ON am.oid = i.relam
		WHERE t.relkind = 'r'
		  AND n.nspname NOT IN ('pg_catalog', 'information_schema')`

	rows, err := d.db.QueryContext(ctx, q)
	if err != nil {
		return fmt.Errorf("list indexes: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var schema, table string
		var idx types.Index
		var where sql.NullString
		var def string
		if err := rows.Scan(&schema, &table, &idx.Name, &idx.Primary, &idx.Unique, &idx.Method, &where, &def, &idx.SizeBytes); err != nil {
			return err
		}
		if where.Valid {
			idx.Where = where.String
		}
		idx.Columns, idx.Include = parseIndexDef(def)

		t, ok := tables[key(schema, table)]
		if !ok {
			continue
		}
		t.Indexes = append(t.Indexes, idx)
		if idx.Primary {
			t.PrimaryKey = append([]string(nil), idx.Columns...)
		}
	}
	return rows.Err()
}

func (d *driver) loadForeignKeys(ctx context.Context, tables map[string]*types.Table) error {
	const q = `
		SELECT n.nspname, t.relname, c.conname,
		       conkey, confkey,
		       rn.nspname, rt.relname,
		       c.confdeltype, c.confupdtype
		FROM pg_constraint c
		JOIN pg_class t  ON t.oid = c.conrelid
		JOIN pg_namespace n  ON n.oid = t.relnamespace
		JOIN pg_class rt ON rt.oid = c.confrelid
		JOIN pg_namespace rn ON rn.oid = rt.relnamespace
		WHERE c.contype = 'f'`

	rows, err := d.db.QueryContext(ctx, q)
	if err != nil {
		return fmt.Errorf("list foreign keys: %w", err)
	}
	defer rows.Close()

	type fkRaw struct {
		schema, table, name string
		conkey, confkey     []int64
		refSchema, refTable string
		del, upd            string
	}

	// Column-number → name map — fetched in one pass after we know the tables.
	var raws []fkRaw
	for rows.Next() {
		var r fkRaw
		if err := rows.Scan(&r.schema, &r.table, &r.name, pgInt64Array(&r.conkey), pgInt64Array(&r.confkey), &r.refSchema, &r.refTable, &r.del, &r.upd); err != nil {
			return err
		}
		raws = append(raws, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Resolve column numbers to names via information_schema.
	colNames, err := d.columnNumberMap(ctx)
	if err != nil {
		return err
	}

	for _, r := range raws {
		fk := types.ForeignKey{
			Name:             r.name,
			ReferencedSchema: r.refSchema,
			ReferencedTable:  r.refTable,
			OnDelete:         fkAction(r.del),
			OnUpdate:         fkAction(r.upd),
		}
		for _, n := range r.conkey {
			fk.Columns = append(fk.Columns, colNames[colKey{r.schema, r.table, int(n)}])
		}
		for _, n := range r.confkey {
			fk.ReferencedColumns = append(fk.ReferencedColumns, colNames[colKey{r.refSchema, r.refTable, int(n)}])
		}
		if t, ok := tables[key(r.schema, r.table)]; ok {
			t.ForeignKeys = append(t.ForeignKeys, fk)
		}
	}
	return nil
}

type colKey struct {
	schema, table string
	num           int
}

func (d *driver) columnNumberMap(ctx context.Context) (map[colKey]string, error) {
	const q = `
		SELECT n.nspname, c.relname, a.attnum, a.attname
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE a.attnum > 0 AND NOT a.attisdropped
		  AND n.nspname NOT IN ('pg_catalog', 'information_schema')`

	rows, err := d.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("column number map: %w", err)
	}
	defer rows.Close()

	out := make(map[colKey]string)
	for rows.Next() {
		var schema, table, name string
		var num int
		if err := rows.Scan(&schema, &table, &num, &name); err != nil {
			return nil, err
		}
		out[colKey{schema, table, num}] = name
	}
	return out, rows.Err()
}

// IndexUsage reads pg_stat_user_indexes for runtime read counts.
func (d *driver) IndexUsage(ctx context.Context) ([]types.IndexUsage, error) {
	const q = `
		SELECT schemaname, relname, indexrelname,
		       COALESCE(idx_scan, 0), COALESCE(idx_tup_read, 0),
		       COALESCE(pg_relation_size(indexrelid), 0)
		FROM pg_stat_user_indexes`

	rows, err := d.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("index usage: %w", err)
	}
	defer rows.Close()

	var out []types.IndexUsage
	for rows.Next() {
		var u types.IndexUsage
		if err := rows.Scan(&u.Schema, &u.Table, &u.Index, &u.Scans, &u.TuplesRead, &u.SizeBytes); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// MissingIndexHints queries pg_qualstats if present. Postgres does not expose
// native missing-index hints, but pg_qualstats (if installed) suggests column
// candidates from observed predicates. Without the extension the result is
// catalog.ErrNotSupported, not an empty list: the caller must be able to tell
// the two apart before reporting "no missing indexes".
func (d *driver) MissingIndexHints(ctx context.Context) ([]types.MissingIndexHint, error) {
	if !d.extensionInstalled(ctx, "pg_qualstats") {
		return nil, fmt.Errorf("pg_qualstats extension not installed: %w", catalog.ErrNotSupported)
	}
	const q = `
		SELECT schemaname, relname, array_agg(DISTINCT attname), sum(execution_count)
		FROM pg_qualstats_by_query qs
		WHERE schemaname NOT IN ('pg_catalog', 'information_schema')
		GROUP BY schemaname, relname
		ORDER BY sum(execution_count) DESC
		LIMIT 50`

	rows, err := d.db.QueryContext(ctx, q)
	if err != nil {
		// The view shape varies across pg_qualstats versions, so this is an
		// expected-ish failure — but it is still a failure, and reporting it is
		// how the user finds out their hints are silently absent.
		return nil, fmt.Errorf("querying pg_qualstats_by_query: %w", err)
	}
	defer rows.Close()

	var out []types.MissingIndexHint
	for rows.Next() {
		var h types.MissingIndexHint
		var cols []string
		var impact float64
		if err := rows.Scan(&h.Schema, &h.Table, pgStringArray(&cols), &impact); err != nil {
			return nil, err
		}
		h.EqualityColumns = cols
		h.Impact = impact
		h.Source = "pg-qualstats"
		out = append(out, h)
	}
	return out, rows.Err()
}

// SlowQueries pulls from pg_stat_statements. Without the extension the result is
// catalog.ErrNotSupported rather than an empty list — an empty list would read as
// "this server ran no slow queries", which is a much stronger claim.
func (d *driver) SlowQueries(ctx context.Context, limit int) ([]types.SlowQuery, error) {
	if !d.extensionInstalled(ctx, "pg_stat_statements") {
		return nil, fmt.Errorf("pg_stat_statements extension not installed: %w", catalog.ErrNotSupported)
	}
	if limit <= 0 {
		limit = 25
	}
	const q = `
		SELECT query, calls, total_exec_time, mean_exec_time, rows,
		       COALESCE(shared_blks_hit, 0), COALESCE(shared_blks_read, 0)
		FROM pg_stat_statements
		ORDER BY total_exec_time DESC
		LIMIT $1`

	rows, err := d.db.QueryContext(ctx, q, limit)
	if err != nil {
		// e.g. an older pg_stat_statements whose columns are named differently.
		return nil, fmt.Errorf("querying pg_stat_statements: %w", err)
	}
	defer rows.Close()

	var out []types.SlowQuery
	for rows.Next() {
		var s types.SlowQuery
		if err := rows.Scan(&s.Text, &s.Calls, &s.TotalMS, &s.MeanMS, &s.Rows, &s.SharedHits, &s.SharedReads); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (d *driver) extensionInstalled(ctx context.Context, name string) bool {
	var exists bool
	err := d.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = $1)`, name).Scan(&exists)
	if err != nil {
		return false
	}
	return exists
}

// --- parsing helpers ---

// parseIndexDef extracts the indexed column list and INCLUDE list from a
// pg_get_indexdef string. Example input:
//
//	CREATE UNIQUE INDEX users_pkey ON public.users USING btree (id) INCLUDE (email)
func parseIndexDef(def string) (cols []string, include []string) {
	openIdx := strings.Index(def, "(")
	if openIdx < 0 {
		return nil, nil
	}
	closeIdx := matchingParen(def, openIdx)
	if closeIdx < 0 {
		return nil, nil
	}
	cols = splitColList(def[openIdx+1 : closeIdx])

	rest := def[closeIdx+1:]
	if incIdx := strings.Index(strings.ToUpper(rest), "INCLUDE"); incIdx >= 0 {
		incOpen := strings.Index(rest[incIdx:], "(")
		if incOpen >= 0 {
			incOpen += incIdx
			incClose := matchingParen(rest, incOpen)
			if incClose > incOpen {
				include = splitColList(rest[incOpen+1 : incClose])
			}
		}
	}
	return cols, include
}

func matchingParen(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func splitColList(inner string) []string {
	// Split on commas at depth 0 (parens inside function-call indexes nest).
	var parts []string
	depth := 0
	start := 0
	for i, r := range inner {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, cleanColExpr(inner[start:i]))
				start = i + 1
			}
		}
	}
	parts = append(parts, cleanColExpr(inner[start:]))
	return parts
}

func cleanColExpr(s string) string {
	s = strings.TrimSpace(s)
	// Strip trailing ASC/DESC and NULLS FIRST/LAST.
	for _, suffix := range []string{" ASC", " DESC", " NULLS FIRST", " NULLS LAST"} {
		up := strings.ToUpper(s)
		if strings.HasSuffix(up, suffix) {
			s = strings.TrimSpace(s[:len(s)-len(suffix)])
		}
	}
	return strings.Trim(s, `"`)
}

func fkAction(c string) string {
	switch c {
	case "a":
		return "NO ACTION"
	case "r":
		return "RESTRICT"
	case "c":
		return "CASCADE"
	case "n":
		return "SET NULL"
	case "d":
		return "SET DEFAULT"
	}
	return ""
}

func key(schema, table string) string { return schema + "." + table }

// pgInt64Array scans a Postgres smallint/int array (e.g. {1,2,3}) into a []int64.
func pgInt64Array(dst *[]int64) sql.Scanner { return &int64ArrayScanner{dst} }

type int64ArrayScanner struct{ dst *[]int64 }

func (s *int64ArrayScanner) Scan(src any) error {
	var str string
	switch v := src.(type) {
	case string:
		str = v
	case []byte:
		str = string(v)
	case nil:
		*s.dst = nil
		return nil
	default:
		return fmt.Errorf("int64ArrayScanner: unexpected type %T", src)
	}
	str = strings.TrimPrefix(str, "{")
	str = strings.TrimSuffix(str, "}")
	if str == "" {
		*s.dst = nil
		return nil
	}
	parts := strings.Split(str, ",")
	out := make([]int64, 0, len(parts))
	for _, p := range parts {
		var n int64
		if _, err := fmt.Sscanf(strings.TrimSpace(p), "%d", &n); err != nil {
			return fmt.Errorf("int64ArrayScanner: %q: %w", p, err)
		}
		out = append(out, n)
	}
	*s.dst = out
	return nil
}

// pgStringArray scans a Postgres text[] array into a []string.
func pgStringArray(dst *[]string) sql.Scanner { return &stringArrayScanner{dst} }

type stringArrayScanner struct{ dst *[]string }

func (s *stringArrayScanner) Scan(src any) error {
	var str string
	switch v := src.(type) {
	case string:
		str = v
	case []byte:
		str = string(v)
	case nil:
		*s.dst = nil
		return nil
	default:
		return fmt.Errorf("stringArrayScanner: unexpected type %T", src)
	}
	str = strings.TrimPrefix(str, "{")
	str = strings.TrimSuffix(str, "}")
	if str == "" {
		*s.dst = nil
		return nil
	}
	// Simple split — Postgres array strings can quote items with backslashes,
	// but for our use (column names, no spaces/commas) plain split is enough.
	parts := strings.Split(str, ",")
	for i, p := range parts {
		parts[i] = strings.Trim(strings.TrimSpace(p), `"`)
	}
	*s.dst = parts
	return nil
}
