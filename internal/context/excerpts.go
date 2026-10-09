package context

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

// bytesPerToken is the conversion used for every output budget. Four bytes per
// token is the usual estimate for English prose and markdown; it errs high for
// code-heavy text, which keeps a budget on the safe side of a client's limit.
const bytesPerToken = 4

// DefaultExcerptBudget is how many tokens of excerpt text context-recommend
// returns by default. Large enough for a handful of full bullets, small enough
// that the whole response stays well under the ~10K tokens some clients keep
// of a tool's output.
const DefaultExcerptBudget = 6000

// Excerpt selection limits. maxExcerptsPerDoc stops one sprawling doc from
// taking the whole budget; excerptRelativeFloor drops passages that matched
// only incidentally compared with the best one.
const (
	maxExcerptsPerDoc    = 3
	maxExcerpts          = 12
	excerptRelativeFloor = 0.4
)

// Excerpt is a passage of a recommended document that matched the query.
type Excerpt struct {
	Section   string  `json:"section"` // heading path, e.g. "Gotchas › Owner visibility"; "" for the preamble
	Heading   string  `json:"heading"` // the passage's own heading — the argument to show's --section
	Text      string  `json:"text"`
	Score     float64 `json:"score"`
	Truncated bool    `json:"truncated,omitempty"` // Text was cut to fit; show the section for the rest
}

// WithExcerpts makes Recommend attach the passages that best match the query
// to the documents it returns, within a total budget of budgetTokens. A budget
// of 0 or less leaves excerpts off.
//
// Names alone were not enough: measured across 81 benchmark runs, agents
// handed a list of names either never fetched the docs at all or fetched a
// 76 KB doc whole and lost the relevant middle to their client's truncation.
func WithExcerpts(budgetTokens int) Option {
	return func(o *recommendOpts) { o.excerptBudget = budgetTokens }
}

type excerptCandidate struct {
	rec     int // index into recs
	excerpt Excerpt
}

// attachExcerpts picks the best-matching passages across the recommended docs
// and attaches them, best first, until the budget is spent. Passages are scored
// with the same IDF-weighted body scorer as ranking, applied to the passage
// together with its heading path, so lexical relevance means the same thing in
// both places and the result is deterministic.
func attachExcerpts(recs []Recommendation, tokens []string, idf map[string]float64, budgetTokens int) {
	budget := budgetTokens * bytesPerToken
	if budget <= 0 || len(recs) == 0 || len(tokens) == 0 {
		return
	}

	type passageRef struct {
		rec  int
		p    Passage
		text string // lowercased heading path + passage, what is matched
	}
	var refs []passageRef
	for i, r := range recs {
		if r.Document == nil {
			continue
		}
		for _, p := range DocPassages(r.Document.Body) {
			refs = append(refs, passageRef{rec: i, p: p, text: strings.ToLower(p.Section.PathString() + "\n" + p.Text)})
		}
	}
	texts := make([]string, len(refs))
	for i, r := range refs {
		texts[i] = r.text
	}
	scores := bm25(texts, tokens)

	var cands []excerptCandidate
	for i, r := range refs {
		if scores[i] <= 0 {
			continue
		}
		cands = append(cands, excerptCandidate{rec: r.rec, excerpt: Excerpt{
			Section: r.p.Section.PathString(),
			Heading: r.p.Section.Heading,
			Text:    r.p.Text,
			Score:   scores[i],
		}})
	}
	if len(cands) == 0 {
		return
	}

	// Best first; ties go to the higher-ranked doc, then document order (the
	// stable sort keeps it), so the selection is deterministic.
	sort.SliceStable(cands, func(a, b int) bool {
		if cands[a].excerpt.Score != cands[b].excerpt.Score {
			return cands[a].excerpt.Score > cands[b].excerpt.Score
		}
		return cands[a].rec < cands[b].rec
	})

	floor := cands[0].excerpt.Score * excerptRelativeFloor
	// No single excerpt may take more than a third of the budget: three
	// partial answers serve an agent better than one complete wall of text.
	perExcerpt := budget / 3

	perDoc := map[int]int{}
	used, taken := 0, 0
	for _, c := range cands {
		if taken >= maxExcerpts || c.excerpt.Score < floor {
			break
		}
		if perDoc[c.rec] >= maxExcerptsPerDoc {
			continue
		}
		ex := c.excerpt
		if len(ex.Text) > perExcerpt {
			ex.Text = truncateUTF8(ex.Text, perExcerpt) + " …"
			ex.Truncated = true
		}
		cost := len(ex.Text) + len(ex.Section) + len(recs[c.rec].Name) + 16 // label overhead
		if used+cost > budget {
			continue // a smaller, lower-scoring passage may still fit
		}
		used += cost
		taken++
		perDoc[c.rec]++
		recs[c.rec].Excerpts = append(recs[c.rec].Excerpts, ex)
	}
}

// BM25 parameters, the textbook values. k1 saturates term frequency; b sets
// how strongly a passage's length is normalised against the average.
const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

