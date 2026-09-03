package daemon

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wow-look-at-my/remote-agent/protocol"
)

func (h *Handler) handleGlob(params map[string]any) *protocol.DaemonResponse {
	pattern, _ := params["pattern"].(string)
	path, _ := params["path"].(string)
	limit := intParam(params, "limit")
	if pattern == "" {
		return errResponse(fmt.Errorf("missing 'pattern' parameter"))
	}
	if path == "" {
		path = "."
	}

	cmd := fmt.Sprintf("%s serve glob --pattern %s --path %s",
		h.daemon.helper(), shellEscape(pattern), shellEscape(path))
	if limit > 0 {
		cmd += fmt.Sprintf(" --limit %d", limit)
	}

	var result protocol.GlobResult
	if resp := h.runRemoteJSON("glob", cmd, &result); resp != nil {
		return resp
	}
	return okResponse(result)
}

func (h *Handler) handleGrep(params map[string]any) *protocol.DaemonResponse {
	pattern, _ := params["pattern"].(string)
	path, _ := params["path"].(string)
	include, _ := params["include"].(string)
	mode, _ := params["mode"].(string)
	insensitive, _ := params["ignore_case"].(bool)
	context := intParam(params, "context")
	limit := intParam(params, "limit")
	if pattern == "" {
		return errResponse(fmt.Errorf("missing 'pattern' parameter"))
	}
	if path == "" {
		path = "."
	}

	cmd := fmt.Sprintf("%s serve grep --pattern %s --path %s",
		h.daemon.helper(), shellEscape(pattern), shellEscape(path))
	if include != "" {
		cmd += " --include " + shellEscape(include)
	}
	if mode != "" {
		cmd += " --mode " + shellEscape(mode)
	}
	if insensitive {
		cmd += " --ignore-case"
	}
	if context > 0 {
		cmd += fmt.Sprintf(" --context %d", context)
	}
	if limit > 0 {
		cmd += fmt.Sprintf(" --limit %d", limit)
	}

	var result protocol.GrepResult
	if resp := h.runRemoteJSON("grep", cmd, &result); resp != nil {
		return resp
	}
	return okResponse(result)
}

// runRemoteJSON runs a remote helper command and decodes its JSON output into
// out.
func (h *Handler) runRemoteJSON(action, cmd string, out any) *protocol.DaemonResponse {
	stdout, stderr, exitCode, err := h.daemon.runner.Run(cmd)
	if err != nil {
		return errResponse(fmt.Errorf("%s failed: %w", action, err))
	}
	if exitCode != 0 {
		return errResponse(fmt.Errorf("%s: %s", action, strings.TrimSpace(string(stderr))))
	}
	var raw map[string]any
	if err := json.Unmarshal(stdout, &raw); err != nil {
		return errResponse(fmt.Errorf("parse %s result: %w", action, err))
	}
	if e, ok := raw["error"]; ok {
		return errResponse(fmt.Errorf("%v", e))
	}
	if err := json.Unmarshal(stdout, out); err != nil {
		return errResponse(fmt.Errorf("parse %s result: %w", action, err))
	}
	return nil
}

// intParam reads a numeric parameter, accepting the float64 that JSON
// decoding produces as well as a plain int from an in-process caller.
func intParam(params map[string]any, key string) int {
	switch v := params[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}
