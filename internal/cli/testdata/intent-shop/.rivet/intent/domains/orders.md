---
tags: [orders, checkout, stock]
owner: orders-team
last_ratified: 2099-01-01
prefix: ORD
---

# Orders — intent

## Invariants

- **ORD-001** An order cannot be placed for more units than are in stock.
  why: overselling forces cancellations, which cost more than the lost sale
