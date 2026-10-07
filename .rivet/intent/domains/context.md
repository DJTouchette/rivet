---
tags: [context, intent, rules, proposals, drafts, runbooks, ratification, agents, authority]
owner: djtouchette
last_ratified: 2026-10-07
prefix: CTX
---

# Context — intent

## Purpose

Rivet's knowledge layer exists so agents act on knowledge people have vouched
for. What must never go wrong: something an agent wrote being served back — to
itself or another agent — with the authority of something a person decided.

## Invariants

- **CTX-001** An agent-drafted runbook is never retrievable until a person promotes it.
  why: a wrong runbook followed under pressure is worse than none
- **CTX-002** Agents never edit ratified intent directly. Their rule changes are
  proposals under .rivet/intent/proposals/, which are never loaded as intent.
  why: a rule an agent wrote would carry the authority of one the business
  decided — "the code does X, so X is the rule" is how a bug becomes policy
- **CTX-003** A rule ID is never reused: two rules sharing an ID fail the check,
  a marker naming a retired rule fails it, and a proposal cannot add a rule
  under an ID that exists.
  why: every marker naming the ID would silently start pointing at a different rule
- **CTX-004** Agents may draft rules, but a drafted rule takes effect only when
  a person approves it at an interactive terminal, and approval refuses a
  draft written against a rule that has since changed.
  why: rules carry the business's authority; a person must decide each one,
  and must never overwrite a newer decision by approving an older draft

## Policies

- **CTX-010** Context lint exits non-zero on any error-severity finding, and on
  any finding at all with --strict.
  why: a findings-based exit code is what makes lint usable as a CI gate

## Non-goals

- Rivet does not decide whether a rule is right. It holds the code to the rules
  people wrote and routes disagreements back to them.
