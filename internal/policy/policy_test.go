package policy

import (
	"testing"

	"github.com/djtouchette/rivet/internal/capabilities"
)

func fakeEnv(vars map[string]string) EnvLookup {
	return func(key string) (string, bool) {
		v, ok := vars[key]
		return v, ok
	}
}

func TestCheck_NoRules(t *testing.T) {
	cap := &capabilities.Capability{
		Name:   "test",
		Kind:   capabilities.KindProjectCommand,
		Safety: capabilities.SafetyLevelSafe,
	}
	violations := Check(nil, cap, nil)
	if len(violations) != 0 {
		t.Errorf("expected no violations, got %d", len(violations))
	}
}

func TestCheck_RequireEnv_Missing(t *testing.T) {
	rules := []Rule{
		{
			Name:       "require-prod",
			Match:      Match{Safety: "dangerous"},
			RequireEnv: []string{"PROD_APPROVED"},
		},
	}
	cap := &capabilities.Capability{
		Name:   "db.migrate",
		Kind:   capabilities.KindProjectCommand,
		Safety: capabilities.SafetyLevelDangerous,
	}

	violations := Check(rules, cap, fakeEnv(map[string]string{}))
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(violations))
	}
	if violations[0].Rule != "require-prod" {
		t.Errorf("expected rule 'require-prod', got %q", violations[0].Rule)
	}
}

func TestCheck_RequireEnv_Present(t *testing.T) {
	rules := []Rule{
		{
			Name:       "require-prod",
			Match:      Match{Safety: "dangerous"},
			RequireEnv: []string{"PROD_APPROVED"},
		},
	}
	cap := &capabilities.Capability{
		Name:   "db.migrate",
		Kind:   capabilities.KindProjectCommand,
		Safety: capabilities.SafetyLevelDangerous,
	}

	violations := Check(rules, cap, fakeEnv(map[string]string{"PROD_APPROVED": "yes"}))
	if len(violations) != 0 {
		t.Errorf("expected no violations, got %d", len(violations))
	}
}

func TestCheck_RequireEnv_Empty(t *testing.T) {
	rules := []Rule{
		{
			Name:       "require-prod",
			Match:      Match{Safety: "dangerous"},
			RequireEnv: []string{"PROD_APPROVED"},
		},
	}
	cap := &capabilities.Capability{
		Name:   "db.migrate",
		Kind:   capabilities.KindProjectCommand,
		Safety: capabilities.SafetyLevelDangerous,
	}

	// Set but empty should also violate.
	violations := Check(rules, cap, fakeEnv(map[string]string{"PROD_APPROVED": ""}))
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation for empty env var, got %d", len(violations))
	}
}

func TestCheck_DenyEnv_Set(t *testing.T) {
	rules := []Rule{
		{
			Name:    "no-ci",
			Match:   Match{Safety: "dangerous"},
			DenyEnv: []string{"CI"},
		},
	}
	cap := &capabilities.Capability{
		Name:   "db.migrate",
		Kind:   capabilities.KindProjectCommand,
		Safety: capabilities.SafetyLevelDangerous,
	}

	violations := Check(rules, cap, fakeEnv(map[string]string{"CI": "true"}))
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(violations))
	}
}

func TestCheck_DenyEnv_NotSet(t *testing.T) {
	rules := []Rule{
		{
			Name:    "no-ci",
			Match:   Match{Safety: "dangerous"},
			DenyEnv: []string{"CI"},
		},
	}
	cap := &capabilities.Capability{
		Name:   "db.migrate",
		Kind:   capabilities.KindProjectCommand,
		Safety: capabilities.SafetyLevelDangerous,
	}

	violations := Check(rules, cap, fakeEnv(map[string]string{}))
	if len(violations) != 0 {
		t.Errorf("expected no violations, got %d", len(violations))
	}
}

func TestCheck_MatchBySafety_NoMatch(t *testing.T) {
	rules := []Rule{
		{
			Name:       "dangerous-only",
			Match:      Match{Safety: "dangerous"},
			RequireEnv: []string{"NEVER_SET_XYZ"},
		},
	}
	cap := &capabilities.Capability{
		Name:   "list.things",
		Kind:   capabilities.KindProjectCommand,
		Safety: capabilities.SafetyLevelSafe,
	}

	// Rule shouldn't match a safe capability.
	violations := Check(rules, cap, fakeEnv(map[string]string{}))
	if len(violations) != 0 {
		t.Errorf("expected no violations (rule shouldn't match safe cap), got %d", len(violations))
	}
}

