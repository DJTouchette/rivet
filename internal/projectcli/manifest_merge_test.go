package projectcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// testManifest is a reader's view of the file, kept local so the merge is
// verified against what YAML actually says rather than against the node tree it
// just built.
type testManifest struct {
	CLI          string `yaml:"cli"`
	Capabilities []struct {
		Name        string   `yaml:"name"`
		Description string   `yaml:"description"`
		Command     []string `yaml:"command"`
		Output      string   `yaml:"output"`
		Safety      string   `yaml:"safety"`
		Params      []struct {
			Name string `yaml:"name"`
			Type string `yaml:"type"`
		} `yaml:"params"`
	} `yaml:"capabilities"`
}

func readManifest(t *testing.T, path string) testManifest {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading manifest: %v", err)
	}
	var m testManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		t.Fatalf("parsing manifest %s: %v\n%s", path, err, data)
	}
	return m
}

func findCap(t *testing.T, m testManifest, name string) int {
	t.Helper()
	for i, c := range m.Capabilities {
		if c.Name == name {
			return i
		}
	}
	t.Fatalf("capability %q not in manifest %+v", name, m)
	return -1
}

func writeManifest(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "capabilities.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A project with no manifest yet has to get one, and the entries have to be
// shaped like the ones the starter manifests write.
func TestMergeManifestCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".rivet", "capabilities.yaml")

	res, err := MergeManifest(path, "./mycli", []DiscoveredCapability{
		{Name: "app.status", Description: "Show status", Command: []string{"query", "status"}, Output: "json", Safety: "safe"},
	}, false)
	if err != nil {
		t.Fatalf("MergeManifest: %v", err)
	}
	if res.Count(StatusAdded) != 1 {
		t.Errorf("added = %d, want 1", res.Count(StatusAdded))
	}

	m := readManifest(t, path)
	if m.CLI != "./mycli" {
		t.Errorf("cli = %q, want ./mycli", m.CLI)
	}
	c := m.Capabilities[findCap(t, m, "app.status")]
	if c.Description != "Show status" || c.Output != "json" || c.Safety != "safe" {
		t.Errorf("unexpected entry: %+v", c)
	}
	if len(c.Command) != 2 || c.Command[0] != "query" {
		t.Errorf("command = %v, want [query status]", c.Command)
	}
}

// register-cli merged by name and skipped every existing entry, so editing a
// description or a command in the discover output and re-running did nothing:
// the discover contract was write-once. Discovery is the only thing that knows
// the CLI's real subcommands, so those fields refresh.
func TestMergeManifestRefreshesDiscoveredFields(t *testing.T) {
	path := writeManifest(t, `cli: ./mycli
capabilities:
  - name: app.status
    description: Old description
    command: [status]
    output: text
    safety: safe
`)

	res, err := MergeManifest(path, "./mycli", []DiscoveredCapability{
		{Name: "app.status", Description: "New description", Command: []string{"query", "status"}, Output: "json", Safety: "safe"},
	}, false)
	if err != nil {
		t.Fatalf("MergeManifest: %v", err)
	}
	if res.Caps[0].Status != StatusUpdated {
		t.Errorf("status = %q, want updated", res.Caps[0].Status)
	}

	c := readManifest(t, path).Capabilities[0]
	if c.Description != "New description" {
		t.Errorf("description = %q, want the discovered one", c.Description)
	}
	if len(c.Command) != 2 || c.Command[1] != "status" {
		t.Errorf("command = %v, want [query status]", c.Command)
	}
	if c.Output != "json" {
		t.Errorf("output = %q, want json", c.Output)
	}

	// The report has to name what moved, or a user re-running register-cli has
	// no way to tell an edited file from an untouched one.
	joined := strings.Join(res.Summary(), "\n")
	for _, want := range []string{"description", "command", "output", "app.status"} {
		if !strings.Contains(joined, want) {
			t.Errorf("summary does not mention %q:\n%s", want, joined)
		}
	}
}

// Safety is the axis discovery deliberately fails closed on: an unlabelled
// command is reported as "dangerous". Tightening therefore applies on its own,
// while relaxing a level the manifest already carries — which may be a
// deliberate hand correction — needs --force and is reported either way.
func TestMergeManifestSafetyPolicy(t *testing.T) {
	tests := []struct {
		name       string
		existing   string
		discovered string
		force      bool
		wantSafety string
		wantStatus MergeStatus
	}{
		{"identical level is a no-op", "safe", "safe", false, "safe", StatusUnchanged},
		{"tightening applies", "safe", "dangerous", false, "dangerous", StatusUpdated},
		{"tightening one step applies", "safe", "guarded", false, "guarded", StatusUpdated},
		{"relaxing is refused", "dangerous", "safe", false, "dangerous", StatusKept},
		{"relaxing one step is refused", "dangerous", "guarded", false, "dangerous", StatusKept},
		{"relaxing with --force applies", "dangerous", "safe", true, "safe", StatusUpdated},
		{"an unknown manifest level has nothing to protect", "bogus", "guarded", false, "guarded", StatusUpdated},
		{"an unknown discovered level never overrides a valid one", "safe", "bogus", false, "safe", StatusKept},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeManifest(t, `cli: ./mycli
capabilities:
  - name: app.reset
    description: Reset the database
    command: [task, reset]
    output: json
    safety: `+tt.existing+"\n")

			res, err := MergeManifest(path, "./mycli", []DiscoveredCapability{{
				Name:        "app.reset",
				Description: "Reset the database",
				Command:     []string{"task", "reset"},
				Output:      "json",
				Safety:      tt.discovered,
			}}, tt.force)
			if err != nil {
				t.Fatalf("MergeManifest: %v", err)
			}

			if got := readManifest(t, path).Capabilities[0].Safety; got != tt.wantSafety {
				t.Errorf("safety = %q, want %q", got, tt.wantSafety)
			}
			if res.Caps[0].Status != tt.wantStatus {
				t.Errorf("status = %q, want %q", res.Caps[0].Status, tt.wantStatus)
			}
			// A refusal that isn't reported is indistinguishable from a merge
			// that did nothing, and leaves no hint that --force exists.
			if tt.wantStatus == StatusKept {
				joined := strings.Join(res.Summary(), "\n")
				if !strings.Contains(joined, "--force") || !strings.Contains(joined, tt.discovered) {
					t.Errorf("summary does not explain the refusal:\n%s", joined)
				}
			}
		})
	}
}

