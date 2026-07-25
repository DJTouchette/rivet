---
related_paths:
  - internal/jobs/worker.go
---

# Worker loop and lease renewal

`Worker.Run` leases a batch with `FOR UPDATE SKIP LOCKED`, then runs each
handler in its own goroutine with the lease renewer ticking at 10s against a
30s lease.

The renewer keeps ticking while a handler is blocked, which is why a wedged
handler holds its row indefinitely and the reaper never sees it. Any change
here should make renewal conditional on handler liveness.

`Run` returns only on context cancellation; a handler panic is recovered,
counted, and the job is released for another attempt.
