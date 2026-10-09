package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"strings"

	"github.com/djtouchette/rivet/internal/capabilities"
	rivetctx "github.com/djtouchette/rivet/internal/context"
	"github.com/djtouchette/rivet/internal/pins"
	"github.com/djtouchette/rivet/internal/policy"
)

const protocolVersion = "2024-11-05"

// ---------------------------------------------------------------------------
// JSON-RPC 2.0 types
// ---------------------------------------------------------------------------

// Request is a JSON-RPC 2.0 request or notification.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // nil for notifications
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ---------------------------------------------------------------------------
// MCP protocol types
// ---------------------------------------------------------------------------

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type initializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    serverCapabilities `json:"capabilities"`
	ServerInfo      serverInfo         `json:"serverInfo"`
}

type serverCapabilities struct {
	Tools     *toolsCapability     `json:"tools,omitempty"`
	Resources *resourcesCapability `json:"resources,omitempty"`
}

type toolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type resourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

// Tool is an MCP tool definition.
type Tool struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	InputSchema inputSchema      `json:"inputSchema"`
	Annotations *toolAnnotations `json:"annotations,omitempty"`
}

// toolAnnotations are MCP's behaviour hints (2025-03-26+). Clients use them
// to decide which calls need the user's go-ahead: Codex refuses every MCP
// call not marked read-only when its approval policy is "never" (codex exec),
// so without these a read-only lookup like rivet.intent is unusable there.
type toolAnnotations struct {
	ReadOnlyHint    *bool `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool `json:"idempotentHint,omitempty"`
}

func hint(b bool) *bool { return &b }

// statefulSafeTools are labelled [safe] (low-risk, no approval needed in
// rivet's own terms) but change state, so they must not claim read-only.
var statefulSafeTools = map[string]bool{"rally.pin": true, "rally.unpin": true}

// annotationsFor derives the MCP hints from rivet's safety level: safe means
// read-only, guarded means it writes but only adds, dangerous may destroy.
func annotationsFor(name string, safety capabilities.SafetyLevel) *toolAnnotations {
	switch {
	case safety == capabilities.SafetyLevelSafe && statefulSafeTools[name]:
		return &toolAnnotations{ReadOnlyHint: hint(false), DestructiveHint: hint(false), IdempotentHint: hint(true)}
	case safety == capabilities.SafetyLevelSafe:
		return &toolAnnotations{ReadOnlyHint: hint(true)}
	case safety == capabilities.SafetyLevelGuarded:
		return &toolAnnotations{ReadOnlyHint: hint(false), DestructiveHint: hint(false)}
	case safety == capabilities.SafetyLevelDangerous:
		return &toolAnnotations{ReadOnlyHint: hint(false), DestructiveHint: hint(true)}
	}
	return nil
}

// builtinSafety reads the "[safe]" / "[guarded]" / "[dangerous]" prefix every
// built-in tool description carries, so the hint can't drift from the label.
func builtinSafety(desc string) capabilities.SafetyLevel {
	for _, lvl := range []capabilities.SafetyLevel{capabilities.SafetyLevelSafe, capabilities.SafetyLevelGuarded, capabilities.SafetyLevelDangerous} {
		if strings.HasPrefix(desc, "["+string(lvl)+"]") {
			return lvl
		}
	}
	return ""
}

type inputSchema struct {
	Type       string                 `json:"type"`
	Properties map[string]interface{} `json:"properties,omitempty"`
	Required   []string               `json:"required,omitempty"`
}

type toolsListResult struct {
	Tools []Tool `json:"tools"`
}

type toolCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

// ToolCallResult is the result of a tools/call invocation.
type ToolCallResult struct {
	Content []ContentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ContentItem is a single content block in a tool result.
type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Resource is an MCP resource definition.
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

type resourcesListResult struct {
	Resources []Resource `json:"resources"`
}

type resourceReadParams struct {
	URI string `json:"uri"`
}

type resourceReadResult struct {
	Contents []resourceContent `json:"contents"`
}

type resourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
}

// ---------------------------------------------------------------------------
// Server
// ---------------------------------------------------------------------------

// Server is a Rivet MCP server that exposes capabilities as tools and
// context documents as resources over JSON-RPC 2.0 stdio.
// learnNudgeThreshold is the number of recon investigation calls
// before the server appends a learn nudge to tool responses.
const learnNudgeThreshold = 5

// contextFirstMessage is appended to the first recon tool response if
// Claude hasn't called rivet.context-show yet in this session.
const contextFirstMessage = "\n\n---\n[rivet] You're using recon tools without reading context docs first. " +
	"Call rivet.context-recommend with your task description, then rivet.context-show — " +
	"the answer may already be documented. Only use recon if context docs don't cover it."

// learnNudgeMessage is appended to recon tool responses when the
// threshold is reached without a rivet.learn call.
const learnNudgeMessage = "\n\n---\n[rivet] You have made multiple recon calls without recording findings. " +
	"You MUST record non-obvious findings — hidden dependencies, performance traps, " +
	"implicit ordering, gotchas — so future sessions don't rediscover them. " +
	"Call rivet.learn now with a title and observation; entries land in .rivet/learnings/ " +
	"and are later promoted into context docs."

// defaultLearningsDir is where the MCP server writes learning-log entries.
const defaultLearningsDir = ".rivet/learnings"

// promoteLearningsThreshold is the number of active (un-promoted) learning
// entries before the server nudges Claude to run a promotion review.
const promoteLearningsThreshold = 10

// promoteMessage is appended to the rivet.learn response when the learning log
// grows past the threshold.
const promoteMessage = `

---
[rivet] The learning log has %d active entries (threshold: %d). Review them and promote the high-value ones into context docs. Run /rivet-promote-learnings if your harness has it, or 'rivet learnings promote <name> --to <doc>' from the CLI, or inspect the entries in %s.`

// reconInvestigationTools are the recon tools that indicate active investigation
// (not just a refresh or overview).
var reconInvestigationTools = map[string]bool{
	"recon.grep":    true,
	"recon.search":  true,
	"recon.related": true,
	"recon.context": true,
	"recon.symbols": true,
	"recon.docs":    true,
}

type Server struct {
	registry     *capabilities.Registry
	executor     *capabilities.Executor
	contexts     []*rivetctx.Document
	wiki         []*rivetctx.Document // free-form reference docs (KindWiki)
	runbooks     []*rivetctx.Document // actionable procedures (KindRunbook)
	code         []*rivetctx.Document // docs extracted from code comments / .context/ sidecars (KindCode)
	intents      []*rivetctx.Document // business rules (KindIntent), shown apart from descriptive docs
	intentRoot   string               // project root scanned for rivet:intent markers
	intentOpts   rivetctx.ScanOptions // marker scan excludes
	intentDir    string               // where rivet.intent-propose files proposals
	pins         *pins.Registry
	policies     []policy.Rule
	version      string
	logger       *log.Logger
	autoCompact  bool              // whether to nudge promotion when the log gets long
	learningsDir string            // where rivet.learn writes entries
	semantic     rivetctx.Semantic // optional embedding-based recommend signal; nil = lexical-only

	// The client on the other end, as of initialize, and the live stream to
	// it during Serve (nil under HandleMessage). elicit overrides how the
	// person is asked, for tests.
	client clientState
	conn   *conn
	elicit Elicitor

	// Session state for nudging.
	reconCallsSinceLearn int
	contextShown         bool // true after rivet.context-show is called
}

// NewServer creates an MCP server backed by the given registry, executor,
// context documents, and pin registry. pinRegistry may be nil to disable
// pinned-resource exposure.
func NewServer(reg *capabilities.Registry, exec *capabilities.Executor, contexts []*rivetctx.Document, pinRegistry *pins.Registry, policies []policy.Rule, version string, autoCompact bool) *Server {
	return &Server{
		registry:     reg,
		executor:     exec,
		contexts:     contexts,
		pins:         pinRegistry,
		policies:     policies,
		version:      version,
		autoCompact:  autoCompact,
		learningsDir: defaultLearningsDir,
		intentRoot:   ".",
		intentDir:    rivetctx.IntentDir,
		logger:       log.New(io.Discard, "", 0),
	}
}

// SetIntent attaches business-rule docs (KindIntent). root is the project
// root the server scans for rivet:intent markers on demand — on demand, so a
// marker an agent adds mid-session is seen by its next call. Proposals are
// filed under root/.rivet/intent/proposals/.
func (s *Server) SetIntent(docs []*rivetctx.Document, root string, opts rivetctx.ScanOptions) {
	s.intents = docs
	s.intentRoot = root
	s.intentOpts = opts
	s.intentDir = filepath.Join(root, rivetctx.IntentDir)
}

// SetLearningsDir overrides the directory where rivet.learn writes entries.
// Used by tests; production code uses the default.
func (s *Server) SetLearningsDir(dir string) {
	s.learningsDir = dir
}

// SetSemantic attaches an optional embedding-based recommend signal. A nil
// scorer (the default) keeps rivet.context-recommend purely lexical.
func (s *Server) SetSemantic(sem rivetctx.Semantic) {
	s.semantic = sem
}

// SetWiki attaches free-form reference docs (KindWiki). They're exposed as
// resources and included — down-weighted — in context-recommend.
func (s *Server) SetWiki(docs []*rivetctx.Document) {
	s.wiki = docs
}

// SetRunbooks attaches actionable procedures (KindRunbook), surfaced through
// the rivet.runbook tool and as resources.
func (s *Server) SetRunbooks(docs []*rivetctx.Document) {
	s.runbooks = docs
}

// SetCodeDocs attaches docs extracted from rivet:context code comments and
// .context/ sidecar markdown (KindCode). They're included — slightly
// down-weighted — in context-recommend and exposed as resources.
func (s *Server) SetCodeDocs(docs []*rivetctx.Document) {
	s.code = docs
}

// SetLogger sets a logger for debug output (written to stderr, never stdout).
func (s *Server) SetLogger(l *log.Logger) {
	s.logger = l
}

// Serve runs the MCP server, reading JSON-RPC requests from in and writing
// responses to out. It blocks until in is closed or a read error occurs.
func (s *Server) Serve(in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	s.conn = &conn{scanner: scanner, out: out}
	defer func() { s.conn = nil }()

	for {
		line, ok := s.conn.next()
		if !ok {
			break
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			s.logger.Printf("invalid JSON-RPC: %v", err)
			s.writeResponse(out, Response{
				JSONRPC: "2.0",
				ID:      nil,
				Error:   &RPCError{Code: -32700, Message: "Parse error"},
			})
			continue
		}

		s.logger.Printf("-> %s (id=%s)", req.Method, string(req.ID))

		// Notifications (no id field) never get a response, and neither do
		// stray responses to requests of ours nobody is waiting on.
		if req.ID == nil || req.Method == "" {
			continue
		}

		s.conn.current = req.ID
		resp := s.handleRequest(&req)
		s.conn.current = nil
		s.writeResponse(out, *resp)
	}

	return scanner.Err()
}

// HandleMessage processes a single JSON-RPC request and returns the response.
// Exposed for testing. Returns nil for notifications.
func (s *Server) HandleMessage(msg []byte) *Response {
	var req Request
	if err := json.Unmarshal(msg, &req); err != nil {
		return &Response{
			JSONRPC: "2.0",
			ID:      nil,
			Error:   &RPCError{Code: -32700, Message: "Parse error"},
		}
	}
	if req.ID == nil {
		return nil
	}
	return s.handleRequest(&req)
}

func (s *Server) writeResponse(out io.Writer, resp Response) {
	if s.conn != nil {
		if err := s.conn.write(resp); err != nil {
			s.logger.Printf("write error: %v", err)
		}
		return
	}
	data, err := json.Marshal(resp)
	if err != nil {
		s.logger.Printf("marshal error: %v", err)
		return
	}
	out.Write(data)
	out.Write([]byte("\n"))
}

func (s *Server) handleRequest(req *Request) *Response {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "ping":
		return s.handlePing(req)
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(req)
	case "resources/list":
		return s.handleResourcesList(req)
	case "resources/read":
		return s.handleResourcesRead(req)
	default:
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: -32601, Message: fmt.Sprintf("Method not found: %s", req.Method)},
		}
	}
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func (s *Server) handleInitialize(req *Request) *Response {
	var params initializeParams
	_ = json.Unmarshal(req.Params, &params)
	s.client = clientState{
		version:     negotiateVersion(params.ProtocolVersion),
		elicitation: params.Capabilities.Elicitation != nil,
		name:        params.ClientInfo.Name,
	}
	// Codex sends name "codex-mcp-client" and title "Codex"; the title is
	// what a person reading an approval record recognises.
	if params.ClientInfo.Title != "" {
		s.client.name = params.ClientInfo.Title
	}
	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: initializeResult{
			ProtocolVersion: s.client.version,
			Capabilities: serverCapabilities{
				Tools:     &toolsCapability{},
				Resources: &resourcesCapability{},
			},
			ServerInfo: serverInfo{Name: "rivet", Version: s.version},
		},
	}
}

func (s *Server) handlePing(req *Request) *Response {
	return &Response{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{}}
}

func (s *Server) handleToolsList(req *Request) *Response {
	caps := s.registry.List()
	tools := make([]Tool, 0, len(caps)+2)

	// Built-in rivet tools for context discovery.
	tools = append(tools,
		Tool{
			Name:        "rivet.context-list",
			Description: "[safe] List all available context documents (domains, modules, paradigms)",
			InputSchema: inputSchema{Type: "object"},
		},
		Tool{
			Name:        "rivet.context-show",
			Description: fmt.Sprintf("[safe] Show a context document by name. Output is capped at ~%d tokens: a larger doc returns an outline of its sections with sizes plus the opening sections — then pass 'section' to read one section, or 'page' to continue.", rivetctx.DefaultShowBudget),
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Name of the context document to show (e.g. 'billing', 'sql-views')",
					},
					"section": map[string]interface{}{
						"type":        "string",
						"description": "Optional. Return only the section under this heading, with its subsections (case-insensitive; a heading path like 'Gotchas > Owner visibility' also works)",
					},
					"page": map[string]interface{}{
						"type":        "integer",
						"description": "Optional. 1-based page of a doc or section that is over the size cap; the response says how many pages there are",
					},
				},
			},
		},
		Tool{
			Name:        "rivet.context-recommend",
			Description: "[safe] Recommend context documents for a task, file path, or keywords. Returns the ranked docs AND the passages from them that best match the query (heading path + text) — often the answer itself. Read more of a doc with rivet.context-show and its 'section' argument.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "A task description, file path, or keywords (e.g. 'investigate billing retries', 'backend/Handlers/PaymentGateway/src/App.cs')",
					},
					"budget": map[string]interface{}{
						"type":        "integer",
						"description": fmt.Sprintf("Optional. Token budget for the quoted passages (default %d, max %d; 0 = names only)", rivetctx.DefaultExcerptBudget, maxMCPExcerptBudget),
					},
				},
			},
		},
		Tool{
			Name:        "rivet.runbook",
			Description: "[safe] Find the operational runbook for a symptom or situation (e.g. 'payments are failing', 'deploy rollback'). Returns the matching procedure — steps, verification, rollback — to follow. Call with no query to list all available runbooks. Runbooks are guidance: run any commands in them through your normal, overseen tools.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "A symptom, alert, or situation (e.g. 'webhook queue backing up', 'rotate db credentials'). Omit to list all runbooks.",
					},
				},
			},
		},
		Tool{
			Name:        "rivet.learn",
			Description: "[guarded] Record a non-obvious finding to the learning log at .rivet/learnings/. One file per entry (parallel-safe). Entries are later promoted into context docs via /rivet-promote-learnings.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"title": map[string]interface{}{
						"type":        "string",
						"description": "Short, specific title. Example: 'ServiceRenderedInsertTrigger fires 5 queries per insert'",
					},
					"observation": map[string]interface{}{
						"type":        "string",
						"description": "What you found — the non-obvious fact.",
					},
					"impact": map[string]interface{}{
						"type":        "string",
						"description": "Why it matters / where it bites (optional).",
					},
					"recommendation": map[string]interface{}{
						"type":        "string",
						"description": "What future sessions should do about it (optional).",
					},
					"related_paths": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Glob patterns pointing to affected source files (optional).",
					},
					"suggested_doc": map[string]interface{}{
						"type":        "string",
						"description": "Context doc name this learning is a candidate to promote into (optional).",
					},
					"confidence": map[string]interface{}{
						"type":        "string",
						"description": "low | medium | high (optional).",
					},
					"author": map[string]interface{}{
						"type":        "string",
						"description": "Author of the entry (optional).",
					},
				},
				Required: []string{"title", "observation"},
			},
		},
		Tool{
			Name:        "rivet.runbook-draft",
			Description: "[guarded] Draft an operational runbook after working through a novel problem. Writes to .rivet/runbooks/drafts/ for HUMAN review — drafts are NOT retrievable via rivet.runbook until a person promotes them. Use 'triggers' (the symptoms that should surface this runbook) and write concrete, verified steps.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"title": map[string]interface{}{
						"type":        "string",
						"description": "Short, specific title. Example: 'Payment webhook backlog recovery'",
					},
					"triggers": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Symptoms/alerts that should surface this runbook (e.g. 'payments failing', 'webhook queue backlog').",
					},
					"steps": map[string]interface{}{
						"type":        "string",
						"description": "The ordered procedure (markdown). Each step: the action and its expected result.",
					},
					"verification": map[string]interface{}{
						"type":        "string",
						"description": "How to confirm the procedure worked (optional).",
					},
					"rollback": map[string]interface{}{
						"type":        "string",
						"description": "How to undo it if needed (optional).",
					},
					"severity": map[string]interface{}{
						"type":        "string",
						"description": "low | medium | high | critical (optional).",
					},
					"owner": map[string]interface{}{
						"type":        "string",
						"description": "Team responsible (optional).",
					},
				},
				Required: []string{"title", "steps"},
			},
		},
		Tool{
			Name: "rivet.intent",
			Description: "[safe] Business rules (intent) the code must keep — ratified by people, so they are requirements, not descriptions. " +
				"Call BEFORE changing business logic. Give exactly one of: 'path' (rules governing a file and markers in it), 'id' (one rule, e.g. 'BIL-001', with the code and tests enforcing it), " +
				"'query' (rules relevant to a task), or 'changes'/'since'/'staged' (rules your change touches and the tests that verify them — run those tests). No arguments lists every intent doc. " +
				"If a change would break a rule, stop and ask the user. To write or change a rule, draft it with rivet.intent-propose for the user to approve; never edit .rivet/intent/ directly.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "A file path relative to the project root (e.g. 'services/billing/invoice.go').",
					},
					"id": map[string]interface{}{
						"type":        "string",
						"description": "A rule ID (e.g. 'BIL-001').",
					},
					"query": map[string]interface{}{
						"type":        "string",
						"description": "A task description or keywords (e.g. 'refund an issued invoice').",
					},
					"changes": map[string]interface{}{
						"type":        "boolean",
						"description": "Analyse the working tree (staged, unstaged, untracked) against HEAD.",
					},
					"since": map[string]interface{}{
						"type":        "string",
						"description": "Analyse every change since this git ref's merge-base with HEAD (e.g. 'main').",
					},
					"staged": map[string]interface{}{
						"type":        "boolean",
						"description": "Analyse staged changes only.",
					},
				},
			},
		},
		Tool{
			Name: "rivet.intent-propose",
			Description: "[guarded] Draft a business rule — add a new one, amend one, or retire one — for a PERSON to approve. " +
				"Writes a complete, ready-to-apply proposal to .rivet/intent/proposals/; it is not a rule until the user approves it with 'rivet intent approve' in their terminal, so the code must keep meeting the current rules meanwhile. " +
				"Use it when the user asks you to write or change a rule, when you find an unwritten rule the code clearly depends on, or when code and a rule conflict and the rule looks wrong. " +
				"New rules get the next free ID automatically. Write the statement as a requirement in business terms, and always give the business reason. Never edit .rivet/intent/ directly and never run approve yourself.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"change": map[string]interface{}{
						"type":        "string",
						"enum":        []string{rivetctx.ProposeAdd, rivetctx.ProposeAmend, rivetctx.ProposeRetire},
						"description": "add | amend | retire",
					},
					"doc": map[string]interface{}{
						"type":        "string",
						"description": "Intent doc to add the rule to (e.g. 'intent/billing'); a name that doesn't exist yet creates that doc on approval. For amend/retire it defaults to the doc defining rule_id.",
					},
					"rule_id": map[string]interface{}{
						"type":        "string",
						"description": "The rule to amend or retire (e.g. 'BIL-010'). Omit for add.",
					},
					"class": map[string]interface{}{
						"type":        "string",
						"enum":        []string{string(rivetctx.RuleInvariant), string(rivetctx.RulePolicy)},
						"description": "invariant (must always hold; breaking it is a defect) or policy (a business decision that may change). Defaults to invariant for add, the current class for amend.",
					},
					"statement": map[string]interface{}{
						"type":        "string",
						"description": "The rule as it should read, one or two sentences (required for add and amend).",
					},
					"why": map[string]interface{}{
						"type":        "string",
						"description": "The business reason — for retire, why it no longer applies. Say who asked if a person did.",
					},
					"enforcement": map[string]interface{}{
						"type":        "string",
						"description": "Optional. 'manual — <who or what checks it>' for a rule enforced outside the code, or 'pending' for a known gap. Omit when code will enforce it.",
					},
					"scope": map[string]interface{}{
						"type":        "string",
						"enum":        []string{string(rivetctx.IntentScopeDomain), string(rivetctx.IntentScopeCrossCutting)},
						"description": "Only when adding to a new doc: domain (default) or cross-cutting.",
					},
					"evidence": map[string]interface{}{
						"type":        "string",
						"description": "What prompted it: the code (file:line), a ticket, the user's words (optional).",
					},
					"author": map[string]interface{}{
						"type":        "string",
						"description": "Who is proposing (optional).",
					},
				},
				Required: []string{"change", "why"},
			},
		},
		Tool{
			Name: "rivet.intent-approve",
			Description: "[guarded] Ask the USER to ratify a drafted intent proposal. Rivet shows them the exact change in a confirmation prompt from their MCP client; " +
				"only their answer can approve it — you cannot see or answer that prompt, and nothing you pass can approve on their behalf. " +
				"Call it when the user wants to approve a proposal (after rivet.intent-propose, or one listed by 'rivet intent proposals'). " +
				"If they decline or dismiss it, nothing changes; ask whether they want it redrafted or rejected.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"proposal": map[string]interface{}{
						"type":        "string",
						"description": "The proposal name from rivet.intent-propose's reply (e.g. 'add-bil-012-46d767'), or its short suffix ('46d767').",
					},
				},
				Required: []string{"proposal"},
			},
		},
		Tool{
			Name:        "rally.pin",
			Description: "[safe] Pin a rally ticket so it stays injected into chat context across turns. Use when starting work on a ticket the user has named.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Ticket ID (e.g. 'RAL-123', 'PROJ-7')",
					},
					"note": map[string]interface{}{
						"type":        "string",
						"description": "Optional short note for why this ticket is pinned",
					},
				},
				Required: []string{"id"},
			},
		},
		Tool{
			Name:        "rally.unpin",
			Description: "[safe] Remove a rally ticket from the pinned set so it stops being injected into chat context.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Ticket ID to unpin",
					},
				},
				Required: []string{"id"},
			},
		},
	)

	for i := range tools {
		tools[i].Annotations = annotationsFor(tools[i].Name, builtinSafety(tools[i].Description))
	}

	// Registered capabilities.
	for _, cap := range caps {
		tool := Tool{
			Name:        cap.Name,
			Description: toolDescription(&cap),
			InputSchema: buildCapabilitySchema(&cap),
			Annotations: annotationsFor(cap.Name, cap.Safety),
		}

		tools = append(tools, tool)
	}

	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  toolsListResult{Tools: tools},
	}
}

func (s *Server) handleToolsCall(req *Request) *Response {
	var params toolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: -32602, Message: "Invalid params: " + err.Error()},
		}
	}

	// Route built-in rivet tools.
	switch params.Name {
	case "rivet.context-list":
		return s.handleContextList(req)
	case "rivet.context-show":
		s.contextShown = true
		name, _ := params.Arguments["name"].(string)
		section, _ := params.Arguments["section"].(string)
		page, _ := intArg(params.Arguments, "page")
		return s.handleContextShow(req, name, section, page)
	case "rivet.context-recommend":
		query, _ := params.Arguments["query"].(string)
		budget, ok := intArg(params.Arguments, "budget")
		if !ok {
			budget = rivetctx.DefaultExcerptBudget
		}
		return s.handleContextRecommend(req, query, budget)
	case "rivet.runbook":
		query, _ := params.Arguments["query"].(string)
		return s.handleRunbook(req, query)
	case "rivet.runbook-draft":
		return s.handleRunbookDraft(req, params.Arguments)
	case "rivet.learn":
		s.reconCallsSinceLearn = 0
		return s.handleLearn(req, params.Arguments)
	case "rivet.intent":
		return s.handleIntent(req, params.Arguments)
	case "rivet.intent-propose":
		return s.handleIntentPropose(req, params.Arguments)
	case "rivet.intent-approve":
		return s.handleIntentApprove(req, params.Arguments)
	case "rally.pin":
		id, _ := params.Arguments["id"].(string)
		note, _ := params.Arguments["note"].(string)
		return s.handlePin(req, "rally", id, note)
	case "rally.unpin":
		id, _ := params.Arguments["id"].(string)
		return s.handleUnpin(req, "rally", id)
	}

	// Track recon investigation calls for learn nudging.
	if reconInvestigationTools[params.Name] {
		s.reconCallsSinceLearn++
	}

	// Route registered capabilities through the executor.
	cap := s.registry.Get(params.Name)

	args, approved := buildArgsFromParams(cap, params.Arguments)

	// Check policy rules before execution.
	if len(s.policies) > 0 && cap != nil {
		if violations := policy.Check(s.policies, cap, nil); len(violations) > 0 {
			return &Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: ToolCallResult{
					Content: []ContentItem{{Type: "text", Text: fmt.Sprintf("Blocked by policy: %s", policy.FormatViolations(violations))}},
					IsError: true,
				},
			}
		}
	}

	result, err := s.executor.Run(context.Background(), params.Name, args, approved)
	if err != nil {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: ToolCallResult{
				Content: []ContentItem{{Type: "text", Text: fmt.Sprintf("Error: %v", err)}},
				IsError: true,
			},
		}
	}

	text := result.Stdout
	if result.Stderr != "" {
		text += "\n--- stderr ---\n" + result.Stderr
	}
	if result.ExitCode != 0 {
		text += fmt.Sprintf("\n(exit code: %d)", result.ExitCode)
	}

	// A missing witness payload is an unknown coverage result, never success.
	// Keep this guard even when a dependency regresses its writer capture.
	// rivet:intent FC-001
	emptyWitness := strings.HasPrefix(params.Name, "witness.") && strings.TrimSpace(text) == ""
	if emptyWitness {
		text = "Witness returned no output; test coverage is unproven. Use witness.select to inspect the selection or run a verified project suite."
	}

	// Append nudges to recon investigation responses.
	if reconInvestigationTools[params.Name] {
		if !s.contextShown && s.reconCallsSinceLearn == 2 {
			// 2 recon calls without reading context docs — gentle nudge.
			text += contextFirstMessage
		} else if s.reconCallsSinceLearn >= learnNudgeThreshold {
			// 5+ recon calls without recording findings — nudge to learn.
			text += learnNudgeMessage
		}
	}

	content := []ContentItem{{Type: "text", Text: text}}
	// Rule-aware test selection rides along with witness's own, in a separate
	// content block so witness's JSON stays parseable on its own.
	if note := s.witnessIntentNote(params.Name, args); note != "" {
		content = append(content, ContentItem{Type: "text", Text: note})
	}

	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: ToolCallResult{
			Content: content,
			// A non-zero exit is a failed tool call and has to be flagged as one.
			// Every in-process runner reserves non-zero for "the command did not
			// do what you asked": witness exits 1 rather than print a test
			// command it cannot get right, and left unflagged that arrives as an
			// ordinary result whose body merely ends in "(exit code: 1)" —
			// indistinguishable, to a client that reads isError, from a tool that
			// ran and found nothing to do.
			IsError: result.ExitCode != 0 || emptyWitness,
		},
	}
}

// allDocs returns every retrievable document across tiers (context + wiki +
// runbooks) for resource listing/reading.
func (s *Server) allDocs() []*rivetctx.Document {
	all := make([]*rivetctx.Document, 0, len(s.contexts)+len(s.wiki)+len(s.runbooks)+len(s.code))
	all = append(all, s.contexts...)
	all = append(all, s.wiki...)
	all = append(all, s.runbooks...)
	all = append(all, s.code...)
	all = append(all, s.intents...)
	return all
}

func (s *Server) handleResourcesList(req *Request) *Response {
	resources := make([]Resource, 0, len(s.contexts)+len(s.wiki)+len(s.runbooks))
	for _, doc := range s.allDocs() {
		resources = append(resources, Resource{
			URI:         doc.URI(),
			Name:        doc.Title,
			Description: fmt.Sprintf("%s: %s", doc.Kind, doc.Name),
			MimeType:    "text/markdown",
		})
	}
	if s.pins != nil {
		items, err := s.pins.List()
		if err != nil {
			s.logger.Printf("pins list: %v", err)
		}
		for _, it := range items {
			resources = append(resources, Resource{
				URI:         it.URI,
				Name:        it.Name,
				Description: it.Description,
				MimeType:    it.MimeType,
			})
		}
	}
	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  resourcesListResult{Resources: resources},
	}
}

func (s *Server) handleResourcesRead(req *Request) *Response {
	var params resourceReadParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: -32602, Message: "Invalid params: " + err.Error()},
		}
	}

	for _, doc := range s.allDocs() {
		if doc.URI() == params.URI {
			return &Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: resourceReadResult{
					Contents: []resourceContent{{
						URI:      doc.URI(),
						MimeType: "text/markdown",
						Text:     doc.Body,
					}},
				},
			}
		}
	}

	if s.pins != nil && s.pins.Has(params.URI) {
		item, err := s.pins.Read(params.URI)
		if err != nil {
			return &Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &RPCError{Code: -32603, Message: err.Error()},
			}
		}
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: resourceReadResult{
				Contents: []resourceContent{{
					URI:      item.URI,
					MimeType: item.MimeType,
					Text:     item.Body,
				}},
			},
		}
	}

	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Error:   &RPCError{Code: -32602, Message: fmt.Sprintf("Resource not found: %s", params.URI)},
	}
}

// ---------------------------------------------------------------------------
// Built-in rivet tool handlers
// ---------------------------------------------------------------------------

func (s *Server) handleContextList(req *Request) *Response {
	if len(s.contexts) == 0 && len(s.intents) == 0 {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  ToolCallResult{Content: []ContentItem{{Type: "text", Text: "No context documents found.\nAdd markdown files to .rivet/context/{domains,modules,paradigms}/"}}},
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%-25s %-12s %s\n", "NAME", "KIND", "TITLE")
	for _, doc := range s.contexts {
		fmt.Fprintf(&b, "%-25s %-12s %s\n", doc.Name, doc.Kind, doc.Title)
	}
	for _, doc := range s.intents {
		fmt.Fprintf(&b, "%-25s %-12s %s\n", doc.Name, doc.Kind, doc.Title)
	}
	if len(s.intents) > 0 {
		b.WriteString("\nintent docs are business rules — use rivet.intent to see which govern a file or a change.\n")
	}

	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  ToolCallResult{Content: []ContentItem{{Type: "text", Text: b.String()}}},
	}
}

// maxMCPExcerptBudget caps the excerpt budget an agent may ask for, so a
// generous request cannot push a recommend result past the client's own
// tool-output limit.
const maxMCPExcerptBudget = 12000

// intArg reads an integer argument. JSON numbers arrive as float64; some
// clients send numbers as strings, which are accepted too.
func intArg(args map[string]interface{}, key string) (int, bool) {
	switch v := args[key].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case string:
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &n); err == nil {
			return n, true
		}
	}
	return 0, false
}

func (s *Server) handleContextShow(req *Request, name, section string, page int) *Response {
	if name == "" {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: ToolCallResult{
				Content: []ContentItem{{Type: "text", Text: "Error: 'name' argument is required"}},
				IsError: true,
			},
		}
	}

	// Search every tier. context-recommend pools wiki and code-extracted docs
	// alongside curated ones, so restricting this to s.contexts meant an agent
	// that followed a recommendation for anything else got "not found".
	all := s.allDocs()
	for _, doc := range all {
		if doc.Name == name {
			// Outgoing links are appended so the agent can walk the graph —
			// a domain doc pointing at the module doc that explains a detail
			// is useless if the pointer isn't followable. Show keeps the
			// result under budget: a 76 KB doc returned whole exceeded
			// Claude Code's tool-output limit and came back as an error.
			text, err := rivetctx.Show(doc, all, rivetctx.ShowOptions{Section: section, Page: page, MCP: true})
			if err != nil {
				return &Response{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result: ToolCallResult{
						Content: []ContentItem{{Type: "text", Text: "Error: " + err.Error()}},
						IsError: true,
					},
				}
			}
			return &Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  ToolCallResult{Content: []ContentItem{{Type: "text", Text: text}}},
			}
		}
	}

	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: ToolCallResult{
			Content: []ContentItem{{Type: "text", Text: fmt.Sprintf("Error: context document %q not found. Use rivet.context-list to see available documents.", name)}},
			IsError: true,
		},
	}
}

func (s *Server) handleContextRecommend(req *Request, query string, budget int) *Response {
	if query == "" {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: ToolCallResult{
				Content: []ContentItem{{Type: "text", Text: "Error: 'query' argument is required"}},
				IsError: true,
			},
		}
	}

	var opts []rivetctx.Option
	if s.semantic != nil {
		opts = append(opts, rivetctx.WithSemantic(s.semantic))
	}
	// Include wiki reference docs and code-extracted docs (both down-weighted
	// by kind) alongside curated context docs; runbooks have their own
	// rivet.runbook tool.
	pool := append(append([]*rivetctx.Document{}, s.contexts...), s.wiki...)
	pool = append(pool, s.code...)
	if budget > maxMCPExcerptBudget {
		budget = maxMCPExcerptBudget
	}
	recs := rivetctx.Recommend(pool, query, 5, append(opts, rivetctx.WithExcerpts(budget))...)

	// Business rules are listed first and apart: they constrain the change,
	// where everything below only explains the code.
	intentRecs := rivetctx.RecommendIntent(s.intents, query, 3, opts...)

	if len(recs) == 0 && len(intentRecs) == 0 {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  ToolCallResult{Content: []ContentItem{{Type: "text", Text: fmt.Sprintf("No context documents match %q", query)}}},
		}
	}

	var sb strings.Builder
	sb.WriteString(rivetctx.FormatIntentRecommendations(intentRecs))
	sb.WriteString(rivetctx.FormatRecommendations(query, recs, true))
	sb.WriteString(semanticCaveat(s.semantic))

	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  ToolCallResult{Content: []ContentItem{{Type: "text", Text: sb.String()}}},
	}
}

// handleRunbook implements the rivet.runbook tool. With a query it finds the
// runbook(s) whose triggers/content best match the symptom and returns the
// top match's full procedure (plus alternatives). With no query it lists the
// available runbooks so the agent can see what's covered.
func (s *Server) handleRunbook(req *Request, query string) *Response {
	text := func(body string) *Response {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  ToolCallResult{Content: []ContentItem{{Type: "text", Text: body}}},
		}
	}

	if len(s.runbooks) == 0 {
		return text("No runbooks found. Add markdown procedures to .rivet/runbooks/ (with `triggers:` frontmatter so they can be found by symptom).")
	}

	// No query → list what's available.
	if strings.TrimSpace(query) == "" {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Available runbooks (%d):\n\n", len(s.runbooks)))
		for _, rb := range s.runbooks {
			sb.WriteString(fmt.Sprintf("  %s — %s\n", rb.Name, rb.Title))
			if len(rb.Triggers) > 0 {
				sb.WriteString(fmt.Sprintf("        triggers: %s\n", strings.Join(rb.Triggers, "; ")))
			}
			sb.WriteString(fmt.Sprintf("        uri: %s\n", rb.URI()))
		}
		sb.WriteString("\nCall rivet.runbook with a symptom (e.g. \"payments failing\") to get the matching procedure.")
		return text(sb.String())
	}

	var opts []rivetctx.Option
	if s.semantic != nil {
		opts = append(opts, rivetctx.WithSemantic(s.semantic))
	}
	matches := rivetctx.RecommendRunbooks(s.runbooks, query, 5, opts...)
	if len(matches) == 0 {
		return text(fmt.Sprintf("No runbook matches %q. Use rivet.runbook with no query to list all runbooks.", query))
	}

	var sb strings.Builder
	best := matches[0]
	sb.WriteString(fmt.Sprintf("Runbook for %q: %s (score %.2f)\n", query, best.Title, best.Score))
	if best.Severity != "" {
		sb.WriteString(fmt.Sprintf("severity: %s\n", best.Severity))
	}
	if len(best.Triggers) > 0 {
		sb.WriteString(fmt.Sprintf("triggers: %s\n", strings.Join(best.Triggers, "; ")))
	}
	sb.WriteString(fmt.Sprintf("uri: %s\n\n", best.URI))
	sb.WriteString(best.Document.Body)

	if len(matches) > 1 {
		sb.WriteString("\n\n---\nOther possibly-relevant runbooks:\n")
		for _, m := range matches[1:] {
			sb.WriteString(fmt.Sprintf("  %.2f  %s — %s\n", m.Score, m.Name, m.Title))
		}
	}
	sb.WriteString(semanticCaveat(s.semantic))
	return text(sb.String())
}

// semanticCaveat returns a caveat line when an embedding backend was configured
// but failed to answer, so the agent knows this ranking is degraded rather than
// authoritative. It returns "" when semantic scoring is off (normal) or working.
//
// This is the same contract as recon's import_stats.unresolved and
// file_parse.status: a weaker answer must announce itself, because an agent
// cannot tell a lexical-only ranking from a semantic one by looking at it, and
// will otherwise treat "the best I could do without embeddings" as "the best
// match that exists".
//
// The Semantic interface deliberately does not carry Err — package context has
// no business knowing about embedder transport failures — so this type-asserts
// for it and stays silent for implementations that do not report one.
func semanticCaveat(sem rivetctx.Semantic) string {
	reporter, ok := sem.(interface{ Err() error })
	if !ok || reporter == nil {
		return ""
	}
	err := reporter.Err()
	if err == nil {
		return ""
	}
	return fmt.Sprintf("\nCaveat: semantic ranking was configured but unavailable (%v), so these results are "+
		"lexical-only and may miss documents that match in meaning rather than wording. "+
		"Treat a weak or empty result as inconclusive, and search the source directly.\n", err)
}

// handleRunbookDraft implements rivet.runbook-draft: the agent drafts a runbook
// into .rivet/runbooks/drafts/ for human promotion. The draft is deliberately
// not retrievable until a person reviews and promotes it.
func (s *Server) handleRunbookDraft(req *Request, args map[string]interface{}) *Response {
	errResp := func(msg string) *Response {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  ToolCallResult{Content: []ContentItem{{Type: "text", Text: "Error: " + msg}}, IsError: true},
		}
	}

	title, _ := args["title"].(string)
	steps, _ := args["steps"].(string)
	if strings.TrimSpace(title) == "" || strings.TrimSpace(steps) == "" {
		return errResp("'title' and 'steps' are required")
	}

	path, err := rivetctx.CreateRunbookDraft(rivetctx.RunbooksDir, rivetctx.NewRunbook{
		Title:        title,
		Triggers:     stringSliceArg(args, "triggers"),
		Severity:     stringArg(args, "severity"),
		Owner:        stringArg(args, "owner"),
		Steps:        steps,
		Verification: stringArg(args, "verification"),
		Rollback:     stringArg(args, "rollback"),
	})
	if err != nil {
		return errResp(err.Error())
	}

	msg := fmt.Sprintf("Drafted runbook at %s.\n\nThis is a DRAFT — it won't be found via rivet.runbook until a human reviews and promotes it with `rivet runbook promote`. Tell the user a draft is ready for review.", path)
	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  ToolCallResult{Content: []ContentItem{{Type: "text", Text: msg}}},
	}
}

func (s *Server) handlePin(req *Request, source, id, note string) *Response {
	if id == "" {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: ToolCallResult{
				Content: []ContentItem{{Type: "text", Text: "Error: 'id' argument is required"}},
				IsError: true,
			},
		}
	}
	if s.pins == nil {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: ToolCallResult{
				Content: []ContentItem{{Type: "text", Text: "Error: pin registry not configured"}},
				IsError: true,
			},
		}
	}
	w, ok := s.pins.WriterFor(source)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: ToolCallResult{
				Content: []ContentItem{{Type: "text", Text: fmt.Sprintf("Error: source %q does not support pin writes", source)}},
				IsError: true,
			},
		}
	}
	if err := w.Pin(id, note); err != nil {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: ToolCallResult{
				Content: []ContentItem{{Type: "text", Text: fmt.Sprintf("Error: %v", err)}},
				IsError: true,
			},
		}
	}
	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  ToolCallResult{Content: []ContentItem{{Type: "text", Text: fmt.Sprintf("Pinned %s", id)}}},
	}
}

func (s *Server) handleUnpin(req *Request, source, id string) *Response {
	if id == "" {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: ToolCallResult{
				Content: []ContentItem{{Type: "text", Text: "Error: 'id' argument is required"}},
				IsError: true,
			},
		}
	}
	if s.pins == nil {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: ToolCallResult{
				Content: []ContentItem{{Type: "text", Text: "Error: pin registry not configured"}},
				IsError: true,
			},
		}
	}
	w, ok := s.pins.WriterFor(source)
	if !ok {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: ToolCallResult{
				Content: []ContentItem{{Type: "text", Text: fmt.Sprintf("Error: source %q does not support pin writes", source)}},
				IsError: true,
			},
		}
	}
	if err := w.Unpin(id); err != nil {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: ToolCallResult{
				Content: []ContentItem{{Type: "text", Text: fmt.Sprintf("Error: %v", err)}},
				IsError: true,
			},
		}
	}
	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  ToolCallResult{Content: []ContentItem{{Type: "text", Text: fmt.Sprintf("Unpinned %s", id)}}},
	}
}

func (s *Server) handleLearn(req *Request, args map[string]interface{}) *Response {
	title := strings.TrimSpace(stringArg(args, "title"))
	observation := strings.TrimSpace(stringArg(args, "observation"))

	errResp := func(msg string) *Response {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: ToolCallResult{
				Content: []ContentItem{{Type: "text", Text: msg}},
				IsError: true,
			},
		}
	}

	if title == "" {
		return errResp("Error: 'title' argument is required")
	}
	if observation == "" {
		return errResp("Error: 'observation' argument is required")
	}

	// Accept either a suggested_doc hint or the legacy 'doc' arg.
	suggested := stringArg(args, "suggested_doc")
	if suggested == "" {
		suggested = stringArg(args, "doc")
	}
	if suggested != "" {
		known := false
		for _, d := range s.contexts {
			if d.Name == suggested {
				known = true
				break
			}
		}
		if !known {
			return errResp(fmt.Sprintf("Error: suggested_doc %q not found. Use rivet.context-list to see available documents, or omit suggested_doc.", suggested))
		}
	}

	entry, err := rivetctx.CreateLearning(s.learningsDir, rivetctx.NewLearning{
		Title:          title,
		Author:         stringArg(args, "author"),
		Confidence:     stringArg(args, "confidence"),
		SuggestedDoc:   suggested,
		RelatedPaths:   stringSliceArg(args, "related_paths"),
		Observation:    observation,
		Impact:         stringArg(args, "impact"),
		Recommendation: stringArg(args, "recommendation"),
	})
	if err != nil {
		return errResp(fmt.Sprintf("Error writing learning: %v", err))
	}

	text := fmt.Sprintf("Recorded learning: %s\nPath: %s", title, entry.Path)

	if s.autoCompact {
		if n := rivetctx.CountActive(s.learningsDir); n >= promoteLearningsThreshold {
			text += fmt.Sprintf(promoteMessage, n, promoteLearningsThreshold, s.learningsDir)
		}
	}

	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  ToolCallResult{Content: []ContentItem{{Type: "text", Text: text}}},
	}
}

func stringArg(args map[string]interface{}, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func stringSliceArg(args map[string]interface{}, key string) []string {
	v, ok := args[key]
	if !ok {
		return nil
	}
	switch vv := v.(type) {
	case []string:
		return vv
	case []interface{}:
		out := make([]string, 0, len(vv))
		for _, it := range vv {
			if s, ok := it.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		if vv == "" {
			return nil
		}
		return []string{vv}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// buildArgsFromParams extracts CLI arguments from MCP tool call arguments.
// If the capability has typed params, named arguments are mapped to CLI flags.
// Otherwise, the generic "args" array is used.
func buildArgsFromParams(cap *capabilities.Capability, arguments map[string]interface{}) ([]string, bool) {
	approved := false
	if v, ok := arguments["approve"]; ok {
		if b, ok := v.(bool); ok {
			approved = b
		}
	}

	// If capability has typed params, map them to CLI flags.
	if cap != nil && len(cap.Params) > 0 {
		var args []string
		for _, p := range cap.Params {
			val, ok := arguments[p.Name]
			if !ok {
				continue
			}

			flag := p.FlagName()

			switch v := val.(type) {
			case bool:
				if v {
					args = append(args, flag)
				}
			default:
				args = append(args, flag, fmt.Sprintf("%v", v))
			}
		}
		return args, approved
	}

	// Fallback: generic args array.
	var args []string
	if rawArgs, ok := arguments["args"]; ok {
		if arr, ok := rawArgs.([]interface{}); ok {
			for _, a := range arr {
				if str, ok := a.(string); ok {
					args = append(args, str)
				}
			}
		}
	}
	return args, approved
}

func toolDescription(cap *capabilities.Capability) string {
	desc := cap.Description
	if desc == "" {
		desc = cap.Name
	}
	return fmt.Sprintf("[%s] %s", cap.Safety, desc)
}

// buildCapabilitySchema generates an MCP inputSchema for a capability.
// If the capability has typed params, each becomes a named property.
// Otherwise, falls back to a generic args array.
func buildCapabilitySchema(cap *capabilities.Capability) inputSchema {
	schema := inputSchema{
		Type:       "object",
		Properties: make(map[string]interface{}),
	}

	if len(cap.Params) > 0 {
		// Typed params — generate proper schema.
		for _, p := range cap.Params {
			prop := map[string]interface{}{
				"type":        p.Type,
				"description": p.Description,
			}
			if len(p.Enum) > 0 {
				prop["enum"] = p.Enum
			}
			if p.Default != "" {
				prop["default"] = p.Default
			}
			schema.Properties[p.Name] = prop
			if p.Required {
				schema.Required = append(schema.Required, p.Name)
			}
		}
	} else {
		// No typed params — generic args array.
		argsDesc := "Extra arguments to pass to the capability"
		if cap.ArgsHint != "" {
			argsDesc = cap.ArgsHint
		}
		schema.Properties["args"] = map[string]interface{}{
			"type":        "array",
			"items":       map[string]string{"type": "string"},
			"description": argsDesc,
		}
	}

	if cap.Safety == capabilities.SafetyLevelDangerous {
		schema.Properties["approve"] = map[string]interface{}{
			"type":        "boolean",
			"description": "Explicitly approve execution of this dangerous capability",
		}
	}

	return schema
}
