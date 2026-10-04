package doctor

import (
	"encoding/json"
	"github.com/djtouchette/rivet/internal/capabilities"
	"github.com/djtouchette/rivet/internal/context/semantic"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRun_NoRivetDir(t *testing.T) {
	tmp := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(tmp)
	defer os.Chdir(origDir)

	t.Setenv(semantic.EnvBackend, "")
	result := Run(capabilities.BuiltinGroups{})

	if !result.HasFailures() {
		t.Fatal("expected failures when .rivet/ does not exist")
	}

	first := result.Checks[0]
	if first.Status != StatusFail {
		t.Errorf("expected first check to fail, got %s", first.Status)
	}
}

func TestRun_ValidSetup(t *testing.T) {
	tmp := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(tmp)
	defer os.Chdir(origDir)

	// Create minimal valid .rivet/ structure.
	for _, d := range []string{
		".rivet",
		".rivet/context/domains",
		".rivet/context/modules",
		".rivet/context/paradigms",
	} {
		os.MkdirAll(d, 0755)
	}

	configYAML := []byte("capabilities: []\n")
	os.WriteFile(filepath.Join(".rivet", "config.yaml"), configYAML, 0644)

	// Add a context file.
	os.WriteFile(filepath.Join(".rivet", "context", "domains", "billing.md"),
		[]byte("# Billing\n\nHandles invoices."), 0644)

	t.Setenv(semantic.EnvBackend, "")
	result := Run(capabilities.BuiltinGroups{})

	if result.HasFailures() {
		for _, c := range result.Checks {
			if c.Status == StatusFail {
				t.Errorf("unexpected failure: %s — %s", c.Name, c.Message)
			}
		}
	}
}

func TestRun_BadConfig(t *testing.T) {
	tmp := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(tmp)
	defer os.Chdir(origDir)

	os.MkdirAll(".rivet", 0755)
	os.WriteFile(filepath.Join(".rivet", "config.yaml"), []byte("{{bad yaml"), 0644)

	t.Setenv(semantic.EnvBackend, "")
	result := Run(capabilities.BuiltinGroups{})

	if !result.HasFailures() {
		t.Fatal("expected failure for bad config")
	}
}

func TestRun_InvalidCapability(t *testing.T) {
	tmp := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(tmp)
	defer os.Chdir(origDir)

	os.MkdirAll(".rivet/context/domains", 0755)
	os.MkdirAll(".rivet/context/modules", 0755)
	os.MkdirAll(".rivet/context/paradigms", 0755)

	configYAML := []byte(`capabilities:
  - name: "bad.cap"
    kind: "invalid_kind"
    safety: "safe"
    command: ["echo"]
`)
	os.WriteFile(filepath.Join(".rivet", "config.yaml"), configYAML, 0644)

	t.Setenv(semantic.EnvBackend, "")
	result := Run(capabilities.BuiltinGroups{})

	// Should have a warning on capabilities, not a hard fail.
	for _, c := range result.Checks {
		if c.Name == "capabilities" && c.Status != StatusWarn {
			t.Errorf("expected warn for invalid capability, got %s: %s", c.Status, c.Message)
		}
	}
}

func TestHasFailures(t *testing.T) {
	r := &Result{
		Checks: []Check{
			{Status: StatusOK},
			{Status: StatusWarn},
		},
	}
	if r.HasFailures() {
		t.Error("should not have failures with only OK and WARN")
	}

	r.Checks = append(r.Checks, Check{Status: StatusFail})
	if !r.HasFailures() {
		t.Error("should have failures with a FAIL check")
	}
}

// Doctor used to report vaulty as "available (embedded + PATH)" — true of the
// binary, and irrelevant. The tools are registered only when a vault exists, so
// it could say "available" for six tools Claude could not see. The check now
// reports registration, and says how to enable what isn't registered.
func TestToolGroupsReportRegistrationNotAvailability(t *testing.T) {
	tests := []struct {
		name       string
		groups     capabilities.BuiltinGroups
		check      string
		wantStatus Status
		wantHint   string // substring the message must carry
	}{
		{"schema off", capabilities.BuiltinGroups{}, "schema tools", StatusSkip, "tools.schema: true"},
		// "schema on" is covered by TestSchemaStatusNamesWhatCanAnswer, which
		// has to write a config: what a registered schema group can actually do
		// depends on which sources that config names, so asserting a status
		// here without one would only pin the no-sources case.
		{"vaulty off", capabilities.BuiltinGroups{}, "vaulty tools", StatusSkip, "rivet vaulty init"},
		{"vaulty on", capabilities.BuiltinGroups{Vaulty: true}, "vaulty tools", StatusOK, "registered"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Result{}
			r.checkToolGroups(tt.groups)

			var got *Check
			for i := range r.Checks {
				if r.Checks[i].Name == tt.check {
					got = &r.Checks[i]
				}
			}
			if got == nil {
				t.Fatalf("no %q check emitted", tt.check)
			}
			if got.Status != tt.wantStatus {
				t.Errorf("status = %s, want %s (%s)", got.Status, tt.wantStatus, got.Message)
			}
			if !strings.Contains(got.Message, tt.wantHint) {
				t.Errorf("message %q missing %q", got.Message, tt.wantHint)
			}
		})
	}
}

