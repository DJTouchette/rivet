package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
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
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	InputSchema inputSchema `json:"inputSchema"`
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
[rivet] The learning log has %d active entries (threshold: %d). Review them and promote the high-value ones into context docs. Run /rivet-promote-learnings, or use rivet.context-learnings-list to inspect.`

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
	pins         *pins.Registry
	policies     []policy.Rule
	version      string
	logger       *log.Logger
	autoCompact  bool              // whether to nudge promotion when the log gets long
	learningsDir string            // where rivet.learn writes entries
	semantic     rivetctx.Semantic // optional embedding-based recommend signal; nil = lexical-only

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
		logger:       log.New(io.Discard, "", 0),
	}
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

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
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

		// Notifications (no id field) never get a response.
		if req.ID == nil {
			continue
		}

		resp := s.handleRequest(&req)
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
	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: initializeResult{
			ProtocolVersion: protocolVersion,
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
			Description: "[safe] Show a context document by name",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Name of the context document to show (e.g. 'billing', 'sql-views')",
					},
				},
			},
		},
		Tool{
			Name:        "rivet.context-recommend",
			Description: "[safe] Recommend context documents for a task, file path, or keywords",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "A task description, file path, or keywords (e.g. 'investigate billing retries', 'backend/Handlers/PaymentGateway/src/App.cs')",
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

	// Registered capabilities.
	for _, cap := range caps {
		tool := Tool{
			Name:        cap.Name,
			Description: toolDescription(&cap),
			InputSchema: buildCapabilitySchema(&cap),
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
		return s.handleContextShow(req, name)
	case "rivet.context-recommend":
		query, _ := params.Arguments["query"].(string)
		return s.handleContextRecommend(req, query)
	case "rivet.runbook":
		query, _ := params.Arguments["query"].(string)
		return s.handleRunbook(req, query)
	case "rivet.runbook-draft":
		return s.handleRunbookDraft(req, params.Arguments)
	case "rivet.learn":
		s.reconCallsSinceLearn = 0
		return s.handleLearn(req, params.Arguments)
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

	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  ToolCallResult{Content: []ContentItem{{Type: "text", Text: text}}},
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
	if len(s.contexts) == 0 {
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

	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  ToolCallResult{Content: []ContentItem{{Type: "text", Text: b.String()}}},
	}
}

func (s *Server) handleContextShow(req *Request, name string) *Response {
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

	for _, doc := range s.contexts {
		if doc.Name == name {
			return &Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  ToolCallResult{Content: []ContentItem{{Type: "text", Text: doc.Body}}},
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

func (s *Server) handleContextRecommend(req *Request, query string) *Response {
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
	recs := rivetctx.Recommend(pool, query, 5, opts...)

	if len(recs) == 0 {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  ToolCallResult{Content: []ContentItem{{Type: "text", Text: fmt.Sprintf("No context documents match %q", query)}}},
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Recommended context for %q:\n\n", query))
	for _, r := range recs {
		sb.WriteString(fmt.Sprintf("  %.2f  [%s] %s — %s\n", r.Score, r.Kind, r.Name, r.Title))
		sb.WriteString(fmt.Sprintf("        signals: %s\n", strings.Join(r.Signals, ", ")))
		sb.WriteString(fmt.Sprintf("        uri: %s\n\n", r.URI))
	}

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
	return text(sb.String())
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
			text += fmt.Sprintf(promoteMessage, n, promoteLearningsThreshold)
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
