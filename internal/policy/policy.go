package policy

import (
	"fmt"
	"os"
	"strings"

	"github.com/djtouchette/rivet/internal/capabilities"
)

// Rule is a policy rule declared in config.
type Rule struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description,omitempty"`
	Match       Match    `yaml:"match"`
	RequireEnv  []string `yaml:"require_env,omitempty"`
	DenyEnv     []string `yaml:"deny_env,omitempty"`
}

// Match describes which capabilities a rule applies to.
// All non-empty fields must match (AND logic).
type Match struct {
	Safety       string   `yaml:"safety,omitempty"`
	Kind         string   `yaml:"kind,omitempty"`
	Capabilities []string `yaml:"capabilities,omitempty"`
}

// Violation is a single policy violation.
type Violation struct {
	Rule    string
	Message string
}

func (v Violation) String() string {
	return fmt.Sprintf("policy %q: %s", v.Rule, v.Message)
}

// EnvLookup is a function that returns the value and presence of an env var.
// Defaults to os.LookupEnv when nil.
type EnvLookup func(key string) (string, bool)

// Check evaluates all rules against a capability and returns any violations.
// If envFn is nil, os.LookupEnv is used.
func Check(rules []Rule, cap *capabilities.Capability, envFn EnvLookup) []Violation {
	if envFn == nil {
		envFn = os.LookupEnv
	}

	var violations []Violation
	for _, rule := range rules {
		if !matches(&rule, cap) {
			continue
		}
		violations = append(violations, enforce(&rule, envFn)...)
	}
	return violations
}

// FormatViolations returns a human-readable error string from violations.
func FormatViolations(violations []Violation) string {
	if len(violations) == 0 {
		return ""
	}
	msgs := make([]string, len(violations))
	for i, v := range violations {
		msgs[i] = v.String()
	}
	return strings.Join(msgs, "; ")
}

// CheckEnv evaluates just the environment constraints of a rule,
// ignoring capability matching. Useful for showing current env state.
func CheckEnv(rule *Rule, envFn EnvLookup) []Violation {
	if envFn == nil {
		envFn = os.LookupEnv
	}
	return enforce(rule, envFn)
}

// matches returns true if the rule applies to the given capability.
func matches(rule *Rule, cap *capabilities.Capability) bool {
	if rule.Match.Safety != "" && string(cap.Safety) != rule.Match.Safety {
		return false
	}
	if rule.Match.Kind != "" && string(cap.Kind) != rule.Match.Kind {
		return false
	}
	if len(rule.Match.Capabilities) > 0 {
		found := false
		for _, name := range rule.Match.Capabilities {
			if name == cap.Name {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// enforce checks the env constraints of a matched rule.
func enforce(rule *Rule, envFn EnvLookup) []Violation {
	var violations []Violation

	for _, key := range rule.RequireEnv {
		val, ok := envFn(key)
		if !ok || val == "" {
			violations = append(violations, Violation{
				Rule:    rule.Name,
				Message: fmt.Sprintf("required env var %s is not set", key),
			})
		}
	}

	for _, key := range rule.DenyEnv {
		if val, ok := envFn(key); ok && val != "" {
			violations = append(violations, Violation{
				Rule:    rule.Name,
				Message: fmt.Sprintf("env var %s must not be set", key),
			})
		}
	}

	return violations
}
