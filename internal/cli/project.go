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
	"github.com/djtouchette/rivet/internal/schema"
	schemaconfig "github.com/djtouchette/rivet/internal/schema/config"
	"github.com/djtouchette/rivet/internal/vaulty"
	"github.com/djtouchette/rivet/internal/witness"
	"github.com/spf13/cobra"
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
		lang       string
	)

	cmd := &cobra.Command{
		Use:   "init-cli",
		Short: "Scaffold a starter project CLI",
		Long: `Generate a project CLI with example commands organized by category
(query, check, task). The scaffolded CLI includes a rivet-discover
subcommand for auto-registration with Rivet.

Supported languages: go, elixir (auto-detected from project if --lang is omitted).

After scaffolding (Go):
  cd <dir> && go mod tidy && make build
  rivet project register-cli <dir>/<name>

After scaffolding (Elixir):
  Mix tasks are added directly to your project — run 'mix help' to see them.
  rivet project register-cli mix --discover <name>.rivet_discover`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := os.Stat(".rivet"); os.IsNotExist(err) {
				return fmt.Errorf(".rivet/ not found — run 'rivet init' first")
			}

			detected := ""
			if lang == "" {
				detected = detectProjectLanguage()
				lang = detected
			}

			// Only elixir and go have scaffolds. Everything else used to fall
			// through to the Go one, so a Python or Node repo silently acquired
			// a cobra module and a go.mod it never asked for. Refusing with the
			// detected language named is more useful than a wrong scaffold, and
			// --lang go is still there for anyone who wants the standalone Go
			// CLI deliberately — it is its own module, so it works from any repo.
			switch lang {
			case "elixir":
				return initElixirCLI(name)
			case "go":
				return initGoCLI(dir, name, modulePath)
			case "":
				// These messages already say exactly what to do, so cobra's
				// usage block underneath them is pure noise.
				cmd.SilenceUsage = true
				return fmt.Errorf("could not detect the project language — pass --lang go or --lang elixir")
			default:
				cmd.SilenceUsage = true
				if detected != "" {
					return fmt.Errorf("detected a %s project, which has no scaffold yet\n\n"+
						"Either pass --lang go for the standalone Go CLI (a separate module, usable from any project),\n"+
						"or write a CLI in %s that answers the discover protocol and register it with:\n"+
						"  rivet project register-cli <command> --discover <args>", lang, lang)
				}
				return fmt.Errorf("unsupported --lang %q: expected go or elixir", lang)
			}
		},
	}

	cmd.Flags().StringVar(&dir, "dir", "", "directory to scaffold into (default: tools/<name>, ignored for elixir)")
	cmd.Flags().StringVar(&name, "name", "projectcli", "CLI name / task namespace")
	cmd.Flags().StringVar(&modulePath, "module", "", "Go module path (default: same as name)")
	cmd.Flags().StringVar(&lang, "lang", "", "scaffold language: go, elixir (auto-detected if omitted)")

	return cmd
}

func initGoCLI(dir, name, modulePath string) error {
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

	printNextSteps(goNextSteps(dir, name, manifestPath))
	return nil
}

// goNextSteps is what to do after scaffolding a Go CLI.
//
// Registration is the step that sets project_cli.command and fills the manifest
// from the scaffolded rivet-discover command, so both scaffolds name it.
func goNextSteps(dir, name, manifestPath string) []string {
	return []string{
		fmt.Sprintf("cd %s && go mod tidy && make build", dir),
		fmt.Sprintf("rivet project register-cli ./%s", filepath.Join(dir, name)),
		fmt.Sprintf("Edit %s to add params to your capabilities", manifestPath),
		"rivet sync",
	}
}

func initElixirCLI(name string) error {
	if name == "projectcli" {
		name = "project"
	}

	result, err := projectcli.ScaffoldElixir(".", name)
	if err != nil {
		return err
	}

	if len(result.Files) == 0 && len(result.Skipped) > 0 {
		fmt.Println("All Mix task files already exist — nothing to do.")
		return nil
	}

	fmt.Println("Scaffolded Elixir project CLI as Mix tasks:")
	for _, f := range result.Files {
		fmt.Printf("  + %s\n", f)
	}
	for _, f := range result.Skipped {
		fmt.Printf("  ~ %s (exists, skipped)\n", f)
	}

	// Write capabilities manifest.
	manifestPath := capabilities.DefaultManifestPath()
	if !fileExists(manifestPath) {
		content := capabilities.StarterManifestElixir(name)
		if err := os.WriteFile(manifestPath, []byte(content), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not write %s: %v\n", manifestPath, err)
		} else {
			fmt.Printf("  + %s\n", manifestPath)
		}
	}

	printNextSteps(elixirNextSteps(name, manifestPath))
	return nil
}

