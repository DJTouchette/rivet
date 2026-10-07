package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Codex end-to-end: the real codex binary driving the real `rivet serve`.
//
// Codex needs a model to decide to call a tool, so a mock of the OpenAI
// Responses API stands in for one. Its first reply is the call a model would
// make in Codex's code mode — a JavaScript `exec` that finds the rivet tool
// in ALL_TOOLS (Codex defers MCP tools there, under sanitised names such as
// mcp__rivet__rivet_intent_approve) and calls it. Codex runs the script, the
// tool call reaches rivet, and the result comes back to the mock in the next
// request, where the test reads it.
//
// These pin what makes intent safe and usable under Codex, as observed with
// Codex 0.160:
//
//   - read-only rivet tools carry readOnlyHint, so they run under `codex
//     exec`, whose approval policy is "never";
//   - rivet.intent-approve is not read-only, so `codex exec` refuses it
//     before it reaches rivet;
//   - with every approval bypassed, the call reaches rivet and rivet asks the
//     person — and Codex, having no person to ask, declines. Nothing changes.
//
// The interactive path (Codex's TUI renders rivet's form; the person types the
// rule ID; the rule is applied) needs a terminal and is not automated here.
//
// Skipped when codex is not on PATH.

type mockResponses struct {
	mu      sync.Mutex
	script  string
	outputs []string
	served  int
}

func (m *mockResponses) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Input []map[string]interface{} `json:"input"`
	}
	_ = json.Unmarshal(body, &req)

	m.mu.Lock()
	for _, item := range req.Input {
		if item["type"] == "custom_tool_call_output" {
			out, _ := json.Marshal(item["output"])
			m.outputs = append(m.outputs, string(out))
		}
	}
	m.served++
	first := m.served == 1
	m.mu.Unlock()

	id := fmt.Sprintf("resp_%d", m.served)
	var item map[string]interface{}
	if first {
		item = map[string]interface{}{"type": "custom_tool_call", "name": "exec", "input": m.script, "call_id": "call_1"}
	} else {
		item = map[string]interface{}{"type": "message", "role": "assistant", "id": "msg_1",
			"content": []interface{}{map[string]interface{}{"type": "output_text", "text": "done"}}}
	}
	events := []map[string]interface{}{
		{"type": "response.created", "response": map[string]interface{}{"id": id}},
		{"type": "response.output_item.done", "item": item},
		{"type": "response.completed", "response": map[string]interface{}{"id": id, "usage": map[string]interface{}{
			"input_tokens": 0, "input_tokens_details": nil, "output_tokens": 0, "output_tokens_details": nil, "total_tokens": 0}}},
	}
	w.Header().Set("Content-Type", "text/event-stream")
	for _, ev := range events {
		data, _ := json.Marshal(ev)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev["type"], data)
	}
}

// codexCall has codex call one rivet tool and returns what the tool returned
// to the model.
func (s *shop) codexCall(t *testing.T, toolSuffix, argsJS string, extra ...string) string {
	t.Helper()
	codexBin, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex is not on PATH")
	}

	mock := &mockResponses{script: "" +
		"const names = ALL_TOOLS.map(t => t.name).filter(n => n.startsWith('mcp__rivet__'));\n" +
		"const n = names.find(n => n.endsWith('" + toolSuffix + "'));\n" +
		"if (!n) { text('NO TOOL among ' + JSON.stringify(names)); exit(); }\n" +
		"const r = await tools[n](" + argsJS + ");\n" +
		"text('RESULT: ' + JSON.stringify(r));\n"}
	srv := httptest.NewServer(mock)
	defer srv.Close()

	codexHome := t.TempDir()
	config := fmt.Sprintf(`model_provider = "mock"

[model_providers.mock]
name = "mock"
base_url = %q
wire_api = "responses"

[mcp_servers.rivet]
command = %q
args = ["serve"]
cwd = %q
env = { HOME = %q }

[projects.%q]
trust_level = "trusted"
`, srv.URL+"/v1", s.bin, s.dir, s.home, s.dir)
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(config), 0644); err != nil {
		t.Fatal(err)
	}

	args := append([]string{"exec", "--skip-git-repo-check"}, extra...)
	args = append(args, "go")
	cmd := exec.Command(codexBin, args...)
	cmd.Dir = s.dir
	cmd.Env = append(os.Environ(), "CODEX_HOME="+codexHome, "HOME="+s.home, "OPENAI_API_KEY=")
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("codex exec: %v\n%s", err, out)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()
	joined := strings.Join(mock.outputs, "\n")
	if !strings.Contains(joined, "RESULT: ") {
		t.Fatalf("codex never returned the tool result to the model.\nexec outputs: %s\ncodex output:\n%s", joined, out)
	}
	return joined
}

func TestIntentHarness_CodexEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex is not on PATH")
	}

	t.Run("read-only lookups run under codex exec", func(t *testing.T) {
		s := newShop(t)
		got := s.codexCall(t, "rivet_intent", `{path: 'services/billing/invoice.go'}`)
		requireContains(t, "rivet.intent via codex", got, "Business rules for services/billing/invoice.go", "BIL-001")
	})

	t.Run("codex exec refuses approval before it reaches rivet", func(t *testing.T) {
		s := newShop(t)
		before := s.read(".rivet/intent/domains/billing.md")
		name := s.propose(map[string]interface{}{"change": "add", "doc": "billing", "statement": "Credit notes never exceed the invoice.", "why": "refund loophole"})
		got := s.codexCall(t, "rivet_intent_approve", fmt.Sprintf(`{proposal: %q}`, name))
		requireContains(t, "approve via codex exec", got, "requires approval")
		if s.read(".rivet/intent/domains/billing.md") != before {
			t.Fatal("codex exec approved a rule")
		}
	})

	t.Run("with every approval bypassed, codex declines rivet's prompt", func(t *testing.T) {
		s := newShop(t)
		before := s.read(".rivet/intent/domains/billing.md")
		name := s.propose(map[string]interface{}{"change": "add", "doc": "billing", "statement": "Credit notes never exceed the invoice.", "why": "refund loophole"})
		got := s.codexCall(t, "rivet_intent_approve", fmt.Sprintf(`{proposal: %q}`, name), "--dangerously-bypass-approvals-and-sandbox")
		requireContains(t, "approve via codex, bypassed", got, "The user declined to approve BIL-012")
		if s.read(".rivet/intent/domains/billing.md") != before {
			t.Fatal("a rule was approved with no person present")
		}
	})
}
