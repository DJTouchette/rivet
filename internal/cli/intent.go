package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/djtouchette/rivet/internal/config"
	rivetctx "github.com/djtouchette/rivet/internal/context"
	"github.com/spf13/cobra"
)

// loadIntentDocs loads .rivet/intent/ and links each domain-scoped doc to the
// curated domain doc it governs, so related_paths can be inherited. It also
// returns the curated docs, which callers need for name validation.
func loadIntentDocs() (intents, contexts []*rivetctx.Document, err error) {
	contexts, err = rivetctx.Load(".rivet/context")
	if err != nil {
		return nil, nil, err
	}
	intents, err = rivetctx.LoadIntent(".")
	if err != nil {
		return nil, nil, err
	}
	rivetctx.LinkIntentDomains(intents, contexts)
	return intents, contexts, nil
}

// intentScanOptions reads intent.exclude from config. A missing or unreadable
// config means only the built-in excludes apply.
func intentScanOptions() rivetctx.ScanOptions {
	cfg, err := config.LoadOrDefault("")
	if err != nil {
		return rivetctx.ScanOptions{}
	}
	return rivetctx.ScanOptions{Exclude: cfg.Intent.Exclude}
}

// lintIntent runs the per-doc rules (via Lint, which needs every doc name to
// validate domain: and [[links]]) and the coverage check, returning only the
// findings about intent.
func lintIntent(intents, contexts []*rivetctx.Document) rivetctx.IntentReport {
	all := append(append([]*rivetctx.Document{}, contexts...), intents...)
	var perDoc []rivetctx.LintWarning
	for _, w := range rivetctx.Lint(all, ".").Warnings {
		if w.Kind == rivetctx.KindIntent {
			perDoc = append(perDoc, w)
		}
	}
	rep := rivetctx.CheckIntentInTree(intents, ".", intentScanOptions())
	rep.Warnings = append(perDoc, rep.Warnings...)
	return rep
}

const noIntentMessage = "No intent documents found. Create one with 'rivet intent scaffold <domain>' — rules live in .rivet/intent/{domains,cross-cutting}/."

func newIntentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "intent",
		Short: "Business rules the code must keep (.rivet/intent/)",
		Long: `Intent documents are the prescriptive tier: the business rules the code is
judged against. Context docs describe the code — when they disagree with it,
the doc is out of date. Intent docs say what the business requires — when the
code disagrees, the code is the defect, or the rule changed, which a person
decides.

  .rivet/intent/domains/<domain>.md        rules for one domain
  .rivet/intent/cross-cutting/<topic>.md   rules that apply everywhere
  .rivet/intent/proposals/                 agent-filed changes, never loaded

Each rule is a bullet with a stable ID under ## Invariants (breaking it is a
defect) or ## Policies (a business decision that may change):

  ## Invariants
  - **BIL-001** An issued invoice is never edited; corrections are credit notes.
    why: tax law requires an immutable audit trail

Code and tests point at the rule they enforce with a marker comment in any
language: "// rivet:intent <ID>", e.g. with BIL-001 as the ID. 'rivet intent check' fails when an
invariant has no marker, a marker names an unknown or retired rule, or two
rules share an ID — run it in CI.`,
	}
	cmd.AddCommand(
		newIntentListCmd(),
		newIntentShowCmd(),
		newIntentCheckCmd(),
		newIntentForCmd(),
		newIntentAffectedCmd(),
		newIntentScaffoldCmd(),
		newIntentProposalsCmd(),
	)
	return cmd
}