// elixirNextSteps is what to do after scaffolding Mix tasks.
//
// The scaffolded discover task is namespaced — `mix <ns>.rivet_discover` — so
// registration has to be told its spelling once, and records it in
// project_cli.discover. Without that flag the task is unreachable and the
// starter manifest is the only thing an Elixir project ever gets.
func elixirNextSteps(name, manifestPath string) []string {
	return []string{
		fmt.Sprintf("mix %s.query.status --json   # test the scaffolded task", name),
		fmt.Sprintf("rivet project register-cli mix --discover %s.rivet_discover", name),
		fmt.Sprintf("Edit %s to add params to your capabilities", manifestPath),
		"rivet sync",
	}
}

func printNextSteps(steps []string) {
	fmt.Println()
	fmt.Println("Next steps:")
	for _, s := range steps {
		fmt.Printf("  %s\n", s)
	}
}

// detectProjectLanguage uses simple file heuristics to determine the primary
// language, returning "" when nothing matches.
//
// It used to fall back to "go", which meant a repo it couldn't identify was
// indistinguishable from a real Go repo and got a Go scaffold either way. An
// honest "I don't know" lets the caller say so.
func detectProjectLanguage() string {
	if fileExists("mix.exs") {
		return "elixir"
	}
	if fileExists("go.mod") {
		return "go"
	}
	if fileExists("package.json") {
		return "node"
	}
	if fileExists("Cargo.toml") {
		return "rust"
	}
	if fileExists("requirements.txt") || fileExists("pyproject.toml") || fileExists("setup.py") {
		return "python"
	}
	if fileExists("Gemfile") {
		return "ruby"
	}
	return ""
}

// --- register-cli ---

