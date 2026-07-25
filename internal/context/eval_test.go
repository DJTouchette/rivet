package context

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Retrieval evaluation harness.
//
// Recommend's scoring is a sum of hand-tuned constants (tag 0.5/0.6, name
// 0.4/0.5/0.6, body 0.2/0.3, path 0.7, semantic 0.45, times a per-kind
// weight). Nothing in the existing tests says whether nudging one of those
// numbers makes retrieval better or worse — they assert mechanics (a signal
// fires, a fallback works), not quality.
//
// This file supplies the missing instrument: a fixed corpus, a hand-labelled
// golden set, and the standard retrieval metrics over them. It is a RATCHET,
// not an aspiration. The thresholds below are the values measured from the
// scorer as it stands, so the test passes today and fails only if a change
// makes retrieval worse. When a tuning change makes things better, raise the
// constants in the same commit.
//
// Everything here is lexical-only and reads from testdata/, so it runs
// offline, in CI, with no embedding backend, and is byte-for-byte
// deterministic (see TestEvalRetrievalIsDeterministic).

const (
	// evalCorpusDir holds the fixture project: a plausible commerce codebase
	// with auth, orders/billing, background jobs, caching, search and deploy
	// context, spanning every retrievable Kind tier.
	evalCorpusDir = "testdata/eval"

	// evalCorpusSize guards against silently evaluating a corpus that lost
	// files — metrics computed over a shrunken corpus are meaningless.
	evalCorpusSize = 21

	// evalDepth is how deep we retrieve. It must exceed the largest k so a
	// miss at 5 still yields a rank for MRR rather than collapsing to 0.
	evalDepth = 10
)

// evalKs are the cutoffs reported and ratcheted. Fixed (not a map) so the
// summary table order is stable.
var evalKs = []int{1, 3, 5}

// goldenQuery is one hand-labelled (query -> expected docs) pair.
//
// want holds document Names. Where more than one doc is a legitimate answer
// the set is genuinely ambiguous and both are listed; see evalRecall for how
// that is scored. why records the human justification, so a future reader can
// tell a scoring bug from a mislabelled golden — it is printed in diagnostics.
type goldenQuery struct {
	query string
	want  []string
	why   string
}

