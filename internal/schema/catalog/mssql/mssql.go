// Package mssql implements the Catalog interface for Microsoft SQL Server
// (including Azure SQL). All queries run against sys.* DMVs and catalog views.
//
// SQL Server is unusually helpful here: sys.dm_db_missing_index_details tells
// you exactly what indexes the query optimizer wished it had, and
// sys.dm_db_index_usage_stats gives per-index read/write counts. We surface
// both directly.
package mssql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/microsoft/go-mssqldb" // registers "sqlserver" database/sql driver

	"github.com/djtouchette/rivet/internal/schema/catalog"
	"github.com/djtouchette/rivet/internal/schema/config"
	"github.com/djtouchette/rivet/internal/schema/types"
)

func init() {
	catalog.Register(types.EngineMSSQL, New)
}

type driver struct {
	db     *sql.DB
	schema string // default filter schema
}

// New builds an MSSQL catalog driver.
func New(db *sql.DB, cfg *config.Database) (catalog.Catalog, error) {
	return &driver{db: db, schema: cfg.Schema}, nil
}

func (d *driver) Engine() types.Engine { return types.EngineMSSQL }

func (d *driver) Close() error { return d.db.Close() }

func (d *driver) Ping(ctx context.Context) error { return d.db.PingContext(ctx) }

func (d *driver) LoadSchema(ctx context.Context) (*types.Schema, error) {
	out := &types.Schema{
		Engine: types.EngineMSSQL,
		Source: "live",
	}
	if err := d.db.QueryRowContext(ctx, `SELECT DB_NAME()`).Scan(&out.Database); err != nil {
		return nil, fmt.Errorf("db_name: %w", err)
	}
	_ = d.db.QueryRowContext(ctx, `SELECT @@VERSION`).Scan(&out.Version)

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
		SELECT s.name, t.name,
		       ISNULL(SUM(p.rows), 0),
		       ISNULL(SUM(a.total_pages) * 8 * 1024, 0)
		FROM sys.tables t
		JOIN sys.schemas s ON s.schema_id = t.schema_id
		LEFT JOIN sys.partitions p ON p.object_id = t.object_id AND p.index_id IN (0, 1)
		LEFT JOIN sys.allocation_units a ON a.container_id = p.partition_id
		WHERE t.is_ms_shipped = 0
		GROUP BY s.name, t.name
		ORDER BY s.name, t.name`

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
		SELECT s.name, t.name, c.name,
		       TYPE_NAME(c.user_type_id), c.is_nullable,
		       ISNULL(OBJECT_DEFINITION(c.default_object_id), ''),
		       c.column_id
		FROM sys.columns c
		JOIN sys.tables t ON t.object_id = c.object_id
		JOIN sys.schemas s ON s.schema_id = t.schema_id
		WHERE t.is_ms_shipped = 0
		ORDER BY s.name, t.name, c.column_id`

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
		if t, ok := tables[key(schema, table)]; ok {
			t.Columns = append(t.Columns, col)
		}
	}
	return rows.Err()
}

