package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	rivetctx "github.com/djtouchette/rivet/internal/context"
	"github.com/djtouchette/rivet/internal/context/semantic"
	"github.com/spf13/cobra"
)

func newRunbookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runbook",
		Short: "Find and manage operational runbooks",
		Long: `Runbooks are actionable, trigger-keyed procedures in .rivet/runbooks/.

Unlike context docs ("what must I know to change this code?") and the wiki
("how does this work?"), a runbook answers "what do I DO when X happens?" —
ordered steps, verification, and rollback, found by the symptom that invokes it.`,
	}
	cmd.AddCommand(newRunbookFindCmd(), newRunbookListCmd(), newRunbookDraftCmd(), newRunbookPromoteCmd())
	return cmd
}

func newRunbookDraftCmd() *cobra.Command {
	var (
		triggers     []string
		severity     string
		owner        string
		relatedPaths []string
		steps        string
		verification string
		rollback     string
	)
	cmd := &cobra.Command{
		Use:   "draft <title>",
		Short: "Draft a runbook for human review (writes to .rivet/runbooks/drafts/)",
		Long: `Create a draft runbook. Drafts are NOT retrievable until a human promotes
them with 'rivet runbook promote' — a wrong runbook followed under pressure is
worse than none, so drafting and trusting are deliberately separated.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(steps) == "" {
				return fmt.Errorf("--steps is required (the procedure)")
			}
			path, err := rivetctx.CreateRunbookDraft(rivetctx.RunbooksDir, rivetctx.NewRunbook{
				Title:        strings.Join(args, " "),
				Triggers:     triggers,
				Severity:     severity,
				Owner:        owner,
				RelatedPaths: relatedPaths,
				Steps:        steps,
				Verification: verification,
				Rollback:     rollback,
			})
			if err != nil {
				return err
			}
			fmt.Printf("Drafted %s\n", path)
			fmt.Println("Review it, then run: rivet runbook promote " + strings.TrimSuffix(filepath.Base(path), ".md"))
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&triggers, "trigger", nil, "symptom/alert that invokes this runbook (repeatable)")
	cmd.Flags().StringVar(&severity, "severity", "", "low | medium | high | critical")
	cmd.Flags().StringVar(&owner, "owner", "", "team responsible")
	cmd.Flags().StringArrayVar(&relatedPaths, "related-path", nil, "related source path glob (repeatable)")
	cmd.Flags().StringVar(&steps, "steps", "", "the procedure (markdown; required)")
	cmd.Flags().StringVar(&verification, "verification", "", "how to confirm it worked")
	cmd.Flags().StringVar(&rollback, "rollback", "", "how to roll back")
	return cmd
}

func newRunbookPromoteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "promote <draft-name>",
		Short: "Promote a reviewed draft into an active runbook",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSuffix(args[0], ".md")
			draftPath := filepath.Join(rivetctx.RunbooksDir, rivetctx.DraftsSubdir, name+".md")
			dest, err := rivetctx.PromoteRunbookDraft(draftPath)
			if err != nil {
				return err
			}
			fmt.Printf("Promoted to %s\n", dest)
			fmt.Println("Set a `last_tested:` date once you've verified the steps.")
			return nil
		},
	}
}

func newRunbookFindCmd() *cobra.Command {
	var (
		maxResults int
		jsonOutput bool
		full       bool
	)
	cmd := &cobra.Command{
		Use:   "find <symptom...>",
		Short: "Find the runbook for a symptom or situation",
		Long: `Find the runbook(s) whose triggers/content best match a symptom.

  rivet runbook find payments are failing
  rivet runbook find "webhook queue backing up" --full`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runbooks, err := rivetctx.LoadRunbooks(".")
			if err != nil {
				return err
			}
			if len(runbooks) == 0 {
				fmt.Println("No runbooks found. Add procedures to .rivet/runbooks/ with `triggers:` frontmatter.")
				return nil
			}

			var opts []rivetctx.Option
			var scorer *semantic.Scorer
			if s, err := semantic.OpenScorer(cmd.Context(), semantic.ConfigFromEnv(), semantic.DefaultStoreDir); err == nil && s != nil {
				scorer = s
				opts = append(opts, rivetctx.WithSemantic(s))
			}

			query := strings.Join(args, " ")
			matches := rivetctx.RecommendRunbooks(runbooks, query, maxResults, opts...)
			warnSemanticFailure(scorer)

			if jsonOutput {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(matches)
			}
			if len(matches) == 0 {
				fmt.Printf("No runbook matches %q. Try 'rivet runbook list'.\n", query)
				return nil
			}

			best := matches[0]
			fmt.Printf("Runbook for %q: %s (%.2f)\n", query, best.Title, best.Score)
			if best.Severity != "" {
				fmt.Printf("severity: %s\n", best.Severity)
			}
			if len(best.Triggers) > 0 {
				fmt.Printf("triggers: %s\n", strings.Join(best.Triggers, "; "))
			}
			fmt.Printf("path: %s\n", best.Document.Path)
			if full {
				fmt.Printf("\n%s\n", best.Document.Body)
			}
			if len(matches) > 1 {
				fmt.Println("\nOther candidates:")
				for _, m := range matches[1:] {
					fmt.Printf("  %.2f  %s — %s\n", m.Score, m.Name, m.Title)
				}
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&maxResults, "max", "n", 5, "max results")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON")
	cmd.Flags().BoolVar(&full, "full", false, "print the full procedure of the top match")
	return cmd
}

func newRunbookListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all runbooks and their triggers",
		RunE: func(cmd *cobra.Command, args []string) error {
			runbooks, err := rivetctx.LoadRunbooks(".")
			if err != nil {
				return err
			}
			if len(runbooks) == 0 {
				fmt.Println("No runbooks found. Add procedures to .rivet/runbooks/.")
				return nil
			}
			fmt.Printf("%-30s %s\n", "NAME", "TRIGGERS")
			for _, rb := range runbooks {
				fmt.Printf("%-30s %s\n", rb.Name, strings.Join(rb.Triggers, "; "))
			}
			return nil
		},
	}
}
