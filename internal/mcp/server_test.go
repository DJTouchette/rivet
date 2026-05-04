package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/djtouchette/rivet/internal/capabilities"
	rivetctx "github.com/djtouchette/rivet/internal/context"
	"github.com/djtouchette/rivet/internal/pins"
	"github.com/djtouchette/rivet/internal/policy"
)

type fakePinProvider struct {
	src     string
	items   []pins.Item
	pinned  map[string]string // id -> note
	pinErr  error
}

func newFakePinProvider(src string) *fakePinProvider {
	return &fakePinProvider{src: src, pinned: map[string]string{}}
}

func (f *fakePinProvider) Source() string { return f.src }
func (f *fakePinProvider) List() ([]pins.Item, error) {
	out := append([]pins.Item(nil), f.items...)
	for id, note := range f.pinned {
		out = append(out, pins.Item{URI: f.src + "://pinned/" + id, Name: id, Description: note})
	}
	return out, nil
}
func (f *fakePinProvider) Read(uri string) (pins.Item, error) {
	for _, it := range f.items {
		if it.URI == uri {
			return it, nil
		}
	}
	return pins.Item{}, fmt.Errorf("not found: %s", uri)
}
func (f *fakePinProvider) Pin(id, note string) error {
	if f.pinErr != nil {
		return f.pinErr
	}
	f.pinned[id] = note
	return nil
}
func (f *fakePinProvider) Unpin(id string) error {
	delete(f.pinned, id)
	return nil
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	reg := capabilities.NewRegistry()
	reg.Register(capabilities.Capability{
		Name:        "echo-test",
		Kind:        capabilities.KindProjectCommand,
		Description: "Echo test command",
		Command:     []string{"echo", "hello"},
		Output:      "text",
		Safety:      capabilities.SafetyLevelSafe,
	})
	reg.Register(capabilities.Capability{
		Name:        "danger-cmd",
		Kind:        capabilities.KindProjectCommand,
		Description: "A dangerous command",
		Command:     []string{"echo", "boom"},
		Output:      "text",
		Safety:      capabilities.SafetyLevelDangerous,
	})

	exec := capabilities.NewExecutor(reg)

	contexts := []*rivetctx.Document{
		{
			Name:  "billing",
			Kind:  rivetctx.KindDomain,
			Title: "Billing Domain",
			Body:  "# Billing Domain\n\nHandles invoices.",
		},
		{
			Name:  "sql-views",
			Kind:  rivetctx.KindParadigm,
			Title: "SQL Views",
			Body:  "# SQL Views\n\nRead-only aggregation pattern.",
		},
	}

	s := NewServer(reg, exec, contexts, nil, nil, "test", true)
	s.SetLearningsDir(t.TempDir())
	return s
}

// call sends a single JSON-RPC request to the server and returns the response.
func call(t *testing.T, s *Server, reqJSON string) *Response {
	t.Helper()
	return s.HandleMessage([]byte(reqJSON))
}

