package analyze

import (
	"strings"
	"testing"

	"github.com/djtouchette/rivet/internal/schema/types"
)

func TestDetectRedundant_PrefixIndex(t *testing.T) {
	s := &types.Schema{Tables: []types.Table{{
		Schema: "public", Name: "orders",
		Indexes: []types.Index{
			{Name: "orders_user_idx", Columns: []string{"user_id"}},
			{Name: "orders_user_tenant_idx", Columns: []string{"user_id", "tenant_id"}},
		},
	}}}
	r := DetectRedundant(s)
	if len(r) != 1 {
		t.Fatalf("want 1 redundancy, got %d: %+v", len(r), r)
	}
	if r[0].Index != "orders_user_idx" || r[0].CoveredBy != "orders_user_tenant_idx" {
		t.Errorf("unexpected redundancy: %+v", r[0])
	}
}

func TestDetectRedundant_PrimaryIsImmune(t *testing.T) {
	s := &types.Schema{Tables: []types.Table{{
		Name: "t",
		Indexes: []types.Index{
			{Name: "pk", Columns: []string{"id"}, Primary: true, Unique: true},
			{Name: "idx_id_plus", Columns: []string{"id", "x"}},
		},
	}}}
	r := DetectRedundant(s)
	if len(r) != 0 {
		t.Fatalf("primary keys should never be reported redundant: %+v", r)
	}
}

func TestDetectRedundant_PartialIndexSafe(t *testing.T) {
	s := &types.Schema{Tables: []types.Table{{
		Name: "t",
		Indexes: []types.Index{
			{Name: "full", Columns: []string{"x", "y"}},
			{Name: "partial", Columns: []string{"x"}, Where: "deleted_at IS NULL"},
		},
	}}}
	r := DetectRedundant(s)
	if len(r) != 0 {
		t.Fatalf("partial indexes aren't redundant with full ones: %+v", r)
	}
}

func TestDetectUnused_ZeroReadsWithWrites(t *testing.T) {
	s := &types.Schema{Tables: []types.Table{{
		Schema: "public", Name: "orders",
		Indexes: []types.Index{
			{Name: "orders_user_idx", Columns: []string{"user_id"}},
		},
	}}}
	usage := []types.IndexUsage{
		{Schema: "public", Table: "orders", Index: "orders_user_idx", Scans: 0, Updates: 1500},
	}
	u := DetectUnused(s, usage)
	if len(u) != 1 {
		t.Fatalf("want 1 unused, got %d", len(u))
	}
	if u[0].Writes != 1500 {
		t.Errorf("writes: %d", u[0].Writes)
	}
}

func TestDetectUnused_PrimaryIgnored(t *testing.T) {
	s := &types.Schema{Tables: []types.Table{{
		Name: "t",
		Indexes: []types.Index{
			{Name: "pk", Columns: []string{"id"}, Primary: true, Unique: true},
		},
	}}}
	usage := []types.IndexUsage{
		{Table: "t", Index: "pk", Scans: 0, Updates: 999},
	}
	if u := DetectUnused(s, usage); len(u) != 0 {
		t.Fatalf("PK should never be flagged unused: %+v", u)
	}
}

func TestDetectMissing_CombinesEngineAndCode(t *testing.T) {
	s := &types.Schema{Tables: []types.Table{{
		Schema: "public", Name: "users",
		Columns: []types.Column{{Name: "email"}, {Name: "tenant_id"}, {Name: "id"}},
		Indexes: []types.Index{
			{Name: "users_pkey", Columns: []string{"id"}, Primary: true, Unique: true},
		},
	}}}

	hints := []types.MissingIndexHint{{
		Schema: "public", Table: "users",
		EqualityColumns: []string{"email"},
		Impact:          92.0, Source: "mssql-dmv",
	}}

	queries := []types.QueryRef{{
		File: "x.cs", Lang: "csharp", Kind: "dapper",
		SQL: "SELECT id FROM users WHERE email = @e",
	}}

	m := DetectMissing(s, hints, queries)
	if len(m) == 0 {
		t.Fatal("expected at least one missing index")
	}

	// The engine hint and code analysis both point at email — they should merge.
	var found bool
	for _, c := range m {
		if strings.EqualFold(c.Table, "users") && len(c.Columns) == 1 && c.Columns[0] == "email" {
			found = true
			if c.Source != "combined" && c.Source != "mssql-dmv" && c.Source != "code-analysis" {
				t.Errorf("unexpected source %q", c.Source)
			}
		}
	}
	if !found {
		t.Errorf("users.email candidate missing: %+v", m)
	}
}

func TestBuildCoverage_FlagsUncoveredPredicate(t *testing.T) {
	s := &types.Schema{Tables: []types.Table{{
		Schema: "public", Name: "orders",
		Columns: []types.Column{{Name: "id"}, {Name: "user_id"}, {Name: "status"}},
		Indexes: []types.Index{
			{Name: "pk", Columns: []string{"id"}, Primary: true, Unique: true},
			{Name: "orders_user_idx", Columns: []string{"user_id"}},
		},
	}}}
	queries := []types.QueryRef{
		{File: "a.cs", Lang: "csharp", SQL: "SELECT * FROM orders WHERE user_id = @u"},
		{File: "b.cs", Lang: "csharp", SQL: "SELECT * FROM orders WHERE status = @s"},
	}
	rep := BuildCoverage(s, queries)
	if rep == nil || len(rep.Tables) != 1 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	var userCovered, statusCovered bool
	for _, p := range rep.Tables[0].Predicates {
		for _, col := range p.Columns {
			if col == "user_id" {
				userCovered = p.Covered
			}
			if col == "status" {
				statusCovered = p.Covered
			}
		}
	}
	if !userCovered {
		t.Errorf("user_id should be covered by orders_user_idx")
	}
	if statusCovered {
		t.Errorf("status should NOT be covered")
	}
}
