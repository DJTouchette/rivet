---
tags: [orders, order, checkout, cart, fulfillment, shipping]
related_paths:
  - services/orders/**
owner: storefront-team
last_reviewed: 2026-01-30
---

# Orders Domain

## Purpose
Models the customer journey from cart to fulfilled shipment. The order is the
single source of truth for what was bought, at what price, and where it goes.

## Lifecycle
`cart -> placed -> authorized -> picked -> shipped -> delivered`, with
`cancelled` reachable from any state before `shipped`.

## Cart abandonment
A cart with no activity for 30 minutes is considered abandoned. Abandoned
carts are kept for 14 days so the recovery campaign can reference them, then
hard-deleted. Abandonment is measured at cart level, never at line-item level,
because a partially emptied cart is still an active cart.

## Pricing
Prices are snapshotted onto the order at `placed`. A later catalogue price
change never mutates an existing order.

## Boundaries
Orders never talk to the payment gateway directly; it asks the billing domain
to authorize and capture. Search and merchandising are read-only consumers of
the catalogue, not of orders.
