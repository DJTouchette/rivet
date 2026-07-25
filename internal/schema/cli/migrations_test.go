package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/djtouchette/rivet/internal/schema/types"
)

// migrationsOutput mirrors the JSON `schema migrations` prints. The command
// marshals migrations.Result, whose fields are untagged, hence the capitalised
// keys — asserting on them here pins the shape agents actually parse.
type migrationsOutput struct {
	Schema  types.Schema            `json:"Schema"`
	Summary types.MigrationsSummary `json:"Summary"`
}

// writeMigrationsConfig writes a config whose schema.migrations section points
// at roots[0] via `dir` and the rest via `dirs` — the exact split AllDirs
// flattens, and the one `schema migrations` used to truncate to its first entry.
func writeMigrationsConfig(t *testing.T, dir string, roots []string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("schema:\n  migrations:\n    dialect: postgres\n")
	if len(roots) > 0 {
		b.WriteString("    dir: " + roots[0] + "\n")
	}
	if len(roots) > 1 {
		b.WriteString("    dirs:\n")
		for _, r := range roots[1:] {
			b.WriteString("      - " + r + "\n")
		}
	}
	b.WriteString("  databases:\n    - name: prod\n      engine: postgres\n      host: db.local\n      default: true\n")
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeMigration(t *testing.T, root, name, body string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Bug 2 end-to-end: `schema migrations` used AllDirs()[0] and `schema overview`
// stopped at the first root that parsed, so a project with migrations split
// across two roots was handed half a schema with nothing to say so.
func TestMigrationCommandsReadEveryRoot(t *testing.T) {
	cases := []struct {
		name string
		// command under test; both consume the same merged result.
		args []string
		// missingSecondRoot makes the second configured root nonexistent.
		missingSecondRoot bool
		wantTables        int
		wantFiles         int
		wantSources       int
		wantFailedSources int
	}{
		{
			name:        "migrations merges both roots",
			args:        []string{"migrations"},
			wantTables:  2,
			wantFiles:   2,
			wantSources: 2,
		},
		{
			name:        "overview merges both roots",
			args:        []string{"overview"},
			wantTables:  2,
			wantFiles:   2,
			wantSources: 2,
		},
		{
			// A root that can't be read must not silently vanish the way it did
			// before: the good root is still merged, the bad one is named.
			name:              "migrations reports a root it could not read",
			args:              []string{"migrations"},
			missingSecondRoot: true,
			wantTables:        1,
			wantFiles:         1,
			wantSources:       2,
			wantFailedSources: 1,
		},
		{
			name:              "overview reports a root it could not read",
			args:              []string{"overview"},
			missingSecondRoot: true,
			wantTables:        1,
			wantFiles:         1,
			wantSources:       2,
			wantFailedSources: 1,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := t.TempDir()
			rootA := filepath.Join(base, "a")
			rootB := filepath.Join(base, "b")
			writeMigration(t, rootA, "001_users.sql", `CREATE TABLE users (id INT PRIMARY KEY);`)
			if !c.missingSecondRoot {
				writeMigration(t, rootB, "002_orders.sql", `CREATE TABLE orders (id INT PRIMARY KEY);`)
			}

			cfgPath := writeMigrationsConfig(t, base, []string{rootA, rootB})
			stubCatalog(t, &fakeCatalog{})

			stdout, stderr, err := runRoot(t, append(c.args,
				"--config", cfgPath, "--cache-dir", t.TempDir())...)
			if err != nil {
				t.Fatalf("%v: %v", c.args, err)
			}

			var summary types.MigrationsSummary
			var tables int
			if c.args[0] == "overview" {
				var ov types.Overview
				if err := json.Unmarshal([]byte(stdout), &ov); err != nil {
					t.Fatalf("parsing overview %q: %v", stdout, err)
				}
				if ov.Migrations == nil {
					t.Fatal("overview reported no migrations at all")
				}
				summary = *ov.Migrations
				tables = summary.Tables
			} else {
				var out migrationsOutput
				if err := json.Unmarshal([]byte(stdout), &out); err != nil {
					t.Fatalf("parsing migrations %q: %v", stdout, err)
				}
				summary = out.Summary
				tables = len(out.Schema.Tables)
			}

			if tables != c.wantTables {
				t.Errorf("got %d tables, want %d — a root was dropped", tables, c.wantTables)
			}
			if summary.Files != c.wantFiles {
				t.Errorf("Files = %d, want %d", summary.Files, c.wantFiles)
			}
			if len(summary.Sources) != c.wantSources {
				t.Errorf("Sources = %+v, want %d entries so the output shows which roots contributed",
					summary.Sources, c.wantSources)
			}
			failed := 0
			for _, s := range summary.Sources {
				if s.Error != "" {
					failed++
				}
			}
			if failed != c.wantFailedSources {
				t.Errorf("%d failed sources, want %d (%+v)", failed, c.wantFailedSources, summary.Sources)
			}
			// A skipped root has to reach the operator, not just the JSON: JSON
			// mode puts it on stderr so stdout stays one parseable document.
			if c.wantFailedSources > 0 && !strings.Contains(stderr, "WARNING") {
				t.Errorf("stderr %q does not warn that a migration root was unreadable", stderr)
			}
		})
	}
}

// Human mode has to show the per-root breakdown too; a merged total alone can't
// reveal that one of two roots contributed nothing.
func TestMigrationsHumanShowsEveryRoot(t *testing.T) {
	base := t.TempDir()
	rootA := filepath.Join(base, "a")
	rootB := filepath.Join(base, "b")
	writeMigration(t, rootA, "001_users.sql", `CREATE TABLE users (id INT PRIMARY KEY);`)
	writeMigration(t, rootB, "002_orders.sql", `CREATE TABLE orders (id INT PRIMARY KEY);`)
	cfgPath := writeMigrationsConfig(t, base, []string{rootA, rootB})
	stubCatalog(t, &fakeCatalog{})

	for _, args := range [][]string{{"migrations", "--human"}, {"overview", "--human"}} {
		stdout, _, err := runRoot(t, append(args, "--config", cfgPath, "--cache-dir", t.TempDir())...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.Contains(stdout, rootA) || !strings.Contains(stdout, rootB) {
			t.Errorf("%v output does not name both roots:\n%s", args, stdout)
		}
	}
}

// Migrations split across roots that reuse the same filenames have no single
// correct order, so the ambiguity is reported rather than resolved in silence.
func TestMigrationsWarnsOnDuplicateFilenames(t *testing.T) {
	base := t.TempDir()
	rootA := filepath.Join(base, "a")
	rootB := filepath.Join(base, "b")
	writeMigration(t, rootA, "001.sql", `CREATE TABLE users (id INT PRIMARY KEY);`)
	writeMigration(t, rootB, "001.sql", `CREATE TABLE orders (id INT PRIMARY KEY);`)
	cfgPath := writeMigrationsConfig(t, base, []string{rootA, rootB})
	stubCatalog(t, &fakeCatalog{})

	stdout, stderr, err := runRoot(t, "migrations", "--config", cfgPath, "--cache-dir", t.TempDir())
	if err != nil {
		t.Fatalf("migrations: %v", err)
	}
	var out migrationsOutput
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("parsing migrations %q: %v", stdout, err)
	}
	if len(out.Summary.Warnings) == 0 {
		t.Error("duplicate migration filenames across roots produced no warning")
	}
	if !strings.Contains(stderr, "001.sql") {
		t.Errorf("stderr %q does not name the colliding file", stderr)
	}
	// Both roots still contribute — the warning is about ordering, not exclusion.
	if len(out.Schema.Tables) != 2 {
		t.Errorf("got %d tables, want both roots merged", len(out.Schema.Tables))
	}
}
