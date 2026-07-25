package doctor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/djtouchette/rivet/internal/capabilities"
	"github.com/djtouchette/rivet/internal/config"
	rivetctx "github.com/djtouchette/rivet/internal/context"
	"github.com/djtouchette/rivet/internal/context/semantic"
)

// Status represents the result of a single check.
type Status string

const (
	StatusOK   Status = "ok"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
	StatusSkip Status = "skip"
)

// Check is the result of a single doctor check.
type Check struct {
	Name    string
	Status  Status
	Message string
}

// Result is the full doctor report.
type Result struct {
	Checks []Check
}

// HasFailures returns true if any check failed.
func (r *Result) HasFailures() bool {
	for _, c := range r.Checks {
		if c.Status == StatusFail {
			return true
		}
	}
	return false
}

// Run executes all doctor checks and returns the result.
//
// groups is the set of optional builtin tool groups this project actually gets,
// resolved by the caller. Doctor deliberately does not re-derive it: duplicating
// the detection is how doctor came to report vaulty as "available" while the
// registry withheld every vaulty.* tool.
func Run(groups capabilities.BuiltinGroups) *Result {
	r := &Result{}

	r.checkRivetDir()
	cfg := r.checkConfig()
	r.checkProjectCLI(cfg)
	r.checkCapabilities(cfg)
	r.checkContextDirs()
	r.checkContextFiles()
	r.checkToolGroups(groups)
	r.checkSemanticIndex()

	return r
}

func (r *Result) add(name string, status Status, msg string) {
	r.Checks = append(r.Checks, Check{Name: name, Status: status, Message: msg})
}

func (r *Result) checkRivetDir() {
	info, err := os.Stat(".rivet")
	if err != nil {
		r.add(".rivet/ directory", StatusFail, "not found — run 'rivet init'")
		return
	}
	if !info.IsDir() {
		r.add(".rivet/ directory", StatusFail, ".rivet exists but is not a directory")
		return
	}
	r.add(".rivet/ directory", StatusOK, "exists")
}

func (r *Result) checkConfig() *config.Config {
	cfg, err := config.LoadOrDefault("")
	if err != nil {
		r.add("config.yaml", StatusFail, fmt.Sprintf("parse error: %v", err))
		return nil
	}

	if _, err := os.Stat(cfg.Path()); err != nil {
		r.add("config.yaml", StatusWarn, "no config file found, using defaults")
	} else {
		r.add("config.yaml", StatusOK, cfg.Path())
	}
	return cfg
}

func (r *Result) checkProjectCLI(cfg *config.Config) {
	if cfg == nil {
		r.add("project CLI", StatusSkip, "config not loaded")
		return
	}

	cmd := cfg.ProjectCLI.Command
	if cmd == "" {
		r.add("project CLI", StatusSkip, "not configured")
		return
	}

	// Check if command exists on PATH or as a file.
	if _, err := exec.LookPath(cmd); err != nil {
		if _, err := os.Stat(cmd); err != nil {
			r.add("project CLI", StatusFail, fmt.Sprintf("%q not found", cmd))
			return
		}
	}
	r.add("project CLI", StatusOK, cmd)
}

func (r *Result) checkCapabilities(cfg *config.Config) {
	if cfg == nil {
		r.add("capabilities", StatusSkip, "config not loaded")
		return
	}

	if len(cfg.Capabilities) == 0 {
		r.add("capabilities", StatusOK, "none configured (builtins only)")
		return
	}

	var warnings []string
	valid := 0
	for _, def := range cfg.Capabilities {
		if def.Name == "" {
			warnings = append(warnings, "capability with empty name")
			continue
		}
		if !capabilities.ValidCapabilityKind(def.Kind) {
			warnings = append(warnings, fmt.Sprintf("%q: invalid kind %q", def.Name, def.Kind))
			continue
		}
		if !capabilities.ValidSafetyLevel(def.Safety) {
			warnings = append(warnings, fmt.Sprintf("%q: invalid safety %q", def.Name, def.Safety))
			continue
		}
		if len(def.Command) == 0 {
			warnings = append(warnings, fmt.Sprintf("%q: no command defined", def.Name))
			continue
		}
		valid++
	}

	if len(warnings) > 0 {
		r.add("capabilities", StatusWarn, fmt.Sprintf("%d valid, %d issues: %s",
			valid, len(warnings), strings.Join(warnings, "; ")))
	} else {
		r.add("capabilities", StatusOK, fmt.Sprintf("%d configured", valid))
	}
}

func (r *Result) checkContextDirs() {
	base := filepath.Join(".rivet", "context")
	if _, err := os.Stat(base); err != nil {
		r.add("context directory", StatusWarn, ".rivet/context/ not found")
		return
	}

	var missing []string
	for _, sub := range []string{"domains", "modules", "paradigms"} {
		if _, err := os.Stat(filepath.Join(base, sub)); err != nil {
			missing = append(missing, sub)
		}
	}

	if len(missing) > 0 {
		r.add("context directory", StatusWarn, fmt.Sprintf("missing subdirectories: %s", strings.Join(missing, ", ")))
	} else {
		r.add("context directory", StatusOK, "all subdirectories present")
	}
}

func (r *Result) checkContextFiles() {
	docs, err := rivetctx.Load(".rivet/context")
	if err != nil {
		r.add("context documents", StatusWarn, fmt.Sprintf("load error: %v", err))
		return
	}

	if len(docs) == 0 {
		r.add("context documents", StatusOK, "none found")
		return
	}

	var warnings []string
	for _, doc := range docs {
		if strings.TrimSpace(doc.Body) == "" {
			warnings = append(warnings, fmt.Sprintf("%s/%s: empty", doc.Kind, doc.Name))
		}
	}

	if len(warnings) > 0 {
		r.add("context documents", StatusWarn, fmt.Sprintf("%d loaded, %d issues: %s",
			len(docs), len(warnings), strings.Join(warnings, "; ")))
	} else {
		r.add("context documents", StatusOK, fmt.Sprintf("%d loaded", len(docs)))
	}
}

