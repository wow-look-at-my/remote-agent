package mcpserver

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/remote-agent/protocol"
)

// fakeBackend records daemon calls and replays canned results.
type fakeBackend struct {
	mu      sync.Mutex
	calls   []backendCall
	results map[string]any // action -> payload marshalled into out
	err     error
}

type backendCall struct {
	Route  protocol.Route
	Action string
	Params map[string]any
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{results: map[string]any{}}
}

// testTarget is the default target the test servers are built with, standing in
// for the one `remote-agent mcp user@host` is given.
const testTarget = "user@testhost"

func (f *fakeBackend) Call(route protocol.Route, action string, params map[string]any, out any) error {
	f.mu.Lock()
	f.calls = append(f.calls, backendCall{Route: route, Action: action, Params: params})
	f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	payload, ok := f.results[action]
	if !ok || out == nil {
		return nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func (f *fakeBackend) lastCall(t *testing.T) backendCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotEmpty(t, f.calls)
	return f.calls[len(f.calls)-1]
}

// exchange runs a batch of JSON-RPC request lines through a server and returns
// the decoded responses.
func exchange(t *testing.T, backend Backend, requests ...string) []map[string]any {
	t.Helper()
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out strings.Builder
	require.NoError(t, New(backend, "test", testTarget, "").Serve(in, &out))

	var responses []map[string]any
	dec := json.NewDecoder(strings.NewReader(out.String()))
	for dec.More() {
		var resp map[string]any
		require.NoError(t, dec.Decode(&resp))
		responses = append(responses, resp)
	}
	return responses
}

func TestInitializeHandshake(t *testing.T) {
	responses := exchange(t, newFakeBackend(),
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)

	require.Len(t, responses, 1)
	result := responses[0]["result"].(map[string]any)
	// A supported version is echoed back rather than downgraded.
	assert.Equal(t, "2025-06-18", result["protocolVersion"])
	assert.Contains(t, result["capabilities"].(map[string]any), "tools")
	assert.Equal(t, "remote-agent", result["serverInfo"].(map[string]any)["name"])
	assert.Contains(t, result["instructions"], "remote")
}

func TestInitializeUnknownVersionFallsBack(t *testing.T) {
	responses := exchange(t, newFakeBackend(),
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`)

	result := responses[0]["result"].(map[string]any)
	assert.Equal(t, protocolVersion, result["protocolVersion"])
}

func TestNotificationsGetNoReply(t *testing.T) {
	responses := exchange(t, newFakeBackend(),
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`)

	require.Len(t, responses, 1)
	assert.Equal(t, float64(2), responses[0]["id"])
}

func TestToolsList(t *testing.T) {
	responses := exchange(t, newFakeBackend(), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)

	tools := responses[0]["result"].(map[string]any)["tools"].([]any)
	names := map[string]bool{}
	for _, entry := range tools {
		tool := entry.(map[string]any)
		names[tool["name"].(string)] = true
		assert.NotEmpty(t, tool["description"], "%s needs a description", tool["name"])
		schema := tool["inputSchema"].(map[string]any)
		assert.Equal(t, "object", schema["type"])
	}
	for _, want := range []string{"run_command", "read_file", "write_file", "edit_file", "list_dir", "glob", "grep", "upload_file", "download_file"} {
		assert.True(t, names[want], "missing tool %s", want)
	}
}

func TestToolsListAndCallReachTheBackend(t *testing.T) {
	backend := newFakeBackend()
	backend.results["read"] = map[string]any{"content": "hello\nworld\n"}

	responses := exchange(t, backend,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read_file","arguments":{"path":"/srv/app.txt"}}}`)

	result := responses[0]["result"].(map[string]any)
	assert.NotEqual(t, true, result["isError"])
	content := result["content"].([]any)[0].(map[string]any)
	assert.Equal(t, "text", content["type"])
	assert.Contains(t, content["text"], "hello")

	call := backend.lastCall(t)
	assert.Equal(t, "read", call.Action)
	assert.Equal(t, "/srv/app.txt", call.Params["path"])
}

func TestToolCallFailureIsReportedInResult(t *testing.T) {
	backend := newFakeBackend()
	backend.err = fmt.Errorf("read /nope: no such file")

	responses := exchange(t, backend,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read_file","arguments":{"path":"/nope"}}}`)

	result := responses[0]["result"].(map[string]any)
	// Tool errors travel in the result so the model sees and can act on them.
	assert.Equal(t, true, result["isError"])
	assert.Nil(t, responses[0]["error"])
	assert.Contains(t, result["content"].([]any)[0].(map[string]any)["text"], "no such file")
}

func TestUnknownToolAndMethodAreProtocolErrors(t *testing.T) {
	responses := exchange(t, newFakeBackend(),
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nope","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"does/not/exist"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":"not-an-object"}`)

	require.Len(t, responses, 3)
	byID := map[float64]map[string]any{}
	for _, resp := range responses {
		byID[resp["id"].(float64)] = resp
	}
	assert.Equal(t, float64(invalidParams), byID[1]["error"].(map[string]any)["code"])
	assert.Equal(t, float64(methodNotFound), byID[2]["error"].(map[string]any)["code"])
	assert.Equal(t, float64(invalidParams), byID[3]["error"].(map[string]any)["code"])
}

func TestEmptyListEndpoints(t *testing.T) {
	responses := exchange(t, newFakeBackend(),
		`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"prompts/list"}`)

	require.Len(t, responses, 2)
	for _, resp := range responses {
		result := resp["result"].(map[string]any)
		assert.Len(t, result[firstKey(result)], 0)
	}
}

func TestMalformedStreamEndsSession(t *testing.T) {
	err := New(newFakeBackend(), "test", testTarget, "").Serve(strings.NewReader("{not json"), &strings.Builder{})
	assert.Error(t, err)
}

func firstKey(m map[string]any) string {
	for k := range m {
		return k
	}
	return ""
}
