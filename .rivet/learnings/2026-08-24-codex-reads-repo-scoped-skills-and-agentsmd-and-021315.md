---
title: Codex reads repo-scoped skills and AGENTS.md, and its SKILL.md schema is confirmed
date: 2026-08-24
confidence: high
suggested_doc: capabilities
promoted: false
---

# Codex reads repo-scoped skills and AGENTS.md, and its SKILL.md schema is confirmed

## Observation
Probed codex 0.149.1 with 'codex debug prompt-input' under an isolated CODEX_HOME and HOME. A SKILL.md at .codex/skills/<name>/SKILL.md in the project root is discovered and listed, and a repo-root AGENTS.md is read into the prompt. The frontmatter schema comes from codex's own bundled validator at $CODEX_HOME/skills/.system/skill-creator/scripts/quick_validate.py: allowed keys are name, description, license, allowed-tools and metadata; name and description are required; name must be hyphen-case and at most 64 characters; description must be at most 1024 characters and must not contain angle brackets.

## Impact
Two earlier scouts left the codex SKILL.md schema unconfirmed and assumed skills were global-only, which would have made codex skills a guess or a skip. Both assumptions were wrong in rivet's favour: skills and the instruction file are per-project, so only the MCP registration is global.

## Recommendation
Use 'codex debug prompt-input' as the cheap, offline, no-login probe for anything about what codex actually loads. It needs no quota and answers in about a second.