// goldenQueries is the labelled set: realistic things an agent asks before
// touching code. Labels are what a human maintainer of the fixture project
// would hand you, decided from the corpus alone and deliberately NOT from
// what the current scorer happens to return.
var goldenQueries = []goldenQuery{
	// --- keyword-ish queries -------------------------------------------------
	{
		query: "retry logic",
		want:  []string{"payment-retry", "job-queue"},
		why:   "genuinely ambiguous: both own a retry schedule. Probes whether shared vocabulary lands on the two owners rather than on the wiki FAQ that merely repeats the word.",
	},
	{
		query: "payment retries",
		want:  []string{"payment-retry"},
		why:   "adversarial: the wiki FAQ is keyword-stuffed with payment/retries and would outrank the curated module if kind tiers were not weighted.",
	},
	{
		query: "session expiry",
		want:  []string{"session-cache"},
		why:   "session-cache owns both expiry clocks; the auth domain deliberately defers storage/expiry to it.",
	},
	{
		query: "cache invalidation strategy",
		want:  []string{"cache-layer"},
		why:   "cache-layer has an Invalidation strategy section; session-cache shares the cache vocabulary but is about one keyspace.",
	},
	{
		query: "error wrapping convention",
		want:  []string{"error-handling"},
		why:   "single unambiguous owner; a sanity check that easy queries stay easy.",
	},
	{
		query: "deploy rollback procedure",
		want:  []string{"deploy-pipeline"},
		why:   "single unambiguous owner.",
	},
	{
		query: "idempotency key reuse across retries",
		want:  []string{"idempotency-keys"},
		why:   "the paradigm states the rule; payment-retry is an instance of it and is an acceptable second, not first.",
	},
	{
		query: "checkout outage postmortem",
		want:  []string{"postmortems/2025-11-checkout-outage"},
		why:   "the wiki tier is the right tier for an incident writeup, so the 0.85 kind weight must not bury it.",
	},

	// --- natural-language queries --------------------------------------------
	{
		query: "why do invoices fail on the second attempt",
		want:  []string{"payment-retry"},
		why:   "payment-retry has a section by almost exactly this name diagnosing the idempotency-key replay. Adversarial against both the billing domain and the wiki FAQ.",
	},
	{
		query: "how do I add a new background job",
		want:  []string{"job-queue"},
		why:   "job-queue has the numbered registration procedure.",
	},
	{
		query: "background jobs stopped draining",
		want:  []string{"job-queue"},
		why:   "symptom phrasing for the Stuck jobs section; the worker.go code doc is a reasonable second.",
	},
	{
		query: "how are webhooks signed",
		want:  []string{"webhook-dispatcher"},
		why:   "single owner, documents the HMAC scheme.",
	},
	{
		query: "oauth login flow",
		want:  []string{"auth"},
		why:   "single owner.",
	},
	{
		query: "revoke a user session",
		want:  []string{"auth"},
		why:   "revocation semantics belong to auth; session-cache only implements the delete, so it is a valid second but not first.",
	},
	{
		query: "cart abandonment window",
		want:  []string{"orders"},
		why:   "single owner; states the 30-minute window.",
	},
	{
		query: "how is search relevance ranked",
		want:  []string{"search"},
		why:   "adversarial against search-indexer, which shares the search vocabulary but owns index build, not ranking.",
	},
	{
		query: "reindex the product catalogue",
		want:  []string{"search-indexer"},
		why:   "mirror of the previous case: the indexer, not the search domain, owns reindexing.",
	},
	{
		query: "invoice stuck in past_due",
		want:  []string{"billing"},
		why:   "past_due is a billing state owned by the dunning sweep; payment-retry has given up by then.",
	},
	{
		query: "what does dunning mean",
		want:  []string{"glossary"},
		why:   "a definition question, so the wiki glossary IS the right answer. Probes whether the kind down-weight suppresses wiki docs even when the wiki is correct.",
	},
	{
		query: "how do I set up my local environment",
		want:  []string{"onboarding/engineering-onboarding"},
		why:   "the only doc about local setup; nothing else mentions make bootstrap.",
	},
	{
		query: "the same charge is declined instantly on every repeat",
		want:  []string{"payment-retry"},
		why:   "a symptom description with almost no literal overlap: payment-retry writes 'declines', 'identical' and 'milliseconds', not 'declined', 'same' or 'instantly'. Deliberately hard — this is the class of query the semantic signal exists to rescue, and it marks how far pure lexical matching gets on its own.",
	},

	// --- file-path queries (should land via related_paths) --------------------
	{
		query: "services/billing/retry/backoff.go",
		want:  []string{"payment-retry"},
		why:   "matches payment-retry's services/billing/retry/** glob; billing's services/billing/** also matches, so this probes glob specificity.",
	},
	{
		query: "services/finance/posting/journal.go",
		want:  []string{"legacy-ledger"},
		why:   "path-glob-ONLY case: no token in this path appears in legacy-ledger's name, title, tags or body, so the path signal is the sole route to the answer.",
	},
	{
		query: "internal/cache/session/store.go",
		want:  []string{"session-cache"},
		why:   "two globs match (internal/cache/** and internal/cache/session/**); the more specific one is the right answer.",
	},
	{
		query: "internal/jobs/worker.go",
		want:  []string{"internal/jobs/worker.go", "job-queue"},
		why:   "the code-extracted doc for that exact file plus the module that owns the tree; both are correct.",
	},
	{
		query: "services/search/indexer/backfill.go",
		want:  []string{"services/search/indexer/backfill.go", "search-indexer"},
		why:   "same shape as above, across a different tier pair.",
	},
	{
		query: ".github/workflows/release.yml",
		want:  []string{"deploy-pipeline"},
		why:   "a non-source path; the only doc claiming .github/workflows/**.",
	},

	// --- harder set ----------------------------------------------------------
	// recall@3 and @5 hit 1.000 on the queries above, which left the harness no
	// headroom to detect an improvement or a subtle regression. These are chosen
	// to be genuinely difficult: distinctive vocabulary buried mid-body, symptom
	// phrasing that shares almost nothing with the doc, and pairs where the
	// broad doc and the specific doc both look plausible.
	{
		query: "blue green index swap",
		want:  []string{"search-indexer"},
		why:   "the alias flip is described only in the indexer; the search domain explicitly disclaims owning the index build. Distinctive vocabulary, none of it in tags.",
	},
	{
		query: "a cold key is hammering the database",
		want:  []string{"cache-layer"},
		why:   "single-flight/stampede protection. The doc says 'stampede', the query says 'hammering' — near-zero literal overlap, so this leans on body matching finding 'cold key'.",
	},
	{
		query: "SCAN over a hot keyspace",
		want:  []string{"cache-layer"},
		why:   "a specific operational warning that appears mid-body with no tag support.",
	},
	{
		query: "my retry keeps replaying the first attempt response",
		want:  []string{"idempotency-keys"},
		why:   "the retry-as-replay failure is idempotency's, not the retry scheduler's, though payment-retry shares the retry and idempotency tags. Probes whether a paradigm can outrank a module on body evidence.",
	},
	{
		query: "double entry journal posting",
		want:  []string{"legacy-ledger"},
		why:   "distinctive vocabulary owned by exactly one doc. A floor check: if this ever misses, something is badly wrong.",
	},
	{
		query: "why did checkout go down",
		want:  []string{"postmortems/2025-11-checkout-outage"},
		why:   "second legitimate-wiki case, in a nested directory, phrased as a question with no shared vocabulary beyond 'checkout'.",
	},
	{
		query: "webhook deliveries keep getting retried",
		want:  []string{"webhook-dispatcher"},
		why:   "'retry' is a tag on four docs. The distinguishing token is 'webhook', so this checks that one precise tag beats three docs sharing the generic one.",
	},
	{
		query: "an analyzer change altered ranking",
		want:  []string{"search-indexer"},
		why:   "mapping/analyzer changes belong to the indexer even though the observable effect is relevance, which is the search domain's subject. Deliberately cross-cutting.",
	},
	{
		query: "lease renewal stopped and jobs got picked up twice",
		want:  []string{"internal/jobs/worker.go", "job-queue"},
		why:   "symptom phrasing that should reach a code-extracted doc; tests that the code tier is reachable by prose, not only by path.",
	},
}

