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

// Builtins stays the unfiltered source of truth — inspect and the filter itself
// both read it, so gating must never mutate or shrink it.
func TestBuiltinsUnfiltered(t *testing.T) {
	before := len(Builtins())
	BuiltinsFor(BuiltinGroups{})
	if after := len(Builtins()); after != before {
		t.Fatalf("Builtins() returned %d capabilities after filtering, want %d", after, before)
	}
}
