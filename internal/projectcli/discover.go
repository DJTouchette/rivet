package projectcli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
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

// RunDiscover executes `<binary> rivet-discover` and parses the output.
// Returns nil capabilities (not an error) if the binary doesn't support discovery.
func RunDiscover(binaryPath string) (*DiscoverResult, error) {
	cmd := exec.Command(binaryPath, "rivet-discover")
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

	var result DiscoverResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("parsing rivet-discover output: %w (got: %s)", err, stdout.String())
	}

	applyDefaults(result.Capabilities, os.Stderr)

	return &result, nil
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
