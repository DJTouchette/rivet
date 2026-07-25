package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/djtouchette/rivet/internal/schema/cache"
	"github.com/djtouchette/rivet/internal/schema/catalog"
	"github.com/djtouchette/rivet/internal/schema/config"
	"github.com/djtouchette/rivet/internal/schema/types"
)

// fakeCatalog stands in for a live database. The catalog drivers themselves are
// covered by sqlmock tests; what needs exercising here is the decision of
// whether to dial at all, so a hand-rolled fake keeps the tests about that.
type fakeCatalog struct {
	err       error // returned by Ping when non-nil, i.e. "server is down"
	tables    []types.Table
	slowRows  int  // how many slow queries the server would return
	gotLimit  int  // the limit the runtime actually asked for
	dialCount *int // shared counter so tests can assert "never dialled"
}

func (f *fakeCatalog) Engine() types.Engine { return types.EnginePostgres }

func (f *fakeCatalog) LoadSchema(ctx context.Context) (*types.Schema, error) {
	return &types.Schema{Database: "app", Engine: types.EnginePostgres, Tables: f.tables, Source: "live"}, nil
}

func (f *fakeCatalog) IndexUsage(ctx context.Context) ([]types.IndexUsage, error) { return nil, nil }

func (f *fakeCatalog) MissingIndexHints(ctx context.Context) ([]types.MissingIndexHint, error) {
	return nil, nil
}

func (f *fakeCatalog) SlowQueries(ctx context.Context, limit int) ([]types.SlowQuery, error) {
	f.gotLimit = limit
	n := f.slowRows
	if n > limit {
		n = limit
	}
	out := make([]types.SlowQuery, n)
	for i := range out {
		out[i] = types.SlowQuery{Text: fmt.Sprintf("SELECT %d", i), Calls: int64(i)}
	}
	return out, nil
}

func (f *fakeCatalog) Ping(ctx context.Context) error { return f.err }
func (f *fakeCatalog) Close() error                   { return nil }

// stubCatalog swaps the package's dialer for the duration of a test and returns
// the fake so assertions can read back what was asked of it.
func stubCatalog(t *testing.T, f *fakeCatalog) *fakeCatalog {
	t.Helper()
	dials := 0
	f.dialCount = &dials
	prev := openCatalog
	openCatalog = func(dbCfg *config.Database) (catalog.Catalog, error) {
		dials++
		return f, nil
	}
	t.Cleanup(func() { openCatalog = prev })
	return f
}

// useTempCache points the cache at a scratch dir and restores the global flag.
func useTempCache(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev := flagCacheDir
	flagCacheDir = dir
	t.Cleanup(func() { flagCacheDir = prev })
	return dir
}

func writeEntry(t *testing.T, e *cache.Entry) {
	t.Helper()
	store, err := openCache()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(e); err != nil {
		t.Fatal(err)
	}
}

func testDB() *config.Database {
	return &config.Database{Name: "prod", Engine: types.EnginePostgres, Host: "db.local"}
}

