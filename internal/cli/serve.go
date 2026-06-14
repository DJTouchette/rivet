package cli

import (
	"fmt"
	"log"
	"os"
	"sync"

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

			// The four context tiers load independently — three are disk walks,
			// LoadCodeDocs shells out to recon to index the repo (the dominant
			// cost). Run them concurrently so the cheap walks overlap that
			// subprocess. Each tier degrades gracefully to nil on error, so a
			// failure warns but never aborts startup.
			var contexts, wiki, runbooks, codeDocs []*rivetctx.Document
			var loadWG sync.WaitGroup
			load := func(label string, dst *[]*rivetctx.Document, fn func() ([]*rivetctx.Document, error)) {
				loadWG.Add(1)
				go func() {
					defer loadWG.Done()
					docs, err := fn()
					if err != nil {
						fmt.Fprintf(os.Stderr, "warning: loading %s: %v\n", label, err)
						return
					}
					*dst = docs
				}()
			}
			load("context", &contexts, func() ([]*rivetctx.Document, error) {
				return rivetctx.Load(".rivet/context")
			})
			load("wiki", &wiki, func() ([]*rivetctx.Document, error) {
				return rivetctx.LoadWiki(".", cfg.Context.WikiPaths)
			})
			load("runbooks", &runbooks, func() ([]*rivetctx.Document, error) {
				return rivetctx.LoadRunbooks(".")
			})
			// Code-extracted docs (rivet:context comments, .context/ sidecars)
			// come from recon's index; this also warms the recon cache so the
			// first tool call is fast.
			load("code docs", &codeDocs, func() ([]*rivetctx.Document, error) {
				return rivetctx.LoadCodeDocs(recon.Run)
			})
			loadWG.Wait()

			pinReg := pins.NewRegistry()
			pinReg.Add(rally.NewPinProvider())

			policies := buildPolicies(cfg)
			srv := mcp.NewServer(reg, exec, contexts, pinReg, policies, version, cfg.Context.ShouldAutoCompact())
			srv.SetWiki(wiki)
			srv.SetRunbooks(runbooks)
			srv.SetCodeDocs(codeDocs)

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

			return srv.Serve(os.Stdin, os.Stdout)
		},
	}

	cmd.Flags().BoolVar(&debug, "debug", false, "enable debug logging to stderr")

	return cmd
}
