#!/bin/sh
# Auto-generated shim used by `remote-agent claude`.
#
# Claude Code invokes its shell prefix as a single-token program followed by
# the full command line for one spawn as one argument, i.e.:
#
#     /path/to/remote-agent-claude-shim.sh '<full command line>'
#
# Since v2.1.185 Claude wraps THREE kinds of spawns this way: Bash tool
# commands, hook commands, and MCP stdio servers. The hidden `claude-shim`
# subcommand tells them apart: Bash tool commands are laundered and forwarded
# to the remote host through the remote-agent daemon, while hooks and MCP
# servers run locally, where the env vars and files Claude prepared for them
# exist (see client/claudeshim.go). The target daemon socket is selected by
# remote-agent itself via the REMOTE_AGENT_SOCKET / REMOTE_AGENT_TARGET
# environment variables, which the launcher exports into Claude's environment.
#
# Using a single-token shim (rather than a multi-word prefix like
# "remote-agent claude-shim") is deliberate: Claude's prefix wrapper
# shell-quotes the program name as one argument, so a multi-word prefix would
# be treated as a single executable name and fail.
exec "${REMOTE_AGENT_BIN:?REMOTE_AGENT_BIN is not set; launch via 'remote-agent claude'}" claude-shim "$@"
