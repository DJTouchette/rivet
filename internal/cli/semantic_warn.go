package cli

import (
	"fmt"
	"os"

	"github.com/djtouchette/rivet/internal/context/semantic"
)

// warnSemanticFailure tells the user, on stderr, that an embedding backend was
// configured but did not answer, so the ranking they are looking at is
// lexical-only.
//
// Falling back to lexical is deliberate and stays that way: a dead embedder
// must never take retrieval down with it. But the fallback used to be
// completely silent — `rivet context recommend` with Ollama stopped, or with
// the model not pulled, printed the same results with the same exit code as a
// correctly-configured lexical run. Whoever set RIVET_EMBED_BACKEND had no way
// to learn it was doing nothing, which is how a project ends up with a
// committed 1.2 MB vectors.bin that has never once been read.
//
// A nil scorer means no backend was configured at all, which is not a problem
// and gets no warning.
func warnSemanticFailure(scorer *semantic.Scorer) {
	if scorer == nil {
		return
	}
	if err := scorer.Err(); err != nil {
		fmt.Fprintf(os.Stderr,
			"warning: semantic ranking unavailable (%v) — results below are lexical-only. Run 'rivet doctor' to diagnose.\n", err)
	}
}
