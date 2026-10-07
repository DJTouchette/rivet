---
tags: [billing, invoice, dunning, refund, credit-note]
owner: finance
last_ratified: 2099-01-01
prefix: BIL
---

# Billing — intent

## Purpose

Billing turns completed orders into invoices and collects on them. What must
never go wrong: an invoice a customer has seen changing underneath them.

## Invariants

- **BIL-001** An issued invoice is never edited; corrections are credit notes.
  why: tax law requires an immutable audit trail of issued invoices
- **BIL-002** Every invoice total equals the sum of its lines, to the cent.
  why: customers and auditors reconcile line by line

## Policies

- **BIL-010** Dunning reminders start 14 days after the due date.
  why: finance's collection policy, reviewed yearly
- **BIL-011** Invoices over 10,000 EUR need a second approver.
  why: fraud control
  enforced: manual — finance ops approve in the ERP

## Non-goals

- Billing does not calculate tax; the tax service does.

## Retired

- ~~**BIL-003**~~ Invoices are emailed as PDF attachments.
  Replaced by the customer portal in 2024.
