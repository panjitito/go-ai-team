// Package mcp exposes Go AI Team's own features to the agents running inside
// it, over the Model Context Protocol.
//
// This is what turns the app from something you drive into something an agent
// can drive: read and update the backlog, write to the shared project memory,
// reuse a saved prompt, run a saved dev command, message another agent, ask for
// a secret to be injected, query a database read-only.
//
// Transport is stdio, because that is what the agent CLIs already speak: the
// CLI spawns `go-ai-team mcp --project <id>` and talks JSON-RPC over the pipe.
// No port, no auth token, no network — the process boundary is the trust
// boundary, and the project id it was launched with scopes everything it can
// reach.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

// Protocol version we implement.
const protocolVersion = "2024-11-05"

// Tool is one callable capability.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`

	// Handler runs the tool. Returning an error becomes an isError result
	// rather than a protocol fault, because a tool that cannot do its job is
	// normal and the agent should read why and adapt.
	Handler func(ctx context.Context, args json.RawMessage) (string, error) `json:"-"`
}

// Server speaks JSON-RPC 2.0 over a pipe.
type Server struct {
	name    string
	version string

	mu    sync.RWMutex
	tools map[string]*Tool
	order []string

	out *json.Encoder
	// writeMu serialises responses; notifications may be emitted concurrently.
	writeMu sync.Mutex
}

// NewServer builds a Server.
func NewServer(name, version string) *Server {
	return &Server{name: name, version: version, tools: map[string]*Tool{}}
}

// Register adds a tool.
func (s *Server) Register(t Tool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tools[t.Name]; !exists {
		s.order = append(s.order, t.Name)
	}
	tt := t
	s.tools[t.Name] = &tt
}

// ---------- wire types ----------

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// content is one block in a tool result.
type content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolResult struct {
	Content []content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

// Serve runs the read-dispatch-write loop until the pipe closes.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	s.out = json.NewEncoder(out)
	// Each message is one line of JSON. A large tool result can be long, so the
	// scanner's buffer is raised well past the default.
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			s.send(response{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
			continue
		}
		// A request with no id is a notification: act, answer nothing.
		if len(req.ID) == 0 {
			continue
		}
		s.dispatch(ctx, req)
	}
	return sc.Err()
}

func (s *Server) send(r response) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.out == nil {
		return
	}
	_ = s.out.Encode(r)
}

func (s *Server) dispatch(ctx context.Context, req request) {
	switch req.Method {
	case "initialize":
		s.send(response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": s.name, "version": s.version},
		}})

	case "tools/list":
		s.mu.RLock()
		list := make([]map[string]any, 0, len(s.order))
		for _, name := range s.order {
			t := s.tools[name]
			list = append(list, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"inputSchema": t.InputSchema,
			})
		}
		s.mu.RUnlock()
		s.send(response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": list}})

	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			s.send(response{JSONRPC: "2.0", ID: req.ID,
				Error: &rpcError{Code: -32602, Message: "invalid params"}})
			return
		}
		s.mu.RLock()
		t := s.tools[p.Name]
		s.mu.RUnlock()
		if t == nil {
			s.send(response{JSONRPC: "2.0", ID: req.ID,
				Error: &rpcError{Code: -32601, Message: "no such tool: " + p.Name}})
			return
		}
		text, err := t.Handler(ctx, p.Arguments)
		if err != nil {
			// A tool failure is data, not a protocol error: the agent should be
			// able to read the reason and try something else.
			s.send(response{JSONRPC: "2.0", ID: req.ID, Result: toolResult{
				Content: []content{{Type: "text", Text: err.Error()}},
				IsError: true,
			}})
			return
		}
		s.send(response{JSONRPC: "2.0", ID: req.ID, Result: toolResult{
			Content: []content{{Type: "text", Text: text}},
		}})

	case "ping":
		s.send(response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}})

	default:
		s.send(response{JSONRPC: "2.0", ID: req.ID,
			Error: &rpcError{Code: -32601, Message: "method not found: " + req.Method}})
	}
}

// ---------- helpers for building tools ----------

// Schema builds an input schema from a property map and required list.
func Schema(props map[string]any, required ...string) json.RawMessage {
	if props == nil {
		props = map[string]any{}
	}
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	b, _ := json.Marshal(m)
	return b
}

// Str describes a string property.
func Str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

// Int describes an integer property.
func Int(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }

// Bool describes a boolean property.
func Bool(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }

// Enum describes a string property limited to a set of values.
func Enum(desc string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": values}
}

// Decode unmarshals tool arguments, tolerating an absent object.
func Decode(raw json.RawMessage, v any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("could not read the arguments: %w", err)
	}
	return nil
}

// JSONText renders a value as indented JSON for a tool result.
func JSONText(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
