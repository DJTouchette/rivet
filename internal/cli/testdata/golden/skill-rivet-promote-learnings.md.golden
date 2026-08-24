---
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

1. Run `rivet learnings list` to see active (un-promoted) entries.

2. For each entry (`rivet learnings show <name>`):

   a. Decide: promote, merge-with-another, or archive.

   b. If **promote**: open the target context doc (use `suggested_doc` from
      the entry's frontmatter as a hint, or use `rivet context recommend`
      against the entry's title). Merge the Observation / Impact / Recommendation
      into the doc's Gotchas or Guidance section as a concise bullet. Bump
      `last_reviewed` in the doc's frontmatter to today.

   c. If **merge**: combine the content of related entries into the promotion
      bullet, then mark all of them promoted.

   d. Mark the entry promoted:
      `rivet learnings promote <name> --to <doc> --archive`

3. If an entry is no longer relevant (fixed, superseded, too speculative),
   archive it without promoting: manually move to `.rivet/learnings/archive/`
   or delete.

4. After promotion passes, run `rivet sync` to update CLAUDE.md.

5. Summarize: how many entries promoted, merged, archived; which context docs
   were updated.

$ARGUMENTS