func (d *driver) loadIndexes(ctx context.Context, tables map[string]*types.Table) error {
	const q = `
		SELECT s.name, t.name, i.name, i.is_primary_key, i.is_unique, i.type_desc,
		       ISNULL(i.filter_definition, ''),
		       c.name, ic.key_ordinal, ic.is_included_column
		FROM sys.indexes i
		JOIN sys.tables t ON t.object_id = i.object_id
		JOIN sys.schemas s ON s.schema_id = t.schema_id
		JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id
		JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
		WHERE t.is_ms_shipped = 0
		  AND i.type > 0  -- skip heap entries
		  AND i.name IS NOT NULL
		ORDER BY s.name, t.name, i.name,
		         CASE WHEN ic.is_included_column = 1 THEN 1 ELSE 0 END,
		         ic.key_ordinal, ic.index_column_id`

	rows, err := d.db.QueryContext(ctx, q)
	if err != nil {
		return fmt.Errorf("list indexes: %w", err)
	}
	defer rows.Close()

	type idxKey struct{ schema, table, name string }
	idxMap := make(map[idxKey]*types.Index)
	var order []idxKey

	for rows.Next() {
		var k idxKey
		var primary, unique bool
		var method, filter, col string
		var keyOrd int
		var included bool
		if err := rows.Scan(&k.schema, &k.table, &k.name, &primary, &unique, &method, &filter, &col, &keyOrd, &included); err != nil {
			return err
		}
		idx, ok := idxMap[k]
		if !ok {
			idx = &types.Index{
				Name:    k.name,
				Unique:  unique,
				Primary: primary,
				Method:  strings.ToLower(method),
				Where:   filter,
			}
			idxMap[k] = idx
			order = append(order, k)
		}
		if included {
			idx.Include = append(idx.Include, col)
		} else {
			idx.Columns = append(idx.Columns, col)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, k := range order {
		idx := idxMap[k]
		if t, ok := tables[key(k.schema, k.table)]; ok {
			t.Indexes = append(t.Indexes, *idx)
			if idx.Primary {
				t.PrimaryKey = append([]string(nil), idx.Columns...)
			}
		}
	}
	return nil
}

func (d *driver) loadForeignKeys(ctx context.Context, tables map[string]*types.Table) error {
	const q = `
		SELECT s.name, t.name, fk.name,
		       c.name, rs.name, rt.name, rc.name,
		       fk.delete_referential_action_desc,
		       fk.update_referential_action_desc,
		       fkc.constraint_column_id
		FROM sys.foreign_keys fk
		JOIN sys.tables t   ON t.object_id  = fk.parent_object_id
		JOIN sys.schemas s  ON s.schema_id  = t.schema_id
		JOIN sys.tables rt  ON rt.object_id = fk.referenced_object_id
		JOIN sys.schemas rs ON rs.schema_id = rt.schema_id
		JOIN sys.foreign_key_columns fkc ON fkc.constraint_object_id = fk.object_id
		JOIN sys.columns c  ON c.object_id  = t.object_id  AND c.column_id  = fkc.parent_column_id
		JOIN sys.columns rc ON rc.object_id = rt.object_id AND rc.column_id = fkc.referenced_column_id
		ORDER BY s.name, t.name, fk.name, fkc.constraint_column_id`

	rows, err := d.db.QueryContext(ctx, q)
	if err != nil {
		return fmt.Errorf("list foreign keys: %w", err)
	}
	defer rows.Close()

	type fkKey struct{ schema, table, name string }
	fkMap := make(map[fkKey]*types.ForeignKey)
	var order []fkKey

	for rows.Next() {
		var k fkKey
		var col, refSchema, refTable, refCol, del, upd string
		var ord int
		if err := rows.Scan(&k.schema, &k.table, &k.name, &col, &refSchema, &refTable, &refCol, &del, &upd, &ord); err != nil {
			return err
		}
		fk, ok := fkMap[k]
		if !ok {
			fk = &types.ForeignKey{
				Name:             k.name,
				ReferencedSchema: refSchema,
				ReferencedTable:  refTable,
				OnDelete:         normalizeAction(del),
				OnUpdate:         normalizeAction(upd),
			}
			fkMap[k] = fk
			order = append(order, k)
		}
		fk.Columns = append(fk.Columns, col)
		fk.ReferencedColumns = append(fk.ReferencedColumns, refCol)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, k := range order {
		if t, ok := tables[key(k.schema, k.table)]; ok {
			t.ForeignKeys = append(t.ForeignKeys, *fkMap[k])
		}
	}
	return nil
}

// IndexUsage reads sys.dm_db_index_usage_stats + size from sys.dm_db_partition_stats.
func (d *driver) IndexUsage(ctx context.Context) ([]types.IndexUsage, error) {
	const q = `
		SELECT s.name, t.name, i.name,
		       ISNULL(us.user_seeks, 0), ISNULL(us.user_scans, 0),
		       ISNULL(us.user_lookups, 0), ISNULL(us.user_updates, 0),
		       ISNULL(SUM(ps.used_page_count), 0) * 8 * 1024
		FROM sys.indexes i
		JOIN sys.tables t   ON t.object_id = i.object_id
		JOIN sys.schemas s  ON s.schema_id = t.schema_id
		LEFT JOIN sys.dm_db_index_usage_stats us
		       ON us.object_id = i.object_id AND us.index_id = i.index_id
		      AND us.database_id = DB_ID()
		LEFT JOIN sys.dm_db_partition_stats ps
		       ON ps.object_id = i.object_id AND ps.index_id = i.index_id
		WHERE t.is_ms_shipped = 0 AND i.name IS NOT NULL AND i.type > 0
		GROUP BY s.name, t.name, i.name,
		         us.user_seeks, us.user_scans, us.user_lookups, us.user_updates`

	rows, err := d.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("index usage: %w", err)
	}
	defer rows.Close()

	var out []types.IndexUsage
	for rows.Next() {
		var u types.IndexUsage
		if err := rows.Scan(&u.Schema, &u.Table, &u.Index, &u.Seeks, &u.Scans, &u.Lookups, &u.Updates, &u.SizeBytes); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// MissingIndexHints reads SQL Server's native missing-index DMVs.
// The optimizer records every missed-index opportunity — this is gold.
func (d *driver) MissingIndexHints(ctx context.Context) ([]types.MissingIndexHint, error) {
	const q = `
		SELECT
		    OBJECT_SCHEMA_NAME(mid.object_id) AS schema_name,
		    OBJECT_NAME(mid.object_id) AS table_name,
		    ISNULL(mid.equality_columns, ''),
		    ISNULL(mid.inequality_columns, ''),
		    ISNULL(mid.included_columns, ''),
		    migs.avg_user_impact
		FROM sys.dm_db_missing_index_details mid
		JOIN sys.dm_db_missing_index_groups mig
		  ON mig.index_handle = mid.index_handle
		JOIN sys.dm_db_missing_index_group_stats migs
		  ON migs.group_handle = mig.index_group_handle
		WHERE mid.database_id = DB_ID()
		ORDER BY migs.avg_user_impact DESC`

	rows, err := d.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("missing index details: %w", err)
	}
	defer rows.Close()

	var out []types.MissingIndexHint
	for rows.Next() {
		var h types.MissingIndexHint
		var eq, ineq, incl string
		if err := rows.Scan(&h.Schema, &h.Table, &eq, &ineq, &incl, &h.Impact); err != nil {
			return nil, err
		}
		h.EqualityColumns = splitBracketList(eq)
		h.InequalityColumns = splitBracketList(ineq)
		h.IncludedColumns = splitBracketList(incl)
		h.Source = "mssql-dmv"
		out = append(out, h)
	}
	return out, rows.Err()
}

// SlowQueries reads sys.dm_exec_query_stats joined with dm_exec_sql_text.
func (d *driver) SlowQueries(ctx context.Context, limit int) ([]types.SlowQuery, error) {
	if limit <= 0 {
		limit = 25
	}
	q := fmt.Sprintf(`
		SELECT TOP %d
		    SUBSTRING(st.text, 1, 2000),
		    qs.execution_count,
		    qs.total_worker_time / 1000.0,
		    (qs.total_worker_time / 1000.0) / NULLIF(qs.execution_count, 0),
		    qs.total_logical_reads,
		    qs.total_physical_reads
		FROM sys.dm_exec_query_stats qs
		CROSS APPLY sys.dm_exec_sql_text(qs.sql_handle) st
		ORDER BY qs.total_worker_time DESC`, limit)

	rows, err := d.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("slow queries: %w", err)
	}
	defer rows.Close()

	var out []types.SlowQuery
	for rows.Next() {
		var s types.SlowQuery
		if err := rows.Scan(&s.Text, &s.Calls, &s.TotalMS, &s.MeanMS, &s.SharedHits, &s.SharedReads); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// splitBracketList parses "[foo], [bar]" (SQL Server's identifier format) into []string.
func splitBracketList(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.TrimPrefix(p, "[")
		p = strings.TrimSuffix(p, "]")
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func normalizeAction(s string) string {
	return strings.ReplaceAll(strings.ToUpper(s), "_", " ")
}

func key(schema, table string) string { return schema + "." + table }
