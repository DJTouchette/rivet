// Package types holds the JSON-serializable data shapes shared between
// catalog drivers, migration parsers, query extractors, and analyzers.
//
// Nothing in this package imports any other internal/schema package,
// so it can be depended on freely without creating import cycles.
package types

// Engine identifies a database engine family.
type Engine string

const (
	EnginePostgres Engine = "postgres"
	EngineMSSQL    Engine = "mssql"
	EngineUnknown  Engine = "unknown"
)

// Schema is a full snapshot of a single logical database.
type Schema struct {
	Database string   `json:"database"`
	Engine   Engine   `json:"engine"`
	Version  string   `json:"version,omitempty"`
	Tables   []Table  `json:"tables"`
	Views    []View   `json:"views,omitempty"`
	Source   string   `json:"source"` // "live" or "migrations"
}

// Table is a table definition with columns, indexes, and constraints.
type Table struct {
	Schema      string       `json:"schema"`
	Name        string       `json:"name"`
	Columns     []Column     `json:"columns"`
	PrimaryKey  []string     `json:"primary_key,omitempty"`
	Indexes     []Index      `json:"indexes,omitempty"`
	ForeignKeys []ForeignKey `json:"foreign_keys,omitempty"`
	RowEstimate int64        `json:"row_estimate,omitempty"`
	SizeBytes   int64        `json:"size_bytes,omitempty"`
}

// QualifiedName returns "<schema>.<name>" or "<name>" when schema is empty.
func (t Table) QualifiedName() string {
	if t.Schema == "" || t.Schema == "dbo" || t.Schema == "public" {
		return t.Name
	}
	return t.Schema + "." + t.Name
}

// Column describes a single column.
type Column struct {
	Name     string `json:"name"`
	DataType string `json:"data_type"`
	Nullable bool   `json:"nullable"`
	Default  string `json:"default,omitempty"`
	Position int    `json:"position"`
}

// Index describes an index (primary key indexes also appear here).
type Index struct {
	Name     string   `json:"name"`
	Columns  []string `json:"columns"`
	Include  []string `json:"include,omitempty"` // INCLUDE/covered columns
	Unique   bool     `json:"unique"`
	Primary  bool     `json:"primary,omitempty"`
	Method   string   `json:"method,omitempty"` // btree, gist, etc.
	Where    string   `json:"where,omitempty"`  // partial-index predicate
	SizeBytes int64   `json:"size_bytes,omitempty"`
}

// ForeignKey describes an outbound foreign key.
type ForeignKey struct {
	Name          string   `json:"name"`
	Columns       []string `json:"columns"`
	ReferencedSchema string `json:"referenced_schema,omitempty"`
	ReferencedTable  string `json:"referenced_table"`
	ReferencedColumns []string `json:"referenced_columns"`
	OnDelete      string   `json:"on_delete,omitempty"`
	OnUpdate      string   `json:"on_update,omitempty"`
}

// View is a logical view (captured for informational completeness).
type View struct {
	Schema     string `json:"schema"`
	Name       string `json:"name"`
	Definition string `json:"definition,omitempty"`
}

// IndexUsage is runtime usage data for a single index, collected from the DB.
type IndexUsage struct {
	Schema     string `json:"schema"`
	Table      string `json:"table"`
	Index      string `json:"index"`
	Seeks      int64  `json:"seeks,omitempty"`      // MSSQL: user_seeks
	Scans      int64  `json:"scans"`                // pg_stat idx_scan; MSSQL: user_scans
	Lookups    int64  `json:"lookups,omitempty"`    // MSSQL: user_lookups
	TuplesRead int64  `json:"tuples_read,omitempty"`
	Updates    int64  `json:"updates"`              // write cost (MSSQL: user_updates; pg_stat: idx_tup_upd)
	SizeBytes  int64  `json:"size_bytes,omitempty"`
}

// MissingIndexHint is a candidate missing index reported by the DB engine
// (Postgres: qualstats/pg_stat; MSSQL: sys.dm_db_missing_index_details).
type MissingIndexHint struct {
	Schema          string   `json:"schema"`
	Table           string   `json:"table"`
	EqualityColumns []string `json:"equality_columns,omitempty"`
	InequalityColumns []string `json:"inequality_columns,omitempty"`
	IncludedColumns []string `json:"included_columns,omitempty"`
	Impact          float64  `json:"impact"`     // engine-specific score (normalized roughly 0..100)
	Source          string   `json:"source"`     // "mssql-dmv", "pg-qualstats", "code-analysis"
}