// unmarshalResult marshals resp.Result to JSON then unmarshals into dst.
func unmarshalResult(t *testing.T, resp *Response, dst interface{}) {
	t.Helper()
	data, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if err := json.Unmarshal(data, dst); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Initialize
// ---------------------------------------------------------------------------

func TestInitialize(t *testing.T) {
	s := newTestServer(t)
	resp := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	var result initializeResult
	unmarshalResult(t, resp, &result)

	if result.ProtocolVersion != protocolVersion {
		t.Errorf("expected protocol %q, got %q", protocolVersion, result.ProtocolVersion)
	}
	if result.ServerInfo.Name != "rivet" {
		t.Errorf("expected server name 'rivet', got %q", result.ServerInfo.Name)
	}
	if result.ServerInfo.Version != "test" {
		t.Errorf("expected version 'test', got %q", result.ServerInfo.Version)
	}
	if result.Capabilities.Tools == nil {
		t.Error("expected tools capability")
	}
	if result.Capabilities.Resources == nil {
		t.Error("expected resources capability")
	}
}

// ---------------------------------------------------------------------------
// Ping
// ---------------------------------------------------------------------------

func TestPing(t *testing.T) {
	s := newTestServer(t)
	resp := call(t, s, `{"jsonrpc":"2.0","id":99,"method":"ping","params":{}}`)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

// ---------------------------------------------------------------------------
// Tools
// ---------------------------------------------------------------------------

func TestToolsList(t *testing.T) {
	s := newTestServer(t)
	resp := call(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	var result toolsListResult
	unmarshalResult(t, resp, &result)

	// 4 rivet tools + 2 rally pin tools + 2 registry capabilities = 8
	if len(result.Tools) != 8 {
		t.Fatalf("expected 8 tools, got %d", len(result.Tools))
	}

	// Built-in rivet tools come first
	if result.Tools[0].Name != "rivet.context-list" {
		t.Errorf("expected first tool 'rivet.context-list', got %q", result.Tools[0].Name)
	}
	if result.Tools[1].Name != "rivet.context-show" {
		t.Errorf("expected second tool 'rivet.context-show', got %q", result.Tools[1].Name)
	}
	// context-show should have a name property
	if _, ok := result.Tools[1].InputSchema.Properties["name"]; !ok {
		t.Error("rivet.context-show should have 'name' property in schema")
	}
	if result.Tools[3].Name != "rivet.learn" {
		t.Errorf("expected fourth tool 'rivet.learn', got %q", result.Tools[3].Name)
	}
	if result.Tools[4].Name != "rally.pin" {
		t.Errorf("expected fifth tool 'rally.pin', got %q", result.Tools[4].Name)
	}
	if result.Tools[5].Name != "rally.unpin" {
		t.Errorf("expected sixth tool 'rally.unpin', got %q", result.Tools[5].Name)
	}

	// Registry capabilities follow (sorted by name)
	if result.Tools[6].Name != "danger-cmd" {
		t.Errorf("expected seventh tool 'danger-cmd', got %q", result.Tools[6].Name)
	}
	if result.Tools[7].Name != "echo-test" {
		t.Errorf("expected eighth tool 'echo-test', got %q", result.Tools[7].Name)
	}

	// Dangerous tool should have approve property
	dangerTool := result.Tools[6]
	if _, ok := dangerTool.InputSchema.Properties["approve"]; !ok {
		t.Error("dangerous tool should have 'approve' property in schema")
	}

	// Safe tool should NOT have approve property
	safeTool := result.Tools[7]
	if _, ok := safeTool.InputSchema.Properties["approve"]; ok {
		t.Error("safe tool should not have 'approve' property in schema")
	}

	// Capability tools should have args property
	if _, ok := safeTool.InputSchema.Properties["args"]; !ok {
		t.Error("tool should have 'args' property in schema")
	}
}

func TestToolsListDescription(t *testing.T) {
	s := newTestServer(t)
	resp := call(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)

	var result toolsListResult
	unmarshalResult(t, resp, &result)

	for _, tool := range result.Tools {
		if !strings.HasPrefix(tool.Description, "[") {
			t.Errorf("tool %q description should start with safety prefix, got %q", tool.Name, tool.Description)
		}
	}
}

func TestToolsCallSafe(t *testing.T) {
	s := newTestServer(t)
	resp := call(t, s, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo-test","arguments":{}}}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	var result ToolCallResult
	unmarshalResult(t, resp, &result)

	if result.IsError {
		t.Error("expected no error")
	}
	if len(result.Content) == 0 {
		t.Fatal("expected content")
	}
	if !strings.Contains(result.Content[0].Text, "hello") {
		t.Errorf("expected 'hello' in output, got %q", result.Content[0].Text)
	}
	if result.Content[0].Type != "text" {
		t.Errorf("expected content type 'text', got %q", result.Content[0].Type)
	}
}

func TestToolsCallWithArgs(t *testing.T) {
	s := newTestServer(t)
	resp := call(t, s, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo-test","arguments":{"args":["world","!"]}}}`)

	var result ToolCallResult
	unmarshalResult(t, resp, &result)

	if result.IsError {
		t.Error("expected no error")
	}
	if !strings.Contains(result.Content[0].Text, "hello world !") {
		t.Errorf("expected 'hello world !' in output, got %q", result.Content[0].Text)
	}
}

func TestToolsCallDangerousWithoutApproval(t *testing.T) {
	s := newTestServer(t)
	resp := call(t, s, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"danger-cmd","arguments":{}}}`)

	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", resp.Error)
	}

	var result ToolCallResult
	unmarshalResult(t, resp, &result)

	if !result.IsError {
		t.Error("expected tool error for unapproved dangerous command")
	}
	if len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, "dangerous") {
		t.Errorf("expected error mentioning 'dangerous', got %v", result.Content)
	}
}

func TestToolsCallDangerousWithApproval(t *testing.T) {
	s := newTestServer(t)
	resp := call(t, s, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"danger-cmd","arguments":{"approve":true}}}`)

	var result ToolCallResult
	unmarshalResult(t, resp, &result)

	if result.IsError {
		t.Errorf("expected success with approval, got error: %v", result.Content)
	}
	if !strings.Contains(result.Content[0].Text, "boom") {
		t.Errorf("expected 'boom' in output, got %q", result.Content[0].Text)
	}
}

func TestToolsCallNotFound(t *testing.T) {
	s := newTestServer(t)
	resp := call(t, s, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"nonexistent","arguments":{}}}`)

	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", resp.Error)
	}

	var result ToolCallResult
	unmarshalResult(t, resp, &result)

	if !result.IsError {
		t.Error("expected error for nonexistent tool")
	}
}

func TestToolsCallInvalidParams(t *testing.T) {
	s := newTestServer(t)
	resp := call(t, s, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":"bad"}`)

	if resp.Error == nil {
		t.Error("expected JSON-RPC error for invalid params")
	}
	if resp.Error.Code != -32602 {
		t.Errorf("expected error code -32602, got %d", resp.Error.Code)
	}
}

// ---------------------------------------------------------------------------
// Context tools (built-in)
// ---------------------------------------------------------------------------

func TestContextListTool(t *testing.T) {
	s := newTestServer(t)
	resp := call(t, s, `{"jsonrpc":"2.0","id":20,"method":"tools/call","params":{"name":"rivet.context-list","arguments":{}}}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	var result ToolCallResult
	unmarshalResult(t, resp, &result)

	if result.IsError {
		t.Error("expected no error")
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "billing") {
		t.Errorf("expected 'billing' in output, got %q", text)
	}
	if !strings.Contains(text, "sql-views") {
		t.Errorf("expected 'sql-views' in output, got %q", text)
	}
	if !strings.Contains(text, "domain") {
		t.Errorf("expected 'domain' in output, got %q", text)
	}
}

func TestContextListToolEmpty(t *testing.T) {
	reg := capabilities.NewRegistry()
	exec := capabilities.NewExecutor(reg)
	s := NewServer(reg, exec, nil, nil, nil, "test", true)

	resp := call(t, s, `{"jsonrpc":"2.0","id":20,"method":"tools/call","params":{"name":"rivet.context-list","arguments":{}}}`)

	var result ToolCallResult
	unmarshalResult(t, resp, &result)

	if result.IsError {
		t.Error("expected no error even with empty context")
	}
	if !strings.Contains(result.Content[0].Text, "No context documents") {
		t.Errorf("expected empty message, got %q", result.Content[0].Text)
	}
}

func TestContextShowTool(t *testing.T) {
	s := newTestServer(t)
	resp := call(t, s, `{"jsonrpc":"2.0","id":21,"method":"tools/call","params":{"name":"rivet.context-show","arguments":{"name":"billing"}}}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	var result ToolCallResult
	unmarshalResult(t, resp, &result)

	if result.IsError {
		t.Error("expected no error")
	}
	if !strings.Contains(result.Content[0].Text, "Billing Domain") {
		t.Errorf("expected billing content, got %q", result.Content[0].Text)
	}
}

func TestContextShowToolNotFound(t *testing.T) {
	s := newTestServer(t)
	resp := call(t, s, `{"jsonrpc":"2.0","id":21,"method":"tools/call","params":{"name":"rivet.context-show","arguments":{"name":"nonexistent"}}}`)

	var result ToolCallResult
	unmarshalResult(t, resp, &result)

	if !result.IsError {
		t.Error("expected error for nonexistent context")
	}
}

func TestContextShowToolMissingName(t *testing.T) {
	s := newTestServer(t)
	resp := call(t, s, `{"jsonrpc":"2.0","id":21,"method":"tools/call","params":{"name":"rivet.context-show","arguments":{}}}`)

	var result ToolCallResult
	unmarshalResult(t, resp, &result)

	if !result.IsError {
		t.Error("expected error when name is missing")
	}
}

// ---------------------------------------------------------------------------
// Resources
// ---------------------------------------------------------------------------

func TestResourcesList(t *testing.T) {
	s := newTestServer(t)
	resp := call(t, s, `{"jsonrpc":"2.0","id":6,"method":"resources/list","params":{}}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	var result resourcesListResult
	unmarshalResult(t, resp, &result)

	if len(result.Resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(result.Resources))
	}

	// Check first resource
	r := result.Resources[0]
	if r.URI != "rivet://context/domains/billing" {
		t.Errorf("expected billing URI, got %q", r.URI)
	}
	if r.Name != "Billing Domain" {
		t.Errorf("expected name 'Billing Domain', got %q", r.Name)
	}
	if r.MimeType != "text/markdown" {
		t.Errorf("expected mimeType 'text/markdown', got %q", r.MimeType)
	}
	if !strings.Contains(r.Description, "domain") {
		t.Errorf("expected 'domain' in description, got %q", r.Description)
	}
}

func TestResourcesListEmpty(t *testing.T) {
	reg := capabilities.NewRegistry()
	exec := capabilities.NewExecutor(reg)
	s := NewServer(reg, exec, nil, nil, nil, "test", true)

	resp := call(t, s, `{"jsonrpc":"2.0","id":6,"method":"resources/list","params":{}}`)

	var result resourcesListResult
	unmarshalResult(t, resp, &result)

	if len(result.Resources) != 0 {
		t.Fatalf("expected 0 resources, got %d", len(result.Resources))
	}
}

func TestResourcesRead(t *testing.T) {
	s := newTestServer(t)
	resp := call(t, s, `{"jsonrpc":"2.0","id":7,"method":"resources/read","params":{"uri":"rivet://context/domains/billing"}}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	var result resourceReadResult
	unmarshalResult(t, resp, &result)

	if len(result.Contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(result.Contents))
	}
	c := result.Contents[0]
	if c.URI != "rivet://context/domains/billing" {
		t.Errorf("expected billing URI, got %q", c.URI)
	}
	if c.MimeType != "text/markdown" {
		t.Errorf("expected mimeType 'text/markdown', got %q", c.MimeType)
	}
	if !strings.Contains(c.Text, "Billing Domain") {
		t.Errorf("expected 'Billing Domain' in text, got %q", c.Text)
	}
}

func TestResourcesReadParadigm(t *testing.T) {
	s := newTestServer(t)
	resp := call(t, s, `{"jsonrpc":"2.0","id":7,"method":"resources/read","params":{"uri":"rivet://context/paradigms/sql-views"}}`)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	var result resourceReadResult
	unmarshalResult(t, resp, &result)

	if !strings.Contains(result.Contents[0].Text, "SQL Views") {
		t.Errorf("expected 'SQL Views' in text, got %q", result.Contents[0].Text)
	}
}

func TestResourcesReadNotFound(t *testing.T) {
	s := newTestServer(t)
	resp := call(t, s, `{"jsonrpc":"2.0","id":8,"method":"resources/read","params":{"uri":"rivet://context/domains/nonexistent"}}`)

	if resp.Error == nil {
		t.Error("expected error for nonexistent resource")
	}
}

func TestResourcesReadInvalidParams(t *testing.T) {
	s := newTestServer(t)
	resp := call(t, s, `{"jsonrpc":"2.0","id":8,"method":"resources/read","params":"bad"}`)

	if resp.Error == nil {
		t.Error("expected JSON-RPC error for invalid params")
	}
	if resp.Error.Code != -32602 {
		t.Errorf("expected error code -32602, got %d", resp.Error.Code)
	}
}

// ---------------------------------------------------------------------------
// Policy enforcement
// ---------------------------------------------------------------------------

func TestToolsCallBlockedByPolicy(t *testing.T) {
	reg := capabilities.NewRegistry()
	reg.Register(capabilities.Capability{
		Name:    "gated-cmd",
		Kind:    capabilities.KindProjectCommand,
		Description: "A gated command",
		Command: []string{"echo", "gated"},
		Output:  "text",
		Safety:  capabilities.SafetyLevelDangerous,
	})

	exec := capabilities.NewExecutor(reg)
	policies := []policy.Rule{
		{
			Name:       "require-prod",
			Match:      policy.Match{Safety: "dangerous"},
			RequireEnv: []string{"NEVER_SET_XYZ_TEST_ONLY"},
		},
	}
	s := NewServer(reg, exec, nil, nil, policies, "test", true)

	resp := call(t, s, `{"jsonrpc":"2.0","id":99,"method":"tools/call","params":{"name":"gated-cmd","arguments":{"approve":true}}}`)

	var result ToolCallResult
	unmarshalResult(t, resp, &result)

	if !result.IsError {
		t.Error("expected policy block error")
	}
	if len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, "policy") {
		t.Errorf("expected policy-related error message, got %v", result.Content)
	}
}

// ---------------------------------------------------------------------------
// Learn nudge
// ---------------------------------------------------------------------------

func newServerWithRecon(t *testing.T) *Server {
	t.Helper()
	reg := capabilities.NewRegistry()
	reg.Register(capabilities.Capability{
		Name:    "recon.search",
		Kind:    capabilities.KindTool,
		Description: "Search",
		Command: []string{"echo", "search results"},
		Output:  "json",
		Safety:  capabilities.SafetyLevelSafe,
	})
	reg.Register(capabilities.Capability{
		Name:    "recon.grep",
		Kind:    capabilities.KindTool,
		Description: "Grep",
		Command: []string{"echo", "grep results"},
		Output:  "json",
		Safety:  capabilities.SafetyLevelSafe,
	})
	reg.Register(capabilities.Capability{
		Name:    "recon.related",
		Kind:    capabilities.KindTool,
		Description: "Related",
		Command: []string{"echo", "related results"},
		Output:  "json",
		Safety:  capabilities.SafetyLevelSafe,
	})
	exec := capabilities.NewExecutor(reg)
	s := NewServer(reg, exec, nil, nil, nil, "test", true)
	s.SetLearningsDir(t.TempDir())
	return s
}

func TestContextFirstNudge(t *testing.T) {
	s := newServerWithRecon(t)

	// First recon call — no nudge yet.
	call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"recon.search","arguments":{"args":["foo"]}}}`)

	// Second recon call without context-show — should get context-first nudge.
	resp := call(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"recon.grep","arguments":{"args":["bar"]}}}`)

	var result ToolCallResult
	unmarshalResult(t, resp, &result)

	if !strings.Contains(result.Content[0].Text, "context docs first") {
		t.Error("should nudge to check context docs first")
	}
}

