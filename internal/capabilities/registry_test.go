package capabilities

import (
	"errors"
	"testing"
)

func TestRegisterAndGet(t *testing.T) {
	reg := NewRegistry()
	cap := Capability{
		Name:        "test.cmd",
		Kind:        KindProjectCommand,
		Description: "A test command",
		Command:     []string{"./bin/cli", "test"},
		Output:      "json",
		Safety:      SafetyLevelSafe,
	}
	if err := reg.Register(cap); err != nil {
		t.Fatal(err)
	}

	got := reg.Get("test.cmd")
	if got == nil {
		t.Fatal("expected to find registered capability")
	}
	if got.Name != "test.cmd" {
		t.Errorf("expected name %q, got %q", "test.cmd", got.Name)
	}
	if got.Kind != KindProjectCommand {
		t.Errorf("expected kind %q, got %q", KindProjectCommand, got.Kind)
	}
	if got.Safety != SafetyLevelSafe {
		t.Errorf("expected safety %q, got %q", SafetyLevelSafe, got.Safety)
	}
}

func TestGetNotFound(t *testing.T) {
	reg := NewRegistry()
	if got := reg.Get("nonexistent"); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestRegisterEmptyName(t *testing.T) {
	reg := NewRegistry()
	err := reg.Register(Capability{Kind: KindTool, Safety: SafetyLevelSafe})
	if !errors.Is(err, ErrEmptyName) {
		t.Fatalf("expected ErrEmptyName, got %v", err)
	}
}

func TestRegisterInvalidKind(t *testing.T) {
	reg := NewRegistry()
	err := reg.Register(Capability{Name: "bad", Kind: "bogus", Safety: SafetyLevelSafe})
	if !errors.Is(err, ErrInvalidKind) {
		t.Fatalf("expected ErrInvalidKind, got %v", err)
	}
}

func TestRegisterInvalidSafety(t *testing.T) {
	reg := NewRegistry()
	err := reg.Register(Capability{Name: "bad", Kind: KindTool, Safety: "yolo"})
	if !errors.Is(err, ErrInvalidSafety) {
		t.Fatalf("expected ErrInvalidSafety, got %v", err)
	}
}

func TestDuplicateRegistration(t *testing.T) {
	reg := NewRegistry()
	cap := Capability{Name: "dup", Kind: KindTool, Safety: SafetyLevelSafe}
	if err := reg.Register(cap); err != nil {
		t.Fatal(err)
	}
	err := reg.Register(cap)
	if !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("expected ErrDuplicateName, got %v", err)
	}
}

func TestListSorted(t *testing.T) {
	reg := NewRegistry()
	for _, name := range []string{"charlie", "alpha", "bravo"} {
		reg.Register(Capability{Name: name, Kind: KindTool, Safety: SafetyLevelSafe})
	}

	caps := reg.List()
	if len(caps) != 3 {
		t.Fatalf("expected 3, got %d", len(caps))
	}
	expected := []string{"alpha", "bravo", "charlie"}
	for i, c := range caps {
		if c.Name != expected[i] {
			t.Errorf("position %d: expected %q, got %q", i, expected[i], c.Name)
		}
	}
}

func TestListByKind(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Capability{Name: "tool1", Kind: KindTool, Safety: SafetyLevelSafe})
	reg.Register(Capability{Name: "cmd1", Kind: KindProjectCommand, Safety: SafetyLevelSafe})
	reg.Register(Capability{Name: "tool2", Kind: KindTool, Safety: SafetyLevelGuarded})

	tools := reg.ListByKind(KindTool)
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	if tools[0].Name != "tool1" || tools[1].Name != "tool2" {
		t.Errorf("unexpected order: %v, %v", tools[0].Name, tools[1].Name)
	}

	cmds := reg.ListByKind(KindProjectCommand)
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d", len(cmds))
	}
}

func TestListBySafety(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Capability{Name: "a", Kind: KindTool, Safety: SafetyLevelSafe})
	reg.Register(Capability{Name: "b", Kind: KindTool, Safety: SafetyLevelDangerous})
	reg.Register(Capability{Name: "c", Kind: KindTool, Safety: SafetyLevelSafe})

	safe := reg.ListBySafety(SafetyLevelSafe)
	if len(safe) != 2 {
		t.Fatalf("expected 2 safe capabilities, got %d", len(safe))
	}

	dangerous := reg.ListBySafety(SafetyLevelDangerous)
	if len(dangerous) != 1 {
		t.Fatalf("expected 1 dangerous capability, got %d", len(dangerous))
	}
}

func TestParseSafetyLevel(t *testing.T) {
	tests := []struct {
		input string
		want  SafetyLevel
		err   bool
	}{
		{"safe", SafetyLevelSafe, false},
		{"guarded", SafetyLevelGuarded, false},
		{"dangerous", SafetyLevelDangerous, false},
		{"", "", true},
		{"yolo", "", true},
	}
	for _, tt := range tests {
		got, err := ParseSafetyLevel(tt.input)
		if tt.err && err == nil {
			t.Errorf("ParseSafetyLevel(%q): expected error", tt.input)
		}
		if !tt.err && err != nil {
			t.Errorf("ParseSafetyLevel(%q): unexpected error: %v", tt.input, err)
		}
		if got != tt.want {
			t.Errorf("ParseSafetyLevel(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseCapabilityKind(t *testing.T) {
	tests := []struct {
		input string
		want  CapabilityKind
		err   bool
	}{
		{"project_command", KindProjectCommand, false},
		{"tool", KindTool, false},
		{"mcp", KindMCP, false},
		{"workflow", KindWorkflow, false},
		{"", "", true},
		{"plugin", "", true},
	}
	for _, tt := range tests {
		got, err := ParseCapabilityKind(tt.input)
		if tt.err && err == nil {
			t.Errorf("ParseCapabilityKind(%q): expected error", tt.input)
		}
		if !tt.err && err != nil {
			t.Errorf("ParseCapabilityKind(%q): unexpected error: %v", tt.input, err)
		}
		if got != tt.want {
			t.Errorf("ParseCapabilityKind(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