func TestCheck_MatchByKind(t *testing.T) {
	rules := []Rule{
		{
			Name:       "tools-require-auth",
			Match:      Match{Kind: "tool"},
			RequireEnv: []string{"AUTH_TOKEN"},
		},
	}
	cap := &capabilities.Capability{
		Name:   "vaulty.exec",
		Kind:   capabilities.KindTool,
		Safety: capabilities.SafetyLevelGuarded,
	}

	violations := Check(rules, cap, fakeEnv(map[string]string{}))
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(violations))
	}
}

func TestCheck_MatchByCapabilityName(t *testing.T) {
	rules := []Rule{
		{
			Name:       "gate-specific",
			Match:      Match{Capabilities: []string{"db.migrate", "search.reindex"}},
			RequireEnv: []string{"PROD_APPROVED"},
		},
	}

	// Should match.
	cap1 := &capabilities.Capability{
		Name:   "db.migrate",
		Kind:   capabilities.KindProjectCommand,
		Safety: capabilities.SafetyLevelDangerous,
	}
	v1 := Check(rules, cap1, fakeEnv(map[string]string{}))
	if len(v1) != 1 {
		t.Errorf("expected 1 violation for db.migrate, got %d", len(v1))
	}

	// Should not match.
	cap2 := &capabilities.Capability{
		Name:   "db.summary",
		Kind:   capabilities.KindProjectCommand,
		Safety: capabilities.SafetyLevelSafe,
	}
	v2 := Check(rules, cap2, fakeEnv(map[string]string{}))
	if len(v2) != 0 {
		t.Errorf("expected no violations for db.summary, got %d", len(v2))
	}
}

func TestCheck_MultipleRules(t *testing.T) {
	rules := []Rule{
		{
			Name:       "require-prod",
			Match:      Match{Safety: "dangerous"},
			RequireEnv: []string{"PROD_APPROVED"},
		},
		{
			Name:    "no-ci",
			Match:   Match{Safety: "dangerous"},
			DenyEnv: []string{"CI"},
		},
	}
	cap := &capabilities.Capability{
		Name:   "db.migrate",
		Kind:   capabilities.KindProjectCommand,
		Safety: capabilities.SafetyLevelDangerous,
	}

	violations := Check(rules, cap, fakeEnv(map[string]string{"CI": "true"}))
	if len(violations) != 2 {
		t.Fatalf("expected 2 violations, got %d", len(violations))
	}
}

func TestCheck_ANDMatchLogic(t *testing.T) {
	rules := []Rule{
		{
			Name:       "specific-dangerous-tool",
			Match:      Match{Safety: "dangerous", Kind: "tool"},
			RequireEnv: []string{"SPECIAL"},
		},
	}

	// Matches safety but not kind — should NOT match.
	cap := &capabilities.Capability{
		Name:   "db.migrate",
		Kind:   capabilities.KindProjectCommand,
		Safety: capabilities.SafetyLevelDangerous,
	}
	violations := Check(rules, cap, fakeEnv(map[string]string{}))
	if len(violations) != 0 {
		t.Errorf("expected no violations (kind mismatch), got %d", len(violations))
	}
}

func TestCheckEnv(t *testing.T) {
	rule := &Rule{
		Name:       "test",
		RequireEnv: []string{"MISSING"},
		DenyEnv:    []string{"PRESENT"},
	}

	violations := CheckEnv(rule, fakeEnv(map[string]string{"PRESENT": "yes"}))
	if len(violations) != 2 {
		t.Fatalf("expected 2 violations, got %d", len(violations))
	}
}

func TestFormatViolations(t *testing.T) {
	violations := []Violation{
		{Rule: "r1", Message: "missing X"},
		{Rule: "r2", Message: "blocked by Y"},
	}
	s := FormatViolations(violations)
	if s == "" {
		t.Error("expected non-empty string")
	}
}

func TestFormatViolations_Empty(t *testing.T) {
	s := FormatViolations(nil)
	if s != "" {
		t.Errorf("expected empty string, got %q", s)
	}
}
