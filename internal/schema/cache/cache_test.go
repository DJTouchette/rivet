package cache

import (
	"testing"
	"time"

	"github.com/djtouchette/rivet/internal/schema/types"
)

func TestSafeFileName(t *testing.T) {
	cases := map[string]string{
		"prod":      "prod",
		"prod/db":   "prod_db",
		"a b.c":     "a_b_c",
		"my-db_1":   "my-db_1",
		"":          "default",
		"../escape": "___escape",
	}
	for in, want := range cases {
		if got := safeFileName(in); got != want {
			t.Errorf("safeFileName(%q) = %q, want %q", in, got, want)
		}
	}
}

// Age and IsStale are what stop the cache from serving week-old catalogs as if
// they were live, so the boundary cases matter more than the happy path.
func TestAgeAndIsStale(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name      string
		fetchedAt time.Time
		maxAge    time.Duration
		wantAge   time.Duration
		wantStale bool
	}{
		{"fresh well inside the budget", now.Add(-time.Hour), 24 * time.Hour, time.Hour, false},
		{"exactly at the budget counts as expired", now.Add(-24 * time.Hour), 24 * time.Hour, 24 * time.Hour, true},
		{"past the budget", now.Add(-72 * time.Hour), 24 * time.Hour, 72 * time.Hour, true},
		// A snapshot with no timestamp predates the TTL field (or was hand-made);
		// an unknown age is never a safe age.
		{"missing timestamp is always stale", time.Time{}, 24 * time.Hour, 0, true},
		// max_age: 0s is the documented "never serve from cache" setting.
		{"zero budget makes everything stale", now, 0, 0, true},
		// Clock skew must not produce a negative age or a spuriously fresh entry.
		{"future timestamp clamps to zero age", now.Add(time.Hour), time.Hour, 0, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := &Entry{Name: "prod", FetchedAt: c.fetchedAt}
			if got := e.Age(now); got != c.wantAge {
				t.Errorf("Age = %v, want %v", got, c.wantAge)
			}
			if got := e.IsStale(now, c.maxAge); got != c.wantStale {
				t.Errorf("IsStale = %v, want %v", got, c.wantStale)
			}
		})
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Loading something absent returns (nil, nil), not an error.
	if e, err := st.Load("missing"); err != nil || e != nil {
		t.Fatalf("Load(missing) = %v, %v; want nil, nil", e, err)
	}

	in := &Entry{Name: "prod", Engine: types.EnginePostgres, Host: "db.local", FetchedAt: time.Now().UTC().Truncate(time.Second), SlowQueryLimit: 50}
	if err := st.Save(in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := st.Load("prod")
	if err != nil || out == nil {
		t.Fatalf("Load: %v / %v", out, err)
	}
	if out.Name != "prod" || out.Engine != types.EnginePostgres || out.Host != "db.local" {
		t.Errorf("round-trip mismatch: %+v", out)
	}
	// The slow-query limit must survive the round trip: a reloaded snapshot has
	// to know how many rows it was allowed to capture.
	if out.SlowQueryLimit != 50 {
		t.Errorf("SlowQueryLimit = %d, want 50", out.SlowQueryLimit)
	}

	names, err := st.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, n := range names {
		if n == "prod" {
			found = true
		}
	}
	if !found {
		t.Errorf("List() = %v, want it to include prod", names)
	}
}