func newProjectRegisterCLICmd() *cobra.Command {
	var (
		skipDiscover  bool
		force         bool
		discoverFlags []string
	)

	cmd := &cobra.Command{
		Use:   "register-cli <path-to-binary|command>",
		Short: "Register a project CLI with Rivet",
		Long: `Register an existing project CLI. If it supports the rivet-discover
protocol, its capabilities are merged into .rivet/capabilities.yaml.

The argument is a path in the repo ("./tools/projectcli/projectcli") or a command
name on PATH ("mix") for a CLI that runs through an interpreter.

The rivet-discover protocol: the CLI answers a discover command by printing JSON
with a "capabilities" array. That command defaults to the single token
"rivet-discover"; pass --discover (repeatable) when it is spelled differently —
an Elixir Mix task lives in the project's namespace:

  rivet project register-cli mix --discover project.rivet_discover

The discover command is saved to project_cli.discover, so later runs need no flag.

Re-running refreshes each discovered capability's description, command and output,
and tightens its safety level, while leaving your typed params — and every comment
in the file — alone. A discovered safety level that would RELAX one already in the
manifest is reported and skipped unless --force is passed.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := projectcli.ResolveTarget(args[0])
			if err != nil {
				return err
			}

			cfg, err := config.LoadProject()
			if err != nil {
				return err
			}

			cfg.ProjectCLI.Command = target.Config
			var actions []string
			actions = append(actions, fmt.Sprintf("set project_cli.command = %s", target.Config))

			// An explicit --discover is recorded, so registering an Elixir CLI is a
			// one-time flag rather than a thing to remember on every re-run.
			discoverArgs := cfg.ProjectCLI.Discover
			if len(discoverFlags) > 0 {
				discoverArgs = discoverFlags
				cfg.ProjectCLI.Discover = discoverFlags
				actions = append(actions, fmt.Sprintf("set project_cli.discover = [%s]", strings.Join(discoverFlags, " ")))
			}
			discoverArgs = projectcli.NormalizeDiscoverArgs(discoverArgs)

			var discovered *projectcli.DiscoverResult
			if !skipDiscover {
				discovered, err = projectcli.RunDiscover(target.Exec, discoverArgs...)
				if err != nil {
					fmt.Fprintf(os.Stderr, "warning: discovery failed: %v\n", err)
				}
			}

			manifestPath := capabilities.DefaultManifestPath()
			var merge *projectcli.MergeResult
			var notes []string
			if discovered != nil && len(discovered.Capabilities) > 0 {
				caps := make([]projectcli.DiscoveredCapability, len(discovered.Capabilities))
				for i, dc := range discovered.Capabilities {
					// The manifest holds subcommand args only: ToCapabilities prepends
					// cli: to every command. Strip whichever spelling of the binary the
					// CLI reported — os.Executable() resolves symlinks, the typed path
					// and a PATH command name do not.
					dc.Command = projectcli.StripBinaryPrefix(dc.Command, args[0], target.Exec, target.Config)
					caps[i] = dc
				}

				merge, err = projectcli.MergeManifest(manifestPath, target.Config, caps, force)
				if err != nil {
					return err
				}
				actions = append(actions, "merged discovered capabilities into "+manifestPath)
			} else {
				if !skipDiscover {
					actions = append(actions, fmt.Sprintf("no rivet-discover support — `%s %s` reported nothing",
						target.Config, strings.Join(discoverArgs, " ")))
					notes = append(notes,
						"If your discover command is spelled differently, pass --discover <token>",
						"  e.g. rivet project register-cli mix --discover project.rivet_discover",
						"Otherwise edit "+manifestPath+" by hand.")
				}
				// Discovery or not, the manifest's cli: has to follow the binary
				// just registered, or config.yaml points at the new one while every
				// capability keeps running the old.
				if fileExists(manifestPath) {
					if _, err := projectcli.MergeManifest(manifestPath, target.Config, nil, force); err != nil {
						return err
					}
					actions = append(actions, fmt.Sprintf("set cli = %s in %s", target.Config, manifestPath))
				}
			}

			if err := cfg.Write(); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}

			fmt.Println("Registered project CLI:")
			for _, a := range actions {
				fmt.Printf("  + %s\n", a)
			}

			if merge != nil {
				fmt.Println()
				for _, line := range merge.Summary() {
					fmt.Println(line)
				}
			}
			for _, n := range notes {
				fmt.Println(n)
			}

			fmt.Println()
			fmt.Printf("Edit %s to add typed params to your capabilities.\n", manifestPath)
			fmt.Println("Run 'rivet inspect capabilities' to see all registered capabilities.")

			return nil
		},
	}

	cmd.Flags().BoolVar(&skipDiscover, "skip-discover", false, "skip running the discover command")
	cmd.Flags().StringArrayVar(&discoverFlags, "discover", nil,
		"discover command argv, repeat for multiple tokens (default: rivet-discover)")
	cmd.Flags().BoolVar(&force, "force", false,
		"let discovered safety levels overwrite the manifest's, even when that relaxes them")

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

			res, err := newExecutor(reg).Run(context.Background(), capName, extraArgs, approve)
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

// newExecutor builds the capability executor every rivet entry point uses.
//
// The in-process runner table is the only thing standing between a builtin and
// os/exec: Executor.RunCapability silently falls back to exec.Command when
// Command[0] has no registered runner, so a missing entry turns `witness.select`
// into a hunt for a `witness` binary that rivet never installs. Registering the
// runners here — instead of once per command — is what keeps the MCP path
// (`rivet serve`) and the CLI path (`rivet project run`) from drifting apart.
// Every capability with Builtin: true must have its Command[0] covered here.
func newExecutor(reg *capabilities.Registry) *capabilities.Executor {
	exec := capabilities.NewExecutor(reg)
	exec.RegisterInProcess("vaulty", vaulty.Run)
	exec.RegisterInProcess("recon", recon.Run)
	exec.RegisterInProcess("witness", witness.Run)
	exec.RegisterInProcess("schema", schema.Run)
	return exec
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

// builtinGroupsFor decides which optional builtin groups this project gets.
// Auto-detection is the default; the tools: section of config.yaml overrides it
// in either direction.
func builtinGroupsFor(cfg *config.Config) capabilities.BuiltinGroups {
	return capabilities.BuiltinGroups{
		Schema: cfg.Tools.SchemaEnabled(schemaInUse(cfg.Path())),
		Vaulty: cfg.Tools.VaultyEnabled(vaultyInUse()),
	}
}

// schemaInUse reports whether the schema: section of .rivet/config.yaml names
// anything the schema tools can work on. With no database, no migrations dir,
// and no code-scan root there is nothing to read and every schema.* call fails
// the same way, so the definitions are pure context cost.
func schemaInUse(rivetConfigPath string) bool {
	sc, err := schemaconfig.Load(rivetConfigPath)
	if err != nil {
		// An unparseable schema section can't drive the tools either.
		return false
	}
	return len(sc.Databases) > 0 || len(sc.Migrations.AllDirs()) > 0 || len(sc.CodeScan.Roots) > 0
}

// vaultyInUse reports whether a vault exists for this project or user. Vaulty
// resolves its config from ./vaulty.{toml,yaml,yml}, .vaulty/, and
// ~/.config/vaulty/, falling back to the ~/.config/vaulty/vault.age store; with
// none of those present every vaulty.* call dies on an unopenable vault.
// Creating that first vault is a human CLI step (`rivet vaulty init`), never an
// MCP one, so withholding the tools until then loses nothing.
func vaultyInUse() bool {
	configNames := []string{"vaulty.toml", "vaulty.yaml", "vaulty.yml"}

	for _, name := range configNames {
		if fileExists(name) || fileExists(filepath.Join(".vaulty", name)) {
			return true
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	dir := filepath.Join(home, ".config", "vaulty")
	for _, name := range append(configNames, "vault.age") {
		if fileExists(filepath.Join(dir, name)) {
			return true
		}
	}
	return false
}

// buildRegistry loads built-in capabilities first, then the capabilities
// manifest (.rivet/capabilities.yaml), then config overrides.
// Shared between inspect, project, and serve commands.
func buildRegistry(cfg *config.Config) *capabilities.Registry {
	reg := capabilities.NewRegistry()

	// Register builtins first, minus the optional groups this project can't use.
	for _, b := range capabilities.BuiltinsFor(builtinGroupsFor(cfg)) {
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
