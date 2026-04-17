// Package catalog provides the driver-neutral Catalog interface for reading
// schema, index usage, missing-index hints, and slow queries from a live DB.
//
// All catalog operations are read-only: they query system catalogs and stats
// views, never application data. Drivers should refuse to run anything that
// can mutate DB state.
package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/djtouchette/rivet/internal/schema/config"
	"github.com/djtouchette/rivet/internal/schema/types"
)

// Catalog is the read-only view of a live database used by schema-intel.
// Implementations live in subpackages (postgres, mssql) and register
// themselves via Register during init().
type Catalog interface {
	Engine() types.Engine

	// LoadSchema reads tables, columns, indexes, FKs, and size estimates.
	LoadSchema(ctx context.Context) (*types.Schema, error)

	// IndexUsage returns runtime read/write counts per index.
	IndexUsage(ctx context.Context) ([]types.IndexUsage, error)

	// MissingIndexHints returns engine-provided missing-index suggestions.
	// May return an empty slice if the engine has nothing or the feature
	// is disabled (e.g. pg_stat_statements missing).
	MissingIndexHints(ctx context.Context) ([]types.MissingIndexHint, error)

	// SlowQueries returns the top N recent expensive queries.
	SlowQueries(ctx context.Context, limit int) ([]types.SlowQuery, error)

	// Ping confirms connectivity without doing any significant work.
	Ping(ctx context.Context) error

	Close() error
}

// ErrNotSupported is returned by catalog features the engine doesn't expose.
var ErrNotSupported = errors.New("not supported on this engine")

// Factory builds a Catalog from a configured *sql.DB and a Database def.
type Factory func(db *sql.DB, cfg *config.Database) (Catalog, error)

var factories = map[types.Engine]Factory{}

// Register wires a factory for an engine. Called from driver init().
func Register(engine types.Engine, f Factory) {
	factories[engine] = f
}

// Open resolves a connection for the given database, dials it, and returns
// a Catalog. The caller MUST call Close when done.
func Open(cfg *config.Database) (Catalog, error) {
	f, ok := factories[cfg.Engine]
	if !ok {
		return nil, fmt.Errorf("no catalog driver registered for engine %q (known: %v)", cfg.Engine, registeredEngines())
	}

	dsn, err := cfg.BuildDSN()
	if err != nil {
		return nil, err
	}

	driverName := driverNameFor(cfg.Engine)
	if driverName == "" {
		return nil, fmt.Errorf("no database/sql driver configured for engine %q", cfg.Engine)
	}

	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("opening %s connection: %w", cfg.Engine, err)
	}

	// Keep the pool small; we only run short catalog queries.
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)

	cat, err := f(db, cfg)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return cat, nil
}

func driverNameFor(engine types.Engine) string {
	switch engine {
	case types.EnginePostgres:
		return "pgx"
	case types.EngineMSSQL:
		return "sqlserver"
	}
	return ""
}

func registeredEngines() []types.Engine {
	out := make([]types.Engine, 0, len(factories))
	for e := range factories {
		out = append(out, e)
	}
	return out
}
