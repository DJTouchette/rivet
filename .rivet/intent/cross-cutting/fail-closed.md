---
tags: [fail-closed, unproven, coverage, witness, scan, false-green, errors]
owner: djtouchette
last_ratified: 2026-10-07
prefix: FC
---

# Fail closed — intent

## Purpose

Rivet's answers feed agents and CI jobs that cannot tell "found nothing" from
"could not look". Whenever rivet could not compute an answer, it has to say so
in the channel the reader actually checks.

## Invariants

- **FC-001** An empty witness answer is never reported as success: the tool
  call is flagged as an error and the coverage called unproven.
  why: a client reads isError, not prose; an unflagged empty answer becomes
  "no tests needed"
- **FC-002** When the code cannot be scanned for intent markers, rule coverage
  is reported as an error, never as an empty pass.
  why: coverage nobody measured has not been shown, and a green CI on it is a
  false green
