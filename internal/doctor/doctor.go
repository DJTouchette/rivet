package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/djtouchette/rivet/internal/capabilities"
	"github.com/djtouchette/rivet/internal/config"
	rivetctx "github.com/djtouchette/rivet/internal/context"
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
func Run() *Result {
	r := &Result{}

	r.checkRivetDir()
	cfg := r.checkConfig()
	r.checkProjectCLI(cfg)
	r.checkCapabilities(cfg)
	r.checkContextDirs()
	r.checkContextFiles()
	r.checkVaulty()

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

func (r *Result) checkVaulty() {
	// Vaulty is embedded, so it's always available as an in-process runner.
	// But check if the vaulty binary is also on PATH for standalone use.
	if _, err := exec.LookPath("vaulty"); err != nil {
		r.add("vaulty", StatusOK, "available (embedded only, not on PATH)")
	} else {
		r.add("vaulty", StatusOK, "available (embedded + PATH)")
	}
}
