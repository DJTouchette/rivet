package mcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// MCP is bidirectional: while handling a tool call, the server can send the
// client a request of its own — elicitation/create, to ask the person a
// question — and wait for the answer before replying. This file is that
// half of the transport: version negotiation, what the client said it can do,
// and the server-to-client round trip over the same stdio stream.

// supportedProtocolVersions are the MCP revisions this server speaks, newest
// first. Elicitation exists from 2025-06-18 on.
var supportedProtocolVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", protocolVersion}

// elicitationVersions are the negotiated versions in which a client may
// advertise and answer elicitation/create.
var elicitationVersions = map[string]bool{"2025-06-18": true, "2025-11-25": true}

// negotiateVersion returns the client's requested version when this server
// supports it, else the newest version it does — the MCP lifecycle rule; the
// client then decides whether it can proceed. A request with no version at
// all (pre-negotiation clients, tests) keeps the baseline.
func negotiateVersion(requested string) string {
	if requested == "" {
		return protocolVersion
	}
	for _, v := range supportedProtocolVersions {
		if v == requested {
			return v
		}
	}
	return supportedProtocolVersions[0]
}

type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
	Capabilities    struct {
		Elicitation *json.RawMessage `json:"elicitation,omitempty"`
	} `json:"capabilities"`
	ClientInfo struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"clientInfo"`
}

// clientState is what the client told us in initialize.
type clientState struct {
	version     string
	elicitation bool
	name        string
}

// conn is the live stdio stream during Serve. It lets a handler send a request
// to the client and read until the matching response arrives. Client requests
// that arrive in the meantime are queued and handled after the current one.
type conn struct {
	scanner *bufio.Scanner
	out     io.Writer
	mu      sync.Mutex // serialises writes
	queue   [][]byte   // client messages read while waiting on a response
	nextID  int
}

func (c *conn) write(v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.out.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

// next returns the next client message: a queued one first, else a fresh
// line from the stream. ok is false at end of input.
func (c *conn) next() ([]byte, bool) {
	if len(c.queue) > 0 {
		msg := c.queue[0]
		c.queue = c.queue[1:]
		return msg, true
	}
	for c.scanner.Scan() {
		line := c.scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		return append([]byte(nil), line...), true
	}
	return nil, false
}

// clientResponse is a JSON-RPC response from the client to a server request.
type clientResponse struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Error  *RPCError       `json:"error"`
}

// errNoClient means there is no live stream to ask over (HandleMessage, tests).
var errNoClient = errors.New("no interactive MCP client connection")

// request sends method to the client and blocks until the client answers.
// Pings that arrive meanwhile are answered inline so a client health check
// can't deadlock; every other client message is queued for the main loop.
func (c *conn) request(method string, params interface{}) (json.RawMessage, error) {
	c.nextID++
	id := fmt.Sprintf("rivet-%d", c.nextID)
	if err := c.write(map[string]interface{}{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	wantID, _ := json.Marshal(id)

	var deferred [][]byte
	defer func() { c.queue = append(deferred, c.queue...) }()
	for {
		msg, ok := c.next()
		if !ok {
			return nil, fmt.Errorf("client closed the connection before answering %s", method)
		}
		var r clientResponse
		if err := json.Unmarshal(msg, &r); err != nil {
			deferred = append(deferred, msg)
			continue
		}
		if r.Method == "" && string(r.ID) == string(wantID) {
			if r.Error != nil {
				return nil, fmt.Errorf("client refused %s: %s", method, r.Error.Message)
			}
			return r.Result, nil
		}
		if r.Method == "ping" && r.ID != nil {
			_ = c.write(Response{JSONRPC: "2.0", ID: r.ID, Result: map[string]interface{}{}})
			continue
		}
		deferred = append(deferred, msg)
	}
}

// Elicitation result actions (MCP 2025-06-18).
const (
	ElicitAccept  = "accept"
	ElicitDecline = "decline"
	ElicitCancel  = "cancel"
)

// ElicitResult is the person's answer to an elicitation.
type ElicitResult struct {
	Action  string                 `json:"action"`
	Content map[string]interface{} `json:"content,omitempty"`
}

// Elicitor asks the person a question through the client and returns their
// answer. The answer comes from the client's UI, never from the model.
type Elicitor func(message string, requestedSchema map[string]interface{}) (*ElicitResult, error)

// elicitor returns how to ask the person, or nil when this client can't be
// asked: it didn't advertise elicitation, negotiated a version without it, or
// there is no live connection. A test may install its own.
func (s *Server) elicitor() Elicitor {
	if s.elicit != nil {
		return s.elicit
	}
	if s.conn == nil || !s.client.elicitation || !elicitationVersions[s.client.version] {
		return nil
	}
	return func(message string, schema map[string]interface{}) (*ElicitResult, error) {
		raw, err := s.conn.request("elicitation/create", map[string]interface{}{
			"message":         message,
			"requestedSchema": schema,
		})
		if err != nil {
			return nil, err
		}
		var res ElicitResult
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, fmt.Errorf("malformed elicitation result: %w", err)
		}
		return &res, nil
	}
}

// SetElicitor installs a fixed elicitor, for tests driving HandleMessage
// without a live client.
func (s *Server) SetElicitor(e Elicitor) { s.elicit = e }
