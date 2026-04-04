package projectcli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
)

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

	// Default kind to project_command if not set.
	for i := range result.Capabilities {
		if result.Capabilities[i].Kind == "" {
			result.Capabilities[i].Kind = "project_command"
		}
		if result.Capabilities[i].Safety == "" {
			result.Capabilities[i].Safety = "safe"
		}
		if result.Capabilities[i].Output == "" {
			result.Capabilities[i].Output = "json"
		}
	}

	return &result, nil
}
