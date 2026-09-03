// Package mcpserver exposes a remote host's shell and filesystem to an MCP
// client as a set of Model Context Protocol tools. A model's own file tools
// read the machine it runs on.
package mcpserver

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/remote-agent/protocol"
)

// The MCP revision this server implements. A revision it can speak is echoed
// back instead.
const protocolVersion = "2024-11-05"

// supportedVersions are the protocol revisions we echo back verbatim.
var supportedVersions = set.Of(
	"2024-10-07",
	"2024-11-05",
	"2025-03-26",
	"2025-06-18",
)

type Backend interface {
	Call(route protocol.Route, action string, params map[string]any, out any) error
}

// Server serves the remote filesystem toolset over an MCP stdio connection.
type Server struct {
	backend Backend
	version string
	// For calls that omit a target. Empty makes the target argument mandatory.
	defaultTarget string
	// For calls that name no control master. Empty leaves the choice to
	// ssh_config.
	defaultControlPath string
	tools              []tool
	out                *json.Encoder
}

// New returns a server over b.
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

// Serve answers JSON-RPC messages strictly in arrival order. Concurrency
// would reorder a write and the edit that follows it, and Claude Code never
// pipelines MCP calls anyway.
func (s *Server) Serve(in io.Reader, out io.Writer) error {
	s.out = json.NewEncoder(out)
	dec := json.NewDecoder(in)

	for {
		var req request
		if err := dec.Decode(&req); err != nil {
			if err == io.EOF {
				return nil
			}
			// A malformed message has no id to answer and leaves the stream
			// unparseable.
			return fmt.Errorf("decode request: %w", err)
		}
		if req.ID == nil {
			// Notification: no reply expected.
			continue
		}
		s.reply(s.handle(&req))
	}
}

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
	if supportedVersions.Contains(params.ProtocolVersion) {
		version = params.ProtocolVersion
	}
	return result(req, map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": "remote-agent", "version": s.version},
		"instructions":    s.instructions(),
	})
}

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
		// A tool failure rides the result, so the model sees it. A JSON-RPC error
		// hides it.
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
