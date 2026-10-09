package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/djtouchette/rivet/internal/capabilities"
	rivetctx "github.com/djtouchette/rivet/internal/context"
)

// newDeliveryServer serves one doc far over the show budget and one small doc
// that states an answer, the shape that failed in practice: a 76 KB domain doc
// came back from context-show as a "result exceeds maximum allowed tokens"
// error, and recommend returned names without the text that answered.
func newDeliveryServer(t *testing.T) *Server {
	t.Helper()
	var b strings.Builder
	b.WriteString("# Certificates\n\nIntro.\n\n")
	for i := 0; i < 80; i++ {
		fmt.Fprintf(&b, "## Part %02d\n\n- marker-%02d %s\n\n", i, i, strings.Repeat("pdf layout notes ", 70))
	}
	b.WriteString("## Gotchas\n\n- Lab tests revoke the clinic's edit grant.\n")

	docs := []*rivetctx.Document{
		{Name: "certificates", Kind: rivetctx.KindDomain, Title: "Certificates", Body: b.String()},
		{Name: "authorization", Kind: rivetctx.KindDomain, Title: "Authorization", Body: "# Authorization\n\n## Gotchas\n\n" +
			"- **Owner certificate visibility is one filter.** An owner sees a certificate only once the lab result is negative.\n" +
			"- Grants are OR-unioned.\n"},
	}
	reg := capabilities.NewRegistry()
	s := NewServer(reg, capabilities.NewExecutor(reg), docs, nil, nil, "test", true)
	s.SetLearningsDir(t.TempDir())
	return s
}

func toolText(t *testing.T, s *Server, name string, args map[string]interface{}) (string, bool) {
	t.Helper()
	params, _ := json.Marshal(map[string]interface{}{"name": name, "arguments": args})
	resp := call(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":`+string(params)+`}`)
	if resp.Error != nil {
		t.Fatalf("%s: rpc error %+v", name, resp.Error)
	}
	var result ToolCallResult
	unmarshalResult(t, resp, &result)
	return result.Content[0].Text, result.IsError
}

func TestContextShowToolStaysUnderBudget(t *testing.T) {
	s := newDeliveryServer(t)

	text, isErr := toolText(t, s, "rivet.context-show", map[string]interface{}{"name": "certificates"})
	if isErr {
		t.Fatalf("over-budget doc should page, not error: %s", text)
	}
	if limit := rivetctx.DefaultShowBudget * 4; len(text) > limit {
		t.Fatalf("context-show returned %d bytes, over the %d-byte budget", len(text), limit)
	}
	for _, want := range []string{"Outline", "- Gotchas (~", `rivet.context-show {"name": "certificates", "page": 2}`, `"section": "<heading>"`} {
		if !strings.Contains(text, want) {
			t.Errorf("page 1 missing %q", want)
		}
	}
}

func TestContextShowToolSectionAndPageArgs(t *testing.T) {
	s := newDeliveryServer(t)

	text, isErr := toolText(t, s, "rivet.context-show", map[string]interface{}{"name": "certificates", "section": "gotchas"})
	if isErr || !strings.HasPrefix(text, "## Gotchas") || !strings.Contains(text, "revoke the clinic's edit grant") {
		t.Errorf("section fetch returned (err=%v) %q", isErr, text)
	}

	page2, isErr := toolText(t, s, "rivet.context-show", map[string]interface{}{"name": "certificates", "page": 2})
	if isErr || !strings.Contains(page2, "page 2 of") || strings.Contains(page2, "marker-00") {
		t.Errorf("page 2 should continue past page 1 (err=%v): %.300s", isErr, page2)
	}

	text, isErr = toolText(t, s, "rivet.context-show", map[string]interface{}{"name": "certificates", "section": "nope"})
	if !isErr || !strings.Contains(text, "Part 00") {
		t.Errorf("unknown section should be an error listing the outline, got (err=%v) %.200s", isErr, text)
	}
}

func TestContextRecommendToolReturnsExcerpts(t *testing.T) {
	s := newDeliveryServer(t)

	text, isErr := toolText(t, s, "rivet.context-recommend", map[string]interface{}{"query": "can an owner see a certificate before the lab result"})
	if isErr {
		t.Fatalf("recommend errored: %s", text)
	}
	if !strings.Contains(text, "Most relevant passages") || !strings.Contains(text, "── authorization › Gotchas") {
		t.Errorf("recommend should quote the matching passage with its heading path:\n%s", text)
	}
	if !strings.Contains(text, "Owner certificate visibility is one filter") {
		t.Errorf("the answering bullet is missing:\n%s", text)
	}

	names, _ := toolText(t, s, "rivet.context-recommend", map[string]interface{}{"query": "owner certificate lab result", "budget": 0})
	if strings.Contains(names, "Most relevant passages") {
		t.Error("budget 0 should return names only")
	}
}