func newIntentListCmd() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List intent documents and their rules",
		RunE: func(cmd *cobra.Command, args []string) error {
			intents, _, err := loadIntentDocs()
			if err != nil {
				return err
			}
			if jsonOutput {
				type row struct {
					Name   string          `json:"name"`
					Scope  string          `json:"scope"`
					Title  string          `json:"title"`
					Domain string          `json:"domain,omitempty"`
					Rules  []rivetctx.Rule `json:"rules"`
				}
				rows := []row{}
				for _, d := range intents {
					rows = append(rows, row{d.Name, string(d.Scope), d.Title, d.Domain, d.Rules})
				}
				return writeJSON(rows)
			}
			if len(intents) == 0 {
				fmt.Println(noIntentMessage)
				return nil
			}
			for i, d := range intents {
				if i > 0 {
					fmt.Println()
				}
				scope := string(d.Scope)
				if d.Domain != "" {
					scope += ", governs " + d.Domain
				}
				fmt.Printf("%s — %s (%s)\n", d.Name, d.Title, scope)
				for _, r := range d.Rules {
					fmt.Println("  " + rivetctx.FormatRuleLine(r))
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON")
	return cmd
}

func newIntentShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <doc | RULE-ID>",
		Short: "Show an intent document, or one rule with where it is enforced",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			intents, _, err := loadIntentDocs()
			if err != nil {
				return err
			}
			target := args[0]
			if rivetctx.IsRuleID(target) {
				r, doc := rivetctx.FindRule(intents, target)
				if r == nil {
					return fmt.Errorf("no intent doc defines rule %s; run 'rivet intent list'", target)
				}
				refs, err := rivetctx.ScanRuleRefs(".", intentScanOptions())
				if err != nil {
					return err
				}
				cov := coverageFor(rivetctx.CheckIntent(intents, refs), r.ID)
				fmt.Print(rivetctx.FormatRuleDetail(*r, doc, cov))
				return nil
			}
			name := target
			if !strings.HasPrefix(name, "intent/") {
				name = "intent/" + name
			}
			for _, d := range intents {
				if d.Name == name {
					fmt.Print(d.Body)
					return nil
				}
			}
			return fmt.Errorf("intent document %q not found; run 'rivet intent list'", name)
		},
	}
}

func coverageFor(rep rivetctx.IntentReport, id string) *rivetctx.RuleCoverage {
	for i := range rep.Rules {
		if rep.Rules[i].Rule.ID == id {
			return &rep.Rules[i]
		}
	}
	return nil
}

