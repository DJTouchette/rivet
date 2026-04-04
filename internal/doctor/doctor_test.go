package doctor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRun_NoRivetDir(t *testing.T) {
	tmp := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(tmp)
	defer os.Chdir(origDir)

	result := Run()

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

	result := Run()

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

	result := Run()

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

	result := Run()

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
