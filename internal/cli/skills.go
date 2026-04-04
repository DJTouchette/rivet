package cli

import (
	"fmt"
	"os"
	"path/filepath"
)

// ensureSkills writes Claude Code skill files to .claude/skills/.
// Non-destructive: existing skill files are never overwritten.
func ensureSkills() ([]string, error) {
	var actions []string

	skills := []struct {
		dir     string
		file    string
		content string
	}{
		{
			dir:     filepath.Join(".claude", "skills", "rivet-setup"),
			file:    "SKILL.md",
			content: rivetSetupSkill,
		},
		{
			dir:     filepath.Join(".claude", "skills", "rivet-fill-context"),
			file:    "SKILL.md",
			content: fillContextSkill,
		},
		{
			dir:     filepath.Join(".claude", "skills", "rivet-compact-context"),
			file:    "SKILL.md",
			content: compactContextSkill,
		},
	}

	for _, s := range skills {
		if err := os.MkdirAll(s.dir, 0755); err != nil {
			return nil, fmt.Errorf("creating %s: %w", s.dir, err)
		}

		path := filepath.Join(s.dir, s.file)
		if fileExists(path) {
			actions = append(actions, fmt.Sprintf("%s already exists, skipped", path))
			continue
		}

		if err := os.WriteFile(path, []byte(s.content), 0644); err != nil {
			return nil, fmt.Errorf("writing %s: %w", path, err)
		}
		actions = append(actions, fmt.Sprintf("added %s skill", filepath.Base(s.dir)))
	}

	return actions, nil
}

const rivetSetupSkill = `---
name: rivet-setup
description: Initialize rivet, scaffold context docs, and fill them out using recon analysis
---

Set up rivet for this project from scratch. This runs the full initialization pipeline.

## Steps

### 1. Initialize rivet

Run:
` + "```" + `
rivet init
` + "```" + `

This creates .rivet/ config, adds the MCP server to .mcp.json, and installs Claude Code skills.
If rivet is already initialized, skip this step.

### 2. Scaffold context docs

Run:
` + "```" + `
rivet context scaffold
` + "```" + `

This analyzes the codebase using recon (hotspots, structure, frameworks) and generates starter context documents in .rivet/context/ with placeholder sections.

If all docs already exist (scaffold reports 0 written), skip to step 3.

### 3. Fill out context docs

For each context document that has <!-- placeholder --> comments:

a. Read the document to see its domain name, tags, and related_paths.

b. Use the rivet MCP tools to gather information:
   - ` + "`recon.search`" + ` with the domain name to find relevant files
   - ` + "`recon.symbols`" + ` on key files to understand the API surface
   - ` + "`recon.related`" + ` on the main context module to find dependencies
   - ` + "`recon.context`" + ` on high-risk files for metrics
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
` + "```" + `
rivet sync
` + "```" + `

This updates CLAUDE.md with the rivet capabilities and context document index.

$ARGUMENTS
`

const fillContextSkill = `---
name: rivet-fill-context
description: Fill out scaffolded rivet context docs using recon analysis of the codebase
---

Fill out the rivet context documents in .rivet/context/ that have placeholder sections.

## Instructions

1. Run ` + "`rivet context list`" + ` to see all context documents.

2. For each document that still has <!-- placeholder --> comments:
   a. Read the document to see its domain name, tags, and related_paths.
   b. Use the rivet MCP tools to gather information:
      - ` + "`recon.search`" + ` with the domain name to find relevant files
      - ` + "`recon.symbols`" + ` on key files to understand the API surface
      - ` + "`recon.related`" + ` on the main context module to find dependencies
      - ` + "`recon.context`" + ` on high-risk files for metrics
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

6. After filling in all docs, run ` + "`rivet sync`" + ` to update CLAUDE.md.

$ARGUMENTS
`

const compactContextSkill = `---
name: rivet-compact-context
description: Review and tighten rivet context docs — deduplicate learnings, prune stale info, keep docs concise
---

Review all rivet context documents and tighten them up.

## Instructions

1. Run ` + "`rivet context list`" + ` to see all context documents.

2. For each document, read it and check for:

   **Learnings section:**
   - Remove duplicate or near-duplicate learnings (keep the most specific one)
   - Remove learnings that are now captured in the main doc sections (Overview, Key modules, Gotchas)
   - If a learning is important enough, promote it into the Gotchas section and remove the learning entry
   - Remove learnings that are no longer accurate (verify against current code if unsure)

   **Overall doc size:**
   - Each doc should be 20-50 lines of content max (excluding frontmatter)
   - If a doc has grown beyond that, trim verbose descriptions to single sentences
   - Merge overlapping bullet points
   - Remove obvious information that anyone could see from reading the code — only keep non-obvious insights

   **Staleness:**
   - Use ` + "`recon.grep`" + ` to verify key modules/functions mentioned in the doc still exist
   - If a module was renamed or removed, update or remove the reference
   - If a gotcha was fixed, remove it

3. Do NOT remove or modify frontmatter (tags, related_paths) or the High-risk files section — those are regenerated by ` + "`rivet context scaffold`" + `.

4. After compacting, run ` + "`rivet sync`" + ` to update CLAUDE.md.

5. Summarize what you changed: how many docs reviewed, learnings promoted/pruned, total lines saved.

$ARGUMENTS
`