// SlowQuery is a recent expensive query from the engine's query log.
type SlowQuery struct {
	Text         string  `json:"text"`
	Calls        int64   `json:"calls"`
	TotalMS      float64 `json:"total_ms"`
	MeanMS       float64 `json:"mean_ms"`
	Rows         int64   `json:"rows,omitempty"`
	SharedHits   int64   `json:"shared_hits,omitempty"`
	SharedReads  int64   `json:"shared_reads,omitempty"`
}

// QueryRef is a SQL query extracted from application source code.
type QueryRef struct {
	File    string     `json:"file"`
	Line    int        `json:"line"`
	Lang    string     `json:"lang"`       // "csharp", "go", "python", "node"
	Kind    string     `json:"kind"`       // "dapper", "sqlx", "pgx", "psycopg", etc.
	SQL     string     `json:"sql"`
	Tables  []string   `json:"tables,omitempty"`
	Columns []ColumnRef `json:"columns,omitempty"`
}

// ColumnRef is a referenced column with the clause context it appears in.
type ColumnRef struct {
	Table  string `json:"table,omitempty"`
	Column string `json:"column"`
	Clause string `json:"clause"` // "where", "join", "order_by", "group_by", "select"
}

// UnusedIndex reports an index that appears to be paying write cost without
// serving reads. Populated from IndexUsage + heuristics.
type UnusedIndex struct {
	Schema    string `json:"schema"`
	Table     string `json:"table"`
	Index     string `json:"index"`
	Reads     int64  `json:"reads"`
	Writes    int64  `json:"writes"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	Reason    string `json:"reason"`
}

// RedundantIndex reports an index whose column prefix is fully covered by
// another index on the same table.
type RedundantIndex struct {
	Schema      string `json:"schema"`
	Table       string `json:"table"`
	Index       string `json:"index"`
	CoveredBy   string `json:"covered_by"`
	Reason      string `json:"reason"`
}

// MissingIndex is a candidate index we believe should exist, either from
// code analysis or from engine hints (or both).
type MissingIndex struct {
	Schema          string   `json:"schema"`
	Table           string   `json:"table"`
	Columns         []string `json:"columns"`
	Include         []string `json:"include,omitempty"`
	Confidence      string   `json:"confidence"` // "high", "medium", "low"
	Source          string   `json:"source"`     // "engine-hint", "code-analysis", "combined"
	Evidence        []string `json:"evidence,omitempty"`
	SampleQueries   []QueryRef `json:"sample_queries,omitempty"`
}

// CoverageReport maps each table to the queries that touch it, and whether
// the predicates they use are covered by existing indexes.
type CoverageReport struct {
	Tables []TableCoverage `json:"tables"`
}

// TableCoverage is coverage information for a single table.
type TableCoverage struct {
	Schema      string        `json:"schema"`
	Table       string        `json:"table"`
	Indexes     []string      `json:"indexes"`
	QueriesHit  int           `json:"queries_hit"`
	Predicates  []PredicateHit `json:"predicates,omitempty"`
}

// PredicateHit captures a column used in a WHERE/JOIN clause and whether
// an index serves it.
type PredicateHit struct {
	Columns    []string `json:"columns"`
	Clause     string   `json:"clause"`
	Covered    bool     `json:"covered"`
	CoveringIndex string `json:"covering_index,omitempty"`
	Occurrences int     `json:"occurrences"`
}

// Overview is the top-level summary emitted by `schema overview`.
type Overview struct {
	Databases []DatabaseSummary `json:"databases"`
	Sources   []string          `json:"sources"`  // configured connection names
	Migrations *MigrationsSummary `json:"migrations,omitempty"`
}

// DatabaseSummary is a short-form summary of a single database.
type DatabaseSummary struct {
	Name       string `json:"name"`
	Engine     Engine `json:"engine"`
	Host       string `json:"host,omitempty"`
	Connected  bool   `json:"connected"`
	Tables     int    `json:"tables"`
	Views      int    `json:"views"`
	Indexes    int    `json:"indexes"`
	Error      string `json:"error,omitempty"`
}

// MigrationsSummary describes what was parsed from on-disk migration files.
type MigrationsSummary struct {
	Directory string   `json:"directory"`
	Files     int      `json:"files"`
	Tables    int      `json:"tables"`
	Indexes   int      `json:"indexes"`
	Dialect   string   `json:"dialect,omitempty"`
	Unparsed  []string `json:"unparsed,omitempty"`
}
