package projectcli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ScaffoldResult holds what was created.
type ScaffoldResult struct {
	Dir     string   // root directory of the scaffolded CLI
	Files   []string // relative paths of created files
	Skipped []string // files that already existed
}

// Scaffold generates a starter project CLI in the given directory.
// The cliName is used for the binary name and module path suffix.
// If dir already contains files, existing files are not overwritten.
func Scaffold(dir, cliName, modulePath string) (*ScaffoldResult, error) {
	if cliName == "" {
		cliName = "projectcli"
	}
	if modulePath == "" {
		modulePath = cliName
	}

	files := scaffoldFiles(cliName, modulePath)

	result := &ScaffoldResult{Dir: dir}

	for _, f := range files {
		path := filepath.Join(dir, f.relPath)

		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, fmt.Errorf("creating directory for %s: %w", f.relPath, err)
		}

		if _, err := os.Stat(path); err == nil {
			result.Skipped = append(result.Skipped, f.relPath)
			continue
		}

		if err := os.WriteFile(path, []byte(f.content), 0644); err != nil {
			return nil, fmt.Errorf("writing %s: %w", f.relPath, err)
		}
		result.Files = append(result.Files, f.relPath)
	}

	return result, nil
}

type scaffoldFile struct {
	relPath string
	content string
}

func scaffoldFiles(cliName, modulePath string) []scaffoldFile {
	return []scaffoldFile{
		{relPath: "go.mod", content: tmplGoMod(modulePath)},
		{relPath: "main.go", content: tmplMain(modulePath)},
		{relPath: "commands/root.go", content: tmplRoot(cliName)},
		{relPath: "commands/discover.go", content: tmplDiscover(cliName)},
		{relPath: "commands/query_status.go", content: tmplQueryStatus()},
		{relPath: "commands/check_health.go", content: tmplCheckHealth()},
		{relPath: "commands/task_seed.go", content: tmplTaskSeed()},
		{relPath: "Makefile", content: tmplMakefile(cliName)},
	}
}

// The Elixir equivalent of this used to live here too, and was deleted for
// being a third hardcoded copy of the same capability list — after the discover
// template above and capabilities.StarterManifest* — which is exactly where
// safety levels drift apart unnoticed. The Go copy went the same way: it had no
// caller, and its comment claimed register-cli used it "when the binary hasn't
// been built yet", which was never true. register-cli only ever runs the real
// discover command. The scaffolded CLI reports its own capabilities; that is
// the single source of truth.

func tmplGoMod(modulePath string) string {
	return fmt.Sprintf(`module %s

go 1.23

require github.com/spf13/cobra v1.9.1

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.6 // indirect
)
`, modulePath)
}

func tmplMain(modulePath string) string {
	return fmt.Sprintf(`package main

import (
	"%s/commands"
	"os"
)

func main() {
	if err := commands.Execute(); err != nil {
		os.Exit(1)
	}
}
`, modulePath)
}

func tmplRoot(cliName string) string {
	title := strings.ReplaceAll(cliName, "-", " ")
	title = strings.ReplaceAll(title, "_", " ")

	return fmt.Sprintf(`package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var jsonOutput bool

var rootCmd = &cobra.Command{
	Use:   "%s",
	Short: "%s — project CLI",
	Long:  "Project-specific operations exposed to Rivet and Claude Code.",
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "output as JSON")

	// Command categories.
	queryCmd := &cobra.Command{
		Use:   "query",
		Short: "Read-only information retrieval",
	}
	checkCmd := &cobra.Command{
		Use:   "check",
		Short: "Validation and diagnostics",
	}
	taskCmd := &cobra.Command{
		Use:   "task",
		Short: "Safe repeatable task execution",
	}

	// Register subcommands.
	queryCmd.AddCommand(newQueryStatusCmd())
	checkCmd.AddCommand(newCheckHealthCmd())
	taskCmd.AddCommand(newTaskSeedCmd())

	rootCmd.AddCommand(queryCmd)
	rootCmd.AddCommand(checkCmd)
	rootCmd.AddCommand(taskCmd)
	rootCmd.AddCommand(newDiscoverCmd())
}

func Execute() error {
	return rootCmd.Execute()
}

// jsonOrText prints val as JSON if --json is set, otherwise prints the human string.
func jsonOrText(jsonStr, humanStr string) {
	if jsonOutput {
		fmt.Fprintln(os.Stdout, jsonStr)
	} else {
		fmt.Fprintln(os.Stdout, humanStr)
	}
}
`, cliName, title)
}

