package context

import (
	"sort"
	"strings"
)

// RunbookMatch is a scored runbook returned by RecommendRunbooks.
type RunbookMatch struct {
	Document *Document `json:"-"`
	Name     string    `json:"name"`
	Title    string    `json:"title"`
	Triggers []string  `json:"triggers"`
	Severity string    `json:"severity"`
	Score    float64   `json:"score"`
	Signals  []string  `json:"signals"`
	URI      string    `json:"uri"`
}

// RecommendRunbooks ranks runbooks against a symptom/operational query. Unlike
// the general recommender, it weights trigger matches most heavily — a runbook
// is meant to be found by the symptom that invokes it ("payments failing"
// → the payment-recovery runbook). Name, body, and (optionally) semantic
// similarity contribute secondarily. Results are sorted by score descending.
func RecommendRunbooks(runbooks []*Document, query string, maxResults int, opts ...Option) []RunbookMatch {
	if maxResults <= 0 {
		maxResults = 5
	}
	if query == "" || len(runbooks) == 0 {
		return nil
	}

	var o recommendOpts
	for _, opt := range opts {
		opt(&o)
	}
	tokens := tokenize(query)
	semOK := o.sem != nil && o.sem.Prepare(query)

	var matches []RunbookMatch
	for _, rb := range runbooks {
		var score float64
		var signals []string

		// Signal 1: Trigger match — the primary retrieval key for runbooks.
		if ts, sig := scoreTriggerMatch(rb.Triggers, tokens); ts > 0 {
			score += ts
			signals = append(signals, sig)
		}
		// Signal 2: Name/title match.
		if ns, sig := scoreNameMatch(rb.Name, rb.Title, tokens); ns > 0 {
			score += ns
			signals = append(signals, sig)
		}
		// Signal 3: Body keyword match.
		if bs, sig := scoreBodyMatch(rb.Body, tokens); bs > 0 {
			score += bs
			signals = append(signals, sig)
		}
		// Signal 4: Semantic similarity (additive; only when configured).
		if semOK {
			if sim, ok := o.sem.SimilarityFor(rb.Name, rb.EmbeddingText()); ok && sim > 0 {
				score += semanticWeight * sim
				signals = append(signals, "semantic-match")
			}
		}

		if score <= 0 {
			continue
		}
		if score > 1.0 {
			score = 1.0
		}
		matches = append(matches, RunbookMatch{
			Document: rb,
			Name:     rb.Name,
			Title:    rb.Title,
			Triggers: rb.Triggers,
			Severity: rb.Severity,
			Score:    score,
			Signals:  signals,
			URI:      rb.URI(),
		})
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].Name < matches[j].Name
	})
	if len(matches) > maxResults {
		matches = matches[:maxResults]
	}
	return matches
}

// scoreTriggerMatch measures how strongly a query matches a runbook's triggers.
// Triggers are multi-word symptom phrases; a token matching any trigger is a
// strong signal because triggers are the runbook's reason for existing.
func scoreTriggerMatch(triggers []string, tokens []string) (float64, string) {
	if len(triggers) == 0 || len(tokens) == 0 {
		return 0, ""
	}
	matches := 0
	for _, tok := range tokens {
		for _, tg := range triggers {
			if strings.Contains(strings.ToLower(tg), tok) {
				matches++
				break
			}
		}
	}
	if matches == 0 {
		return 0, ""
	}
	coverage := float64(matches) / float64(len(tokens))
	weight := 0.6 * coverage
	if matches == len(tokens) {
		weight = 0.7 // every query token hit a trigger
	}
	return weight, "trigger-match"
}
