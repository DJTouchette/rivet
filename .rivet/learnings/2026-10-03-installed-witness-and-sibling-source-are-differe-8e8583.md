---
title: Installed Witness and sibling source are different runtime owners
date: 2026-10-03
author: Astra
confidence: high
related_paths:
  - internal/witness/**
  - internal/cli/witness.go
  - internal/mcp/server.go
  - scripts/verify-witness-mcp.py
promoted: false
---

# Installed Witness and sibling source are different runtime owners

## Observation
The installed Rivet v0.19.1 binary embeds Witness v0.4.2; this checkout pins v0.5.0. New manifest planning lives in the standalone Witness module, not a second selector in internal/witness. An empty MCP payload must remain an unproven error, and explicit CLI execution exit codes must survive the adapter.

## Recommendation
Use the isolated MCP smoke script with a temporary Go workspace to review composed commits. Release Witness and update the Rivet module pin through the normal delivery process before replacing binaries; never infer installed behavior from sibling files.
