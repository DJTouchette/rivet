---
title: Doctor tests need a live local embedding contract and matching index identity
date: 2026-10-03
confidence: high
related_paths:
  - internal/doctor/**
promoted: false
---

# Doctor tests need a live local embedding contract and matching index identity

## Observation
Doctor probes an Ollama-shaped endpoint and matches persisted model, host and dimension. Nonempty arbitrary bytes are not a valid index. Tests now use httptest and semantic.Store, with explicit environment restoration; generic Run fixtures disable the inherited backend.