func TestContextFirstNudgeSuppressedAfterContextShow(t *testing.T) {
	s := newServerWithRecon(t)

	// Call context-show first.
	call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"rivet.context-show","arguments":{"name":"billing"}}}`)

	// Recon call after context-show — no context-first nudge.
	resp := call(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"recon.search","arguments":{"args":["foo"]}}}`)

	var result ToolCallResult
	unmarshalResult(t, resp, &result)

	if strings.Contains(result.Content[0].Text, "context docs first") {
		t.Error("context-first nudge should NOT appear after context-show")
	}
}

func TestLearnNudgeNotShownBeforeThreshold(t *testing.T) {
	s := newServerWithRecon(t)
	s.contextShown = true // simulate proper flow

	// 2 recon calls — below threshold of 3.
	call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"recon.search","arguments":{"args":["foo"]}}}`)
	resp := call(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"recon.grep","arguments":{"args":["bar"]}}}`)

	var result ToolCallResult
	unmarshalResult(t, resp, &result)

	if strings.Contains(result.Content[0].Text, "[rivet]") {
		t.Error("nudge should NOT appear before threshold")
	}
}

func TestLearnNudgeShownAtThreshold(t *testing.T) {
	s := newServerWithRecon(t)
	s.contextShown = true // simulate proper flow

	// 5 recon calls — at threshold.
	call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"recon.search","arguments":{"args":["foo"]}}}`)
	call(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"recon.grep","arguments":{"args":["bar"]}}}`)
	call(t, s, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"recon.related","arguments":{"args":["baz"]}}}`)
	call(t, s, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"recon.search","arguments":{"args":["qux"]}}}`)
	resp := call(t, s, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"recon.grep","arguments":{"args":["quux"]}}}`)

	var result ToolCallResult
	unmarshalResult(t, resp, &result)

	if !strings.Contains(result.Content[0].Text, "[rivet]") {
		t.Error("nudge should appear at threshold")
	}
	if !strings.Contains(result.Content[0].Text, "rivet-learner") {
		t.Error("nudge should mention rivet-learner agent")
	}
}

func TestLearnNudgeResetsAfterLearn(t *testing.T) {
	s := newServerWithRecon(t)
	s.contextShown = true // simulate proper flow

	// 5 recon calls to trigger nudge.
	for i := 0; i < 5; i++ {
		call(t, s, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"recon.search","arguments":{"args":["foo"]}}}`, i+1))
	}

	// Now call rivet.learn — should reset counter.
	call(t, s, `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"rivet.learn","arguments":{"title":"Test Finding","observation":"a non-obvious finding"}}}`)

	// Next recon call should NOT have nudge (counter reset to 0, now at 1).
	resp := call(t, s, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"recon.search","arguments":{"args":["foo"]}}}`)

	var result ToolCallResult
	unmarshalResult(t, resp, &result)

	if strings.Contains(result.Content[0].Text, "[rivet]") {
		t.Error("nudge should NOT appear after rivet.learn reset")
	}
}

// ---------------------------------------------------------------------------
// rivet.learn handler
// ---------------------------------------------------------------------------

func TestLearnWritesLearningFile(t *testing.T) {
	s := newServerWithRecon(t)

	resp := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"rivet.learn","arguments":{"title":"Retry Split","observation":"scheduler and adapter are separate","related_paths":["services/scheduler/**"]}}}`)

	var result ToolCallResult
	unmarshalResult(t, resp, &result)

	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "Recorded learning") {
		t.Errorf("expected 'Recorded learning' in response, got: %s", result.Content[0].Text)
	}

	entries, err := rivetctx.LoadLearnings(s.learningsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry in %s, got %d", s.learningsDir, len(entries))
	}
	if entries[0].Title != "Retry Split" {
		t.Errorf("title mismatch: %q", entries[0].Title)
	}
	if len(entries[0].RelatedPaths) != 1 || entries[0].RelatedPaths[0] != "services/scheduler/**" {
		t.Errorf("related_paths mismatch: %v", entries[0].RelatedPaths)
	}
}

