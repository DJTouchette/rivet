package cli

import (
	"context"
	"encoding/json"
	"errors"
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

// pingTimeout is deliberately much shorter than ctxTimeout: `overview` dials
// every configured database just to answer "is it up?", so an unreachable host
// must fail fast instead of stalling the whole summary.
const pingTimeout = 5 * time.Second

// defaultSlowQueryLimit is how many slow queries a snapshot captures when the
// caller doesn't ask for a specific number. Matches the default of
// `schema queries slow --limit`.
const defaultSlowQueryLimit = 25

// loadConfig returns the parsed schema config.
func loadConfig() (*config.Config, error) {
	return config.Load(flagConfig)
}

// resolveDB finds the right Database entry using the --db flag.
func resolveDB(cfg *config.Config) (*config.Database, error) {
	return cfg.ResolveDatabase(flagDB)
}

// openCatalog dials a database and returns a Catalog. The caller must Close.
// It's a var so tests can substitute a fake catalog without a live server.
var openCatalog = func(dbCfg *config.Database) (catalog.Catalog, error) {
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

// pullSnapshot refreshes the cached schema for one database. slowLimit caps the
// slow-query capture; it is recorded on the entry so a later request for more
// rows can tell the snapshot is too narrow to answer it.
func pullSnapshot(cat catalog.Catalog, dbCfg *config.Database, slowLimit int) (*cache.Entry, error) {
	ctx, cancel := newCtx()
	defer cancel()

	if slowLimit <= 0 {
		slowLimit = defaultSlowQueryLimit
	}

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

	// Hints and slow queries are *optional* catalog sections, so a failure here
	// is recorded rather than fatal: pg_qualstats and pg_stat_statements are
	// absent on plenty of perfectly healthy databases, and failing the whole
	// snapshot would take `schema tables` and `schema describe` down with them.
	// What is not acceptable is the old behaviour of storing nil and moving on —
	// that made "we couldn't look" indistinguishable from "there are none". Every
	// failure becomes a Gap that travels with the snapshot (including through the
	// cache file) and is surfaced by reportFreshness on every command.
	var gaps []cache.Gap
	hints, err := cat.MissingIndexHints(ctx)
	if err != nil {
		hints = nil
		gaps = append(gaps, newGap(cache.FeatureMissingIndexHints, err))
	}
	slow, err := cat.SlowQueries(ctx, slowLimit)
	if err != nil {
		slow = nil
		gaps = append(gaps, newGap(cache.FeatureSlowQueries, err))
	}

	e := &cache.Entry{
		Name: dbCfg.Name, Engine: dbCfg.Engine,
		Host:           dbCfg.Host,
		FetchedAt:      time.Now().UTC(),
		Schema:         sch,
		IndexUsage:     usage,
		Hints:          hints,
		SlowQueries:    slow,
		SlowQueryLimit: slowLimit,
		Gaps:           gaps,
	}
	return e, nil
}

// newGap classifies a capture failure. A driver that wraps catalog.ErrNotSupported
// is telling us the engine simply doesn't expose the feature — a permanent,
// blameless condition — while anything else (permission denied, a malformed
// stats view, a timeout) is a problem somebody can fix. Both are recorded; only
// the wording and the Kind differ.
func newGap(feature string, err error) cache.Gap {
	kind := cache.GapFailed
	if errors.Is(err, catalog.ErrNotSupported) {
		kind = cache.GapUnavailable
	}
	return cache.Gap{Feature: feature, Kind: kind, Reason: err.Error()}
}

// need describes what a command requires of a snapshot beyond being fresh.
type need struct {
	// SlowQueryLimit is how many slow-query rows the command intends to show.
	// A snapshot captured with a smaller limit physically cannot answer it, so
	// it is re-read even when its age is still within max_age.
	SlowQueryLimit int
}

// snapshot is a cache entry plus the freshness facts a command must be able to
// show the user. Commands never get a bare *cache.Entry any more, precisely so
// the age of the data can't be dropped on the floor on the way to the screen.
type snapshot struct {
	*cache.Entry

	Age     time.Duration // 0 when Live
	MaxAge  time.Duration // the configured freshness budget
	Stale   bool          // older than MaxAge (or of unknown age)
	Live    bool          // read from the database during this command
	Warning string        // non-empty when stale data was served deliberately
}

// loadOrFetch returns a usable snapshot for dbCfg, re-reading the database when
// the cached one is missing, expired, or too narrow for what the caller needs.
func loadOrFetch(cfg *config.Config, dbCfg *config.Database, n need) (*snapshot, error) {
	maxAge, err := cfg.Cache.MaxAgeDuration()
	if err != nil {
		return nil, err
	}

	store, err := openCache()
	if err != nil {
		return nil, err
	}

	cached, err := store.Load(dbCfg.Name)
	if err != nil {
		// A corrupt or unreadable snapshot is not fatal — it just means we have
		// nothing cached and must go to the database.
		cached = nil
	}

	now := time.Now().UTC()
	if cached != nil && !cached.IsStale(now, maxAge) && cached.SlowQueryLimit >= n.SlowQueryLimit {
		return &snapshot{Entry: cached, Age: cached.Age(now), MaxAge: maxAge}, nil
	}

	fresh, fetchErr := fetchSnapshot(dbCfg, n.SlowQueryLimit)
	if fetchErr == nil {
		_ = store.Save(fresh)
		return &snapshot{Entry: fresh, MaxAge: maxAge, Live: true}, nil
	}

	// Deliberate decision: when the database is unreachable and all we hold is
	// an expired snapshot, we serve it rather than failing. Schema questions
	// ("which columns does orders have?") are still mostly answerable from
	// week-old data, and an agent or a developer on a train is better served by
	// answers labelled STALE than by an error. The labelling is what makes this
	// safe — see freshnessLine; the warning is never suppressed.
	if cached != nil {
		s := &snapshot{
			Entry:  cached,
			Age:    cached.Age(now),
			MaxAge: maxAge,
			Stale:  cached.IsStale(now, maxAge),
		}
		s.Warning = fmt.Sprintf("database unreachable (%v) — serving cached data", fetchErr)
		if cached.SlowQueryLimit < n.SlowQueryLimit {
			s.Warning += fmt.Sprintf("; slow queries were captured with limit %d, so at most that many can be shown", cached.SlowQueryLimit)
		}
		return s, nil
	}

	// Nothing cached and nothing live: there is no answer to give.
	return nil, fetchErr
}

// fetchSnapshot dials the database and captures a snapshot. slowLimit is raised
// to the default so an ordinary command still fills the slow-query section.
func fetchSnapshot(dbCfg *config.Database, slowLimit int) (*cache.Entry, error) {
	if slowLimit < defaultSlowQueryLimit {
		slowLimit = defaultSlowQueryLimit
	}
	cat, err := openCatalog(dbCfg)
	if err != nil {
		return nil, err
	}
	defer cat.Close()
	return pullSnapshot(cat, dbCfg, slowLimit)
}

// pingDatabase reports whether the database answers right now. `overview` used
// to infer "connected" from the mere existence of a snapshot file, i.e. it
// claimed a connection it had never made; a short ping keeps the field honest
// without paying for a full catalog read.
func pingDatabase(dbCfg *config.Database) error {
	cat, err := openCatalog(dbCfg)
	if err != nil {
		return err
	}
	defer cat.Close()

	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()
	return cat.Ping(ctx)
}

// freshnessLine renders the one-line provenance banner shown with every result
// that came out of the cache. Incompleteness rides the same line as age: both
// answer "how much should I trust this?", and there is exactly one place a
// command can forget to print.
func (s *snapshot) freshnessLine() string {
	var line string
	if s.Live {
		line = "snapshot: live — just read from the database"
	} else {
		when := "unknown time"
		if !s.FetchedAt.IsZero() {
			when = s.FetchedAt.Format(time.RFC3339)
		}
		line = fmt.Sprintf("snapshot: %s old (fetched %s)", humanAge(s.Age), when)
		if s.Stale {
			line += fmt.Sprintf(" — STALE, past the %s max age; run 'rivet schema refresh'", humanDuration(s.MaxAge))
		}
	}
	if s.Warning != "" {
		line += " — " + s.Warning
	}
	if gaps := s.GapSummary(); gaps != "" {
		line += " — INCOMPLETE: " + gaps
	}
	return line
}

// reportFreshness surfaces the snapshot's age and any missing sections so cached
// or partial data can never pass itself off as live and complete. Human mode gets
// it inline on stdout; JSON mode gets it on stderr so stdout stays a single
// parseable document (the MCP bridge relays stderr to the caller, so it stays
// visible there too).
func reportFreshness(cmd *cobra.Command, s *snapshot) {
	if s == nil {
		return
	}
	reportLine(cmd, s.freshnessLine())
}

// reportGap warns when the very section a command exists to show is the one
// missing from the snapshot. freshnessLine already mentions it, but there the
// reader has to connect "INCOMPLETE: slow_queries" to the empty list they are
// looking at; this says it outright, because an empty result read as "none"
// is the failure this whole mechanism exists to prevent.
//
// what names the data in the command's own words, e.g. "slow queries".
func reportGap(cmd *cobra.Command, s *snapshot, feature, what string) {
	if s == nil {
		return
	}
	g := s.Gap(feature)
	if g == nil {
		return
	}
	reportLine(cmd, fmt.Sprintf(
		"WARNING: %s were not captured in this snapshot (%s: %s) — this result is 'unknown', not 'none'",
		what, g.Kind, g.Reason))
}

// reportLine writes one out-of-band provenance line: stdout in human mode,
// stderr in JSON mode so stdout stays a single parseable document.
func reportLine(cmd *cobra.Command, line string) {
	if flagHuman {
		fmt.Fprintln(cmd.OutOrStdout(), line)
		return
	}
	fmt.Fprintln(cmd.ErrOrStderr(), line)
}

// humanAge renders a duration at the coarsest useful precision — nobody reading
// "is this data current?" cares about the seconds in a six-day-old snapshot.
func humanAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%02dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

// humanDuration renders a configured budget (as opposed to an elapsed age),
// keeping the shape the user typed in config where possible.
func humanDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	return d.String()
}

// Guarantee imports stay used for build-time verification across files.
var _ types.Engine = types.EnginePostgres
