---
tags: [errors, error, wrapping, logging, observability, panic]
owner: platform-team
last_reviewed: 2026-01-12
---

# Error Handling Conventions

## Wrapping
Wrap with `%w` and a verb-phrase that says what was being attempted:
`fmt.Errorf("leasing job %s: %w", id, err)`. Do not prefix with the package
name — the stack already says where you are — and do not capitalise or
punctuate the message.

Wrap exactly once per boundary crossing. A message that reads like
`billing: charging invoice: charging invoice: dial tcp` means someone wrapped
inside a loop.

## Sentinels vs types
Use a sentinel (`errors.Is`) for a condition callers branch on. Use a typed
error (`errors.As`) only when callers need a field from it. Everything else is
an opaque wrapped error.

## Logging
Log an error exactly once, at the outermost boundary that handles it. An error
that is both logged and returned will be logged again by the caller, and the
duplicate makes incident timelines unreadable.

## Panics
Panic only for programmer error that cannot be recovered into a sensible
state. Every goroutine entry point recovers, logs, and increments a counter so
a crash loop is visible as a metric rather than a silence.
