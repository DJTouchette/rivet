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
					printScaffoldNote(doc)
					continue
				}

				if err := os.WriteFile(doc.path, []byte(doc.content), 0644); err != nil {
					return fmt.Errorf("writing %s: %w", doc.path, err)
				}
				wrote++
				fmt.Printf("  + %s\n", doc.path)
				printScaffoldNote(doc)
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
	// note explains something the reader would otherwise have to guess at —
	// currently only "recon could not name a framework, and here is why". It is
	// printed when the doc is written, so a thin stack.md is never a silent
	// surprise.
	note string
}

// printScaffoldNote surfaces a doc's caveat next to the file it was written to.
// A doc that says less than the user expected needs to say why at the moment it
// appears, not only inside itself.
func printScaffoldNote(doc scaffoldDoc) {
	if doc.note == "" {
		return
	}
	fmt.Printf("        note: %s\n", doc.note)
}

// The subset of `recon overview` that the scaffolder reads. Named types rather
// than an anonymous struct because two of these fields — Dependencies and
// FrameworkStatus — are new, and the difference between "recon said no" and
// "this recon build never said anything" now changes what gets written.
type reconFramework struct {
	Name     string `json:"name"`
	Language string `json:"language"`
	Evidence string `json:"evidence"`
}

// reconDependency is what a manifest declares. Recon reports these separately
// from frameworks because a manifest entry is not proof that a project is built
// on something: a Maven project used to list its own artifact id and its build
// plugins as frameworks.
type reconDependency struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Language string `json:"language"`
	Manifest string `json:"manifest"`
}

type reconOverview struct {
	Root      string `json:"root"`
	Languages []struct {
		Name       string   `json:"name"`
		FileCount  int      `json:"file_count"`
		Extensions []string `json:"extensions"`
	} `json:"languages"`
	Frameworks   []reconFramework  `json:"frameworks"`
	Dependencies []reconDependency `json:"dependencies"`
	Structure    []struct {
		Path      string   `json:"path"`
		FileCount int      `json:"file_count"`
		Languages []string `json:"languages"`
		Purpose   string   `json:"purpose"`
	} `json:"structure"`
	Entrypoints []struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
	} `json:"entrypoints"`

	// FrameworkStatus says why Frameworks is empty. An empty list alone meant
	// both "nothing matched" and "recon has no detector for this language", and
	// the scaffolder used to act on the more optimistic reading by writing
	// nothing at all.
	FrameworkStatus string `json:"framework_status"`
}

// Statuses recon reports for its framework list. The set is open — recon may
// add values, e.g. for a detector that errored — so nothing here switches
// exhaustively on it. An unrecognised status is reported verbatim and treated
// as "undetermined", which is the safe reading for any value we don't know.
const (
	detectStatusFound       = "found"
	detectStatusNoneMatched = "none_matched"
	detectStatusUnsupported = "unsupported"
)

func scaffold() ([]scaffoldDoc, error) {
	// Get overview from recon.
	stdout, stderr, exitCode, err := recon.Run([]string{"overview"})
	if err != nil {
		return nil, fmt.Errorf("running recon overview: %w", err)
	}
	if exitCode != 0 {
		return nil, fmt.Errorf("recon overview failed: %s", stderr)
	}

	var overview reconOverview
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

	// Build the stack doc unconditionally. It used to be gated on a non-empty
	// framework list, which meant that a repo recon has no framework rules for
	// got no stack doc and no explanation — and the doc's real value (project
	// conventions, testing approach, common patterns) never depended on
	// framework detection in the first place.
	docs = append(docs, buildFrameworkDoc(overview, primaryLang))

	// Build a hotspots paradigm doc if there are high-risk files.
	if len(hotspots) >= 3 {
		doc := buildHotspotsDoc(hotspots)
		docs = append(docs, doc)
	}

	return docs, nil
}

type domainInfo struct {
	name    string
	dirPath string
	files   int
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

// buildFrameworkDoc writes .rivet/context/paradigms/stack.md, whether or not
// recon could name a framework.
//
// Tags come from the framework list and the primary language only, never from
// the dependency list. Tag matching in internal/context scores a substring hit
// in either direction, so a tag per manifest entry would hand this one doc cheap
// partial credit on half the queries in the repo ("auth" landing on `oauthlib`,
// "test" on `@types/node`), and tags are supposed to be a deliberate authoring
// signal rather than a dump. Dependencies go in the body instead, where the
// body signal is IDF-weighted and capped.
func buildFrameworkDoc(ov reconOverview, primaryLang string) scaffoldDoc {
	var b strings.Builder

	names := make([]string, 0, len(ov.Frameworks))
	tags := make([]string, 0, len(ov.Frameworks)+4)
	// These three are what the doc is about no matter what detection returned —
	// including "frameworks", which is the question this doc answers even when
	// the answer is "recon could not tell". Leaving it off made the body's own
	// subject untagged, which the untagged-theme lint rule reports and retrieval
	// pays for.
	tags = append(tags, primaryLang, "stack", "frameworks", "conventions")
	for _, f := range ov.Frameworks {
		names = append(names, f.Name)
		tags = append(tags, strings.ToLower(strings.ReplaceAll(f.Name, " ", "-")))
	}

	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("tags: [%s]\n", strings.Join(dedupeNonEmpty(tags), ", ")))
	b.WriteString("---\n\n")

	b.WriteString("# Stack & Conventions\n\n")
	if primaryLang != "" {
		b.WriteString(fmt.Sprintf("**Language:** %s\n", primaryLang))
	}

	frameworkLine, note := frameworkSummary(ov.Frameworks, ov.FrameworkStatus)
	b.WriteString(fmt.Sprintf("**Frameworks:** %s\n\n", frameworkLine))

	if deps := dependencySection(ov.Dependencies); deps != "" {
		b.WriteString(deps)
	}

	b.WriteString("## Project conventions\n\n")
	b.WriteString("<!-- Describe naming conventions, file organization patterns, etc. -->\n\n")

	b.WriteString("## Testing approach\n\n")
	b.WriteString("<!-- How tests are organized, what test framework is used, fixtures, etc. -->\n\n")

	b.WriteString("## Common patterns\n\n")
	b.WriteString("<!-- Describe patterns used across the codebase (e.g., behaviours, GenServers, contexts) -->\n\n")

	path := filepath.Join(".rivet", "context", "paradigms", "stack.md")
	return scaffoldDoc{path: path, content: b.String(), note: note}
}

