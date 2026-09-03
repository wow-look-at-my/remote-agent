package mcpserver

import (
	"fmt"

	"github.com/wow-look-at-my/remote-agent/protocol"
)

const (
	// What read_file returns when the caller asks for no window.
	defaultReadLines = 2000
	// maxReadLine caps the length of a single returned line.
	maxReadLine = 2000
	// The largest inline image.
	maxImageBytes = 5 << 20
	// Per output stream. A build log runs to megabytes, which is context spent
	// for nothing.
	maxOutputBytes = 64 << 10
)

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`

	handler func(args map[string]any) ([]contentBlock, error) `json:"-"`
}

// buildTools declares the toolset.
func (s *Server) buildTools() []tool {
	return []tool{
		s.tool(tool{
			Name: "run_command",
			Description: "Run a shell command on the REMOTE host and return its output. " +
				"This is the only way to execute anything in this session -- there is no local shell tool. " +
				"Each call is a separate one-shot shell, so a `cd` does not carry to the next call: pass cwd instead. " +
				"A non-zero exit is reported as an error, with whatever the command printed.",
			InputSchema: schema(
				props{
					"command": prop("string", "Shell command line, run by the remote user's shell."),
					"cwd":     prop("string", "Absolute directory on the remote host to run it in (default the remote home directory)."),
					"timeout": prop("number", fmt.Sprintf(
						"Seconds to wait before giving up on the command (default %d, maximum %d). "+
							"The call returns an error at the deadline; the command may still be running on the remote host.",
						int(protocol.ExecDefaultTimeout.Seconds()), int(protocol.ExecMaxTimeout.Seconds()))),
				},
				"command",
			),
			handler: s.runCommand,
		}),
		s.tool(tool{
			Name: "read_file",
			Description: "Read a file on the REMOTE host (the same machine Bash commands run on). " +
				"Returns the contents with line numbers, so text can be quoted back to edit_file exactly. " +
				"Images are returned visually. Use this instead of a local file-reading tool -- there is none in this session.",
			InputSchema: schema(
				props{
					"path":   prop("string", "Absolute path on the remote host."),
					"offset": prop("integer", "1-based line number to start from (for reading a window of a large file)."),
					"limit":  prop("integer", fmt.Sprintf("Maximum lines to return (default %d).", defaultReadLines)),
				},
				"path",
			),
			handler: s.readFile,
		}),
		s.tool(tool{
			Name: "write_file",
			Description: "Write a file on the REMOTE host, creating it or overwriting it entirely. " +
				"Prefer edit_file for changing part of an existing file.",
			InputSchema: schema(
				props{
					"path":    prop("string", "Absolute path on the remote host."),
					"content": prop("string", "Full contents to write."),
					"mode":    prop("string", "Octal file mode (default 0644)."),
				},
				"path", "content",
			),
			handler: s.writeFile,
		}),
		s.tool(tool{
			Name: "edit_file",
			Description: "Replace exact text in a file on the REMOTE host. " +
				"old_string must appear exactly once unless replace_all is true; include surrounding lines to make it unique. " +
				"Read the file first so the text matches byte for byte.",
			InputSchema: schema(
				props{
					"path":        prop("string", "Absolute path on the remote host."),
					"old_string":  prop("string", "Exact text to replace (must be unique unless replace_all)."),
					"new_string":  prop("string", "Replacement text."),
					"replace_all": prop("boolean", "Replace every occurrence instead of requiring a unique match."),
				},
				"path", "old_string", "new_string",
			),
			handler: s.editFile,
		}),
		s.tool(tool{
			Name:        "list_dir",
			Description: "List a directory on the REMOTE host.",
			InputSchema: schema(
				props{
					"path":      prop("string", "Directory on the remote host (default the remote home directory)."),
					"recursive": prop("boolean", "Walk the whole tree instead of one level."),
				},
			),
			handler: s.listDir,
		}),
		s.tool(tool{
			Name: "glob",
			Description: "Find files on the REMOTE host by glob pattern, most recently modified first. " +
				"Supports '**' for any number of directories and brace alternatives, e.g. '**/*.{go,mod}'. " +
				"Use this to locate files by name; use grep to search their contents.",
			InputSchema: schema(
				props{
					"pattern": prop("string", "Glob pattern, matched against paths relative to path."),
					"path":    prop("string", "Directory to search (default the remote working directory)."),
					"limit":   prop("integer", "Maximum paths to return."),
				},
				"pattern",
			),
			handler: s.glob,
		}),
		s.tool(tool{
			Name: "grep",
			Description: "Search file contents on the REMOTE host with a regular expression (RE2 syntax). " +
				"Binary files, .git and node_modules are skipped.",
			InputSchema: schema(
				props{
					"pattern":          prop("string", "Regular expression to search for."),
					"path":             prop("string", "File or directory to search (default the remote working directory)."),
					"include":          prop("string", "Glob limiting which files are searched, e.g. '**/*.go'."),
					"output_mode":      enumProp("string", "content returns matching lines (default), files_with_matches returns paths, count returns per-file counts.", protocol.GrepModeContent, protocol.GrepModeFiles, protocol.GrepModeCount),
					"case_insensitive": prop("boolean", "Ignore case."),
					"context_lines":    prop("integer", "Lines of context around each match (content mode)."),
					"limit":            prop("integer", "Maximum results to return."),
				},
				"pattern",
			),
			handler: s.grep,
		}),
		s.tool(tool{
			Name: "upload_file",
			Description: "Copy a file from the LOCAL machine (where Claude Code runs) to the REMOTE host. " +
				"The only way to move a local file across; every other tool here works remote-side only.",
			InputSchema: schema(
				props{
					"local_path":  prop("string", "Path on the local machine."),
					"remote_path": prop("string", "Destination path on the remote host."),
				},
				"local_path", "remote_path",
			),
			handler: s.uploadFile,
		}),
		s.tool(tool{
			Name:        "download_file",
			Description: "Copy a file from the REMOTE host to the LOCAL machine (where Claude Code runs).",
			InputSchema: schema(
				props{
					"remote_path": prop("string", "Path on the remote host."),
					"local_path":  prop("string", "Destination path on the local machine."),
				},
				"remote_path", "local_path",
			),
			handler: s.downloadFile,
		}),
	}
}

// tool adds the routing arguments every tool takes: which host to act on, and
// optionally the control master to reach it through. target is required
// exactly when the server has no default, so a call can never be routed to a
// host nobody named.
func (s *Server) tool(t tool) tool {
	desc := "SSH target to act on, e.g. user@host, user@host:2222 for a non-standard SSH port, " +
		"or a Host alias from ~/.ssh/config. Give the port whenever the host listens on one: " +
		"each port is a separate machine to act on, so root@127.0.0.1:2201 and root@127.0.0.1:2202 " +
		"never share a connection. The connection is opened on demand."
	if s.defaultTarget != "" {
		desc += " Defaults to " + s.defaultTarget + "."
	}
	t.InputSchema = addProp(t.InputSchema, "target", prop("string", desc))
	if s.defaultTarget == "" {
		t.InputSchema = addRequired(t.InputSchema, "target")
	}

	controlDesc := "Path to an OpenSSH control-master socket (ControlPath) to run through, " +
		"e.g. /tmp/cm-user@host:22. Pass this when you have been given a control socket for the host: " +
		"the connection is borrowed from that master, so no separate authentication happens -- " +
		"which is the only way in when the host needs a one-time password or a hardware key. " +
		"Naming one makes it mandatory: the call fails rather than opening its own connection. " +
		"Omit it when the host needs no master or ~/.ssh/config already sets a ControlPath for it."
	if s.defaultControlPath != "" {
		controlDesc += " Defaults to " + s.defaultControlPath + "."
	}
	t.InputSchema = addProp(t.InputSchema, "control_path", prop("string", controlDesc))
	return t
}

// route resolves where a call goes: which host, and how to reach it. Both
// come from the call itself, falling back to the server's defaults.
func (s *Server) route(args map[string]any) (protocol.Route, error) {
	target := stringArg(args, "target")
	if target == "" {
		target = s.defaultTarget
	}
	if target == "" {
		return protocol.Route{}, fmt.Errorf("missing required argument: target (the SSH target to act on, e.g. user@host or user@host:2222)")
	}
	controlPath := stringArg(args, "control_path")
	if controlPath == "" {
		controlPath = s.defaultControlPath
	}
	return protocol.Route{Target: target, ControlPath: controlPath}, nil
}

// Schema helpers keep the tool declarations readable.

type props map[string]any

func schema(p props, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": map[string]any(p)}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func addProp(s map[string]any, name string, p map[string]any) map[string]any {
	properties, ok := s["properties"].(map[string]any)
	if !ok {
		properties = map[string]any{}
		s["properties"] = properties
	}
	properties[name] = p
	return s
}

// addRequired marks an existing property required.
func addRequired(s map[string]any, name string) map[string]any {
	required, _ := s["required"].([]string)
	s["required"] = append(required, name)
	return s
}

func prop(typ, description string) map[string]any {
	return map[string]any{"type": typ, "description": description}
}

func enumProp(typ, description string, values ...string) map[string]any {
	p := prop(typ, description)
	p["enum"] = values
	return p
}