// --- baseline (the ratchet) --------------------------------------------------
//
// MEASURED BASELINE — 2026-07-24, lexical-only Recommend, 27 golden queries
// over the 21-doc fixture corpus, against the scoring constants as they stand
// (tag 0.5/0.6, name 0.4/0.5/0.6, body 0.2/0.3, path 0.7, kind 1.0/0.9/0.85):
//
//	recall@1 = 0.815 (22/27)   recall@3 = 0.963 (26/27)
//	recall@5 = 0.963 (26/27)   MRR      = 0.894
//
// That first measurement named four scoring defects, three of which have since
// been fixed in recommend.go:
//
//  1. FIXED — scorePathMatch returned a flat 0.7 for ANY matching glob, so a
//     broad glob (services/billing/**) tied exactly with the specific one
//     (services/billing/retry/**) and the alphabetical name tiebreak picked the
//     winner. It now scores by the share of query path segments a pattern pins
//     down, and takes the best pattern rather than the first.
//  2. FIXED — a keyword-stuffed wiki doc reached the 1.0 clamp, and the clamp
//     ran AFTER kindWeight, erasing the 0.85 wiki penalty at exactly the top of
//     the range where it was meant to bite. Scores are now squashed
//     asymptotically before the tier weight, so saturation can neither erase a
//     tier penalty nor collapse distinct scores into a tie.
//  3. FIXED — no stemming, so "declined" did not match "declines". Query tokens
//     are now trimmed to a substring probe before matching body and name text.
//     Single-character tokens are also dropped: the "I" in "how do I…" body-
//     matched nearly every document and diluted real coverage ratios.
//  4. FIXED (mostly) — a broad doc outranked the specific one because coverage
//     counted every token alike, so three ubiquitous words beat the one rare
//     word that actually identified the subject. Tag, name and body coverage
//     are now weighted by inverse document frequency measured over the live
//     corpus (see tokenIDF). That is a general fix; the alternative — asserting
//     that modules are more specific than domains — is a semantic claim the
//     corpus does not support, and would break the queries where the domain is
//     genuinely right.
//
// Post-fix measurement on those same 27 goldens:
//
//	recall@1 = 0.926 (25/27)   recall@3 = 1.000 (27/27)
//	recall@5 = 1.000 (27/27)   MRR      = 0.957
//
// HARDER SET — recall@3 and @5 hit ceiling there, leaving no headroom to detect
// either an improvement or a subtle regression, so nine harder goldens were
// added (36 total): distinctive vocabulary buried mid-body, symptom phrasing
// sharing almost nothing with the target, and broad/specific pairs in both
// directions. Note that CHANGING THE GOLDEN SET RESETS COMPARABILITY — metrics
// are only comparable against a fixed corpus and a fixed golden set, so the
// numbers below cannot be read against the ones above.
//
// TERM FREQUENCY — the IDF work above left term frequency binary: a doc was
// asked whether a token appeared, never how much of it was about that token.
// That could not tell "the subject of a section" from "named once in a
// Boundaries section that disclaims it", which is how the search domain — whose
// own text says reindexes belong to search-indexer — outranked search-indexer
// for a reindexing query. A doc was retrieved for the thing it explicitly
// disowned, and that pattern is common in exactly the best-written docs.
//
// scoreBodyMatchWeighted now applies BM25-shaped saturating term frequency.
// Saturating rather than linear: unbounded frequency would hand the win to
// whatever repeats a word most, which is the keyword-stuffed FAQ this corpus
// exists to guard against.
//
// Current measurement — 36 goldens, 21-doc corpus, IDF + saturating TF:
//
//	recall@1 = 0.944 (34/36)   recall@3 = 1.000 (36/36)
//	recall@5 = 1.000 (36/36)   MRR      = 0.972
//
// The thresholds below are set AT those values, deliberately, so the test
// passes today and can only fail on a regression. They are a floor, not a goal.
// Raise them when a change moves them up.
//
// Notably, TF rescued "the same charge is declined instantly on every repeat" —
// the query labelled as the case only embeddings could reach. Term density got
// there without them, which moves where the semantic signal actually earns its
// keep.
//
// Two goldens still miss at rank 1, both landing at rank 2, and they fail for
// DIFFERENT reasons — worth keeping straight before anyone tunes at them:
//
//   - "reindex the product catalogue" is a near-tie: 0.663 vs 0.644 after TF
//     narrowed it from a 0.083 gap. The remainder is the domain's title
//     matching "product". Closing 0.019 means turning an arbitrary knob, and
//     the two docs genuinely are both relevant. Left alone deliberately.
//   - "an analyzer change altered ranking" is NOT a scorer defect. search-indexer
//     is under-tagged: its body says analyzer changes alter relevance, but it
//     tags neither `relevance` nor `ranking`, so the domain wins on an explicit
//     authoring signal the module never gave. Verified by adding those two tags
//     to the fixture, which flips the query to rank 1 (reverted — fitting the
//     corpus to the test would defeat the point). The real fix is a lint rule
//     that flags a doc whose body features a term absent from its tags. Do not
//     "fix" this in the scorer: that would mean overriding explicit tags with
//     implicit body evidence, which is backwards.
//
// recall@3 and @5 are at ceiling again. The discriminating power is at @1 and
// MRR; if those saturate, add harder goldens rather than declaring victory.
const (
	baselineRecallAt1 = 0.944
	baselineRecallAt3 = 1.000
	baselineRecallAt5 = 1.000
	baselineMRR       = 0.972

	// evalEpsilon absorbs float64 formatting noise when comparing a freshly
	// computed metric against a constant literal rounded to three places.
	evalEpsilon = 1e-3
)

