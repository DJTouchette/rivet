---
tags: [webhook, webhooks, delivery, signing, hmac, retry, subscriptions]
related_paths:
  - services/webhooks/**
owner: platform-team
last_reviewed: 2026-02-06
---

# Outbound Webhook Dispatcher

## What it does
Delivers domain events to customer-registered HTTP endpoints, at least once,
in per-subscription order.

## How webhooks are signed
Every request carries `X-Signature: v1=<hex>` and `X-Timestamp`. The signature
is an HMAC-SHA256 over `"{timestamp}.{raw body}"` using the subscription's
secret. Consumers must compare in constant time and reject a timestamp older
than five minutes, otherwise the signature is replayable forever.

Secrets are rotatable: during rotation both the old and new secret produce a
signature and both headers are sent, so consumers can cut over without
downtime.

## Delivery and retry
Non-2xx or timeout retries at 10s, 1m, 10m, 1h, 6h, then the subscription is
marked degraded and the customer is emailed. A subscription failing for 72
hours is auto-disabled.

## Ordering
Per-subscription ordering is preserved by a single in-flight delivery per
subscription. This is why one slow consumer backs up only its own queue.
