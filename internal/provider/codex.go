package provider

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// CodexMCPTOML registers `rivet serve` with codex. It is the exact table
// `codex mcp add rivet -- rivet serve` writes, confirmed by running that
// command against an isolated CODEX_HOME and reading the result back with
// `codex mcp list --json`.
const CodexMCPTOML = `[mcp_servers.rivet]
command = "rivet"
args = ["serve"]
`

// codexConfigPath is where codex keeps the table above. It is global, not
// per-project: codex has no repo-root equivalent of .mcp.json.
const codexConfigPath = "$CODEX_HOME/config.toml (~/.codex/config.toml by default)"

// codex is the Codex CLI. Two of its conventions differ from Claude Code in
// ways that shape this implementation:
//
//   - MCP servers are registered globally in config.toml, not per-project, so
//     rivet cannot write a committable file for them. It shells out to `codex
//     mcp add` and lets codex own its own config, the same discipline rivet
//     applies to rally's on-disk artifacts.
//   - Subagent briefs use a manifest format rivet has not ported, so AgentsDir
//     is empty and the generated instruction file omits the paragraph about
//     them.
//
// Skills and the instruction file are both repo-scoped and both confirmed to
// load: a SKILL.md under .codex/skills and an AGENTS.md at the repo root show
// up in `codex debug prompt-input`.
type codex struct{}

// Codex returns the Codex CLI provider.
func Codex() Provider { return codex{} }

func (codex) Name() string { return "codex" }

func (codex) InstructionFile() string { return "AGENTS.md" }

func (codex) SkillsDir() string { return filepath.Join(".codex", "skills") }

// AgentsDir is empty: rivet ships no codex subagents in this version.
func (codex) AgentsDir() string { return "" }

func (codex) Detect(dir string) bool {
	return existsIn(dir, ".codex") || existsIn(dir, "AGENTS.md")
}

// WriteMCPConfig runs `codex mcp add rivet -- rivet serve` when codex is on
// PATH. Rivet does not edit config.toml itself; the file belongs to codex and
// hand-writing another tool's config is how you end up fighting its schema.
// When codex is absent there is nothing to delegate to, so the exact table is
// printed for the user to paste.
func (codex) WriteMCPConfig(dir string) (string, error) {
	bin, err := exec.LookPath("codex")
	if err != nil {
		return fmt.Sprintf("codex is not on PATH, so rivet did not register the MCP server.\n"+
			"    Add this to %s yourself, or run 'codex mcp add rivet -- rivet serve':\n\n%s",
			codexConfigPath, indent(CodexMCPTOML, "      ")), nil
	}

	cmd := exec.Command(bin, "mcp", "add", "rivet", "--", "rivet", "serve")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("codex mcp add rivet -- rivet serve: %w\n%s\n\nAdd this to %s yourself:\n\n%s",
			err, strings.TrimSpace(string(out)), codexConfigPath, CodexMCPTOML)
	}

	return "registered rivet with codex (global, in " + codexConfigPath + ")", nil
}

func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
