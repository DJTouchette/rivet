package capabilities

import (
	"strings"
	"testing"
)

func countPrefix(caps []Capability, prefix string) int {
	n := 0
	for _, c := range caps {
		if strings.HasPrefix(c.Name, prefix) {
			n++
		}
	}
	return n
}

func TestBuiltinsForGating(t *testing.T) {
	full := Builtins()
	schemaTotal := countPrefix(full, "schema.")
	vaultyTotal := countPrefix(full, "vaulty.")
	if schemaTotal == 0 || vaultyTotal == 0 {
		t.Fatalf("builtins should contain both groups, got %d schema / %d vaulty", schemaTotal, vaultyTotal)
	}

	tests := []struct {
		name       string
		groups     BuiltinGroups
		wantSchema int
		wantVaulty int
	}{
		{"bare project", BuiltinGroups{}, 0, 0},
		{"schema configured", BuiltinGroups{Schema: true}, schemaTotal, 0},
		{"vaulty configured", BuiltinGroups{Vaulty: true}, 0, vaultyTotal},
		{"both configured", BuiltinGroups{Schema: true, Vaulty: true}, schemaTotal, vaultyTotal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuiltinsFor(tt.groups)

			if n := countPrefix(got, "schema."); n != tt.wantSchema {
				t.Errorf("schema.* count = %d, want %d", n, tt.wantSchema)
			}
			if n := countPrefix(got, "vaulty."); n != tt.wantVaulty {
				t.Errorf("vaulty.* count = %d, want %d", n, tt.wantVaulty)
			}

			// recon and witness need no configuration — they are the product,
			// and must survive every combination of gates.
			for _, prefix := range []string{"recon.", "witness."} {
				if n, want := countPrefix(got, prefix), countPrefix(full, prefix); n != want {
					t.Errorf("%s* count = %d, want %d (must never be gated)", prefix, n, want)
				}
			}
		})
	}
}

func TestBuiltinsForNamedTools(t *testing.T) {
	tests := []struct {
		name    string
		groups  BuiltinGroups
		tool    string
		present bool
	}{
		{"schema tool present when configured", BuiltinGroups{Schema: true}, "schema.tables", true},
		{"schema tool absent when not configured", BuiltinGroups{}, "schema.tables", false},
		{"vaulty tool present when configured", BuiltinGroups{Vaulty: true}, "vaulty.list", true},
		{"vaulty tool absent when not configured", BuiltinGroups{}, "vaulty.list", false},
		{"recon always present", BuiltinGroups{}, "recon.search", true},
		{"witness always present", BuiltinGroups{}, "witness.select", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			found := false
			for _, c := range BuiltinsFor(tt.groups) {
				if c.Name == tt.tool {
					found = true
					break
				}
			}
			if found != tt.present {
				t.Errorf("%s present = %v, want %v", tt.tool, found, tt.present)
			}
		})
	}
}

func builtinByName(t *testing.T, name string) Capability {
	t.Helper()
	for _, c := range Builtins() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("builtin %q not registered", name)
	return Capability{}
}

// The witness.* descriptions are the entire contract an agent has for reading
// witness's output: MCP hands it the Description and the ArgsHint and nothing
// else. Witness fails closed — it emits one command per line, refuses to invent
// an invocation for a language it has no runner for, and widens to the whole
// suite when it cannot prove a selection — and every one of those behaviours
// reads as a false green if the description does not name it. A consumer that
// runs only the first line of a polyglot answer, or reads exit 1 as "no tests",
// reports a pass for tests that never ran.
//
// Every clause below has to be true of the WITNESS RIVET EMBEDS, which is a
// tagged module version and not the sibling working tree — see
// internal/witness.PinnedVersion and the tests beside it, which check the flags
// named here against the embedded build's own --help.
func TestWitnessDescriptionsStateTheFailClosedContract(t *testing.T) {
	tests := []struct {
		capability string
		// want are substrings that must appear in Description + ArgsHint,
		// each standing for one part of the contract.
		want []string
	}{
		{
			capability: "witness.run",
			want: []string{
				"ONE COMMAND PER LINE", // a polyglot repo answers with several
				"EVERY line",           // and all of them have to run
				"CHECK EACH LINE",      // and an older build can emit one that mixes ecosystems
				"Exit code 1",          // a language with no known runner
				"Do not improvise",     // 'cargo test <path>' is a name filter, not a path
				"WHOLE-SUITE",          // a build that can widen says so on stderr
				"unproven",             // and zero lines is never proof on its own
			},
		},
		{
			capability: "witness.select",
			want: []string{
				// The summary fields are named as evidence, never as the
				// condition that makes an empty selection safe: a build that
				// does not emit them would make that condition always true.
				"unmapped",
				"not_indexed",
				"analysis_error",
				"NOT A GREEN LIGHT",
				"unproven",
			},
		},
		{capability: "witness.staged", want: []string{"summary.unmapped", "summary.not_indexed", "unproven"}},
		{capability: "witness.since", want: []string{"summary.unmapped", "summary.not_indexed", "unproven"}},
	}

	for _, tt := range tests {
		t.Run(tt.capability, func(t *testing.T) {
			c := builtinByName(t, tt.capability)
			text := c.Description + " " + c.ArgsHint
			for _, want := range tt.want {
				if !strings.Contains(text, want) {
					t.Errorf("description does not mention %q:\n%s", want, text)
				}
			}
		})
	}
}

// witness.run used to promise "a command STRING", singular. Witness now returns
// one command per line, so the singular phrasing is not a stale nicety: it is an
// instruction to run one of N commands and call the result a pass.
func TestWitnessRunDoesNotPromiseASingleCommandString(t *testing.T) {
	c := builtinByName(t, "witness.run")
	for _, stale := range []string{"a command STRING", "the right command to run"} {
		if strings.Contains(c.Description, stale) {
			t.Errorf("description still promises a single command (%q): %s", stale, c.Description)
		}
	}
}

// Builtins stays the unfiltered source of truth — inspect and the filter itself
// both read it, so gating must never mutate or shrink it.
func TestBuiltinsUnfiltered(t *testing.T) {
	before := len(Builtins())
	BuiltinsFor(BuiltinGroups{})
	if after := len(Builtins()); after != before {
		t.Fatalf("Builtins() returned %d capabilities after filtering, want %d", after, before)
	}
}
