package witness

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A witness failure that reaches the caller as a bare exit code is the same
// thing as no failure at all: the MCP tool result shows an empty stdout next to
// "(exit code: 1)", which reads as "nothing to run". The reason has to travel
// with it.
//
// An unknown flag is used because cobra rejects it identically in every witness
// version, so this stays a test of rivet's adapter rather than of whichever
// witness happens to be pinned.
func TestRunSurfacesTheReasonAFailedCommandGives(t *testing.T) {
	stdout, stderr, code, err := Run([]string{"select", "--no-such-flag"})
	if err != nil {
		t.Fatalf("Run returned a fatal error: %v", err)
	}
	if code == 0 {
		t.Errorf("exit code = 0 for a rejected flag, want non-zero")
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty on failure", stdout)
	}
	if !strings.Contains(stderr, "no-such-flag") {
		t.Errorf("stderr does not explain the failure: %q", stderr)
	}
}

// A selection that found tests must come back either as commands to run or as a
// loud refusal — never as an empty answer with exit 0, which is the false green
// witness.run's description tells the agent to watch for.
//
// Java stands in for "a language witness may not be able to run". Which
// languages those are is witness's business and changes as it grows runners, so
// the assertion is on the shape of the answer rather than on the list: if there
// is no command, there must be a non-zero exit and a reason. Nothing about a
// java repo may produce silence and a zero.
func TestRunNeverAnswersASelectionWithSilentSuccess(t *testing.T) {
	requireFailClosedWitness(t)
	repo := javaRepo(t)
	t.Chdir(repo)

	paths, _, _, err := Run([]string{"select", "--format", "paths"})
	if err != nil {
		t.Fatalf("Run returned a fatal error: %v", err)
	}
	if strings.TrimSpace(paths) == "" {
		t.Fatalf("no test selected for the edited source, so there is nothing to assert about running it")
	}

	stdout, stderr, code, err := Run([]string{"select", "--format", "exec"})
	if err != nil {
		t.Fatalf("Run returned a fatal error: %v", err)
	}

	if strings.TrimSpace(stdout) != "" {
		if code != 0 {
			t.Errorf("exit code = %d alongside a command %q: the caller cannot tell whether to run it", code, stdout)
		}
		return
	}

	// No command: witness declined to invent one, which must be audible.
	if code == 0 {
		t.Fatalf("no command and exit code 0 for a selection of %q — the tests are reported as run when nothing ran (stderr %q)",
			strings.TrimSpace(paths), stderr)
	}
	if strings.TrimSpace(stderr) == "" {
		t.Errorf("exit code %d with no explanation on stderr; the caller has nothing to report", code)
	}
}

// `select --format exec` answers with one command per LINE. A polyglot repo
// therefore needs every line run: witness.run's description says so, and this
// pins the output shape that promise is made about, so a witness bump that went
// back to a single command would fail here rather than silently make the
// description a lie.
func TestRunEmitsOneCommandPerLineForAPolyglotSelection(t *testing.T) {
	requireFailClosedWitness(t)
	repo := polyglotRepo(t)
	t.Chdir(repo)

	stdout, stderr, code, err := Run([]string{"select", "--format", "exec"})
	if err != nil {
		t.Fatalf("Run returned a fatal error: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stdout %q, stderr %q)", code, stdout, stderr)
	}

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) < 2 {
		t.Fatalf("got %d command(s), want one per ecosystem:\n%s", len(lines), stdout)
	}
	var elixir, node bool
	for _, l := range lines {
		if strings.HasPrefix(l, "mix test ") {
			elixir = true
		}
		if strings.Contains(l, "jest") {
			node = true
		}
	}
	if !elixir || !node {
		t.Errorf("commands do not cover both ecosystems (elixir=%v node=%v):\n%s", elixir, node, stdout)
	}
}

// requireFailClosedWitness skips the tests that assert witness's fail-closed
// output contract when the embedded witness predates it.
//
// go.mod still pins the last released witness, which answers a polyglot
// selection with a single `mix test <js paths>` and prints a bare, unrunnable
// path for a language it has no runner for — the false greens the capability
// descriptions in internal/capabilities now warn the agent about. The probe is
// --fallback, a flag that only exists in the fail-closed witness, so it does not
// beg the question by testing the same behaviour it gates on. Bumping the pin is
// what turns these tests from skipped into the check that the new contract
// actually arrived.
func requireFailClosedWitness(t *testing.T) {
	t.Helper()
	stdout, stderr, _, err := Run([]string{"select", "--help"})
	if err != nil {
		t.Fatalf("probing the embedded witness: %v", err)
	}
	if !strings.Contains(stdout+stderr, "--fallback") {
		t.Skip("embedded witness has no --fallback: it predates the fail-closed contract; bump github.com/djtouchette/witness in go.mod")
	}
}

// javaRepo builds a repository in a language witness has no runner for, with an
// uncommitted edit to the source its one test covers.
func javaRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "pom.xml", `<project><modelVersion>4.0.0</modelVersion><groupId>com.example</groupId><artifactId>calc</artifactId><version>1.0</version></project>`)
	writeFile(t, dir, "src/main/java/com/example/Calculator.java", `package com.example;

public class Calculator {
    public int add(int a, int b) { return a + b; }
}
`)
	writeFile(t, dir, "src/test/java/com/example/CalculatorTest.java", `package com.example;

public class CalculatorTest {
    public void testAdd() { new Calculator().add(1, 2); }
}
`)
	commit(t, dir)
	writeFile(t, dir, "src/main/java/com/example/Calculator.java", `package com.example;

public class Calculator {
    public int add(int a, int b) { return a + b; }
    public int sub(int a, int b) { return a - b; }
}
`)
	return dir
}

// polyglotRepo builds a repository whose one change touches two ecosystems, so
// no single test command can cover it.
func polyglotRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "mix.exs", `defmodule Shop.MixProject do
  use Mix.Project
  def project, do: [app: :shop, version: "0.1.0"]
end
`)
	writeFile(t, dir, "package.json", `{"name":"shop","version":"1.0.0","devDependencies":{"jest":"^29.0.0"}}`)
	writeFile(t, dir, "lib/shop/cart.ex", `defmodule Shop.Cart do
  def total(items), do: Enum.sum(items)
end
`)
	writeFile(t, dir, "test/shop/cart_test.exs", `defmodule Shop.CartTest do
  use ExUnit.Case
  alias Shop.Cart
  test "total" do
    assert Cart.total([1, 2]) == 3
  end
end
`)
	writeFile(t, dir, "assets/js/cart.js", `export function total(items) {
  return items.reduce((a, b) => a + b, 0);
}
`)
	writeFile(t, dir, "assets/js/__tests__/cart.test.js", `import { total } from "../cart";

test("total", () => {
  expect(total([1, 2])).toBe(3);
});
`)
	commit(t, dir)
	writeFile(t, dir, "lib/shop/cart.ex", `defmodule Shop.Cart do
  def total(items), do: Enum.sum(items)
  def count(items), do: length(items)
end
`)
	writeFile(t, dir, "assets/js/cart.js", `export function total(items) {
  return items.reduce((a, b) => a + b, 0);
}

export function count(items) {
  return items.length;
}
`)
	return dir
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// commit turns the directory into a git repository with everything committed,
// so the later edits show up as a working-tree diff — the input witness reads
// when it is given no explicit file arguments.
func commit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"add", "-A"},
		{"commit", "-qm", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(cmd.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}
