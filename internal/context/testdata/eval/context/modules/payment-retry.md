---
tags: [retry, retries, backoff, payment, invoice, idempotency, gateway]
related_paths:
  - services/billing/retry/**
  - services/billing/invoice.go
owner: payments-team
last_reviewed: 2026-03-14
---

# Payment Retry Scheduler

## What it does
Re-attempts failed card charges for issued invoices on a bounded schedule:
attempt 1 immediately, then +6h, +24h, +72h, then give up and hand the invoice
to dunning. The schedule is per invoice, not per customer.

## Why invoices fail on the second attempt
This is the single most reported bug in the module and it is almost always the
same root cause. The gateway idempotency key is derived from the invoice id
alone, not from `(invoice id, attempt number)`. The gateway therefore treats
the second attempt as a replay of the first and returns the cached decline
verbatim — including `do_not_honor` — without ever touching the issuer. The
charge looks like it failed again when in truth it was never sent.

Symptoms: attempt 1 declines with a real issuer code, attempts 2..4 decline
within milliseconds with an identical code and identical gateway trace id.

Fix: include the attempt counter in the key. See the `idempotency-keys`
paradigm for the key construction rules.

## Backoff
Backoff is a fixed table rather than exponential jitter because card issuers
rate-limit per merchant per day, and a jittered schedule made the daily cap
hard to reason about.

## Interaction with the job queue
Attempts are enqueued onto the generic background job queue, but the schedule
and the give-up decision live here. Do not add payment-specific behaviour to
the queue.
