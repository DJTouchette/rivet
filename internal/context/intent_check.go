package context

import (
	"fmt"
	"strings"
	"time"
)

// StaleRatifyDays is how long an intent doc can go without a person
// re-confirming it before lint flags it. Longer than StaleReviewDays: business
// rules change less often than code, and re-ratifying is a deliberate act.
const StaleRatifyDays = 180

// Coverage statuses, from best to worst.
const (
	CoverageEnforced   = "enforced"   // referenced by code and by tests
	CoverageUntested   = "untested"   // referenced by code only
	CoverageTestOnly   = "test-only"  // referenced by tests only
	CoverageManual     = "manual"     // declared as enforced outside the code
	CoveragePending    = "pending"    // declared as a known gap
	CoverageUnenforced = "unenforced" // nothing points at it
	CoverageRetired    = "retired"
)

// RuleCoverage pairs a rule with the markers pointing at it.
type RuleCoverage struct {
	Rule   Rule      `json:"rule"`
	Status string    `json:"status"`
	Code   []RuleRef `json:"code"`
	Tests  []RuleRef `json:"tests"`
}

// IntentReport is the result of checking intent docs against the code.
type IntentReport struct {
	Rules    []RuleCoverage `json:"rules"`
	Unknown  []RuleRef      `json:"unknown_references"`
	Retired  []RuleRef      `json:"retired_references"`
	Warnings []LintWarning  `json:"warnings"`
}

// HasErrors reports whether any finding is error severity.
func (r IntentReport) HasErrors() bool {
	return LintResult{Warnings: r.Warnings}.HasErrors()
}

// coverageStatus classifies a rule from its declared enforcement and refs.
func coverageStatus(r Rule, code, tests int) string {
	switch {
	case !r.Active():
		return CoverageRetired
	case code > 0 && tests > 0:
		return CoverageEnforced
	case r.Enforcement == EnforcementManual:
		return CoverageManual
	case code > 0:
		return CoverageUntested
	case tests > 0:
		return CoverageTestOnly
	case r.Enforcement == EnforcementPending:
		return CoveragePending
	}
	return CoverageUnenforced
}

// CheckIntent holds the intent docs against the markers found in the code.
// It is pure: the caller scans (ScanRuleRefs) and passes the refs in.
//
// Error severity, because each means the rules and the code have come apart:
//
//	duplicate-rule-id       — two rules share an ID, so every marker is ambiguous
//	unenforced-invariant    — an invariant nothing in the code points at
//	unknown-rule-reference  — code points at a rule no intent doc defines
//	retired-rule-reference  — code still enforces a rule that was retired
//
// Warning severity:
//
//	unenforced-policy       — a policy nothing points at
//	pending-enforcement     — an invariant explicitly marked as a known gap
//	untested-invariant      — enforced in code, but no test says it holds
func CheckIntent(intents []*Document, refs []RuleRef) IntentReport {
	var rep IntentReport

	defined := make(map[string]Rule)
	definedIn := make(map[string][]string)
	for _, d := range intents {
		for _, r := range d.Rules {
			if _, ok := defined[r.ID]; !ok {
				defined[r.ID] = r
			}
			definedIn[r.ID] = append(definedIn[r.ID], fmt.Sprintf("%s:%d", r.Path, r.Line))
		}
	}

	byID := make(map[string][]RuleRef)
	for _, ref := range refs {
		byID[ref.ID] = append(byID[ref.ID], ref)
	}

	warn := func(doc string, path string, sev Severity, rule, msg string) {
		rep.Warnings = append(rep.Warnings, LintWarning{
			Document: doc, Kind: KindIntent, Path: path, Severity: sev, Rule: rule, Message: msg,
		})
	}

	reportedDup := make(map[string]bool)
	for _, d := range intents {
		for _, r := range d.Rules {
			// rivet:intent CTX-003
			if locs := definedIn[r.ID]; len(locs) > 1 && !reportedDup[r.ID] {
				reportedDup[r.ID] = true
				warn(d.Name, r.Path, SeverityError, "duplicate-rule-id",
					fmt.Sprintf("%s is defined %d times (%s) — every rivet:intent marker naming it is ambiguous; give one a new ID",
						r.ID, len(locs), strings.Join(locs, ", ")))
			}

			var code, tests []RuleRef
			for _, ref := range byID[r.ID] {
				if ref.Test {
					tests = append(tests, ref)
				} else {
					code = append(code, ref)
				}
			}
			status := coverageStatus(r, len(code), len(tests))
			rep.Rules = append(rep.Rules, RuleCoverage{Rule: r, Status: status, Code: code, Tests: tests})

			switch status {
			case CoverageUnenforced:
				if r.Class == RulePolicy {
					warn(d.Name, r.Path, SeverityWarning, "unenforced-policy",
						fmt.Sprintf("%s (policy) has no rivet:intent marker in code or tests — mark where it's enforced, or declare `enforced: manual — <how>`", r.ID))
				} else {
					warn(d.Name, r.Path, SeverityError, "unenforced-invariant",
						fmt.Sprintf("%s (invariant) has no rivet:intent marker in code or tests — mark the code that enforces it, declare `enforced: manual — <how>`, or `enforced: pending` to track it as a known gap", r.ID))
				}
			case CoveragePending:
				warn(d.Name, r.Path, SeverityWarning, "pending-enforcement",
					fmt.Sprintf("%s is marked enforced: pending — a known gap with nothing in the code enforcing it yet", r.ID))
			case CoverageUntested:
				if r.Class == RuleInvariant {
					warn(d.Name, r.Path, SeverityWarning, "untested-invariant",
						fmt.Sprintf("%s is enforced in code (%s) but no test is marked as verifying it — add `rivet:intent %s` to the test that proves it holds",
							r.ID, refLocations(code, 3), r.ID))
				}
			}
		}
	}

	for _, ref := range refs {
		r, ok := defined[ref.ID]
		loc := fmt.Sprintf("%s:%d", ref.File, ref.Line)
		switch {
		case !ok:
			rep.Unknown = append(rep.Unknown, ref)
			warn(loc, ref.File, SeverityError, "unknown-rule-reference",
				fmt.Sprintf("rivet:intent marker names %s, which no intent doc defines — fix the ID, or add the rule to .rivet/intent/", ref.ID))
		// rivet:intent CTX-003
		case !r.Active():
			rep.Retired = append(rep.Retired, ref)
			warn(loc, ref.File, SeverityError, "retired-rule-reference",
				fmt.Sprintf("rivet:intent marker names %s, which was retired in %s — remove the code enforcing it, or reinstate the rule", ref.ID, r.Doc))
		}
	}

	return rep
}

