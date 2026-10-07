package context

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Affected answers the review question "which business rules does this change
// touch, and which tests prove they still hold?" A rule is touched when the
// change:
//
//   - edits a file carrying a `rivet:intent` marker for it,
//   - edits a file the rule's intent doc governs (related_paths),
//   - removes or adds a marker for it, or
//   - edits the rule itself in .rivet/intent/.
//
// The tests that carry markers for touched rules are the rule-aware test
// selection: they are named by intent, not by import graph, so they catch the
// case where a change breaks a rule in a file no dependency analysis connects
// to the test.

// ChangeSpec says which change to analyse. With Paths set, those files are
// the change and there is no base to compare markers against. Otherwise the
// change is the working tree against Since (default HEAD; a branch name is
// resolved to its merge-base with HEAD, like `git diff main...`), or the index
// against HEAD when Staged is set.
type ChangeSpec struct {
	Since  string
	Staged bool
	Paths  []string
}

// AffectedRule is one rule touched by a change.
type AffectedRule struct {
	Rule            Rule     `json:"rule"`
	Reasons         []string `json:"reasons"`
	Code            []string `json:"code"`  // files enforcing it, after the change
	Tests           []string `json:"tests"` // test files verifying it, after the change
	LostEnforcement bool     `json:"lost_enforcement"`
}

