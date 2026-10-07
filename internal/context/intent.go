package context

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Intent documents are the prescriptive tier: what the business requires and
// why. Every other tier is descriptive — it explains the code, and when the code
// and a domain doc disagree, the doc is what's out of date. Intent inverts that.
// When the code and an intent rule disagree, the code is the defect, or the
// business changed its mind, which is a person's call and never an agent's.
//
// That difference in authority is why intent is its own Kind with its own
// directory rather than a section in the domain docs:
//
//   - Agents never write it. They can file a proposal under
//     .rivet/intent/proposals/, which is never loaded as intent.
//   - Every rule carries a stable ID (BIL-001) so code and tests can point at
//     the rule they enforce with a `rivet:intent <ID>` marker, and lint can
//     prove each rule is enforced somewhere.
//   - Retired rules stay listed, so an ID is never silently reused for a
//     different rule.

// Default on-disk locations, relative to the project root.
const (
	IntentDir = ".rivet/intent"
	// IntentProposalsSubdir holds agent-filed rule-change proposals awaiting a
	// person. Nothing under it is ever loaded as intent.
	IntentProposalsSubdir = "proposals"
)

// IntentScope says whether an intent doc governs one domain or cuts across
// all of them ("money is always integer minor units").
type IntentScope string

const (
	IntentScopeDomain       IntentScope = "domain"
	IntentScopeCrossCutting IntentScope = "cross-cutting"
)

// intentScopeDirs maps each subdirectory of .rivet/intent/ to its scope.
var intentScopeDirs = []struct {
	dir   string
	scope IntentScope
}{
	{"domains", IntentScopeDomain},
	{"cross-cutting", IntentScopeCrossCutting},
}

// RuleClass is the section a rule sits in, which decides how strictly it is
// held. Breaking a policy is a product decision; breaking an invariant is a
// defect.
type RuleClass string

const (
	RuleInvariant RuleClass = "invariant"
	RulePolicy    RuleClass = "policy"
	// RuleRetired rules no longer apply, but their IDs stay reserved: code
	// still pointing at one is an error, and the ID cannot be reused.
	RuleRetired RuleClass = "retired"
)

// Enforcement values a rule can declare with an `enforced:` line. The empty
// value is the default and means "enforced in code or tests" — the coverage
// check expects at least one `rivet:intent` reference.
const (
	EnforcementManual  = "manual"  // enforced outside the code (a review, an ops procedure); exempt from coverage
	EnforcementPending = "pending" // a known gap: coverage findings are downgraded to warnings
)

// Rule is one business rule parsed out of an intent document.
type Rule struct {
	ID        string    `json:"id"`
	Class     RuleClass `json:"class"`
	Statement string    `json:"statement"`
	Why       string    `json:"why,omitempty"`
	// Enforcement is "", EnforcementManual, EnforcementPending, or — when the
	// author wrote something unrecognised — the raw value, which lint flags.
	Enforcement     string `json:"enforcement,omitempty"`
	EnforcementNote string `json:"enforcement_note,omitempty"`
	Doc             string `json:"doc"`  // name of the intent doc defining it
	Path            string `json:"path"` // file the rule lives in
	Line            int    `json:"line"` // 1-based line of the rule's bullet in Path
	// EndLine is the last line of the rule's block (bullet plus continuation
	// lines), so the block can be replaced or moved when a proposal is applied.
	EndLine int `json:"-"`
	// OutsideSection is true when the rule was found outside an Invariants,
	// Policies, or Retired section, so its class is a guess.
	OutsideSection bool `json:"-"`
}

// Active reports whether the rule currently applies.
func (r Rule) Active() bool { return r.Class != RuleRetired }

// ruleIDPattern is the shape of a rule ID: an uppercase prefix, a dash, digits.
const ruleIDPattern = `[A-Z][A-Z0-9]*-[0-9]+`

var (
	ruleIDRe = regexp.MustCompile(`^` + ruleIDPattern + `$`)
	// ruleItemRe matches a rule bullet: "- **BIL-001** statement". The ID may be
	// struck through (~~**BIL-003**~~) in a Retired section, and may be
	// followed by a colon or dash before the statement.
	ruleItemRe = regexp.MustCompile(`^(\s*)[-*+]\s+(?:~~)?\*\*(` + ruleIDPattern + `)\*\*(?:~~)?\s*[:.\x{2014}\x{2013}-]?\s*(.*)$`)
	// ruleKeyRe matches a continuation key line: "why: ...", "enforced: ...".
	ruleKeyRe = regexp.MustCompile(`^(?:[-*+]\s+)?(why|enforced|enforcement)\s*:\s*(.*)$`)
)