// --- harness -----------------------------------------------------------------

// loadEvalCorpus reads the fixture project through the real loaders, so the
// harness exercises actual frontmatter parsing rather than hand-built structs.
// Code docs are loaded from a directory tree that mirrors the source layout:
// walkDocs names a doc by its path relative to the root, which reproduces
// LoadCodeDocs' naming for file-level sidecars (name == the annotated file).
func loadEvalCorpus(t *testing.T) []*Document {
	t.Helper()

	docs, err := Load(filepath.Join(evalCorpusDir, "context"))
	if err != nil {
		t.Fatalf("loading curated context docs: %v", err)
	}
	wiki, err := walkDocs(filepath.Join(evalCorpusDir, "wiki"), KindWiki, map[string]bool{})
	if err != nil {
		t.Fatalf("loading wiki docs: %v", err)
	}
	code, err := walkDocs(filepath.Join(evalCorpusDir, "code"), KindCode, map[string]bool{})
	if err != nil {
		t.Fatalf("loading code docs: %v", err)
	}

	docs = append(docs, wiki...)
	docs = append(docs, code...)
	sortDocs(docs)

	if len(docs) != evalCorpusSize {
		t.Fatalf("corpus has %d docs, want %d — metrics are only comparable against a fixed corpus", len(docs), evalCorpusSize)
	}
	return docs
}

