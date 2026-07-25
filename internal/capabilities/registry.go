package capabilities

import (
	"errors"
	"fmt"
	"sort"
)

type SafetyLevel string

const (
	SafetyLevelSafe      SafetyLevel = "safe"
	SafetyLevelGuarded   SafetyLevel = "guarded"
	SafetyLevelDangerous SafetyLevel = "dangerous"
)

type CapabilityKind string

const (
	KindProjectCommand CapabilityKind = "project_command"
	KindTool           CapabilityKind = "tool"
	KindMCP            CapabilityKind = "mcp"
	KindWorkflow       CapabilityKind = "workflow"
)

var (
	ErrEmptyName     = errors.New("capability name must not be empty")
	ErrInvalidKind   = errors.New("invalid capability kind")
	ErrInvalidSafety = errors.New("invalid safety level")
	ErrDuplicateName = errors.New("capability already registered")
)

// Param describes a named parameter for a capability.
type Param struct {
	Name        string   `yaml:"name"        json:"name"`
	Type        string   `yaml:"type"        json:"type"` // string, number, boolean, integer
	Description string   `yaml:"description" json:"description,omitempty"`
	Required    bool     `yaml:"required"    json:"required,omitempty"`
	Default     string   `yaml:"default"     json:"default,omitempty"`
	Enum        []string `yaml:"enum"        json:"enum,omitempty"`
	Flag        string   `yaml:"flag"        json:"flag,omitempty"` // CLI flag name (default: --<name>)
}

// FlagName returns the CLI flag to use for this param.
func (p Param) FlagName() string {
	if p.Flag != "" {
		return p.Flag
	}
	return "--" + p.Name
}

// Capability is the runtime model for a registered capability.
type Capability struct {
	Name             string         `yaml:"name"`
	Kind             CapabilityKind `yaml:"kind"`
	Description      string         `yaml:"description"`
	Command          []string       `yaml:"command"`
	Output           string         `yaml:"output"`
	Safety           SafetyLevel    `yaml:"safety"`
	Params           []Param        `yaml:"params,omitempty"`
	RequiresApproval bool           `yaml:"requires_approval,omitempty"`
	Builtin          bool           `yaml:"-"` // true for in-process builtins (not serialized)
	ArgsHint         string         `yaml:"-"` // hint for MCP tool args description (not serialized)
}

// Registry holds registered capabilities in memory.
type Registry struct {
	caps map[string]*Capability
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{caps: make(map[string]*Capability)}
}

// Register adds a capability to the registry after validation.
func (r *Registry) Register(cap Capability) error {
	if cap.Name == "" {
		return ErrEmptyName
	}
	if !ValidCapabilityKind(string(cap.Kind)) {
		return fmt.Errorf("capability %q: %w: %q", cap.Name, ErrInvalidKind, cap.Kind)
	}
	if !ValidSafetyLevel(string(cap.Safety)) {
		return fmt.Errorf("capability %q: %w: %q", cap.Name, ErrInvalidSafety, cap.Safety)
	}
	if _, exists := r.caps[cap.Name]; exists {
		return fmt.Errorf("%w: %q", ErrDuplicateName, cap.Name)
	}
	c := cap // copy
	r.caps[cap.Name] = &c
	return nil
}

// Override registers a capability, replacing any existing one with the same name.
// Validation is still enforced.
func (r *Registry) Override(cap Capability) error {
	if cap.Name == "" {
		return ErrEmptyName
	}
	if !ValidCapabilityKind(string(cap.Kind)) {
		return fmt.Errorf("capability %q: %w: %q", cap.Name, ErrInvalidKind, cap.Kind)
	}
	if !ValidSafetyLevel(string(cap.Safety)) {
		return fmt.Errorf("capability %q: %w: %q", cap.Name, ErrInvalidSafety, cap.Safety)
	}
	c := cap
	r.caps[cap.Name] = &c
	return nil
}

// Get returns a capability by name, or nil if not found.
func (r *Registry) Get(name string) *Capability {
	c, ok := r.caps[name]
	if !ok {
		return nil
	}
	copy := *c
	return &copy
}

// List returns all registered capabilities, sorted by name.
func (r *Registry) List() []Capability {
	result := make([]Capability, 0, len(r.caps))
	for _, c := range r.caps {
		result = append(result, *c)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// ListByKind returns capabilities filtered by kind, sorted by name.
func (r *Registry) ListByKind(kind CapabilityKind) []Capability {
	var result []Capability
	for _, c := range r.caps {
		if c.Kind == kind {
			result = append(result, *c)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// ListBySafety returns capabilities filtered by safety level, sorted by name.
func (r *Registry) ListBySafety(level SafetyLevel) []Capability {
	var result []Capability
	for _, c := range r.caps {
		if c.Safety == level {
			result = append(result, *c)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// ParseSafetyLevel converts a string to SafetyLevel.
func ParseSafetyLevel(s string) (SafetyLevel, error) {
	switch SafetyLevel(s) {
	case SafetyLevelSafe, SafetyLevelGuarded, SafetyLevelDangerous:
		return SafetyLevel(s), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidSafety, s)
	}
}

// ParseCapabilityKind converts a string to CapabilityKind.
func ParseCapabilityKind(s string) (CapabilityKind, error) {
	switch CapabilityKind(s) {
	case KindProjectCommand, KindTool, KindMCP, KindWorkflow:
		return CapabilityKind(s), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidKind, s)
	}
}

// ValidSafetyLevel returns true if the string is a recognized safety level.
func ValidSafetyLevel(s string) bool {
	_, err := ParseSafetyLevel(s)
	return err == nil
}

// ValidCapabilityKind returns true if the string is a recognized capability kind.
func ValidCapabilityKind(s string) bool {
	_, err := ParseCapabilityKind(s)
	return err == nil
}
