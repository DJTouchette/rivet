package context

import (
	"fmt"
	"strings"
)

// Text renderers shared by the CLI and the MCP server, so an agent and a person
// reading the same rule see the same words.

// IntentPreamble is said once wherever intent is shown to an agent. It is the
// whole point of the tier: these are requirements, and the agent does not get
// to decide they are wrong.
const IntentPreamble = "These are business rules, ratified by people — requirements the code must meet, not descriptions of what it does. " +
	"If a change would break one, stop and ask the user. To add, change or retire a rule, draft it with rivet.intent-propose for the user to approve; never edit .rivet/intent/ yourself."

// maxRulesPerDoc caps how many rules a summary lists per doc before pointing
// at rivet.intent for the rest.
const maxRulesPerDoc = 8

// FormatRuleLine renders a rule as one line: "BIL-001 [invariant] statement".
func FormatRuleLine(r Rule) string {
	return fmt.Sprintf("%s [%s] %s", r.ID, r.Class, oneLine(r.Statement, 160))
}

// FormatRuleDetail renders a rule in full, with its coverage when known.
func FormatRuleDetail(r Rule, doc *Document, cov *RuleCoverage) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s [%s] — %s\n", r.ID, r.Class, docLabel(doc))
	fmt.Fprintf(&b, "defined at: %s:%d\n\n", r.Path, r.Line)
	b.WriteString(r.Statement + "\n")
	if r.Why != "" {
		fmt.Fprintf(&b, "\nwhy: %s\n", r.Why)
	}
	switch r.Enforcement {
	case "":
	case EnforcementManual, EnforcementPending:
		note := ""
		if r.EnforcementNote != "" {
			note = " — " + r.EnforcementNote
		}
		fmt.Fprintf(&b, "enforced: %s%s\n", r.Enforcement, note)
	default:
		fmt.Fprintf(&b, "enforced: %s (unrecognised)\n", r.Enforcement)
	}
	if cov != nil {
		fmt.Fprintf(&b, "\ncoverage: %s\n", cov.Status)
		if len(cov.Code) > 0 {
			b.WriteString("enforced in:\n")
			for _, ref := range cov.Code {
				fmt.Fprintf(&b, "  %s:%d\n", ref.File, ref.Line)
			}
		}
		if len(cov.Tests) > 0 {
			b.WriteString("verified by:\n")
			for _, ref := range cov.Tests {
				fmt.Fprintf(&b, "  %s:%d\n", ref.File, ref.Line)
			}
		}
	}
	return b.String()
}

func docLabel(doc *Document) string {
	if doc == nil {
		return ""
	}
	if doc.Title != "" && doc.Title != doc.Name {
		return doc.Name + " (" + doc.Title + ")"
	}
	return doc.Name
}

// FormatIntentSummary renders a doc's active rules, one line each.
func FormatIntentSummary(doc *Document, indent string) string {
	var b strings.Builder
	n := 0
	for _, r := range doc.Rules {
		if !r.Active() {
			continue
		}
		if n == maxRulesPerDoc {
			fmt.Fprintf(&b, "%s…and %d more — rivet.intent {\"query\": %q}\n", indent, countActive(doc.Rules)-n, doc.Name)
			break
		}
		b.WriteString(indent + FormatRuleLine(r) + "\n")
		n++
	}
	return b.String()
}

func countActive(rules []Rule) int {
	n := 0
	for _, r := range rules {
		if r.Active() {
			n++
		}
	}
	return n
}

