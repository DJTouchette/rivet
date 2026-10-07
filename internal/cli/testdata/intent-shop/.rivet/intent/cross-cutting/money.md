---
tags: [money, currency, rounding, cents]
owner: platform
last_ratified: 2099-01-01
prefix: MON
---

# Money — intent

## Invariants

- **MON-001** Money is stored and computed as integer minor units, never floats.
  why: floating point loses cents in sums