func newIntentCheckCmd() *cobra.Command {
	var jsonOutput, strict bool
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Check every rule is enforced in code and nothing points at a missing rule (CI gate)",
		Long: `Hold the intent docs against the rivet:intent markers in the code.

Errors (exit non-zero):
  unenforced-invariant    an invariant no code or test marks
  unknown-rule-reference  a marker names a rule no intent doc defines
  retired-rule-reference  a marker names a retired rule
  duplicate-rule-id       two rules share an ID
  rule-prefix-mismatch    a rule ID doesn't use its doc's prefix:
  no-rules / empty-rule   a doc or rule with nothing in it
  intent-scan-failed      the code could not be scanned, so coverage is unproven

Warnings (exit non-zero only with --strict):
  unenforced-policy, pending-enforcement, untested-invariant,
  missing-rationale, manual-without-reason, invalid-enforcement,
  rule-outside-section, missing-owner, missing-ratification,
  stale-ratification, missing-tags, missing-related-paths, unknown-domain

An invariant enforced outside the code can declare it with
"enforced: manual — <how>"; a known gap with "enforced: pending", which
downgrades it to a warning until it is fixed.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			intents, contexts, err := loadIntentDocs()
			if err != nil {
				return err
			}
			if len(intents) == 0 {
				if jsonOutput {
					return writeJSON(rivetctx.IntentReport{Rules: []rivetctx.RuleCoverage{}, Warnings: []rivetctx.LintWarning{}})
				}
				fmt.Println(noIntentMessage)
				return nil
			}
			rep := lintIntent(intents, contexts)
			failed := rep.HasErrors() || (strict && len(rep.Warnings) > 0)
			if failed {
				cmd.SilenceUsage = true
			}

			if jsonOutput {
				if rep.Warnings == nil {
					rep.Warnings = []rivetctx.LintWarning{}
				}
				if err := writeJSON(rep); err != nil {
					return err
				}
				if failed {
					return errIntentCheckFailed
				}
				return nil
			}

			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "RULE\tCLASS\tSTATUS\tCODE\tTESTS\tDOC")
			counts := map[string]int{}
			for _, c := range rep.Rules {
				counts[c.Status]++
				fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%s\n", c.Rule.ID, c.Rule.Class, c.Status, len(c.Code), len(c.Tests), c.Rule.Doc)
			}
			tw.Flush()

			var parts []string
			for _, s := range []string{rivetctx.CoverageEnforced, rivetctx.CoverageUntested, rivetctx.CoverageTestOnly,
				rivetctx.CoverageManual, rivetctx.CoveragePending, rivetctx.CoverageUnenforced, rivetctx.CoverageRetired} {
				if counts[s] > 0 {
					parts = append(parts, fmt.Sprintf("%d %s", counts[s], s))
				}
			}
			fmt.Printf("\n%d rule(s) in %d intent doc(s): %s\n", len(rep.Rules), len(intents), strings.Join(parts, ", "))

			if len(rep.Warnings) == 0 {
				fmt.Println("No issues found.")
				return nil
			}
			fmt.Printf("\n%d issue(s):\n\n", len(rep.Warnings))
			printLintWarnings(rep.Warnings)
			if failed {
				return errIntentCheckFailed
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON")
	cmd.Flags().BoolVar(&strict, "strict", false, "exit non-zero on warnings too, not just errors")
	return cmd
}

var errIntentCheckFailed = fmt.Errorf("intent check found issues")

func printLintWarnings(ws []rivetctx.LintWarning) {
	for _, w := range ws {
		severity := "WARN"
		if w.Severity == rivetctx.SeverityError {
			severity = "ERR "
		}
		fmt.Printf("  [%s] %s (%s): %s\n", severity, w.Document, w.Rule, w.Message)
	}
	fmt.Println()
}

func newIntentForCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "for <path>",
		Short: "Show the business rules that govern a file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			intents, _, err := loadIntentDocs()
			if err != nil {
				return err
			}
			refs, err := rivetctx.ScanRuleRefs(".", intentScanOptions())
			if err != nil {
				return err
			}
			fmt.Print(rivetctx.FormatRulesForPath(intents, cleanRelPath(args[0]), refs))
			return nil
		},
	}
}

// cleanRelPath normalises a user-supplied path to the slash-relative form
// markers and related_paths use.
func cleanRelPath(p string) string {
	if filepath.IsAbs(p) {
		if wd, err := os.Getwd(); err == nil {
			if rel, err := filepath.Rel(wd, p); err == nil {
				p = rel
			}
		}
	}
	return filepath.ToSlash(filepath.Clean(p))
}

func newIntentAffectedCmd() *cobra.Command {
	var (
		since      string
		staged     bool
		jsonOutput bool
	)
	cmd := &cobra.Command{
		Use:   "affected [paths...]",
		Short: "Show the business rules a change touches and the tests that verify them",
		Long: `List the rules a change touches: rules marked in changed files, rules whose
intent doc governs a changed file, markers added or removed, and edits to the
rules themselves. Prints the test files carrying markers for those rules —
run them alongside witness's selection.

With no arguments the change is the working tree (staged, unstaged and
untracked) against HEAD. --since compares against a ref's merge-base with HEAD,
like 'git diff main...'; --staged compares the index with HEAD. Paths name the
changed files directly, without comparing markers against a base.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			intents, _, err := loadIntentDocs()
			if err != nil {
				return err
			}
			spec := rivetctx.ChangeSpec{Since: since, Staged: staged}
			for _, a := range args {
				spec.Paths = append(spec.Paths, cleanRelPath(a))
			}
			if staged && (since != "" || len(spec.Paths) > 0) {
				return fmt.Errorf("--staged cannot be combined with --since or paths")
			}
			rep, err := rivetctx.Affected(".", intents, spec, intentScanOptions())
			if err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(rep)
			}
			fmt.Print(rivetctx.FormatAffected(rep))
			return nil
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "compare against this git ref (merge-base with HEAD)")
	cmd.Flags().BoolVar(&staged, "staged", false, "analyse staged changes only")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON")
	return cmd
}