func tmplDiscover(cliName string) string {
	return fmt.Sprintf(`package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

type discoveredCapability struct {
	Name        string   `+"`"+`json:"name"`+"`"+`
	Kind        string   `+"`"+`json:"kind"`+"`"+`
	Description string   `+"`"+`json:"description"`+"`"+`
	Command     []string `+"`"+`json:"command"`+"`"+`
	Output      string   `+"`"+`json:"output"`+"`"+`
	Safety      string   `+"`"+`json:"safety"`+"`"+`
}

type discoverResult struct {
	Capabilities []discoveredCapability `+"`"+`json:"capabilities"`+"`"+`
}

func newDiscoverCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "rivet-discover",
		Short:  "Output capability definitions for Rivet registration",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Resolve this binary's path for the command field.
			bin, err := os.Executable()
			if err != nil {
				bin = "%s"
			}
			bin, _ = filepath.Abs(bin)

			result := discoverResult{
				Capabilities: []discoveredCapability{
					{
						Name:        "%s.status",
						Kind:        "project_command",
						Description: "Show project status summary",
						Command:     []string{bin, "query", "status"},
						Output:      "json",
						Safety:      "safe",
					},
					{
						Name:        "%s.health",
						Kind:        "project_command",
						Description: "Run health checks",
						Command:     []string{bin, "check", "health"},
						Output:      "json",
						Safety:      "safe",
					},
					{
						Name:        "%s.seed",
						Kind:        "project_command",
						Description: "Seed development data",
						Command:     []string{bin, "task", "seed"},
						Output:      "json",
						Safety:      "guarded",
					},
				},
			}

			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(result); err != nil {
				return fmt.Errorf("encoding discover output: %%w", err)
			}
			return nil
		},
	}
}
`, cliName, cliName, cliName, cliName)
}

func tmplQueryStatus() string {
	return `package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/cobra"
)

func newQueryStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show project status summary",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO: Replace with real project status logic.
			status := map[string]any{
				"status":  "ok",
				"go":      runtime.Version(),
				"os":      runtime.GOOS,
				"arch":    runtime.GOARCH,
			}

			if jsonOutput {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(status)
			}

			fmt.Printf("Status:  %s\n", status["status"])
			fmt.Printf("Go:      %s\n", status["go"])
			fmt.Printf("OS:      %s/%s\n", status["os"], status["arch"])
			return nil
		},
	}
}
`
}

func tmplCheckHealth() string {
	return `package commands

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

type healthCheck struct {
	Name   string ` + "`" + `json:"name"` + "`" + `
	Status string ` + "`" + `json:"status"` + "`" + `
	Detail string ` + "`" + `json:"detail,omitempty"` + "`" + `
}

func newCheckHealthCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "Run health checks",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO: Replace with real health checks.
			checks := []healthCheck{
				{Name: "config", Status: "pass", Detail: "configuration loaded"},
				{Name: "dependencies", Status: "pass", Detail: "all dependencies available"},
			}

			allPass := true
			for _, c := range checks {
				if c.Status != "pass" {
					allPass = false
				}
			}

			result := map[string]any{
				"healthy": allPass,
				"checks":  checks,
			}

			if jsonOutput {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(result)
			}

			for _, c := range checks {
				icon := "OK"
				if c.Status != "pass" {
					icon = "FAIL"
				}
				fmt.Printf("  [%s] %s — %s\n", icon, c.Name, c.Detail)
			}
			if allPass {
				fmt.Println("\nAll checks passed.")
			} else {
				fmt.Println("\nSome checks failed.")
			}
			return nil
		},
	}
}
`
}

func tmplTaskSeed() string {
	return `package commands

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newTaskSeedCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "seed",
		Short: "Seed development data",
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO: Replace with real seed logic.
			result := map[string]any{
				"seeded": true,
				"records": map[string]int{
					"users":   5,
					"items":   20,
				},
			}

			if jsonOutput {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(result)
			}

			fmt.Println("Seeded development data:")
			fmt.Println("  users:  5")
			fmt.Println("  items:  20")
			return nil
		},
	}
}
`
}

func tmplMakefile(cliName string) string {
	return fmt.Sprintf(`BIN := %s

.PHONY: build clean

build:
	go build -o $(BIN) .

clean:
	rm -f $(BIN)
`, cliName)
}

// fileExists returns true if path exists on disk.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// mkdirForFile creates all parent directories for the given file path.
func mkdirForFile(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0755)
}

// writeFile writes content to the given path.
func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}
