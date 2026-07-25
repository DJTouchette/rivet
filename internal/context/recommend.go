package context

import (
	"math"
	"path/filepath"
	"sort"
	"strings"
)

// Recommendation is a scored context document match.
type Recommendation struct {
	Document *Document `json:"-"`
	Name     string    `json:"name"`
	Kind     Kind      `json:"kind"`
	Title    string    `json:"title"`
	Score    float64   `json:"score"`
	Signals  []string  `json:"signals"`
	URI      string    `json:"uri"`
}

// Semantic is the optional embedding-based scorer. It is satisfied by
// *semantic.Scorer but defined here so package context has no dependency on the
// embedding machinery — callers that don't configure embeddings never pull it
// in. Prepare is called once per query (and reports whether semantic scoring is
// possible); SimilarityFor returns a cosine in [0,1] for a document's text, or
// ok=false when no comparable vector exists.
type Semantic interface {
	Prepare(query string) bool
	SimilarityFor(id, text string) (float64, bool)
}

// semanticWeight scales cosine similarity into the same additive range as the
// lexical signals. At ~0.45 a strong semantic hit (cosine ~0.8) contributes
// ~0.36 — comparable to a tag match — so it augments rather than dominates.
const semanticWeight = 0.45

// Option configures Recommend. The zero set of options reproduces the original
// lexical-only behaviour, so existing callers are unaffected.
type Option func(*recommendOpts)

type recommendOpts struct {
	sem Semantic
}

// WithSemantic adds an embedding-based "semantic-match" signal. A nil scorer,
// or one whose Prepare reports false, leaves scoring purely lexical.
func WithSemantic(s Semantic) Option {
	return func(o *recommendOpts) { o.sem = s }
}

// Recommend returns context documents relevant to a query.
// The query can be a natural language description, a file path, or keywords.
// Documents are scored by tag match, path pattern match, title/name match,
// body keyword match, and — when an embedder is configured via WithSemantic —
// semantic similarity, then returned sorted by score descending.
func Recommend(docs []*Document, query string, maxResults int, opts ...Option) []Recommendation {
	if maxResults <= 0 {
		maxResults = 5
	}
	if query == "" || len(docs) == 0 {
		return nil
	}

	var o recommendOpts
	for _, opt := range opts {
		opt(&o)
	}

	tokens := tokenize(query)
	isPath := looksLikePath(query)

	// Embed the query once; if the scorer can't prepare, drop to lexical-only.
	semOK := o.sem != nil && o.sem.Prepare(query)

	var results []Recommendation

	for _, doc := range docs {
		var score float64
		var signals []string

		// Signal 1: Tag match (highest weight for exact tag hits)
		tagScore, tagSignal := scoreTagMatch(doc.Tags, tokens)
		if tagScore > 0 {
			score += tagScore
			signals = append(signals, tagSignal)
		}

		// Signal 2: Path pattern match (if query looks like a file path)
		if isPath {
			pathScore, pathSignal := scorePathMatch(doc.RelatedPaths, query)
			if pathScore > 0 {
				score += pathScore
				signals = append(signals, pathSignal)
			}
		}

		// Signal 3: Name/title match
		nameScore, nameSignal := scoreNameMatch(doc.Name, doc.Title, tokens)
		if nameScore > 0 {
			score += nameScore
			signals = append(signals, nameSignal)
		}

		// Signal 4: Body keyword match
		bodyScore, bodySignal := scoreBodyMatch(doc.Body, tokens)
		if bodyScore > 0 {
			score += bodyScore
			signals = append(signals, bodySignal)
		}

		// Signal 5: Semantic similarity (additive; only when configured)
		if semOK {
			if sim, ok := o.sem.SimilarityFor(doc.Name, doc.EmbeddingText()); ok && sim > 0 {
				score += semanticWeight * sim
				signals = append(signals, "semantic-match")
			}
		}

		if score > 0 {
			// Squash the lexical sum into [0,1) *before* the tier weight, not
			// after. Applying a hard clamp last let a doc that summed past
			// ~1.18 come out at 1.0 even after its 0.85 wiki penalty, so the
			// tier weight stopped working exactly at the top of the range where
			// it was supposed to bite. Squash-then-weight guarantees a wiki doc
			// can never reach a curated doc's ceiling.
			score = softCap(score)

			// Down-weight reference tiers (wiki) so curated context docs lead
			// for code-change queries; runbooks have their own dedicated tool.
			score *= kindWeight(doc.Kind)
			results = append(results, Recommendation{
				Document: doc,
				Name:     doc.Name,
				Kind:     doc.Kind,
				Title:    doc.Title,
				Score:    score,
				Signals:  signals,
				URI:      doc.URI(),
			})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Name < results[j].Name
	})

	if len(results) > maxResults {
		results = results[:maxResults]
	}

	return results
}

