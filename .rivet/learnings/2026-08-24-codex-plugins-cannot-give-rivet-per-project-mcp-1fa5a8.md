---
title: Codex plugins cannot give rivet per-project MCP registration
date: 2026-08-24
confidence: high
suggested_doc: mcp
related_paths:
  - internal/cli/init.go
  - internal/mcp/**
promoted: false
---

# Codex plugins cannot give rivet per-project MCP registration

## Observation
Probed codex-cli 0.149.1 against an isolated CODEX_HOME and HOME. A .codex-plugin/plugin.json with "mcpServers": "./.mcp.json" does register rivet's server: after install, codex mcp list --json returned the exact stdio transport from rivet's own .mcp.json, unmodified. But nothing about it is per-project. A repo-root .codex-plugin/plugin.json does NOT auto-load: from a project containing one, codex mcp list returned [] and codex plugin list reported no plugins. Activation needs a marketplace manifest at <root>/.agents/plugins/marketplace.json plus two commands, codex plugin marketplace add <root> and codex plugin add <name>@<marketplace>, both of which write to the global config.toml ([marketplaces.<name>] with an absolute source path, and [plugins."<name>@<mp>"] enabled = true). Once installed the server is visible from every directory, including unrelated ones. Install also COPIES the plugin into CODEX_HOME/plugins/cache/<mp>/<plugin>/<version>/, so editing the repo's .mcp.json changes nothing until a version bump and reinstall. The plugin may sit at the repo root with source.path "."; "repository-scoped plugin migration is not allowed" turned out to be unrelated, it is in external-agent-migration and concerns importing Claude's repo plugins, not codex's own.

## Impact
This kills the idea that codex plugins restore the property that makes rivet's .mcp.json pleasant, namely that cloning a project gives you the MCP server for free. Codex MCP registration is global and per-user however you get there, so the codex path has a manual step the Claude path does not.

## Recommendation
Do not build the plugin path for v1. Have rivet print or run 'codex mcp add rivet -- rivet serve' and let codex own its own config file. Revisit plugins only if skills and subagents are wanted as one installable bundle, and if so treat the version cachebuster and reinstall loop as a first-class cost.
