package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/djtouchette/rivet/internal/recon"
	"github.com/spf13/cobra"
)

func newContextScaffoldCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "scaffold",
		Short: "Generate starter context docs from your codebase",
		Long: `Analyze the project using recon and generate starter context documents
in .rivet/context/. Existing documents are never overwritten.

The scaffolder creates domain and module docs based on the project's
top-level source directories, hotspot analysis, and framework detection.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := os.Stat(".rivet"); os.IsNotExist(err) {
				return fmt.Errorf(".rivet/ not found — run 'rivet init' first")
			}

			docs, err := scaffold()
			if err != nil {
				return err
			}

			if len(docs) == 0 {
				fmt.Println("No context documents to scaffold.")
				return nil
			}

			var wrote, skipped int
			for _, doc := range docs {
				dir := filepath.Dir(doc.path)
				if err := os.MkdirAll(dir, 0755); err != nil {
					return fmt.Errorf("creating %s: %w", dir, err)
				}

				if fileExists(doc.path) {
					skipped++
					if dryRun {
						fmt.Printf("  skip  %s (exists)\n", doc.path)
					}
					continue
				}

				if dryRun {
					fmt.Printf("  write %s\n", doc.path)
					continue
				}

				if err := os.WriteFile(doc.path, []byte(doc.content), 0644); err != nil {
					return fmt.Errorf("writing %s: %w", doc.path, err)
				}
				wrote++
				fmt.Printf("  + %s\n", doc.path)
			}

			if dryRun {
				fmt.Printf("\nDry run: would write %d files, skip %d existing\n", len(docs)-skipped, skipped)
			} else {
				fmt.Printf("\nScaffolded %d context docs (%d skipped)\n", wrote, skipped)
				if wrote > 0 {
					fmt.Println("Review and edit them, then run 'rivet sync' to update CLAUDE.md")
				}
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be created without writing")
	return cmd
}

type scaffoldDoc struct {
	path    string
	content string
}

func scaffold() ([]scaffoldDoc, error) {
	// Get overview from recon.
	stdout, stderr, exitCode, err := recon.Run([]string{"overview"})
	if err != nil {
		return nil, fmt.Errorf("running recon overview: %w", err)
	}
	if exitCode != 0 {
		return nil, fmt.Errorf("recon overview failed: %s", stderr)
	}

	var overview struct {
		Root       string `json:"root"`
		Languages  []struct {
			Name       string   `json:"name"`
			FileCount  int      `json:"file_count"`
			Extensions []string `json:"extensions"`
		} `json:"languages"`
		Frameworks []struct {
			Name     string `json:"name"`
			Language string `json:"language"`
		} `json:"frameworks"`
		Structure []struct {
			Path      string   `json:"path"`
			FileCount int      `json:"file_count"`
			Languages []string `json:"languages"`
			Purpose   string   `json:"purpose"`
		} `json:"structure"`
		Entrypoints []struct {
			Path string `json:"path"`
			Kind string `json:"kind"`
		} `json:"entrypoints"`
	}
	if err := json.Unmarshal([]byte(stdout), &overview); err != nil {
		return nil, fmt.Errorf("parsing recon overview: %w", err)
	}

	// Get hotspots.
	hotStdout, _, _, _ := recon.Run([]string{"hotspots"})
	var hotspots []struct {
		RelPath      string  `json:"path"`
		FanIn        int     `json:"fan_in"`
		FanOut       int     `json:"fan_out"`
		Churn        int     `json:"churn"`
		HotspotScore float64 `json:"hotspot_score"`
	}
	json.Unmarshal([]byte(hotStdout), &hotspots)

	var docs []scaffoldDoc

	// Detect primary language for language-specific scaffolding.
	primaryLang := ""
	if len(overview.Languages) > 0 {
		primaryLang = overview.Languages[0].Name
	}

	// Build domain docs from source directories.
	domains := discoverDomains(primaryLang)
	for _, domain := range domains {
		doc := buildDomainDoc(domain, hotspots, primaryLang)
		docs = append(docs, doc)
	}

	// Build a paradigm doc for the primary framework.
	if len(overview.Frameworks) > 0 {
		doc := buildFrameworkDoc(overview.Frameworks, primaryLang)
		docs = append(docs, doc)
	}

	// Build a hotspots paradigm doc if there are high-risk files.
	if len(hotspots) >= 3 {
		doc := buildHotspotsDoc(hotspots)
		docs = append(docs, doc)
	}

	return docs, nil
}

type domainInfo struct {
	name     string
	dirPath  string
	files    int
}

// discoverDomains scans the filesystem for domain-level directories.
// For Elixir: scans lib/<app>/subdirs that have .ex files with context modules.
// For others: scans top-level source directories.
func discoverDomains(primaryLang string) []domainInfo {
	var domains []domainInfo

	switch primaryLang {
	case "elixir":
		domains = discoverElixirDomains()
	default:
		domains = discoverGenericDomains()
	}

	sort.Slice(domains, func(i, j int) bool {
		return domains[i].files > domains[j].files
	})
	return domains
}

func discoverElixirDomains() []domainInfo {
	var domains []domainInfo

	// Find lib/<app_name>/ directories.
	libEntries, err := os.ReadDir("lib")
	if err != nil {
		return nil
	}

	for _, appDir := range libEntries {
		if !appDir.IsDir() {
			continue
		}
		appPath := filepath.Join("lib", appDir.Name())

		// Scan subdirectories of lib/<app_name>/ as domains.
		subEntries, err := os.ReadDir(appPath)
		if err != nil {
			continue
		}

		for _, sub := range subEntries {
			if !sub.IsDir() {
				continue
			}
			domainPath := filepath.Join(appPath, sub.Name())
			count := countFiles(domainPath, ".ex", ".exs")
			if count < 2 {
				continue
			}
			domains = append(domains, domainInfo{
				name:    sub.Name(),
				dirPath: domainPath,
				files:   count,
			})
		}
	}
	return domains
}

func discoverGenericDomains() []domainInfo {
	var domains []domainInfo
	// Scan common source directories.
	for _, srcDir := range []string{"src", "lib", "app", "pkg", "internal"} {
		entries, err := os.ReadDir(srcDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			domainPath := filepath.Join(srcDir, entry.Name())
			count := countFiles(domainPath, "")
			if count < 3 {
				continue
			}
			domains = append(domains, domainInfo{
				name:    entry.Name(),
				dirPath: domainPath,
				files:   count,
			})
		}
	}
	return domains
}

// countFiles counts files with any of the given extensions (or all files if empty) recursively.
func countFiles(dir string, exts ...string) int {
	count := 0
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if len(exts) == 0 || exts[0] == "" {
			count++
			return nil
		}
		for _, ext := range exts {
			if strings.HasSuffix(info.Name(), ext) {
				count++
				return nil
			}
		}
		return nil
	})
	return count
}

func buildDomainDoc(domain domainInfo, hotspots []struct {
	RelPath      string  `json:"path"`
	FanIn        int     `json:"fan_in"`
	FanOut       int     `json:"fan_out"`
	Churn        int     `json:"churn"`
	HotspotScore float64 `json:"hotspot_score"`
}, primaryLang string) scaffoldDoc {
	var b strings.Builder

	// Frontmatter.
	tags := deriveTags(domain.name)
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("tags: [%s]\n", strings.Join(tags, ", ")))
	b.WriteString("related_paths:\n")
	b.WriteString(fmt.Sprintf("  - \"%s/**\"\n", domain.dirPath))
	// Include the context module file (e.g., lib/app/accounts.ex alongside lib/app/accounts/).
	if primaryLang == "elixir" {
		b.WriteString(fmt.Sprintf("  - \"%s.ex\"\n", domain.dirPath))
	}
	b.WriteString("---\n\n")

	// Title.
	title := strings.ReplaceAll(domain.name, "_", " ")
	title = strings.Title(title)
	b.WriteString(fmt.Sprintf("# %s\n\n", title))
	b.WriteString(fmt.Sprintf("Source: `%s/` (%d files)\n\n", domain.dirPath, domain.files))

	// Hotspot files in this domain.
	// Match both lib/app/domain/ subdir files and lib/app/domain.ex context module.
	contextFile := domain.dirPath + ".ex"
	var domainHotspots []string
	for _, h := range hotspots {
		inDir := strings.HasPrefix(h.RelPath, domain.dirPath+"/")
		isContextFile := h.RelPath == contextFile
		if (inDir || isContextFile) && h.HotspotScore > 0.05 {
			domainHotspots = append(domainHotspots,
				fmt.Sprintf("- `%s` — fan-in: %d, churn: %d, score: %.2f",
					h.RelPath, h.FanIn, h.Churn, h.HotspotScore))
		}
	}
	if len(domainHotspots) > 0 {
		b.WriteString("## High-risk files\n\n")
		for _, line := range domainHotspots {
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	// Placeholder sections for the user to fill in.
	b.WriteString("## Overview\n\n")
	b.WriteString("<!-- Describe what this domain does and its key responsibilities -->\n\n")
	b.WriteString("## Key modules\n\n")
	b.WriteString("<!-- List the important modules/files and what they do -->\n\n")
	b.WriteString("## Failure modes\n\n")
	b.WriteString("<!-- How does this domain fail? Error handling, retries, circuit breakers, silent failures -->\n\n")
	b.WriteString("## Gotchas\n\n")
	b.WriteString("<!-- Non-obvious things: edge cases, implicit dependencies, common mistakes -->\n\n")

	path := filepath.Join(".rivet", "context", "domains", domain.name+".md")
	return scaffoldDoc{path: path, content: b.String()}
}

func buildFrameworkDoc(frameworks []struct {
	Name     string `json:"name"`
	Language string `json:"language"`
}, primaryLang string) scaffoldDoc {
	var b strings.Builder

	names := make([]string, 0, len(frameworks))
	tags := make([]string, 0, len(frameworks))
	for _, f := range frameworks {
		names = append(names, f.Name)
		tags = append(tags, strings.ToLower(strings.ReplaceAll(f.Name, " ", "-")))
	}

	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("tags: [%s, %s]\n", primaryLang, strings.Join(tags, ", ")))
	b.WriteString("---\n\n")

	b.WriteString(fmt.Sprintf("# Stack & Conventions\n\n"))
	b.WriteString(fmt.Sprintf("**Language:** %s\n", primaryLang))
	b.WriteString(fmt.Sprintf("**Frameworks:** %s\n\n", strings.Join(names, ", ")))

	b.WriteString("## Project conventions\n\n")
	b.WriteString("<!-- Describe naming conventions, file organization patterns, etc. -->\n\n")

	b.WriteString("## Testing approach\n\n")
	b.WriteString("<!-- How tests are organized, what test framework is used, fixtures, etc. -->\n\n")

	b.WriteString("## Common patterns\n\n")
	b.WriteString("<!-- Describe patterns used across the codebase (e.g., behaviours, GenServers, contexts) -->\n\n")

	path := filepath.Join(".rivet", "context", "paradigms", "stack.md")
	return scaffoldDoc{path: path, content: b.String()}
}

func buildHotspotsDoc(hotspots []struct {
	RelPath      string  `json:"path"`
	FanIn        int     `json:"fan_in"`
	FanOut       int     `json:"fan_out"`
	Churn        int     `json:"churn"`
	HotspotScore float64 `json:"hotspot_score"`
}) scaffoldDoc {
	var b strings.Builder

	b.WriteString("---\n")
	b.WriteString("tags: [risk, refactoring, hotspots, dependencies]\n")
	b.WriteString("---\n\n")

	b.WriteString("# Hotspots & Risk Areas\n\n")
	b.WriteString("Files with high fan-in and churn — the riskiest to modify.\n")
	b.WriteString("Generated by `rivet context scaffold` from `recon hotspots`.\n\n")

	// Show top 10.
	limit := 10
	if len(hotspots) < limit {
		limit = len(hotspots)
	}

	b.WriteString("| File | Fan-in | Churn | Score |\n")
	b.WriteString("|------|--------|-------|-------|\n")
	for _, h := range hotspots[:limit] {
		b.WriteString(fmt.Sprintf("| `%s` | %d | %d | %.2f |\n",
			h.RelPath, h.FanIn, h.Churn, h.HotspotScore))
	}
	b.WriteString("\n")

	// Related paths for all hotspot files.
	b.WriteString("## Guidelines\n\n")
	b.WriteString("<!-- Add notes about why these files are risky and what to watch out for -->\n\n")
	b.WriteString("When modifying any file listed above:\n")
	b.WriteString("- Check the fan-in count — that many files depend on this module\n")
	b.WriteString("- Run the full test suite, not just the file's own tests\n")
	b.WriteString("- Consider using `recon related <file>` to find affected files\n")

	path := filepath.Join(".rivet", "context", "paradigms", "hotspots.md")
	return scaffoldDoc{path: path, content: b.String()}
}

// deriveTags creates tags from a domain name by splitting on underscores/hyphens.
func deriveTags(name string) []string {
	tags := []string{name}
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '_' || r == '-'
	})
	if len(parts) > 1 {
		tags = append(tags, parts...)
	}
	// Deduplicate.
	seen := make(map[string]bool)
	var unique []string
	for _, t := range tags {
		if !seen[t] {
			seen[t] = true
			unique = append(unique, t)
		}
	}
	sort.Strings(unique[1:]) // keep the full name first
	return unique
}