func TestLearnRequiresTitleAndObservation(t *testing.T) {
	s := newServerWithRecon(t)

	resp := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"rivet.learn","arguments":{"observation":"missing title"}}}`)
	var result ToolCallResult
	unmarshalResult(t, resp, &result)
	if !result.IsError {
		t.Error("expected error for missing title")
	}

	resp = call(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"rivet.learn","arguments":{"title":"missing observation"}}}`)
	unmarshalResult(t, resp, &result)
	if !result.IsError {
		t.Error("expected error for missing observation")
	}
}

func TestLearnRejectsUnknownSuggestedDoc(t *testing.T) {
	s := newServerWithRecon(t)

	resp := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"rivet.learn","arguments":{"title":"t","observation":"o","suggested_doc":"not-a-real-doc"}}}`)
	var result ToolCallResult
	unmarshalResult(t, resp, &result)
	if !result.IsError {
		t.Error("expected error for unknown suggested_doc")
	}
}

// ---------------------------------------------------------------------------
// Error handling
// ---------------------------------------------------------------------------

func TestMethodNotFound(t *testing.T) {
	s := newTestServer(t)
	resp := call(t, s, `{"jsonrpc":"2.0","id":10,"method":"nonexistent","params":{}}`)

	if resp.Error == nil {
		t.Error("expected error for unknown method")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("expected error code -32601, got %d", resp.Error.Code)
	}
}

func TestParseError(t *testing.T) {
	s := newTestServer(t)
	resp := s.HandleMessage([]byte("not json"))

	if resp.Error == nil {
		t.Error("expected parse error")
	}
	if resp.Error.Code != -32700 {
		t.Errorf("expected error code -32700, got %d", resp.Error.Code)
	}
}

func TestNotificationReturnsNil(t *testing.T) {
	s := newTestServer(t)
	// Notification has no id field
	resp := s.HandleMessage([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	if resp != nil {
		t.Errorf("expected nil response for notification, got %+v", resp)
	}
}

// ---------------------------------------------------------------------------
// Serve (integration)
// ---------------------------------------------------------------------------

func TestServeMultipleMessages(t *testing.T) {
	s := newTestServer(t)

	messages := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{}}`,
	}, "\n") + "\n"

	in := strings.NewReader(messages)
	var out strings.Builder

	if err := s.Serve(in, &out); err != nil {
		t.Fatal(err)
	}

	// Should have 3 responses (notification gets no response)
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 response lines, got %d:\n%s", len(lines), out.String())
	}

	// Verify each is valid JSON-RPC
	for i, line := range lines {
		var resp Response
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Errorf("line %d: invalid JSON: %v", i, err)
		}
		if resp.Error != nil {
			t.Errorf("line %d: unexpected error: %+v", i, resp.Error)
		}
	}
}

