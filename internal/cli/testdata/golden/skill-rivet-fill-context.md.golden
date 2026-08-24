---
name: rivet-fill-context
description: Fill out scaffolded rivet context docs using recon analysis of the codebase
---

Fill out the rivet context documents in .rivet/context/ that have placeholder sections.

## Instructions

1. Run `rivet context list` to see all context documents.

2. For each document that still has <!-- placeholder --> comments:
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
      - **Failure modes**: How does this domain fail? Error handling, retries, circuit breakers, silent failures. A future session asking "what happens when X fails?" should find the answer here without reading source.
      - **Gotchas**: Non-obvious things — edge cases, implicit dependencies, ordering requirements, common mistakes

3. Write for an AI reader. Focus on:
   - How things connect (what calls what, what depends on what)
   - What is NOT obvious from reading the code
   - Failure paths and error handling — these are the most common investigation questions
   - Constraints and invariants that must be maintained
   - Common mistakes or traps

4. Keep each document concise — aim for 20-40 lines of content per doc. Don't exhaustively list every function; focus on what matters for someone making changes.

5. Preserve the existing frontmatter (tags, related_paths) and any high-risk files sections. Only fill in the placeholder sections.

6. After filling in all docs, run `rivet sync` to update CLAUDE.md.

$ARGUMENTS