// bm25 scores each (already lowercased) passage against the query tokens.
//
// Choosing a passage to quote is a different problem from ranking documents,
// and the document scorer gets it wrong in both directions. Its term
// frequency let a 26-row seed table that repeats "owner" in every row beat
// the one bullet stating the visibility rule; dropping frequency instead let
// any 2 KB bullet win by containing more of the query's words by chance. BM25
// is the standard answer to exactly that: frequency saturates, and is
// normalised by the passage's length relative to the average.
//
// Rarity is measured across the candidate passages themselves, not across
// documents. Within the top docs for a query nearly every document says
// "owner" somewhere, but few passages do, and it is passages being chosen.
func bm25(texts []string, tokens []string) []float64 {
	scores := make([]float64, len(texts))
	if len(texts) == 0 || len(tokens) == 0 {
		return scores
	}

	total := 0
	for _, t := range texts {
		total += len(t)
	}
	avg := float64(total) / float64(len(texts))
	if avg == 0 {
		return scores
	}

	n := float64(len(texts))
	for _, tok := range tokens {
		probe := stemProbe(tok)
		counts := make([]int, len(texts))
		df := 0
		for i, t := range texts {
			if c := strings.Count(t, probe); c > 0 {
				counts[i] = c
				df++
			}
		}
		if df == 0 {
			continue
		}
		idf := math.Log(1 + (n-float64(df)+0.5)/(float64(df)+0.5))
		for i, c := range counts {
			if c == 0 {
				continue
			}
			tf := float64(c)
			norm := bm25K1 * (1 - bm25B + bm25B*float64(len(texts[i]))/avg)
			scores[i] += idf * tf * (bm25K1 + 1) / (tf + norm)
		}
	}
	return scores
}

// truncateUTF8 cuts s to at most n bytes without splitting a rune, preferring
// to stop at the last line break or space in the final fifth of the cut.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	cut := s[:n]
	if i := strings.LastIndexAny(cut, "\n "); i > n*4/5 {
		cut = cut[:i]
	}
	return cut
}

// ShowHint renders how to fetch more of a document, in the calling surface's
// own syntax: a CLI command for the CLI, a tool call for MCP. section and page
// are optional (""/0).
func ShowHint(mcp bool, name, section string, page int) string {
	if mcp {
		args := fmt.Sprintf(`"name": %q`, name)
		if section != "" {
			args += fmt.Sprintf(`, "section": %q`, section)
		}
		if page > 0 {
			args += fmt.Sprintf(`, "page": %d`, page)
		}
		return "rivet.context-show {" + args + "}"
	}
	cmd := "rivet context show " + shellQuote(name)
	if section != "" {
		cmd += " --section " + shellQuote(section)
	}
	if page > 0 {
		cmd += fmt.Sprintf(" --page %d", page)
	}
	return cmd
}

// shellQuote double-quotes an argument when it needs it. Doc names are
// usually bare words; headings usually are not.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'`$&|;<>()*?[]{}!#~\\") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`").Replace(s) + `"`
}

// FormatRecommendations renders context recommendations — the ranked list,
// then the excerpts best first — for the CLI (mcp=false) or an MCP tool
// result (mcp=true). The two differ only in how they say "read more".
//
// Excerpts come last on purpose: clients that cut long tool output keep the
// head and the tail, and this keeps both the list and the best passage.
func FormatRecommendations(query string, recs []Recommendation, mcp bool) string {
	if len(recs) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Recommended context for %q:\n\n", query)
	for _, r := range recs {
		fmt.Fprintf(&b, "  %.2f  [%s] %s — %s\n", r.Score, r.Kind, r.Name, r.Title)
		fmt.Fprintf(&b, "        signals: %s\n", strings.Join(r.Signals, ", "))
		fmt.Fprintf(&b, "        uri: %s\n\n", r.URI)
	}

	type labelled struct {
		name string
		ex   Excerpt
	}
	var all []labelled
	for _, r := range recs {
		for _, ex := range r.Excerpts {
			all = append(all, labelled{r.Name, ex})
		}
	}
	if len(all) == 0 {
		return b.String()
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].ex.Score > all[j].ex.Score })

	b.WriteString("Most relevant passages (best first):\n\n")
	for _, l := range all {
		label := l.name
		if l.ex.Section != "" {
			label += " › " + l.ex.Section
		}
		fmt.Fprintf(&b, "── %s\n", label)
		b.WriteString(l.ex.Text)
		b.WriteString("\n")
		if l.ex.Truncated {
			fmt.Fprintf(&b, "   (cut short — full section: %s)\n", ShowHint(mcp, l.name, l.ex.Heading, 0))
		}
		b.WriteString("\n")
	}
	if mcp {
		b.WriteString(`Read a whole section with rivet.context-show {"name": "<name>", "section": "<heading>"}.` + "\n")
	} else {
		b.WriteString(`Read a whole section with: rivet context show <name> --section "<heading>"` + "\n")
	}
	return b.String()
}