// The regression this guards: loadOrFetch used to return the cached snapshot if
// the file merely existed, so FetchedAt was written and never read and a
// months-old catalog answered every query as if it were live.
func TestLoadOrFetch_Expiry(t *testing.T) {
	cases := []struct {
		name string
		// cached is nil when nothing has been snapshotted yet.
		cachedAge time.Duration
		noCache   bool
		maxAge    string // schema.cache.max_age
		dbDown    bool
		wantLive  bool
		wantStale bool
		wantDials int
		wantWarn  bool
		wantErr   bool
	}{
		{
			name:      "fresh snapshot is served without dialling",
			cachedAge: time.Hour,
			wantDials: 0,
		},
		{
			name:      "expired snapshot is re-read from the database",
			cachedAge: 48 * time.Hour,
			wantLive:  true,
			wantDials: 1,
		},
		{
			name:      "no snapshot at all goes straight to the database",
			noCache:   true,
			wantLive:  true,
			wantDials: 1,
		},
		{
			// The deliberate trade-off: stale answers beat no answers, provided
			// they are labelled. The warning is asserted, not just the data.
			name:      "expired snapshot is served with a warning when the db is down",
			cachedAge: 48 * time.Hour,
			dbDown:    true,
			wantStale: true,
			wantWarn:  true,
			wantDials: 1,
		},
		{
			name:    "no snapshot and no database is a hard error",
			noCache: true,
			dbDown:  true,
			wantErr: true,
		},
		{
			name:      "config max_age shortens the window",
			cachedAge: 2 * time.Hour,
			maxAge:    "1h",
			wantLive:  true,
			wantDials: 1,
		},
		{
			name:      "config max_age lengthens the window",
			cachedAge: 48 * time.Hour,
			maxAge:    "168h",
			wantDials: 0,
		},
		{
			// max_age: 0s means the cache is write-through only.
			name:      "zero max_age always re-reads",
			cachedAge: time.Second,
			maxAge:    "0s",
			wantLive:  true,
			wantDials: 1,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			useTempCache(t)

			fake := &fakeCatalog{tables: []types.Table{{Schema: "public", Name: "users"}}}
			if c.dbDown {
				fake.err = errors.New("connection refused")
			}
			stubCatalog(t, fake)

			if !c.noCache {
				writeEntry(t, &cache.Entry{
					Name:           "prod",
					Engine:         types.EnginePostgres,
					FetchedAt:      time.Now().UTC().Add(-c.cachedAge),
					Schema:         &types.Schema{Database: "app", Tables: []types.Table{{Name: "cached_only"}}},
					SlowQueryLimit: defaultSlowQueryLimit,
				})
			}

			cfg := &config.Config{Cache: config.CacheConfig{MaxAge: c.maxAge}}
			snap, err := loadOrFetch(cfg, testDB(), need{})
			if c.wantErr {
				if err == nil {
					t.Fatal("expected an error with neither cache nor database")
				}
				return
			}
			if err != nil {
				t.Fatalf("loadOrFetch: %v", err)
			}

			if snap.Live != c.wantLive {
				t.Errorf("Live = %v, want %v", snap.Live, c.wantLive)
			}
			if snap.Stale != c.wantStale {
				t.Errorf("Stale = %v, want %v", snap.Stale, c.wantStale)
			}
			if got := *fake.dialCount; got != c.wantDials {
				t.Errorf("dialled %d times, want %d", got, c.wantDials)
			}
			if hasWarn := snap.Warning != ""; hasWarn != c.wantWarn {
				t.Errorf("Warning = %q, want present=%v", snap.Warning, c.wantWarn)
			}

			// Whatever was served, the age must be reportable — that's the
			// whole point of not refreshing silently.
			line := snap.freshnessLine()
			if line == "" {
				t.Error("freshnessLine is empty")
			}
			if c.wantStale && !strings.Contains(line, "STALE") {
				t.Errorf("stale snapshot reported as %q, want a STALE marker", line)
			}
			if !c.wantLive && !c.wantStale && strings.Contains(line, "STALE") {
				t.Errorf("fresh snapshot reported as stale: %q", line)
			}
		})
	}
}

// A cached snapshot answers from disk, so the freshly written entry has to be
// the one a subsequent command reads.
func TestLoadOrFetch_SavesFetchedSnapshot(t *testing.T) {
	useTempCache(t)
	fake := stubCatalog(t, &fakeCatalog{tables: []types.Table{{Schema: "public", Name: "users"}}})

	cfg := &config.Config{}
	if _, err := loadOrFetch(cfg, testDB(), need{}); err != nil {
		t.Fatalf("first loadOrFetch: %v", err)
	}
	// Second call must be answered from the snapshot just written.
	snap, err := loadOrFetch(cfg, testDB(), need{})
	if err != nil {
		t.Fatalf("second loadOrFetch: %v", err)
	}
	if snap.Live {
		t.Error("second call re-read the database; the first fetch was not persisted")
	}
	if *fake.dialCount != 1 {
		t.Errorf("dialled %d times, want 1", *fake.dialCount)
	}
	if snap.Schema == nil || len(snap.Schema.Tables) != 1 {
		t.Errorf("persisted snapshot lost its schema: %+v", snap.Entry)
	}
}

