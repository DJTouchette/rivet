---
name: rivet-setup
description: Initialize rivet, scaffold context docs, and fill them out using recon analysis
---

Set up rivet for this project from scratch. This runs the full initialization pipeline.

## Steps

### 1. Initialize rivet

Run:
```
rivet init
```

This creates .rivet/ config, adds the MCP server to .mcp.json, and installs Claude Code skills.
If rivet is already initialized, skip this step.

### 2. Scaffold context docs

Run:
```
rivet context scaffold
```

This analyzes the codebase using recon (hotspots, structure, frameworks) and generates starter context documents in .rivet/context/ with placeholder sections.

If all docs already exist (scaffold reports 0 written), skip to step 3.

### 3. Fill out context docs

For each context document that has <!-- placeholder --> comments:

a. Read the document to see its domain name, tags, and related_paths.

b. Use the rivet MCP tools to gather information:
   - `recon.search` with the domain name to find relevant files
   - `recon.symbols` on key files to understand the API surface
   - `recon.related` on the main context module to find dependencies
   - `recon.context` on high-risk files for metrics
   - Read the actual source files to understand the logic

c. Fill in the placeholder sections:
   - **Overview**: 2-4 sentences on what this domain does and its responsibilities
   - **Key modules**: List the important files with a one-line description of each
   - **Failure modes**: How does this domain fail? Error handling, retries, circuit breakers, silent failures
   - **Gotchas**: Non-obvious things — edge cases, implicit dependencies, ordering requirements, common mistakes

Writing guidelines:
- Write for an AI reader — focus on how things connect, not what is obvious from the code
- Keep each doc concise: 20-40 lines of content
- Preserve existing frontmatter and high-risk files sections
- Don't exhaustively list every function; focus on what matters for someone making changes

### 4. Sync

Run:
```
rivet sync
```

This updates CLAUDE.md with the rivet capabilities and context document index.

$ARGUMENTS