// softCapKnee is where scoring stops being linear. Below it a score is its own
// raw sum, which keeps the familiar magnitudes; above it the curve bends toward
// 1.0 without ever reaching it.
const softCapKnee = 0.8

// softCap maps a raw additive score into [0,1) while preserving order.
//
// A hard clamp at 1.0 collapsed every strongly-matching document onto the same
// value, so the ranking degenerated into the alphabetical name tiebreak exactly
// among the top results — the place discrimination matters most. An asymptotic
// curve keeps every distinct sum distinct: more signal always means a higher
// score, with diminishing returns instead of a cliff.
func softCap(score float64) float64 {
	if score <= softCapKnee {
		return score
	}
	return softCapKnee + (1-softCapKnee)*(1-math.Exp(-(score-softCapKnee)/(1-softCapKnee)))
}

// kindWeight scales a doc's score by its retrieval tier. Curated context kinds
// (domain/module/paradigm) score at full weight; code-extracted docs sit just
// below (they're authoritative but narrow); wiki reference docs are
// down-weighted so they augment rather than outrank code-adjacent context.
func kindWeight(k Kind) float64 {
	switch k {
	case KindWiki:
		return 0.85
	case KindCode:
		return 0.9
	}
	return 1.0
}

// tokenize splits a query into lowercase tokens, filtering noise words.
func tokenize(query string) []string {
	noise := map[string]bool{
		"the": true, "a": true, "an": true, "in": true, "on": true,
		"for": true, "of": true, "to": true, "and": true, "or": true,
		"is": true, "it": true, "this": true, "that": true, "with": true,
		"how": true, "what": true, "why": true, "where": true, "when": true,
		"investigate": true, "fix": true, "debug": true, "look": true, "check": true,
		"find": true, "show": true, "get": true,
	}

	parts := strings.Fields(strings.ToLower(query))
	var tokens []string
	for _, p := range parts {
		// Strip path separators for path-like queries
		p = strings.Trim(p, "/.")
		// Single characters are dropped, not because they're noise words but
		// because every signal here matches by substring: the "I" in "how do I
		// reindex" appears in nearly every document body, so it contributed a
		// body-match to the entire corpus and diluted real coverage ratios.
		if p == "" || len(p) < 2 || noise[p] {
			continue
		}
		tokens = append(tokens, p)
	}

	// If all tokens were noise, use the original parts
	if len(tokens) == 0 {
		for _, p := range parts {
			p = strings.Trim(p, "/.")
			if p != "" {
				tokens = append(tokens, p)
			}
		}
	}

	return tokens
}

// stemProbe trims a common inflectional suffix so a token can be used as a
// substring probe against unstemmed text: "declined" becomes "declin", which
// then matches "declines" and "declining" as well. Every signal here already
// matches by Contains, so shortening the needle is all stemming needs to be.
//
// It is deliberately timid. Only suffixes that change meaning rarely are cut,
// and only when at least minStemLength characters survive — trimming "gas" to
// "ga" would match far more than it should.
func stemProbe(tok string) string {
	const minStemLength = 4

	for _, suffix := range []string{"ing", "ed", "es", "s"} {
		if !strings.HasSuffix(tok, suffix) {
			continue
		}
		if stem := strings.TrimSuffix(tok, suffix); len(stem) >= minStemLength {
			return stem
		}
		break // a real suffix that's too short to trim — leave the token alone
	}
	return tok
}

func looksLikePath(query string) bool {
	return strings.Contains(query, "/") || strings.Contains(query, ".")
}