// Registering the schema tools and being able to answer with them are separate
// facts. A migrations dir alone registers all six and makes exactly one work,
// so reporting a flat OK tells the reader the opposite of what is true.
func TestSchemaStatusNamesWhatCanAnswer(t *testing.T) {
	tests := []struct {
		name       string
		configYAML string
		wantStatus Status
		wantHints  []string
	}{
		{
			name: "live database",
			configYAML: `schema:
  databases:
    - name: app
      engine: postgres
      dsn: postgres://localhost/app
`,
			wantStatus: StatusOK,
			wantHints:  []string{"registered", "database"},
		},
		{
			name: "migrations only",
			configYAML: `schema:
  migrations:
    dir: ./db/migrations
`,
			wantStatus: StatusWarn,
			wantHints:  []string{"migrations", "schema.databases"},
		},
		{
			name: "code scan only",
			configYAML: `schema:
  code_scan:
    roots: [./src]
`,
			wantStatus: StatusWarn,
			wantHints:  []string{"code_scan", "schema.databases"},
		},
		{
			name:       "section with no sources",
			configYAML: "schema: {}\n",
			wantStatus: StatusWarn,
			wantHints:  []string{"no database, migrations dir or code_scan root"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			if err := os.MkdirAll(".rivet", 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(".rivet", "config.yaml"), []byte(tt.configYAML), 0644); err != nil {
				t.Fatal(err)
			}

			r := &Result{}
			r.checkToolGroups(capabilities.BuiltinGroups{Schema: true})

			var got *Check
			for i := range r.Checks {
				if r.Checks[i].Name == "schema tools" {
					got = &r.Checks[i]
				}
			}
			if got == nil {
				t.Fatal("no schema tools check emitted")
			}
			if got.Status != tt.wantStatus {
				t.Errorf("status = %s, want %s (%s)", got.Status, tt.wantStatus, got.Message)
			}
			for _, hint := range tt.wantHints {
				if !strings.Contains(got.Message, hint) {
					t.Errorf("message %q missing %q", got.Message, hint)
				}
			}
		})
	}
}

// An ungated group must never be reported as missing — recon and witness need
// no configuration, so there is nothing to warn about.
func TestToolGroupsSaysNothingAboutUngatedTools(t *testing.T) {
	r := &Result{}
	r.checkToolGroups(capabilities.BuiltinGroups{})

	for _, c := range r.Checks {
		if strings.Contains(c.Name, "recon") || strings.Contains(c.Name, "witness") {
			t.Errorf("unexpected check for an ungated group: %s", c.Name)
		}
	}
}

