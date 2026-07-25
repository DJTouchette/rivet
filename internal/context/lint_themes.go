package context

import (
	"sort"
	"strings"
	"unicode"
)

// A doc can be about something it never says it's about. Tags are how retrieval
// learns a doc's subject, and they're the one part nobody re-reads: prose gets
// rewritten, frontmatter written once. So a doc drifts until its body covers a
// topic its tags never mention, and it stops being findable by that topic.
//
// This was measured, not guessed. In the retrieval eval corpus, search-indexer's
// body says analyzer changes alter relevance while tagging neither `relevance`
// nor `ranking`; a query about exactly that lands on the broad search domain
// instead. Adding the two tags flips it to the right doc. The scorer is behaving
// correctly there — it is honouring an explicit authoring signal the module
// never gave — so the fix belongs here, at authoring time.
//
// The rule has to be quiet to be useful. It gates CI in this repo, so a term is
// only reported when it is repeated, distinctive across the corpus, and not
// already implied by the tags, name or title.

const (
	// themeMinOccurrences is how often a term must appear before it counts as a
	// theme rather than a passing mention.
	//
	// Calibrated twice, and the second time is the one that counts.
	//
	// First pass used this repo's own nine docs and settled on 4. That was a bad
	// corpus for the job: freshly written, by one author, in one sitting. Run
	// against a real 55-doc corpus that had grown organically, threshold 4
	// produced 143 warnings — 2.6 per document — which is not a list anyone
	// reads, and a rule nobody reads is a rule that does nothing.
	//
	// Measured on that corpus: 4 -> 143, 6 -> 61, 8 -> 28, 10 -> 9. Eight lands
	// at roughly one warning per two documents while still surfacing the terms
	// that matter (a domain doc saying "principals" nine times and tagging none
	// of it). Ten is quieter but starts dropping real misses, because the
	// per-document cap has to fall with it.
	//
	// Note this is a different kind of tuning from fitting a metric. The output
	// is read by a person, so "how many warnings will they act on" is the actual
	// design constraint, not a number to be maximised.
	themeMinOccurrences = 8

	// themeMaxDocFraction keeps a term distinctive. A word half the corpus uses
	// says nothing about which doc owns it, however often one repeats it.
	themeMaxDocFraction = 0.20

	// themeMinLength drops short words, which are overwhelmingly grammar.
	themeMinLength = 5

	// themeMaxReported bounds the noise from any single document.
	themeMaxReported = 2
)

// themeCandidate is a repeated body term and how often it appeared.
type themeCandidate struct {
	term  string
	count int
}

// untaggedThemes returns terms a document dwells on that its tags, name and
// title never mention. docFreq is how many documents each term appears in.
func untaggedThemes(doc *Document, docFreq map[string]int, corpusSize int) []themeCandidate {
	// A doc with no tags at all is already reported by missing-tags; piling on
	// with every theme it has would bury that.
	if len(doc.Tags) == 0 {
		return nil
	}

	counts := bodyTermCounts(doc.Body)
	if len(counts) == 0 {
		return nil
	}

	maxDocs := float64(corpusSize) * themeMaxDocFraction

	// Singular and plural are one theme, not two. Counting them separately
	// reported "adapter" and "adapters" as independent misses of the same word,
	// and split a term's frequency so neither half cleared the threshold.
	counts = mergeByStem(counts)

	var out []themeCandidate
	for term, n := range counts {
		if n < themeMinOccurrences {
			continue
		}
		// Distinctive to a few docs. corpusSize can be small in tests, so a term
		// unique to this doc always qualifies regardless of the fraction.
		if df := docFreq[term]; df > 1 && float64(df) > maxDocs {
			continue
		}
		if coveredByMetadata(term, doc) {
			continue
		}
		out = append(out, themeCandidate{term: term, count: n})
	}

	// Most-repeated first, name as a stable tiebreak so output never reorders
	// between runs.
	sort.Slice(out, func(i, j int) bool {
		if out[i].count != out[j].count {
			return out[i].count > out[j].count
		}
		return out[i].term < out[j].term
	})

	if len(out) > themeMaxReported {
		out = out[:themeMaxReported]
	}
	return out
}

// mergeByStem collapses inflections of one word into a single entry, summing
// their counts and reporting the most frequent surface form so the message
// quotes something the author will recognise from their own prose.
func mergeByStem(counts map[string]int) map[string]int {
	type merged struct {
		surface string
		total   int
		best    int
	}

	byStem := make(map[string]*merged, len(counts))
	for term, n := range counts {
		stem := stemProbe(term)
		m, ok := byStem[stem]
		if !ok {
			byStem[stem] = &merged{surface: term, total: n, best: n}
			continue
		}
		m.total += n
		// Ties go to the shorter form, which is the lemma often enough.
		if n > m.best || (n == m.best && len(term) < len(m.surface)) {
			m.surface, m.best = term, n
		}
	}

	out := make(map[string]int, len(byStem))
	for _, m := range byStem {
		out[m.surface] = m.total
	}
	return out
}

