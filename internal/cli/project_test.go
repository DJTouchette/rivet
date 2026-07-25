package cli

import (
	"io"
	"os"
	"strings"
	"testing"
)

// init-cli's closing instructions are the only place most people learn how to
// register what was just scaffolded. The Elixir path never mentioned
// register-cli at all — it wrote the manifest itself and left the scaffolded
// rivet_discover task unreachable — and a bare `register-cli mix` cannot find a
// namespaced Mix task, so the discover spelling has to be part of the line.
func TestScaffoldNextSteps(t *testing.T) {
	tests := []struct {
		name  string
		steps []string
		want  []string
	}{
		{
			name:  "go",
			steps: goNextSteps("tools/projectcli", "projectcli", ".rivet/capabilities.yaml"),
			want: []string{
				"cd tools/projectcli && go mod tidy && make build",
				"rivet project register-cli ./tools/projectcli/projectcli",
				".rivet/capabilities.yaml",
				"rivet sync",
			},
		},
		{
			name:  "elixir",
			steps: elixirNextSteps("project", ".rivet/capabilities.yaml"),
			want: []string{
				"mix project.query.status",
				"rivet project register-cli mix --discover project.rivet_discover",
				".rivet/capabilities.yaml",
				"rivet sync",
			},
		},
		{
			name:  "elixir with a custom namespace",
			steps: elixirNextSteps("acme", ".rivet/capabilities.yaml"),
			want:  []string{"rivet project register-cli mix --discover acme.rivet_discover"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			joined := strings.Join(tt.steps, "\n")
			for _, want := range tt.want {
				if !strings.Contains(joined, want) {
					t.Errorf("next steps do not mention %q:\n%s", want, joined)
				}
			}
		})
	}
}

// detectProjectLanguage used to fall back to "go", so a repo it could not
// identify was indistinguishable from a real Go repo. An honest "" lets
// init-cli refuse instead of scaffolding the wrong thing.
func TestDetectProjectLanguage(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		want  string
	}{
		{"elixir", []string{"mix.exs"}, "elixir"},
		{"go", []string{"go.mod"}, "go"},
		{"node", []string{"package.json"}, "node"},
		{"rust", []string{"Cargo.toml"}, "rust"},
		{"python via pyproject", []string{"pyproject.toml"}, "python"},
		{"python via requirements", []string{"requirements.txt"}, "python"},
		{"ruby", []string{"Gemfile"}, "ruby"},
		{"nothing recognisable", nil, ""},
		// Elixir is checked before Go so an Elixir project with a stray go.mod
		// (a tool directory, say) still scaffolds Mix tasks.
		{"elixir wins over go", []string{"mix.exs", "go.mod"}, "elixir"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			for _, f := range tt.files {
				if err := os.WriteFile(f, []byte("x"), 0644); err != nil {
					t.Fatalf("write %s: %v", f, err)
				}
			}
			if got := detectProjectLanguage(); got != tt.want {
				t.Errorf("detectProjectLanguage() = %q, want %q", got, tt.want)
			}
		})
	}
}

// A language with no scaffold must be refused by name, not silently handed the
// Go scaffold — a Python repo acquiring a cobra module and a go.mod is a
// genuinely confusing outcome.
func TestInitCLIRefusesUnscaffoldedLanguage(t *testing.T) {
	tests := []struct {
		name      string
		setup     []string
		args      []string
		wantParts []string
	}{
		{
			name:      "detected python",
			setup:     []string{"pyproject.toml"},
			args:      []string{"init-cli"},
			wantParts: []string{"python", "--lang go", "discover protocol"},
		},
		{
			name:      "explicit unsupported lang",
			setup:     nil,
			args:      []string{"init-cli", "--lang", "haskell"},
			wantParts: []string{"haskell", "go or elixir"},
		},
		{
			name:      "nothing detectable",
			setup:     nil,
			args:      []string{"init-cli"},
			wantParts: []string{"could not detect", "--lang go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			if err := os.MkdirAll(".rivet", 0755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			for _, f := range tt.setup {
				if err := os.WriteFile(f, []byte("x"), 0644); err != nil {
					t.Fatalf("write %s: %v", f, err)
				}
			}

			cmd := newProjectCmd()
			cmd.SetArgs(tt.args)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true

			err := cmd.Execute()
			if err == nil {
				t.Fatal("expected a refusal, got success")
			}
			for _, want := range tt.wantParts {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q missing %q", err.Error(), want)
				}
			}
			// Nothing should have been scaffolded.
			if _, statErr := os.Stat("tools"); !os.IsNotExist(statErr) {
				t.Error("a refused init-cli must not leave a scaffold behind")
			}
		})
	}
}
