package cli

import (
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