func newIntentScaffoldCmd() *cobra.Command {
	var (
		crossCutting bool
		prefix       string
		force        bool
	)
	cmd := &cobra.Command{
		Use:   "scaffold <name>",
		Short: "Create an intent document skeleton for a domain or a cross-cutting topic",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSuffix(strings.TrimPrefix(args[0], "intent/"), ".md")
			if name == "" || strings.ContainsAny(name, `/\ `) {
				return fmt.Errorf("name must be a single path segment, e.g. billing")
			}
			if prefix == "" {
				prefix = defaultRulePrefix(name)
			}
			if !rivetctx.IsRuleID(prefix + "-1") {
				return fmt.Errorf("prefix %q must be uppercase letters/digits starting with a letter, e.g. BIL", prefix)
			}
			sub := "domains"
			if crossCutting {
				sub = "cross-cutting"
			}
			path := filepath.Join(rivetctx.IntentDir, sub, name+".md")
			if _, err := os.Stat(path); err == nil && !force {
				return fmt.Errorf("%s already exists (use --force to overwrite)", path)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return err
			}

			// A domain doc to inherit related_paths from is only worth
			// mentioning when one exists.
			hasDomainDoc := false
			if !crossCutting {
				if _, err := os.Stat(filepath.Join(".rivet", "context", "domains", name+".md")); err == nil {
					hasDomainDoc = true
				}
			}
			if err := os.WriteFile(path, []byte(intentTemplate(name, prefix, crossCutting, hasDomainDoc)), 0644); err != nil {
				return err
			}
			fmt.Printf("Created %s\n", path)
			fmt.Println("Fill in the rules, then mark the code that enforces each one with a rivet:intent comment and run 'rivet intent check'.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&crossCutting, "cross-cutting", false, "rules that apply across every domain")
	cmd.Flags().StringVar(&prefix, "prefix", "", "rule ID prefix (default: first three letters of the name)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing doc")
	return cmd
}

func defaultRulePrefix(name string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		if (r >= 'A' && r <= 'Z') || (b.Len() > 0 && r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
		if b.Len() == 3 {
			break
		}
	}
	if b.Len() == 0 {
		return "RULE"
	}
	return b.String()
}

func intentTemplate(name, prefix string, crossCutting, hasDomainDoc bool) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "tags: [%s]\n", name)
	b.WriteString("owner:\n")
	b.WriteString("last_ratified:\n")
	fmt.Fprintf(&b, "prefix: %s\n", prefix)
	switch {
	case crossCutting:
		b.WriteString("# related_paths: omit to apply everywhere, or list globs to narrow it\n")
	case hasDomainDoc:
		fmt.Fprintf(&b, "# related_paths: inherited from .rivet/context/domains/%s.md unless set here\n", name)
	default:
		b.WriteString("related_paths:\n  - \"src/" + name + "/**\"\n")
	}
	b.WriteString("---\n\n")
	title := strings.ToUpper(name[:1]) + name[1:]
	fmt.Fprintf(&b, "# %s — intent\n\n", title)
	b.WriteString("## Purpose\n\n")
	b.WriteString("<!-- What this is for in business terms, who depends on it, and what must never go wrong -->\n\n")
	b.WriteString("## Invariants\n\n")
	fmt.Fprintf(&b, "<!-- Rules that must always hold; breaking one is a defect. One bullet each: - **%s-001** statement, then an indented why: line -->\n\n", prefix)
	b.WriteString("## Policies\n\n")
	fmt.Fprintf(&b, "<!-- Business decisions that may change (limits, windows, thresholds). Same format: - **%s-100** statement -->\n\n", prefix)
	b.WriteString("## Non-goals\n\n")
	b.WriteString("<!-- What this deliberately does not do, so nobody fixes it -->\n\n")
	b.WriteString("## Open questions\n\n")
	b.WriteString("<!-- Undecided rules. Nothing here is enforced -->\n\n")
	b.WriteString("## Retired\n\n")
	b.WriteString("<!-- Rules that no longer apply. Keep them listed so their IDs are never reused -->\n")
	return b.String()
}

func newIntentProposalsCmd() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "proposals",
		Short: "List agent-filed rule changes awaiting a person",
		Long: `Agents never edit .rivet/intent/. When one thinks a rule is wrong, missing, or
obsolete it files a proposal with the rivet.intent-propose tool, which lands in
.rivet/intent/proposals/. Nothing there is loaded as intent. Review each one,
edit the intent doc yourself if you agree, and delete the proposal.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			props, err := rivetctx.ListIntentProposals(rivetctx.IntentDir)
			if err != nil {
				return err
			}
			if jsonOutput {
				if props == nil {
					props = []rivetctx.IntentProposal{}
				}
				return writeJSON(props)
			}
			if len(props) == 0 {
				fmt.Println("No intent proposals awaiting review.")
				return nil
			}
			for _, p := range props {
				rule := p.RuleID
				if rule == "" {
					rule = "(new)"
				}
				fmt.Printf("%s  %-7s %-8s %-20s %s\n", p.Date, p.Change, rule, p.Doc, p.Path)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON")
	return cmd
}

func writeJSON(v interface{}) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
