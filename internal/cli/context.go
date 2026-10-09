package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/djtouchette/rivet/internal/config"
	rivetctx "github.com/djtouchette/rivet/internal/context"
	"github.com/djtouchette/rivet/internal/context/semantic"
	"github.com/djtouchette/rivet/internal/recon"
	"github.com/spf13/cobra"
)

// wikiPathsFromConfig returns the configured extra wiki roots, or nil if config
// is absent/unreadable (wiki then loads only the native .rivet/wiki/ tree).
func wikiPathsFromConfig() []string {
	cfg, err := config.LoadOrDefault("")
	if err != nil {
		return nil
	}
	return cfg.Context.WikiPaths
}

func newContextCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Manage project context documents",
		Long: `List and view structured context documents from .rivet/context/.

Context documents describe how parts of the codebase should be understood and changed.
They are organized into three categories:

  domains/   — business or system areas (billing, auth, scheduling)
  modules/   — narrower technical subsystems (patient-search, ledger-sync)
  paradigms/ — cross-cutting patterns (sql-views, event-handling, caching)

Business rules — what the code must do rather than what it does — live in
.rivet/intent/ and are managed with 'rivet intent'. They are listed, shown,
recommended and linted alongside context docs.`,
	}

	cmd.AddCommand(
		newContextListCmd(),
		newContextShowCmd(),
		newContextRecommendCmd(),
		newContextScaffoldCmd(),
		newContextLintCmd(),
		newContextIndexCmd(),
	)

	return cmd
}

func newContextIndexCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "index",
		Short: "Precompute context embeddings for semantic recommend",
		Long: `Embed every context document into a vector cache under ` + semantic.DefaultStoreDir + `.

The cache (manifest.json + vectors.bin) is deterministic and meant to be
committed to git, so semantic recommend works offline without re-embedding the
corpus on every run. Re-running only embeds new or changed chunks.

Configure the embedder with environment variables:

  RIVET_EMBED_BACKEND   onnx | ollama | openai   (unset = semantic disabled)
  RIVET_EMBED_MODEL     model name, or path to a local ONNX model directory
  RIVET_EMBED_BASE_URL  override API/daemon base URL
  RIVET_EMBED_API_KEY   bearer token for an HTTP API (never an MCP argument)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := semantic.ConfigFromEnv()
			if cfg.Backend == semantic.BackendNone {
				return fmt.Errorf("no embedder configured: set %s (onnx|ollama|openai)", semantic.EnvBackend)
			}
			emb, err := semantic.New(cfg)
			if err != nil {
				return err
			}

			docs, err := rivetctx.Load(".rivet/context")
			if err != nil {
				return err
			}
			// Index wiki + runbooks alongside context docs — the wiki is the
			// corpus that most benefits from semantic retrieval.
			wiki, err := rivetctx.LoadWiki(".", wikiPathsFromConfig())
			if err != nil {
				return err
			}
			runbooks, err := rivetctx.LoadRunbooks(".")
			if err != nil {
				return err
			}
			docs = append(append(docs, wiki...), runbooks...)
			if intents, err := rivetctx.LoadIntent("."); err == nil {
				docs = append(docs, intents...)
			}
			if len(docs) == 0 {
				fmt.Println("No context, wiki, or runbook documents to index.")
				return nil
			}

			store, err := semantic.OpenStore(semantic.DefaultStoreDir, emb.ID(), emb.Dim())
			if err != nil {
				return err
			}

			added, err := semantic.IndexDocs(cmd.Context(), emb, store, docs)
			if err != nil {
				return err
			}
			if err := store.Save(); err != nil {
				return err
			}
			fmt.Printf("Indexed %d documents (%d new/changed chunks, %d vectors total) into %s\n",
				len(docs), added, store.Len(), semantic.DefaultStoreDir)
			fmt.Println("Commit that directory so semantic recommend works offline.")
			return nil
		},
	}
}

func newContextListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all context documents",
		RunE: func(cmd *cobra.Command, args []string) error {
			docs, err := rivetctx.Load(".rivet/context")
			if err != nil {
				return err
			}
			if intents, err := rivetctx.LoadIntent("."); err == nil {
				docs = append(docs, intents...)
			}
			if len(docs) == 0 {
				fmt.Println("No context documents found.")
				fmt.Println("Add markdown files to .rivet/context/{domains,modules,paradigms}/")
				return nil
			}

			fmt.Printf("%-25s %-12s %s\n", "NAME", "KIND", "TITLE")
			for _, d := range docs {
				fmt.Printf("%-25s %-12s %s\n", d.Name, d.Kind, d.Title)
			}
			return nil
		},
	}
}

func newContextShowCmd() *cobra.Command {
	var (
		section string
		page    int
		budget  int
	)
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show a context document by name",
		Long: `Show a context document by name.