// queryEval is one golden query's outcome.
type queryEval struct {
	golden goldenQuery
	recs   []Recommendation
	rankOf map[string]int  // expected doc name -> 1-based rank, 0 if not retrieved
	recall map[int]float64 // k -> recall@k
	rr     float64         // reciprocal rank of the first relevant hit, 0 if none
}

// evalSummary is the aggregate over the golden set.
type evalSummary struct {
	docs    int
	queries int
	recall  map[int]float64
	mrr     float64
	signals map[string]int // how often each signal fired on a rank-1 hit
}

// runEval scores every golden query. opts are passed through to Recommend so a
// future caller can evaluate an alternative configuration (a deterministic
// semantic stub, say) against the same goldens without touching the harness.
func runEval(t *testing.T, docs []*Document, opts ...Option) ([]queryEval, evalSummary) {
	t.Helper()

	evals := make([]queryEval, 0, len(goldenQueries))
	sum := evalSummary{
		docs:    len(docs),
		queries: len(goldenQueries),
		recall:  map[int]float64{},
		signals: map[string]int{},
	}

	for _, g := range goldenQueries {
		recs := Recommend(docs, g.query, evalDepth, opts...)

		qe := queryEval{
			golden: g,
			recs:   recs,
			rankOf: map[string]int{},
			recall: map[int]float64{},
		}
		for _, w := range g.want {
			qe.rankOf[w] = 0
		}
		for i, r := range recs {
			if _, expected := qe.rankOf[r.Name]; expected && qe.rankOf[r.Name] == 0 {
				qe.rankOf[r.Name] = i + 1
			}
		}

		for _, k := range evalKs {
			qe.recall[k] = evalRecall(qe.rankOf, k)
			sum.recall[k] += qe.recall[k]
		}

		// Reciprocal rank: 1/rank of the first expected doc anywhere in the
		// retrieved depth. 0 when none was retrieved.
		best := 0
		for _, rank := range qe.rankOf {
			if rank > 0 && (best == 0 || rank < best) {
				best = rank
			}
		}
		if best > 0 {
			qe.rr = 1 / float64(best)
		}
		sum.mrr += qe.rr

		if len(recs) > 0 {
			for _, s := range recs[0].Signals {
				sum.signals[s]++
			}
		}

		evals = append(evals, qe)
	}

	n := float64(len(goldenQueries))
	for _, k := range evalKs {
		sum.recall[k] /= n
	}
	sum.mrr /= n

	return evals, sum
}

// evalRecall computes recall@k for one query as
//
//	|expected ∩ top-k| / min(|expected|, k)
//
// The min() denominator is what makes multi-answer goldens fair: with two
// equally-correct docs, surfacing either one at rank 1 is a full hit at k=1
// (you cannot fit two docs in one slot), while at k=3 both are required. A
// plain |expected| denominator would punish the scorer for the shape of the
// label rather than for the ranking.
func evalRecall(rankOf map[string]int, k int) float64 {
	hits := 0
	for _, rank := range rankOf {
		if rank > 0 && rank <= k {
			hits++
		}
	}
	denom := len(rankOf)
	if k < denom {
		denom = k
	}
	if denom == 0 {
		return 0
	}
	return float64(hits) / float64(denom)
}