// Bug 2: the slow-query limit is applied when the snapshot is captured, so a
// snapshot taken with limit 25 cannot answer --limit 50 and must be re-read.
func TestLoadOrFetch_SlowQueryLimit(t *testing.T) {
	cases := []struct {
		name         string
		cachedLimit  int
		requested    int
		dbDown       bool
		wantDials    int
		wantAskedFor int // limit passed down to the catalog, 0 = never asked
		wantWarn     bool
	}{
		{
			name:        "request within the captured limit uses the cache",
			cachedLimit: 25,
			requested:   10,
			wantDials:   0,
		},
		{
			name:        "request equal to the captured limit uses the cache",
			cachedLimit: 25,
			requested:   25,
			wantDials:   0,
		},
		{
			// The bug: this used to be silently truncated to the cached 25.
			name:         "request beyond the captured limit re-reads with the bigger limit",
			cachedLimit:  25,
			requested:    50,
			wantDials:    1,
			wantAskedFor: 50,
		},
		{
			// Pre-TTL snapshots have no recorded limit, so they can't be
			// trusted to satisfy any slow-query request.
			name:         "snapshot with no recorded limit is re-read",
			cachedLimit:  0,
			requested:    25,
			wantDials:    1,
			wantAskedFor: 25,
		},
		{
			// Ordinary commands ask for nothing, so a narrow snapshot is fine.
			name:        "commands that need no slow queries ignore the limit",
			cachedLimit: 0,
			requested:   0,
			wantDials:   0,
		},
		{
			name:         "an unreachable db falls back to the narrower capture with a warning",
			cachedLimit:  25,
			requested:    50,
			dbDown:       true,
			wantDials:    1,
			wantAskedFor: 0,
			wantWarn:     true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			useTempCache(t)

			fake := &fakeCatalog{slowRows: 100}
			if c.dbDown {
				fake.err = errors.New("connection refused")
			}
			stubCatalog(t, fake)

			slow := make([]types.SlowQuery, c.cachedLimit)
			writeEntry(t, &cache.Entry{
				Name:           "prod",
				Engine:         types.EnginePostgres,
				FetchedAt:      time.Now().UTC(), // fresh: only the limit is in question
				Schema:         &types.Schema{Database: "app"},
				SlowQueries:    slow,
				SlowQueryLimit: c.cachedLimit,
			})

			snap, err := loadOrFetch(&config.Config{}, testDB(), need{SlowQueryLimit: c.requested})
			if err != nil {
				t.Fatalf("loadOrFetch: %v", err)
			}
			if got := *fake.dialCount; got != c.wantDials {
				t.Errorf("dialled %d times, want %d", got, c.wantDials)
			}
			if fake.gotLimit != c.wantAskedFor {
				t.Errorf("asked the catalog for limit %d, want %d", fake.gotLimit, c.wantAskedFor)
			}
			if hasWarn := snap.Warning != ""; hasWarn != c.wantWarn {
				t.Errorf("Warning = %q, want present=%v", snap.Warning, c.wantWarn)
			}
			if c.wantWarn && !strings.Contains(snap.Warning, "limit") {
				t.Errorf("warning %q should say the capture was narrower than requested", snap.Warning)
			}
		})
	}
}

// A live fetch must never capture fewer rows than the default, or an incidental
// `--limit 1` would poison the shared snapshot for every later command.
func TestFetchSnapshot_LimitFloor(t *testing.T) {
	useTempCache(t)
	fake := stubCatalog(t, &fakeCatalog{slowRows: 100})

	if _, err := loadOrFetch(&config.Config{}, testDB(), need{SlowQueryLimit: 1}); err != nil {
		t.Fatalf("loadOrFetch: %v", err)
	}
	if fake.gotLimit != defaultSlowQueryLimit {
		t.Errorf("captured with limit %d, want at least the default %d", fake.gotLimit, defaultSlowQueryLimit)
	}
}

