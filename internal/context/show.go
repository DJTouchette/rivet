package context

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// DefaultShowBudget is the most context-show emits in one response, in tokens.
// Claude Code rejects a tool result over ~25K tokens outright, and Codex keeps
// only the head and tail of output over ~10K — dropping the middle, which is
// exactly where a long doc's relevant section tends to be. 8K clears both.
const DefaultShowBudget = 8000

// minShowBudget keeps a tiny budget from producing pages too small to hold a
// header and any content at all.
const minShowBudget = 500

// showOverhead is the room reserved on every page for the header and footer
// lines, which name the doc, the page and the next command.
const showOverhead = 700

// ShowOptions selects what context-show returns.
type ShowOptions struct {
	Section string // heading (or heading path) to return instead of the whole doc
	Page    int    // 1-based page of an over-budget doc or section; 0 = first
	Budget  int    // tokens; 0 = DefaultShowBudget
	MCP     bool   // phrase "how to get the rest" as MCP tool calls, not CLI commands
}

// Show renders a document for context-show without ever exceeding the budget.
//
// A document within budget is returned whole, with its outgoing links,
// exactly as before. One over budget returns page 1: an outline of its
// sections with their sizes, then as many sections as fit, and explicit
// instructions for fetching the rest by section or by page. Returning an error
// or a silently truncated doc were the two failures this replaces — the first
// is what Claude Code did with a 76 KB doc, the second what Codex did.
func Show(doc *Document, all []*Document, o ShowOptions) (string, error) {
	budgetTokens := o.Budget
	if budgetTokens <= 0 {
		budgetTokens = DefaultShowBudget
	}
	if budgetTokens < minShowBudget {
		budgetTokens = minShowBudget
	}
	budget := budgetTokens * bytesPerToken

	body := normalizeNewlines(doc.Body)
	sections := SplitSections(body)

	if o.Section != "" {
		return showSection(doc, sections, o, budget)
	}

	full := body + FormatWikiLinks(doc, all)
	if len(full) <= budget {
		if o.Page > 1 {
			return "", fmt.Errorf("%s fits in one response; it has no page %d", doc.Name, o.Page)
		}
		return full, nil
	}

	units := make([]string, 0, len(sections)+1)
	for _, s := range sections {
		units = append(units, s.Text)
	}
	if links := FormatWikiLinks(doc, all); links != "" {
		units = append(units, links)
	}

	outline := renderOutline(sections, budget/4)
	firstCap := budget - showOverhead - len(outline)
	pages := paginate(units, firstCap, budget-showOverhead)

	page := o.Page
	if page <= 0 {
		page = 1
	}
	if page > len(pages) {
		return "", fmt.Errorf("%s has %d pages; there is no page %d", doc.Name, len(pages), page)
	}

	var b strings.Builder
	size := fmt.Sprintf("~%d tokens (%.1f KB)", len(full)/bytesPerToken, float64(len(full))/1024)
	if page == 1 {
		fmt.Fprintf(&b, "[rivet] %s is %s, over the %d-token show budget. This is page 1 of %d: the outline, then the opening sections.\n",
			doc.Name, size, budgetTokens, len(pages))
		fmt.Fprintf(&b, "Fetch one section: %s\n", ShowHint(o.MCP, doc.Name, "<heading>", 0))
		fmt.Fprintf(&b, "Or page through:   %s\n\n", ShowHint(o.MCP, doc.Name, "", 2))
		b.WriteString(outline)
		b.WriteString("\n---\n\n")
	} else {
		fmt.Fprintf(&b, "[rivet] %s (%s), page %d of %d.\n\n---\n\n", doc.Name, size, page, len(pages))
	}
	b.WriteString(pages[page-1])
	writePageFooter(&b, o.MCP, doc.Name, "", page, len(pages))
	return clampBytes(b.String(), budget), nil
}

// showSection returns one section (with everything nested under it), paged if
// it is itself over budget.
func showSection(doc *Document, sections []Section, o ShowOptions, budget int) (string, error) {
	idx, others := findSection(sections, o.Section)
	if idx < 0 {
		return "", fmt.Errorf("%s has no section %q. Sections:\n%s", doc.Name, o.Section, renderOutline(sections, budget/2))
	}
	span := sectionSpan(sections, idx)

	var note string
	if len(others) > 0 {
		note = fmt.Sprintf("[rivet] %q also matches: %s — pass the full heading path to pick one.\n\n",
			o.Section, strings.Join(others, "; "))
	}

	if len(span)+len(note) <= budget {
		if o.Page > 1 {
			return "", fmt.Errorf("section %q of %s fits in one response; it has no page %d", o.Section, doc.Name, o.Page)
		}
		return note + span, nil
	}

	pages := paginate(lineUnits(span), budget-showOverhead-len(note), budget-showOverhead)
	page := o.Page
	if page <= 0 {
		page = 1
	}
	if page > len(pages) {
		return "", fmt.Errorf("section %q of %s has %d pages; there is no page %d", o.Section, doc.Name, len(pages), page)
	}

	var b strings.Builder
	b.WriteString(note)
	fmt.Fprintf(&b, "[rivet] Section %q of %s is ~%d tokens, over the show budget — page %d of %d.\n\n",
		o.Section, doc.Name, len(span)/bytesPerToken, page, len(pages))
	b.WriteString(pages[page-1])
	writePageFooter(&b, o.MCP, doc.Name, o.Section, page, len(pages))
	return clampBytes(b.String(), budget), nil
}

