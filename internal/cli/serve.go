package cli

import (
	"fmt"
	"log"
	"os"

	"github.com/djtouchette/rivet/internal/capabilities"
	"github.com/djtouchette/rivet/internal/config"
	rivetctx "github.com/djtouchette/rivet/internal/context"
	"github.com/djtouchette/rivet/internal/context/semantic"
	"github.com/djtouchette/rivet/internal/mcp"
	"github.com/djtouchette/rivet/internal/pins"
	"github.com/djtouchette/rivet/internal/rally"
	"github.com/djtouchette/rivet/internal/recon"
	"github.com/djtouchette/rivet/internal/schema"
	"github.com/djtouchette/rivet/internal/vaulty"
	"github.com/djtouchette/rivet/internal/witness"
	"github.com/spf13/cobra"
)

func newServeCmd(version string) *cobra.Command {
	var debug bool

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the MCP server for Claude Code",
		Long: `Start the Rivet MCP server, exposing registered capabilities as MCP tools
and context documents as MCP resources over JSON-RPC 2.0 stdio.

Configure Claude Code to use this server by adding to your MCP settings:

  {
    "mcpServers": {
      "rivet": {
        "command": "rivet",
        "args": ["serve"]
      }
    }
  }`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadOrDefault("")
			if err != nil {
				return err
			}

			reg := buildRegistry(cfg)

			exec := capabilities.NewExecutor(reg)
			exec.RegisterInProcess("vaulty", vaulty.Run)
			exec.RegisterInProcess("recon", recon.Run)
			exec.RegisterInProcess("witness", witness.Run)
			exec.RegisterInProcess("schema", schema.Run)

			contexts, err := rivetctx.Load(".rivet/context")
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: loading context: %v\n", err)
				contexts = nil
			}

			pinReg := pins.NewRegistry()
			pinReg.Add(rally.NewPinProvider())

			policies := buildPolicies(cfg)
			srv := mcp.NewServer(reg, exec, contexts, pinReg, policies, version, cfg.Context.ShouldAutoCompact())

			// Optional embedding-based recommend signal. Disabled unless
			// RIVET_EMBED_BACKEND is set; failures degrade to lexical-only.
			if scorer, err := semantic.OpenScorer(cmd.Context(), semantic.ConfigFromEnv(), semantic.DefaultStoreDir); err != nil {
				fmt.Fprintf(os.Stderr, "warning: semantic recommend disabled: %v\n", err)
			} else if scorer != nil {
				srv.SetSemantic(scorer)
			}

			if debug {
				srv.SetLogger(log.New(os.Stderr, "[rivet-mcp] ", log.LstdFlags))
			}

			// Warm the recon cache in the background so the first
			// recon tool call doesn't pay the cold-start cost.
			go recon.Run([]string{"refresh"})

			return srv.Serve(os.Stdin, os.Stdout)
		},
	}

	cmd.Flags().BoolVar(&debug, "debug", false, "enable debug logging to stderr")

	return cmd
}
