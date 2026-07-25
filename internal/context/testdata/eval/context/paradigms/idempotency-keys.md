---
tags: [idempotency, idempotent, dedupe, deduplication, retry, safety]
owner: platform-team
last_reviewed: 2026-03-14
---

# Idempotency Keys

## Rule
Every write that crosses a network boundary carries an idempotency key, and
that key must be unique per *intended effect*, not per resource.

## Key construction
Compose the key from the identity of the effect and its attempt:
`{resource type}:{resource id}:{operation}:{attempt}`.

Omitting the attempt component is the most common mistake in this codebase.
A key that is stable across attempts turns every retry into a replay: the
remote system returns its cached response for the first attempt, so a
transient failure is permanently frozen into a failure. Key reuse across
attempts is never what you want unless you are deliberately deduplicating a
client double-submit.

## Storage
Keys and their responses are held for 24 hours. After that, a repeat is
treated as a fresh request; this is safe because every operation that uses
keys is also guarded by a state check at the destination.

## Testing
Any handler with a key must have a test that runs it twice with the same key
and asserts one effect, and a test that runs it twice with different keys and
asserts two.
