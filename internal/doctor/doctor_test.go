package doctor

import (
	"github.com/djtouchette/rivet/internal/capabilities"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun_NoRivetDir(t *testing.T) {
	tmp := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(tmp)
	defer os.Chdir(origDir)

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
		{"schema on", capabilities.BuiltinGroups{Schema: true}, "schema tools", StatusOK, "registered"},
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
