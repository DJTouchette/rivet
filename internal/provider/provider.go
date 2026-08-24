// Package provider defines the seam between rivet's generators and the agent
// harness that reads what they produce. Rivet packages the same knowledge for
// every harness: an instruction file, an MCP registration, skills, and
// sometimes subagent briefs. Each harness expects those artifacts under
// different names, in different formats, and in different places. A Provider
// answers where and how; the content stays provider-neutral.
//
// The shape follows pins.Provider: a short list of questions a caller can ask,
// with each implementation owning the one harness it knows about.
package provider

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Provider describes where one agent harness expects rivet's artifacts.
type Provider interface {
	// Name is the identifier accepted by --provider and printed in output.
	Name() string

	// InstructionFile is the repo-root file the harness reads at session
	// start, relative to the project root.
	InstructionFile() string

	// SkillsDir is where skills are installed, relative to the project root.
	// An empty string means the harness has no skills and rivet installs none.
	SkillsDir() string

	// AgentsDir is where subagent briefs are installed, relative to the
	// project root. An empty string means rivet ships no subagents for this
	// harness, and the generated instruction file leaves out the paragraph
	// that points at them.
	AgentsDir() string

	// WriteMCPConfig registers `rivet serve` with the harness for the project
	// rooted at dir. It returns one line describing what it did, in the same
	// voice as the other setup actions.
	WriteMCPConfig(dir string) (string, error)

	// Detect reports whether this harness is already set up for the project
	// rooted at dir.
	Detect(dir string) bool
}

// All returns every known provider, in a stable order. Claude comes first
// because it is the default everywhere the choice is ambiguous.
func All() []Provider {
	return []Provider{Claude(), Codex()}
}

// New returns the provider with the given name.
func New(name string) (Provider, error) {
	for _, p := range All() {
		if p.Name() == name {
			return p, nil
		}
	}
	return nil, fmt.Errorf("unknown provider %q (want %s)", name, strings.Join(append(names(), "both", "auto"), ", "))
}

// Resolve turns a --provider value into the providers to act on. The spec is a
// provider name, "both", or "auto".
//
// "auto" looks at what the project already has. A project carrying markers for
// one harness gets that harness; a project carrying markers for both gets both.
// A project carrying neither gets claude, because rivet has always written
// CLAUDE.md and a new provider must not silently take that away.
func Resolve(spec, dir string) ([]Provider, error) {
	switch spec {
	case "both":
		return All(), nil
	case "auto", "":
		var found []Provider
		for _, p := range All() {
			if p.Detect(dir) {
				found = append(found, p)
			}
		}
		if len(found) == 0 {
			return []Provider{Claude()}, nil
		}
		return found, nil
	}

	p, err := New(spec)
	if err != nil {
		return nil, err
	}
	return []Provider{p}, nil
}

// Names lists every provider name, sorted, for help text.
func Names() []string {
	n := names()
	sort.Strings(n)
	return n
}

func names() []string {
	var out []string
	for _, p := range All() {
		out = append(out, p.Name())
	}
	return out
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func existsIn(dir, name string) bool {
	return exists(filepath.Join(dir, name))
}