// RuleChange is an edit to a rule's definition in an intent doc.
type RuleChange struct {
	ID     string `json:"id"`
	Doc    string `json:"doc"`
	Change string `json:"change"` // added | removed | modified | retired | reinstated
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

// AffectedReport is the result of Affected.
type AffectedReport struct {
	Base         string         `json:"base,omitempty"`
	ChangedFiles []string       `json:"changed_files"`
	Rules        []AffectedRule `json:"rules"`
	RuleChanges  []RuleChange   `json:"rule_changes"`
	Tests        []string       `json:"tests"`
	RemovedRefs  []RuleRef      `json:"removed_references"`
	AddedRefs    []RuleRef      `json:"added_references"`
}

// Affected computes the rules a change touches. intents is the current
// (post-change) set of intent docs.
func Affected(root string, intents []*Document, spec ChangeSpec, opts ScanOptions) (AffectedReport, error) {
	var rep AffectedReport

	var (
		changed  []string
		current  []RuleRef
		baseRefs []RuleRef
		baseRev  string
		err      error
	)

	switch {
	case len(spec.Paths) > 0:
		for _, p := range spec.Paths {
			changed = append(changed, filepath.ToSlash(filepath.Clean(p)))
		}
		if current, err = ScanRuleRefs(root, opts); err != nil {
			return rep, err
		}
	case spec.Staged:
		baseRev = "HEAD"
		if !revExists(root, baseRev) {
			baseRev = ""
		}
		if changed, err = gitLines(root, "diff", "--cached", "--name-only", "--no-renames", "-z"); err != nil {
			return rep, err
		}
		if current, err = ScanRuleRefsAt(root, "", true, opts); err != nil {
			return rep, err
		}
	default:
		since := spec.Since
		if since == "" {
			since = "HEAD"
		}
		if !revExists(root, since) {
			return rep, fmt.Errorf("unknown git revision %q", since)
		}
		baseRev = since
		if since != "HEAD" {
			if mb, err := gitOut(root, "merge-base", since, "HEAD"); err == nil && strings.TrimSpace(mb) != "" {
				baseRev = strings.TrimSpace(mb)
			}
		}
		if changed, err = gitLines(root, "diff", "--name-only", "--no-renames", "-z", baseRev); err != nil {
			return rep, err
		}
		untracked, err := gitLines(root, "ls-files", "-o", "--exclude-standard", "-z")
		if err != nil {
			return rep, err
		}
		changed = append(changed, untracked...)
		if current, err = ScanRuleRefs(root, opts); err != nil {
			return rep, err
		}
	}
	rep.Base = baseRev
	if baseRev != "" {
		if baseRefs, err = ScanRuleRefsAt(root, baseRev, false, opts); err != nil {
			return rep, err
		}
	}

	// Rivet's own state (context docs, learnings, caches) is not code; of
	// .rivet/ only the intent docs themselves are part of a rule change.
	kept := changed[:0]
	for _, f := range changed {
		if !strings.HasPrefix(f, ".rivet/") || isIntentDocPath(f) {
			kept = append(kept, f)
		}
	}
	changed = uniqueSorted(kept)
	rep.ChangedFiles = changed
	changedSet := make(map[string]bool, len(changed))
	for _, f := range changed {
		changedSet[f] = true
	}

	// Rule definitions changed by the diff.
	var baseIntentRules []Rule
	if baseRev != "" || spec.Staged {
		for _, f := range changed {
			if !isIntentDocPath(f) {
				continue
			}
			var before []Rule
			if baseRev != "" {
				before = rulesAtRev(root, baseRev, f)
			}
			var after []Rule
			if spec.Staged {
				after = rulesAtRev(root, "", f) // index
			} else {
				after = rulesInFile(root, f)
			}
			baseIntentRules = append(baseIntentRules, before...)
			rep.RuleChanges = append(rep.RuleChanges, diffRules(intentDocName(f), before, after)...)
		}
	}

	// Markers added and removed, matched by (rule, file) so a marker that only
	// moved lines within its file is not reported.
	if baseRev != "" {
		rep.RemovedRefs = refsMinus(baseRefs, current)
		rep.AddedRefs = refsMinus(current, baseRefs)
	}

	defs := make(map[string]Rule)
	for _, r := range baseIntentRules {
		defs[r.ID] = r
	}
	for _, r := range AllRules(intents) {
		defs[r.ID] = r // current definition wins
	}

	affected := make(map[string]*AffectedRule)
	touch := func(id, reason string) {
		r, ok := defs[id]
		if !ok {
			return // unknown IDs are CheckIntent's problem, not a touched rule
		}
		a := affected[id]
		if a == nil {
			a = &AffectedRule{Rule: r}
			affected[id] = a
		}
		for _, existing := range a.Reasons {
			if existing == reason {
				return
			}
		}
		a.Reasons = append(a.Reasons, reason)
	}

	for _, ref := range current {
		if changedSet[ref.File] {
			if ref.Test {
				touch(ref.ID, fmt.Sprintf("verified by changed test %s:%d", ref.File, ref.Line))
			} else {
				touch(ref.ID, fmt.Sprintf("enforced in changed file %s:%d", ref.File, ref.Line))
			}
		}
	}
	for _, ref := range rep.RemovedRefs {
		touch(ref.ID, fmt.Sprintf("rivet:intent marker removed from %s", ref.File))
	}
	for _, ref := range rep.AddedRefs {
		touch(ref.ID, fmt.Sprintf("rivet:intent marker added in %s", ref.File))
	}
	for _, rc := range rep.RuleChanges {
		touch(rc.ID, fmt.Sprintf("rule %s in this change", rc.Change))
	}
	for _, f := range changed {
		if isIntentDocPath(f) {
			continue
		}
		for _, d := range intents {
			// Cross-cutting docs without related_paths govern everything; listing
			// all of them for every change would bury the rules that matter.
			if len(d.RelatedPaths) == 0 {
				continue
			}
			if !docGoverns(d, f) {
				continue
			}
			for _, r := range d.Rules {
				if r.Active() {
					touch(r.ID, fmt.Sprintf("governs changed file %s (%s related_paths)", f, d.Name))
				}
			}
		}
	}

	testSet := make(map[string]bool)
	for id, a := range affected {
		code, tests := map[string]bool{}, map[string]bool{}
		for _, ref := range current {
			if ref.ID != id {
				continue
			}
			if ref.Test {
				tests[ref.File] = true
			} else {
				code[ref.File] = true
			}
		}
		a.Code = sortedKeys(code)
		a.Tests = sortedKeys(tests)
		for _, t := range a.Tests {
			testSet[t] = true
		}
		if a.Rule.Active() && len(code)+len(tests) == 0 {
			for _, ref := range rep.RemovedRefs {
				if ref.ID == id {
					a.LostEnforcement = true
					break
				}
			}
		}
		rep.Rules = append(rep.Rules, *a)
	}
	sort.Slice(rep.Rules, func(i, j int) bool { return ruleIDLess(rep.Rules[i].Rule.ID, rep.Rules[j].Rule.ID) })
	rep.Tests = sortedKeys(testSet)
	return rep, nil
}

func docGoverns(d *Document, file string) bool {
	for _, p := range d.RelatedPaths {
		if governsPath(p, file) {
			return true
		}
	}
	return false
}

// isIntentDocPath reports whether a changed file is a ratified intent doc
// (not a proposal).
func isIntentDocPath(f string) bool {
	if !strings.HasPrefix(f, IntentDir+"/") || !strings.HasSuffix(f, ".md") {
		return false
	}
	rest := strings.TrimPrefix(f, IntentDir+"/")
	return !strings.HasPrefix(rest, IntentProposalsSubdir+"/") && !strings.EqualFold(filepath.Base(f), "README.md")
}

// intentDocName maps .rivet/intent/domains/billing.md to intent/billing.
func intentDocName(f string) string {
	rest := strings.TrimSuffix(strings.TrimPrefix(f, IntentDir+"/"), ".md")
	for _, sd := range intentScopeDirs {
		if strings.HasPrefix(rest, sd.dir+"/") {
			return "intent/" + strings.TrimPrefix(rest, sd.dir+"/")
		}
	}
	return "intent/" + rest
}

func rulesFromRaw(name, path, raw string) []Rule {
	_, body := parseFrontmatter(raw)
	return ParseRules(name, path, body, bodyLineOffset(raw, body))
}

// rulesAtRev parses an intent doc as it was at rev ("" = the index). A file
// that did not exist there has no rules.
func rulesAtRev(root, rev, f string) []Rule {
	out, err := gitOut(root, "show", rev+":"+f)
	if err != nil {
		return nil
	}
	return rulesFromRaw(intentDocName(f), f, out)
}

func rulesInFile(root, f string) []Rule {
	data, err := os.ReadFile(filepath.Join(root, f))
	if err != nil {
		return nil
	}
	return rulesFromRaw(intentDocName(f), f, string(data))
}

func diffRules(doc string, before, after []Rule) []RuleChange {
	b := make(map[string]Rule)
	for _, r := range before {
		b[r.ID] = r
	}
	a := make(map[string]Rule)
	for _, r := range after {
		a[r.ID] = r
	}
	var out []RuleChange
	for id, ar := range a {
		br, existed := b[id]
		switch {
		case !existed:
			out = append(out, RuleChange{ID: id, Doc: doc, Change: "added", After: ar.Statement})
		case br.Active() && !ar.Active():
			out = append(out, RuleChange{ID: id, Doc: doc, Change: "retired", Before: br.Statement, After: ar.Statement})
		case !br.Active() && ar.Active():
			out = append(out, RuleChange{ID: id, Doc: doc, Change: "reinstated", Before: br.Statement, After: ar.Statement})
		case br.Statement != ar.Statement || br.Class != ar.Class || br.Why != ar.Why ||
			br.Enforcement != ar.Enforcement || br.EnforcementNote != ar.EnforcementNote:
			out = append(out, RuleChange{ID: id, Doc: doc, Change: "modified", Before: br.Statement, After: ar.Statement})
		}
	}
	for id, br := range b {
		if _, ok := a[id]; !ok {
			out = append(out, RuleChange{ID: id, Doc: doc, Change: "removed", Before: br.Statement})
		}
	}
	sort.Slice(out, func(i, j int) bool { return ruleIDLess(out[i].ID, out[j].ID) })
	return out
}

// refsMinus returns refs in a whose (ID, File) pair has more occurrences in a
// than in b.
func refsMinus(a, b []RuleRef) []RuleRef {
	count := make(map[[2]string]int)
	for _, r := range b {
		count[[2]string{r.ID, r.File}]++
	}
	var out []RuleRef
	for _, r := range a {
		k := [2]string{r.ID, r.File}
		if count[k] > 0 {
			count[k]--
			continue
		}
		out = append(out, r)
	}
	return out
}

func revExists(root, rev string) bool {
	_, err := gitOut(root, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	return err == nil
}

func gitOut(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// gitLines runs a git command with -z output and splits it.
func gitLines(root string, args ...string) ([]string, error) {
	out, err := gitOut(root, args...)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, l := range strings.Split(out, "\x00") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, filepath.ToSlash(l))
		}
	}
	return lines, nil
}

func uniqueSorted(in []string) []string {
	set := make(map[string]bool, len(in))
	for _, s := range in {
		set[s] = true
	}
	return sortedKeys(set)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