// scoreTagMatch checks how many query tokens match document tags.
func scoreTagMatch(tags []string, tokens []string) (float64, string) {
	if len(tags) == 0 || len(tokens) == 0 {
		return 0, ""
	}

	// An exact tag hit is worth more than a substring brush, so a doc tagged
	// exactly "index" outranks one tagged "indexing-pipeline" for that token.
	const partialCredit = 0.6

	credit := 0.0
	exact := 0
	for _, tok := range tokens {
		// Both sides are stemmed before comparison. Comparing raw forms in one
		// direction was asymmetric, and comparing them in both directions does
		// not actually help: "cache" is not a substring of "caching" either way
		// — they diverge at cach|e vs cach|i. Reducing both to "cach"/"cache"
		// first is what makes the pair match at all, in either order.
		probe := stemProbe(tok)
		best := 0.0
		for _, tag := range tags {
			tagLower := strings.ToLower(tag)
			if tagLower == tok {
				best = 1.0
				break
			}
			tagProbe := stemProbe(tagLower)
			if strings.Contains(tagProbe, probe) || strings.Contains(probe, tagProbe) {
				best = partialCredit
			}
		}
		if best == 1.0 {
			exact++
		}
		credit += best
	}

	if credit == 0 {
		return 0, ""
	}

	// Score on the share of the query the tags account for, weighted by how
	// exactly they account for it.
	coverage := credit / float64(len(tokens))
	weight := 0.5 * coverage
	if exact == len(tokens) {
		weight = 0.6 // every token landed on a tag exactly
	}

	return weight, "tag-match"
}

// Path matches are scored on how much of the query path the pattern actually
// pins down. A flat score made `services/billing/**` tie exactly with
// `services/billing/retry/**` for a file under the latter, leaving the
// alphabetical name tiebreak to pick the winner — a coin flip on the one signal
// path queries depend on entirely.
const (
	pathMatchBase  = 0.5 // a match that pins down almost nothing
	pathMatchRange = 0.3 // added in proportion to specificity, so 0.8 is exact
)

// scorePathMatch checks if the query path matches any related_paths glob
// patterns, scoring by the specificity of the *best*-matching pattern. Patterns
// are all considered rather than returning on the first hit — a doc listing
// both a broad and a narrow pattern should be credited for the narrow one.
func scorePathMatch(patterns []string, queryPath string) (float64, string) {
	if len(patterns) == 0 {
		return 0, ""
	}

	queryPath = filepath.Clean(queryPath)
	querySegments := len(strings.Split(queryPath, "/"))

	best := 0.0
	for _, pattern := range patterns {
		if !pathPatternMatches(pattern, queryPath) {
			continue
		}
		if s := pathMatchBase + pathMatchRange*pathSpecificity(pattern, querySegments); s > best {
			best = s
		}
	}

	if best == 0 {
		return 0, ""
	}
	return best, "path-match"
}

// pathPatternMatches reports whether a related_paths pattern covers a path,
// trying direct glob, directory-prefix, and ** forms in that order.
func pathPatternMatches(pattern, queryPath string) bool {
	if matched, _ := filepath.Match(pattern, queryPath); matched {
		return true
	}

	// Directory patterns: "a/b/**" and "a/b/*" both cover everything under a/b.
	cleanPattern := strings.TrimSuffix(pattern, "/**")
	cleanPattern = strings.TrimSuffix(cleanPattern, "/*")
	if cleanPattern != pattern && strings.HasPrefix(queryPath, cleanPattern) {
		return true
	}

	return strings.Contains(pattern, "**") && matchDoubleGlob(pattern, queryPath)
}

// pathSpecificity returns, in [0,1], the fraction of the query path's segments
// a pattern names literally. `services/billing/**` pins two of four segments in
// `services/billing/retry/backoff.go` (0.5); `services/billing/retry/**` pins
// three (0.75); a pattern with no wildcards at all pins everything (1.0).
func pathSpecificity(pattern string, querySegments int) float64 {
	if querySegments <= 0 {
		return 0
	}

	literal := 0
	for _, seg := range strings.Split(filepath.Clean(pattern), "/") {
		if strings.ContainsAny(seg, "*?[") {
			break // everything from the first wildcard on is unpinned
		}
		literal++
	}

	if literal > querySegments {
		literal = querySegments
	}
	return float64(literal) / float64(querySegments)
}

