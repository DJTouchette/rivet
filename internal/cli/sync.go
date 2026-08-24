package cli

import (
	"fmt"

	"github.com/djtouchette/rivet/internal/config"
	rivetctx "github.com/djtouchette/rivet/internal/context"
	"github.com/djtouchette/rivet/internal/provider"
	rivetsync "github.com/djtouchette/rivet/internal/sync"
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	var outputPath string
	var providerSpec string

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync Rivet state into the agent instruction file",
		Long: `Generate or update the Rivet-managed section in the agent instruction file:
CLAUDE.md for Claude Code, AGENTS.md for Codex.

Only content between <!-- rivet:start --> and <!-- rivet:end --> markers
is modified. Everything outside those markers is left untouched.

If the instruction file does not exist, it is created with the Rivet section.

--provider picks the harness. The default, auto, writes for whichever harness
this project already carries markers for, and falls back to claude when it
cannot tell. --output overrides the path and writes a single file.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			providers, err := provider.Resolve(providerSpec, ".")
			if err != nil {
				return err
			}

			cfg, err := config.LoadOrDefault("")
			if err != nil {
				return err
			}

			reg := buildRegistry(cfg)
			caps := reg.List()

			docs, err := rivetctx.Load(".rivet/context")
			if err != nil {
				fmt.Printf("warning: loading context: %v\n", err)
				docs = nil
			}

			// --output names one file, so it only makes sense for one
			// provider. Naming both would silently write the same path twice.
			if cmd.Flags().Changed("output") && len(providers) > 1 {
				return fmt.Errorf("--output writes a single file, so it cannot be combined with %d providers; pass --provider claude or --provider codex", len(providers))
			}

			for _, p := range providers {
				path := p.InstructionFile()
				if cmd.Flags().Changed("output") {
					path = outputPath
				}

				section := rivetsync.GenerateInstructions(p, caps, docs)
				if err := rivetsync.WriteInstructions(path, section); err != nil {
					return err
				}

				fmt.Printf("Synced Rivet section in %s\n", path)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&outputPath, "output", "CLAUDE.md", "path to the instruction file (overrides the provider's default)")
	cmd.Flags().StringVar(&providerSpec, "provider", "auto", providerFlagUsage)

	return cmd
}