// frameworkSummary renders the doc's Frameworks line and, when there is nothing
// to list, the note explaining that to whoever ran the scaffolder.
//
// Every branch says what recon could and could not determine. "None" and
// "recon has no rules here" are different facts, and a reader who cannot tell
// them apart cannot decide whether to go and fill the section in by hand.
func frameworkSummary(frameworks []reconFramework, status string) (line, note string) {
	if len(frameworks) > 0 {
		names := make([]string, 0, len(frameworks))
		for _, f := range frameworks {
			names = append(names, f.Name)
		}
		line = strings.Join(names, ", ")
		if status != "" && status != detectStatusFound {
			// Frameworks were reported alongside a status that doesn't claim
			// they were found — don't present the list as complete.
			line += fmt.Sprintf(" (recon status %q — the list may be incomplete)", status)
			note = fmt.Sprintf("recon listed frameworks with status %q; stack.md flags the list as possibly incomplete", status)
		}
		return line, note
	}

	switch status {
	case detectStatusNoneMatched:
		return "none matched — recon checked this project's languages and nothing proved a framework (no known dependency, config file, or source marker). Name one here if this project uses one.",
			"recon matched no framework for this project — stack.md scaffolded with that section left for you to fill in"
	case detectStatusUnsupported:
		return "undetermined — recon has no framework detector for this project's languages, so it could not tell either way. Fill this in by hand.",
			"recon has no framework detector for this project's languages — stack.md scaffolded with that section left for you to fill in"
	case "":
		// Recon predates the status fields (rivet embeds a pinned version), so
		// an empty list here is genuinely ambiguous and is reported as such.
		return "undetermined — recon reported none, and this recon build does not say whether that means \"none matched\" or \"no detector for these languages\". Fill this in by hand.",
			"recon reported no frameworks and no status for why — stack.md scaffolded with that section left for you to fill in"
	default:
		// A status added after this rivet build. Report it verbatim rather than
		// guessing at what it means; "undetermined" is true of all of them.
		return fmt.Sprintf("undetermined — recon reported none with status %q, which this version of rivet does not recognise. Fill this in by hand.", status),
			fmt.Sprintf("recon reported framework status %q, which this version of rivet does not recognise — stack.md scaffolded with that section left for you to fill in", status)
	}
}

// dependencyDisplayLimit caps the manifest list in the stack doc. The point is
// to give whoever fills the doc in something factual to work from, not to paste
// a lockfile into a context document.
const dependencyDisplayLimit = 10

// dependencySection renders what the manifests declare. This is the list that
// used to be presented as "Frameworks", which is how a Maven project came to
// claim its own build plugins as frameworks it was built on.
func dependencySection(deps []reconDependency) string {
	if len(deps) == 0 {
		return ""
	}

	manifests := make([]string, 0, 4)
	for _, d := range deps {
		if d.Manifest != "" {
			manifests = append(manifests, d.Manifest)
		}
	}
	manifests = dedupeNonEmpty(manifests)

	var b strings.Builder
	b.WriteString("## Declared dependencies\n\n")
	if len(manifests) > 0 {
		b.WriteString(fmt.Sprintf("recon read %d from %s. A manifest entry is a declaration, not proof that the project is built on it — which is why these are listed apart from the frameworks above.\n\n",
			len(deps), strings.Join(manifests, ", ")))
	} else {
		b.WriteString(fmt.Sprintf("recon read %d declared. A manifest entry is a declaration, not proof that the project is built on it — which is why these are listed apart from the frameworks above.\n\n",
			len(deps)))
	}

	limit := len(deps)
	if limit > dependencyDisplayLimit {
		limit = dependencyDisplayLimit
	}
	for _, d := range deps[:limit] {
		if d.Version != "" {
			b.WriteString(fmt.Sprintf("- %s %s\n", d.Name, d.Version))
			continue
		}
		b.WriteString(fmt.Sprintf("- %s\n", d.Name))
	}
	if rest := len(deps) - limit; rest > 0 {
		b.WriteString(fmt.Sprintf("- ... and %d more\n", rest))
	}
	b.WriteString("\n")

	return b.String()
}

// dedupeNonEmpty preserves order, drops blanks and drops repeats. Blanks matter:
// a repo whose language recon could not name used to render `tags: [, react]`,
// and the frontmatter parser reads that empty element as a real tag.
func dedupeNonEmpty(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
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
