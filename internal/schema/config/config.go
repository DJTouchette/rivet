// Package config parses the schema-intel section of .rivet/config.yaml.
// It's intentionally separate from internal/config so the schema subsystem
// can be extracted to its own repo without dragging rivet-level config along.
package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/djtouchette/rivet/internal/schema/types"
)

// Config is the top-level schema configuration.
type Config struct {
	// Databases is the list of configured connections.
	Databases []Database `yaml:"databases,omitempty" json:"databases,omitempty"`

	// Migrations locates on-disk SQL migration files for static analysis.
	Migrations MigrationsConfig `yaml:"migrations,omitempty" json:"migrations,omitempty"`

	// CodeScan controls query extraction from application source.
	CodeScan CodeScanConfig `yaml:"code_scan,omitempty" json:"code_scan,omitempty"`

	// Cache controls how long an on-disk snapshot may be served before the
	// database is re-read.
	Cache CacheConfig `yaml:"cache,omitempty" json:"cache,omitempty"`
}

// Database is a single connection target.
type Database struct {
	Name     string       `yaml:"name"                json:"name"`
	Engine   types.Engine `yaml:"engine"              json:"engine"`
	DSN      string       `yaml:"dsn,omitempty"       json:"dsn,omitempty"`
	Host     string       `yaml:"host,omitempty"      json:"host,omitempty"`
	Port     int          `yaml:"port,omitempty"      json:"port,omitempty"`
	User     string       `yaml:"user,omitempty"      json:"user,omitempty"`
	Password string       `yaml:"password,omitempty"  json:"-"` // redact in JSON
	Database string       `yaml:"database,omitempty"  json:"database,omitempty"`
	SSLMode  string       `yaml:"sslmode,omitempty"   json:"sslmode,omitempty"` // postgres only
	Schema   string       `yaml:"schema,omitempty"    json:"schema,omitempty"`  // default search schema

	// Default marks this database as the one used when no --db flag is passed.
	Default bool `yaml:"default,omitempty"   json:"default,omitempty"`
}

// MigrationsConfig points at SQL migration files.
type MigrationsConfig struct {
	Dir     string   `yaml:"dir,omitempty"     json:"dir,omitempty"`
	Dirs    []string `yaml:"dirs,omitempty"    json:"dirs,omitempty"`
	Dialect string   `yaml:"dialect,omitempty" json:"dialect,omitempty"` // hint: "postgres" or "mssql"
}

// AllDirs returns Dir plus Dirs.
func (m MigrationsConfig) AllDirs() []string {
	var out []string
	if m.Dir != "" {
		out = append(out, m.Dir)
	}
	out = append(out, m.Dirs...)
	return out
}

// CodeScanConfig controls SQL extraction from application source.
type CodeScanConfig struct {
	// Roots limits the scan to these directories. Defaults to the repo root.
	Roots []string `yaml:"roots,omitempty" json:"roots,omitempty"`

	// Include is a list of glob patterns to include (applied after default exts).
	Include []string `yaml:"include,omitempty" json:"include,omitempty"`

	// Exclude is a list of glob patterns to skip (e.g. ["**/node_modules/**"]).
	Exclude []string `yaml:"exclude,omitempty" json:"exclude,omitempty"`

	// Languages limits extraction to specific languages
	// (valid: "csharp", "go", "python", "node"). Default: all.
	Languages []string `yaml:"languages,omitempty" json:"languages,omitempty"`
}

// DefaultCacheMaxAge is how long a snapshot may be served before it is
// re-read from the database. A day is chosen because the things a snapshot
// carries move on very different clocks: DDL changes land with a deploy, but
// index usage counters and slow-query stats drift continuously. Anything
// longer and "unused index" verdicts start describing last week's traffic;
// anything shorter and ordinary interactive use pays a dial on every command.
const DefaultCacheMaxAge = 24 * time.Hour

// CacheConfig tunes snapshot freshness.
type CacheConfig struct {
	// MaxAge is a Go duration string ("30m", "12h", "7d" is NOT valid — use
	// "168h"). Empty means DefaultCacheMaxAge. "0s" disables caching for
	// reads: every command re-reads the database and the snapshot becomes a
	// pure write-through fallback for when the DB is unreachable.
	MaxAge string `yaml:"max_age,omitempty" json:"max_age,omitempty"`
}