func TestRallyPinTool(t *testing.T) {
	reg := capabilities.NewRegistry()
	exec := capabilities.NewExecutor(reg)
	pinReg := pins.NewRegistry()
	fake := newFakePinProvider("rally")
	pinReg.Add(fake)

	s := NewServer(reg, exec, nil, pinReg, nil, "test", true)

	resp := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"rally.pin","arguments":{"id":"RAL-1","note":"WIP"}}}`)
	var result ToolCallResult
	unmarshalResult(t, resp, &result)
	if result.IsError {
		t.Fatalf("rally.pin returned error: %+v", result.Content)
	}
	if note, ok := fake.pinned["RAL-1"]; !ok || note != "WIP" {
		t.Fatalf("expected RAL-1 pinned with note WIP, got %+v", fake.pinned)
	}

	resp = call(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"rally.unpin","arguments":{"id":"RAL-1"}}}`)
	unmarshalResult(t, resp, &result)
	if result.IsError {
		t.Fatalf("rally.unpin returned error: %+v", result.Content)
	}
	if _, ok := fake.pinned["RAL-1"]; ok {
		t.Fatal("RAL-1 should be unpinned")
	}
}

func TestRallyPinTool_MissingID(t *testing.T) {
	reg := capabilities.NewRegistry()
	exec := capabilities.NewExecutor(reg)
	pinReg := pins.NewRegistry()
	pinReg.Add(newFakePinProvider("rally"))

	s := NewServer(reg, exec, nil, pinReg, nil, "test", true)

	resp := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"rally.pin","arguments":{}}}`)
	var result ToolCallResult
	unmarshalResult(t, resp, &result)
	if !result.IsError {
		t.Fatal("expected IsError when id is missing")
	}
}

func TestRallyPinTool_NoRegistry(t *testing.T) {
	reg := capabilities.NewRegistry()
	exec := capabilities.NewExecutor(reg)

	s := NewServer(reg, exec, nil, nil, nil, "test", true)

	resp := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"rally.pin","arguments":{"id":"RAL-1"}}}`)
	var result ToolCallResult
	unmarshalResult(t, resp, &result)
	if !result.IsError {
		t.Fatal("expected IsError when pin registry is nil")
	}
}
