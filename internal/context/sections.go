package context

import (
	"strings"
)

// Section is one heading-delimited slice of a document body. Text runs from
// the heading line up to (not including) the next heading of any level, so the
// sections of a document tile it exactly: concatenating every Text in order
// reproduces the (newline-normalised) body.
//
// A body that opens with prose before its first heading yields a leading
// section with Level 0 and an empty Heading.
type Section struct {
	Heading string   // heading text without the leading #s; "" for the preamble
	Level   int      // 1-6 for a heading, 0 for the preamble
	Path    []string // headings from the outermost level-2 ancestor down to this one
	Text    string   // the heading line and everything under it, up to the next heading
}

// PathString renders the section's heading path for display, e.g.
// "Gotchas › Owner visibility". The level-1 title is left out: it names the
// document, which the caller already shows.
func (s Section) PathString() string {
	return strings.Join(s.Path, " › ")
}

// normalizeNewlines converts CRLF bodies to LF. Docs checked out on Windows
// arrive with CRLF, and a heading line that still ends in \r would never equal
// the heading an agent asks for.
func normalizeNewlines(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// headingLevel returns the ATX heading level of a line and its text, or 0 when
// the line is not a heading. "#tag" is not a heading; "# Title" is.
func headingLevel(line string) (int, string) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 { // four spaces of indent is a code block
		return 0, ""
	}
	n := 0
	for n < len(trimmed) && trimmed[n] == '#' {
		n++
	}
	if n == 0 || n > 6 {
		return 0, ""
	}
	rest := trimmed[n:]
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		return 0, ""
	}
	text := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(rest), "#"))
	return n, text
}

// isFence reports whether a line opens or closes a fenced code block. Headings
// inside a fence are code, not structure.
func isFence(line string) bool {
	t := strings.TrimLeft(line, " ")
	return strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")
}

// SplitSections splits a markdown body at every heading outside a fenced code
// block. It is the unit context-show pages by and context-recommend scores by,
// so a short section that answers a question exactly is not diluted by the
// forty other sections of the document it happens to live in.
func SplitSections(body string) []Section {
	body = normalizeNewlines(body)
	if body == "" {
		return nil
	}

	var sections []Section
	var stack []Section // open ancestors, for Path
	cur := Section{}
	var buf strings.Builder
	inFence := false

	flush := func() {
		cur.Text = buf.String()
		if cur.Text != "" {
			sections = append(sections, cur)
		}
		buf.Reset()
	}

	lines := strings.SplitAfter(body, "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		bare := strings.TrimRight(line, "\n")
		if isFence(bare) {
			inFence = !inFence
		}
		if !inFence {
			if level, text := headingLevel(bare); level > 0 {
				flush()
				for len(stack) > 0 && stack[len(stack)-1].Level >= level {
					stack = stack[:len(stack)-1]
				}
				var path []string
				for _, a := range stack {
					if a.Level > 1 {
						path = append(path, a.Heading)
					}
				}
				if level > 1 {
					path = append(path, text)
				}
				cur = Section{Heading: text, Level: level, Path: path}
				stack = append(stack, cur)
			}
		}
		buf.WriteString(line)
	}
	flush()
	return sections
}

// sectionSpan returns the text of sections[i] together with every subsection
// nested under it — what a reader means by "the Gotchas section".
func sectionSpan(sections []Section, i int) string {
	var b strings.Builder
	b.WriteString(sections[i].Text)
	level := sections[i].Level
	if level == 0 { // the preamble has no children
		return b.String()
	}
	for j := i + 1; j < len(sections) && sections[j].Level > level; j++ {
		b.WriteString(sections[j].Text)
	}
	return b.String()
}

// Passage is the smallest unit an excerpt is made of: one top-level bullet
// (with its continuation lines and nested bullets) or one paragraph, tagged
// with the section it came from. Context docs keep their knowledge in bullets
// — a "Learnings" section is often fifty of them — so a section is still far
// too coarse to quote back to an agent as "the relevant part".
type Passage struct {
	Section Section // the section the passage belongs to
	Text    string  // the passage's markdown, trimmed of surrounding blank lines
}

// isListItem reports whether a line starts a top-level list item: "- ", "* ",
// "+ " or "1. ", indented by at most one space. Deeper indents are nested items
// and stay with their parent.
func isListItem(line string) bool {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 1 {
		return false
	}
	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "+ ") {
		return true
	}
	i := 0
	for i < len(trimmed) && trimmed[i] >= '0' && trimmed[i] <= '9' {
		i++
	}
	return i > 0 && i < len(trimmed)-1 && (trimmed[i] == '.' || trimmed[i] == ')') && trimmed[i+1] == ' '
}

// SplitPassages breaks a section's body (everything after its heading line)
// into passages. A new passage starts at each top-level list item and at each
// unindented line following a blank line. Fenced code stays whole inside the
// passage that introduced it.
func SplitPassages(sec Section) []Passage {
	lines := strings.Split(sec.Text, "\n")
	if sec.Level > 0 && len(lines) > 0 {
		lines = lines[1:] // the heading itself is carried by sec, not repeated
	}

	var out []Passage
	var buf []string
	inFence := false
	afterBlank := false

	flush := func() {
		text := strings.TrimSpace(strings.Join(buf, "\n"))
		if text != "" {
			out = append(out, Passage{Section: sec, Text: text})
		}
		buf = buf[:0]
	}

	for _, line := range lines {
		if isFence(line) {
			if !inFence && afterBlank {
				flush()
			}
			inFence = !inFence
			buf = append(buf, line)
			afterBlank = false
			continue
		}
		if inFence {
			buf = append(buf, line)
			continue
		}
		if strings.TrimSpace(line) == "" {
			afterBlank = true
			buf = append(buf, line)
			continue
		}
		indented := strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t")
		if isListItem(line) || (afterBlank && !indented) {
			flush()
		}
		buf = append(buf, line)
		afterBlank = false
	}
	flush()
	return out
}

// DocPassages returns every passage in a document body, in order.
func DocPassages(body string) []Passage {
	var out []Passage
	for _, sec := range SplitSections(body) {
		out = append(out, SplitPassages(sec)...)
	}
	return out
}