// TestEvalRetrievalQuality is the ratchet. It measures the golden set and
// fails if any metric drops below the committed baseline. It never fails for
// being *too good* — improvements are reported and the constants are meant to
// be raised by hand, so a lucky change cannot silently become the new floor.
func TestEvalRetrievalQuality(t *testing.T) {
	docs := loadEvalCorpus(t)
	evals, sum := runEval(t, docs)

	reportEval(t, sum)
	reportMisses(t, evals)

	// Table-driven so a new metric is one line, and so the failure message
	// names the metric rather than a line number.
	checks := []struct {
		name string
		got  float64
		min  float64
	}{
		{"recall@1", sum.recall[1], baselineRecallAt1},
		{"recall@3", sum.recall[3], baselineRecallAt3},
		{"recall@5", sum.recall[5], baselineRecallAt5},
		{"MRR", sum.mrr, baselineMRR},
	}
	for _, c := range checks {
		if c.got < c.min-evalEpsilon {
			t.Errorf("REGRESSION: %s = %.3f, below committed baseline %.3f\n"+
				"  retrieval got worse. The per-query diagnostics above show which\n"+
				"  golden queries moved; fix the scoring or, if the goldens are wrong,\n"+
				"  fix the labels — do not lower the baseline to make this pass.",
				c.name, c.got, c.min)
		}
		if c.got > c.min+evalEpsilon {
			t.Logf("IMPROVED: %s = %.3f, above baseline %.3f — raise the baseline constant to lock this in", c.name, c.got, c.min)
		}
	}
}

// TestEvalRetrievalIsDeterministic guards the harness itself. Recommend sorts
// with sort.Slice (unstable) and the harness walks maps, so a scoring change
// that introduced a tie without a tiebreak would show up as a flaky CI failure
// rather than an obvious bug. Two identical runs must agree exactly.
func TestEvalRetrievalIsDeterministic(t *testing.T) {
	docs := loadEvalCorpus(t)

	_, first := runEval(t, docs)
	for i := 0; i < 3; i++ {
		evals, again := runEval(t, docs)
		if again.mrr != first.mrr {
			t.Fatalf("run %d: MRR %.6f != %.6f — scoring is not deterministic", i, again.mrr, first.mrr)
		}
		for _, k := range evalKs {
			if again.recall[k] != first.recall[k] {
				t.Fatalf("run %d: recall@%d %.6f != %.6f — scoring is not deterministic", i, k, again.recall[k], first.recall[k])
			}
		}
		// Ranked order, not just the aggregate: two docs tied on score and
		// broken only by chance would average out in the metrics.
		for _, qe := range evals {
			ordered := make([]string, len(qe.recs))
			for j, r := range qe.recs {
				ordered[j] = r.Name
			}
			if got := strings.Join(ordered, "|"); got != orderingFor(docs, qe.golden.query) {
				t.Fatalf("query %q: ranking changed between runs:\n  %s", qe.golden.query, got)
			}
		}
	}
}

func orderingFor(docs []*Document, query string) string {
	recs := Recommend(docs, query, evalDepth)
	names := make([]string, len(recs))
	for i, r := range recs {
		names[i] = r.Name
	}
	return strings.Join(names, "|")
}

// TestEvalCorpusIsWellFormed checks the fixture itself, so a broken corpus
// reads as a corpus problem instead of a mysterious metrics drop.
func TestEvalCorpusIsWellFormed(t *testing.T) {
	docs := loadEvalCorpus(t)

	byName := map[string]*Document{}
	kinds := map[Kind]int{}
	for _, d := range docs {
		if prev, dup := byName[d.Name]; dup {
			t.Errorf("duplicate doc name %q (%s and %s)", d.Name, prev.Path, d.Path)
		}
		byName[d.Name] = d
		kinds[d.Kind]++

		if d.Title == "" || d.Title == d.Name {
			t.Errorf("%s: no # heading, title fell back to the filename", d.Path)
		}
		if d.Body == "" {
			t.Errorf("%s: empty body", d.Path)
		}
		// Code docs are extracted from source and carry no tags (see
		// LoadCodeDocs); every other tier is curated and must be tagged, since
		// tag-match is the strongest lexical signal.
		if d.Kind != KindCode && len(d.Tags) == 0 {
			t.Errorf("%s: no tags", d.Path)
		}
	}

	// Every tier must be represented or the kind weights are not under test.
	for _, k := range []Kind{KindDomain, KindModule, KindParadigm, KindWiki, KindCode} {
		if kinds[k] == 0 {
			t.Errorf("corpus has no %s docs — kindWeight(%s) is untested", k, k)
		}
	}

	// Every golden label must name a real doc, or the metric silently caps
	// below 1.0 forever.
	for _, g := range goldenQueries {
		if len(g.want) == 0 {
			t.Errorf("golden %q has no expected docs", g.query)
		}
		for _, w := range g.want {
			if byName[w] == nil {
				t.Errorf("golden %q expects unknown doc %q", g.query, w)
			}
		}
	}
}