// matchDoubleGlob handles ** patterns like "backend/Handlers/PaymentGateway/**"
func matchDoubleGlob(pattern, path string) bool {
	// Split pattern on **
	parts := strings.SplitN(pattern, "**", 2)
	if len(parts) != 2 {
		return false
	}

	prefix := parts[0]
	suffix := parts[1]

	if prefix != "" && !strings.HasPrefix(path, prefix) {
		return false
	}

	if suffix != "" && suffix != "/" {
		remaining := strings.TrimPrefix(path, prefix)
		suffix = strings.TrimPrefix(suffix, "/")
		return strings.HasSuffix(remaining, suffix)
	}

	return true
}

// scoreNameMatch checks if query tokens match the document name or title.
func scoreNameMatch(name, title string, tokens []string) (float64, string) {
	nameLower := strings.ToLower(name)
	titleLower := strings.ToLower(title)

	matches := 0
	for _, tok := range tokens {
		probe := stemProbe(tok)
		if strings.Contains(nameLower, probe) || strings.Contains(titleLower, probe) {
			matches++
		}
	}

	if matches == 0 {
		return 0, ""
	}

	coverage := float64(matches) / float64(len(tokens))
	weight := 0.4 * coverage
	if matches == len(tokens) {
		weight = 0.5
	}
	// Exact name match is strongest
	for _, tok := range tokens {
		if tok == nameLower {
			weight = 0.6
			break
		}
	}

	return weight, "name-match"
}

// LearningRecommendation is a scored learning-log match. Learning matches sit
// below context doc matches in priority — they're a fallback for recent or
// emerging knowledge that hasn't been promoted yet.
type LearningRecommendation struct {
	Entry   *LearningEntry `json:"-"`
	Name    string         `json:"name"` // filename without .md
	Title   string         `json:"title"`
	Date    string         `json:"date"`
	Score   float64        `json:"score"`
	Signals []string       `json:"signals"`
	Path    string         `json:"path"`
}

// RecommendLearnings returns active learning-log entries relevant to a query.
// Promoted entries are excluded (their content has been absorbed into context
// docs). Max results defaults to 5.
func RecommendLearnings(entries []*LearningEntry, query string, maxResults int) []LearningRecommendation {
	if maxResults <= 0 {
		maxResults = 5
	}
	if query == "" || len(entries) == 0 {
		return nil
	}

	tokens := tokenize(query)
	isPath := looksLikePath(query)

	var results []LearningRecommendation
	for _, e := range entries {
		if e.Promoted {
			continue
		}

		var score float64
		var signals []string

		titleScore, titleSignal := scoreNameMatch(e.Title, e.Title, tokens)
		if titleScore > 0 {
			score += titleScore
			signals = append(signals, titleSignal)
		}

		if isPath {
			pathScore, pathSignal := scorePathMatch(e.RelatedPaths, query)
			if pathScore > 0 {
				score += pathScore
				signals = append(signals, pathSignal)
			}
		}

		bodyScore, bodySignal := scoreBodyMatch(e.Body, tokens)
		if bodyScore > 0 {
			score += bodyScore
			signals = append(signals, bodySignal)
		}

		if score <= 0 {
			continue
		}
		if score > 1.0 {
			score = 1.0
		}

		name := strings.TrimSuffix(filepath.Base(e.Path), ".md")
		date := ""
		if !e.Date.IsZero() {
			date = e.Date.Format("2006-01-02")
		}
		results = append(results, LearningRecommendation{
			Entry:   e,
			Name:    name,
			Title:   e.Title,
			Date:    date,
			Score:   score,
			Signals: signals,
			Path:    e.Path,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		// Tiebreak: newer first.
		if !results[i].Entry.Date.Equal(results[j].Entry.Date) {
			return results[i].Entry.Date.After(results[j].Entry.Date)
		}
		return results[i].Name < results[j].Name
	})

	if len(results) > maxResults {
		results = results[:maxResults]
	}
	return results
}

// scoreBodyMatch checks if query tokens appear in the document body.
func scoreBodyMatch(body string, tokens []string) (float64, string) {
	if body == "" || len(tokens) == 0 {
		return 0, ""
	}

	bodyLower := strings.ToLower(body)
	matches := 0
	for _, tok := range tokens {
		if strings.Contains(bodyLower, stemProbe(tok)) {
			matches++
		}
	}

	if matches == 0 {
		return 0, ""
	}

	coverage := float64(matches) / float64(len(tokens))
	weight := 0.2 * coverage
	if matches == len(tokens) {
		weight = 0.3 // all tokens found in body
	}

	return weight, "body-match"
}
