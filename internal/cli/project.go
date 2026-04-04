package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/djtouchette/rivet/internal/capabilities"
	"github.com/djtouchette/rivet/internal/config"
	"github.com/djtouchette/rivet/internal/policy"
	"github.com/djtouchette/rivet/internal/projectcli"
	"github.com/djtouchette/rivet/internal/recon"
	"github.com/djtouchette/rivet/internal/vaulty"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newProjectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Interact with project capabilities",
	}

	cmd.AddCommand(newProjectRunCmd())
	cmd.AddCommand(newProjectInitCLICmd())
	cmd.AddCommand(newProjectRegisterCLICmd())
	cmd.AddCommand(newProjectCommandsCmd())

	return cmd
}

// --- init-cli ---

func newProjectInitCLICmd() *cobra.Command {
	var (
		dir        string
		name       string
		modulePath string
	)

	cmd := &cobra.Command{
		Use:   "init-cli",
		Short: "Scaffold a starter project CLI",
		Long: `Generate a Go + cobra project CLI with example commands organized by
category (query, check, task). The scaffolded CLI includes a rivet-discover
subcommand for auto-registration with Rivet.

After scaffolding:
  cd <dir> && go mod tidy && make build
  rivet project register-cli <dir>/<name>`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := os.Stat(".rivet"); os.IsNotExist(err) {
				return fmt.Errorf(".rivet/ not found — run 'rivet init' first")
			}

			if dir == "" {
				dir = filepath.Join("tools", name)
			}

			result, err := projectcli.Scaffold(dir, name, modulePath)
			if err != nil {
				return err
			}

			if len(result.Files) == 0 && len(result.Skipped) > 0 {
				fmt.Printf("All files already exist in %s/ — nothing to do.\n", dir)
				return nil
			}

			fmt.Printf("Scaffolded project CLI in %s/:\n", dir)
			for _, f := range result.Files {
				fmt.Printf("  + %s\n", f)
			}
			for _, f := range result.Skipped {
				fmt.Printf("  ~ %s (exists, skipped)\n", f)
			}

			// Write capabilities manifest.
			manifestPath := capabilities.DefaultManifestPath()
			if !fileExists(manifestPath) {
				cliPath := "./" + filepath.Join(dir, name)
				content := capabilities.StarterManifest(cliPath, name)
				if err := os.WriteFile(manifestPath, []byte(content), 0644); err != nil {
					fmt.Fprintf(os.Stderr, "warning: could not write %s: %v\n", manifestPath, err)
				} else {
					fmt.Printf("  + %s\n", manifestPath)
				}
			}

			fmt.Println()
			fmt.Println("Next steps:")
			fmt.Printf("  cd %s && go mod tidy && make build\n", dir)
			fmt.Printf("  Edit %s to add params to your capabilities\n", manifestPath)
			fmt.Println("  rivet sync")

			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "", "directory to scaffold into (default: tools/<name>)")
	cmd.Flags().StringVar(&name, "name", "projectcli", "CLI binary name")
	cmd.Flags().StringVar(&modulePath, "module", "", "Go module path (default: same as name)")

	return cmd
}

// --- register-cli ---

