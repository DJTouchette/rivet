package context

import "strings"

// Context docs cross-reference each other with `[[doc-name]]`, the same syntax
// Obsidian and most personal-wiki tools use. An optional alias is supported —
// `[[doc-name|how it reads in the sentence]]` — with the target always the part
// before the pipe.
//
// Links are how a reader (or an agent following a recommendation) walks from a
// domain doc to the module doc that explains a detail. That only works if the
// targets actually exist, which is why lint validates them: a typo'd link is
// otherwise invisible, since nothing renders these.

// WikiLink is one `[[target|alias]]` reference found in a document body.
type WikiLink struct {
	Target string // the doc name being linked to
	Alias  string // display text, empty when the link has no alias
}

// WikiLinks extracts every wiki link from a markdown body, in order of
// appearance and deduplicated by target.
//
// Content inside fenced code blocks and inline code spans is skipped. A doc
// explaining the link syntax itself — or showing `[[example]]` in a snippet —
// should not acquire a dependency on a doc named "example".
func WikiLinks(body string) []WikiLink {
	var links []WikiLink
	seen := make(map[string]bool)

	inFence := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)

		// ``` and ~~~ both open and close fences; anything between is literal.
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}

		for _, link := range parseWikiLinks(stripInlineCode(line)) {
			if seen[link.Target] {
				continue
			}
			seen[link.Target] = true
			links = append(links, link)
		}
	}

	return links
}

// WikiLinkTargets returns just the link targets, for callers that only need to
// resolve or validate them.
func WikiLinkTargets(body string) []string {
	links := WikiLinks(body)
	targets := make([]string, 0, len(links))
	for _, l := range links {
		targets = append(targets, l.Target)
	}
	return targets
}

// stripInlineCode blanks out `code span` content so links inside it are not
// picked up. Backtick runs are replaced rather than deleted so nothing on either
// side of a span accidentally joins into a link.
func stripInlineCode(line string) string {
	parts := strings.Split(line, "`")
	// Backtick content sits at odd indices: text`code`text`code`text.
	for i := 1; i < len(parts); i += 2 {
		parts[i] = strings.Repeat(" ", len(parts[i]))
	}
	return strings.Join(parts, " ")
}

// parseWikiLinks pulls [[target]] and [[target|alias]] out of a single line.
func parseWikiLinks(line string) []WikiLink {
	var links []WikiLink

	rest := line
	for {
		open := strings.Index(rest, "[[")
		if open < 0 {
			return links
		}
		rest = rest[open+2:]

		close := strings.Index(rest, "]]")
		if close < 0 {
			// An unterminated "[[" is not a link; there's nothing after it to scan.
			return links
		}

		inner := rest[:close]
		rest = rest[close+2:]

		// A nested "[[" means the opener we found belongs to a malformed
		// construct — skip it and let the scan continue from the inner one.
		if strings.Contains(inner, "[[") {
			continue
		}

		target, alias := splitLinkAlias(inner)
		if target == "" {
			continue
		}
		links = append(links, WikiLink{Target: target, Alias: alias})
	}
}

// splitLinkAlias separates "target|alias" into its parts, trimming both.
func splitLinkAlias(inner string) (target, alias string) {
	if pipe := strings.Index(inner, "|"); pipe >= 0 {
		return strings.TrimSpace(inner[:pipe]), strings.TrimSpace(inner[pipe+1:])
	}
	return strings.TrimSpace(inner), ""
}

// FormatWikiLinks renders a document's outgoing links as a trailing block, or
// "" when it has none. Links are invisible in plain markdown, so this is what
// makes them followable: a reader gets the names to look up next, and an agent
// gets the exact argument context-show takes.
//
// Broken links are shown rather than hidden. A reader who can see the target is
// missing knows not to go hunting for it.
func FormatWikiLinks(doc *Document, all []*Document) string {
	resolved, broken := ResolveWikiLinks(doc.Body, all)
	if len(resolved) == 0 && len(broken) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n---\nLinked documents:\n")
	for _, d := range resolved {
		title := d.Title
		if title == "" {
			title = d.Name
		}
		b.WriteString("  " + d.Name + " (" + string(d.Kind) + ") — " + title + "\n")
	}
	for _, target := range broken {
		b.WriteString("  " + target + " — MISSING, no such document\n")
	}
	return b.String()
}

// ResolveWikiLinks matches a body's links against a set of documents, returning
// the documents found and the targets that matched nothing.
//
// Lookup is by exact document name — the same key `context show` and
// `rivet.context-show` take — so a link resolves to whatever those commands
// would hand back.
func ResolveWikiLinks(body string, docs []*Document) (resolved []*Document, broken []string) {
	byName := make(map[string]*Document, len(docs))
	for _, d := range docs {
		// First writer wins, matching the lookup order callers use. Genuine
		// collisions are reported separately by lint's duplicate-name rule.
		if _, exists := byName[d.Name]; !exists {
			byName[d.Name] = d
		}
	}

	for _, target := range WikiLinkTargets(body) {
		if doc, ok := byName[target]; ok {
			resolved = append(resolved, doc)
			continue
		}
		broken = append(broken, target)
	}

	return resolved, broken
}
