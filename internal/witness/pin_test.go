package witness

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/djtouchette/rivet/internal/capabilities"
)

// goModPin is the version go.mod actually pins, which is the only thing that
// decides what `witnessapp.NewCommand` returns.
var goModPin = regexp.MustCompile(`(?m)^\s*github\.com/djtouchette/witness\s+(v[^\s]+)`)

// The pinned version is duplicated into PinnedVersion so the rest of rivet can
// name it; a duplicate that drifts is worse than none, because the comment
// hanging off it explains why the capability descriptions look the way they do.
func TestPinnedVersionMatchesGoMod(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}
	m := goModPin.FindSubmatch(data)
	if m == nil {
		t.Fatal("go.mod does not require github.com/djtouchette/witness")
	}
	if got := string(m[1]); got != PinnedVersion {
		t.Errorf("go.mod pins witness %s but PinnedVersion is %s.\n"+
			"Bumping the pin changes what the agent actually gets: re-read the witness.* descriptions in "+
			"internal/capabilities/builtins.go against the new build's `select --help` and its real JSON output, "+
			"then update this constant.", got, PinnedVersion)
	}
}

// witnessFlagRe pulls out the flags a description tells the agent to PASS —
// the ones written as a JSON argument, ["--kind", "unit"]. Flags quoted inside
// an example command line (`npx jest --runTestsByPath ...`) belong to the test
// runner, not to witness, and are not witness's to accept.
var witnessFlagRe = regexp.MustCompile(`\["(--[a-zA-Z][a-zA-Z0-9-]*)"`)

// TestDescriptionsOnlyNameFlagsWitnessAccepts is the guard on the gap between
// the sibling working tree and the tagged module rivet embeds.
//
// A description written against ../witness can advertise flags this build does
// not have. Cobra rejects an unknown flag with exit 1 before witness runs at
// all, so the agent's "safer" invocation — the one the description told it to
// reach for when it needed certainty — is the one that fails. The test asks the
// embedded binary itself rather than hard-coding a list, so it keeps working
// across a bump in either direction.
func TestDescriptionsOnlyNameFlagsWitnessAccepts(t *testing.T) {
	help := witnessSelectHelp(t)

	for _, c := range capabilities.Builtins() {
		if !strings.HasPrefix(c.Name, "witness.") {
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			for _, m := range witnessFlagRe.FindAllStringSubmatch(c.Description+" "+c.ArgsHint, -1) {
				flag := m[1]
				if !strings.Contains(help, flag) {
					t.Errorf("description names %s, which the embedded witness (%s) rejects as an unknown flag; "+
						"either drop it or bump the pin", flag, PinnedVersion)
				}
			}
		})
	}
}

// TestDescriptionsDoNotGateSafetyOnAnAbsentField is the guard on the subtler
// half of the same gap.
//
// "an empty tests[] is safe when summary.unmapped is empty" reads as caution
// and behaves as the opposite: against a witness whose summary has no unmapped
// field the condition holds on every single run, so the rule that was supposed
// to catch an unproven selection green-lights all of them. The descriptions
// must state the default — empty is unproven — rather than a condition under
// which empty is safe.
func TestDescriptionsDoNotGateSafetyOnAnAbsentField(t *testing.T) {
	// Phrasings that turn a missing field into a pass.
	banned := []string{
		"is only \"nothing to test\" when",
		"is only \"nothing to test\" if",
		"safe when summary",
		"safe if summary",
	}

	for _, c := range capabilities.Builtins() {
		if !strings.HasPrefix(c.Name, "witness.") {
			continue
		}
		text := c.Description + " " + c.ArgsHint
		for _, phrase := range banned {
			if strings.Contains(text, phrase) {
				t.Errorf("%s: %q makes an empty selection safe whenever the field is absent, which is every run on a witness that does not emit it:\n%s",
					c.Name, phrase, text)
			}
		}
		if !strings.Contains(strings.ToLower(text), "unproven") {
			t.Errorf("%s: description must say an empty selection is unproven by default:\n%s", c.Name, text)
		}
	}
}

// witnessSelectHelp returns `witness select --help` from the embedded build.
func witnessSelectHelp(t *testing.T) string {
	t.Helper()
	stdout, stderr, _, err := Run([]string{"select", "--help"})
	if err != nil {
		t.Fatalf("probing the embedded witness: %v", err)
	}
	help := stdout + stderr
	if !strings.Contains(help, "--format") {
		t.Fatalf("`select --help` does not look like witness's help output:\n%s", help)
	}
	return help
}