Output never exceeds --budget tokens (default ` + fmt.Sprint(rivetctx.DefaultShowBudget) + `, ~4 bytes each). A
document over budget prints page 1 — an outline of its sections with their
sizes, then the opening sections — and says how to get the rest:

  rivet context show <name> --section "<heading>"   one section, with its subsections
  rivet context show <name> --page 2                the next page

--section matches a heading case-insensitively, or a heading path such as
"Gotchas > Owner visibility". A section that is itself over budget is paged
the same way: --section "<heading>" --page 2.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Search every tier, not just curated context: context-recommend
			// surfaces wiki docs too, and a name it returned has to be
			// retrievable or following a recommendation dead-ends.
			docs, err := rivetctx.Load(".rivet/context")
			if err != nil {
				return err
			}
			if wiki, err := rivetctx.LoadWiki(".", nil); err == nil {
				docs = append(docs, wiki...)
			}
			if runbooks, err := rivetctx.LoadRunbooks("."); err == nil {
				docs = append(docs, runbooks...)
			}
			if intents, err := rivetctx.LoadIntent("."); err == nil {
				docs = append(docs, intents...)
			}

			name := args[0]
			for _, d := range docs {
				if d.Name == name {
					out, err := rivetctx.Show(d, docs, rivetctx.ShowOptions{
						Section: section,
						Page:    page,
						Budget:  budget,
					})
					if err != nil {
						return err
					}
					fmt.Print(out)
					return nil
				}
			}

			return fmt.Errorf("context document %q not found; run 'rivet context list' to see available documents", name)
		},
	}
	cmd.Flags().StringVar(&section, "section", "", "show only the section under this heading (or heading path)")
	cmd.Flags().IntVar(&page, "page", 0, "page of an over-budget document or section (1-based)")
	cmd.Flags().IntVar(&budget, "budget", rivetctx.DefaultShowBudget, "maximum output size in tokens (~4 bytes each)")
	return cmd
}

