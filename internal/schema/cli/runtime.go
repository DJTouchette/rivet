package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/djtouchette/rivet/internal/schema/cache"
	"github.com/djtouchette/rivet/internal/schema/catalog"
	"github.com/djtouchette/rivet/internal/schema/config"
	"github.com/djtouchette/rivet/internal/schema/types"
)

// ctxTimeout is the max wall-clock for any one catalog call. Long enough for
// a big schema, short enough that a forgotten VPN won't hang the CLI forever.
const ctxTimeout = 30 * time.Second

// loadConfig returns the parsed schema config.
func loadConfig() (*config.Config, error) {
	return config.Load(flagConfig)
}

// resolveDB finds the right Database entry using the --db flag.
func resolveDB(cfg *config.Config) (*config.Database, error) {
	return cfg.ResolveDatabase(flagDB)
}

// openCatalog dials a database and returns a Catalog. The caller must Close.
func openCatalog(dbCfg *config.Database) (catalog.Catalog, error) {
	return catalog.Open(dbCfg)
}

// openCache returns a Store anchored at --cache-dir (or the default).
func openCache() (*cache.Store, error) {
	return cache.Open(flagCacheDir)
}

// newCtx returns a per-request context with the default timeout.
func newCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), ctxTimeout)
}

// outputJSON writes JSON to the command's stdout.
func outputJSON(cmd *cobra.Command, v any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// pullSnapshot refreshes the cached schema for one database.
func pullSnapshot(cat catalog.Catalog, dbCfg *config.Database) (*cache.Entry, error) {
	ctx, cancel := newCtx()
	defer cancel()

	if err := cat.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping %s: %w", dbCfg.Name, err)
	}

	sch, err := cat.LoadSchema(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading schema for %s: %w", dbCfg.Name, err)
	}
	usage, err := cat.IndexUsage(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading index usage for %s: %w", dbCfg.Name, err)
	}
	hints, err := cat.MissingIndexHints(ctx)
	if err != nil {
		// Don't hard-fail if hints are unavailable — extension may not be installed.
		hints = nil
	}
	slow, err := cat.SlowQueries(ctx, 25)
	if err != nil {
		slow = nil
	}

	e := &cache.Entry{
		Name: dbCfg.Name, Engine: dbCfg.Engine,
		Host:       dbCfg.Host,
		FetchedAt:  time.Now().UTC(),
		Schema:     sch,
		IndexUsage: usage,
		Hints:      hints,
		SlowQueries: slow,
	}
	return e, nil
}

// loadOrFetch returns the cached snapshot if it exists, or fetches live.
func loadOrFetch(dbCfg *config.Database) (*cache.Entry, error) {
	store, err := openCache()
	if err != nil {
		return nil, err
	}
	if e, err := store.Load(dbCfg.Name); err == nil && e != nil {
		return e, nil
	}
	cat, err := openCatalog(dbCfg)
	if err != nil {
		return nil, err
	}
	defer cat.Close()

	entry, err := pullSnapshot(cat, dbCfg)
	if err != nil {
		return nil, err
	}
	_ = store.Save(entry)
	return entry, nil
}

// Guarantee imports stay used for build-time verification across files.
var _ types.Engine = types.EnginePostgres
