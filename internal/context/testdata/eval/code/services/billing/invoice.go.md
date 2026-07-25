---
related_paths:
  - services/billing/invoice.go
---

# Invoice state machine and charge execution

`ChargeInvoice` is the only entry point that talks to the gateway. It loads
the invoice `FOR UPDATE`, refuses anything not in `issued`, increments
`attempt_count`, and hands the attempt to the gateway client.

The `attempt_count` increment happens before the gateway call, deliberately,
so a crash mid-call still burns an attempt rather than looping forever.

`transition` is the only function permitted to write `invoice.state`; it
validates against the allowed transition table and returns
`ErrInvalidTransition` otherwise.
