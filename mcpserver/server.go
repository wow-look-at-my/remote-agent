// Package mcpserver exposes the remote host's filesystem to Claude Code (or
// any other MCP client) as a set of Model Context Protocol tools.
//
// Claude Code's built-in Read/Write/Edit/Glob/Grep tools call Node's fs
// directly against the machine Claude runs on; unlike the Bash tool they have
// no shell-prefix hook, so there is no way to redirect them at the remote
// host. `remote-agent claude` therefore turns the built-ins off and registers
// this server in their place, so every file operation the model performs
// lands on the remote machine that its shell commands already run on.
//
// The transport is the MCP stdio transport: newline-delimited JSON-RPC 2.0 on
// stdin/stdout. The server holds no state of its own -- each tool call becomes
// one request to the local remote-agent daemon, which owns the SSH connection.
package mcpserver

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/wow-look-at-my/remote-agent/protocol"
)

// protocolVersion is the MCP revision this server implements. A client that
// asks for a different revision gets its own version echoed back when we can
// speak it (the wire format has been compatible across these revisions);
// anything unknown falls back to this one.
const protocolVersion = "2024-11-05"

// supportedVersions are the protocol revisions we echo back verbatim.
var supportedVersions = map[string]bool{
	"2024-10-07": true,
	"2024-11-05": true,
	"2025-03-26": true,
	"2025-06-18": true,
}

// Backend performs one daemon action against the host a route names, decoding
// the result into out, and starts a daemon for that route when none is running.
// It is the seam that keeps the tool layer testable without a daemon or an SSH
// host.
type Backend interface {
	Call(route protocol.Route, action string, params map[string]any, out any) error
}

// Server serves the remote filesystem toolset over an MCP stdio connection.
type Server struct {
	backend Backend
	version string
	// defaultTarget is the SSH target used by calls that omit one. Empty makes
	// the target argument mandatory on every tool.
	defaultTarget string
	// defaultControlPath is the control-master socket used by calls that name
	// none. Empty leaves the choice to ssh_config, per host.
	defaultControlPath string
	tools              []tool
	out                *json.Encoder
}

// New returns a server exposing the remote toolset backed by b. version is
// reported to the client as the server version. defaultTarget, when non-empty,
// is the SSH target tool calls act on unless they name another; when it is
// empty every tool call must carry its own target. defaultControlPath is the
// control master calls ride unless they name their own.
func New(b Backend, version, defaultTarget, defaultControlPath string) *Server {
	s := &Server{
		backend:            b,
		version:            version,
		defaultTarget:      defaultTarget,
		defaultControlPath: defaultControlPath,
	}
	s.tools = s.buildTools()
	return s
}

// Serve reads JSON-RPC messages from in until EOF, writing replies to out.
//
// Requests are handled strictly in arrival order. Handling them concurrently
// would shave latency off batched reads, but it also reorders writes: a client
// that sends write_file and then edit_file for the same path without waiting
// would see the edit run against the pre-write contents. Claude Code never
// pipelines MCP calls (MCP tools inherit isConcurrencySafe=false, so its tool
// executor runs them one at a time), so serializing costs nothing there and
// keeps every other client's dependent operations ordered.
func (s *Server) Serve(in io.Reader, out io.Writer) error {
	s.out = json.NewEncoder(out)
	dec := json.NewDecoder(in)

	for {
		var req request
		if err := dec.Decode(&req); err != nil {
			if err == io.EOF {
				return nil
			}
			// A malformed message cannot be answered (its id is unknown) and
			// leaves the stream unparseable, so the session ends here.
			return fmt.Errorf("decode request: %w", err)
		}
		if req.ID == nil {
			// Notification: no reply expected.
			continue
		}
		s.reply(s.handle(&req))
	}
}

// handle dispatches one request to its method implementation.
func (s *Server) handle(req *request) *response {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "ping":
		return result(req, map[string]any{})
	case "tools/list":
		return result(req, map[string]any{"tools": s.tools})
	case "tools/call":
		return s.handleToolCall(req)
	case "resources/list":
		return result(req, map[string]any{"resources": []any{}})
	case "prompts/list":
		return result(req, map[string]any{"prompts": []any{}})
	default:
		return failure(req, methodNotFound, "unknown method: "+req.Method)
	}
}

func (s *Server) handleInitialize(req *request) *response {
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(req.Params, &params)

	version := protocolVersion
	if supportedVersions[params.ProtocolVersion] {
		version = params.ProtocolVersion
	}
	return result(req, map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": "remote-agent", "version": s.version},
		"instructions":    s.instructions(),
	})
}

// instructions tell the client what these tools act on, and where the SSH
// target comes from -- the one thing a caller cannot guess.
func (s *Server) instructions() string {
	base := "Tools for a remote host reached over SSH by remote-agent: run_command runs a shell " +
		"command there, and the rest read and write its files. " +
		"Every path is a path on that remote machine, and every command runs on it. " +
		"Use these for all file access and all command execution -- nothing here touches the local machine " +
		"except upload_file and download_file, which say so. "
	if s.defaultTarget != "" {
		return base + "Tools act on " + s.defaultTarget + " unless a call passes a different `target` (user@host). " +
			"An SSH connection is opened on demand; nothing has to be started first."
	}
	return base + "Every call must pass `target` (user@host, or a Host alias from ~/.ssh/config) naming the machine to act on. " +
		"An SSH connection to it is opened on demand; nothing has to be started first."
}

func (s *Server) handleToolCall(req *request) *response {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return failure(req, invalidParams, "invalid tool call parameters: "+err.Error())
	}
	t := s.lookup(params.Name)
	if t == nil {
		return failure(req, invalidParams, "unknown tool: "+params.Name)
	}

	content, err := t.handler(params.Arguments)
	if err != nil {
		// Tool failures are reported in the result, not as a protocol error:
		// the model is meant to see them and adapt (wrong path, ambiguous
		// edit), which a JSON-RPC error would hide behind a client-level
		// failure.
		return result(req, callResult{
			Content: []contentBlock{{Type: "text", Text: "Error: " + err.Error()}},
			IsError: true,
		})
	}
	return result(req, callResult{Content: content})
}

func (s *Server) lookup(name string) *tool {
	for i := range s.tools {
		if s.tools[i].Name == name {
			return &s.tools[i]
		}
	}
	return nil
}

// reply writes one response.
func (s *Server) reply(resp *response) {
	if resp == nil {
		return
	}
	if err := s.out.Encode(resp); err != nil {
		// stdout is gone (client exited); nothing left to report it to.
		return
	}
}

// JSON-RPC error codes used by this server.
const (
	invalidParams  = -32602
	methodNotFound = -32601
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// callResult is the MCP tools/call payload.
type callResult struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// contentBlock is one piece of a tool result: text, or an inline image for
// screenshots and other images read off the remote host.
type contentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

func result(req *request, payload any) *response {
	return &response{JSONRPC: "2.0", ID: req.ID, Result: payload}
}

func failure(req *request, code int, message string) *response {
	return &response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: code, Message: message}}
}