func TestHumanAge(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "0s"},
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{90 * time.Minute, "1h30m"},
		{25 * time.Hour, "1d01h"},
		{200 * time.Hour, "8d08h"},
		{-time.Hour, "0s"}, // clock skew must not print a negative age
	}
	for _, c := range cases {
		if got := humanAge(c.in); got != c.want {
			t.Errorf("humanAge(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// End-to-end through cobra: `queries slow --limit 50` used to be capped at the
// 25 hardcoded in pullSnapshot regardless of the flag.
func TestQueriesSlowCommand_LimitFlowsThrough(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeSchemaConfig(t, dir, "")
	fake := stubCatalog(t, &fakeCatalog{slowRows: 100})

	out, _, err := runRoot(t, "queries", "slow", "--limit", "50",
		"--config", cfgPath, "--cache-dir", t.TempDir())
	if err != nil {
		t.Fatalf("queries slow: %v", err)
	}
	if fake.gotLimit != 50 {
		t.Errorf("catalog asked for limit %d, want 50", fake.gotLimit)
	}

	var rows []types.SlowQuery
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("parsing output %q: %v", out, err)
	}
	if len(rows) != 50 {
		t.Errorf("got %d slow queries, want 50", len(rows))
	}
}

// The age has to reach the user, not just the struct: JSON mode puts it on
// stderr so stdout stays parseable, human mode prints it inline.
func TestFreshnessIsSurfacedToTheUser(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeSchemaConfig(t, dir, "")
	cacheDir := t.TempDir()

	stubCatalog(t, &fakeCatalog{tables: []types.Table{{Schema: "public", Name: "users"}}})

	// First run fetches live and writes the snapshot.
	if _, _, err := runRoot(t, "tables", "--config", cfgPath, "--cache-dir", cacheDir); err != nil {
		t.Fatalf("tables (live): %v", err)
	}

	// Second run is served from cache and must say so on stderr, leaving stdout
	// as valid JSON.
	stdout, stderr, err := runRoot(t, "tables", "--config", cfgPath, "--cache-dir", cacheDir)
	if err != nil {
		t.Fatalf("tables (cached): %v", err)
	}
	if !strings.Contains(stderr, "snapshot:") {
		t.Errorf("stderr %q does not report the snapshot age", stderr)
	}
	var tables []types.Table
	if err := json.Unmarshal([]byte(stdout), &tables); err != nil {
		t.Fatalf("stdout is not clean JSON (%v): %q", err, stdout)
	}

	// Human mode carries the same line on stdout.
	stdout, _, err = runRoot(t, "tables", "--human", "--config", cfgPath, "--cache-dir", cacheDir)
	if err != nil {
		t.Fatalf("tables --human: %v", err)
	}
	if !strings.Contains(stdout, "snapshot:") {
		t.Errorf("human output %q does not report the snapshot age", stdout)
	}
}

// Bug: overview reported Connected: true whenever a snapshot file loaded, i.e.
// it claimed a connection it never made. Connected must track the ping.
func TestOverviewConnectedReflectsReality(t *testing.T) {
	cases := []struct {
		name          string
		dbDown        bool
		writeSnapshot bool
		wantConnected bool
		wantStale     bool
	}{
		{"reachable db with a fresh snapshot", false, true, true, false},
		{"unreachable db with a snapshot on disk is NOT connected", true, true, false, false},
		{"reachable db with no snapshot is still connected", false, false, true, false},
		{"unreachable db with no snapshot", true, false, false, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := writeSchemaConfig(t, dir, "")
			cacheDir := t.TempDir()

			fake := &fakeCatalog{}
			if c.dbDown {
				fake.err = errors.New("connection refused")
			}
			stubCatalog(t, fake)

			if c.writeSnapshot {
				prev := flagCacheDir
				flagCacheDir = cacheDir
				writeEntry(t, &cache.Entry{
					Name: "prod", Engine: types.EnginePostgres,
					FetchedAt: time.Now().UTC(),
					Schema:    &types.Schema{Database: "app", Tables: []types.Table{{Name: "users"}}},
				})
				flagCacheDir = prev
			}

			stdout, _, err := runRoot(t, "overview", "--config", cfgPath, "--cache-dir", cacheDir)
			if err != nil {
				t.Fatalf("overview: %v", err)
			}
			var ov types.Overview
			if err := json.Unmarshal([]byte(stdout), &ov); err != nil {
				t.Fatalf("parsing overview %q: %v", stdout, err)
			}
			if len(ov.Databases) != 1 {
				t.Fatalf("got %d database summaries, want 1", len(ov.Databases))
			}
			got := ov.Databases[0]
			if got.Connected != c.wantConnected {
				t.Errorf("Connected = %v, want %v (error field: %q)", got.Connected, c.wantConnected, got.Error)
			}
			if c.writeSnapshot && got.SnapshotAge == "" {
				t.Error("summary does not report the snapshot age")
			}
			if got.SnapshotStale != c.wantStale {
				t.Errorf("SnapshotStale = %v, want %v", got.SnapshotStale, c.wantStale)
			}
		})
	}
}

// Overview must flag a stale snapshot even when the database is reachable —
// the counts it prints still came from the old file.
func TestOverviewFlagsStaleSnapshot(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeSchemaConfig(t, dir, "  cache:\n    max_age: 1h\n")
	cacheDir := t.TempDir()

	stubCatalog(t, &fakeCatalog{})

	prev := flagCacheDir
	flagCacheDir = cacheDir
	writeEntry(t, &cache.Entry{
		Name: "prod", Engine: types.EnginePostgres,
		FetchedAt: time.Now().UTC().Add(-6 * time.Hour),
		Schema:    &types.Schema{Database: "app"},
	})
	flagCacheDir = prev

	stdout, _, err := runRoot(t, "overview", "--config", cfgPath, "--cache-dir", cacheDir)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	var ov types.Overview
	if err := json.Unmarshal([]byte(stdout), &ov); err != nil {
		t.Fatalf("parsing overview: %v", err)
	}
	if !ov.Databases[0].SnapshotStale {
		t.Errorf("6h-old snapshot with max_age 1h not marked stale: %+v", ov.Databases[0])
	}
	if !strings.Contains(ov.Databases[0].Error, "stale") {
		t.Errorf("stale snapshot has no explanation: %q", ov.Databases[0].Error)
	}
}

// --- helpers ---

// writeSchemaConfig writes a minimal .rivet/config.yaml with one database and
// optional extra lines inside the schema: section.
func writeSchemaConfig(t *testing.T, dir, extra string) string {
	t.Helper()
	body := "schema:\n" + extra +
		"  databases:\n    - name: prod\n      engine: postgres\n      host: db.local\n      default: true\n"
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// runRoot executes the schema command tree with captured streams. Persistent
// flags are package-level vars, so a fresh root per run is what resets them.
func runRoot(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := NewRoot("schema", "test")
	var outBuf, errBuf strings.Builder
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(args)
	root.SilenceErrors = true
	root.SilenceUsage = true
	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}