// IsRuleID reports whether s has the shape of a rule ID.
func IsRuleID(s string) bool { return ruleIDRe.MatchString(s) }

// LoadIntent reads intent documents from .rivet/intent/domains/ and
// .rivet/intent/cross-cutting/ under projectRoot. Markdown directly in
// .rivet/intent/ is treated as domain scope. The proposals/ staging area is
// never loaded. A missing directory yields no docs and no error.
//
// Names are "intent/<file>" so an intent doc never collides with the curated
// domain doc it governs ("billing" vs "intent/billing").
func LoadIntent(projectRoot string) ([]*Document, error) {
	base := IntentDir
	if !filepath.IsAbs(base) {
		base = filepath.Join(projectRoot, IntentDir)
	}
	if info, err := os.Stat(base); os.IsNotExist(err) || (err == nil && !info.IsDir()) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("stat %s: %w", base, err)
	}

	var docs []*Document

	// Root-level files are domain-scoped; subdirectories are handled below.
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", base, err)
	}
	for _, e := range entries {
		if e.IsDir() || !isIntentFile(e.Name()) {
			continue
		}
		doc, err := readIntentDoc(filepath.Join(base, e.Name()), strings.TrimSuffix(e.Name(), ".md"), IntentScopeDomain)
		if err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}

	for _, sd := range intentScopeDirs {
		dir := filepath.Join(base, sd.dir)
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				// rivet:intent CTX-002
				if path != dir && (strings.HasPrefix(d.Name(), ".") || d.Name() == IntentProposalsSubdir || d.Name() == "archive") {
					return fs.SkipDir
				}
				return nil
			}
			if !isIntentFile(d.Name()) {
				return nil
			}
			rel, relErr := filepath.Rel(dir, path)
			if relErr != nil {
				rel = d.Name()
			}
			doc, err := readIntentDoc(path, strings.TrimSuffix(filepath.ToSlash(rel), ".md"), sd.scope)
			if err != nil {
				return err
			}
			docs = append(docs, doc)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walking %s: %w", dir, err)
		}
	}

	sortDocs(docs)
	return docs, nil
}

func isIntentFile(name string) bool {
	return strings.HasSuffix(name, ".md") && !strings.HasPrefix(name, ".") &&
		!strings.EqualFold(name, "README.md")
}

func readIntentDoc(path, base string, scope IntentScope) (*Document, error) {
	doc, err := readDoc(path, "intent/"+base, KindIntent)
	if err != nil {
		return nil, err
	}
	fm, _ := parseFrontmatter(doc.RawBody)
	doc.Scope = scope
	doc.Prefix = fm.prefix
	doc.Domain = fm.domain
	if fm.lastRatified != "" {
		if t, err := time.Parse("2006-01-02", fm.lastRatified); err == nil {
			doc.LastRatified = t
		}
	}
	if doc.Title == doc.Name {
		doc.Title = base
	}
	doc.Rules = ParseRules(doc.Name, path, doc.Body, bodyLineOffset(doc.RawBody, doc.Body))
	return doc, nil
}

// bodyLineOffset returns how many lines precede body within raw, so a line
// number counted in the body can be reported as a line in the file.
func bodyLineOffset(raw, body string) int {
	if body == "" {
		return 0
	}
	idx := strings.Index(raw, body)
	if idx < 0 {
		return 0
	}
	return strings.Count(raw[:idx], "\n")
}