func writePageFooter(b *strings.Builder, mcp bool, name, section string, page, total int) {
	if !strings.HasSuffix(b.String(), "\n") {
		b.WriteString("\n")
	}
	if page < total {
		fmt.Fprintf(b, "\n[rivet] End of page %d of %d. Next: %s\n", page, total, ShowHint(mcp, name, section, page+1))
	} else {
		fmt.Fprintf(b, "\n[rivet] End of page %d of %d (last).\n", page, total)
	}
}

// findSection resolves a requested heading. Matching is case-insensitive and
// tries, in order: the heading text, the full heading path ("A › B", "A > B"
// or "A / B"), then a unique substring of the heading. It returns the first
// match and the paths of any other exact matches, so a doc with two
// "Learnings" headings answers rather than refusing.
func findSection(sections []Section, want string) (int, []string) {
	norm := func(s string) string {
		s = strings.ToLower(strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(s), "#")))
		for _, sep := range []string{" > ", " / ", " » "} {
			s = strings.ReplaceAll(s, sep, " › ")
		}
		return s
	}
	w := norm(want)
	if w == "" {
		return -1, nil
	}

	match := func(pred func(Section) bool) (int, []string) {
		first := -1
		var others []string
		for i, s := range sections {
			if s.Level == 0 || !pred(s) {
				continue
			}
			if first < 0 {
				first = i
			} else {
				others = append(others, sectionLabel(s))
			}
		}
		return first, others
	}

	if i, others := match(func(s Section) bool { return norm(s.Heading) == w }); i >= 0 {
		return i, others
	}
	if i, others := match(func(s Section) bool { return norm(s.PathString()) == w }); i >= 0 {
		return i, others
	}
	if i, others := match(func(s Section) bool { return strings.Contains(norm(s.Heading), w) }); i >= 0 && len(others) == 0 {
		return i, nil
	}
	return -1, nil
}

func sectionLabel(s Section) string {
	if p := s.PathString(); p != "" {
		return p
	}
	return s.Heading
}

// renderOutline lists a document's headings with the size of each (nested
// sections included), indented by level. If the full outline would exceed
// limit bytes it falls back to shallower headings, then to a count of what was
// left out — the outline must never be what pushes a page over budget.
func renderOutline(sections []Section, limit int) string {
	sizes := make([]int, len(sections))
	for i := range sections {
		sizes[i] = len(sectionSpan(sections, i))
	}

	// A single level-1 heading is the document's title — the caller already
	// names the doc — so it is left out. A doc built from several level-1
	// headings uses them as its sections, and they are listed.
	titles := 0
	for _, s := range sections {
		if s.Level == 1 {
			titles++
		}
	}
	base := 2
	if titles > 1 {
		base = 1
	}

	render := func(maxLevel int) (string, int) {
		var b strings.Builder
		b.WriteString("Outline (≈ tokens per section, nested sections included):\n")
		omitted := 0
		for i, s := range sections {
			if s.Level < base {
				continue
			}
			if s.Level > maxLevel {
				omitted++
				continue
			}
			line := fmt.Sprintf("%s- %s (~%d)\n", strings.Repeat("  ", s.Level-base), s.Heading, sizes[i]/bytesPerToken)
			if b.Len()+len(line) > limit-80 {
				omitted++
				continue
			}
			b.WriteString(line)
		}
		return b.String(), omitted
	}

	for _, maxLevel := range []int{6, base + 1, base} {
		out, omitted := render(maxLevel)
		if omitted == 0 || maxLevel == base {
			if omitted > 0 {
				out += fmt.Sprintf("  … %d deeper or further headings not listed\n", omitted)
			}
			return out
		}
	}
	return ""
}

// paginate packs units into pages, page 1 holding up to firstCap bytes and
// every later page up to restCap. Units are kept whole where they fit; a unit
// larger than a page is split at line breaks, and a line larger than a page at
// a rune boundary, so no page ever exceeds its cap.
func paginate(units []string, firstCap, restCap int) []string {
	if restCap < 1 {
		restCap = 1
	}
	if firstCap < 1 {
		firstCap = 1
	}

	var pages []string
	var cur strings.Builder
	capFor := func() int {
		if len(pages) == 0 {
			return firstCap
		}
		return restCap
	}
	flush := func() {
		pages = append(pages, cur.String())
		cur.Reset()
	}
	add := func(piece string) {
		if cur.Len() > 0 && cur.Len()+len(piece) > capFor() {
			flush()
		}
		cur.WriteString(piece)
	}

	for _, u := range units {
		if len(u) <= capFor() || (len(u) <= restCap && cur.Len() > 0) {
			add(u)
			continue
		}
		// Too big to place whole: break it into lines, and lines into
		// rune-safe pieces, each no larger than a later page.
		for _, line := range lineUnits(u) {
			for len(line) > 0 {
				room := capFor() - cur.Len()
				if room <= 0 {
					flush()
					continue
				}
				if len(line) <= room {
					cur.WriteString(line)
					break
				}
				if cur.Len() > 0 && len(line) <= capFor() {
					flush()
					continue
				}
				piece := truncateUTF8(line, room)
				if piece == "" {
					if cur.Len() > 0 {
						flush()
						continue
					}
					_, n := utf8.DecodeRuneInString(line)
					piece = line[:n]
				}
				cur.WriteString(piece)
				line = line[len(piece):]
				flush()
			}
		}
	}
	if cur.Len() > 0 || len(pages) == 0 {
		flush()
	}
	return pages
}

// lineUnits splits text into lines, keeping each line's newline.
func lineUnits(s string) []string {
	return strings.SplitAfter(s, "\n")
}

// clampBytes is the last guarantee that a response honours its budget. The
// pagination above already sizes pages to fit; this only matters if a header
// grew past the space reserved for it.
func clampBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return truncateUTF8(s, n-4) + "\n…\n"
}
