package mcp

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/djtouchette/rivet/internal/capabilities"
	rivetctx "github.com/djtouchette/rivet/internal/context"
	"github.com/djtouchette/rivet/internal/pins"
)

func TestNegotiateVersion(t *testing.T) {
	cases := map[string]string{
		"2025-06-18": "2025-06-18",
		"2024-11-05": "2024-11-05",
		"2099-01-01": supportedProtocolVersions[0], // unknown → our newest; the client decides
		"":           protocolVersion,              // no negotiation attempted
	}
	for in, want := range cases {
		if got := negotiateVersion(in); got != want {
			t.Errorf("negotiateVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

// approveProject builds a project with one intent doc and one pending
// proposal to add a rule, and a server pointed at it.
func approveProject(t *testing.T) (*Server, string, string) {
	t.Helper()
	root := t.TempDir()
	doc := filepath.Join(root, rivetctx.IntentDir, "domains", "billing.md")
	if err := os.MkdirAll(filepath.Dir(doc), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(doc, []byte("---\nowner: finance\nprefix: BIL\n---\n\n# Billing\n\n## Invariants\n\n- **BIL-001** One.\n  why: w\n"), 0644); err != nil {
		t.Fatal(err)
	}
	intents, err := rivetctx.LoadIntent(root)
	if err != nil {
		t.Fatal(err)
	}
	reg := capabilities.NewRegistry()
	s := NewServer(reg, capabilities.NewExecutor(reg), nil, pins.NewRegistry(), nil, "test", false)
	s.SetIntent(intents, root, rivetctx.ScanOptions{})

	resp := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"rivet.intent-propose","arguments":{"change":"add","doc":"billing","statement":"Credit notes never exceed the invoice.","why":"refund loophole"}}}`)
	var r ToolCallResult
	unmarshalResult(t, resp, &r)
	if r.IsError {
		t.Fatalf("propose: %s", r.Content[0].Text)
	}
	props, _ := rivetctx.ListIntentProposals(filepath.Join(root, rivetctx.IntentDir))
	if len(props) != 1 {
		t.Fatalf("proposals = %+v", props)
	}
	return s, root, props[0].Name
}

func approveCall(t *testing.T, s *Server, name string) ToolCallResult {
	t.Helper()
	resp := call(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"rivet.intent-approve","arguments":{"proposal":"`+name+`"}}}`)
	var r ToolCallResult
	unmarshalResult(t, resp, &r)
	return r
}

func billingDoc(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, rivetctx.IntentDir, "domains", "billing.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// rivet:intent CTX-004
func TestIntentApproveNeedsThePersonsAnswer(t *testing.T) {
	cases := []struct {
		name     string
		elicitor Elicitor
		applied  bool
		say      string
	}{
		{"no elicitation support", nil, false, "will not approve a rule without the user's own confirmation"},
		{"declined", func(string, map[string]interface{}) (*ElicitResult, error) {
			return &ElicitResult{Action: ElicitDecline}, nil
		}, false, "declined"},
		{"dismissed", func(string, map[string]interface{}) (*ElicitResult, error) {
			return &ElicitResult{Action: ElicitCancel}, nil
		}, false, "dismissed"},
		{"wrong confirmation", func(string, map[string]interface{}) (*ElicitResult, error) {
			return &ElicitResult{Action: ElicitAccept, Content: map[string]interface{}{"confirm": "yes"}}, nil
		}, false, "did not match"},
		{"accepted with the rule ID", func(msg string, schema map[string]interface{}) (*ElicitResult, error) {
			if !strings.Contains(msg, "+ - **BIL-002** Credit notes never exceed the invoice.") || !strings.Contains(msg, "Type BIL-002") {
				t.Errorf("prompt doesn't show the change:\n%s", msg)
			}
			return &ElicitResult{Action: ElicitAccept, Content: map[string]interface{}{"confirm": " BIL-002 "}}, nil
		}, true, "is now a ratified rule"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, root, name := approveProject(t)
			before := billingDoc(t, root)
			s.SetElicitor(c.elicitor)

			r := approveCall(t, s, name)
			if !strings.Contains(r.Content[0].Text, c.say) {
				t.Fatalf("reply = %q, want it to say %q", r.Content[0].Text, c.say)
			}
			after := billingDoc(t, root)
			pending, _ := rivetctx.ListIntentProposals(filepath.Join(root, rivetctx.IntentDir))
			if c.applied {
				if !strings.Contains(after, "**BIL-002** Credit notes never exceed the invoice.") || len(pending) != 0 {
					t.Fatalf("accepted but not applied/archived:\n%s\npending=%v", after, pending)
				}
				if s.intents == nil || func() bool { r, _ := rivetctx.FindRule(s.intents, "BIL-002"); return r == nil }() {
					t.Fatal("server still serves the pre-approval rules")
				}
				return
			}
			if after != before || len(pending) != 1 {
				t.Fatalf("not approved, but the doc or proposal changed (pending=%d)", len(pending))
			}
		})
	}
}

// TestIntentApproveOverTheWire runs the elicitation round trip through Serve
// on a real stream: the server asks mid-tool-call, the client interleaves a
// ping and an unrelated request before answering, and only the person's
// answer completes the call.
func TestIntentApproveOverTheWire(t *testing.T) {
	s, root, name := approveProject(t)

	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- s.Serve(serverR, serverW); serverW.Close() }()

	in := bufio.NewScanner(clientR)
	send := func(v string) {
		if _, err := clientW.Write([]byte(v + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	recv := func() map[string]interface{} {
		if !in.Scan() {
			t.Fatalf("server closed: %v", in.Err())
		}
		var m map[string]interface{}
		if err := json.Unmarshal(in.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	send(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{"elicitation":{}},"clientInfo":{"name":"test-client","title":"Test Client"}}}`)
	if v := recv()["result"].(map[string]interface{})["protocolVersion"]; v != "2025-06-18" {
		t.Fatalf("negotiated %v", v)
	}

	send(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"rivet.intent-approve","arguments":{"proposal":"` + name + `"}}}`)
	ask := recv()
	if ask["method"] != "elicitation/create" {
		t.Fatalf("expected an elicitation request, got %v", ask)
	}
	params := ask["params"].(map[string]interface{})
	if !strings.Contains(params["message"].(string), "BIL-002") {
		t.Fatalf("elicitation message = %v", params["message"])
	}
	if _, ok := params["requestedSchema"].(map[string]interface{})["properties"].(map[string]interface{})["confirm"]; !ok {
		t.Fatalf("schema = %v", params["requestedSchema"])
	}

	// While the person thinks: a ping (answered at once) and another request
	// (queued until the approval finishes).
	send(`{"jsonrpc":"2.0","id":8,"method":"ping"}`)
	if pong := recv(); pong["id"] != float64(8) {
		t.Fatalf("ping not answered while waiting: %v", pong)
	}
	send(`{"jsonrpc":"2.0","id":9,"method":"tools/list","params":{}}`)

	idJSON, _ := json.Marshal(ask["id"])
	send(`{"jsonrpc":"2.0","id":` + string(idJSON) + `,"result":{"action":"accept","content":{"confirm":"BIL-002"}}}`)

	first := recv()
	if first["id"] != float64(7) {
		t.Fatalf("expected the tool call's reply first, got %v", first)
	}
	text := first["result"].(map[string]interface{})["content"].([]interface{})[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, "is now a ratified rule") || !strings.Contains(text, "confirmed in Test Client") {
		t.Fatalf("approve reply = %s", text)
	}
	if second := recv(); second["id"] != float64(9) {
		t.Fatalf("queued request not served afterwards: %v", second)
	}
	if !strings.Contains(billingDoc(t, root), "**BIL-002**") {
		t.Fatal("rule not written")
	}

	clientW.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestIntentApproveRefusedWithoutElicitationCapability(t *testing.T) {
	s, root, name := approveProject(t)
	before := billingDoc(t, root)

	// A client that negotiates elicitation's version but never offers it, and
	// one that offers it on a version that predates it, are both refused.
	for _, init := range []string{
		`{"protocolVersion":"2025-06-18","capabilities":{}}`,
		`{"protocolVersion":"2024-11-05","capabilities":{"elicitation":{}}}`,
	} {
		in := strings.NewReader(
			`{"jsonrpc":"2.0","id":0,"method":"initialize","params":` + init + `}` + "\n" +
				`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"rivet.intent-approve","arguments":{"proposal":"` + name + `"}}}` + "\n")
		var out strings.Builder
		if err := s.Serve(in, &out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "did not offer to ask the user directly") || strings.Contains(out.String(), "elicitation/create") {
			t.Fatalf("init %s: %s", init, out.String())
		}
	}
	if billingDoc(t, root) != before {
		t.Fatal("approved without asking")
	}
}

// TestIntentApproveStopsWaitingWhenCancelled: Codex times MCP tool calls out
// and cancels them. A person still reading the prompt must not leave the
// server blocked forever, and nothing may be applied.
func TestIntentApproveStopsWaitingWhenCancelled(t *testing.T) {
	s, root, name := approveProject(t)
	before := billingDoc(t, root)
	in := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{"elicitation":{"form":{},"url":{}}},"clientInfo":{"name":"codex-mcp-client"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"rivet.intent-approve","arguments":{"proposal":"` + name + `"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":5,"reason":"timed out"}}`,
		`{"jsonrpc":"2.0","id":6,"method":"ping"}`,
	}, "\n") + "\n")
	var out strings.Builder
	if err := s.Serve(in, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "elicitation/create") || !strings.Contains(got, "cancelled this call before the user answered") {
		t.Fatalf("output:\n%s", got)
	}
	if !strings.Contains(got, `"id":6`) {
		t.Fatalf("server stopped serving after the cancellation:\n%s", got)
	}
	if billingDoc(t, root) != before {
		t.Fatal("applied after the call was cancelled")
	}
}

// TestApprovalSchemaFitsCodex pins the form to what Codex can render as a
// text input and will never auto-accept. See approvalSchema.
func TestApprovalSchemaFitsCodex(t *testing.T) {
	schema := approvalSchema("BIL-012")
	props := schema["properties"].(map[string]interface{})
	if len(props) == 0 {
		t.Fatal("an elicitation schema without properties is auto-accepted by Codex under approval_policy=never")
	}
	allowed := map[string]bool{"type": true, "title": true, "description": true, "minLength": true, "maxLength": true, "format": true, "default": true}
	for name, raw := range props {
		for key := range raw.(map[string]interface{}) {
			if !allowed[key] {
				t.Errorf("property %q uses %q: Codex would drop the text input and fall back to accept/decline", name, key)
			}
		}
		if raw.(map[string]interface{})["default"] != nil {
			t.Errorf("property %q has a default — a pre-filled confirmation defeats typing it", name)
		}
	}
}

// TestToolAnnotations: clients gate calls on these. Codex refuses every MCP
// call not marked read-only under approval_policy=never (codex exec), so the
// lookups must say so — and nothing that writes may claim it.
func TestToolAnnotations(t *testing.T) {
	s := newTestServer(t)
	resp := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	var result toolsListResult
	unmarshalResult(t, resp, &result)
	readOnly := map[string]bool{}
	for _, tl := range result.Tools {
		if tl.Annotations == nil || tl.Annotations.ReadOnlyHint == nil {
			t.Errorf("%s has no readOnlyHint", tl.Name)
			continue
		}
		readOnly[tl.Name] = *tl.Annotations.ReadOnlyHint
	}
	for _, name := range []string{"rivet.context-list", "rivet.context-show", "rivet.context-recommend", "rivet.runbook", "rivet.intent", "echo-test"} {
		if !readOnly[name] {
			t.Errorf("%s should be read-only", name)
		}
	}
	for _, name := range []string{"rivet.learn", "rivet.runbook-draft", "rivet.intent-propose", "rivet.intent-approve", "rally.pin", "rally.unpin", "danger-cmd"} {
		if readOnly[name] {
			t.Errorf("%s writes, so it must not claim read-only", name)
		}
	}
	for _, tl := range result.Tools {
		if tl.Name == "danger-cmd" && (tl.Annotations.DestructiveHint == nil || !*tl.Annotations.DestructiveHint) {
			t.Error("dangerous capabilities must be marked destructive")
		}
	}
}