// ParseRules extracts rules from an intent document body.
//
// A rule is a bullet whose first token is a bold rule ID:
//
//	## Invariants
//	- **BIL-001** An issued invoice is never edited.
//	  why: tax and audit trail
//	  enforced: manual — finance reviews the ledger monthly
//
// The H2 section it sits under decides its class: Invariants, Policies, or
// Retired. Indented lines after the bullet continue the rule; `why:` and
// `enforced:` lines are captured as fields, and a wrapped line extends
// whichever came last — the statement, or the most recent field. Content inside fenced code blocks and HTML comments is ignored,
// so a doc can show the format without defining a rule.
func ParseRules(docName, path, body string, lineOffset int) []Rule {
	var rules []Rule
	var cur *Rule
	curIndent := 0
	lastKey := "" // field a non-key continuation line extends; "" = the statement
	section := RuleClass("")
	sectionKnown := false
	inFence := false
	inComment := false

	flush := func() {
		if cur != nil {
			cur.Statement = strings.TrimSpace(cur.Statement)
			rules = append(rules, *cur)
			cur = nil
		}
	}

	for i, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			flush()
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		// HTML comments hide content from readers, so a rule inside one (a
		// commented-out example, a rule someone is drafting) is not a rule.
		if inComment {
			if strings.Contains(trimmed, "-->") {
				inComment = false
			}
			continue
		}
		if strings.HasPrefix(trimmed, "<!--") {
			flush()
			inComment = !strings.Contains(trimmed, "-->")
			continue
		}

		if strings.HasPrefix(trimmed, "#") {
			flush()
			level := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
			heading := strings.ToLower(strings.TrimSpace(strings.TrimLeft(trimmed, "#")))
			switch {
			case level == 1:
				section, sectionKnown = "", false
			case level == 2:
				section, sectionKnown = classifySection(heading)
			}
			// H3+ are subgroups within the current section.
			continue
		}

		if m := ruleItemRe.FindStringSubmatch(line); m != nil {
			flush()
			class := section
			if !sectionKnown {
				class = RuleInvariant // stricter guess; lint flags the placement
			}
			cur = &Rule{
				ID:             m[2],
				Class:          class,
				Statement:      strings.TrimSpace(m[3]),
				Doc:            docName,
				Path:           path,
				Line:           lineOffset + i + 1,
				EndLine:        lineOffset + i + 1,
				OutsideSection: !sectionKnown,
			}
			curIndent = len(m[1])
			lastKey = ""
			continue
		}

		if cur == nil {
			continue
		}
		if trimmed == "" {
			continue // blank lines don't end a rule; an unindented line does
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent <= curIndent {
			flush()
			continue
		}
		cur.EndLine = lineOffset + i + 1
		if km := ruleKeyRe.FindStringSubmatch(trimmed); km != nil {
			val := strings.TrimSpace(km[2])
			switch km[1] {
			case "why":
				cur.Why = joinProse(cur.Why, val)
				lastKey = "why"
			default: // enforced / enforcement
				cur.Enforcement, cur.EnforcementNote = parseEnforcement(val)
				lastKey = "enforced"
			}
			continue
		}
		// A wrapped line continues whatever came last: the statement until a
		// key appears, then that key's value.
		text := strings.TrimSpace(strings.TrimLeft(trimmed, "-*+ "))
		switch lastKey {
		case "why":
			cur.Why = joinProse(cur.Why, text)
		case "enforced":
			cur.EnforcementNote = joinProse(cur.EnforcementNote, text)
		default:
			cur.Statement = joinProse(cur.Statement, text)
		}
	}
	flush()
	return rules
}

func joinProse(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + " " + b
}

// classifySection maps an H2 heading to a rule class. ok is false for any other
// section (Purpose, Non-goals, Open questions, ...).
func classifySection(heading string) (RuleClass, bool) {
	switch {
	case strings.HasPrefix(heading, "invariant"):
		return RuleInvariant, true
	case strings.HasPrefix(heading, "polic"):
		return RulePolicy, true
	case strings.HasPrefix(heading, "retired"):
		return RuleRetired, true
	}
	return "", false
}

// parseEnforcement splits an `enforced:` value into a known mode and a note.
// "manual — finance reviews monthly" → ("manual", "finance reviews monthly").
// Values meaning "in code" (code, tests, automated) normalise to "".
func parseEnforcement(val string) (mode, note string) {
	lower := strings.ToLower(val)
	for _, m := range []string{EnforcementManual, EnforcementPending} {
		if strings.HasPrefix(lower, m) {
			return m, strings.TrimSpace(strings.TrimLeft(val[len(m):], " :-—–,;("))
		}
	}
	for _, m := range []string{"code", "tests", "test", "automated"} {
		if lower == m {
			return "", ""
		}
	}
	return val, ""
}

