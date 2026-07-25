---
tags: [payment, payments, retry, retries, invoice, invoices, billing, dunning, backoff, idempotency, charge, gateway]
last_reviewed: 2025-08-19
---

# Payment Retries FAQ

A grab-bag of questions people have asked in #payments about payment retries,
invoice retries, billing retries and the retry backoff. Written by support,
not by the payments team, and not kept in step with the code.

**Q: How many times do we retry a payment?**
A: Three or four, depending who you ask. Check with the payments team.

**Q: Why did this invoice retry fail again?**
A: Usually the card. Ask the customer to update their payment method. If the
retry keeps failing on every attempt, escalate to the payments team — there
was a bug about idempotency and the retry backoff at some point.

**Q: Can I force a retry?**
A: There used to be an admin button. It may still exist.

**Q: What is dunning?**
A: The emails we send when an invoice is past due.

> This page is retained for search history. For anything authoritative about
> payment retries, invoice charging, or the backoff schedule, read the module
> docs instead of this page.