// MaxAgeDuration resolves MaxAge, applying the default when unset. A bad value
// is an error rather than a silent fallback: silently ignoring a misconfigured
// TTL would reintroduce exactly the staleness this setting exists to prevent.
func (c CacheConfig) MaxAgeDuration() (time.Duration, error) {
	if strings.TrimSpace(c.MaxAge) == "" {
		return DefaultCacheMaxAge, nil
	}
	d, err := time.ParseDuration(strings.TrimSpace(c.MaxAge))
	if err != nil {
		return 0, fmt.Errorf("schema.cache.max_age %q: %w (want a Go duration like \"30m\" or \"12h\")", c.MaxAge, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("schema.cache.max_age %q: must not be negative", c.MaxAge)
	}
	return d, nil
}

// Default returns an empty config with sensible defaults applied.
func Default() *Config {
	return &Config{}
}

// Load reads .rivet/config.yaml and extracts the `schema:` section.
// Returns a default config if the file or section is absent.
func Load(rivetConfigPath string) (*Config, error) {
	if rivetConfigPath == "" {
		rivetConfigPath = filepath.Join(".rivet", "config.yaml")
	}

	data, err := os.ReadFile(rivetConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return nil, fmt.Errorf("reading %s: %w", rivetConfigPath, err)
	}

	var wrapper struct {
		Schema *Config `yaml:"schema"`
	}
	if err := yaml.Unmarshal(data, &wrapper); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", rivetConfigPath, err)
	}

	if wrapper.Schema == nil {
		return Default(), nil
	}
	return wrapper.Schema, nil
}

// ResolveDatabase finds a database by name, or returns the default when name is "".
func (c *Config) ResolveDatabase(name string) (*Database, error) {
	if len(c.Databases) == 0 {
		return nil, fmt.Errorf("no databases configured (add `schema.databases` to .rivet/config.yaml)")
	}

	if name == "" {
		for i := range c.Databases {
			if c.Databases[i].Default {
				return &c.Databases[i], nil
			}
		}
		// If only one is configured, treat it as the default.
		if len(c.Databases) == 1 {
			return &c.Databases[0], nil
		}
		names := make([]string, len(c.Databases))
		for i, d := range c.Databases {
			names[i] = d.Name
		}
		return nil, fmt.Errorf("multiple databases configured (%s) but none marked default — pass --db <name>", strings.Join(names, ", "))
	}

	for i := range c.Databases {
		if c.Databases[i].Name == name {
			return &c.Databases[i], nil
		}
	}
	return nil, fmt.Errorf("database %q not found in config", name)
}

// BuildDSN returns a database/sql-ready connection string, expanding ${ENV_VAR}
// references and substituting secrets from the environment. Passwords are
// never reflected back in errors or logs.
func (d *Database) BuildDSN() (string, error) {
	if d.Engine == "" {
		return "", fmt.Errorf("database %q: engine not set", d.Name)
	}

	// Explicit DSN wins. Still expand env vars.
	if d.DSN != "" {
		return os.ExpandEnv(d.DSN), nil
	}

	host := d.Host
	if host == "" {
		host = "localhost"
	}

	// Password from env: allow literal ${VAR} or $VAR in the yaml for secret indirection.
	pw := os.ExpandEnv(d.Password)
	user := os.ExpandEnv(d.User)
	dbname := os.ExpandEnv(d.Database)

	switch d.Engine {
	case types.EnginePostgres:
		port := d.Port
		if port == 0 {
			port = 5432
		}
		u := &url.URL{
			Scheme: "postgres",
			Host:   fmt.Sprintf("%s:%d", host, port),
			Path:   "/" + dbname,
		}
		if user != "" {
			if pw != "" {
				u.User = url.UserPassword(user, pw)
			} else {
				u.User = url.User(user)
			}
		}
		q := u.Query()
		if d.SSLMode != "" {
			q.Set("sslmode", d.SSLMode)
		} else {
			q.Set("sslmode", "prefer")
		}
		if d.Schema != "" {
			q.Set("search_path", d.Schema)
		}
		u.RawQuery = q.Encode()
		return u.String(), nil

	case types.EngineMSSQL:
		port := d.Port
		if port == 0 {
			port = 1433
		}
		q := url.Values{}
		if dbname != "" {
			q.Set("database", dbname)
		}
		u := &url.URL{
			Scheme:   "sqlserver",
			Host:     fmt.Sprintf("%s:%d", host, port),
			RawQuery: q.Encode(),
		}
		if user != "" {
			if pw != "" {
				u.User = url.UserPassword(user, pw)
			} else {
				u.User = url.User(user)
			}
		}
		return u.String(), nil

	default:
		return "", fmt.Errorf("database %q: unsupported engine %q (want postgres|mssql)", d.Name, d.Engine)
	}
}

// Redacted returns a copy with Password blanked, safe to log or return in errors.
func (d *Database) Redacted() Database {
	c := *d
	c.Password = ""
	return c
}