// LinkIntentDomains connects domain-scoped intent docs to the curated domain
// doc they govern: the `domain:` frontmatter, or by default the doc with the
// same base name. When the intent doc declares no related_paths of its own it
// inherits the domain doc's, so "which rules govern this file" works without
// maintaining the same globs twice. Docs are modified in place.
func LinkIntentDomains(intents, contexts []*Document) {
	domains := make(map[string]*Document)
	for _, d := range contexts {
		if d.Kind == KindDomain {
			domains[d.Name] = d
		}
	}
	for _, in := range intents {
		if in.Kind != KindIntent || in.Scope != IntentScopeDomain {
			continue
		}
		name := in.Domain
		if name == "" {
			name = strings.TrimPrefix(in.Name, "intent/")
		}
		dom, ok := domains[name]
		if !ok {
			continue
		}
		in.Domain = dom.Name
		if len(in.RelatedPaths) == 0 && len(dom.RelatedPaths) > 0 {
			in.RelatedPaths = append([]string(nil), dom.RelatedPaths...)
			in.InheritedPaths = true
		}
	}
}

// AllRules returns every rule across the given intent docs, in doc order.
func AllRules(intents []*Document) []Rule {
	var out []Rule
	for _, d := range intents {
		out = append(out, d.Rules...)
	}
	return out
}

// FindRule returns the first rule with the given ID and the doc defining it.
func FindRule(intents []*Document, id string) (*Rule, *Document) {
	for _, d := range intents {
		for i := range d.Rules {
			if d.Rules[i].ID == id {
				return &d.Rules[i], d
			}
		}
	}
	return nil, nil
}

// IntentsGoverning returns the intent docs whose related_paths cover relPath,
// plus every cross-cutting doc without related_paths (those apply everywhere).
func IntentsGoverning(intents []*Document, relPath string) []*Document {
	relPath = filepath.ToSlash(filepath.Clean(relPath))
	var out []*Document
	for _, d := range intents {
		if len(d.RelatedPaths) == 0 {
			if d.Scope == IntentScopeCrossCutting {
				out = append(out, d)
			}
			continue
		}
		for _, p := range d.RelatedPaths {
			if governsPath(p, relPath) {
				out = append(out, d)
				break
			}
		}
	}
	return out
}

// governsPath reports whether a related_paths glob covers path, matching
// segment by segment: "**" spans any number of directories, other segments
// use filepath.Match. Unlike the recommend scorer's prefix match, which only
// ranks, this decides which rules apply to a file, so "services/billing/**"
// must not cover services/billing-legacy/x.go. "dir/*" is treated like
// "dir/**", the convention context docs already use.
func governsPath(pattern, path string) bool {
	pattern = strings.TrimPrefix(filepath.ToSlash(pattern), "./")
	if strings.HasSuffix(pattern, "/*") {
		pattern = strings.TrimSuffix(pattern, "*") + "**"
	}
	return globSegments(strings.Split(pattern, "/"), strings.Split(path, "/"))
}

func globSegments(pat, path []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			if len(rest) == 0 {
				return true
			}
			for i := 0; i <= len(path); i++ {
				if globSegments(rest, path[i:]) {
					return true
				}
			}
			return false
		}
		if len(path) == 0 {
			return false
		}
		if ok, _ := filepath.Match(pat[0], path[0]); !ok {
			return false
		}
		pat, path = pat[1:], path[1:]
	}
	return len(path) == 0
}

// SortRules orders rules by ID, numerically within a prefix (BIL-2 < BIL-10).
func SortRules(rules []Rule) {
	sort.SliceStable(rules, func(i, j int) bool { return ruleIDLess(rules[i].ID, rules[j].ID) })
}

func ruleIDLess(a, b string) bool {
	ap, an := splitRuleID(a)
	bp, bn := splitRuleID(b)
	if ap != bp {
		return ap < bp
	}
	if an != bn {
		return an < bn
	}
	return a < b
}

func splitRuleID(id string) (string, int) {
	dash := strings.LastIndex(id, "-")
	if dash < 0 {
		return id, 0
	}
	n := 0
	for _, r := range id[dash+1:] {
		if r < '0' || r > '9' {
			return id, 0
		}
		n = n*10 + int(r-'0')
	}
	return id[:dash], n
}