// FormatRulesForPath renders the rules that apply to one file: the intent docs
// governing it and the markers inside it.
func FormatRulesForPath(intents []*Document, relPath string, refs []RuleRef) string {
	governing := IntentsGoverning(intents, relPath)
	var inFile []RuleRef
	for _, r := range refs {
		if r.File == relPath {
			inFile = append(inFile, r)
		}
	}
	if len(governing) == 0 && len(inFile) == 0 {
		return fmt.Sprintf("No business rules govern %s: no intent doc's related_paths cover it and it carries no rivet:intent markers.\n", relPath)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Business rules for %s\n%s\n\n", relPath, IntentPreamble)
	for _, d := range governing {
		why := "related_paths cover this file"
		if len(d.RelatedPaths) == 0 {
			why = "cross-cutting, applies everywhere"
		}
		fmt.Fprintf(&b, "%s — %s\n", docLabel(d), why)
		b.WriteString(FormatIntentSummary(d, "  "))
		b.WriteString("\n")
	}
	if len(inFile) > 0 {
		b.WriteString("rivet:intent markers in this file:\n")
		for _, ref := range inFile {
			label := ref.ID
			if r, _ := FindRule(intents, ref.ID); r != nil {
				label = FormatRuleLine(*r)
			} else {
				label += " (UNKNOWN — no intent doc defines it)"
			}
			fmt.Fprintf(&b, "  line %d: %s\n", ref.Line, label)
		}
	}
	return b.String()
}

// FormatAffected renders an AffectedReport for a person or an agent.
func FormatAffected(rep AffectedReport) string {
	var b strings.Builder
	if len(rep.Rules) == 0 {
		fmt.Fprintf(&b, "This change (%d file(s)) touches no business rules.\n", len(rep.ChangedFiles))
		return b.String()
	}
	fmt.Fprintf(&b, "This change touches %d business rule(s).\n%s\n\n", len(rep.Rules), IntentPreamble)
	for _, a := range rep.Rules {
		b.WriteString(FormatRuleLine(a.Rule) + "\n")
		for _, reason := range a.Reasons {
			fmt.Fprintf(&b, "    - %s\n", reason)
		}
		if a.LostEnforcement {
			b.WriteString("    ! nothing enforces this rule any more — this change removed its last rivet:intent marker\n")
		}
		if len(a.Tests) > 0 {
			fmt.Fprintf(&b, "    tests: %s\n", strings.Join(a.Tests, ", "))
		} else if a.Rule.Enforcement == EnforcementManual {
			fmt.Fprintf(&b, "    enforced manually: %s — check it by hand\n", nonEmpty(a.Rule.EnforcementNote, "no procedure given"))
		} else if a.Rule.Active() {
			b.WriteString("    tests: none marked — nothing proves this rule still holds\n")
		}
	}
	if len(rep.RuleChanges) > 0 {
		b.WriteString("\nRule definitions changed:\n")
		for _, rc := range rep.RuleChanges {
			fmt.Fprintf(&b, "  %s %s (%s)\n", rc.ID, rc.Change, rc.Doc)
			if rc.Before != "" && rc.Change != "added" {
				fmt.Fprintf(&b, "    before: %s\n", oneLine(rc.Before, 200))
			}
			if rc.After != "" && rc.Change != "removed" {
				fmt.Fprintf(&b, "    after:  %s\n", oneLine(rc.After, 200))
			}
		}
		b.WriteString("  Code enforcing a changed rule must be updated in the same change.\n")
	}
	if len(rep.Tests) > 0 {
		b.WriteString("\nRun the tests that verify these rules:\n")
		for _, t := range rep.Tests {
			b.WriteString("  " + t + "\n")
		}
	}
	return b.String()
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > max {
		return string([]rune(s)[:max-1]) + "…"
	}
	return s
}

// RecommendIntent ranks intent docs for a query with the same scorer as every
// other tier. It is kept out of the general pool on purpose: a rule is not one
// more piece of background to weigh against a wiki page, so callers show it
// in its own section.
func RecommendIntent(intents []*Document, query string, maxResults int, opts ...Option) []Recommendation {
	return Recommend(intents, query, maxResults, opts...)
}

// FormatIntentRecommendations renders the "business rules that apply" section
// of a context recommendation, or "" when nothing matched.
func FormatIntentRecommendations(recs []Recommendation) string {
	if len(recs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Business rules (intent) that apply:\n")
	b.WriteString(IntentPreamble + "\n\n")
	for _, r := range recs {
		fmt.Fprintf(&b, "  %.2f  [intent] %s — %s\n", r.Score, r.Name, r.Title)
		if r.Document != nil {
			b.WriteString(FormatIntentSummary(r.Document, "        "))
		}
		fmt.Fprintf(&b, "        uri: %s\n\n", r.URI)
	}
	return b.String()
}

func nonEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