// --- reporting ---------------------------------------------------------------

// reportEval prints the summary table. t.Logf output surfaces with -v, and
// automatically on failure, which is exactly when it is wanted.
func reportEval(t *testing.T, sum evalSummary) {
	t.Helper()
	t.Logf("")
	t.Logf("retrieval eval — %d golden queries over %d docs (lexical only, no embeddings)", sum.queries, sum.docs)
	t.Logf("  %-10s %8s %10s", "METRIC", "VALUE", "BASELINE")
	t.Logf("  %-10s %8.3f %10.3f", "recall@1", sum.recall[1], baselineRecallAt1)
	t.Logf("  %-10s %8.3f %10.3f", "recall@3", sum.recall[3], baselineRecallAt3)
	t.Logf("  %-10s %8.3f %10.3f", "recall@5", sum.recall[5], baselineRecallAt5)
	t.Logf("  %-10s %8.3f %10.3f", "MRR", sum.mrr, baselineMRR)

	// Which signals actually carry the top hit. If one signal decides nearly
	// every query, the other weights are not really doing any work and tuning
	// them is theatre.
	names := make([]string, 0, len(sum.signals))
	for s := range sum.signals {
		names = append(names, s)
	}
	sort.Strings(names)
	t.Logf("")
	t.Logf("  signals present on the rank-1 result:")
	for _, s := range names {
		t.Logf("    %-16s %d/%d queries", s, sum.signals[s], sum.queries)
	}
}

// reportMisses prints per-query diagnostics for every query that did not place
// all of its expected docs in the top 3 — what outranked the right answer, on
// which signals, and at what score. This is the output that makes the harness
// useful for tuning rather than merely a pass/fail gate.
func reportMisses(t *testing.T, evals []queryEval) {
	t.Helper()

	t.Logf("")
	t.Logf("per-query results (--> marks an expected doc):")
	for _, qe := range evals {
		status := "ok  "
		if qe.recall[1] < 1 {
			status = "MISS"
		}
		t.Logf("")
		t.Logf("%s %-46q want=%s  rank@1=%.2f rank@3=%.2f rr=%.2f",
			status, qe.golden.query, strings.Join(qe.golden.want, ","),
			qe.recall[1], qe.recall[3], qe.rr)

		if qe.recall[3] >= 1 && qe.recall[1] >= 1 {
			continue // clean hit; the one-line summary above is enough
		}

		t.Logf("     label rationale: %s", qe.golden.why)
		expected := map[string]bool{}
		for _, w := range qe.golden.want {
			expected[w] = true
		}
		shown := len(qe.recs)
		if shown > 6 {
			shown = 6 // enough to see what got in the way
		}
		for i := 0; i < shown; i++ {
			r := qe.recs[i]
			marker := "   "
			if expected[r.Name] {
				marker = "-->"
			}
			t.Logf("     %s %2d. %.3f  %-38s %-8s %s",
				marker, i+1, r.Score, r.Name, r.Kind, strings.Join(r.Signals, "+"))
		}
		for _, w := range qe.golden.want {
			if qe.rankOf[w] == 0 {
				t.Logf("     !!! %q never retrieved (searched top %d) — no signal fired for it at all", w, evalDepth)
			} else if qe.rankOf[w] > shown {
				t.Logf("     --> %q is down at rank %d", w, qe.rankOf[w])
			}
		}
	}
}