// A built index with no backend configured is inert, and silently so: semantic
// scoring degrades to lexical by design, which makes "off" and "broken"
// indistinguishable. Found in the wild as a committed vectors.bin doing nothing.
func TestSemanticIndexStates(t *testing.T) {
	// These subtests change cwd and environment: keep them sequential. t.Chdir
	// and t.Setenv restore all process state, including a developer's settings.
	tests := []struct {
		name, backend, index, response string
		wantStatus                     Status
		wantHints                      []string
	}{
		{"index but no backend", "", "match", "ok", StatusWarn, []string{"RIVET_EMBED_BACKEND is unset", "lexical-only"}},
		{"index and backend", "ollama", "match", "ok", StatusOK, []string{"ollama reachable", "1 cached vectors match"}},
		{"backend but no index", "ollama", "", "ok", StatusWarn, []string{"reachable but there is no index", "rivet context index"}},
		{"neither", "", "", "ok", StatusSkip, []string{"lexical only"}},
		{"different model", "ollama", "model", "ok", StatusWarn, []string{"cached vectors are unusable", "RIVET_EMBED_MODEL/RIVET_EMBED_BASE_URL"}},
		{"different host", "ollama", "host", "ok", StatusWarn, []string{"cached vectors are unusable", "RIVET_EMBED_MODEL/RIVET_EMBED_BASE_URL"}},
		{"different dimension", "ollama", "dimension", "ok", StatusWarn, []string{"cached vectors are unusable", "Re-run 'rivet context index'"}},
		{"corrupt index", "ollama", "corrupt", "ok", StatusWarn, []string{"works but the index could not be read"}},
		{"unavailable with index", "ollama", "match", "unavailable", StatusFail, []string{"not answering", "falling back to lexical", "ollama pull fixture-model"}},
		{"unavailable without index", "ollama", "", "unavailable", StatusFail, []string{"not answering", "falling back to lexical"}},
		{"model not pulled", "ollama", "match", "missing model", StatusFail, []string{"not answering", "ollama pull fixture-model"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var req struct{ Model, Prompt string }
				if r.Method != http.MethodPost || r.URL.Path != "/api/embeddings" || json.NewDecoder(r.Body).Decode(&req) != nil || req.Model != "fixture-model" || req.Prompt != "rivet doctor probe" {
					t.Errorf("invalid Ollama probe: %s %s %+v", r.Method, r.URL.Path, req)
					http.Error(w, "invalid probe", http.StatusBadRequest)
					return
				}
				if r.Header.Get("Authorization") != "" {
					t.Error("fixture received unexpected authorization")
				}
				w.Header().Set("Content-Type", "application/json")
				if tt.response == "missing model" {
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"error":"model fixture-model not found"}`))
					return
				}
				_, _ = w.Write([]byte(`{"embedding":[1,0,0]}`))
			}))
			t.Cleanup(srv.Close)
			if tt.response == "unavailable" {
				srv.Close()
			}
			t.Setenv(semantic.EnvBackend, tt.backend)
			t.Setenv(semantic.EnvBaseURL, srv.URL)
			t.Setenv(semantic.EnvModel, "fixture-model")
			t.Setenv(semantic.EnvAPIKey, "")

			if tt.index != "" {
				cfg := semantic.Config{Backend: "ollama", BaseURL: srv.URL, Model: "fixture-model"}
				if tt.index == "model" {
					cfg.Model = "old-model"
				}
				if tt.index == "host" {
					cfg.BaseURL = "http://old-host.invalid"
				}
				emb, err := semantic.New(cfg)
				if err != nil {
					t.Fatal(err)
				}
				vec := semantic.Vector{1, 0, 0}
				if tt.index == "dimension" {
					vec = semantic.Vector{1, 0}
				}
				store, err := semantic.OpenStore(semantic.DefaultStoreDir, emb.ID(), len(vec))
				if err != nil {
					t.Fatal(err)
				}
				if err := store.Put(semantic.HashText(emb.ID(), "fixture document"), vec); err != nil {
					t.Fatal(err)
				}
				if err := store.Save(); err != nil {
					t.Fatal(err)
				}
				if tt.index == "corrupt" {
					if err := os.WriteFile(filepath.Join(semantic.DefaultStoreDir, "manifest.json"), []byte("invalid"), 0644); err != nil {
						t.Fatal(err)
					}
				}
			}
			r := &Result{}
			r.checkSemanticIndex()
			if len(r.Checks) != 1 {
				t.Fatalf("expected one check, got %d", len(r.Checks))
			}
			got := r.Checks[0]
			if got.Status != tt.wantStatus {
				t.Errorf("status = %s, want %s (%s)", got.Status, tt.wantStatus, got.Message)
			}
			for _, hint := range tt.wantHints {
				if !strings.Contains(got.Message, hint) {
					t.Errorf("message %q missing %q", got.Message, hint)
				}
			}
			wantCalls := int32(1)
			if tt.backend == "" || tt.response == "unavailable" {
				wantCalls = 0
			}
			if calls.Load() != wantCalls {
				t.Errorf("probe requests = %d, want %d", calls.Load(), wantCalls)
			}
		})
	}
}

// An empty vectors.bin is not an index. Treating it as one would report
// "working" for a build that produced nothing.
func TestSemanticIndexIgnoresEmptyVectorFile(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("RIVET_EMBED_BACKEND", "")
	if err := os.MkdirAll(".rivet/embeddings", 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(".rivet/embeddings/vectors.bin", nil, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	r := &Result{}
	r.checkSemanticIndex()
	if r.Checks[0].Status != StatusSkip {
		t.Errorf("an empty index file should not count as an index: %+v", r.Checks[0])
	}
}
