---
tags: [ledger, accounting, double-entry, journal, gl]
related_paths:
  - services/finance/posting/**
owner: finance-eng
last_reviewed: 2025-11-22
---

# Double-Entry Ledger

## What it does
The general ledger that every money movement is eventually posted to. Journal
entries are append-only and always balance to zero across debits and credits.

## Posting rules
- A journal entry is written once and never amended. A mistake is corrected by
  a reversing entry that references the original.
- Entries are posted asynchronously from the source event, so the ledger lags
  the operational system by up to a minute. Reconciliation tolerates the lag;
  reporting must not read the ledger for anything sub-minute.
- Accounting periods close on the 5th business day. A posting dated into a
  closed period is rejected outright, not silently moved.

## Chart of accounts
The account tree is owned by the finance team and lives in a versioned CSV,
not in code. Adding an account is a data change with a review, not a deploy.

## Status
This module predates the current service split and is on the long tail of the
migration plan. It is still the system of record for statutory reporting, so
it is maintained but not extended.
