package config

import (
	"strings"
	"testing"

	"github.com/djtouchette/rivet/internal/schema/types"
)

func TestBuildDSN_Postgres(t *testing.T) {
	d := &Database{
		Name: "prod", Engine: types.EnginePostgres,
		Host: "db.example.com", Port: 5433,
		User: "ro", Password: "secret", Database: "app",
		SSLMode: "require", Schema: "analytics",
	}
	dsn, err := d.BuildDSN()
	if err != nil {
		t.Fatalf("BuildDSN: %v", err)
	}
	for _, want := range []string{"postgres://", "ro:secret@", "db.example.com:5433", "/app", "sslmode=require", "search_path=analytics"} {
		if !strings.Contains(dsn, want) {
			t.Errorf("dsn %q missing %q", dsn, want)
		}
	}
}

func TestBuildDSN_PostgresDefaults(t *testing.T) {
	d := &Database{Name: "p", Engine: types.EnginePostgres, User: "u", Database: "app"}
	dsn, err := d.BuildDSN()
	if err != nil {
		t.Fatalf("BuildDSN: %v", err)
	}
	// Default host localhost, port 5432, sslmode prefer.
	for _, want := range []string{"localhost:5432", "sslmode=prefer"} {
		if !strings.Contains(dsn, want) {
			t.Errorf("dsn %q missing default %q", dsn, want)
		}
	}
}

func TestBuildDSN_MSSQL(t *testing.T) {
	d := &Database{Name: "m", Engine: types.EngineMSSQL, Host: "mssql.local", User: "sa", Password: "pw", Database: "Sales"}
	dsn, err := d.BuildDSN()
	if err != nil {
		t.Fatalf("BuildDSN: %v", err)
	}
	for _, want := range []string{"sqlserver://", "sa:pw@", "mssql.local:1433", "database=Sales"} {
		if !strings.Contains(dsn, want) {
			t.Errorf("dsn %q missing %q", dsn, want)
		}
	}
}

func TestBuildDSN_EnvExpansion(t *testing.T) {
	t.Setenv("TEST_PG_PASS", "from-env")
	d := &Database{Name: "p", Engine: types.EnginePostgres, User: "u", Password: "${TEST_PG_PASS}", Database: "app"}
	dsn, err := d.BuildDSN()
	if err != nil {
		t.Fatalf("BuildDSN: %v", err)
	}
	if !strings.Contains(dsn, "u:from-env@") {
		t.Errorf("env var not expanded in dsn %q", dsn)
	}
	if strings.Contains(dsn, "TEST_PG_PASS") {
		t.Errorf("dsn leaked the literal env reference: %q", dsn)
	}
}

func TestBuildDSN_ExplicitDSNWins(t *testing.T) {
	t.Setenv("TEST_DSN_HOST", "myhost")
	d := &Database{Name: "p", Engine: types.EnginePostgres, DSN: "postgres://u@${TEST_DSN_HOST}:5432/app"}
	dsn, err := d.BuildDSN()
	if err != nil {
		t.Fatalf("BuildDSN: %v", err)
	}
	if dsn != "postgres://u@myhost:5432/app" {
		t.Errorf("explicit DSN = %q, want env-expanded literal", dsn)
	}
}

func TestBuildDSN_Errors(t *testing.T) {
	if _, err := (&Database{Name: "x"}).BuildDSN(); err == nil {
		t.Error("expected error when engine is unset")
	}
	if _, err := (&Database{Name: "x", Engine: types.Engine("oracle")}).BuildDSN(); err == nil {
		t.Error("expected error for unsupported engine")
	}
}

func TestRedacted(t *testing.T) {
	d := &Database{Name: "p", Engine: types.EnginePostgres, User: "u", Password: "secret"}
	r := d.Redacted()
	if r.Password != "" {
		t.Errorf("redacted password = %q, want empty", r.Password)
	}
	if d.Password != "secret" {
		t.Error("Redacted mutated the original password")
	}
	if r.User != "u" || r.Name != "p" {
		t.Error("Redacted dropped non-secret fields")
	}
}

func TestResolveDatabase(t *testing.T) {
	none := &Config{}
	if _, err := none.ResolveDatabase(""); err == nil {
		t.Error("expected error with no databases configured")
	}

	multi := &Config{Databases: []Database{
		{Name: "a", Engine: types.EnginePostgres},
		{Name: "b", Engine: types.EnginePostgres, Default: true},
	}}
	if d, err := multi.ResolveDatabase(""); err != nil || d.Name != "b" {
		t.Errorf("empty name should resolve the default (b); got %v / %v", d, err)
	}
	if d, err := multi.ResolveDatabase("a"); err != nil || d.Name != "a" {
		t.Errorf("by-name resolve failed: %v / %v", d, err)
	}
	if _, err := multi.ResolveDatabase("missing"); err == nil {
		t.Error("expected error for unknown database name")
	}

	// Multiple with no default and no name → ambiguous error.
	ambig := &Config{Databases: []Database{{Name: "a"}, {Name: "b"}}}
	if _, err := ambig.ResolveDatabase(""); err == nil {
		t.Error("expected ambiguity error when multiple dbs and none default")
	}

	// Single database is treated as the default.
	single := &Config{Databases: []Database{{Name: "only", Engine: types.EnginePostgres}}}
	if d, err := single.ResolveDatabase(""); err != nil || d.Name != "only" {
		t.Errorf("single db should resolve without --db; got %v / %v", d, err)
	}
}

func TestAllDirs(t *testing.T) {
	m := MigrationsConfig{Dir: "db/migrations", Dirs: []string{"extra/a", "extra/b"}}
	got := m.AllDirs()
	want := []string{"db/migrations", "extra/a", "extra/b"}
	if len(got) != len(want) {
		t.Fatalf("AllDirs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("AllDirs[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
