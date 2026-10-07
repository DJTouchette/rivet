---
tags: [billing, invoice, dunning, credit-note]
owner: billing-team
last_reviewed: 2099-01-01
related_paths:
  - "services/billing/**"
---

# Billing

Invoices are created by `services/billing/invoice.go`; dunning reminders are
scheduled by `services/billing/dunning.ts`.

## Gotchas

- Corrections go through credit notes, never edits.
