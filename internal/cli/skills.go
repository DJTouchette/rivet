package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/djtouchette/rivet/internal/provider"
)

// ensureSkills writes the rivet skill files into the provider's skills
// directory. Non-destructive: existing skill files are never overwritten.
// A provider with no skills directory gets no skills and no error.
func ensureSkills(p provider.Provider) ([]string, error) {
	var actions []string

	skillsDir := p.SkillsDir()
	if skillsDir == "" {
		return nil, nil
	}

	skills := []struct {
		dir     string
		file    string
		content string
	}{
		{
			dir:     filepath.Join(skillsDir, "rivet-setup"),
			file:    "SKILL.md",
			content: renderSkill(rivetSetupSkill, p),
		},
		{
			dir:     filepath.Join(skillsDir, "rivet-fill-context"),
			file:    "SKILL.md",
			content: renderSkill(fillContextSkill, p),
		},
		{
			dir:     filepath.Join(skillsDir, "rivet-compact-context"),
			file:    "SKILL.md",
			content: renderSkill(compactContextSkill, p),
		},
		{
			dir:     filepath.Join(skillsDir, "rivet-promote-learnings"),
			file:    "SKILL.md",
			content: renderSkill(promoteLearningsSkill, p),
		},
	}

	for _, s := range skills {
		if err := os.MkdirAll(s.dir, 0755); err != nil {
			return nil, fmt.Errorf("creating %s: %w", s.dir, err)
		}

		path := filepath.Join(s.dir, s.file)
		if action, done, err := refreshGenerated(path, s.content); err != nil {
			return nil, err
		} else if done {
			actions = append(actions, action)
			continue
		}
		if err := os.WriteFile(path, []byte(s.content), 0644); err != nil {
			return nil, fmt.Errorf("writing %s: %w", path, err)
		}
		actions = append(actions, fmt.Sprintf("added %s skill", filepath.Base(s.dir)))
	}

	return actions, nil
}

// renderSkill fills in the handful of places where a skill body has to name
// something harness-specific: the instruction file it tells you to sync, what
// `rivet init` installs, how the promote skill is invoked, and whether the
// trailing $ARGUMENTS token means anything. Everything else in these bodies is
// the same procedure for any agent.
func renderSkill(content string, p provider.Provider) string {
	subs := skillSubstitutions(p)
	for placeholder, value := range subs {
		content = strings.ReplaceAll(content, placeholder, value)
	}
	return content
}

func skillSubstitutions(p provider.Provider) map[string]string {
	if p.Name() == provider.Codex().Name() {
		return map[string]string{
			"{{INSTRUCTION_FILE}}": p.InstructionFile(),
			"{{INIT_EFFECT}}":      "registers the MCP server with codex, and installs Codex skills",
			"{{PROMOTE_SKILL}}":    "the rivet-promote-learnings skill",
			"{{ARGUMENTS}}":        "",
		}
	}
	return map[string]string{
		"{{INSTRUCTION_FILE}}": p.InstructionFile(),
		"{{INIT_EFFECT}}":      "adds the MCP server to .mcp.json, and installs Claude Code skills",
		"{{PROMOTE_SKILL}}":    "/rivet-promote-learnings",
		"{{ARGUMENTS}}":        "\n$ARGUMENTS\n",
	}
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

This creates .rivet/ config, {{INIT_EFFECT}}.
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

This updates {{INSTRUCTION_FILE}} with the rivet capabilities and context document index.
{{ARGUMENTS}}`

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

6. After filling in all docs, run ` + "`rivet sync`" + ` to update {{INSTRUCTION_FILE}}.
{{ARGUMENTS}}`

const compactContextSkill = `---
name: rivet-compact-context
description: Review and tighten rivet context docs — trim stale info, keep docs concise, bump last_reviewed
---

Review all rivet context documents and tighten them up. This is about the
curated layer (.rivet/context/) — for the capture layer (learning log), use
{{PROMOTE_SKILL}} instead.

## Instructions

1. Run ` + "`rivet context list`" + ` to see all context documents.

2. Run ` + "`rivet context lint`" + ` — address ` + "`missing-owner`" + `, ` + "`stale-review`" + `,
   ` + "`stale-related-path`" + `, ` + "`stale-reference`" + ` findings.

3. For each document, read it and check for:

   **Overall doc size:**
   - Each doc should be 20-50 lines of content max (excluding frontmatter)
   - If a doc has grown beyond that, trim verbose descriptions to single sentences
   - Merge overlapping bullet points
   - Remove obvious information that anyone could see from reading the code — only keep non-obvious insights

   **Staleness:**
   - Use ` + "`recon.grep`" + ` to verify key modules/functions mentioned still exist
   - If a module was renamed or removed, update or remove the reference
   - If a gotcha was fixed, remove it

   **Frontmatter hygiene:**
   - Ensure ` + "`owner`" + ` is set
   - Bump ` + "`last_reviewed`" + ` to today's date once you've reviewed the doc

4. Do NOT remove ` + "`tags`" + ` / ` + "`related_paths`" + ` or the High-risk files section — those
   are regenerated by ` + "`rivet context scaffold`" + `.

5. After compacting, run ` + "`rivet sync`" + ` to update {{INSTRUCTION_FILE}}.

6. Summarize what you changed: how many docs reviewed, lines saved, docs
   re-reviewed.
{{ARGUMENTS}}`

const promoteLearningsSkill = `---
name: rivet-promote-learnings
description: Review the rivet learning log and promote high-value entries into curated context docs
---

Review the capture-layer learning log at .rivet/learnings/ and promote the
valuable entries into curated context docs at .rivet/context/.

## Principles

- Not every learning deserves permanence. Be selective.
- Promote only when the learning is: recurring, non-obvious from code, likely
  to impact future work, and specific enough to guide action.
- Merge related entries — one promoted bullet can cover multiple learnings.
- Stale or one-off entries should be archived, not promoted.

## Instructions

1. Run ` + "`rivet learnings list`" + ` to see active (un-promoted) entries.

2. For each entry (` + "`rivet learnings show <name>`" + `):

   a. Decide: promote, merge-with-another, or archive.

   b. If **promote**: open the target context doc (use ` + "`suggested_doc`" + ` from
      the entry's frontmatter as a hint, or use ` + "`rivet context recommend`" + `
      against the entry's title). Merge the Observation / Impact / Recommendation
      into the doc's Gotchas or Guidance section as a concise bullet. Bump
      ` + "`last_reviewed`" + ` in the doc's frontmatter to today.

   c. If **merge**: combine the content of related entries into the promotion
      bullet, then mark all of them promoted.

   d. Mark the entry promoted:
      ` + "`rivet learnings promote <name> --to <doc> --archive`" + `

3. If an entry is no longer relevant (fixed, superseded, too speculative),
   archive it without promoting: manually move to ` + "`.rivet/learnings/archive/`" + `
   or delete.

4. After promotion passes, run ` + "`rivet sync`" + ` to update {{INSTRUCTION_FILE}}.

5. Summarize: how many entries promoted, merged, archived; which context docs
   were updated.
{{ARGUMENTS}}`
