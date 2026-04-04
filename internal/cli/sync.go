package cli

import (
	"fmt"

	"github.com/djtouchette/rivet/internal/config"
	rivetctx "github.com/djtouchette/rivet/internal/context"
	rivetsync "github.com/djtouchette/rivet/internal/sync"
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	var claudeMDPath string

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync Rivet state into CLAUDE.md",
		Long: `Generate or update the Rivet-managed section in CLAUDE.md.

Only content between <!-- rivet:start --> and <!-- rivet:end --> markers
is modified. Everything outside those markers is left untouched.

If CLAUDE.md does not exist, it is created with the Rivet section.`,
		RunE: func(cmd *cobra.Command, args []string) error {
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

			section := rivetsync.GenerateClaudeMD(caps, docs)

			if err := rivetsync.WriteClaudeMD(claudeMDPath, section); err != nil {
				return err
			}

			fmt.Printf("Synced Rivet section in %s\n", claudeMDPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&claudeMDPath, "output", "CLAUDE.md", "path to the CLAUDE.md file")

	return cmd
}