func refLocations(refs []RuleRef, max int) string {
	var locs []string
	for i, r := range refs {
		if i == max {
			locs = append(locs, fmt.Sprintf("+%d more", len(refs)-max))
			break
		}
		locs = append(locs, fmt.Sprintf("%s:%d", r.File, r.Line))
	}
	return strings.Join(locs, ", ")
}

// CheckIntentInTree scans root for markers and checks intents against them. A
// scan failure is returned as an error-severity finding rather than an empty
// result: coverage that could not be measured has not been shown, and passing
// CI on it would be a false green.
func CheckIntentInTree(intents []*Document, root string, opts ScanOptions) IntentReport {
	// rivet:intent FC-002
	refs, err := ScanRuleRefs(root, opts)
	if err != nil {
		return IntentReport{Warnings: []LintWarning{{
			Document: IntentDir, Kind: KindIntent, Path: IntentDir, Severity: SeverityError,
			Rule: "intent-scan-failed", Message: fmt.Sprintf("could not scan for rivet:intent markers, so rule coverage is unproven: %v", err),
		}}}
	}
	return CheckIntent(intents, refs)
}

// lintIntentDoc holds the per-document rules for an intent doc. The coverage
// rules need the code, so they live in CheckIntent.
func lintIntentDoc(doc *Document, knownNames map[string]int, add func(Severity, string, string)) {
	if strings.TrimSpace(doc.Owner) == "" {
		add(SeverityWarning, "missing-owner", "no owner in frontmatter — name who can ratify changes to these rules")
	}
	if doc.LastRatified.IsZero() {
		add(SeverityWarning, "missing-ratification", "no last_ratified date in frontmatter — add the date a person last confirmed these rules")
	} else if age := int(time.Since(doc.LastRatified).Hours() / 24); age > StaleRatifyDays {
		add(SeverityWarning, "stale-ratification",
			fmt.Sprintf("last ratified %d days ago (threshold: %d) — confirm the rules still hold and update last_ratified", age, StaleRatifyDays))
	}
	if len(doc.Tags) == 0 {
		add(SeverityWarning, "missing-tags", "no tags in frontmatter — add tags so context-recommend surfaces these rules")
	}
	if doc.Scope == IntentScopeDomain && len(doc.RelatedPaths) == 0 {
		add(SeverityWarning, "missing-related-paths",
			"no related_paths and no matching domain doc to inherit them from — without them rivet can't tell which files these rules govern")
	}
	if doc.Domain != "" && knownNames != nil && knownNames[doc.Domain] == 0 {
		add(SeverityWarning, "unknown-domain",
			fmt.Sprintf("domain: %q does not match any context doc", doc.Domain))
	}

	if len(doc.Rules) == 0 {
		add(SeverityError, "no-rules", "no rules found — write each rule as a bullet starting with a bold ID under ## Invariants or ## Policies, e.g. \"- **BIL-001** An issued invoice is never edited.\"")
		return
	}
	for _, r := range doc.Rules {
		at := fmt.Sprintf("%s (line %d)", r.ID, r.Line)
		if r.OutsideSection {
			add(SeverityWarning, "rule-outside-section",
				fmt.Sprintf("%s is not under an Invariants, Policies, or Retired heading, so it is held as an invariant — move it to say what it is", at))
		}
		if doc.Prefix != "" && !strings.HasPrefix(r.ID, doc.Prefix+"-") {
			add(SeverityError, "rule-prefix-mismatch",
				fmt.Sprintf("%s does not use this doc's prefix %q — rule IDs must be %s-<n>", at, doc.Prefix, doc.Prefix))
		}
		if !r.Active() {
			continue
		}
		if strings.TrimSpace(r.Statement) == "" {
			add(SeverityError, "empty-rule", fmt.Sprintf("%s has no statement", at))
		}
		if r.Class == RuleInvariant && strings.TrimSpace(r.Why) == "" {
			add(SeverityWarning, "missing-rationale",
				fmt.Sprintf("%s has no `why:` line — without the reason, nobody can tell whether a change to it is safe", at))
		}
		switch r.Enforcement {
		case "", EnforcementPending:
		case EnforcementManual:
			if r.EnforcementNote == "" {
				add(SeverityWarning, "manual-without-reason",
					fmt.Sprintf("%s is `enforced: manual` without saying how — write `enforced: manual — <who or what checks it>`", at))
			}
		default:
			add(SeverityWarning, "invalid-enforcement",
				fmt.Sprintf("%s has `enforced: %s` — use manual, pending, or leave it out to mean enforced in code", at, r.Enforcement))
		}
	}
}
