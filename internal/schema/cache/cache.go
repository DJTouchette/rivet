// Package cache stores schema snapshots as JSON files under .rivet/schema/.
// Each configured database gets one file. Cache is advisory: if it's missing
// or stale the caller re-queries the live DB.
package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/djtouchette/rivet/internal/schema/types"
)

// Entry is one persisted snapshot.
type Entry struct {
	Name        string                   `json:"name"`
	Engine      types.Engine             `json:"engine"`
	Host        string                   `json:"host,omitempty"`
	FetchedAt   time.Time                `json:"fetched_at"`
	Schema      *types.Schema            `json:"schema,omitempty"`
	IndexUsage  []types.IndexUsage       `json:"index_usage,omitempty"`
	Hints       []types.MissingIndexHint `json:"hints,omitempty"`
	SlowQueries []types.SlowQuery        `json:"slow_queries,omitempty"`

	// SlowQueryLimit is the row cap SlowQueries was captured with. Stored so a
	// later request for *more* rows than were ever fetched can tell the
	// difference between "the server had no more" and "we never asked".
	SlowQueryLimit int `json:"slow_query_limit,omitempty"`
}

// Age reports how long ago the snapshot was taken, relative to now.
func (e *Entry) Age(now time.Time) time.Duration {
	if e == nil || e.FetchedAt.IsZero() {
		return 0
	}
	d := now.Sub(e.FetchedAt)
	if d < 0 {
		// Clock skew (or a snapshot copied from another machine). Treat the
		// future as "just fetched" rather than reporting a negative age.
		return 0
	}
	return d
}

// IsStale reports whether the snapshot has outlived maxAge. A snapshot with no
// FetchedAt is always stale: an unknown age is not a safe age. maxAge of 0
// means nothing cached is ever fresh.
func (e *Entry) IsStale(now time.Time, maxAge time.Duration) bool {
	if e == nil || e.FetchedAt.IsZero() {
		return true
	}
	return e.Age(now) >= maxAge
}

// Store is a filesystem-backed cache. Zero value is unusable — call Open.
type Store struct {
	dir string
}

// Default returns the conventional cache directory.
func Default() string { return filepath.Join(".rivet", "schema") }

// Open returns a Store rooted at the given directory, creating it if missing.
func Open(dir string) (*Store, error) {
	if dir == "" {
		dir = Default()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating cache dir %s: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}

// Path returns the on-disk path for the given database name.
func (s *Store) Path(name string) string {
	return filepath.Join(s.dir, safeFileName(name)+".json")
}

// Load reads a snapshot. Returns (nil, nil) if the file doesn't exist.
func (s *Store) Load(name string) (*Entry, error) {
	data, err := os.ReadFile(s.Path(name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var e Entry
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, fmt.Errorf("parsing cache entry %s: %w", name, err)
	}
	return &e, nil
}

// Save writes a snapshot (overwrites any existing).
func (s *Store) Save(e *Entry) error {
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.Path(e.Name) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path(e.Name))
}

// List returns the names of every cached database.
func (s *Store) List() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if filepath.Ext(name) != ".json" {
			continue
		}
		out = append(out, name[:len(name)-len(".json")])
	}
	return out, nil
}

// safeFileName replaces filesystem-hostile chars with underscores.
func safeFileName(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "default"
	}
	return string(out)
}
