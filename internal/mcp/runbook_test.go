package mcp

import (
	"strings"
	"testing"

	rivetctx "github.com/djtouchette/rivet/internal/context"
)

func serverWithRunbooks(t *testing.T) *Server {
	t.Helper()
	s := newTestServer(t)
	s.SetWiki([]*rivetctx.Document{
		{Name: "onboarding", Kind: rivetctx.KindWiki, Title: "Onboarding", Body: "Welcome to the team."},
	})
	s.SetRunbooks([]*rivetctx.Document{
		{
			Name: "payment-recovery", Kind: rivetctx.KindRunbook,
			Title: "Payment webhook backlog recovery", Severity: "high",
			Triggers: []string{"payments failing", "webhook queue backlog"},
			Body:     "## Steps\n1. Scale workers.\n## Verification\nQueue drains.",
		},
	})
	return s
}

func TestHandleRunbook_FindBySymptom(t *testing.T) {
	s := serverWithRunbooks(t)
	resp := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"rivet.runbook","arguments":{"query":"payments are failing"}}}`)
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}
	var result ToolCallResult
	unmarshalResult(t, resp, &result)
	text := result.Content[0].Text
	// Returns the full procedure of the best match.
	if !strings.Contains(text, "Payment webhook backlog recovery") {
		t.Errorf("expected the matching runbook title, got: %s", text)
	}
	if !strings.Contains(text, "Scale workers") {
		t.Errorf("expected the procedure body, got: %s", text)
	}
}

func TestHandleRunbook_ListWhenNoQuery(t *testing.T) {
	s := serverWithRunbooks(t)
	resp := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"rivet.runbook","arguments":{}}}`)
	var result ToolCallResult
	unmarshalResult(t, resp, &result)
	text := result.Content[0].Text
	if !strings.Contains(text, "Available runbooks") || !strings.Contains(text, "payment-recovery") {
		t.Errorf("expected a runbook list, got: %s", text)
	}
	if !strings.Contains(text, "payments failing") {
		t.Errorf("list should show triggers, got: %s", text)
	}
}

func TestHandleRunbook_EmptySet(t *testing.T) {
	s := newTestServer(t) // no runbooks set
	resp := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"rivet.runbook","arguments":{"query":"anything"}}}`)
	var result ToolCallResult
	unmarshalResult(t, resp, &result)
	if !strings.Contains(result.Content[0].Text, "No runbooks found") {
		t.Errorf("expected no-runbooks message, got: %s", result.Content[0].Text)
	}
}

func TestHandleRunbook_NoMatch(t *testing.T) {
	s := serverWithRunbooks(t)
	resp := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"rivet.runbook","arguments":{"query":"kubernetes pod eviction"}}}`)
	var result ToolCallResult
	unmarshalResult(t, resp, &result)
	if !strings.Contains(result.Content[0].Text, "No runbook matches") {
		t.Errorf("expected no-match message, got: %s", result.Content[0].Text)
	}
}

func TestHandleRunbookDraft(t *testing.T) {
	s := serverWithRunbooks(t)
	// Point the runbooks dir at a temp location via cwd is awkward; instead
	// verify the handler validates required args and reports the draft path.
	resp := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"rivet.runbook-draft","arguments":{"title":"X"}}}`)
	var result ToolCallResult
	unmarshalResult(t, resp, &result)
	if !result.IsError || !strings.Contains(result.Content[0].Text, "required") {
		t.Errorf("missing steps should be an error, got: %+v", result)
	}
}

func TestResources_IncludeWikiAndRunbooks(t *testing.T) {
	s := serverWithRunbooks(t)

	// resources/list should include the wiki and runbook URIs.
	resp := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"resources/list","params":{}}`)
	var list resourcesListResult
	unmarshalResult(t, resp, &list)
	uris := map[string]bool{}
	for _, r := range list.Resources {
		uris[r.URI] = true
	}
	if !uris["rivet://wiki/onboarding"] {
		t.Errorf("wiki resource not listed: %v", uris)
	}
	if !uris["rivet://runbook/payment-recovery"] {
		t.Errorf("runbook resource not listed: %v", uris)
	}

	// resources/read should return a runbook's body.
	resp = call(t, s, `{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"rivet://runbook/payment-recovery"}}`)
	if resp.Error != nil {
		t.Fatalf("read error: %+v", resp.Error)
	}
	var read resourceReadResult
	unmarshalResult(t, resp, &read)
	if len(read.Contents) == 0 || !strings.Contains(read.Contents[0].Text, "Scale workers") {
		t.Errorf("runbook body not returned: %+v", read)
	}
}
