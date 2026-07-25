package projectcli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// defaultSafety is what a discovered capability gets when its rivet-discover
// output omits the "safety" field.
//
// It is deliberately the most restrictive level. Discovery reports commands
// rivet has never seen and cannot inspect; an omitted field means "the project
// CLI author didn't say", not "this is read-only". Defaulting to "safe" made
// unlabelled destructive commands auto-runnable over MCP with no approval step,
// which is fail-open on the one axis that has to fail closed.
//
// "dangerous" rather than "guarded" because guarded is not actually enforced
// anywhere: Executor.RunCapability only gates SafetyLevelDangerous, so a
// guarded default would still let an unlabelled `db.reset` run unattended.
// Dangerous is the only level that forces an explicit --approve / approve:true.
// The cost is visible and cheap to fix: discovery runs once, at
// `rivet project register-cli`, and writes into .rivet/capabilities.yaml where
// a human can correct the level — as opposed to a silent downgrade nobody sees.
const defaultSafety = "dangerous"

// DiscoveredCapability is a capability reported by a project CLI's
// rivet-discover command.
type DiscoveredCapability struct {
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Description string   `json:"description"`
	Command     []string `json:"command"`
	Output      string   `json:"output"`
	Safety      string   `json:"safety"`
}

// DiscoverResult is the JSON envelope returned by rivet-discover.
type DiscoverResult struct {
	Capabilities []DiscoveredCapability `json:"capabilities"`
}

// DefaultDiscoverArgs is the argv appended to the project CLI when the project
// doesn't say otherwise: the hidden subcommand the Go scaffold registers.
func DefaultDiscoverArgs() []string { return []string{"rivet-discover"} }

// NormalizeDiscoverArgs drops empty tokens and falls back to the default.
//
// A config that says `discover: []` or `discover: [""]` means "I didn't
// configure this", not "run the binary with no arguments" — the latter would
// exec the CLI's bare help output and try to parse it as JSON.
func NormalizeDiscoverArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if strings.TrimSpace(a) != "" {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		return DefaultDiscoverArgs()
	}
	return out
}

// RunDiscover executes the project CLI's discover command and parses the output.
// Returns nil capabilities (not an error) if the binary doesn't support discovery.
//
// discoverArgs is the argv to pass, defaulting to DefaultDiscoverArgs when
// empty. It is a list rather than a single token because a project CLI's
// discover command isn't always one: `mix <ns>.rivet_discover` takes two.
func RunDiscover(binaryPath string, discoverArgs ...string) (*DiscoverResult, error) {
	cmd := exec.Command(binaryPath, NormalizeDiscoverArgs(discoverArgs)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		// If the binary doesn't have a rivet-discover command, that's fine.
		// Common exit codes for unknown command: 1, 2, 127.
		return nil, nil
	}

	if stdout.Len() == 0 {
		return nil, nil
	}

	result, err := parseDiscoverOutput(stdout.Bytes())
	if err != nil {
		return nil, err
	}

	applyDefaults(result.Capabilities, os.Stderr)

	return result, nil
}

// parseDiscoverOutput decodes the discover envelope, tolerating a preamble.
//
// The protocol says stdout is JSON, and a CLI rivet built obeys it. A CLI run
// through a build tool does not: Mix prints "Compiling 1 file (.ex)" to stdout,
// not stderr, whenever the project is stale — which it always is the first time
// you run discovery after editing the discover task. Failing there would mean
// registration works on the second try and not the first.
//
// The retry starts at the first brace and keeps the original error if that
// doesn't parse either, so genuinely malformed output still reports what it saw.
func parseDiscoverOutput(out []byte) (*DiscoverResult, error) {
	var result DiscoverResult
	err := json.Unmarshal(out, &result)
	if err == nil {
		return &result, nil
	}

	if i := bytes.IndexByte(out, '{'); i > 0 {
		var retry DiscoverResult
		if json.Unmarshal(out[i:], &retry) == nil {
			return &retry, nil
		}
	}

	return nil, fmt.Errorf("parsing rivet-discover output: %w (got: %s)", err, out)
}

// applyDefaults back-fills the fields a project CLI may legitimately omit.
//
// Kind and output are cosmetic, but safety changes what can run unattended, so
// defaulting it is announced on warn rather than applied silently: a capability
// whose safety level was decided for it, without the author noticing, is its
// own trap. Capabilities that declare a safety level are left exactly as-is.
func applyDefaults(caps []DiscoveredCapability, warn io.Writer) {
	for i := range caps {
		if caps[i].Kind == "" {
			caps[i].Kind = "project_command"
		}
		if caps[i].Output == "" {
			caps[i].Output = "json"
		}
		if caps[i].Safety == "" {
			caps[i].Safety = defaultSafety
			fmt.Fprintf(warn, "warning: capability %q declared no safety level; defaulting to %q (requires explicit approval)\n",
				caps[i].Name, defaultSafety)
			fmt.Fprintf(warn, "         add \"safety\": \"safe\" | \"guarded\" | \"dangerous\" to its rivet-discover output to change this\n")
		}
	}
}
