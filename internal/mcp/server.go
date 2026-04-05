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
const learnNudgeMessage = "\n\n---\n[rivet] You have made multiple recon calls without calling rivet.learn. " +
	"You MUST record non-obvious findings — hidden dependencies, performance traps, " +
	"implicit ordering, gotchas — so future sessions don't rediscover them. " +
	"Call rivet.learn now with the relevant context doc name and a concise finding."

// consolidateLearningsThreshold is the number of learnings in a single doc
// before the server tells Claude to consolidate.
const consolidateLearningsThreshold = 8

// consolidateLinesThreshold is the total line count of a doc before
// the server tells Claude to consolidate.
const consolidateLinesThreshold = 60

// consolidateMessage is appended to the rivet.learn response when
// a doc exceeds size thresholds.
const consolidateMessage = `

---
[rivet] The "%s" context doc is getting long (%d learnings, %d lines). You MUST consolidate it now:

1. Read the full doc with rivet.context-show
2. Promote the most important learnings into the Gotchas section as permanent entries
3. Remove learnings that are now covered by Gotchas or are duplicates
4. Remove any learnings that are obvious from the code or no longer accurate
5. Keep the doc under 50 lines of content (excluding frontmatter)
6. Write the consolidated doc back to .rivet/context/domains/%s.md`

// reconInvestigationTools are the recon tools that indicate active investigation
// (not just a refresh or overview).
var reconInvestigationTools = map[string]bool{
	"recon.grep":    true,
	"recon.search":  true,
	"recon.related": true,
	"recon.context": true,
	"recon.symbols": true,
}

type Server struct {
	registry    *capabilities.Registry
	executor    *capabilities.Executor
	contexts    []*rivetctx.Document
	policies    []policy.Rule
	version     string
	logger      *log.Logger
	autoCompact bool // whether to nudge consolidation when docs get long

	// Session state for nudging.
	reconCallsSinceLearn int
	contextShown         bool // true after rivet.context-show is called
}

// NewServer creates an MCP server backed by the given registry, executor,
// and context documents.
func NewServer(reg *capabilities.Registry, exec *capabilities.Executor, contexts []*rivetctx.Document, policies []policy.Rule, version string, autoCompact bool) *Server {
	return &Server{
		registry:    reg,
		executor:    exec,
		contexts:    contexts,
		policies:    policies,
		version:     version,
		autoCompact: autoCompact,
		logger:      log.New(io.Discard, "", 0),
	}
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
			Name:        "rivet.learn",
			Description: "[guarded] Record a non-obvious finding to a context document. Call this after discovering something that would help future investigations — hidden dependencies, performance traps, implicit ordering, gotchas.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"doc": map[string]interface{}{
						"type":        "string",
						"description": "Name of the context document to append to (e.g. 'billing', 'accounts')",
					},
					"learning": map[string]interface{}{
						"type":        "string",
						"description": "A concise, single-line finding. Example: 'ServiceRenderedInsertTrigger fires 2 queries (CheckForThirdParties) + 3-table join (AddTax) on every insert'",
					},
				},
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
	case "rivet.learn":
		s.reconCallsSinceLearn = 0
		doc, _ := params.Arguments["doc"].(string)
		learning, _ := params.Arguments["learning"].(string)
		return s.handleLearn(req, doc, learning)
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

func (s *Server) handleResourcesList(req *Request) *Response {
	resources := make([]Resource, 0, len(s.contexts))
	for _, doc := range s.contexts {
		resources = append(resources, Resource{
			URI:         doc.URI(),
			Name:        doc.Title,
			Description: fmt.Sprintf("%s context: %s", doc.Kind, doc.Name),
			MimeType:    "text/markdown",
		})
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

	for _, doc := range s.contexts {
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

	recs := rivetctx.Recommend(s.contexts, query, 5)

	if len(recs) == 0 {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: ToolCallResult{Content: []ContentItem{{Type: "text", Text: fmt.Sprintf("No context documents match %q", query)}}},
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

func (s *Server) handleLearn(req *Request, docName, learning string) *Response {
	if docName == "" {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: ToolCallResult{
				Content: []ContentItem{{Type: "text", Text: "Error: 'doc' argument is required"}},
				IsError: true,
			},
		}
	}
	if learning == "" {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: ToolCallResult{
				Content: []ContentItem{{Type: "text", Text: "Error: 'learning' argument is required"}},
				IsError: true,
			},
		}
	}

	// Find the context document.
	var doc *rivetctx.Document
	for _, d := range s.contexts {
		if d.Name == docName {
			doc = d
			break
		}
	}
	if doc == nil {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: ToolCallResult{
				Content: []ContentItem{{Type: "text", Text: fmt.Sprintf("Error: context document %q not found. Use rivet.context-list to see available documents.", docName)}},
				IsError: true,
			},
		}
	}

	// Append to the file.
	if err := rivetctx.AppendLearning(doc.Path, learning); err != nil {
		return &Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: ToolCallResult{
				Content: []ContentItem{{Type: "text", Text: fmt.Sprintf("Error writing learning: %v", err)}},
				IsError: true,
			},
		}
	}

	text := fmt.Sprintf("Recorded learning in %s: %s", docName, learning)

	// Check if doc needs consolidation (only nudge if auto_compact is enabled).
	if s.autoCompact {
		stats := rivetctx.GetDocStats(doc.Path)
		if stats.Learnings >= consolidateLearningsThreshold || stats.Lines >= consolidateLinesThreshold {
			text += fmt.Sprintf(consolidateMessage, docName, stats.Learnings, stats.Lines, docName)
		}
	}

	return &Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  ToolCallResult{Content: []ContentItem{{Type: "text", Text: text}}},
	}
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
