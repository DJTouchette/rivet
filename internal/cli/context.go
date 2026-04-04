package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	rivetctx "github.com/djtouchette/rivet/internal/context"
	"github.com/spf13/cobra"
)

func newContextCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Manage project context documents",
		Long: `List and view structured context documents from .rivet/context/.

Context documents describe how parts of the codebase should be understood and changed.
They are organized into three categories:

  domains/   — business or system areas (billing, auth, scheduling)
  modules/   — narrower technical subsystems (patient-search, ledger-sync)
  paradigms/ — cross-cutting patterns (sql-views, event-handling, caching)`,
	}

	cmd.AddCommand(
		newContextListCmd(),
		newContextShowCmd(),
		newContextRecommendCmd(),
		newContextScaffoldCmd(),
		newContextLintCmd(),
	)

	return cmd
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
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show a context document by name",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			docs, err := rivetctx.Load(".rivet/context")
			if err != nil {
				return err
			}

			name := args[0]
			for _, d := range docs {
				if d.Name == name {
					fmt.Print(d.Body)
					return nil
				}
			}

			return fmt.Errorf("context document %q not found; run 'rivet context list' to see available documents", name)
		},
	}
}

func newContextRecommendCmd() *cobra.Command {
	var (
		maxResults int
		jsonOutput bool
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
			if len(docs) == 0 {
				fmt.Println("No context documents found.")
				fmt.Println("Add markdown files to .rivet/context/{domains,modules,paradigms}/")
				return nil
			}

			query := strings.Join(args, " ")
			recs := rivetctx.Recommend(docs, query, maxResults)

			if jsonOutput {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(recs)
			}

			if len(recs) == 0 {
				fmt.Printf("No context documents match %q\n", query)
				return nil
			}

			fmt.Printf("Recommended context for %q:\n\n", query)
			for _, r := range recs {
				fmt.Printf("  %.2f  [%s] %s — %s\n", r.Score, r.Kind, r.Name, r.Title)
				fmt.Printf("        signals: %s\n", strings.Join(r.Signals, ", "))
				fmt.Printf("        uri: %s\n\n", r.URI)
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&maxResults, "max", "n", 5, "max results")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON")
	return cmd
}

func newContextLintCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "lint",
		Short: "Validate context documents for quality and staleness",
		Long: `Check context documents for common issues:

  missing-tags          — no tags in frontmatter
  missing-related-paths — no related_paths in frontmatter
  placeholder-section   — unfilled <!-- ... --> template comments
  empty-body            — no content beyond headings
  stale-related-path    — related_paths glob matches no files on disk
  stale-reference       — backtick-quoted paths in body don't exist on disk`,
		RunE: func(cmd *cobra.Command, args []string) error {
			docs, err := rivetctx.Load(".rivet/context")
			if err != nil {
				return err
			}
			if len(docs) == 0 {
				fmt.Println("No context documents found.")
				return nil
			}

			result := rivetctx.Lint(docs, ".")

			if jsonOutput {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(result)
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
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON")
	return cmd
}