func newContextRecommendCmd() *cobra.Command {
	var (
		maxResults int
		jsonOutput bool
		budget     int
	)
	cmd := &cobra.Command{
		Use:   "recommend <query>",
		Short: "Recommend context documents for a task, file path, or keywords",
		Long: `Recommend context documents relevant to a query.

The query can be:
  - a natural language task: "investigate billing retries"
  - a file path: "backend/Handlers/PaymentGateway/src/App.cs"
  - keywords: "payment invoice retry"

Matching uses document tags, related_paths globs, titles, and body keywords.
Body text is scored section by section, so a short section that answers the
query outranks a long document that merely mentions every word somewhere.

Below the ranked list it quotes the passages that best match the query, each
labelled with its document and heading path, within --budget tokens (default
` + fmt.Sprint(rivetctx.DefaultExcerptBudget) + `; 0 lists names only). Often a passage is the answer. When it is
not, read the section it came from:

  rivet context show <name> --section "<heading>"

To add tags and related_paths, use frontmatter in context documents:

  ---
  tags: [billing, invoice, payment, retry]
  related_paths:
    - backend/Handlers/PaymentGateway/**
    - clients/web/src/pages/Invoices/**
  ---`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			docs, err := rivetctx.Load(".rivet/context")
			if err != nil {
				return err
			}

			// Include wiki reference docs (down-weighted by kind) in recommend.
			if wiki, err := rivetctx.LoadWiki(".", wikiPathsFromConfig()); err == nil {
				docs = append(docs, wiki...)
			}

			// Include docs that live in the code itself (rivet:context
			// comments and .context/ sidecars), also down-weighted.
			if codeDocs, err := rivetctx.LoadCodeDocs(recon.Run); err == nil {
				docs = append(docs, codeDocs...)
			}

			if len(docs) == 0 {
				fmt.Println("No context documents found.")
				fmt.Println("Add markdown files to .rivet/context/{domains,modules,paradigms}/,")
				fmt.Println("or annotate code with rivet:context comments / .context/ sidecar markdown.")
				return nil
			}

			query := strings.Join(args, " ")

			// Optional semantic signal (env-configured); lexical-only otherwise.
			var opts []rivetctx.Option
			var scorer *semantic.Scorer
			if s, err := semantic.OpenScorer(cmd.Context(), semantic.ConfigFromEnv(), semantic.DefaultStoreDir); err != nil {
				fmt.Fprintf(os.Stderr, "warning: semantic recommend disabled: %v\n", err)
			} else if s != nil {
				scorer = s
				opts = append(opts, rivetctx.WithSemantic(s))
			}
			recs := rivetctx.Recommend(docs, query, maxResults, append(opts, rivetctx.WithExcerpts(budget))...)
			warnSemanticFailure(scorer)

			learnings, _ := rivetctx.LoadLearnings(".rivet/learnings")
			learnRecs := rivetctx.RecommendLearnings(learnings, query, maxResults)

			// Business rules get their own section: they are requirements,
			// not one more document to weigh against the others.
			var intentRecs []rivetctx.Recommendation
			if intents, _, err := loadIntentDocs(); err == nil {
				intentRecs = rivetctx.RecommendIntent(intents, query, 3, opts...)
			}

			if jsonOutput {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if intentRecs == nil {
					intentRecs = []rivetctx.Recommendation{}
				}
				return enc.Encode(map[string]interface{}{
					"context":   recs,
					"intent":    intentRecs,
					"learnings": learnRecs,
				})
			}

			if len(recs) == 0 && len(learnRecs) == 0 && len(intentRecs) == 0 {
				fmt.Printf("No context documents or learning entries match %q\n", query)
				return nil
			}

			fmt.Print(rivetctx.FormatIntentRecommendations(intentRecs))

			fmt.Print(rivetctx.FormatRecommendations(query, recs, false))

			if len(learnRecs) > 0 {
				fmt.Printf("Related learning log entries (unverified):\n\n")
				for _, r := range learnRecs {
					date := r.Date
					if date == "" {
						date = "—"
					}
					fmt.Printf("  %.2f  [learning %s] %s\n", r.Score, date, r.Title)
					fmt.Printf("        signals: %s\n", strings.Join(r.Signals, ", "))
					fmt.Printf("        path: %s\n\n", r.Path)
				}
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&maxResults, "max", "n", 5, "max results")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON")
	cmd.Flags().IntVar(&budget, "budget", rivetctx.DefaultExcerptBudget, "token budget for passages quoted from the top docs (0 = names only)")
	return cmd
}

func newContextLintCmd() *cobra.Command {
	var jsonOutput bool
	var strict bool

	cmd := &cobra.Command{
		Use:   "lint",
		Short: "Validate context documents for quality and staleness",
		Long: `Check context documents for common issues.

Curated docs (domain/module/paradigm):
  missing-tags          — no tags in frontmatter
  missing-related-paths — no related_paths in frontmatter
  missing-owner         — nobody is responsible for keeping this accurate
  missing-review        — no last_reviewed date, so staleness can't be tracked
  stale-review          — last_reviewed is older than the threshold

Runbooks:
  missing-triggers      — no symptoms, so it can't be found when it's needed
  missing-owner         — no owning team
  untested-runbook      — no last_tested date
  stale-test            — last_tested is older than the threshold

Every document:
  placeholder-section   — unfilled <!-- ... --> template comments
  empty-body            — no content beyond headings (error)
  stale-related-path    — related_paths glob matches no files on disk
  stale-reference       — backtick-quoted paths in body don't exist on disk
  broken-wikilink       — [[link]] naming no known document
  self-wikilink         — [[link]] pointing at its own document
  duplicate-name        — two documents share a name (error)
  untagged-theme        — a subject the body dwells on that no tag covers,
                          so retrieval can't find the doc by it

Intent docs (.rivet/intent/) — see 'rivet intent check --help' for the full list:
  no-rules / empty-rule        — a doc or rule with nothing in it (error)
  duplicate-rule-id            — two rules share an ID (error)
  rule-prefix-mismatch         — a rule ID that doesn't use the doc's prefix (error)
  unenforced-invariant         — no rivet:intent marker in code or tests (error)
  unknown-rule-reference       — a marker naming an undefined rule (error)
  retired-rule-reference       — a marker naming a retired rule (error)
  untested-invariant, unenforced-policy, pending-enforcement,
  missing-rationale, missing-ratification, stale-ratification,
  open-proposal (an agent-drafted rule change awaiting a person), ...

Wiki docs are free-form and often imported, so only the universal rules apply.
Code-extracted docs are exempt from frontmatter rules — a rivet:context comment
has nowhere to put an owner.

Exits non-zero if any error-severity issue is found, or on any issue at all
with --strict.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			docs, err := rivetctx.Load(".rivet/context")
			if err != nil {
				return err
			}
			// Lint native wiki + runbooks too (not external wiki_paths — we
			// don't own those). Runbooks get their own kind-specific rules.
			if wiki, err := rivetctx.LoadWiki(".", nil); err == nil {
				docs = append(docs, wiki...)
			}
			if runbooks, err := rivetctx.LoadRunbooks("."); err == nil {
				docs = append(docs, runbooks...)
			}
			// Intent docs are linted here too, with their coverage against
			// the code, so the existing CI step enforces business rules
			// without a second command. A load failure is reported, not
			// skipped: silently linting nothing would be a false green.
			intents, err := rivetctx.LoadIntent(".")
			if err != nil {
				return fmt.Errorf("loading intent docs: %w", err)
			}
			rivetctx.LinkIntentDomains(intents, docs)
			docs = append(docs, intents...)
			if len(docs) == 0 {
				fmt.Println("No context, wiki, or runbook documents found.")
				return nil
			}

			result := rivetctx.Lint(docs, ".")
			if len(intents) > 0 {
				coverage := rivetctx.CheckIntentInTree(intents, ".", intentScanOptions())
				result.Warnings = append(result.Warnings, coverage.Warnings...)
				result.Warnings = append(result.Warnings, rivetctx.ProposalWarnings(rivetctx.IntentDir)...)
			}

			// A findings-based exit code is what makes this usable in CI. Usage
			// text is suppressed because every finding has already been printed
			// in detail; the root command suppresses cobra's duplicate error
			// line for every subcommand.
			// rivet:intent CTX-010
			failed := result.HasErrors() || (strict && len(result.Warnings) > 0)
			if failed {
				cmd.SilenceUsage = true
			}

			if jsonOutput {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(result); err != nil {
					return err
				}
				if failed {
					return errLintFailed
				}
				return nil
			}

			if len(result.Warnings) == 0 {
				fmt.Printf("Checked %d context docs — no issues found.\n", result.DocsRead)
				return nil
			}

			fmt.Printf("Checked %d context docs — %d issue(s):\n\n", result.DocsRead, len(result.Warnings))
			for _, w := range result.Warnings {
				severity := "WARN"
				if w.Severity == rivetctx.SeverityError {
					severity = "ERR "
				}
				fmt.Printf("  [%s] %s (%s): %s\n", severity, w.Document, w.Rule, w.Message)
			}
			fmt.Println()

			if failed {
				return errLintFailed
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON")
	cmd.Flags().BoolVar(&strict, "strict", false, "exit non-zero on warnings too, not just errors")
	return cmd
}

// errLintFailed signals a non-zero exit without adding a second error line to
// output that already listed every finding in detail.
var errLintFailed = errors.New("context lint found issues")
