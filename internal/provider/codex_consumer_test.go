package provider

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestCodexMCPConfigRoundTrip is the check that stops the TOML golden from
// locking in bytes no consumer can read. A golden file will happily pin
// "mcpServers" inside a TOML table forever; only codex can say whether the
// table it produced is the one codex reads back.
//
// The probe writes CodexMCPTOML as the whole of an isolated CODEX_HOME's
// config.toml and asks codex to list what it found. It needs no login, makes
// no network call, and touches nothing outside the temp directory. It is
// skipped when codex is not installed.
// codexMCPEntry is the slice of `codex mcp list --json` this check cares
// about. Extra fields codex reports are ignored on purpose: pinning them would
// make the test fail on a codex upgrade that changed nothing rivet relies on.
type codexMCPEntry struct {
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	Transport struct {
		Type    string   `json:"type"`
		Command string   `json:"command"`
		Args    []string `json:"args"`
	} `json:"transport"`
}

func TestCodexMCPConfigRoundTrip(t *testing.T) {
	bin, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex is not on PATH; skipping the consumer round-trip")
	}

	codexHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(CodexMCPTOML), 0644); err != nil {
		t.Fatalf("writing config.toml: %v", err)
	}

	out, err := runCodex(t, bin, codexHome, "mcp", "list", "--json")
	if err != nil {
		t.Fatalf("codex mcp list --json: %v\n%s", err, out)
	}

	var entries []codexMCPEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		t.Fatalf("codex mcp list did not return JSON: %v\n%s", err, out)
	}

	var rivet *codexMCPEntry
	for i := range entries {
		if entries[i].Name == "rivet" {
			rivet = &entries[i]
		}
	}
	if rivet == nil {
		t.Fatalf("codex did not read a rivet server out of the TOML rivet emits:\n%s\ngot: %s", CodexMCPTOML, out)
	}

	if !rivet.Enabled {
		t.Errorf("codex read the rivet server but reports it disabled")
	}
	if rivet.Transport.Type != "stdio" {
		t.Errorf("transport type is %q, want stdio", rivet.Transport.Type)
	}
	if rivet.Transport.Command != "rivet" {
		t.Errorf("transport command is %q, want rivet", rivet.Transport.Command)
	}
	if len(rivet.Transport.Args) != 1 || rivet.Transport.Args[0] != "serve" {
		t.Errorf("transport args are %v, want [serve]", rivet.Transport.Args)
	}
}

// TestCodexMCPAddProducesTheTOMLRivetPrints closes the other half of the loop.
// WriteMCPConfig delegates to `codex mcp add` when codex is present and prints
// CodexMCPTOML when it is not, so the two paths have to agree on what ends up
// in config.toml.
func TestCodexMCPAddProducesTheTOMLRivetPrints(t *testing.T) {
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex is not on PATH; skipping the add round-trip")
	}

	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	if _, err := Codex().WriteMCPConfig(t.TempDir()); err != nil {
		t.Fatalf("WriteMCPConfig: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if err != nil {
		t.Fatalf("codex mcp add wrote no config.toml: %v", err)
	}
	if string(got) != CodexMCPTOML {
		t.Errorf("codex mcp add wrote:\n%s\nbut rivet tells the user to paste:\n%s", got, CodexMCPTOML)
	}
}

func runCodex(t *testing.T, bin, codexHome string, args ...string) ([]byte, error) {
	t.Helper()

	// codex resolves CODEX_HOME from the environment, and HOME is where it
	// would fall back to. Both are redirected so the probe cannot read or
	// write the developer's real configuration.
	cmd := exec.Command(bin, args...)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(),
		"CODEX_HOME="+codexHome,
		"HOME="+t.TempDir(),
	)

	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = cmd.Output()
		close(done)
	}()

	select {
	case <-done:
		return out, err
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		return nil, exec.ErrNotFound
	}
}
