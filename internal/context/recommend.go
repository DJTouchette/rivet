package context

import (
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
			if score > 1.0 {
				score = 1.0
			}
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
		if p == "" || noise[p] {
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

func looksLikePath(query string) bool {
	return strings.Contains(query, "/") || strings.Contains(query, ".")
}

// scoreTagMatch checks how many query tokens match document tags.
func scoreTagMatch(tags []string, tokens []string) (float64, string) {
	if len(tags) == 0 || len(tokens) == 0 {
		return 0, ""
	}

	matches := 0
	for _, tok := range tokens {
		for _, tag := range tags {
			if strings.Contains(strings.ToLower(tag), tok) {
				matches++
				break
			}
		}
	}

	if matches == 0 {
		return 0, ""
	}

	// Score based on fraction of tokens matched
	coverage := float64(matches) / float64(len(tokens))
	weight := 0.5 * coverage
	if matches == len(tokens) {
		weight = 0.6 // all tokens matched tags
	}

	return weight, "tag-match"
}

// scorePathMatch checks if the query path matches any related_paths glob patterns.
func scorePathMatch(patterns []string, queryPath string) (float64, string) {
	if len(patterns) == 0 {
		return 0, ""
	}

	queryPath = filepath.Clean(queryPath)

	for _, pattern := range patterns {
		// Try direct glob match
		if matched, _ := filepath.Match(pattern, queryPath); matched {
			return 0.7, "path-match"
		}

		// Try prefix match for directory patterns
		cleanPattern := strings.TrimSuffix(pattern, "/**")
		cleanPattern = strings.TrimSuffix(cleanPattern, "/*")
		if cleanPattern != pattern && strings.HasPrefix(queryPath, cleanPattern) {
			return 0.7, "path-match"
		}

		// Try ** glob (manual matching)
		if strings.Contains(pattern, "**") {
			if matchDoubleGlob(pattern, queryPath) {
				return 0.7, "path-match"
			}
		}
	}

	return 0, ""
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
		if strings.Contains(nameLower, tok) || strings.Contains(titleLower, tok) {
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
	Name    string         `json:"name"`    // filename without .md
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
		if strings.Contains(bodyLower, tok) {
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
