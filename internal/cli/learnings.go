package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	rivetctx "github.com/djtouchette/rivet/internal/context"
	"github.com/spf13/cobra"
)

const learningsDir = ".rivet/learnings"

func newLearningsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "learnings",
		Short: "Manage the project learning log (capture layer)",
		Long: `The learning log is a low-friction capture layer at .rivet/learnings/.

Each entry is a small markdown file with its own frontmatter. Entries are
captured during development and later reviewed and promoted into the curated
context docs at .rivet/context/.

Workflow:

  rivet learnings add "Retry split across systems" -o "Scheduler and adapter..."
  rivet learnings list
  rivet learnings show <name>
  rivet learnings promote <name> --to retry

Parallel-safe: one file per entry, so multiple authors never conflict on a
shared section.`,
	}

	cmd.AddCommand(
		newLearningsListCmd(),
		newLearningsShowCmd(),
		newLearningsAddCmd(),
		newLearningsPromoteCmd(),
	)
	return cmd
}

func newLearningsListCmd() *cobra.Command {
	var (
		includePromoted bool
		jsonOutput      bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List learning log entries",
		RunE: func(cmd *cobra.Command, args []string) error {
			entries, err := rivetctx.LoadLearnings(learningsDir)
			if err != nil {
				return err
			}
			if !includePromoted {
				entries = filterActive(entries)
			}
			if jsonOutput {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(entries)
			}
			if len(entries) == 0 {
				fmt.Println("No learning log entries.")
				fmt.Printf("Add entries to %s/ or via `rivet learnings add` / rivet.learn.\n", learningsDir)
				return nil
			}
			fmt.Printf("%-12s %-10s %-8s %s\n", "DATE", "STATUS", "CONF", "TITLE")
			for _, e := range entries {
				status := "active"
				if e.Promoted {
					status = "promoted"
				}
				date := "—"
				if !e.Date.IsZero() {
					date = e.Date.Format("2006-01-02")
				}
				conf := e.Confidence
				if conf == "" {
					conf = "—"
				}
				fmt.Printf("%-12s %-10s %-8s %s\n", date, status, conf, e.Title)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&includePromoted, "all", false, "include already-promoted entries")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON")
	return cmd
}

func newLearningsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Show a learning log entry by filename (with or without .md)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			entry, err := findLearning(args[0])
			if err != nil {
				return err
			}
			fmt.Print(entry.RawBody)
			return nil
		},
	}
}

func newLearningsAddCmd() *cobra.Command {
	var (
		observation    string
		impact         string
		recommendation string
		confidence     string
		author         string
		suggestedDoc   string
		relatedPaths   []string
	)
	cmd := &cobra.Command{
		Use:   "add <title>",
		Short: "Add a new learning log entry",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			title := strings.Join(args, " ")
			if observation == "" {
				return fmt.Errorf("--observation is required")
			}
			entry, err := rivetctx.CreateLearning(learningsDir, rivetctx.NewLearning{
				Title:          title,
				Author:         author,
				Confidence:     confidence,
				SuggestedDoc:   suggestedDoc,
				RelatedPaths:   relatedPaths,
				Observation:    observation,
				Impact:         impact,
				Recommendation: recommendation,
			})
			if err != nil {
				return err
			}
			fmt.Printf("Created %s\n", entry.Path)
			return nil
		},
	}
	cmd.Flags().StringVarP(&observation, "observation", "o", "", "what you found (required)")
	cmd.Flags().StringVarP(&impact, "impact", "i", "", "why it matters")
	cmd.Flags().StringVarP(&recommendation, "recommendation", "r", "", "what to do about it")
	cmd.Flags().StringVarP(&confidence, "confidence", "c", "", "low | medium | high")
	cmd.Flags().StringVarP(&author, "author", "a", "", "author name")
	cmd.Flags().StringVar(&suggestedDoc, "suggested-doc", "", "context doc this is a candidate to promote into")
	cmd.Flags().StringSliceVarP(&relatedPaths, "related", "p", nil, "related path glob (repeatable)")
	return cmd
}

func newLearningsPromoteCmd() *cobra.Command {
	var (
		docName string
		archive bool
	)
	cmd := &cobra.Command{
		Use:   "promote <name>",
		Short: "Mark a learning as promoted into a context doc",
		Long: `Mark a learning entry as promoted. This is bookkeeping only — it rewrites
the entry's frontmatter (promoted: true, promoted_to: <doc>). The actual
content merge into a context doc is done manually or via /rivet-promote-learnings.

Use --archive to also move the file into .rivet/learnings/archive/.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			entry, err := findLearning(args[0])
			if err != nil {
				return err
			}
			target := docName
			if target == "" {
				target = entry.SuggestedDoc
			}
			if target == "" {
				return fmt.Errorf("--to is required (or set suggested_doc in the entry's frontmatter)")
			}
			if err := rivetctx.MarkPromoted(entry.Path, target); err != nil {
				return err
			}
			fmt.Printf("Marked %s as promoted to %s\n", filepath.Base(entry.Path), target)
			if archive {
				dest, err := rivetctx.ArchiveLearning(entry.Path)
				if err != nil {
					return err
				}
				fmt.Printf("Archived to %s\n", dest)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&docName, "to", "", "context doc name this was promoted into")
	cmd.Flags().BoolVar(&archive, "archive", false, "move the entry to .rivet/learnings/archive/ after marking")
	return cmd
}

func findLearning(name string) (*rivetctx.LearningEntry, error) {
	entries, err := rivetctx.LoadLearnings(learningsDir)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSuffix(name, ".md")
	for _, e := range entries {
		base := strings.TrimSuffix(filepath.Base(e.Path), ".md")
		if base == name {
			return e, nil
		}
	}
	return nil, fmt.Errorf("learning %q not found; run `rivet learnings list` to see entries", name)
}

func filterActive(entries []*rivetctx.LearningEntry) []*rivetctx.LearningEntry {
	var out []*rivetctx.LearningEntry
	for _, e := range entries {
		if !e.Promoted {
			out = append(out, e)
		}
	}
	return out
}