// .rivet/capabilities.yaml is a file rivet tells users to edit — it is where
// typed params get added, and the starter manifest is mostly comments
// explaining how. A struct round-trip erased all of it on every register-cli.
func TestMergeManifestPreservesHandEdits(t *testing.T) {
	path := writeManifest(t, `# Project CLI capabilities exposed to Claude Code.
# Param types: string, number, integer, boolean

cli: ./mycli

capabilities:
  # Seeding is guarded on purpose — see RUNBOOK.md.
  - name: app.seed
    description: Seed development data
    command: [task, seed]
    output: json
    safety: guarded
    params:
      - name: count
        type: integer
        description: Number of records to seed

  # Not reported by discovery; added by hand.
  - name: app.manual
    description: Something discovery knows nothing about
    command: [task, manual]
    output: json
    safety: safe
`)

	res, err := MergeManifest(path, "./mycli", []DiscoveredCapability{
		{Name: "app.seed", Description: "Seed development data", Command: []string{"task", "seed"}, Output: "json", Safety: "guarded"},
		{Name: "app.new", Description: "Brand new", Command: []string{"query", "new"}, Output: "json", Safety: "safe"},
	}, false)
	if err != nil {
		t.Fatalf("MergeManifest: %v", err)
	}
	if res.Count(StatusUnchanged) != 1 || res.Count(StatusAdded) != 1 {
		t.Errorf("counts = %d unchanged / %d added, want 1 / 1", res.Count(StatusUnchanged), res.Count(StatusAdded))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"# Param types: string, number, integer, boolean",
		"# Seeding is guarded on purpose — see RUNBOOK.md.",
		"# Not reported by discovery; added by hand.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("comment %q lost:\n%s", want, text)
		}
	}

	m := readManifest(t, path)
	seed := m.Capabilities[findCap(t, m, "app.seed")]
	if len(seed.Params) != 1 || seed.Params[0].Name != "count" || seed.Params[0].Type != "integer" {
		t.Errorf("typed params lost: %+v", seed.Params)
	}
	// A capability discovery has never heard of is still someone's work.
	findCap(t, m, "app.manual")
	findCap(t, m, "app.new")
}

// Re-running with nothing changed must leave the file byte-identical, or every
// register-cli shows up as a diff and nobody trusts the ones that matter.
func TestMergeManifestIsIdempotent(t *testing.T) {
	path := writeManifest(t, `# comment
cli: ./mycli
capabilities:
  - name: app.status
    description: Show status
    command: [query, status]
    output: json
    safety: safe
`)
	caps := []DiscoveredCapability{
		{Name: "app.status", Description: "Show status", Command: []string{"query", "status"}, Output: "json", Safety: "safe"},
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	res, err := MergeManifest(path, "./mycli", caps, false)
	if err != nil {
		t.Fatalf("MergeManifest: %v", err)
	}
	if res.Caps[0].Status != StatusUnchanged {
		t.Errorf("status = %q, want unchanged", res.Caps[0].Status)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("a no-op merge rewrote the file:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// The cli: key is the one thing register-cli always owns.
func TestMergeManifestUpdatesCLI(t *testing.T) {
	path := writeManifest(t, "cli: ./old\ncapabilities: []\n")

	if _, err := MergeManifest(path, "mix", nil, false); err != nil {
		t.Fatalf("MergeManifest: %v", err)
	}
	if got := readManifest(t, path).CLI; got != "mix" {
		t.Errorf("cli = %q, want mix", got)
	}
}

// A manifest that doesn't parse is still a file full of someone's work. The old
// path would have rendered a fresh document straight over it.
func TestMergeManifestRefusesUnparseableFile(t *testing.T) {
	broken := "cli: ./mycli\ncapabilities:\n  - name: app.status\n   bad indent: [\n"
	path := writeManifest(t, broken)

	if _, err := MergeManifest(path, "./mycli", []DiscoveredCapability{{Name: "app.status"}}, false); err == nil {
		t.Fatal("expected an error for an unparseable manifest")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != broken {
		t.Errorf("the unparseable file was modified:\n%s", got)
	}
}