// checkToolGroups reports which optional builtin groups this project exposes
// over MCP, and how to turn on the ones it doesn't.
//
// It used to report vaulty as "available (embedded + PATH)", which was true of
// the binary and irrelevant to the question anyone is asking. The tools are
// registered only when a vault exists, so doctor could cheerfully say
// "available" for six tools Claude could not see. What matters is whether the
// tools are there, and if not, what to do about it.
func (r *Result) checkToolGroups(groups capabilities.BuiltinGroups) {
	// recon.* and witness.* need no configuration and are never gated, so
	// there's nothing here that could go wrong for them.
	if groups.Schema {
		r.add("schema tools", StatusOK, "registered — schema: section is configured")
	} else {
		r.add("schema tools", StatusSkip,
			"not registered — add a database, migrations dir or code_scan root under schema: in .rivet/config.yaml (or force with tools.schema: true)")
	}

	if groups.Vaulty {
		r.add("vaulty tools", StatusOK, "registered — a vault is configured")
	} else {
		r.add("vaulty tools", StatusSkip,
			"not registered — no vault found; run 'rivet vaulty init' to create one (or force with tools.vaulty: true)")
	}
}

// checkSemanticIndex reports an embeddings index that is present but inert.
//
// Semantic scoring degrades to lexical silently when no backend is configured,
// which is deliberate — a missing embedder should never break retrieval. But it
// makes "semantic search is off" and "semantic search is broken" look identical,
// and a project that has run `rivet context index` has already paid to build the
// thing. Found in the wild: a 1.2 MB committed vectors.bin doing nothing,
// because the environment that built it was not the environment querying it.
//
// Reading RIVET_EMBED_BACKEND is not enough to answer the question. The first
// version of this check reported OK — "index present and ollama backend
// configured" — with Ollama unreachable and the model not pulled, because the
// env var was set. A health check that only reads its own configuration cannot
// distinguish working from broken, which is the entire failure it exists to
// catch. So this one contacts the backend and compares the index's model
// against the configured one.
func (r *Result) checkSemanticIndex() {
	vectors := filepath.Join(semantic.DefaultStoreDir, "vectors.bin")
	info, err := os.Stat(vectors)
	indexPresent := err == nil && info.Size() > 0

	cfg := semantic.ConfigFromEnv()

	if cfg.Backend == "" {
		if indexPresent {
			r.add("semantic search", StatusWarn,
				fmt.Sprintf("index built (%s) but RIVET_EMBED_BACKEND is unset — retrieval is lexical-only and the index is unused. Set the backend that built it, e.g. RIVET_EMBED_BACKEND=ollama", vectors))
			return
		}
		r.add("semantic search", StatusSkip, "not configured — retrieval is lexical only (see 'rivet runbook find \"set up embeddings\"')")
		return
	}

	emb, err := semantic.New(cfg)
	if err != nil || emb == nil {
		r.add("semantic search", StatusFail,
			fmt.Sprintf("RIVET_EMBED_BACKEND=%s could not be constructed: %v — retrieval silently falls back to lexical", cfg.Backend, err))
		return
	}

	// Probe. A backend that cannot embed one short string cannot serve a query,
	// and the failure is otherwise invisible: recommend exits 0 with lexical
	// results that look exactly like a correctly-lexical run.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := emb.Embed(ctx, []string{"rivet doctor probe"}); err != nil {
		r.add("semantic search", StatusFail, fmt.Sprintf(
			"backend %s (%s) is configured but not answering: %v — retrieval is silently falling back to lexical. For ollama, check the daemon is running and the model is pulled ('ollama pull %s')",
			cfg.Backend, emb.ID(), err, ollamaModelHint(cfg.Model)))
		return
	}

	if !indexPresent {
		r.add("semantic search", StatusWarn,
			"backend "+cfg.Backend+" is reachable but there is no index — run 'rivet context index' so queries do not embed the corpus on demand")
		return
	}

	// The index is on disk and the backend works — but they may not be the same
	// model. The store's key embeds the base URL as well as the model name, so
	// pointing at a different host silently invalidates every cached vector.
	store, err := semantic.OpenStore(semantic.DefaultStoreDir, emb.ID(), emb.Dim())
	if err != nil {
		r.add("semantic search", StatusWarn, fmt.Sprintf("backend %s works but the index could not be read: %v", cfg.Backend, err))
		return
	}
	if was, discarded := store.Discarded(); discarded {
		r.add("semantic search", StatusWarn, fmt.Sprintf(
			"index at %s was built by %q but the configured backend is %q — the cached vectors are unusable and every query re-embeds on demand. Re-run 'rivet context index', or point RIVET_EMBED_MODEL/RIVET_EMBED_BASE_URL back at the model that built it",
			vectors, was, emb.ID()))
		return
	}

	r.add("semantic search", StatusOK, fmt.Sprintf("%s reachable, %d cached vectors match %s", cfg.Backend, store.Len(), emb.ID()))
}

// ollamaModelHint names the model an ollama user needs to pull, filling in the
// default that newHTTPEmbedder would have applied when none was configured.
func ollamaModelHint(model string) string {
	if model == "" {
		return "nomic-embed-text"
	}
	return model
}