// coveredByMetadata reports whether a term is already implied by the doc's tags,
// name or title. Comparison is stemmed both ways, so a body full of "caching"
// does not get reported against a doc tagged "cache".
func coveredByMetadata(term string, doc *Document) bool {
	probe := lintStem(term)

	for _, tag := range doc.Tags {
		tagProbe := lintStem(strings.ToLower(tag))
		if strings.Contains(tagProbe, probe) || strings.Contains(probe, tagProbe) {
			return true
		}
	}

	for _, field := range []string{doc.Name, doc.Title} {
		if strings.Contains(strings.ToLower(field), probe) {
			return true
		}
	}
	return false
}

// lintStem is stemProbe plus the y/ies equivalence, so a doc tagged `queries`
// counts as covering a body full of "query".
//
// That rule lives here rather than in stemProbe because the two callers want
// opposite things. Retrieval wants precision — collapsing every trailing y
// widened probes across unrelated words and measurably cost recall. This check
// wants recall: matching too eagerly only means a warning is skipped, which is
// far cheaper than telling someone to add a tag they already have.
func lintStem(word string) string {
	const minStemLength = 4

	if strings.HasSuffix(word, "ies") && len(word) > minStemLength {
		return strings.TrimSuffix(word, "es")
	}
	if strings.HasSuffix(word, "y") && len(word) > minStemLength {
		return strings.TrimSuffix(word, "y") + "i"
	}
	return stemProbe(word)
}

// bodyTermCounts counts prose words in a body, skipping anything that isn't
// running text.
//
// Fenced blocks and inline code are excluded because identifiers, paths and
// flags repeat constantly and are never tags — reporting `filepath` as an
// untagged theme would be exactly the noise that gets a lint rule switched off.
func bodyTermCounts(body string) map[string]int {
	counts := make(map[string]int)

	inFence := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}

		for _, word := range strings.Fields(stripInlineCode(line)) {
			if term, ok := normalizeTerm(word); ok {
				counts[term]++
			}
		}
	}
	return counts
}

// normalizeTerm lowercases a word and reports whether it is prose worth
// counting. Anything containing a digit or punctuation is treated as an
// identifier rather than a word.
func normalizeTerm(word string) (string, bool) {
	word = strings.ToLower(strings.Trim(word, ".,;:!?()[]{}\"'`*_#>-"))
	if len(word) < themeMinLength {
		return "", false
	}
	for _, r := range word {
		if !unicode.IsLetter(r) {
			return "", false
		}
	}
	if themeStopWords[word] {
		return "", false
	}
	return word, true
}

// themeStopWords are words long enough to survive the length filter but which
// carry no subject meaning. Documentation prose in particular is thick with
// hedges and process verbs that would otherwise dominate every count.
var themeStopWords = map[string]bool{
	"which": true, "there": true, "their": true, "these": true, "those": true,
	"where": true, "when": true, "while": true, "would": true, "could": true,
	"should": true, "because": true, "before": true, "after": true, "about": true,
	"every": true, "other": true, "another": true, "always": true, "never": true,
	"still": true, "again": true, "being": true, "doing": true,
	"first": true, "second": true, "third": true, "instead": true, "rather": true,
	"without": true, "within": true, "through": true, "against": true, "between": true,
	"means": true, "meant": true, "makes": true, "made": true, "gets": true,
	"something": true, "anything": true, "nothing": true, "everything": true,
	"someone": true, "anyone": true, "everyone": true, "cannot": true,
	"however": true, "therefore": true, "otherwise": true, "already": true,
	"actually": true, "really": true, "simply": true, "just": true,
	"usually": true, "often": true, "sometimes": true, "generally": true,
	"whether": true, "either": true, "neither": true, "both": true,
	"itself": true, "themselves": true, "yourself": true,
	"below": true, "above": true, "under": true, "over": true,
	"until": true, "unless": true, "since": true, "during": true,
	// Generic quantifiers. They describe how much of something there is, never
	// what the doc is about, so they cleared the threshold in docs full of
	// metrics without ever being a subject worth tagging.
	"count": true, "counts": true, "number": true, "numbers": true,
	"times": true, "value": true, "values": true, "total": true,
	// Narration verbs. Documentation prose says what a thing "is reported" or
	// "is resolved" constantly, and those describe how the doc talks about its
	// subject rather than what the subject is.
	"reported": true, "resolved": true, "touched": true, "described": true,
	"documented": true, "mentioned": true, "considered": true, "expected": true,
	"returned": true, "treated": true, "applied": true, "called": true,
	// Document-structure nouns: a doc talking about "this table" or "the
	// section above" is describing its own layout, not its subject. "table"
	// costs a little — a database doc really is about tables — but such a doc
	// will already carry the tag, so the miss is cheap and the noise is not.
	"section": true, "sections": true, "table": true, "tables": true,
	"column": true, "columns": true, "heading": true, "headings": true,
	"paragraph": true, "bullet": true,
}

// bodyTermDocFreq counts, for every prose term, how many documents contain it.
func bodyTermDocFreq(docs []*Document) map[string]int {
	freq := make(map[string]int)
	for _, doc := range docs {
		for term := range bodyTermCounts(doc.Body) {
			freq[term]++
		}
	}
	return freq
}
