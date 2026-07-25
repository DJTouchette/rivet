---
tags: [billing, invoice, invoicing, payment, dunning, charge]
related_paths:
  - services/billing/**
  - internal/billing/**
owner: payments-team
last_reviewed: 2026-02-11
---

# Billing Domain

## Purpose
Owns the invoice lifecycle and everything that turns usage into money owed.
An invoice moves through `draft -> issued -> paid | past_due -> written_off`.
Only this domain may transition an invoice; other services request a
transition through the billing API.

## Invariants
- An invoice in `paid` never transitions again. Refunds create a credit note,
  they do not reopen the invoice.
- `past_due` is entered by the dunning sweep, not by a failed charge. A single
  failed charge leaves the invoice `issued` and eligible for another attempt.
- Currency is stored in minor units; there is no floating point anywhere in
  the ledger path.

## Dunning
The dunning sweep runs daily and escalates: reminder email at day 3, service
warning at day 10, suspension at day 21. Escalation state lives on the
invoice, not on the customer.

## Out of scope
The mechanics of *how* a charge is re-attempted (backoff schedule, attempt
counters, gateway idempotency) live in the `payment-retry` module. This doc
only defines when an invoice is eligible to be charged again.