func newProjectRegisterCLICmd() *cobra.Command {
	var skipDiscover bool

	cmd := &cobra.Command{
		Use:   "register-cli <path-to-binary>",
		Short: "Register a project CLI with Rivet",
		Long: `Register an existing project CLI binary. If the binary supports the
rivet-discover protocol, its capabilities are automatically added to config.yaml.

The rivet-discover protocol: the binary should have a hidden "rivet-discover"
subcommand that outputs JSON with a "capabilities" array.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			binaryPath := args[0]

			// Resolve to absolute path, then make relative to cwd if possible.
			absPath, err := filepath.Abs(binaryPath)
			if err != nil {
				return fmt.Errorf("resolving path: %w", err)
			}

			// Check it exists.
			info, err := os.Stat(absPath)
			if err != nil {
				return fmt.Errorf("binary not found: %s", binaryPath)
			}
			if info.IsDir() {
				return fmt.Errorf("%s is a directory, not a binary", binaryPath)
			}

			// Use relative path for config if under cwd.
			cwd, _ := os.Getwd()
			configPath := absPath
			if rel, err := filepath.Rel(cwd, absPath); err == nil && !strings.HasPrefix(rel, "..") {
				configPath = "./" + rel
			}

			cfg, err := config.LoadOrDefault("")
			if err != nil {
				return err
			}

			cfg.ProjectCLI.Command = configPath
			var actions []string
			actions = append(actions, fmt.Sprintf("set project_cli.command = %s", configPath))

			// Try discovery → write capabilities manifest.
			var discovered *projectcli.DiscoverResult
			if !skipDiscover {
				discovered, err = projectcli.RunDiscover(absPath)
				if err != nil {
					fmt.Fprintf(os.Stderr, "warning: discovery failed: %v\n", err)
				}
			}

			manifestPath := capabilities.DefaultManifestPath()
			if discovered != nil && len(discovered.Capabilities) > 0 {
				// Write or merge into capabilities manifest.
				manifest := capabilities.LoadManifestOrNil(manifestPath)
				if manifest == nil {
					manifest = &capabilities.Manifest{}
				}
				manifest.CLI = configPath

				existing := make(map[string]bool)
				for _, c := range manifest.Capabilities {
					existing[c.Name] = true
				}

				var added int
				for _, dc := range discovered.Capabilities {
					if existing[dc.Name] {
						continue
					}
					// Strip the CLI binary from the command to store relative subcommands.
					subCmd := dc.Command
					if len(subCmd) > 0 && subCmd[0] == absPath {
						subCmd = subCmd[1:]
					}
					manifest.Capabilities = append(manifest.Capabilities, capabilities.ManifestCap{
						Name:        dc.Name,
						Description: dc.Description,
						Command:     subCmd,
						Output:      dc.Output,
						Safety:      dc.Safety,
					})
					added++
				}

				data, _ := yaml.Marshal(manifest)
				if err := os.WriteFile(manifestPath, data, 0644); err != nil {
					return fmt.Errorf("writing manifest: %w", err)
				}
				actions = append(actions, fmt.Sprintf("wrote %s (%d capabilities, %d new)",
					manifestPath, len(manifest.Capabilities), added))
			} else if !skipDiscover {
				actions = append(actions, "no rivet-discover support — edit "+manifestPath+" manually")
			}

			if err := cfg.Write(); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}

			fmt.Println("Registered project CLI:")
			for _, a := range actions {
				fmt.Printf("  + %s\n", a)
			}

			fmt.Println()
			fmt.Printf("Edit %s to add typed params to your capabilities.\n", manifestPath)
			fmt.Println("Run 'rivet inspect capabilities' to see all registered capabilities.")

			return nil
		},
	}

	cmd.Flags().BoolVar(&skipDiscover, "skip-discover", false, "skip running rivet-discover")

	return cmd
}

// --- commands ---

func newProjectCommandsCmd() *cobra.Command {
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "commands",
		Short: "List registered project commands",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadOrDefault("")
			if err != nil {
				return err
			}

			reg := buildRegistry(cfg)
			caps := reg.ListByKind(capabilities.KindProjectCommand)

			if jsonOutput {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(caps)
			}

			if len(caps) == 0 {
				fmt.Println("No project commands registered.")
				if cfg.ProjectCLI.Command == "" {
					fmt.Println()
					fmt.Println("Get started:")
					fmt.Println("  rivet project init-cli    Scaffold a starter project CLI")
					fmt.Println("  rivet project register-cli <path>    Register an existing CLI")
				}
				return nil
			}

			fmt.Printf("Project commands (%d):\n\n", len(caps))
			for _, c := range caps {
				safety := string(c.Safety)
				fmt.Printf("  %-30s [%s]  %s\n", c.Name, safety, c.Description)
			}

			if cfg.ProjectCLI.Command != "" {
				fmt.Printf("\nProject CLI: %s\n", cfg.ProjectCLI.Command)
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON")

	return cmd
}

// --- run (existing) ---

func newProjectRunCmd() *cobra.Command {
	var (
		approve    bool
		jsonOutput bool
	)

	cmd := &cobra.Command{
		Use:   "run <capability> [-- extra-args...]",
		Short: "Execute a registered capability",
		Long: `Execute a registered capability by name, passing any extra arguments after --.

Examples:
  rivet project run db.patient-summary
  rivet project run db.patient-summary -- --since 7d
  rivet project run dangerous.cmd --approve`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			capName := args[0]
			extraArgs := args[1:]

			cfg, err := config.LoadOrDefault("")
			if err != nil {
				return err
			}

			reg := buildRegistry(cfg)

			cap := reg.Get(capName)
			if cap == nil {
				return fmt.Errorf("capability %q not found; run 'rivet inspect capabilities' to see available capabilities", capName)
			}

			if cap.Safety == capabilities.SafetyLevelGuarded {
				fmt.Fprintf(os.Stderr, "warning: %q is a guarded capability\n", capName)
			}

			// Check policy rules before execution.
			rules := buildPolicies(cfg)
			if violations := policy.Check(rules, cap, nil); len(violations) > 0 {
				return fmt.Errorf("capability %q blocked by policy: %s",
					capName, policy.FormatViolations(violations))
			}

			executor := capabilities.NewExecutor(reg)
			executor.RegisterInProcess("vaulty", vaulty.Run)
			executor.RegisterInProcess("recon", recon.Run)
			res, err := executor.Run(context.Background(), capName, extraArgs, approve)
			if err != nil {
				return err
			}

			if jsonOutput {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}

			if res.Stderr != "" {
				fmt.Fprint(os.Stderr, res.Stderr)
			}
			fmt.Print(res.Stdout)

			if res.ExitCode != 0 {
				os.Exit(res.ExitCode)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&approve, "approve", false, "approve execution of dangerous capabilities")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output result as JSON (includes stdout, stderr, exit code)")

	return cmd
}

// buildPolicies converts config policy definitions to policy.Rule values.
func buildPolicies(cfg *config.Config) []policy.Rule {
	rules := make([]policy.Rule, len(cfg.Policies))
	for i, def := range cfg.Policies {
		rules[i] = policy.Rule{
			Name:        def.Name,
			Description: def.Description,
			Match: policy.Match{
				Safety:       def.Match.Safety,
				Kind:         def.Match.Kind,
				Capabilities: def.Match.Capabilities,
			},
			RequireEnv: def.RequireEnv,
			DenyEnv:    def.DenyEnv,
		}
	}
	return rules
}

// buildRegistry loads built-in capabilities first, then the capabilities
// manifest (.rivet/capabilities.yaml), then config overrides.
// Shared between inspect, project, and serve commands.
func buildRegistry(cfg *config.Config) *capabilities.Registry {
	reg := capabilities.NewRegistry()

	// Register builtins first.
	for _, b := range capabilities.Builtins() {
		reg.Register(b)
	}

	// Load capabilities manifest (typed params, project CLI commands).
	if m := capabilities.LoadManifestOrNil(capabilities.DefaultManifestPath()); m != nil {
		caps, err := m.ToCapabilities()
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: manifest: %v\n", err)
		}
		for _, c := range caps {
			if err := reg.Override(c); err != nil {
				fmt.Fprintf(os.Stderr, "warning: manifest cap %q: %v\n", c.Name, err)
			}
		}
	}

	// Config capabilities override everything (including manifest).
	for _, def := range cfg.Capabilities {
		kind, err := capabilities.ParseCapabilityKind(def.Kind)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping %q: %v\n", def.Name, err)
			continue
		}
		safety, err := capabilities.ParseSafetyLevel(def.Safety)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping %q: %v\n", def.Name, err)
			continue
		}
		cap := capabilities.Capability{
			Name:             def.Name,
			Kind:             kind,
			Description:      def.Description,
			Command:          def.Command,
			Output:           def.Output,
			Safety:           safety,
			RequiresApproval: def.RequiresApproval,
		}
		if err := reg.Override(cap); err != nil {
			fmt.Fprintf(os.Stderr, "warning: %v\n", err)
		}
	}
	return reg
}
